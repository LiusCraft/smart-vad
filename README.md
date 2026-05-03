# Smart VAD

Voice Activity Detection (VAD) slicing tool powered by [Silero VAD](https://github.com/snakers4/silero-vad). Detects speech segments in WAV audio, splits into separate files, and generates an interactive HTML report.

## Features

- **VAD Detection** — Silero VAD via ONNX Runtime (CGO)
- **Adaptive VAD** — Dynamic noise floor estimation, auto-adjusting threshold/min_speech, RMS energy post-filter (`--adaptive`)
- **Audio Slicing** — Split WAV by speech segments
- **HTML Report** — Interactive waveform, VAD confidence chart, per-segment audio players with playback speed control
- **Streaming API** — Feed audio chunks incrementally via `Process()`/`Flush()`
- **CLI + HTTP Server** — Command-line tool and web upload interface

## Quick Start

```bash
# Install ONNX Runtime (macOS)
brew install onnxruntime

# Download Silero VAD model
pip install silero-vad
cp $(python3 -c "import silero_vad; import os; print(os.path.join(os.path.dirname(silero_vad.__file__),'data','silero_vad.onnx'))") .

# Run CLI (standard mode)
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" \
CGO_LDFLAGS="-L/opt/homebrew/lib" \
go run ./cmd/demo --input audio.wav --model silero_vad.onnx

# Run CLI (adaptive mode — auto-adjusts to background noise)
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" \
CGO_LDFLAGS="-L/opt/homebrew/lib" \
go run ./cmd/demo --input audio.wav --model silero_vad.onnx --adaptive

# Or run HTTP server
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" \
CGO_LDFLAGS="-L/opt/homebrew/lib" \
go run ./cmd/server --model silero_vad.onnx
```

## Adaptive VAD

In noisy environments (e.g. shopping malls, exhibitions), standard VAD with a fixed threshold can't balance sensitivity vs. false positives:
- **Fixed low threshold** → captures background chatter as speech
- **Fixed high threshold** → misses soft-spoken users in quiet moments

The `--adaptive` flag enables runtime adaptation with three mechanisms:

### 1. Noise Floor Estimation
Computes the average RMS energy of the quietest 10% of audio frames. This captures the **background noise level** rather than speech energy, ensuring accurate baseline in mixed speech/noise audio.

### 2. Dynamic Parameter Mapping
Maps the noise floor to VAD parameters:

| Noise Floor | Threshold | Min Speech |
|-------------|-----------|------------|
| ≤ -50 dB    | 0.5       | 250 ms     |
| -50 ~ -40   | 0.5 → 0.7 | 250 → 400  |
| -40 ~ -35   | 0.7 → 0.8 | 400 → 500  |
| > -35 dB    | 0.85      | 600 ms     |

### 3. RMS Energy Post-Filter
Discards detected segments whose average energy is below `noiseFloor + 6 dB`, filtering out distant/unintended speech.

### SDK Usage

```go
import "github.com/LiusCraft/smart-vad/vad"

// Batch mode
adaptCfg := vad.AdaptiveConfig{
    DetectorConfig: vad.Config{
        ModelPath:  "silero_vad.onnx",
        SampleRate: 16000,
    },
}
adaptDetector, _ := vad.NewAdaptiveDetector(adaptCfg)
result, _ := adaptDetector.Detect(pcm)

// Streaming mode
adaptDetector.Reset()
adaptDetector.Process(chunk1)
adaptDetector.Process(chunk2)
result := adaptDetector.Flush()
```

## Project Structure

```
├── vad/            # VAD detection package (streaming + batch)
├── slice/          # Audio slicing and WAV export
├── html/           # HTML report generation (embedded templates)
├── template/       # HTML templates (embedded via //go:embed)
├── cmd/demo/       # CLI entry point
└── cmd/server/     # HTTP server
```

## SDK Usage

```go
import "github.com/LiusCraft/smart-vad/vad"

// Batch mode
result, err := detector.Detect(pcm)

// Streaming mode
detector.Reset()
detector.Process(chunk1)
detector.Process(chunk2)
result := detector.Flush()
```
