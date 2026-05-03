# Smart VAD - Design Spec

## Overview

Command-line tool and Go SDK for Silero VAD-based audio slicing. Takes a 16kHz mono WAV file, detects voice segments using Silero VAD (via ONNX Runtime), slices audio into separate files, and generates an HTML visualization report.

## Architecture

```
smart-vad/
├── vad/              # VAD detection package (SDK)
│   └── vad.go
├── slice/            # Audio slicing package (SDK)
│   └── slice.go
├── html/             # HTML report generation (SDK)
│   └── html.go
├── cmd/demo/         # CLI entry point
│   └── main.go
├── go.mod
└── go.sum
```

Data flow: WAV file → PCM float32 → VAD segments → sliced PCM → WAV files + HTML report

## Package: `vad`

Low-level wrapper around `github.com/streamer45/silero-vad-go/speech`. Uses the public `Infer()` method to iterate over audio windows (512 samples @ 16kHz, 256 @ 8kHz), collecting per-frame speech probabilities and applying a state machine to produce segments.

```go
type Config struct {
    ModelPath            string
    SampleRate           int     // 16000
    Threshold            float32 // default 0.5
    MinSilenceDurationMs int     // merge silence threshold
    MinSpeechDurationMs  int     // filter short noise bursts
    SpeechPadMs          int     // padding around segments
}

type Segment struct {
    Start float64 // seconds
    End   float64 // seconds
}

type Result struct {
    Segments []Segment
    Probs    []float32 // per-window speech probabilities, for visualization
}

type Detector struct { ... }

func NewDetector(cfg Config) (*Detector, error)
func (d *Detector) Detect(pcm []float32) (Result, error)
func (d *Detector) Destroy() error
```

State machine logic (same as upstream):
- When prob >= Threshold && !triggered → mark speech start (with SpeechPadMs padding backwards)
- When prob < (Threshold - 0.15) && triggered → begin silence timer
- When silence exceeds MinSilenceDurationMs → mark speech end (with SpeechPadMs padding forward)
- Each window's prob is appended to `Result.Probs` for HTML visualization
- After state machine pass, segments shorter than `MinSpeechDurationMs` are discarded (post-filter)
- `Detect` calls `Reset()` internally before processing to ensure clean state

## Package: `slice`

Splits PCM audio data by VAD segments and exports to WAV files.

```go
func Split(pcm []float32, segments []vad.Segment, sampleRate int) [][]float32

func WriteWAV(filename string, pcm []float32, sampleRate int) error
```

- `Split` does zero-copy sub-slicing of the PCM buffer per segment (converting float64 seconds to sample indices)
- `WriteWAV` writes standard 16-bit PCM WAV files

## Package: `html`

Generates a self-contained HTML report page with three visualization sections:

1. **Full waveform** with VAD regions overlaid (green = speech, gray = silence)
2. **VAD confidence score** line chart along the timeline
3. **Individual segment waveforms** with inline audio playback (base64-encoded)

Uses wavesurfer.js (CDN) for waveform rendering and HTML5 Audio for playback.

```go
type ReportData struct {
    SampleRate   int
    Duration     float64
    PCM          []float32
    VADProbs     []float32    // from vad.Result.Probs
    Segments     []vad.Segment
    SegmentFiles []string     // filenames of sliced WAV files
    SegmentPCM   [][]float32  // sliced PCM for inline base64 audio
}

func Render(data ReportData, w io.Writer) error
```

## CLI (`cmd/demo/main.go`)

```
go run ./cmd/demo --input test.wav --model silero_vad.onnx [--output ./out]
```

Default output: `./output/report.html` + `./output/segments/seg-001.wav`, `seg-002.wav`, ...

## Error Handling

- All packages return errors; CLI handles fatal errors with clear messages
- Invalid WAV format: early error from decoder
- ONNX model not found: error from Detector creation
- Empty audio / no speech detected: valid result with empty segment list

## Dependencies

- `github.com/streamer45/silero-vad-go` — CGO + ONNX Runtime VAD
- `github.com/go-audio/wav` — WAV decoding
- System: ONNX Runtime shared library + C headers (`brew install onnxruntime` on macOS)
- Silero VAD ONNX model file (`silero_vad.onnx`, downloaded from releases)
