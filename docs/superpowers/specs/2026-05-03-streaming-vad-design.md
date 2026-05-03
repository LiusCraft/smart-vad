# Streaming VAD — Live Microphone Analysis

## Overview

Add a live microphone capture page (`/live`) to the Smart VAD web application. The browser captures audio from the user's microphone via `getUserMedia`, sends PCM chunks to the Go server over WebSocket, which runs streaming VAD (`Detector.Process()`) and pushes incremental results back for real-time rendering.

## Architecture

```
Browser Live Page                    Go Server
┌─────────────────────┐             ┌──────────────────────┐
│ getUserMedia        │  WebSocket  │ /ws handler          │
│ → PCM chunks        │ ──binary──▶ │ → Detector.Process() │
│                     │ ◀──JSON──── │ → state diff → push  │
│ Canvas实时曲线       │  增量更新    │                      │
│ 动态Segment Cards    │             │ gorilla/websocket    │
│ 音频播放器           │             │                      │
└─────────────────────┘             └──────────────────────┘
```

## Backend Changes

### 1. `vad/vad.go` — Add getter methods

```go
func (d *Detector) GetProbs() []float32   // all accumulated probs
func (d *Detector) GetSegments() []Segment // current segments (last may be unclosed)
func (d *Detector) IsTriggered() bool      // currently in speech state
func (d *Detector) CurrentSample() int     // total processed samples
```

These expose necessary streaming state without breaking existing APIs.

### 2. `cmd/server/main.go` — WebSocket handler `/ws`

Upgrade HTTP to WebSocket using `gorilla/websocket`:

**Connection setup**:
- URL param: `/ws?adaptive=true` (optional, default false)
- On connect: create `Detector` (or `AdaptiveDetector`), call `Reset()`

**Message handling**:

| Direction | Format | Action |
|-----------|--------|--------|
| Client → Server | Binary (PCM float32, mono 16kHz) | `Detector.Process(chunk)` → diff → push JSON |
| Client → Server | Text `{"type":"flush"}` | `Detector.Flush()` → return final result |
| Client → Server | Text `{"type":"reset"}` | `Detector.Reset()` → clear state |
| Server → Client | Text JSON (see protocol below) | Incremental updates |

**Diff tracking**:
- Before Process: save `len(probs)`, `len(segments)`, `triggered` state
- After Process: compute new probs, new/modified segments, state changes
- Send only the diffs to minimize message size

**WebSocket → Client JSON protocol**:

```jsonc
// New VAD probabilities (appended to existing curve)
{"type":"probs","probs":[0.1,0.85,0.92],"start_index":42}

// A speech segment just started (not yet closed)
{"type":"segment_start","start":12.5}

// A speech segment just ended
{"type":"segment_end","start":12.5,"end":15.3}

// State change: entered or left speech
{"type":"state","triggered":true}

// Current time / progress
{"type":"progress","time":30.2}

// Adaptive VAD baseline info (only if adaptive mode)
{"type":"adaptive_info","baseline_db":-42.5,"energy_offset_db":6}

// Final flush result
{"type":"flush_result","segments":[...],"probs":[...],"duration":45.2}

// Error
{"type":"error","message":"..."}
```

### 3. `cmd/server/main.go` — New handler `/live`

Serves the live VAD HTML page (from embedded template).

## Frontend Changes

### New template: `template/live.html`

A standalone page at `/live` with these sections:

#### Microphone Controls
- "Start Microphone" button → `navigator.mediaDevices.getUserMedia({audio: true})`
- "Stop" button → close mic + send `{"type":"flush"}`
- "Reset" button → send `{"type":"reset"}`
- Status indicator (recording / idle / error)
- VU meter (canvas-based level indicator)

#### Real-time VAD Confidence Chart
- Canvas 2D rendering
- Data arrives incrementally, drawn point by point
- Auto-scroll when curve reaches right edge (shift viewport)
- Threshold line at 0.5 (red dashed)
- Current position cursor (red vertical line)
- Speech segment highlights (green/red translucent rectangles on chart)
- Time axis labels on bottom

#### Segments Cards
- Dynamically created `<div>` cards
- Each card shows: segment number, time range, duration
- Click to seek/play the segment
- Auto-scroll to newest card

#### Merged Audio Player
- After flush, concatenate all segments into a single playable audio
- Similar to existing report's filtered output player

#### Adaptive VAD Info
- Show baseline dB and energy offset if adaptive mode

### Audio Capture Implementation

```js
const stream = await navigator.mediaDevices.getUserMedia({audio: true});
const ac = new AudioContext({sampleRate: 16000});

// Use AudioWorklet for modern browsers, ScriptProcessorNode as fallback
const node = ac.createScriptProcessor(4096, 1, 1);
node.onaudioprocess = (e) => {
  const input = e.inputBuffer.getChannelData(0); // Float32Array
  ws.send(input.buffer); // binary WebSocket message
};
```

Considerations:
- Request 16kHz sample rate from AudioContext if supported
- 4096 samples = 256ms per chunk at 16kHz (reasonable latency)
- Use `AudioWorklet` with fallback to `ScriptProcessorNode` (deprecated but widely supported)

### WebSocket Client

```js
const ws = new WebSocket(`ws://${location.host}/ws?adaptive=${adaptive}`);
ws.binaryType = 'arraybuffer';

ws.onmessage = (e) => {
  const msg = JSON.parse(e.data);
  switch (msg.type) {
    case 'probs':     appendProbs(msg.probs); break;
    case 'segment_start': showSegmentStart(msg.start); break;
    case 'segment_end':   addSegmentCard(msg); break;
    case 'state':     updateTriggered(msg.triggered); break;
    case 'progress':  updateProgress(msg.time); break;
    case 'flush_result': showFlushResult(msg); break;
  }
};
```

### Styling
- Consistent dark theme with existing report page (`#1a1a2e` background)
- Same component styling (cards, buttons, section containers)
- Responsive layout

## Dependencies

- `github.com/gorilla/websocket` — WebSocket support (Go)
- No new frontend dependencies (vanilla JS + Canvas)

## File Changes Summary

| File | Change |
|------|--------|
| `go.mod` | Add `gorilla/websocket` |
| `vad/vad.go` | Add 4 getter methods |
| `cmd/server/main.go` | Add `/ws` and `/live` handlers |
| `template/live.html` | New file: live VAD page |
| `template/template.go` | Embed live.html |

## Edge Cases

- **Mic permission denied**: Show clear error with instructions
- **WebSocket disconnect**: Auto-reconnect with exponential backoff (1s, 2s, 4s, max 30s)
- **Long sessions**: Cap displayed data points (e.g. max 5000 probs), drop oldest on overflow
- **Silence**: Show status indicator when no speech detected for extended period
- **Browser compatibility**: Provide fallback for AudioContext options
- **Concurrent connections**: Each WebSocket gets its own Detector instance (already stateless)
