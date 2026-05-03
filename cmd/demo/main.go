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
	adaptive := flag.Bool("adaptive", false, "enable adaptive VAD (dynamic baseline threshold)")
	flag.Parse()

	if *input == "" || *model == "" {
		flag.Usage()
		os.Exit(1)
	}

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
	sampleRate := int(dec.SampleRate)

	if sampleRate != 16000 && sampleRate != 8000 {
		log.Fatalf("unsupported sample rate: %d (use 8000 or 16000)", sampleRate)
	}

	log.Printf("Loaded: %s (%d Hz, %d samples, %.2fs)",
		*input, sampleRate, len(pcm), float64(len(pcm))/float64(sampleRate))

	var result vad.Result

	if *adaptive {
		log.Print("Adaptive VAD enabled")
		adaptDetector, err := vad.NewAdaptiveDetector(vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            *model,
				SampleRate:           sampleRate,
				Threshold:            float32(*threshold),
				MinSilenceDurationMs: *minSilence,
				MinSpeechDurationMs:  *minSpeech,
				SpeechPadMs:          *padMs,
			},
		})
		if err != nil {
			log.Fatalf("create adaptive detector: %v", err)
		}
		defer adaptDetector.Destroy()

		result, err = adaptDetector.Detect(pcm)
		if err != nil {
			log.Fatalf("detect: %v", err)
		}
	} else {
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

		result, err = detector.Detect(pcm)
		if err != nil {
			log.Fatalf("detect: %v", err)
		}
	}

	log.Printf("Detected %d speech segments", len(result.Segments))
	for _, s := range result.Segments {
		log.Printf("  segment: %.2fs - %.2fs (%.2fs)", s.Start, s.End, s.End-s.Start)
	}

	if len(result.Segments) == 0 {
		log.Println("No speech detected, generating report without segments")
	}

	starts := make([]float64, len(result.Segments))
	ends := make([]float64, len(result.Segments))
	for i, s := range result.Segments {
		starts[i] = s.Start
		ends[i] = s.End
	}
	segPCMs := slice.Split(pcm, starts, ends, sampleRate)

	outDir := *output
	segDir := filepath.Join(outDir, "segments")
	if err := os.MkdirAll(segDir, 0755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	segFiles := make([]string, len(segPCMs))
	for i, seg := range segPCMs {
		filename := filepath.Join(segDir, fmt.Sprintf("seg-%03d.wav", i+1))
		if err := slice.WriteWAV(filename, seg, sampleRate); err != nil {
			log.Fatalf("write segment %d: %v", i+1, err)
		}
		segFiles[i] = filename
		log.Printf("  wrote: %s", filename)
	}

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
