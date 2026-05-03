package html

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	data := ReportData{
		SampleRate:   16000,
		Duration:     1.0,
		PCM:          make([]float32, 16000),
		VADProbs:     []float32{0.1, 0.2, 0.8, 0.9, 0.7, 0.3, 0.1},
		Segments:     []Segment{{Start: 0.2, End: 0.6}},
		SegmentFiles: []string{"seg-001.wav"},
		SegmentPCM:   [][]float32{make([]float32, 6400)},
		HasResults:   true,
	}

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
		HasResults: true,
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
		HasResults: true,
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
