# VAD Chart Segment Tooltip

## Summary

Add hover tooltip on the VAD Confidence chart to display per-segment VAD parameters (confidence stats, RMS energy, adaptive parameters) for both regular and energy-filtered segments.

## Data Model Changes

### Go Backend

**html.Segment** — add RMS field:
```go
type Segment struct {
    Start float64
    End   float64
    RMS   float64 // RMS energy in dB, computed per segment
}
```

**reportTmplData** — add adaptive mode global parameters:
```go
type reportTmplData struct {
    // ... existing fields ...
    AdaptiveVAD    bool    // whether adaptive mode was used
    BaselineDB     float64 // noise floor baseline (dB)
    EnergyOffsetDB float64 // energy offset (dB)
}
```

### JSON Data Passed to Frontend

Segments JSON now includes RMS:
```json
{"start":1.23,"end":3.45,"rms":-18.5}
```

New global variables:
- `adaptiveVAD` (bool)
- `baselineDB` (float64)
- `energyOffsetDB` (float64)

## Changes by File

### html/html.go
1. Add `RMS float64` to `Segment` struct
2. Add `AdaptiveVAD bool`, `BaselineDB float64`, `EnergyOffsetDB float64` to `reportTmplData`
3. Update `encodeSegments` to emit RMS
4. Wire new fields in `Render()` from `ReportData`

### cmd/server/main.go
1. Compute RMS for each segment via `vad.RMS()` before building `htmlSegments`/`htmlFiltered`
2. Pass adaptive params to `html.ReportData`

### template/report.html

**New state:**
- `segments` now includes `rms`, `filteredSegments` includes `rms`

**Canvas interaction:**
- Listen `mousemove` on `vadCanvas.parentElement` (the chart-wrap)
- Compute which segment (if any) the cursor is over based on x-position → time mapping
- Show/hide a positioned tooltip `<div>` 

**Tooltip content per segment:**
```
Segment N
Time: 1.23s – 3.45s (2.22s)
Confidence: avg 0.87 | min 0.62 | max 0.99
RMS: -18.5 dB
Threshold: 0.70 | Baseline: -42.3 dB
```

Confidence stats computed on frontend by slicing `vadProbs` array at the segment's frame range.

**Adaptive mode** — additionally shows: noise floor baseline, energy offset.

## Interaction Design

- Tooltip appears on `mousemove` over the `.chart-wrap` area
- The x-coordinate is mapped to time; if time falls within a segment's [start, end], show that segment's tooltip
- Tooltip positioned near cursor (with offset to avoid blocking the view)
- Tooltip disappears on `mouseleave`
- Threshold line (0.5) hover is not a segment

## Edge Cases

- Overlapping segments: not possible in current VAD logic, but if multiple match, show the first one
- No segment under cursor: hide tooltip
- Very short segments: tooltip should still show if cursor is within bounds
- Resize: tooltip position recalculated on each mousemove (already handled)
- Adaptive mode not active: hide adaptive-specific fields in tooltip
