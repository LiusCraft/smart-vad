# VAD Chart Segment Tooltip - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add hover tooltip on the VAD Confidence chart showing per-segment VAD parameters

**Architecture:** Add RMS field to html.Segment and adaptive params to html.ReportData, compute RMS per segment in server handler, cache baseline/energyOffset in AdaptiveDetector, then add canvas mouse event handling + tooltip rendering in frontend template.

**Tech Stack:** Go (html/template), Canvas 2D API

---

### Task 1: Extend Go data model (html/html.go)

**Files:**
- Modify: html/html.go

- [ ] **Step 1: Add RMS to html.Segment**

```go
type Segment struct {
    Start float64
    End   float64
    RMS   float64
}
```

- [ ] **Step 2: Add fields to ReportData and reportTmplData**

Add to ReportData (after HasResults):

```go
    AdaptiveVAD    bool
    BaselineDB     float64
    EnergyOffsetDB float64
```

Add to reportTmplData (after FilteredCount):

```go
    AdaptiveVAD    bool
    BaselineDB     float64
    EnergyOffsetDB float64
```

- [ ] **Step 3: Wire new fields in Render()**

After `tmplData.FilteredCount = len(data.FilteredSegments)`:

```go
    tmplData.AdaptiveVAD = data.AdaptiveVAD
    tmplData.BaselineDB = data.BaselineDB
    tmplData.EnergyOffsetDB = data.EnergyOffsetDB
```

- [ ] **Step 4: Update encodeSegments to emit RMS**

```go
func encodeSegments(segments []Segment) string {
    var b bytes.Buffer
    b.WriteByte('[')
    for i, s := range segments {
        if i > 0 {
            b.WriteByte(',')
        }
        fmt.Fprintf(&b, `{"start":%.4f,"end":%.4f,"rms":%.2f}`, s.Start, s.End, s.RMS)
    }
    b.WriteByte(']')
    return b.String()
}
```

- [ ] **Step 5: Build and verify**

Run: `go build ./html/`
Expected: no errors.

---

### Task 2: Cache adaptive params in vad/adaptive.go

**Files:**
- Modify: vad/adaptive.go

- [ ] **Step 1: Add cached fields to AdaptiveDetector struct**

Add after `rawSegments []Segment`:

```go
    baselineDB     float64
    energyOffsetDB float64
```

- [ ] **Step 2: Cache values in Detect() after computeBaseline**

After `baseline := a.computeBaseline()`:

```go
    a.baselineDB = baseline
    a.energyOffsetDB = a.cfg.EnergyOffsetDB
```

- [ ] **Step 3: Add getter methods**

```go
func (a *AdaptiveDetector) BaselineDB() float64 {
    return a.baselineDB
}

func (a *AdaptiveDetector) EnergyOffsetDB() float64 {
    return a.energyOffsetDB
}
```

- [ ] **Step 4: Build and verify**

Run: `go build ./vad/`
Expected: no errors.

---

### Task 3: Compute RMS and pass params in cmd/server/main.go

**Files:**
- Modify: cmd/server/main.go

- [ ] **Step 1: Declare adaptDetector outside if block**

Before the adaptive/normal if-else, add:

```go
    var adaptDetector *vad.AdaptiveDetector
```

Inside the adaptive block, change `adaptDetector, err :=` to `adaptDetector, err =` to use outer scope.

- [ ] **Step 2: Compute RMS for each segment**

Replace the htmlSegments loop:

```go
    htmlSegments := make([]html.Segment, len(result.Segments))
    for i, s := range result.Segments {
        rms := 0.0
        if i < len(segPCMs) {
            rms = vad.RMS(segPCMs[i])
        }
        htmlSegments[i] = html.Segment{Start: s.Start, End: s.End, RMS: rms}
    }
```

Replace the htmlFiltered loop:

```go
    htmlFiltered := make([]html.Segment, len(filteredSegments))
    for i, s := range filteredSegments {
        rms := 0.0
        if i < len(filteredPCMs) {
            rms = vad.RMS(filteredPCMs[i])
        }
        htmlFiltered[i] = html.Segment{Start: s.Start, End: s.End, RMS: rms}
    }
```

- [ ] **Step 3: Pass adaptive params**

After the if-else block, add:

```go
    var baselineDB, energyOffsetDB float64
    if adaptDetector != nil {
        baselineDB = adaptDetector.BaselineDB()
        energyOffsetDB = adaptDetector.EnergyOffsetDB()
    }
```

In ReportData literal, add:

```go
        AdaptiveVAD:    r.FormValue("adaptive") == "true",
        BaselineDB:     baselineDB,
        EnergyOffsetDB: energyOffsetDB,
```

- [ ] **Step 4: Build and verify**

Run: `go build ./cmd/server/`
Expected: no errors.

---

### Task 4: Add canvas hover tooltip in template/report.html

**Files:**
- Modify: template/report.html

- [ ] **Step 1: Add new template variables**

After `const windowMs = {{.WindowMs}};`, add:

```javascript
const adaptiveVAD = {{.AdaptiveVAD}};
const baselineDB = {{.BaselineDB}};
const energyOffsetDB = {{.EnergyOffsetDB}};
```

- [ ] **Step 2: Add tooltip CSS**

Add to style block:

```css
#vadTooltip{position:absolute;background:rgba(15,52,96,0.95);border:1px solid #4a9eff;border-radius:8px;padding:10px 14px;font-size:12px;line-height:1.6;pointer-events:none;display:none;z-index:10;white-space:nowrap;backdrop-filter:blur(4px)}
#vadTooltip .tt-title{color:#4a9eff;font-weight:600;font-size:13px;margin-bottom:4px}
#vadTooltip .tt-row{color:#ccc}
#vadTooltip .tt-label{color:#888}
```

- [ ] **Step 3: Add tooltip HTML element**

After the vadCursor div, add:

```html
<div id="vadTooltip"></div>
```

- [ ] **Step 4: Add helper functions for confidence stats and tooltip**

Before drawVAD(), add:

```javascript
function segmentConfidenceStats(seg) {
  const frameSec = windowMs / 1000;
  const startFrame = Math.floor(seg.start / frameSec);
  const endFrame = Math.min(vadProbs.length - 1, Math.ceil(seg.end / frameSec));
  const slice = vadProbs.slice(startFrame, endFrame + 1);
  if (slice.length === 0) return null;
  const sum = slice.reduce((a, b) => a + b, 0);
  return {
    avg: sum / slice.length,
    min: Math.min(...slice),
    max: Math.max(...slice)
  };
}

function showSegmentTooltip(seg, isFiltered, x, y) {
  const el = document.getElementById('vadTooltip');
  const stats = segmentConfidenceStats(seg);
  const idx = isFiltered ? filteredSegments.indexOf(seg) : segments.indexOf(seg);
  const label = isFiltered ? 'Filtered ' + (idx + 1) : 'Segment ' + (idx + 1);
  let html = '<div class="tt-title">' + label + '</div>';
  html += '<div class="tt-row"><span class="tt-label">Time:</span> ' + seg.start.toFixed(2) + 's - ' + seg.end.toFixed(2) + 's (' + (seg.end - seg.start).toFixed(2) + 's)</div>';
  if (stats) {
    html += '<div class="tt-row"><span class="tt-label">Confidence:</span> avg ' + stats.avg.toFixed(3) + ' | min ' + stats.min.toFixed(3) + ' | max ' + stats.max.toFixed(3) + '</div>';
  }
  html += '<div class="tt-row"><span class="tt-label">RMS:</span> ' + seg.rms.toFixed(1) + ' dB</div>';
  if (adaptiveVAD) {
    html += '<div class="tt-row"><span class="tt-label">Baseline:</span> ' + baselineDB.toFixed(1) + ' dB | <span class="tt-label">EnergyOffset:</span> ' + energyOffsetDB.toFixed(1) + ' dB</div>';
  }
  if (isFiltered) {
    html += '<div class="tt-row" style="color:#ff6b6b">Discarded by energy filter</div>';
  }
  el.innerHTML = html;
  const p = el.parentElement;
  el.style.left = Math.min(x + 12, p.clientWidth - el.offsetWidth - 4) + 'px';
  el.style.top = Math.max(4, y - el.offsetHeight - 8) + 'px';
  el.style.display = 'block';
}

function hideSegmentTooltip() {
  document.getElementById('vadTooltip').style.display = 'none';
}
```

- [ ] **Step 5: Add mouse event listeners on chart-wrap**

After the ensureVAD/resize handler block, add:

```javascript
(function() {
  const wrap = document.querySelector('.chart-wrap');
  const dur = pcmLen / sr;

  function findSegmentAtTime(t) {
    for (const seg of segments) {
      if (t >= seg.start && t <= seg.end) return { seg, filtered: false };
    }
    for (const seg of filteredSegments) {
      if (t >= seg.start && t <= seg.end) return { seg, filtered: true };
    }
    return null;
  }

  wrap.addEventListener('mousemove', function(e) {
    const rect = wrap.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;
    const pad = { l: 40, r: 16 };
    const cw = rect.width - pad.l - pad.r;
    if (mx < pad.l || mx > rect.width - pad.r) { hideSegmentTooltip(); return; }
    const t = (mx - pad.l) / cw * dur;
    const hit = findSegmentAtTime(t);
    if (hit) {
      showSegmentTooltip(hit.seg, hit.filtered, mx, my);
    } else {
      hideSegmentTooltip();
    }
  });

  wrap.addEventListener('mouseleave', hideSegmentTooltip);
})();
```

- [ ] **Step 6: Verify the build works**

Run: `go build ./...`
Expected: no errors.

---

### Self-Review Checklist

- [ ] Spec coverage: Every requirement covered (per-segment RMS, adaptive params, tooltip rendering)
- [ ] No placeholder content
- [ ] Type consistency across tasks
