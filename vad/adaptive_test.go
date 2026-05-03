package vad

import (
	"math"
	"testing"
)

func TestFrameRMS(t *testing.T) {
	tests := []struct {
		name  string
		pcm   []float32
		want  float64
		delta float64
	}{
		{"silence", make([]float32, 512), -300, 10},
		{"sine half amplitude", genSine(440, 512, 0.5), -6.02, 0.1},
		{"sine full amplitude", genSine(440, 512, 1.0), 0, 0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := frameRMS(tt.pcm)
			if math.Abs(got-tt.want) > tt.delta {
				t.Errorf("frameRMS() = %.2f, want %.2f ± %.2f", got, tt.want, tt.delta)
			}
		})
	}
}

func TestMapParams(t *testing.T) {
	a := &AdaptiveDetector{
		inner: &Detector{minSilenceMs: 500},
		cfg: AdaptiveConfig{
			AdaptThresholdMin: 0.5,
			AdaptThresholdMax: 0.85,
			AdaptMinSpeechMin: 250,
			AdaptMinSpeechMax: 600,
		},
	}

	tests := []struct {
		name          string
		baseline      float64
		wantThreshold float32
		wantMinSpeech int
	}{
		{"very quiet", -55, 0.5, 250},
		{"quiet boundary low", -50, 0.5, 250},
		{"mid quiet", -45, 0.6, 325},
		{"mid noisy", -37, 0.76, 460},
		{"noisy boundary high", -35, 0.8, 500},
		{"very noisy", -30, 0.85, 600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th, ms, _ := a.mapParams(tt.baseline)
			if th != tt.wantThreshold {
				t.Errorf("threshold = %.2f, want %.2f", th, tt.wantThreshold)
			}
			if ms != tt.wantMinSpeech {
				t.Errorf("minSpeech = %d, want %d", ms, tt.wantMinSpeech)
			}
		})
	}
}

func TestFilterSegments(t *testing.T) {
	pcm := make([]float32, 16000)
	for i := 1000; i < 3000; i++ {
		pcm[i] = 0.8
	}
	for i := 5000; i < 7000; i++ {
		pcm[i] = 0.01
	}

	segments := []Segment{
		{Start: 0.0625, End: 0.1875},
		{Start: 0.3125, End: 0.4375},
	}

	filtered := FilterSegments(pcm, segments, 16000, -30)
	if len(filtered) != 1 {
		t.Fatalf("got %d segments, want 1", len(filtered))
	}
	if filtered[0].Start != segments[0].Start {
		t.Errorf("kept wrong segment: got start %.4f, want %.4f", filtered[0].Start, segments[0].Start)
	}
}

func TestFilterSegmentsAllPass(t *testing.T) {
	pcm := make([]float32, 16000)
	for i := 0; i < 16000; i++ {
		pcm[i] = 0.5
	}
	segments := []Segment{
		{Start: 0.0, End: 0.5},
		{Start: 0.5, End: 1.0},
	}
	filtered := FilterSegments(pcm, segments, 16000, -40)
	if len(filtered) != 2 {
		t.Errorf("got %d segments, want 2", len(filtered))
	}
}

func TestFilterSegmentsAllDiscard(t *testing.T) {
	pcm := make([]float32, 16000)
	segments := []Segment{
		{Start: 0.0, End: 0.5},
	}
	filtered := FilterSegments(pcm, segments, 16000, -10)
	if len(filtered) != 0 {
		t.Errorf("got %d segments, want 0", len(filtered))
	}
}

func TestComputeBaseline(t *testing.T) {
	a := &AdaptiveDetector{
		inner: &Detector{minSilenceMs: 500},
		cfg: AdaptiveConfig{
			Percentile: 0.85,
		},
		frameDB:  make([]float64, 0, 100),
		capacity: 100,
	}

	for i := 0; i < 80; i++ {
		a.addFrame(-50)
	}
	for i := 0; i < 20; i++ {
		a.addFrame(-20)
	}

	baseline := a.computeBaseline()
	if baseline < -25 {
		t.Errorf("baseline = %.1f, want near -20 (P85 should capture the loud frames)", baseline)
	}
}

func genSine(freq float64, n int, amplitude float32) []float32 {
	pcm := make([]float32, n)
	for i := 0; i < n; i++ {
		pcm[i] = amplitude * float32(math.Sin(2*math.Pi*freq*float64(i)/16000))
	}
	return pcm
}
