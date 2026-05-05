package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-audio/wav"
	"github.com/liushunshun/smart-vad/cmd/internal/check"
	"github.com/liushunshun/smart-vad/html"
	"github.com/liushunshun/smart-vad/logger"
	"github.com/liushunshun/smart-vad/slice"
	"github.com/liushunshun/smart-vad/vad"
)

func main() {
	input := flag.String("input", "", "input WAV file path (16kHz mono)")
	model := flag.String("model", "", "path to silero_vad.onnx model")
	output := flag.String("output", "./output", "output directory")
	threshold := flag.Float64("threshold", 0.3, "VAD threshold")
	minSilence := flag.Int("min-silence", 100, "min silence duration in ms")
	minSpeech := flag.Int("min-speech", 100, "min speech duration in ms")
	padMs := flag.Int("pad", 30, "padding around segments in ms")
	targetSR := flag.Int("samplerate", 16000, "target sample rate (16000 or 8000)")
	adaptive := flag.Bool("adaptive", false, "enable adaptive VAD (dynamic baseline threshold)")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	logger.Init(*debug)

	if *input == "" || *model == "" {
		flag.Usage()
		os.Exit(1)
	}

	check.ModelExists(*model)

	f, err := os.Open(*input)
	if err != nil {
		logger.Fatal("open input failed", "error", err)
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		logger.Fatal("invalid WAV file")
	}

	buf, err := dec.FullPCMBuffer()
	if err != nil {
		logger.Fatal("read PCM failed", "error", err)
	}

	pcm := buf.AsFloat32Buffer().Data
	sampleRate := int(dec.SampleRate)

	if *targetSR != 16000 && *targetSR != 8000 {
		logger.Fatal("unsupported target sample rate", "rate", *targetSR)
	}

	if sampleRate != *targetSR {
		logger.Info("resampling audio", "from", sampleRate, "to", *targetSR)
		pcm = slice.Resample(pcm, sampleRate, *targetSR)
		sampleRate = *targetSR
	}

	if sampleRate != 16000 && sampleRate != 8000 {
		logger.Fatal("unsupported sample rate", "rate", sampleRate)
	}

	logger.Info("loaded input",
		"path", *input,
		"rate", sampleRate,
		"samples", len(pcm),
		"duration_sec", float64(len(pcm))/float64(sampleRate))

	var result vad.Result
	var filteredSegments []vad.Segment
	var adaptDetector *vad.AdaptiveDetector

	if *adaptive {
		logger.Info("adaptive VAD enabled")
		adaptDetector, err = vad.NewAdaptiveDetector(vad.AdaptiveConfig{
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
			logger.Fatal("create adaptive detector failed", "error", err)
		}
		defer adaptDetector.Destroy()

		result, err = adaptDetector.Detect(pcm)
		if err != nil {
			logger.Fatal("detection failed", "error", err)
		}
		filteredSegments = adaptDetector.FilteredSegments()
		logger.Debug("adaptive VAD params",
			"baseline_db", adaptDetector.BaselineDB(),
			"energy_offset_db", adaptDetector.EnergyOffsetDB(),
			"threshold", adaptDetector.Inner().GetThreshold())
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
			logger.Fatal("create detector failed", "error", err)
		}
		defer detector.Destroy()

		result, err = detector.Detect(pcm)
		if err != nil {
			logger.Fatal("detection failed", "error", err)
		}
	}

	logger.Info("detected speech segments",
		"count", len(result.Segments),
		"filtered", len(filteredSegments))
	for _, s := range result.Segments {
		logger.Debug("segment",
			"start", s.Start,
			"end", s.End,
			"duration", s.End-s.Start)
	}

	if len(result.Segments) == 0 {
		logger.Warn("no speech detected, generating report without segments")
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
		logger.Fatal("create output dir failed", "error", err)
	}

	segFiles := make([]string, len(segPCMs))
	for i, seg := range segPCMs {
		filename := filepath.Join(segDir, fmt.Sprintf("seg-%03d.wav", i+1))
		if err := slice.WriteWAV(filename, seg, sampleRate); err != nil {
			logger.Fatal("write segment failed", "index", i+1, "error", err)
		}
		segFiles[i] = filename
		logger.Debug("wrote segment file", "path", filename)
	}

	var filteredPCMs [][]float32
	if len(filteredSegments) > 0 {
		fStarts := make([]float64, len(filteredSegments))
		fEnds := make([]float64, len(filteredSegments))
		for i, s := range filteredSegments {
			fStarts[i] = s.Start
			fEnds[i] = s.End
		}
		filteredPCMs = slice.Split(pcm, fStarts, fEnds, sampleRate)
	}

	reportPath := filepath.Join(outDir, "report.html")
	rf, err := os.Create(reportPath)
	if err != nil {
		logger.Fatal("create report file failed", "error", err)
	}
	defer rf.Close()

	duration := float64(len(pcm)) / float64(sampleRate)
	htmlSegments := make([]html.Segment, len(result.Segments))
	for i, s := range result.Segments {
		htmlSegments[i] = html.Segment{Start: s.Start, End: s.End}
	}

	htmlFiltered := make([]html.Segment, len(filteredSegments))
	for i, s := range filteredSegments {
		htmlFiltered[i] = html.Segment{Start: s.Start, End: s.End}
	}

	var baselineDB, energyOffsetDB, windowDuration, noiseFloorFrac float64
	if adaptDetector != nil {
		baselineDB = adaptDetector.BaselineDB()
		energyOffsetDB = adaptDetector.EnergyOffsetDB()
		windowDuration = 30
		noiseFloorFrac = 0.1
	}

	if err := html.Render(html.ReportData{
		SampleRate:         sampleRate,
		Duration:           duration,
		PCM:                pcm,
		VADProbs:           result.Probs,
		Segments:           htmlSegments,
		FilteredSegments:   htmlFiltered,
		SegmentFiles:       segFiles,
		SegmentPCM:         segPCMs,
		FilteredSegmentPCM: filteredPCMs,
		Threshold:          float32(*threshold),
		MinSpeechMs:        *minSpeech,
		MinSilenceMs:       *minSilence,
		SpeechPadMs:        *padMs,
		WindowDuration:     windowDuration,
		NoiseFloorFrac:     noiseFloorFrac,
		EnergyOffsetDB:     energyOffsetDB,
		BaselineDB:         baselineDB,
	}, rf); err != nil {
		logger.Fatal("render report failed", "error", err)
	}

	logger.Info("report generated", "path", reportPath)
}
