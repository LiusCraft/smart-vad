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
		{"sine half amplitude", genSine(440, 512, 0.5), -9.03, 0.1},
		{"sine full amplitude", genSine(440, 512, 1.0), -3.01, 0.1},
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
			NoiseFloorFrac: 0.1,
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
	if baseline != -50 {
		t.Errorf("baseline = %.1f, want -50 (bottom 10%% avg = 10 frames at -50)", baseline)
	}
}

func TestAdaptiveConfigValidation(t *testing.T) {
	// Defaults should be set
	cfg := AdaptiveConfig{
		DetectorConfig: Config{ModelPath: "test.onnx", SampleRate: 16000, Threshold: 0.5},
	}
	cfg.setDefaults()
	if cfg.WindowDuration != 30 {
		t.Errorf("WindowDuration = %.0f, want 30", cfg.WindowDuration)
	}
	if cfg.NoiseFloorFrac != 0.1 {
		t.Errorf("NoiseFloorFrac = %.2f, want 0.1", cfg.NoiseFloorFrac)
	}
	if cfg.EnergyOffsetDB != 6 {
		t.Errorf("EnergyOffsetDB = %.0f, want 6", cfg.EnergyOffsetDB)
	}
	if cfg.AdaptThresholdMin != 0.5 {
		t.Errorf("AdaptThresholdMin = %.1f, want 0.5", cfg.AdaptThresholdMin)
	}
	if cfg.AdaptThresholdMax != 0.85 {
		t.Errorf("AdaptThresholdMax = %.2f, want 0.85", cfg.AdaptThresholdMax)
	}
	if cfg.AdaptMinSpeechMin != 250 {
		t.Errorf("AdaptMinSpeechMin = %d, want 250", cfg.AdaptMinSpeechMin)
	}
	if cfg.AdaptMinSpeechMax != 600 {
		t.Errorf("AdaptMinSpeechMax = %d, want 600", cfg.AdaptMinSpeechMax)
	}
}

func TestAdaptiveConfigValidationCustom(t *testing.T) {
	// Custom values should NOT be overwritten by setDefaults
	cfg := AdaptiveConfig{
		WindowDuration: 60,
		NoiseFloorFrac: 0.2,
	}
	cfg.setDefaults()
	if cfg.WindowDuration != 60 {
		t.Errorf("WindowDuration should stay 60, got %.0f", cfg.WindowDuration)
	}
	if cfg.NoiseFloorFrac != 0.2 {
		t.Errorf("NoiseFloorFrac should stay 0.2, got %.2f", cfg.NoiseFloorFrac)
	}
}

func genSine(freq float64, n int, amplitude float32) []float32 {
	pcm := make([]float32, n)
	for i := 0; i < n; i++ {
		pcm[i] = amplitude * float32(math.Sin(2*math.Pi*freq*float64(i)/16000))
	}
	return pcm
}
