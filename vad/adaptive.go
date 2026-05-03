package vad

import (
	"math"
	"sort"
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
	return 20*math.Log10(rms+1e-15) + 3.01
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

type AdaptiveConfig struct {
	DetectorConfig Config

	WindowDuration float64
	Percentile     float64
	EnergyOffsetDB float64

	AdaptThresholdMin float32
	AdaptThresholdMax float32
	AdaptMinSpeechMin int
	AdaptMinSpeechMax int
}

func (c *AdaptiveConfig) setDefaults() {
	if c.WindowDuration == 0 {
		c.WindowDuration = 30
	}
	if c.Percentile == 0 {
		c.Percentile = 0.85
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
	idx := int(float64(n-1) * a.cfg.Percentile)
	return sorted[idx]
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
	threshold, minSpeechMs, minSilenceMs := a.mapParams(baseline)

	a.inner.SetThreshold(threshold)
	a.inner.SetMinSpeechDurationMs(minSpeechMs)
	a.inner.SetMinSilenceDurationMs(minSilenceMs)

	result, err := a.inner.Detect(pcm)
	if err != nil {
		return Result{}, err
	}

	minDB := baseline + a.cfg.EnergyOffsetDB
	result.Segments = FilterSegments(pcm, result.Segments, a.sampleRate, minDB)

	return result, nil
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
	threshold, minSpeechMs, minSilenceMs := a.mapParams(baseline)
	a.inner.SetThreshold(threshold)
	a.inner.SetMinSpeechDurationMs(minSpeechMs)
	a.inner.SetMinSilenceDurationMs(minSilenceMs)

	return a.inner.Process(chunk)
}

func (a *AdaptiveDetector) Flush() Result {
	return a.inner.Flush()
}

func (a *AdaptiveDetector) Destroy() error {
	return a.inner.Destroy()
}
