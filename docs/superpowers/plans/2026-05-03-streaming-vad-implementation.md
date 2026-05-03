# Streaming VAD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a live microphone capture page (`/live`) that streams audio to the Go server via WebSocket, runs VAD in real-time, and renders results dynamically.

**Architecture:** Browser captures mic via getUserMedia, sends PCM chunks as binary WebSocket messages. Server runs Detector.Process() per chunk, computes state diffs, pushes JSON updates back. Canvas draws scrolling confidence curve; segment cards appear on segment_end events.

**Tech Stack:** Go, gorilla/websocket, vanilla JS, Canvas 2D

---

### Task 1: Add gorilla/websocket dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Run go get**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go get github.com/gorilla/websocket@v1.5.3
```

Expected: `go.mod` includes `github.com/gorilla/websocket v1.5.3`

- [ ] **Verify build**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go build ./...
```

Expected: clean build

- [ ] **Commit**

```
git add go.mod go.sum && git commit -m "chore: add gorilla/websocket dependency"
```

---

### Task 2: Add getter methods to Detector + AdaptiveDetector

**Files:**
- Modify: `vad/vad.go` — add getters after line 225
- Modify: `vad/adaptive.go` — add delegate getters after line 262

- [ ] **Add to `vad/vad.go`** after `SetMinSpeechDurationMs`:

```go
func (d *Detector) GetProbs() []float32     { return d.probs }
func (d *Detector) GetSegments() []Segment   { return d.segments }
func (d *Detector) IsTriggered() bool         { return d.triggered }
func (d *Detector) CurrentSample() int        { return d.currSample }
func (d *Detector) GetThreshold() float32     { return d.threshold }
```

- [ ] **Modify `vad/adaptive.go`** — store baseline after Process so streaming can read it:

In `Process()`, add `a.baselineDB = baseline` after computing baseline:

```go
func (a *AdaptiveDetector) Process(chunk []float32) error {
	ws := a.frameSize
	for i := 0; i <= len(chunk)-ws; i += ws {
		a.addFrame(frameRMS(chunk[i : i+ws]))
	}
	baseline := a.computeBaseline()
	a.baselineDB = baseline  // ← add this line
	threshold, minSpeechMs, minSilenceMs := a.mapParams(baseline)
	a.inner.SetThreshold(threshold)
	a.inner.SetMinSpeechDurationMs(minSpeechMs)
	a.inner.SetMinSilenceDurationMs(minSilenceMs)
	return a.inner.Process(chunk)
}
```

- [ ] **Add delegate getters to `vad/adaptive.go`** after `Destroy`:

```go
func (a *AdaptiveDetector) Inner() *Detector   { return a.inner }
func (a *AdaptiveDetector) GetProbs() []float32 { return a.inner.GetProbs() }
func (a *AdaptiveDetector) GetSegments() []Segment { return a.inner.GetSegments() }
func (a *AdaptiveDetector) IsTriggered() bool   { return a.inner.IsTriggered() }
```

- [ ] **Run vad tests**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go test ./vad/... -v
```

Expected: all tests PASS

- [ ] **Commit**

```
git add vad/vad.go vad/adaptive.go && git commit -m "feat: add getter methods for streaming state"
```

---

### Task 3: Create live.html template

**Files:**
- Create: `template/live.html`

- [ ] **Write `template/live.html`**

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Live VAD</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#1a1a2e;color:#e0e0e0;padding:24px}
.header{display:flex;justify-content:space-between;align-items:center;margin-bottom:20px}
.header h1{font-size:22px}
.header .nav a{color:#4a9eff;text-decoration:none;font-size:13px;margin-left:12px}
.controls-bar{background:#16213e;border-radius:12px;padding:16px 20px;margin-bottom:20px;display:flex;align-items:center;gap:16px;flex-wrap:wrap}
.controls-bar button{background:#0f3460;border:none;color:#fff;padding:10px 20px;border-radius:8px;cursor:pointer;font-size:14px;transition:background .2s}
.controls-bar button:hover{background:#1a4a80}
.controls-bar button.primary{background:#2ecc71}
.controls-bar button.primary:hover{background:#27ae60}
.controls-bar button.danger{background:#e74c3c}
.controls-bar button.danger:hover{background:#c0392b}
.controls-bar button:disabled{background:#555;cursor:not-allowed}
.controls-bar .status{display:flex;align-items:center;gap:8px;font-size:13px;color:#888}
.controls-bar .status .dot{width:10px;height:10px;border-radius:50%;background:#555}
.controls-bar .status .dot.recording{background:#e74c3c;animation:pulse 1s infinite}
.controls-bar .status .dot.speech{background:#f1c40f}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:0.4}}
.controls-bar .stat-val{font-size:13px;color:#888}
.controls-bar .stat-val span{color:#e0e0e0;font-weight:600}
.adaptive-label{display:flex;align-items:center;gap:6px;cursor:pointer;color:#888;font-size:13px}
.adaptive-label input{width:16px;height:16px;accent-color:#4a9eff;cursor:pointer}
.section{background:#16213e;border-radius:12px;padding:20px;margin-bottom:20px}
.section h2{font-size:16px;margin-bottom:12px;color:#a0a0b0}
.chart-wrap{width:100%;height:160px;position:relative;background:#0f3460;border-radius:8px;overflow:hidden}
.chart-wrap canvas{width:100%;height:100%;display:block}
#vadCursor{position:absolute;top:0;width:2px;height:100%;background:#ff6b6b;pointer-events:none;display:none}
.segments-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px}
.segment-card{background:#0f3460;border-radius:8px;padding:12px;font-size:13px;border-left:3px solid #2ecc71;animation:fadeIn .3s}
@keyframes fadeIn{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:translateY(0)}}
.segment-card .seg-header{display:flex;justify-content:space-between;margin-bottom:4px}
.segment-card .seg-num{color:#4a9eff;font-weight:600}
.segment-card .seg-time{color:#aaa}
.segment-card .seg-dur{color:#888;font-size:12px}
.adaptive-info{font-size:12px;color:#888;margin-top:8px;padding:8px 12px;background:#0f3460;border-radius:6px;display:none}
.adaptive-info.show{display:block}
.adaptive-info span{color:#e0e0e0}
.flush-section{display:none}
.flush-section.show{display:block}
.flush-section audio{width:100%;margin-top:8px}
.error-msg{background:#1a0a0a;border:1px solid #e74c3c;border-radius:8px;padding:12px 16px;color:#e74c3c;font-size:13px;margin-bottom:16px;display:none}
</style>
</head>
<body>

<div class="error-msg" id="errorMsg"></div>

<div class="header">
<h1>Live VAD</h1>
<div class="nav"><a href="/">Upload Mode</a></div>
</div>

<div class="controls-bar">
<button id="startBtn" class="primary">Start Mic</button>
<button id="stopBtn" disabled class="danger">Stop</button>
<button id="resetBtn" disabled>Reset</button>
<div class="status">
<span class="dot" id="statusDot"></span>
<span id="statusText">Idle</span>
</div>
<div class="stat-val">
Time: <span id="timeDisplay">0:00</span> &middot;
Segments: <span id="segCount" style="color:#2ecc71">0</span>
</div>
<label class="adaptive-label">
<input type="checkbox" id="adaptiveCb" checked>
Adaptive VAD
</label>
</div>

<div class="section">
<h2>VAD Confidence</h2>
<div class="chart-wrap">
<canvas id="vadCanvas"></canvas>
<div id="vadCursor"></div>
</div>
<div class="adaptive-info" id="adaptiveInfo">
Baseline: <span id="baselineDB">-</span> dB &middot;
Energy Offset: <span id="energyOffsetDB">-</span> dB &middot;
Threshold: <span id="currentThreshold">0.50</span>
</div>
</div>

<div class="section">
<h2>Speech Segments</h2>
<div id="segments" class="segments-grid"></div>
<p id="noSegments" style="color:#555;font-size:13px">No speech segments detected yet</p>
</div>

<div class="section flush-section" id="flushSection">
<h2>Merged Result</h2>
<audio controls id="mergedAudio" style="width:100%"></audio>
</div>

<script>
const SR = 16000;
const CHART_DURATION = 60;
const startBtn = document.getElementById('startBtn');
const stopBtn = document.getElementById('stopBtn');
const resetBtn = document.getElementById('resetBtn');
const statusDot = document.getElementById('statusDot');
const statusText = document.getElementById('statusText');
const timeDisplay = document.getElementById('timeDisplay');
const segCount = document.getElementById('segCount');
const adaptiveCb = document.getElementById('adaptiveCb');
const adaptiveInfo = document.getElementById('adaptiveInfo');
const baselineDB = document.getElementById('baselineDB');
const energyOffsetDB = document.getElementById('energyOffsetDB');
const currentThreshold = document.getElementById('currentThreshold');
const canvas = document.getElementById('vadCanvas');
const vadCursor = document.getElementById('vadCursor');
const segmentsDiv = document.getElementById('segments');
const noSegments = document.getElementById('noSegments');
const flushSection = document.getElementById('flushSection');
const mergedAudio = document.getElementById('mergedAudio');
const errorMsg = document.getElementById('errorMsg');
const ctx = canvas.getContext('2d');

let ws, stream, audioCtx, processor, source, isRunning = false;
let probs = [], segments = [], currentTime = 0, triggered = false, curThreshold = 0.5;
let segmentCounter = 0;

function showError(m) { errorMsg.textContent = m; errorMsg.style.display = 'block'; setTimeout(() => errorMsg.style.display = 'none', 5000); }
function setStatus(t, c) { statusText.textContent = t; statusDot.className = 'dot' + (c ? ' ' + c : ''); }
function fmt(t) { const m = Math.floor(t/60), s = Math.floor(t%60); return m + ':' + String(s).padStart(2,'0'); }

function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(proto + '//' + location.host + '/ws?adaptive=' + adaptiveCb.checked);
  ws.binaryType = 'arraybuffer';
  ws.onopen = () => setStatus('Connected');
  ws.onmessage = e => { if (typeof e.data === 'string') handleMsg(JSON.parse(e.data)); };
  ws.onclose = () => { if (isRunning) { setStatus('Disconnected'); stopMic(); } };
  ws.onerror = () => showError('WebSocket error');
}

function handleMsg(msg) {
  switch (msg.type) {
    case 'probs':
      if (msg.probs && msg.probs.length > 0) {
        probs.push(...msg.probs);
        currentTime = probs.length * 512 / SR;
        redrawChart();
      }
      break;
    case 'segment_start':
      segments.push({start: msg.start, end: null});
      break;
    case 'segment_end':
      for (let i = segments.length - 1; i >= 0; i--) {
        if (segments[i].end === null) {
          segments[i].end = msg.end;
          segments[i].rms = msg.rms || 0;
          segmentCounter++;
          addSegmentCard(segments[i], segmentCounter);
          break;
        }
      }
      segCount.textContent = segments.filter(s => s.end !== null).length;
      noSegments.style.display = 'none';
      break;
    case 'state':
      triggered = msg.triggered;
      if (triggered) statusDot.className = 'dot speech';
      break;
    case 'progress':
      currentTime = msg.time;
      timeDisplay.textContent = fmt(currentTime);
      updateCursor(currentTime);
      break;
    case 'adaptive_info':
      baselineDB.textContent = msg.baseline_db.toFixed(1);
      energyOffsetDB.textContent = msg.energy_offset_db.toFixed(1);
      adaptiveInfo.classList.add('show');
      break;
    case 'threshold':
      curThreshold = msg.threshold;
      currentThreshold.textContent = msg.threshold.toFixed(2);
      break;
    case 'flush_result':
      isRunning = false;
      flushSection.classList.add('show');
      setStatus('Done');
      startBtn.disabled = false;
      stopBtn.disabled = true;
      if (msg.merged_audio) mergedAudio.src = msg.merged_audio;
      break;
    case 'error':
      showError(msg.message);
      break;
  }
}

function resizeCanvas() {
  const p = canvas.parentElement;
  canvas.width = p.clientWidth * 2;
  canvas.height = p.clientHeight * 2;
  canvas.style.width = p.clientWidth + 'px';
  canvas.style.height = p.clientHeight + 'px';
}

function redrawChart() {
  resizeCanvas();
  ctx.scale(2, 2);
  const W = canvas.clientWidth, H = canvas.clientHeight;
  const pad = {t: 10, b: 22, l: 40, r: 16};
  const cw = W - pad.l - pad.r, ch = H - pad.t - pad.b;
  const dur = Math.max(currentTime || 1, 1);
  const scrollOff = currentTime > CHART_DURATION ? currentTime - CHART_DURATION : 0;
  const viewDur = Math.min(dur, CHART_DURATION);

  ctx.fillStyle = '#0f3460'; ctx.fillRect(0, 0, W, H);
  ctx.strokeStyle = 'rgba(255,255,255,0.08)'; ctx.lineWidth = 1;
  ctx.font = '10px sans-serif'; ctx.textAlign = 'right';
  for (let i = 0; i <= 4; i++) {
    const y = pad.t + ch / 4 * i;
    ctx.beginPath(); ctx.moveTo(pad.l, y); ctx.lineTo(W - pad.r, y); ctx.stroke();
    ctx.fillStyle = '#666'; ctx.fillText((1 - i * 0.25).toFixed(2), pad.l - 6, y + 4);
  }
  const ty = pad.t + ch * (1 - Math.min(1, Math.max(0, curThreshold)));
  ctx.strokeStyle = 'rgba(255,107,107,0.4)'; ctx.setLineDash([4, 4]);
  ctx.beginPath(); ctx.moveTo(pad.l, ty); ctx.lineTo(W - pad.r, ty); ctx.stroke(); ctx.setLineDash([]);
  ctx.fillStyle = 'rgba(255,107,107,0.6)'; ctx.font = '9px sans-serif'; ctx.textAlign = 'left';
  ctx.fillText('threshold ' + curThreshold.toFixed(2), W - pad.r - 82, ty - 4);

  if (probs.length < 2) return;
  ctx.strokeStyle = '#2ecc71'; ctx.lineWidth = 2; ctx.beginPath();
  let first = true;
  for (let i = 0; i < probs.length; i++) {
    const t = i * 512 / SR;
    if (t < scrollOff) continue;
    const x = pad.l + cw * (t - scrollOff) / viewDur;
    const y = pad.t + ch * (1 - Math.min(1, Math.max(0, probs[i])));
    if (first) { ctx.moveTo(x, y); first = false; } else { ctx.lineTo(x, y); }
  }
  ctx.stroke();

  ctx.globalAlpha = 0.15;
  for (const seg of segments) {
    if (seg.end === null) continue;
    const x1 = pad.l + cw * (seg.start - scrollOff) / viewDur;
    const x2 = pad.l + cw * (seg.end - scrollOff) / viewDur;
    if (x2 < pad.l || x1 > W - pad.r) continue;
    ctx.fillStyle = '#2ecc71';
    ctx.fillRect(Math.max(pad.l, x1), pad.t, Math.min(x2, W-pad.r) - Math.max(pad.l, x1), ch);
  }
  ctx.globalAlpha = 1;

  ctx.fillStyle = '#666'; ctx.font = '10px sans-serif'; ctx.textAlign = 'center';
  for (let i = 0; i <= 6; i++) {
    const t = scrollOff + viewDur / 6 * i;
    ctx.fillText(t.toFixed(1) + 's', pad.l + cw / 6 * i, H - pad.b + 10);
  }
}

function updateCursor(t) {
  const p = canvas.parentElement;
  const W = p.clientWidth, pad = {l: 40, r: 16}, cw = W - pad.l - pad.r;
  const scrollOff = currentTime > CHART_DURATION ? currentTime - CHART_DURATION : 0;
  const viewDur = Math.min(Math.max(currentTime || 1, 1), CHART_DURATION);
  const x = pad.l + cw * (t - scrollOff) / viewDur;
  vadCursor.style.left = x + 'px';
  vadCursor.style.display = (t > 0 && x >= pad.l && x <= W - pad.r) ? 'block' : 'none';
}

function addSegmentCard(seg, num) {
  const card = document.createElement('div');
  card.className = 'segment-card';
  card.innerHTML = '<div class="seg-header"><span class="seg-num">Segment ' + num + '</span><span class="seg-time">' + seg.start.toFixed(2) + 's &ndash; ' + seg.end.toFixed(2) + 's</span></div><div class="seg-dur">Duration: ' + (seg.end - seg.start).toFixed(2) + 's' + (seg.rms ? ' &middot; RMS: ' + seg.rms.toFixed(1) + ' dB' : '') + '</div>';
  segmentsDiv.appendChild(card);
  card.scrollIntoView({behavior: 'smooth', block: 'end'});
}

async function startMic() {
  try {
    stream = await navigator.mediaDevices.getUserMedia({audio: {echoCancellation: true, noiseSuppression: true, sampleRate: {ideal: 16000}, channelCount: {ideal: 1}}});
  } catch (e) { showError('Mic denied: ' + e.message); return; }
  audioCtx = new (window.AudioContext || window.webkitAudioContext)();
  source = audioCtx.createMediaStreamSource(stream);
  processor = audioCtx.createScriptProcessor(4096, 1, 1);
  processor.onaudioprocess = e => {
    if (!isRunning) return;
    const input = e.inputBuffer.getChannelData(0);
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(input);
  };
  source.connect(processor);
  processor.connect(audioCtx.destination);
  isRunning = true;
  startBtn.disabled = true;
  stopBtn.disabled = false;
  resetBtn.disabled = false;
  adaptiveCb.disabled = true;
  setStatus('Recording', 'recording');
  connectWS();
}

function stopMic() {
  isRunning = false;
  if (processor) { processor.disconnect(); processor = null; }
  if (source) { source.disconnect(); source = null; }
  if (audioCtx) { audioCtx.close(); audioCtx = null; }
  if (stream) { stream.getTracks().forEach(t => t.stop()); stream = null; }
  if (ws) { ws.close(); ws = null; }
  adaptiveCb.disabled = false;
  stopBtn.disabled = true;
}

startBtn.addEventListener('click', startMic);
stopBtn.addEventListener('click', () => {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({type: 'flush'}));
  setStatus('Flushing...');
  stopBtn.disabled = true;
});
resetBtn.addEventListener('click', () => {
  probs = []; segments = []; currentTime = 0; triggered = false; curThreshold = 0.5;
  segmentCounter = 0;
  adaptiveInfo.classList.remove('show');
  segmentsDiv.innerHTML = '';
  noSegments.style.display = 'block';
  flushSection.classList.remove('show');
  segCount.textContent = '0';
  vadCursor.style.display = 'none';
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({type: 'reset'}));
  redrawChart();
});

let rt;
window.addEventListener('resize', () => { clearTimeout(rt); rt = setTimeout(redrawChart, 200); });
</script>
</body>
</html>
```

- [ ] **Verify file exists**

```
ls -la template/live.html
```

- [ ] **Commit**

```
git add template/live.html && git commit -m "feat: add live VAD HTML template"
```

---

### Task 4: Embed live.html in template.go

**Files:**
- Modify: `template/template.go`

- [ ] **Add embed directive** — replace file content with:

```go
package templates

import _ "embed"

//go:embed report.html
var Report string

//go:embed live.html
var Live string
```

- [ ] **Verify compiles**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go build ./...
```

Expected: clean build

- [ ] **Commit**

```
git add template/template.go && git commit -m "feat: embed live.html template"
```

---

### Task 5: Add WebSocket handler and /live route to server

**Files:**
- Modify: `cmd/server/main.go`

This is the core task. The server needs:
1. `/live` route → serves the live.html template
2. `/ws` route → WebSocket handler for streaming VAD

- [ ] **Rewrite `cmd/server/main.go`** — complete replacement:

```go
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/go-audio/wav"
	"github.com/gorilla/websocket"
	"github.com/liushunshun/smart-vad/html"
	"github.com/liushunshun/smart-vad/slice"
	"github.com/liushunshun/smart-vad/template"
	"github.com/liushunshun/smart-vad/vad"
)

var modelPath string

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	model := flag.String("model", "silero_vad.onnx", "path to silero_vad.onnx")
	flag.Parse()
	modelPath = *model

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		log.Fatalf("model not found: %s", modelPath)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/live", handleLive)
	mux.HandleFunc("/ws", handleWS)
	mux.HandleFunc("/analyze", handleAnalyze)

	log.Printf("Starting server on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	if err := html.Render(html.ReportData{}, &buf); err != nil {
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

func handleLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(templates.Live))
}

// ---- WebSocket session ----

type wsSession struct {
	mu sync.Mutex

	conn          *websocket.Conn
	detector      *vad.Detector
	adaptDetector *vad.AdaptiveDetector

	triggered   bool

	// Accumulated PCM for flush
	pcmBuf []float32
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	session := &wsSession{conn: conn}

	adaptive := r.URL.Query().Get("adaptive") == "true"
	if adaptive {
		ad, err := vad.NewAdaptiveDetector(vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            modelPath,
				SampleRate:           16000,
				Threshold:            0.5,
				MinSilenceDurationMs: 100,
				MinSpeechDurationMs:  100,
				SpeechPadMs:          30,
			},
		})
		if err != nil {
			conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
		session.adaptDetector = ad
		session.detector = ad.Inner()
	} else {
		d, err := vad.NewDetector(vad.Config{
			ModelPath:            modelPath,
			SampleRate:           16000,
			Threshold:            0.5,
			MinSilenceDurationMs: 100,
			MinSpeechDurationMs:  100,
			SpeechPadMs:          30,
		})
		if err != nil {
			conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
		session.detector = d
	}
	defer session.detector.Destroy()
	session.detector.Reset()

	// Send initial adaptive info
	if session.adaptDetector != nil {
		conn.WriteJSON(map[string]interface{}{
			"type":             "adaptive_info",
			"baseline_db":      session.adaptDetector.BaselineDB(),
			"energy_offset_db": session.adaptDetector.EnergyOffsetDB(),
		})
		conn.WriteJSON(map[string]interface{}{
			"type":      "threshold",
			"threshold": session.detector.GetThreshold(),
		})
	}

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if mt == websocket.TextMessage {
			var msg map[string]interface{}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg["type"] {
			case "flush":
				session.flush()
				return
			case "reset":
				session.reset()
			}
			continue
		}

		if mt == websocket.BinaryMessage {
			pcm := bytesToFloat32(data)
			session.processChunk(pcm)
		}
	}
}

func bytesToFloat32(data []byte) []float32 {
	n := len(data) / 4
	pcm := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := uint32(data[i*4]) | uint32(data[i*4+1])<<8 |
			uint32(data[i*4+2])<<16 | uint32(data[i*4+3])<<24
		pcm[i] = math.Float32frombits(bits)
	}
	return pcm
}

func (s *wsSession) processChunk(pcm []float32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Capture state before processing
	prevProbsLen := len(s.detector.GetProbs())
	prevSegs := s.detector.GetSegments()
	prevSegLen := len(prevSegs)
	prevLastEnd := float64(0)
	if prevSegLen > 0 {
		prevLastEnd = prevSegs[prevSegLen-1].End
	}

	if s.adaptDetector != nil {
		if err := s.adaptDetector.Process(pcm); err != nil {
			s.conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
	} else {
		if err := s.detector.Process(pcm); err != nil {
			s.conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
	}

	// Accumulate PCM for potential flush
	s.pcmBuf = append(s.pcmBuf, pcm...)

	curProbs := s.detector.GetProbs()
	curSegs := s.detector.GetSegments()

	// Send new probs
	if len(curProbs) > prevProbsLen {
		newProbs := curProbs[prevProbsLen:]
		probs32 := make([]float32, len(newProbs))
		copy(probs32, newProbs)
		s.conn.WriteJSON(map[string]interface{}{
			"type":  "probs",
			"probs": probs32,
		})
	}

	// Detect segment_end: the segment at prevSegLen-1 was unclosed (End==0)
	// and is now closed (End>0). Only the last segment can be unclosed.
	if prevSegLen > 0 && prevLastEnd == 0 && prevSegLen <= len(curSegs) && curSegs[prevSegLen-1].End > 0 {
		closed := curSegs[prevSegLen-1]
		rms := 0.0
		startSample := int(math.Round(closed.Start * 16000))
		endSample := int(math.Round(closed.End * 16000))
		if startSample >= 0 && endSample <= len(s.pcmBuf) && startSample < endSample {
			rms = vad.RMS(s.pcmBuf[startSample:endSample])
		}
		s.conn.WriteJSON(map[string]interface{}{
			"type":  "segment_end",
			"start": closed.Start,
			"end":   closed.End,
			"rms":   rms,
		})
	}

	// Detect new segment starts
	for i := prevSegLen; i < len(curSegs); i++ {
		s.conn.WriteJSON(map[string]interface{}{
			"type":  "segment_start",
			"start": curSegs[i].Start,
		})
	}

	// State change
	curTriggered := s.detector.IsTriggered()
	if curTriggered != s.triggered {
		s.triggered = curTriggered
		s.conn.WriteJSON(map[string]interface{}{
			"type":      "state",
			"triggered": curTriggered,
		})
	}

	// Progress
	currentTime := float64(s.detector.CurrentSample()) / 16000
	s.conn.WriteJSON(map[string]interface{}{
		"type": "progress",
		"time": currentTime,
	})

	// Adaptive threshold update
	if s.adaptDetector != nil {
		s.conn.WriteJSON(map[string]interface{}{
			"type":      "threshold",
			"threshold": s.detector.GetThreshold(),
		})
	}
}

func (s *wsSession) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.detector.Flush()

	// Build merged WAV from segments
	var mergedWAV []byte
	if len(result.Segments) > 0 && len(s.pcmBuf) > 0 {
		mergedPCM := make([]float32, 0)
		for _, seg := range result.Segments {
			startSample := int(math.Round(seg.Start * 16000))
			endSample := int(math.Round(seg.End * 16000))
			if startSample < 0 { startSample = 0 }
			if endSample > len(s.pcmBuf) { endSample = len(s.pcmBuf) }
			if startSample >= endSample { continue }
			mergedPCM = append(mergedPCM, s.pcmBuf[startSample:endSample]...)
		}
		if len(mergedPCM) > 0 {
			mergedWAV = pcmToWAVBytes(mergedPCM, 16000)
		}
	}

	segmentsJSON := make([]map[string]interface{}, len(result.Segments))
	for i, seg := range result.Segments {
		segmentsJSON[i] = map[string]interface{}{
			"start": seg.Start,
			"end":   seg.End,
		}
	}

	resp := map[string]interface{}{
		"type":     "flush_result",
		"segments": segmentsJSON,
		"duration": float64(s.detector.CurrentSample()) / 16000,
	}
	if len(mergedWAV) > 0 {
		b64 := base64.StdEncoding.EncodeToString(mergedWAV)
		resp["merged_audio"] = "data:audio/wav;base64," + b64
	}
	s.conn.WriteJSON(resp)
}

func (s *wsSession) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detector.Reset()
	s.pcmBuf = s.pcmBuf[:0]
	s.triggered = false
}

func pcmToWAVBytes(pcm []float32, sampleRate int) []byte {
	n := len(pcm)
	buf := make([]byte, 44+n*2)
	copy(buf[0:4], "RIFF")
	put32(buf[4:8], uint32(36+n*2))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	put32(buf[16:20], 16)
	put16(buf[20:22], 1)
	put16(buf[22:24], 1)
	put32(buf[24:28], uint32(sampleRate))
	put32(buf[28:32], uint32(sampleRate*2))
	put16(buf[32:34], 2)
	put16(buf[34:36], 16)
	copy(buf[36:40], "data")
	put32(buf[40:44], uint32(n*2))
	for i, s := range pcm {
		v := int16(s * math.MaxInt16)
		if s < 0 { v = int16(s * 0x8000) }
		put16(buf[44+i*2:], uint16(v))
	}
	return buf
}

func put16(buf []byte, v uint16) { binary.LittleEndian.PutUint16(buf, v) }
func put32(buf []byte, v uint32) { binary.LittleEndian.PutUint32(buf, v) }

// ---- Analyze handler (unchanged from original) ----

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	file, _, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "missing audio file", 400)
		return
	}
	defer file.Close()

	tmpDir, err := os.MkdirTemp("", "smart-vad-*")
	if err != nil {
		http.Error(w, "temp dir failed", 500)
		return
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "input.wav")
	f, err := os.Create(tmpFile)
	if err != nil {
		http.Error(w, "create temp failed", 500)
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		f.Close()
		http.Error(w, "write temp failed", 500)
		return
	}
	f.Close()

	af, err := os.Open(tmpFile)
	if err != nil {
		http.Error(w, "open temp failed", 500)
		return
	}
	defer af.Close()

	dec := wav.NewDecoder(af)
	if !dec.IsValidFile() {
		http.Error(w, "invalid WAV", 400)
		return
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		http.Error(w, fmt.Sprintf("read PCM: %v", err), 500)
		return
	}

	pcm := buf.AsFloat32Buffer().Data
	sr := dec.SampleRate

	if sr != 16000 && sr != 8000 {
		http.Error(w, fmt.Sprintf("unsupported sample rate: %d", sr), 400)
		return
	}

	var result vad.Result
	var filteredSegments []vad.Segment
	var adaptDetector *vad.AdaptiveDetector

	if r.FormValue("adaptive") == "true" {
		var err error
		adaptDetector, err = vad.NewAdaptiveDetector(vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            modelPath,
				SampleRate:           16000,
				Threshold:            0.5,
				MinSilenceDurationMs: 100,
				MinSpeechDurationMs:  100,
				SpeechPadMs:          30,
			},
		})
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
		filteredSegments = adaptDetector.FilteredSegments()
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

	starts := make([]float64, len(result.Segments))
	ends := make([]float64, len(result.Segments))
	for i, s := range result.Segments {
		starts[i] = s.Start
		ends[i] = s.End
	}
	srInt := int(sr)
	segPCMs := slice.Split(pcm, starts, ends, srInt)

	segFiles := make([]string, len(segPCMs))
	segDir := filepath.Join(tmpDir, "segments")
	os.MkdirAll(segDir, 0755)
	for i, seg := range segPCMs {
		fname := filepath.Join(segDir, fmt.Sprintf("seg-%03d.wav", i+1))
		slice.WriteWAV(fname, seg, srInt)
		segFiles[i] = fname
	}

	var filteredPCMs [][]float32
	if len(filteredSegments) > 0 {
		fStarts := make([]float64, len(filteredSegments))
		fEnds := make([]float64, len(filteredSegments))
		for i, s := range filteredSegments {
			fStarts[i] = s.Start
			fEnds[i] = s.End
		}
		filteredPCMs = slice.Split(pcm, fStarts, fEnds, srInt)
	}

	duration := float64(len(pcm)) / float64(sr)
	htmlSegments := make([]html.Segment, len(result.Segments))
	for i, s := range result.Segments {
		rms := 0.0
		if i < len(segPCMs) {
			rms = vad.RMS(segPCMs[i])
		}
		htmlSegments[i] = html.Segment{Start: s.Start, End: s.End, RMS: rms}
	}

	htmlFiltered := make([]html.Segment, len(filteredSegments))
	for i, s := range filteredSegments {
		rms := 0.0
		if i < len(filteredPCMs) {
			rms = vad.RMS(filteredPCMs[i])
		}
		htmlFiltered[i] = html.Segment{Start: s.Start, End: s.End, RMS: rms}
	}

	var baselineDB, energyOffsetDB float64
	if adaptDetector != nil {
		baselineDB = adaptDetector.BaselineDB()
		energyOffsetDB = adaptDetector.EnergyOffsetDB()
	}

	var reportBuf bytes.Buffer
	if err := html.Render(html.ReportData{
		SampleRate:         srInt,
		Duration:           duration,
		PCM:                pcm,
		VADProbs:           result.Probs,
		Segments:           htmlSegments,
		FilteredSegments:   htmlFiltered,
		SegmentFiles:       segFiles,
		SegmentPCM:         segPCMs,
		FilteredSegmentPCM: filteredPCMs,
		BackURL:            "/",
		HasResults:         true,
		AdaptiveVAD:        r.FormValue("adaptive") == "true",
		BaselineDB:         baselineDB,
		EnergyOffsetDB:     energyOffsetDB,
	}, &reportBuf); err != nil {
		http.Error(w, fmt.Sprintf("render: %v", err), 500)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(reportBuf.Bytes())
}
```

Note the import changed from `"github.com/liushunshun/smart-vad/template"` to `"github.com/liushunshun/smart-vad/templates"` — the directory is `template/` but the package is `templates` (from `template/template.go:1`). Verify this is the correct package name from the existing code.



- [ ] **Build and verify**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go build ./cmd/server
```

Expected: clean build, `server` binary created

- [ ] **Run existing tests**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go test ./... -v
```

Expected: all tests PASS (no regressions)

- [ ] **Commit**

```
git add cmd/server/main.go && git commit -m "feat: add WebSocket handler and /live route for streaming VAD"
```

---

### Task 6: Manual integration test

- [ ] **Start server**

```
cd /Users/liushunshun/workspace/coding/voice/smart-vad && \
CGO_CFLAGS="-I/opt/homebrew/include/onnxruntime" CGO_LDFLAGS="-L/opt/homebrew/lib" \
go run ./cmd/server --model silero_vad.onnx
```

Expected: server starts on `:8080`

- [ ] **Open live page**

Open browser to `http://localhost:8080/live`

Expected: Live VAD page loads with dark theme, Start Mic button visible

- [ ] **Test mic capture**

Click "Start Mic" — browser asks for mic permission. Allow it.

Expected: Status shows "Recording" with red dot, time counter advances, VAD confidence curve appears

- [ ] **Test speech detection**

Speak into the microphone. 

Expected: VAD confidence curve shows fluctuations above/below threshold line. Speech segments highlighted in green on chart. Segment cards appear dynamically.

- [ ] **Test stop + flush**

Click "Stop" — sends flush to server.

Expected: "Merged Result" section appears with playable audio of all speech segments concatenated.

- [ ] **Test adaptive mode**

Refresh page. Keep Adaptive VAD checkbox checked. Start mic with background noise + speech.

Expected: Adaptive info section shows baseline dB and energy offset. Threshold may adjust over time based on noise floor.

- [ ] **Test reset**

Start mic, let some audio accumulate. Click "Reset" — chart clears, segments removed, counter resets.
