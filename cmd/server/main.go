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
	"strconv"
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
		fmt.Fprintf(os.Stderr, `Error: model file not found: %s

The server needs the Silero VAD model (silero_vad.onnx) to run.

How to resolve:

  1. Run the setup script (recommended):
     ./scripts/setup.sh

  2. Or install manually:
     Download the model from the official Silero VAD repo:
       curl -LO https://github.com/snakers4/silero-vad/raw/master/files/silero_vad.onnx

     Or via Python:
       pip install silero-vad
       cp $(python3 -c "import silero_vad, os; print(os.path.join(os.path.dirname(silero_vad.__file__),'data','silero_vad.onnx'))") .

  3. Use -model flag to point to an existing model file:
     ./server -model /path/to/silero_vad.onnx

`, modelPath)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/live", handleLive)
	mux.HandleFunc("/ws", handleWS)
	mux.HandleFunc("/analyze", handleAnalyze)

	log.Printf("Starting server on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func readCookieBool(r *http.Request, name string) bool {
	c, err := r.Cookie(name)
	if err != nil {
		return false
	}
	v, err := strconv.ParseBool(c.Value)
	return err == nil && v
}

func setCookieBool(w http.ResponseWriter, name string, val bool) {
	http.SetCookie(w, &http.Cookie{
		Name:   name,
		Value:  strconv.FormatBool(val),
		MaxAge: 86400 * 365,
	})
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	var buf bytes.Buffer
	adaptiveChecked := readCookieBool(r, "adaptive")
	disableRMSChecked := readCookieBool(r, "disable_rms")
	if err := html.Render(html.ReportData{
		AdaptiveChecked:   adaptiveChecked,
		DisableRMSChecked: disableRMSChecked,
	}, &buf); err != nil {
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

	triggered bool

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
	disableRMS := r.URL.Query().Get("disable_rms") == "true"
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
			DisableRMSPostFilter: disableRMS,
		})
		if err != nil {
			conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
		session.adaptDetector = ad
		session.detector = ad.Inner()
		defer ad.Destroy()
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
		defer session.detector.Destroy()
	}
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
		var audioData string
		if startSample >= 0 && endSample <= len(s.pcmBuf) && startSample < endSample {
			segPCM := s.pcmBuf[startSample:endSample]
			rms = vad.RMS(segPCM)
			wav := pcmToWAVBytes(segPCM, 16000)
			audioData = "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav)
		}
		s.conn.WriteJSON(map[string]interface{}{
			"type":  "segment_end",
			"start": closed.Start,
			"end":   closed.End,
			"rms":   rms,
			"audio": audioData,
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
		s.conn.WriteJSON(map[string]interface{}{
			"type":             "adaptive_info",
			"baseline_db":      s.adaptDetector.BaselineDB(),
			"energy_offset_db": s.adaptDetector.EnergyOffsetDB(),
		})
	}
}

func (s *wsSession) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for open segment before flush
	preSegs := s.detector.GetSegments()
	hadOpen := len(preSegs) > 0 && preSegs[len(preSegs)-1].End == 0

	result := s.detector.Flush()

	// Send segment_end for the segment that was just closed by Flush()
	if hadOpen && len(result.Segments) > 0 {
		last := result.Segments[len(result.Segments)-1]
		rms := 0.0
		startSample := int(math.Round(last.Start * 16000))
		endSample := int(math.Round(last.End * 16000))
		var audioData string
		if startSample >= 0 && endSample <= len(s.pcmBuf) && startSample < endSample {
			segPCM := s.pcmBuf[startSample:endSample]
			rms = vad.RMS(segPCM)
			wav := pcmToWAVBytes(segPCM, 16000)
			audioData = "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav)
		}
		s.conn.WriteJSON(map[string]interface{}{
			"type":  "segment_end",
			"start": last.Start,
			"end":   last.End,
			"rms":   rms,
			"audio": audioData,
		})
	}

	// Build merged WAV from segments
	var mergedWAV []byte
	if len(result.Segments) > 0 && len(s.pcmBuf) > 0 {
		mergedPCM := make([]float32, 0)
		for _, seg := range result.Segments {
			startSample := int(math.Round(seg.Start * 16000))
			endSample := int(math.Round(seg.End * 16000))
			if startSample < 0 {
				startSample = 0
			}
			if endSample > len(s.pcmBuf) {
				endSample = len(s.pcmBuf)
			}
			if startSample >= endSample {
				continue
			}
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
		if s < 0 {
			v = int16(s * 0x8000)
		}
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
	sr := int(dec.SampleRate)

	targetSR := 0
	fmt.Sscanf(r.FormValue("samplerate"), "%d", &targetSR)

	if targetSR != 0 && targetSR != 16000 && targetSR != 8000 {
		http.Error(w, fmt.Sprintf("unsupported target sample rate: %d", targetSR), 400)
		return
	}

	if targetSR != 0 && sr != targetSR {
		log.Printf("Resampling from %d Hz to %d Hz", sr, targetSR)
		pcm = slice.Resample(pcm, sr, targetSR)
		sr = targetSR
	}

	if sr != 16000 && sr != 8000 {
		http.Error(w, fmt.Sprintf("unsupported sample rate: %d", sr), 400)
		return
	}

	adaptiveOn := r.FormValue("adaptive") == "true"
	disableRMS := r.FormValue("disable_rms") == "true"
	setCookieBool(w, "adaptive", adaptiveOn)
	setCookieBool(w, "disable_rms", disableRMS)

	var result vad.Result
	var filteredSegments []vad.Segment
	var adaptDetector *vad.AdaptiveDetector

	if adaptiveOn {
		adaptDetector, err = vad.NewAdaptiveDetector(vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            modelPath,
				SampleRate:           16000,
				Threshold:            0.5,
				MinSilenceDurationMs: 100,
				MinSpeechDurationMs:  100,
				SpeechPadMs:          30,
			},
			DisableRMSPostFilter: disableRMS,
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
