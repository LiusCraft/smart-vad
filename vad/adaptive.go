package vad

import (
	"fmt"
	"math"
	"sort"

	"github.com/liushunshun/smart-vad/logger"
)

func RMS(pcm []float32) float64 {
	var sum float64
	for _, s := range pcm {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(pcm)))
	return 20 * math.Log10(rms+1e-9)
}

func FilterSegments(pcm []float32, segments []Segment, sampleRate int, minDB float64) []Segment {
	filtered := make([]Segment, 0, len(segments))
	for _, seg := range segments {
		startSample := int(math.Round(seg.Start * float64(sampleRate)))
		endSample := int(math.Round(seg.End * float64(sampleRate)))
		if startSample < 0 {
			startSample = 0
		}
		if endSample > len(pcm) {
			endSample = len(pcm)
		}
		if startSample >= endSample {
			continue
		}
		db := RMS(pcm[startSample:endSample])
		if db >= minDB {
			filtered = append(filtered, seg)
		} else {
			logger.Debug("RMS filter: segment removed",
				"start", seg.Start,
				"end", seg.End,
				"rms_db", db,
				"min_db", minDB)
		}
	}
	return filtered
}

func frameRMS(frame []float32) float64 {
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(frame)))
	return 20 * math.Log10(rms+1e-15)
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

type AdaptiveConfig struct {
	DetectorConfig Config

	WindowDuration       float64
	NoiseFloorFrac       float64 // fraction of quietest frames to average for noise baseline, default 0.1
	EnergyOffsetDB       float64
	AdaptThresholdMin    float32
	AdaptThresholdMax    float32
	AdaptMinSpeechMin    int
	AdaptMinSpeechMax    int
	DisableRMSPostFilter bool
}

func (c *AdaptiveConfig) setDefaults() {
	if c.WindowDuration == 0 {
		c.WindowDuration = 30
	}
	if c.NoiseFloorFrac == 0 {
		c.NoiseFloorFrac = 0.1
	}
	if c.EnergyOffsetDB == 0 {
		c.EnergyOffsetDB = 6
	}
	if c.AdaptThresholdMin == 0 {
		c.AdaptThresholdMin = 0.5
	}
	if c.AdaptThresholdMax == 0 {
		c.AdaptThresholdMax = 0.85
	}
	if c.AdaptMinSpeechMin == 0 {
		c.AdaptMinSpeechMin = 250
	}
	if c.AdaptMinSpeechMax == 0 {
		c.AdaptMinSpeechMax = 600
	}
}

type AdaptiveDetector struct {
	inner *Detector
	cfg   AdaptiveConfig

	frameDB    []float64
	capacity   int
	frameSize  int
	sampleRate int

	rawSegments  []Segment // pre-filter segments, for inspection
	keptSegments []Segment // post-filter segments retained

	baselineDB     float64
	energyOffsetDB float64
}

func NewAdaptiveDetector(cfg AdaptiveConfig) (*AdaptiveDetector, error) {
	cfg.setDefaults()

	inner, err := NewDetector(cfg.DetectorConfig)
	if err != nil {
		return nil, err
	}

	ws := 512
	if cfg.DetectorConfig.SampleRate == 8000 {
		ws = 256
	}

	framesPerSec := float64(cfg.DetectorConfig.SampleRate) / float64(ws)
	capacity := int(cfg.WindowDuration * framesPerSec)

	logger.Debug("adaptive detector created",
		"sample_rate", cfg.DetectorConfig.SampleRate,
		"window_duration_sec", cfg.WindowDuration,
		"frame_capacity", capacity,
		"frame_size", ws,
		"noise_floor_frac", cfg.NoiseFloorFrac,
		"energy_offset_db", cfg.EnergyOffsetDB,
		"threshold_range", fmt.Sprintf("[%.2f, %.2f]", cfg.AdaptThresholdMin, cfg.AdaptThresholdMax),
		"min_speech_range", fmt.Sprintf("[%d, %d]", cfg.AdaptMinSpeechMin, cfg.AdaptMinSpeechMax),
		"disable_rms_filter", cfg.DisableRMSPostFilter)

	return &AdaptiveDetector{
		inner:      inner,
		cfg:        cfg,
		frameDB:    make([]float64, 0, capacity),
		capacity:   capacity,
		frameSize:  ws,
		sampleRate: cfg.DetectorConfig.SampleRate,
	}, nil
}

func (a *AdaptiveDetector) addFrame(rmsDB float64) {
	a.frameDB = append(a.frameDB, rmsDB)
	if len(a.frameDB) > a.capacity {
		a.frameDB = a.frameDB[len(a.frameDB)-a.capacity:]
	}
}

func (a *AdaptiveDetector) resetBaseline() {
	a.frameDB = a.frameDB[:0]
}

func (a *AdaptiveDetector) computeBaseline() float64 {
	n := len(a.frameDB)
	if n == 0 {
		return -60
	}
	sorted := make([]float64, n)
	copy(sorted, a.frameDB)
	sort.Float64s(sorted)

	// Noise floor = average of the quietest NoiseFloorFrac fraction of frames.
	// This captures background noise level, not speech level.
	count := int(math.Ceil(float64(n) * a.cfg.NoiseFloorFrac))
	if count < 1 {
		count = 1
	}
	var sum float64
	for i := 0; i < count; i++ {
		sum += sorted[i]
	}
	return sum / float64(count)
}

func (a *AdaptiveDetector) mapParams(baselineDB float64) (threshold float32, minSpeechMs int, minSilenceMs int) {
	switch {
	case baselineDB <= -50:
		return a.cfg.AdaptThresholdMin, a.cfg.AdaptMinSpeechMin, a.inner.minSilenceMs
	case baselineDB <= -40:
		t := (baselineDB + 50) / 10
		th := lerp(float64(a.cfg.AdaptThresholdMin), 0.7, t)
		ms := lerp(float64(a.cfg.AdaptMinSpeechMin), 400, t)
		return float32(th), int(math.Round(ms)), a.inner.minSilenceMs
	case baselineDB <= -35:
		t := (baselineDB + 40) / 5
		th := lerp(0.7, 0.8, t)
		ms := lerp(400, 500, t)
		return float32(th), int(math.Round(ms)), a.inner.minSilenceMs
	default:
		return a.cfg.AdaptThresholdMax, a.cfg.AdaptMinSpeechMax, a.inner.minSilenceMs
	}
}

func (a *AdaptiveDetector) Detect(pcm []float32) (Result, error) {
	a.resetBaseline()

	ws := a.frameSize
	for i := 0; i <= len(pcm)-ws; i += ws {
		a.addFrame(frameRMS(pcm[i : i+ws]))
	}

	baseline := a.computeBaseline()
	a.baselineDB = baseline
	a.energyOffsetDB = a.cfg.EnergyOffsetDB
	threshold, minSpeechMs, minSilenceMs := a.mapParams(baseline)

	logger.Debug("adaptive detect",
		"baseline_db", baseline,
		"mapped_threshold", threshold,
		"mapped_min_speech_ms", minSpeechMs,
		"frames_collected", len(a.frameDB))

	a.inner.SetThreshold(threshold)
	a.inner.SetMinSpeechDurationMs(minSpeechMs)
	a.inner.SetMinSilenceDurationMs(minSilenceMs)

	result, err := a.inner.Detect(pcm)
	if err != nil {
		return Result{}, err
	}

	if !a.cfg.DisableRMSPostFilter {
		a.rawSegments = make([]Segment, len(result.Segments))
		copy(a.rawSegments, result.Segments)

		minDB := baseline + a.cfg.EnergyOffsetDB
		before := len(result.Segments)
		result.Segments = FilterSegments(pcm, result.Segments, a.sampleRate, minDB)
		removed := before - len(result.Segments)
		if removed > 0 {
			logger.Debug("RMS post-filter removed segments",
				"removed", removed,
				"remaining", len(result.Segments),
				"min_db", minDB)
		}

		a.keptSegments = make([]Segment, len(result.Segments))
		copy(a.keptSegments, result.Segments)
	}

	return result, nil
}

// FilteredSegments returns segments that were discarded by the RMS energy post-filter.
func (a *AdaptiveDetector) FilteredSegments() []Segment {
	kept := make(map[Segment]bool, len(a.keptSegments))
	for _, s := range a.keptSegments {
		kept[s] = true
	}
	var filtered []Segment
	for _, s := range a.rawSegments {
		if !kept[s] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (a *AdaptiveDetector) Reset() {
	a.inner.Reset()
	a.resetBaseline()
}

func (a *AdaptiveDetector) Process(chunk []float32) error {
	ws := a.frameSize
	for i := 0; i <= len(chunk)-ws; i += ws {
		a.addFrame(frameRMS(chunk[i : i+ws]))
	}

	baseline := a.computeBaseline()
	if math.Abs(baseline-a.baselineDB) >= 3 {
		a.baselineDB = baseline
		threshold, minSpeechMs, minSilenceMs := a.mapParams(baseline)
		logger.Debug("adaptive params updated",
			"baseline_db", baseline,
			"threshold", threshold,
			"min_speech_ms", minSpeechMs)
		a.inner.SetThreshold(threshold)
		a.inner.SetMinSpeechDurationMs(minSpeechMs)
		a.inner.SetMinSilenceDurationMs(minSilenceMs)
	}

	return a.inner.Process(chunk)
}

func (a *AdaptiveDetector) Flush() Result {
	return a.inner.Flush()
}

func (a *AdaptiveDetector) BaselineDB() float64 {
	return a.baselineDB
}

func (a *AdaptiveDetector) EnergyOffsetDB() float64 {
	return a.energyOffsetDB
}

func (a *AdaptiveDetector) Destroy() error {
	return a.inner.Destroy()
}

func (a *AdaptiveDetector) Inner() *Detector       { return a.inner }
func (a *AdaptiveDetector) GetProbs() []float32    { return a.inner.GetProbs() }
func (a *AdaptiveDetector) GetSegments() []Segment { return a.inner.GetSegments() }
func (a *AdaptiveDetector) IsTriggered() bool      { return a.inner.IsTriggered() }
