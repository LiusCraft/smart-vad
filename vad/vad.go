package vad

import (
	"fmt"

	"github.com/streamer45/silero-vad-go/speech"
)

type Config struct {
	ModelPath            string
	SampleRate           int
	Threshold            float32
	MinSilenceDurationMs int
	MinSpeechDurationMs  int
	SpeechPadMs          int
}

func (c Config) validate() error {
	if c.ModelPath == "" {
		return fmt.Errorf("ModelPath is required")
	}
	if c.SampleRate != 8000 && c.SampleRate != 16000 {
		return fmt.Errorf("SampleRate must be 8000 or 16000")
	}
	if c.Threshold <= 0 || c.Threshold >= 1 {
		return fmt.Errorf("Threshold must be in (0, 1)")
	}
	if c.MinSilenceDurationMs < 0 {
		return fmt.Errorf("MinSilenceDurationMs must be >= 0")
	}
	if c.MinSpeechDurationMs < 0 {
		return fmt.Errorf("MinSpeechDurationMs must be >= 0")
	}
	if c.SpeechPadMs < 0 {
		return fmt.Errorf("SpeechPadMs must be >= 0")
	}
	return nil
}

func (c Config) windowSize() int {
	if c.SampleRate == 8000 {
		return 256
	}
	return 512
}

type Segment struct {
	Start float64
	End   float64
}

type Result struct {
	Segments []Segment
	Probs    []float32
}

type Detector struct {
	inner *speech.Detector
	cfg   Config

	// Runtime-adjustable params (initialized from cfg in NewDetector)
	threshold    float32
	minSilenceMs int
	minSpeechMs  int

	triggered  bool
	tempEnd    int
	currSample int
	segments   []Segment
	probs      []float32
}

func NewDetector(cfg Config) (*Detector, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if cfg.Threshold == 0 {
		cfg.Threshold = 0.5
	}

	speechCfg := speech.DetectorConfig{
		ModelPath:            cfg.ModelPath,
		SampleRate:           cfg.SampleRate,
		Threshold:            cfg.Threshold,
		MinSilenceDurationMs: cfg.MinSilenceDurationMs,
		SpeechPadMs:          cfg.SpeechPadMs,
	}

	inner, err := speech.NewDetector(speechCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech detector: %w", err)
	}

	return &Detector{
		inner:        inner,
		cfg:          cfg,
		threshold:    cfg.Threshold,
		minSilenceMs: cfg.MinSilenceDurationMs,
		minSpeechMs:  cfg.MinSpeechDurationMs,
	}, nil
}

func (d *Detector) Reset() {
	d.triggered = false
	d.tempEnd = 0
	d.currSample = 0
	d.segments = nil
	d.probs = nil
	d.inner.Reset()
}

func (d *Detector) Process(chunk []float32) error {
	ws := d.cfg.windowSize()
	if len(chunk) < ws {
		return fmt.Errorf("chunk too short: need at least %d samples", ws)
	}

	minSilenceSamples := d.minSilenceMs * d.cfg.SampleRate / 1000
	speechPadSamples := d.cfg.SpeechPadMs * d.cfg.SampleRate / 1000

	for i := 0; i <= len(chunk)-ws; i += ws {
		speechProb, err := d.inner.Infer(chunk[i : i+ws])
		if err != nil {
			return fmt.Errorf("infer failed: %w", err)
		}

		d.probs = append(d.probs, speechProb)
		d.currSample += ws

		if speechProb >= d.threshold && d.tempEnd != 0 {
			d.tempEnd = 0
		}

		if speechProb >= d.threshold && !d.triggered {
			d.triggered = true
			start := float64(d.currSample-ws-speechPadSamples) / float64(d.cfg.SampleRate)
			if start < 0 {
				start = 0
			}
			d.segments = append(d.segments, Segment{Start: start})
		}

		if speechProb < (d.threshold-0.15) && d.triggered {
			if d.tempEnd == 0 {
				d.tempEnd = d.currSample
			}
			if d.currSample-d.tempEnd < minSilenceSamples {
				continue
			}
			end := float64(d.tempEnd+speechPadSamples) / float64(d.cfg.SampleRate)
			d.tempEnd = 0
			d.triggered = false
			if len(d.segments) > 0 {
				d.segments[len(d.segments)-1].End = end
			}
		}
	}

	return nil
}

func (d *Detector) Flush() Result {
	if d.triggered && len(d.segments) > 0 {
		end := float64(d.currSample) / float64(d.cfg.SampleRate)
		d.segments[len(d.segments)-1].End = end
	}

	segments := d.segments
	if d.minSpeechMs > 0 {
		minDur := float64(d.minSpeechMs) / 1000
		filtered := segments[:0]
		for _, s := range segments {
			if s.End-s.Start >= minDur {
				filtered = append(filtered, s)
			}
		}
		segments = filtered
	}

	probs := d.probs
	return Result{Segments: segments, Probs: probs}
}

func (d *Detector) Detect(pcm []float32) (Result, error) {
	ws := d.cfg.windowSize()
	if len(pcm) < ws {
		return Result{}, fmt.Errorf("audio too short: need at least %d samples", ws)
	}

	d.Reset()

	if err := d.Process(pcm); err != nil {
		return Result{}, err
	}

	return d.Flush(), nil
}

func (d *Detector) Destroy() error {
	return d.inner.Destroy()
}

func (d *Detector) SetThreshold(t float32) {
	if t > 0 && t < 1 {
		d.threshold = t
	}
}

func (d *Detector) SetMinSilenceDurationMs(ms int) {
	if ms >= 0 {
		d.minSilenceMs = ms
	}
}

func (d *Detector) SetMinSpeechDurationMs(ms int) {
	if ms >= 0 {
		d.minSpeechMs = ms
	}
}
