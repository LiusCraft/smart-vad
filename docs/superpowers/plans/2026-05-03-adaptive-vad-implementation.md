# Adaptive VAD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add dynamic baseline adaptive threshold + RMS energy verification to the smart-vad project.

**Architecture:** New `AdaptiveDetector` wrapper in `vad/adaptive.go` with minimal changes to existing `Detector` (`vad/vad.go`). Three independent mechanisms: rolling RMS noise baseline (P85 over 30s window), dynamic parameter mapping (baseline → threshold/minSpeech/minSilence), and RMS energy post-filter. Both modes supported — batch (`Detect`) uses full audio for baseline and applies post-filter internally; streaming (`Process/Flush`) adjusts params per-chunk.

**Tech Stack:** Go 1.21+, `github.com/streamer45/silero-vad-go`

---

## File Structure

### New files
- `vad/adaptive.go` — AdaptiveDetector + standalone helpers (RMS, FilterSegments)

### Modified files
- `vad/vad.go` — Add runtime-adjustable fields + setters to Detector
- `vad/vad_test.go` — Add setter tests
- `cmd/demo/main.go` — Add `--adaptive` flag
- `cmd/server/main.go` — Add `--adaptive` flag

---

### Task 1: Modify Detector with runtime-adjustable params

**Files:**
- Modify: `vad/vad.go` (add fields + setters + update state machine references)
- Modify: `vad/vad_test.go` (add setter tests)

- [ ] **Step 1: Write setter tests** in `vad/vad_test.go`

Paste this at the end of `vad/vad_test.go`:

```go
func TestRuntimeSetters(t *testing.T) {
	d := &Detector{
		cfg:         Config{Threshold: 0.5, MinSilenceDurationMs: 100, MinSpeechDurationMs: 100},
		threshold:   0.5,
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
		cfg:         Config{Threshold: 0.5, MinSilenceDurationMs: 100, MinSpeechDurationMs: 100},
		threshold:   0.5,
		minSilenceMs: 100,
		minSpeechMs:  100,
	}

	d.SetThreshold(0)   // invalid (must be > 0)
	if d.threshold != 0.5 {
		t.Errorf("threshold should remain 0.5, got %f", d.threshold)
	}

	d.SetThreshold(1)   // invalid (must be < 1)
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
```

- [ ] **Step 2: Run setter tests to verify they fail**

Run: `go test ./vad/ -v -run TestRuntimeSetters`
Expected: FAIL — `SetThreshold` method not defined

- [ ] **Step 3: Add runtime fields + setters to Detector**

In `vad/vad.go`, add three new fields to `Detector` struct:

```go
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
```

In `NewDetector`, initialize the new fields after the cfg validation block:

```go
	return &Detector{
		inner:        inner,
		cfg:          cfg,
		threshold:    cfg.Threshold,
		minSilenceMs: cfg.MinSilenceDurationMs,
		minSpeechMs:  cfg.MinSpeechDurationMs,
	}, nil
```

Add setter methods after `Destroy`:

```go
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
```

- [ ] **Step 4: Update state machine to use runtime fields**

In `Process()`, replace all references:

| Before | After |
|--------|-------|
| `d.cfg.Threshold` | `d.threshold` |
| `d.cfg.Threshold-0.15` | `d.threshold-0.15` |
| `d.cfg.MinSilenceDurationMs` | `d.minSilenceMs` |

In `Flush()`, replace:

| Before | After |
|--------|-------|
| `d.cfg.MinSpeechDurationMs` | `d.minSpeechMs` |

- [ ] **Step 5: Run all vad tests**

Run: `go test ./vad/ -v`
Expected: All tests pass (existing + new setter tests)

- [ ] **Step 6: Commit**

```bash
git add vad/vad.go vad/vad_test.go
git commit -m "feat: add runtime-adjustable params to Detector"
```

---

### Task 2: Create AdaptiveDetector + standalone helpers

**Files:**
- Create: `vad/adaptive.go`
- Create: `vad/adaptive_test.go`

- [ ] **Step 1: Write adaptive_test.go**

Create `vad/adaptive_test.go`:

```go
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
	a := &AdaptiveDetector{cfg: AdaptiveConfig{
		AdaptThresholdMin: 0.5,
		AdaptThresholdMax: 0.85,
		AdaptMinSpeechMin: 250,
		AdaptMinSpeechMax: 600,
	}}

	tests := []struct {
		name          string
		baseline      float64
		wantThreshold float32
		wantMinSpeech int
	}{
		{"very quiet", -55, 0.5, 250},
		{"quiet boundary low", -50, 0.5, 250},
		{"mid quiet", -45, 0.6, 325},
		{"mid noisy", -37, 0.74, 440},
		{"noisy boundary high", -35, 0.8, 500},
		{"very noisy", -30, 0.85, 600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th, ms := a.mapParams(tt.baseline)
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
	// segment 0: loud (indices 1000-3000)
	for i := 1000; i < 3000; i++ {
		pcm[i] = 0.8
	}
	// segment 1: quiet (indices 5000-7000)
	for i := 5000; i < 7000; i++ {
		pcm[i] = 0.01
	}

	segments := []Segment{
		{Start: 0.0625, End: 0.1875}, // 1000-3000 @ 16kHz
		{Start: 0.3125, End: 0.4375}, // 5000-7000 @ 16kHz
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
	pcm := make([]float32, 16000) // all silence
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
		cfg: AdaptiveConfig{
			Percentile: 0.85,
		},
		frameDB: make([]float64, 0, 100),
	}

	// Add 100 frames: 90 at -50 dB, 10 at -20 dB
	for i := 0; i < 90; i++ {
		a.addFrame(-50)
	}
	for i := 0; i < 10; i++ {
		a.addFrame(-20)
	}

	baseline := a.computeBaseline()
	// P85 at 85th index = -20 (since 85 out of 100 = the -20 region)
	if baseline > -25 {
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
```

- [ ] **Step 2: Run adaptive tests to verify they fail**

Run: `go test ./vad/ -v -run TestFrameRMS`
Expected: FAIL — `frameRMS` not defined

- [ ] **Step 3: Write vad/adaptive.go**

Create `vad/adaptive.go`:

```go
package vad

import (
	"math"
	"sort"
)

// --- Standalone helpers ---

// RMS returns the RMS energy of pcm in dBFS.
func RMS(pcm []float32) float64 {
	var sum float64
	for _, s := range pcm {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(pcm)))
	return 20 * math.Log10(rms + 1e-9)
}

// FilterSegments discards segments whose average RMS energy is below minDB.
// minDB should be an absolute threshold (e.g., baselineDB + offset).
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

// --- Internal helpers ---

func frameRMS(frame []float32) float64 {
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(frame)))
	return 20*math.Log10(rms+1e-9) + 3.01 // +3dB to approximate dBFS for 16-bit signed
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

// --- AdaptiveConfig ---

type AdaptiveConfig struct {
	DetectorConfig Config

	WindowDuration  float64 // rolling window in seconds, default 30
	Percentile      float64 // noise baseline percentile, default 0.85
	EnergyOffsetDB  float64 // post-filter offset from baseline, default 6

	AdaptThresholdMin float32 // default 0.5
	AdaptThresholdMax float32 // default 0.85
	AdaptMinSpeechMin int     // ms, default 250
	AdaptMinSpeechMax int     // ms, default 600
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

// --- AdaptiveDetector ---

type AdaptiveDetector struct {
	inner      *Detector
	cfg        AdaptiveConfig

	frameDB    []float64 // capped slice of frame RMS dB values
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
	minDB := -60.0
	midLowDB := -50.0
	midHighDB := -40.0
	highDB := -35.0

	switch {
	case baselineDB <= midLowDB:
		return a.cfg.AdaptThresholdMin, a.cfg.AdaptMinSpeechMin, a.inner.minSilenceMs
	case baselineDB <= midHighDB:
		t := (baselineDB - midLowDB) / (midHighDB - midLowDB)
		th := lerp(float64(a.cfg.AdaptThresholdMin), 0.7, t)
		ms := lerp(float64(a.cfg.AdaptMinSpeechMin), 400, t)
		return float32(th), int(math.Round(ms)), a.inner.minSilenceMs
	case baselineDB <= highDB:
		t := (baselineDB - midHighDB) / (highDB - midHighDB)
		th := lerp(0.7, 0.8, t)
		ms := lerp(400, 500, t)
		return float32(th), int(math.Round(ms)), a.inner.minSilenceMs
	default:
		return a.cfg.AdaptThresholdMax, a.cfg.AdaptMinSpeechMax, a.inner.minSilenceMs
	}
}

// --- Batch mode ---

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

// --- Streaming mode ---

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
	result := a.inner.Flush()
	return result
}

func (a *AdaptiveDetector) Destroy() error {
	return a.inner.Destroy()
}
```

- [ ] **Step 4: Run adaptive tests**

Run: `go test ./vad/ -v -run 'TestFrameRMS|TestMapParams|TestFilterSegments|TestComputeBaseline'`
Expected: All 7 tests pass

- [ ] **Step 5: Commit**

```bash
git add vad/adaptive.go vad/adaptive_test.go
git commit -m "feat: add AdaptiveDetector with dynamic baseline and RMS filtering"
```

---

### Task 3: Update CLI and server with --adaptive flag

**Files:**
- Modify: `cmd/demo/main.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Update cmd/demo/main.go**

Add `adaptive` flag near other flags:

```go
	adaptive := flag.Bool("adaptive", false, "enable adaptive VAD (dynamic threshold)")
```

After the sample rate check and before "VAD detection" comment, add adaptive branch:

```go
	var result vad.Result

	if *adaptive {
		log.Print("Adaptive VAD enabled")
		adaptCfg := vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            *model,
				SampleRate:           sampleRate,
				Threshold:            float32(*threshold),
				MinSilenceDurationMs: *minSilence,
				MinSpeechDurationMs:  *minSpeech,
				SpeechPadMs:          *padMs,
			},
		}
		adaptDetector, err := vad.NewAdaptiveDetector(adaptCfg)
		if err != nil {
			log.Fatalf("create adaptive detector: %v", err)
		}
		defer adaptDetector.Destroy()

		result, err = adaptDetector.Detect(pcm)
		if err != nil {
			log.Fatalf("detect: %v", err)
		}
	} else {
		detector, err := vad.NewDetector(vad.Config{
			ModelPath:            *model,
			SampleRate:           sampleRate,
			Threshold:            float32(*threshold),
			MinSilenceDurationMs: *minSilence,
			MinSpeechDurationMs:  *minSpeech,
			SpeechPadMs:          *padMs,
		})
		if err != nil {
			log.Fatalf("create detector: %v", err)
		}
		defer detector.Destroy()

		result, err = detector.Detect(pcm)
		if err != nil {
			log.Fatalf("detect: %v", err)
		}
	}
```

Remove the old detector creation and result lines (the block after line 57 in the current file).

- [ ] **Step 2: Update cmd/server/main.go**

Add `adaptive` flag before `model`:

```go
	adaptive := flag.Bool("adaptive", false, "enable adaptive VAD (dynamic threshold)")
```

Replace the single detector creation block with an adaptive branch. After sample rate check and before "VAD detection" in `handleAnalyze`:

```go
	var result vad.Result

	if *adaptive {
		adaptCfg := vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            modelPath,
				SampleRate:           16000,
				Threshold:            0.5,
				MinSilenceDurationMs: 100,
				MinSpeechDurationMs:  100,
				SpeechPadMs:          30,
			},
		}
		adaptDetector, err := vad.NewAdaptiveDetector(adaptCfg)
		if err != nil {
			http.Error(w, fmt.Sprintf("adaptive detector: %v", err), 500)
			return
		}
		defer adaptDetector.Destroy()

		result, err = adaptDetector.Detect(pcm)
		if err != nil {
			http.Error(w, fmt.Sprintf("detect: %v", err), 500)
			return
		}
	} else {
		detector, err := vad.NewDetector(vad.Config{
			ModelPath:            modelPath,
			SampleRate:           16000,
			Threshold:            0.5,
			MinSilenceDurationMs: 100,
			MinSpeechDurationMs:  100,
			SpeechPadMs:          30,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("detector: %v", err), 500)
			return
		}
		defer detector.Destroy()

		result, err = detector.Detect(pcm)
		if err != nil {
			http.Error(w, fmt.Sprintf("detect: %v", err), 500)
			return
		}
	}
```

Remove the old detector creation + detect lines (lines 77-122 in current file).

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: Compiles without errors

- [ ] **Step 4: Commit**

```bash
git add cmd/demo/main.go cmd/server/main.go
git commit -m "feat: add --adaptive flag to CLI and server"
```

---

### Self-Review

1. **Spec coverage:**
   - Detector runtime fields + setters → Task 1 ✓
   - frameRMS helper → Task 2 ✓
   - FilterSegments standalone function → Task 2 ✓
   - AdaptiveDetector.Detect (batch, baseline + map + filter) → Task 2 ✓
   - AdaptiveDetector.Process/Flush (streaming, per-chunk update) → Task 2 ✓
   - Parameter mapping with interpolation → Task 2 ✓
   - CLI + server --adaptive flag → Task 3 ✓
   - All unit tests covered: frameRMS, mapParams, FilterSegments, computeBaseline → Task 2 ✓

2. **Placeholder scan:** No TBD, TODO, "similar to", "fill in details" patterns. Every step has complete code.

3. **Type consistency:**
   - `AdaptiveConfig.DetectorConfig` is `vad.Config` — matches existing type ✓
   - `AdaptiveDetector.Detect` returns `Result` with `Segments []Segment` and `Probs []float32` — matches existing type ✓
   - `FilterSegments` uses `Segment{Start, End float64}` — matches vad.Segment ✓
   - `frameRMS` returns `float64` — used as dB value with `addFrame(float64)` ✓
   - `mapParams` returns `(threshold float32, minSpeechMs int, minSilenceMs int)` — matches setter signatures ✓
   - `len(a.frameDB)` slice operations — consistent with capped slice approach ✓
