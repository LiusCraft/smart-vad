# Adaptive VAD — Design Spec

## Overview

Add dynamic baseline adaptive threshold + RMS energy verification to the smart-vad project, following the strategy described in [the blog post](https://github.com/LiusCraft/smart-vad/issues/1). Implemented as a new `AdaptiveDetector` wrapper in the `vad` package with minimal changes to the existing `Detector`.

## Architecture

```
AdaptiveDetector  (vad/adaptive.go)
  ├── wraps *Detector
  ├── maintains rolling RMS energy window (30s, circular buffer)
  ├── computes noise baseline (avg of quietest 10% frames)
  ├── maps baseline → adapted params (threshold, minSpeech, minSilence)
  ├── injects params via Detector setters
  └── post-filters segments by RMS energy
```

Both batch (`Detect`) and streaming (`Process`/`Flush`) modes are supported through the same AdaptiveDetector API.

## Changes to Existing `Detector` (`vad/vad.go`)

### New fields (initialized from Config in NewDetector)

```go
type Detector struct {
    // ... existing fields
    threshold      float32  // runtime threshold, overrides cfg.Threshold
    minSilenceMs   int
    minSpeechMs    int
}
```

### New methods

```go
func (d *Detector) SetThreshold(t float32)
func (d *Detector) SetMinSilenceDurationMs(ms int)
func (d *Detector) SetMinSpeechDurationMs(ms int)
```

### Internal changes

- `Process()`: replace `d.cfg.Threshold` → `d.threshold`, `d.cfg.MinSilenceDurationMs` → `d.minSilenceMs`
- `Flush()`: replace `d.cfg.MinSpeechDurationMs` → `d.minSpeechMs`

Minimal, backward-compatible. All existing tests pass unchanged.

## New `AdaptiveDetector` (`vad/adaptive.go`)

### Types

```go
type AdaptiveConfig struct {
    DetectorConfig    Config

    WindowDuration    float64  // rolling window in seconds, default 30
    NoiseFloorFrac    float64  // noise floor fraction, default 0.1 (average of bottom 10% frames)
    EnergyOffsetDB    float64  // post-filter offset, default 6

    // Adaptive range
    AdaptThresholdMin float32  // default 0.5
    AdaptThresholdMax float32  // default 0.85
    AdaptMinSpeechMin int      // ms, default 250
    AdaptMinSpeechMax int      // ms, default 600
}

type AdaptiveDetector struct {
    inner       *Detector
    cfg         AdaptiveConfig

    // Circular buffer for frame-level RMS energies (dB)
    frameDB     []float64
    writeIdx    int
    frameCount  int

    baselineDB  float64  // cached noise baseline
    sampleRate  int
    frameSize   int
}
```

### Methods

```go
func NewAdaptiveDetector(cfg AdaptiveConfig) (*AdaptiveDetector, error)

func (a *AdaptiveDetector) Detect(pcm []float32) (Result, error)
func (a *AdaptiveDetector) Reset()
func (a *AdaptiveDetector) Process(chunk []float32) error
func (a *AdaptiveDetector) Flush() Result
func (a *AdaptiveDetector) Destroy() error
```

### Detect (batch mode)

1. Compute per-frame RMS dB for entire `pcm`
2. Sort & average bottom `NoiseFloorFrac` (default 10%) → `baselineDB`
3. `mapParams(baselineDB)` → threshold, minSpeechMs, minSilenceMs
4. Inject params via inner setters
5. `inner.Detect(pcm)` → raw result
6. Filter `raw.Segments`: compute segment-average RMS dB, discard < `baselineDB + EnergyOffsetDB`
7. Return filtered result

### Process (streaming mode)

1. For each frame in chunk:
   - Compute frame RMS dB, append to circular buffer
2. After processing all frames in chunk: recompute P85 from buffer → `baselineDB`
3. `mapParams(baselineDB)` → inject params via inner setters
4. `inner.Process(chunk)`

### Flush

1. `inner.Flush()` → raw result
2. Post-filter is NOT applied in Flush (no PCM buffer available in streaming mode).
   Callers who want post-filtering can call `FilterSegments(pcm, result.Segments, sr, minDB)` externally.
3. Return result

### Reset

- Reset inner detector
- Reset circular buffer (clear frame count)
- Reset baseline cache

### Parameter Mapping (`mapParams`)

```go
baseline ≤ -50  → threshold=MinThreshold,   minSpeech=AdaptMinSpeechMin
-50 < baseline ≤ -40 → linear interpolate   MinThreshold→0.7,  AdaptMinSpeechMin→400
-40 < baseline ≤ -35 → linear interpolate   0.7→0.8,           400→500
baseline > -35       → threshold=MaxThreshold, minSpeech=AdaptMinSpeechMax
```

### RMS Energy Calculation

Frame RMS (for baseline window):

```go
func frameRMS(pcm []float32) float64 {
    var sum float64
    for _, s := range pcm {
        sum += float64(s) * float64(s)
    }
    rms := math.Sqrt(sum / float64(len(pcm)))
    return 20 * math.Log10(rms + 1e-9)
}
```

Segment RMS (for post-filter): same formula, applied over the full segment.

### Circular Buffer

- Capacity: `ceil(WindowDuration * SampleRate / frameSize)` frames
- Frame size: 512 (16kHz) or 256 (8kHz) — same as inference window
- On each frame processed: write frameDB[writeIdx], increment writeIdx (mod capacity), increment frameCount (capped at capacity)
- Baseline computed from first `min(frameCount, capacity)` entries

### Edge Cases

- **No speech detected**: RMS post-filter is a no-op (empty segments list)
- **Short audio (< 30s)**: use whatever frames are available for baseline
- **Audio shorter than one window**: return error (same as base Detector)
- **All segments filtered out**: return empty Result (valid state)
- **EnergyOffsetDB too aggressive**: caller can adjust to a lower value

## Testing (`vad/adaptive_test.go`)

### Tests

| Test | Description |
|------|-------------|
| `TestAdaptiveConfigValidation` | Config defaults and validation |
| `TestAdaptiveConfigValidationCustom` | Custom values preserved by setDefaults |
| `TestFrameRMS` | Known sine wave → expected dB value |
| `TestComputeBaseline` | Synthetic frame energies → noise floor average |
| `TestMapParams` | Known baseline → expected threshold/minSpeech |
| `TestFilterSegments` (+AllPass, +AllDiscard) | Segments above/below baseline+offset → filtering |

Tests do not require an ONNX model (unit-test the algorithmic logic only).

## Integration

- `cmd/demo/main.go`: add `--adaptive` flag to enable AdaptiveDetector
- `cmd/server/main.go`: add `--adaptive` flag

When `--adaptive` is passed, create AdaptiveDetector instead of Detector. All other logic (slice, html, output) unchanged.

## Non-Goals

- ONNX model hot-reload
- Speaker diarization / voiceprint
- Multi-microphone beamforming
- Real-time parameter tuning via external signal (e.g., HTTP endpoint)
