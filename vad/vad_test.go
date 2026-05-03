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
