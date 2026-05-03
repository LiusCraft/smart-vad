# Smart VAD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI tool + SDK for Silero VAD-based audio slicing with HTML visualization.

**Architecture:** Four Go packages (`vad`, `slice`, `html`, `cmd/demo`) layered cleanly. `vad` wraps `streamer45/silero-vad-go` via CGO + ONNX Runtime. `slice` splits PCM by VAD segments. `html` generates a self-contained report with wavesurfer.js. CLI wires them together.

**Tech Stack:** Go 1.21+, `github.com/streamer45/silero-vad-go` (CGO + ONNX Runtime), `github.com/go-audio/wav`, wavesurfer.js CDN

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `vad/vad.go` (stub)
- Create: `slice/slice.go` (stub)
- Create: `html/html.go` (stub)
- Create: `cmd/demo/main.go` (stub)
- Create: `.gitignore`

- [ ] **Step 1: Initialize go.mod and create directory structure**

Run:
```bash
mkdir -p vad slice html cmd/demo
```

- [ ] **Step 2: Write go.mod**

```go
module github.com/liushunshun/smart-vad

go 1.21.4
```

- [ ] **Step 3: Write .gitignore**

```
# Binaries
*.exe
*.exe~
*.dll
*.so
*.dylib

# Test binary
*.test

# Output
/output/

# IDE
.idea/
.vscode/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Dependencies
/vendor/
```

- [ ] **Step 4: Write package stubs**

`vad/vad.go`:
```go
package vad

type Config struct{}
type Segment struct{}
type Result struct{}
type Detector struct{}

func NewDetector(cfg Config) (*Detector, error) { return nil, nil }
func (d *Detector) Detect(pcm []float32) (Result, error) { return Result{}, nil }
func (d *Detector) Destroy() error { return nil }
```

`slice/slice.go`:
```go
package slice

func Split(pcm []float32, segments []any, sampleRate int) [][]float32 { return nil }
func WriteWAV(filename string, pcm []float32, sampleRate int) error { return nil }
```

`html/html.go`:
```go
package html

import "io"

type ReportData struct{}

func Render(data ReportData, w io.Writer) error { return nil }
```

`cmd/demo/main.go`:
```go
package main

func main() {}
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: compiles without errors (stubs are valid Go)

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "chore: scaffold project structure"
```

---

### Task 2: `vad` Package — Core VAD Detection

**Files:**
- Modify: `vad/vad.go`
- Create: `vad/vad_test.go`

- [ ] **Step 1: Write the full type definitions**

Replace `vad/vad.go` contents:

```go
package vad

import (
	"fmt"
	"math"

	"github.com/streamer45/silero-vad-go/speech"
)

type Config struct {
	ModelPath            string
	SampleRate           int
	Threshold            float32
	MinSilenceDurationMs int
	MinSpeechDurationMs  int
	SpeechPadMs          int
}

func (c Config) validate() error {
	if c.ModelPath == "" {
		return fmt.Errorf("ModelPath is required")
	}
	if c.SampleRate != 8000 && c.SampleRate != 16000 {
		return fmt.Errorf("SampleRate must be 8000 or 16000")
	}
	if c.Threshold <= 0 || c.Threshold >= 1 {
		return fmt.Errorf("Threshold must be in (0, 1)")
	}
	if c.MinSilenceDurationMs < 0 {
		return fmt.Errorf("MinSilenceDurationMs must be >= 0")
	}
	if c.MinSpeechDurationMs < 0 {
		return fmt.Errorf("MinSpeechDurationMs must be >= 0")
	}
	if c.SpeechPadMs < 0 {
		return fmt.Errorf("SpeechPadMs must be >= 0")
	}
	return nil
}

func (c Config) windowSize() int {
	if c.SampleRate == 8000 {
		return 256
	}
	return 512
}

type Segment struct {
	Start float64
	End   float64
}

type Result struct {
	Segments []Segment
	Probs    []float32
}

type Detector struct {
	inner *speech.Detector
	cfg   Config
}

func NewDetector(cfg Config) (*Detector, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	// Defaults
	if cfg.Threshold == 0 {
		cfg.Threshold = 0.5
	}

	speechCfg := speech.DetectorConfig{
		ModelPath:            cfg.ModelPath,
		SampleRate:           cfg.SampleRate,
		Threshold:            cfg.Threshold,
		MinSilenceDurationMs: cfg.MinSilenceDurationMs,
		SpeechPadMs:          cfg.SpeechPadMs,
	}

	inner, err := speech.NewDetector(speechCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech detector: %w", err)
	}

	return &Detector{inner: inner, cfg: cfg}, nil
}

func (d *Detector) Detect(pcm []float32) (Result, error) {
	if err := d.inner.Reset(); err != nil {
		return Result{}, fmt.Errorf("reset failed: %w", err)
	}

	ws := d.cfg.windowSize()
	if len(pcm) < ws {
		return Result{}, fmt.Errorf("audio too short: need at least %d samples", ws)
	}

	minSilenceSamples := d.cfg.MinSilenceDurationMs * d.cfg.SampleRate / 1000
	speechPadSamples := d.cfg.SpeechPadMs * d.cfg.SampleRate / 1000

	var segments []Segment
	var probs []float32

	currSample := 0
	triggered := false
	tempEnd := 0

	for i := 0; i <= len(pcm)-ws; i += ws {
		speechProb, err := d.inner.Infer(pcm[i : i+ws])
		if err != nil {
			return Result{}, fmt.Errorf("infer failed at sample %d: %w", i, err)
		}

		probs = append(probs, speechProb)
		currSample += ws

		if speechProb >= d.cfg.Threshold && tempEnd != 0 {
			tempEnd = 0
		}

		if speechProb >= d.cfg.Threshold && !triggered {
			triggered = true
			start := float64(currSample-ws-speechPadSamples) / float64(d.cfg.SampleRate)
			if start < 0 {
				start = 0
			}
			segments = append(segments, Segment{Start: start})
		}

		if speechProb < (d.cfg.Threshold-0.15) && triggered {
			if tempEnd == 0 {
				tempEnd = currSample
			}
			if currSample-tempEnd < minSilenceSamples {
				continue
			}
			end := float64(tempEnd+speechPadSamples) / float64(d.cfg.SampleRate)
			tempEnd = 0
			triggered = false
			if len(segments) > 0 {
				segments[len(segments)-1].End = end
			}
		}
	}

	// Close last open segment
	if triggered && len(segments) > 0 {
		end := float64(len(pcm)) / float64(d.cfg.SampleRate)
		segments[len(segments)-1].End = end
	}

	// Post-filter: remove segments shorter than MinSpeechDurationMs
	if d.cfg.MinSpeechDurationMs > 0 {
		minDur := float64(d.cfg.MinSpeechDurationMs) / 1000
		filtered := segments[:0]
		for _, s := range segments {
			if s.End-s.Start >= minDur {
				filtered = append(filtered, s)
			}
		}
		segments = filtered
	}

	return Result{Segments: segments, Probs: probs}, nil
}

func (d *Detector) Destroy() error {
	return d.inner.Destroy()
}
```

- [ ] **Step 2: Write the test**

`vad/vad_test.go`:
```go
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
	if got := Config{SampleRate: 16000}.windowSize(); got != 512 {
		t.Errorf("windowSize() = %d, want 512", got)
	}
	if got := Config{SampleRate: 8000}.windowSize(); got != 256 {
		t.Errorf("windowSize() = %d, want 256", got)
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
```

- [ ] **Step 3: Run the tests**

Run: `go test ./vad/ -v`
Expected: Unit tests pass (not calling into actual ONNX so the detection tests are skipped; structural tests pass).

---

### Task 3: `slice` Package — Audio Splitting

**Files:**
- Modify: `slice/slice.go`
- Create: `slice/slice_test.go`

- [ ] **Step 1: Implement split + WAV export**

Replace `slice/slice.go` with:

```go
package slice

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

func Split(pcm []float32, starts, ends []float64, sampleRate int) [][]float32 {
	var result [][]float32
	for i := range starts {
		startSample := int(math.Round(starts[i] * float64(sampleRate)))
		endSample := int(math.Round(ends[i] * float64(sampleRate)))
		if startSample < 0 {
			startSample = 0
		}
		if endSample > len(pcm) {
			endSample = len(pcm)
		}
		if startSample >= endSample {
			continue
		}
		seg := make([]float32, endSample-startSample)
		copy(seg, pcm[startSample:endSample])
		result = append(result, seg)
	}
	return result
}

func WriteWAV(filename string, pcm []float32, sampleRate int) error {
	if err := os.MkdirAll(dirname(filename), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	numSamples := len(pcm)
	bitsPerSample := 16
	numChannels := 1
	bytesPerSample := bitsPerSample / 8
	blockAlign := numChannels * bytesPerSample
	byteRate := sampleRate * blockAlign
	dataSize := numSamples * blockAlign
	fileSize := 44 + dataSize

	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(fileSize-8))
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16) // chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(header[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bitsPerSample))
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	if _, err := f.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	samples := make([]int16, numSamples)
	for i, s := range pcm {
		samples[i] = int16(math.MaxInt16 * s)
	}
	if err := binary.Write(f, binary.LittleEndian, samples); err != nil {
		return fmt.Errorf("write samples: %w", err)
	}

	return nil
}

func dirname(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}
```

- [ ] **Step 2: Write slice tests**

`slice/slice_test.go`:
```go
package slice

import (
	"math"
	"os"
	"testing"
)

func TestSplit(t *testing.T) {
	pcm := make([]float32, 16000) // 1 second of silence at 16kHz
	for i := 0; i < 16000; i++ {
		if i >= 2000 && i < 4000 {
			pcm[i] = 0.5 // voice segment 1
		}
		if i >= 8000 && i < 10000 {
			pcm[i] = 0.5 // voice segment 2
		}
	}

	segments := Split(pcm, []float64{0.125, 0.5}, []float64{0.25, 0.625}, 16000)
	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if len(segments[0]) != 2000 {
		t.Errorf("segment 0 length = %d, want 2000", len(segments[0]))
	}
	if len(segments[1]) != 2000 {
		t.Errorf("segment 1 length = %d, want 2000", len(segments[1]))
	}
}

func TestSplitBounds(t *testing.T) {
	pcm := make([]float32, 16000)
	segments := Split(pcm, []float64{-0.5, 0.5}, []float64{0.1, 1.5}, 16000)
	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if segments[0][0] != 0 {
		t.Error("first sample should be 0 (clamped)")
	}
}

func TestWriteWAV(t *testing.T) {
	pcm := make([]float32, 16000)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 16000))
	}

	tmpFile := t.TempDir() + "/test.wav"
	if err := WriteWAV(tmpFile, pcm, 16000); err != nil {
		t.Fatalf("WriteWAV failed: %v", err)
	}

	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	// WAV header (44) + 16000 samples * 2 bytes
	expectedSize := int64(44 + 16000*2)
	if info.Size() != expectedSize {
		t.Errorf("file size = %d, want %d", info.Size(), expectedSize)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./slice/ -v`
Expected: All tests pass

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: add slice package for audio splitting and WAV export"
```

---

### Task 4: `html` Package — Visualization Report

**Files:**
- Modify: `html/html.go`
- Create: `html/html_test.go`

- [ ] **Step 1: Implement HTML report generator**

Replace `html/html.go` with:

```go
package html

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

type ReportData struct {
	SampleRate   int
	Duration     float64
	PCM          []float32
	VADProbs     []float32
	Segments     []Segment
	SegmentFiles []string
	SegmentPCM   [][]float32
}

type Segment struct {
	Start float64
	End   float64
}

func Render(data ReportData, w io.Writer) error {
	var b strings.Builder

	b.WriteString(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Smart VAD Report</title>
<script src="https://unpkg.com/wavesurfer.js@7"></script>
<script src="https://unpkg.com/wavesurfer.js@7/dist/plugins/regions.min.js"></script>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#1a1a2e;color:#e0e0e0;padding:24px}
h1{font-size:24px;margin-bottom:8px}
h2{font-size:18px;margin-bottom:12px;color:#a0a0b0}
.summary{color:#888;margin-bottom:24px;font-size:14px}
.section{background:#16213e;border-radius:12px;padding:20px;margin-bottom:20px}
.waveform-container{width:100%;height:160px}
.chart-wrap{width:100%;height:120px;position:relative;background:#0f3460;border-radius:8px}
.chart-wrap canvas{width:100%;height:100%}
.segments-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:12px}
.segment-card{background:#0f3460;border-radius:8px;padding:12px}
.segment-card h3{font-size:14px;margin-bottom:8px}
.segment-card audio{width:100%}
.label{color:#888;font-size:12px}
</style>
</head>
<body>
<h1>Smart VAD Report</h1>
<p class="summary">Sample Rate: `)
	fmt.Fprintf(&b, "%d", data.SampleRate)
	b.WriteString(` Hz | Duration: `)
	fmt.Fprintf(&b, "%.2f", data.Duration)
	b.WriteString(`s | Segments: `)
	fmt.Fprintf(&b, "%d", len(data.Segments))
	b.WriteString(`</p>

<div class="section">
<h2>Full Waveform</h2>
<div id="waveform" class="waveform-container"></div>
</div>

<div class="section">
<h2>VAD Confidence</h2>
<div class="chart-wrap"><canvas id="vadCanvas"></canvas></div>
</div>

<div class="section">
<h2>Sliced Segments (`)
	fmt.Fprintf(&b, "%d", len(data.Segments))
	b.WriteString(`)</h2>
<div id="segments" class="segments-grid"></div>
</div>

<script>
const waveformData = `)
	b.WriteString(encodeFloat32Array(downsample(data.PCM, 2000)))
	b.WriteString(`;
const vadProbs = `)
	b.WriteString(encodeFloat32Array(data.VADProbs))
	b.WriteString(`;
const segments = `)
	b.WriteString(encodeSegments(data.Segments))
	b.WriteString(`;
const segmentAudios = `)
	b.WriteString(encodeSegmentAudios(data.SegmentPCM, data.SampleRate))
	b.WriteString(`;
const segmentFiles = `)
	b.WriteString(encodeStringArray(data.SegmentFiles))
	b.WriteString(`;

function pcmToWav(samples, sr) {
  const n = samples.length, buf = new ArrayBuffer(44 + n * 2), v = new DataView(buf);
  const w = (o, s) => { for (let i = 0; i < s.length; i++) v.setUint8(o + i, s.charCodeAt(i)); };
  w(0,'RIFF'); v.setUint32(4,36+n*2,true); w(8,'WAVE'); w(12,'fmt ');
  v.setUint32(16,16,true); v.setUint16(20,1,true); v.setUint16(22,1,true);
  v.setUint32(24,sr,true); v.setUint32(28,sr*2,true); v.setUint16(32,2,true); v.setUint16(34,16,true);
  w(36,'data'); v.setUint32(40,n*2,true);
  for (let i = 0; i < n; i++) {
    const s = Math.max(-1,Math.min(1,samples[i]));
    v.setInt16(44+i*2, s<0 ? s*0x8000 : s*0x7FFF, true);
  }
  return new Blob([buf], {type:'audio/wav'});
}

const ws = WaveSurfer.create({
  container:'#waveform', waveColor:'#4a9eff', progressColor:'#2d7de6',
  cursorColor:'#ff6b6b', barWidth:2, barGap:1, barRadius:2, height:160
});
const blob = pcmToWav(waveformData, `)
	fmt.Fprintf(&b, "%d", data.SampleRate)
	b.WriteString(`);
ws.load(URL.createObjectURL(blob));
ws.on('ready', () => {
  const r = ws.registerPlugin(WaveSurfer.Regions.create());
  segments.forEach(s => r.addRegion({start:s.start,end:s.end,color:'rgba(46,204,113,0.3)',drag:false,resize:false}));
});

function drawVAD() {
  const c = document.getElementById('vadCanvas'), p = c.parentElement;
  c.width = p.clientWidth*2; c.height = p.clientHeight*2;
  c.style.width = p.clientWidth+'px'; c.style.height = p.clientHeight+'px';
  const ctx = c.getContext('2d'); ctx.scale(2,2);
  const W = p.clientWidth, H = p.clientHeight, pad = {t:10,b:20,l:40,r:20};
  const cw = W-pad.l-pad.r, ch = H-pad.t-pad.b;
  ctx.fillStyle='#0f3460'; ctx.fillRect(0,0,W,H);
  ctx.strokeStyle='rgba(255,255,255,0.1)'; ctx.lineWidth=1;
  for(let i=0;i<=4;i++){let y=pad.t+ch/4*i; ctx.beginPath(); ctx.moveTo(pad.l,y); ctx.lineTo(W-pad.r,y); ctx.stroke();
    ctx.fillStyle='#888'; ctx.font='10px sans-serif'; ctx.textAlign='right'; ctx.fillText((1-i*0.25).toFixed(2),pad.l-5,y+4);}
  let ty=pad.t+ch*(1-0.5); ctx.strokeStyle='rgba(255,107,107,0.5)'; ctx.setLineDash([4,4]);
  ctx.beginPath(); ctx.moveTo(pad.l,ty); ctx.lineTo(W-pad.r,ty); ctx.stroke(); ctx.setLineDash([]);
  ctx.fillStyle='#ff6b6b'; ctx.font='10px sans-serif'; ctx.textAlign='left'; ctx.fillText('threshold (0.5)',W-pad.r-80,ty-4);
  if(vadProbs.length<2)return;
  ctx.strokeStyle='#2ecc71'; ctx.lineWidth=2; ctx.beginPath();
  for(let i=0;i<vadProbs.length;i++){let x=pad.l+cw*i/(vadProbs.length-1),y=pad.t+ch*(1-vadProbs[i]); i===0?ctx.moveTo(x,y):ctx.lineTo(x,y);}
  ctx.stroke(); ctx.lineTo(pad.l+cw,pad.t+ch); ctx.lineTo(pad.l,pad.t+ch); ctx.closePath(); ctx.fillStyle='rgba(46,204,113,0.1)'; ctx.fill();
  ctx.fillStyle='#888'; ctx.font='10px sans-serif'; ctx.textAlign='center';
  let dur = waveformData.length/`)
	fmt.Fprintf(&b, "%d", data.SampleRate)
	b.WriteString(`;
  for(let i=0;i<=5;i++){let x=pad.l+cw/5*i; ctx.fillText((dur/5*i).toFixed(1)+'s',x,H-pad.b+14);}
}
drawVAD(); window.addEventListener('resize',drawVAD);

const grid = document.getElementById('segments');
segments.forEach((s,i) => {
  const card = document.createElement('div'); card.className = 'segment-card';
  const label = 'Segment ' + (i+1);
  card.innerHTML = '<h3>' + label + ' <span class="label">(' + s.start.toFixed(2) + 's - ' + s.end.toFixed(2) + 's)</span></h3>'
    + (segmentAudios[i] ? '<audio controls src="' + segmentAudios[i] + '"></audio>' : '')
    + '<p class="label">' + (segmentFiles[i] || 'seg-' + String(i+1).padStart(3,'0') + '.wav') + '</p>';
  grid.appendChild(card);
});
</script>
</body>
</html>`)

	_, err := io.WriteString(w, b.String())
	return err
}

func downsample(pcm []float32, target int) []float32 {
	if len(pcm) <= target {
		return pcm
	}
	stride := len(pcm) / target
	down := make([]float32, target)
	for i := range down {
		down[i] = pcm[i*stride]
	}
	return down
}

func encodeFloat32Array(data []float32) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, v := range data {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.6f", v)
	}
	b.WriteByte(']')
	return b.String()
}

func encodeStringArray(data []string) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range data {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}

func encodeSegments(segments []Segment) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range segments {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"start":%.4f,"end":%.4f}`, s.Start, s.End)
	}
	b.WriteByte(']')
	return b.String()
}

func encodeSegmentAudios(segments [][]float32, sampleRate int) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte(',')
		}
		wavData := pcmToWAVBytes(seg, sampleRate)
		b64 := base64.StdEncoding.EncodeToString(wavData)
		fmt.Fprintf(&b, `"data:audio/wav;base64,%s"`, b64)
	}
	b.WriteByte(']')
	return b.String()
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
		put16(buf[44+i*2:], uint16(int16(math.MaxInt16*s)))
	}
	return buf
}

func put16(buf []byte, v uint16) { binary.LittleEndian.PutUint16(buf, v) }
func put32(buf []byte, v uint32) { binary.LittleEndian.PutUint32(buf, v) }
```

- [ ] **Step 2: Write html test**

`html/html_test.go`:
```go
package html

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	data := ReportData{
		SampleRate: 16000,
		Duration:   1.0,
		PCM:        make([]float32, 16000),
		VADProbs:   []float32{0.1, 0.2, 0.8, 0.9, 0.7, 0.3, 0.1},
		Segments:   []Segment{{Start: 0.2, End: 0.6}},
		SegmentFiles: []string{"seg-001.wav"},
		SegmentPCM:   [][]float32{make([]float32, 6400)},
	}

	// Fill some PCM data
	for i := range data.PCM {
		data.PCM[i] = float32(i) / 16000
	}

	var buf bytes.Buffer
	if err := Render(data, &buf); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Smart VAD Report") {
		t.Error("Missing title")
	}
	if !strings.Contains(output, "wavesurfer.js") {
		t.Error("Missing wavesurfer.js CDN link")
	}
	if !strings.Contains(output, "data:audio/wav;base64") {
		t.Error("Missing base64 audio data")
	}
	if !strings.Contains(output, "vadCanvas") {
		t.Error("Missing VAD chart canvas")
	}
}

func TestRenderEmptySegments(t *testing.T) {
	data := ReportData{
		SampleRate: 16000,
		Duration:   0.5,
		PCM:        make([]float32, 8000),
		VADProbs:   []float32{0.1, 0.1},
	}

	var buf bytes.Buffer
	if err := Render(data, &buf); err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if len(buf.String()) == 0 {
		t.Error("expected non-empty output")
	}
}

func TestRenderWritesFile(t *testing.T) {
	data := ReportData{
		SampleRate: 16000,
		Duration:   1.0,
		PCM:        make([]float32, 16000),
	}

	tmpFile := t.TempDir() + "/report.html"
	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := Render(data, f); err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("expected non-empty file")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./html/ -v`
Expected: All tests pass

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: add html package for VAD report visualization"
```

---

### Task 5: CLI Entry Point

**Files:**
- Modify: `cmd/demo/main.go`
- Create: `cmd/demo/main_test.go`

- [ ] **Step 1: Write CLI main.go**

Replace `cmd/demo/main.go` with:

```go
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/go-audio/wav"
	"github.com/liushunshun/smart-vad/html"
	"github.com/liushunshun/smart-vad/slice"
	"github.com/liushunshun/smart-vad/vad"
)

func main() {
	input := flag.String("input", "", "input WAV file path (16kHz mono)")
	model := flag.String("model", "", "path to silero_vad.onnx model")
	output := flag.String("output", "./output", "output directory")
	threshold := flag.Float64("threshold", 0.5, "VAD threshold")
	minSilence := flag.Int("min-silence", 100, "min silence duration in ms")
	minSpeech := flag.Int("min-speech", 100, "min speech duration in ms")
	padMs := flag.Int("pad", 30, "padding around segments in ms")
	flag.Parse()

	if *input == "" || *model == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Open WAV file
	f, err := os.Open(*input)
	if err != nil {
		log.Fatalf("open input: %v", err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		log.Fatalf("invalid WAV file")
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		log.Fatalf("read PCM: %v", err)
	}

	pcm := buf.AsFloat32Buffer().Data
	sampleRate := dec.SampleRate

	if sampleRate != 16000 && sampleRate != 8000 {
		log.Fatalf("unsupported sample rate: %d (use 8000 or 16000)", sampleRate)
	}

	log.Printf("Loaded: %s (%d Hz, %d samples, %.2fs)",
		*input, sampleRate, len(pcm), float64(len(pcm))/float64(sampleRate))

	// VAD detection
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

	result, err := detector.Detect(pcm)
	if err != nil {
		log.Fatalf("detect: %v", err)
	}

	log.Printf("Detected %d speech segments", len(result.Segments))
	for _, s := range result.Segments {
		log.Printf("  segment: %.2fs - %.2fs (%.2fs)", s.Start, s.End, s.End-s.Start)
	}

	if len(result.Segments) == 0 {
		log.Println("No speech detected, generating report without segments")
	}

	// Slice audio
	starts := make([]float64, len(result.Segments))
	ends := make([]float64, len(result.Segments))
	for i, s := range result.Segments {
		starts[i] = s.Start
		ends[i] = s.End
	}
	segPCMs := slice.Split(pcm, starts, ends, sampleRate)

	// Create output directory
	outDir := *output
	segDir := filepath.Join(outDir, "segments")
	if err := os.MkdirAll(segDir, 0755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	// Write segment files
	segFiles := make([]string, len(segPCMs))
	for i, seg := range segPCMs {
		filename := filepath.Join(segDir, fmt.Sprintf("seg-%03d.wav", i+1))
		if err := slice.WriteWAV(filename, seg, sampleRate); err != nil {
			log.Fatalf("write segment %d: %v", i+1, err)
		}
		segFiles[i] = filename
		log.Printf("  wrote: %s", filename)
	}

	// Generate HTML report
	reportPath := filepath.Join(outDir, "report.html")
	rf, err := os.Create(reportPath)
	if err != nil {
		log.Fatalf("create report: %v", err)
	}
	defer rf.Close()

	duration := float64(len(pcm)) / float64(sampleRate)
	htmlSegments := make([]html.Segment, len(result.Segments))
	for i, s := range result.Segments {
		htmlSegments[i] = html.Segment{Start: s.Start, End: s.End}
	}

	if err := html.Render(html.ReportData{
		SampleRate:   sampleRate,
		Duration:     duration,
		PCM:          pcm,
		VADProbs:     result.Probs,
		Segments:     htmlSegments,
		SegmentFiles: segFiles,
		SegmentPCM:   segPCMs,
	}, rf); err != nil {
		log.Fatalf("render report: %v", err)
	}

	log.Printf("Report: %s", reportPath)
}
```

- [ ] **Step 2: Update go.mod with dependencies**

Run:
```bash
go get github.com/go-audio/wav@v1.1.0
go get github.com/streamer45/silero-vad-go@v0.2.1
go mod tidy
```
Expected: `go.mod` and `go.sum` updated with dependencies.

- [ ] **Step 3: Build to verify compilation**

Run: `go build ./...`
Expected: Compiles successfully (CGO + ONNX Runtime required on system)

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: add CLI demo with full VAD slicing pipeline"
```

---

### Self-Review

After writing the plan, verify against spec:

1. **Spec coverage:**
   - `vad` package with Config/Segment/Result/Detector → Task 2 ✓
   - `slice` package with Split/WriteWAV → Task 3 ✓
   - `html` package with ReportData/Render → Task 4 ✓
   - CLI with flags → Task 5 ✓
   - Per-frame VAD probabilities collected in vad.Detect → Task 2 ✓
   - HTML: waveform + VAD chart + segment cards → Task 4 ✓
   - Self-contained HTML with wavesurfer.js CDN → Task 4 ✓
   - Segment WAV export → Task 3 ✓
   - MinSpeechDurationMs post-filter → Task 2 ✓
1. **Placeholder scan:** All code is complete, no TBD/TODO.
1. **Type consistency:**
   - Task 2 defines `vad.Segment{Start, End float64}` and `vad.Result{Segments, Probs}`
   - Task 3 uses `starts []float64, ends []float64` as input — matches vad.Segment.Start/.End
   - Task 4 defines its own `html.Segment{Start, End float64}` — separate from vad.Segment ✓
   - Task 5 bridges vad.Segment → html.Segment correctly ✓
