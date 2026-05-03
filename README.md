# Smart VAD

Voice Activity Detection (VAD) slicing tool powered by [Silero VAD](https://github.com/snakers4/silero-vad). Detects speech segments in WAV audio, splits into separate files, and generates an interactive HTML report.

## Features

- **VAD Detection** — Silero VAD via ONNX Runtime (CGO)
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

# Run CLI
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" \
CGO_LDFLAGS="-L/opt/homebrew/lib" \
go run ./cmd/demo --input audio.wav --model silero_vad.onnx

# Or run HTTP server
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" \
CGO_LDFLAGS="-L/opt/homebrew/lib" \
go run ./cmd/server --model silero_vad.onnx
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
