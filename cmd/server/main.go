package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-audio/wav"
	"github.com/liushunshun/smart-vad/html"
	"github.com/liushunshun/smart-vad/slice"
	"github.com/liushunshun/smart-vad/vad"
)

var modelPath string

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
	mux.HandleFunc("/analyze", handleAnalyze)

	log.Printf("Starting server on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if err := html.Render(html.ReportData{}, &buf); err != nil {
		http.Error(w, "render failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

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
