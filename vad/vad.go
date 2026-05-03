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

	return &Detector{inner: inner, cfg: cfg}, nil
}

func (d *Detector) Detect(pcm []float32) (Result, error) {
	ws := d.cfg.windowSize()
	if len(pcm) < ws {
		return Result{}, fmt.Errorf("audio too short: need at least %d samples", ws)
	}

	if err := d.inner.Reset(); err != nil {
		return Result{}, fmt.Errorf("reset failed: %w", err)
	}

	minSilenceSamples := d.cfg.MinSilenceDurationMs * d.cfg.SampleRate / 1000
	speechPadSamples := d.cfg.SpeechPadMs * d.cfg.SampleRate / 1000

	var segments []Segment
	var probs []float32

	currSample := 0
	triggered := false
	tempEnd := 0

	for i := 0; i <= len(pcm)-ws; i += ws {
		speechProb, err := d.inner.Infer(pcm[i : i+ws])
		if err != nil {
			return Result{}, fmt.Errorf("infer failed at sample %d: %w", i, err)
		}

		probs = append(probs, speechProb)
		currSample += ws

		if speechProb >= d.cfg.Threshold && tempEnd != 0 {
			tempEnd = 0
		}

		if speechProb >= d.cfg.Threshold && !triggered {
			triggered = true
			start := float64(currSample-ws-speechPadSamples) / float64(d.cfg.SampleRate)
			if start < 0 {
				start = 0
			}
			segments = append(segments, Segment{Start: start})
		}

		if speechProb < (d.cfg.Threshold-0.15) && triggered {
			if tempEnd == 0 {
				tempEnd = currSample
			}
			if currSample-tempEnd < minSilenceSamples {
				continue
			}
			end := float64(tempEnd+speechPadSamples) / float64(d.cfg.SampleRate)
			tempEnd = 0
			triggered = false
			if len(segments) > 0 {
				segments[len(segments)-1].End = end
			}
		}
	}

	if triggered && len(segments) > 0 {
		end := float64(len(pcm)) / float64(d.cfg.SampleRate)
		segments[len(segments)-1].End = end
	}

	if d.cfg.MinSpeechDurationMs > 0 {
		minDur := float64(d.cfg.MinSpeechDurationMs) / 1000
		filtered := segments[:0]
		for _, s := range segments {
			if s.End-s.Start >= minDur {
				filtered = append(filtered, s)
			}
		}
		segments = filtered
	}

	return Result{Segments: segments, Probs: probs}, nil
}

func (d *Detector) Destroy() error {
	return d.inner.Destroy()
}
