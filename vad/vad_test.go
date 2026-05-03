package vad

import (
	"math"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"empty model path", Config{}, true},
		{"invalid sample rate", Config{ModelPath: "test.onnx", SampleRate: 22050}, true},
		{"threshold out of range", Config{ModelPath: "test.onnx", SampleRate: 16000, Threshold: 1.5}, true},
		{"valid config", Config{ModelPath: "test.onnx", SampleRate: 16000, Threshold: 0.5}, false},
		{"valid 8k", Config{ModelPath: "test.onnx", SampleRate: 8000, Threshold: 0.5}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestWindowSize(t *testing.T) {
	got := Config{SampleRate: 16000}.windowSize()
	if got != 512 {
		t.Errorf("windowSize() = %d, want 512 for 16kHz", got)
	}
	got = Config{SampleRate: 8000}.windowSize()
	if got != 256 {
		t.Errorf("windowSize() = %d, want 256 for 8kHz", got)
	}
}

func TestDetectShortAudio(t *testing.T) {
	d := &Detector{cfg: Config{SampleRate: 16000}}
	_, err := d.Detect(make([]float32, 100))
	if err == nil {
		t.Error("expected error for short audio")
	}
}

func generateSineWave(freq float64, sampleRate int, duration float64) []float32 {
	n := int(float64(sampleRate) * duration)
	pcm := make([]float32, n)
	for i := 0; i < n; i++ {
		pcm[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / float64(sampleRate)))
	}
	return pcm
}

func generateSilence(sampleRate int, duration float64) []float32 {
	n := int(float64(sampleRate) * duration)
	return make([]float32, n)
}

func TestRuntimeSetters(t *testing.T) {
	d := &Detector{
		cfg:          Config{Threshold: 0.5, MinSilenceDurationMs: 100, MinSpeechDurationMs: 100},
		threshold:    0.5,
		minSilenceMs: 100,
		minSpeechMs:  100,
	}

	d.SetThreshold(0.8)
	if d.threshold != 0.8 {
		t.Errorf("threshold = %f, want 0.8", d.threshold)
	}

	d.SetMinSilenceDurationMs(500)
	if d.minSilenceMs != 500 {
		t.Errorf("minSilenceMs = %d, want 500", d.minSilenceMs)
	}

	d.SetMinSpeechDurationMs(600)
	if d.minSpeechMs != 600 {
		t.Errorf("minSpeechMs = %d, want 600", d.minSpeechMs)
	}
}

func TestRuntimeSettersRejectInvalid(t *testing.T) {
	d := &Detector{
		cfg:          Config{Threshold: 0.5, MinSilenceDurationMs: 100, MinSpeechDurationMs: 100},
		threshold:    0.5,
		minSilenceMs: 100,
		minSpeechMs:  100,
	}

	d.SetThreshold(0) // invalid (must be > 0)
	if d.threshold != 0.5 {
		t.Errorf("threshold should remain 0.5, got %f", d.threshold)
	}

	d.SetThreshold(1) // invalid (must be < 1)
	if d.threshold != 0.5 {
		t.Errorf("threshold should remain 0.5, got %f", d.threshold)
	}

	d.SetMinSilenceDurationMs(-1) // invalid
	if d.minSilenceMs != 100 {
		t.Errorf("minSilenceMs should remain 100, got %d", d.minSilenceMs)
	}

	d.SetMinSpeechDurationMs(-1) // invalid
	if d.minSpeechMs != 100 {
		t.Errorf("minSpeechMs should remain 100, got %d", d.minSpeechMs)
	}
}
