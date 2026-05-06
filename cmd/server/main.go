package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-audio/wav"
	"github.com/gorilla/websocket"
	"github.com/liushunshun/smart-vad/cmd/internal/check"
	"github.com/liushunshun/smart-vad/html"
	"github.com/liushunshun/smart-vad/logger"
	"github.com/liushunshun/smart-vad/slice"
	templates "github.com/liushunshun/smart-vad/template"
	"github.com/liushunshun/smart-vad/vad"
)

// ---- Environment helpers ----

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// ---- Server configuration ----

type serverConfig struct {
	Addr           string
	ModelPath      string
	Threshold      float64
	Debug          bool
	WSToken        string
	MaxUploadMB    int
	MaxPCMDurSec   int
	MaxWSPCMDurSec int
	WSMaxConns     int
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	AllowedOrigins []string
	PprofEnabled   bool
}

func loadConfig() serverConfig {
	addr := flag.String("addr", getEnv("SMART_VAD_ADDR", ":8080"), "listen address")
	model := flag.String("model", getEnv("SMART_VAD_MODEL", "silero_vad.onnx"), "path to silero_vad.onnx")
	threshold := flag.Float64("threshold", getEnvFloat("SMART_VAD_THRESHOLD", 0.3), "VAD threshold")
	debug := flag.Bool("debug", getEnvBool("SMART_VAD_DEBUG", false), "enable debug logging")
	wsToken := flag.String("ws-token", getEnv("SMART_VAD_WS_TOKEN", ""), "WebSocket auth token (required if set)")

	maxUploadMB := flag.Int("max-upload-mb", getEnvInt("SMART_VAD_MAX_UPLOAD_MB", 500), "max upload size in MB")
	maxPCMDurSec := flag.Int("max-pcm-dur", getEnvInt("SMART_VAD_MAX_PCM_DUR", 600), "max audio duration in seconds")
	maxWSPCMDurSec := flag.Int("max-ws-pcm-dur", getEnvInt("SMART_VAD_MAX_WS_PCM_DUR", 120), "max buffered PCM seconds per WS session")
	wsMaxConns := flag.Int("ws-max-conns", getEnvInt("SMART_VAD_WS_MAX_CONNS", 10), "max concurrent WebSocket connections")

	readTimeout := flag.Duration("read-timeout", getEnvDuration("SMART_VAD_READ_TIMEOUT", 30*time.Second), "HTTP read timeout")
	writeTimeout := flag.Duration("write-timeout", getEnvDuration("SMART_VAD_WRITE_TIMEOUT", 60*time.Second), "HTTP write timeout")
	idleTimeout := flag.Duration("idle-timeout", getEnvDuration("SMART_VAD_IDLE_TIMEOUT", 120*time.Second), "HTTP idle timeout")

	allowedOrigins := flag.String("allowed-origins", getEnv("SMART_VAD_ALLOWED_ORIGINS", ""), "comma-separated allowed WebSocket origins")
	pprofEnabled := flag.Bool("pprof", getEnvBool("SMART_VAD_PPROF", false), "enable /debug/pprof endpoints")

	flag.Parse()

	cfg := serverConfig{
		Addr:           *addr,
		ModelPath:      *model,
		Threshold:      *threshold,
		Debug:          *debug,
		WSToken:        *wsToken,
		MaxUploadMB:    *maxUploadMB,
		MaxPCMDurSec:   *maxPCMDurSec,
		MaxWSPCMDurSec: *maxWSPCMDurSec,
		WSMaxConns:     *wsMaxConns,
		ReadTimeout:    *readTimeout,
		WriteTimeout:   *writeTimeout,
		IdleTimeout:    *idleTimeout,
		PprofEnabled:   *pprofEnabled,
	}

	if *allowedOrigins != "" {
		cfg.AllowedOrigins = strings.Split(*allowedOrigins, ",")
		for i := range cfg.AllowedOrigins {
			cfg.AllowedOrigins[i] = strings.TrimSpace(cfg.AllowedOrigins[i])
		}
	}

	return cfg
}

// ---- Origin checker ----

func buildOriginChecker(allowed []string) func(r *http.Request) bool {
	if len(allowed) == 0 {
		return func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			return strings.HasPrefix(origin, "http://localhost") ||
				strings.HasPrefix(origin, "http://127.0.0.1")
		}
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		for _, a := range allowed {
			if a == "*" || strings.HasPrefix(origin, a) {
				return true
			}
		}
		return false
	}
}

// ---- Rate limiter ----

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*rateVisitor
}

type rateVisitor struct {
	tokens   float64
	lastTime time.Time
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{visitors: make(map[string]*rateVisitor)}
	go rl.reap()
	return rl
}

func (rl *rateLimiter) allow(ip string, ratePerSec float64, burst int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	v, ok := rl.visitors[ip]
	if !ok {
		rl.visitors[ip] = &rateVisitor{tokens: float64(burst - 1), lastTime: now}
		return true
	}

	elapsed := now.Sub(v.lastTime).Seconds()
	v.tokens = math.Min(float64(burst), v.tokens+elapsed*ratePerSec)
	v.lastTime = now

	if v.tokens >= 1 {
		v.tokens--
		return true
	}
	return false
}

func (rl *rateLimiter) reap() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastTime) > 5*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// ---- WebSocket connection tracker ----

type wsConnTracker struct {
	mu    sync.Mutex
	count int
	max   int
}

func (t *wsConnTracker) acquire() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count >= t.max {
		return false
	}
	t.count++
	return true
}

func (t *wsConnTracker) release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count--
}

// ---- Server state ----

var (
	cfg      serverConfig
	upgrader websocket.Upgrader
	wsTrack  wsConnTracker
	limiter  *rateLimiter
	ready    atomic.Bool
)

func main() {
	cfg = loadConfig()
	logger.Init(cfg.Debug)

	check.ModelExists(cfg.ModelPath)

	upgrader = websocket.Upgrader{
		CheckOrigin: buildOriginChecker(cfg.AllowedOrigins),
	}

	wsTrack = wsConnTracker{max: cfg.WSMaxConns}
	limiter = newRateLimiter()

	// Preload model to validate and warm up
	go func() {
		d, err := vad.NewDetector(vad.Config{
			ModelPath:  cfg.ModelPath,
			SampleRate: 16000,
			Threshold:  float32(cfg.Threshold),
		})
		if err != nil {
			logger.Error("model preload failed", "error", err)
			return
		}
		d.Destroy()
		ready.Store(true)
		logger.Info("model preloaded successfully")
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/ready", handleReady)
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/live", handleLive)
	mux.HandleFunc("/ws", handleWS)
	mux.HandleFunc("/analyze", handleAnalyze)

	if cfg.PprofEnabled {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("received signal, shutting down gracefully", "signal", sig.String())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown error", "error", err)
		}
	}()

	logger.Info("server started", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("server stopped unexpectedly", "error", err)
	}
	logger.Info("server stopped")
}

// ---- Health / Ready ----

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	if ready.Load() {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	} else {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("not ready: model loading"))
	}
}

// ---- Cookie helpers ----

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

// ---- Page handlers ----

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
		logger.Error("render index failed", "error", err)
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

	// Accumulated PCM for flush, capped to maxPCMDurSec
	pcmBuf       []float32
	maxPCMLen    int // max samples to buffer (maxWSPCMDurSec * sampleRate)
	sampleOffset int // offset into the logical stream for pcmBuf trimming
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	// Token auth
	if cfg.WSToken != "" {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = r.Header.Get("Authorization")
			token = strings.TrimPrefix(token, "Bearer ")
		}
		if token != cfg.WSToken {
			logger.Warn("websocket auth rejected", "remote", r.RemoteAddr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Connection limit
	if !wsTrack.acquire() {
		logger.Warn("websocket connection rejected: max connections reached",
			"remote", r.RemoteAddr,
			"max", cfg.WSMaxConns)
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer wsTrack.release()

	logger.Debug("websocket connection request",
		"remote", r.RemoteAddr,
		"adaptive", r.URL.Query().Get("adaptive"))

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Warn("websocket upgrade failed", "error", err)
		return
	}
	defer func() {
		logger.Debug("websocket connection closed", "remote", r.RemoteAddr)
		conn.Close()
	}()

	session := &wsSession{conn: conn}
	session.maxPCMLen = cfg.MaxWSPCMDurSec * 16000

	// Set read deadline for the first message
	conn.SetReadDeadline(time.Now().Add(5 * time.Minute))

	adaptive := r.URL.Query().Get("adaptive") == "true"
	disableRMS := r.URL.Query().Get("disable_rms") == "true"
	if adaptive {
		ad, err := vad.NewAdaptiveDetector(vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            cfg.ModelPath,
				SampleRate:           16000,
				Threshold:            float32(cfg.Threshold),
				MinSilenceDurationMs: 100,
				MinSpeechDurationMs:  100,
				SpeechPadMs:          30,
			},
			DisableRMSPostFilter: disableRMS,
		})
		if err != nil {
			logger.Error("create adaptive detector failed", "error", err)
			conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
		logger.Debug("adaptive detector created", "disable_rms", disableRMS)
		session.adaptDetector = ad
		session.detector = ad.Inner()
		defer ad.Destroy()
	} else {
		d, err := vad.NewDetector(vad.Config{
			ModelPath:            cfg.ModelPath,
			SampleRate:           16000,
			Threshold:            float32(cfg.Threshold),
			MinSilenceDurationMs: 100,
			MinSpeechDurationMs:  100,
			SpeechPadMs:          30,
		})
		if err != nil {
			logger.Error("create detector failed", "error", err)
			conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
			return
		}
		logger.Debug("detector created")
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

	msgCount := 0
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			logger.Debug("websocket read closed", "messages_processed", msgCount)
			break
		}

		// Refresh read deadline on each message
		conn.SetReadDeadline(time.Now().Add(5 * time.Minute))

		msgCount++

		if mt == websocket.TextMessage {
			var msg map[string]interface{}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			msgType, _ := msg["type"].(string)
			logger.Debug("websocket text message", "type", msgType)
			switch msgType {
			case "flush":
				session.flush()
				continue
			case "reset":
				session.reset()
			}
			continue
		}

		if mt == websocket.BinaryMessage {
			pcm := bytesToFloat32(data)
			logger.Debug("websocket binary chunk", "samples", len(pcm))
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

	start := time.Now()
	if s.adaptDetector != nil {
		if err := s.adaptDetector.Process(pcm); err != nil {
			s.writeErr("adaptive process chunk failed", err)
			logger.Error("adaptive process chunk failed", "error", err)
			return
		}
	} else {
		if err := s.detector.Process(pcm); err != nil {
			s.writeErr("process chunk failed", err)
			logger.Error("process chunk failed", "error", err)
			return
		}
	}
	logger.Debug("chunk processed", "samples", len(pcm), "duration_us", time.Since(start).Microseconds())

	// Accumulate PCM for potential flush, with cap
	s.pcmBuf = append(s.pcmBuf, pcm...)
	if len(s.pcmBuf) > s.maxPCMLen {
		// Trim oldest samples, adjust sample offset
		excess := len(s.pcmBuf) - s.maxPCMLen
		s.pcmBuf = s.pcmBuf[excess:]
		s.sampleOffset += excess
	}

	curProbs := s.detector.GetProbs()
	curSegs := s.detector.GetSegments()

	// Send new probs
	if len(curProbs) > prevProbsLen {
		newProbs := curProbs[prevProbsLen:]
		probs32 := make([]float32, len(newProbs))
		copy(probs32, newProbs)
		s.writeJSON(map[string]interface{}{
			"type":  "probs",
			"probs": probs32,
		})
	}

	// Detect segment_end
	if prevSegLen > 0 && prevLastEnd == 0 && prevSegLen <= len(curSegs) && curSegs[prevSegLen-1].End > 0 {
		closed := curSegs[prevSegLen-1]
		rms := 0.0
		var audioData string
		startSample := int(math.Round(closed.Start*16000)) - s.sampleOffset
		endSample := int(math.Round(closed.End*16000)) - s.sampleOffset
		if startSample >= 0 && endSample <= len(s.pcmBuf) && startSample < endSample {
			segPCM := s.pcmBuf[startSample:endSample]
			rms = vad.RMS(segPCM)
			wavBytes := slice.WAVBytes(segPCM, 16000)
			audioData = "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wavBytes)
		}
		logger.Debug("segment ended",
			"start", closed.Start,
			"end", closed.End,
			"rms_db", rms)
		s.writeJSON(map[string]interface{}{
			"type":  "segment_end",
			"start": closed.Start,
			"end":   closed.End,
			"rms":   rms,
			"audio": audioData,
		})
	}

	// Detect new segment starts
	for i := prevSegLen; i < len(curSegs); i++ {
		logger.Debug("segment started", "start", curSegs[i].Start)
		s.writeJSON(map[string]interface{}{
			"type":  "segment_start",
			"start": curSegs[i].Start,
		})
	}

	// State change
	curTriggered := s.detector.IsTriggered()
	if curTriggered != s.triggered {
		s.triggered = curTriggered
		logger.Debug("vad state changed", "triggered", curTriggered)
		s.writeJSON(map[string]interface{}{
			"type":      "state",
			"triggered": curTriggered,
		})
	}

	// Progress
	currentTime := float64(s.detector.CurrentSample()) / 16000
	s.writeJSON(map[string]interface{}{
		"type": "progress",
		"time": currentTime,
	})

	// Adaptive threshold update
	if s.adaptDetector != nil {
		s.writeJSON(map[string]interface{}{
			"type":      "threshold",
			"threshold": s.detector.GetThreshold(),
		})
		s.writeJSON(map[string]interface{}{
			"type":             "adaptive_info",
			"baseline_db":      s.adaptDetector.BaselineDB(),
			"energy_offset_db": s.adaptDetector.EnergyOffsetDB(),
		})
	}
}

func (s *wsSession) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	logger.Debug("flush called", "buffered_samples", len(s.pcmBuf))

	// Check for open segment before flush
	preSegs := s.detector.GetSegments()
	hadOpen := len(preSegs) > 0 && preSegs[len(preSegs)-1].End == 0

	result := s.detector.Flush()

	logger.Info("flush result", "segments", len(result.Segments))

	// Send segment_end for the segment that was just closed by Flush()
	if hadOpen && len(result.Segments) > 0 {
		last := result.Segments[len(result.Segments)-1]
		rms := 0.0
		var audioData string
		startSample := int(math.Round(last.Start*16000)) - s.sampleOffset
		endSample := int(math.Round(last.End*16000)) - s.sampleOffset
		if startSample >= 0 && endSample <= len(s.pcmBuf) && startSample < endSample {
			segPCM := s.pcmBuf[startSample:endSample]
			rms = vad.RMS(segPCM)
			wavBytes := slice.WAVBytes(segPCM, 16000)
			audioData = "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wavBytes)
		}
		s.writeJSON(map[string]interface{}{
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
			startSample := int(math.Round(seg.Start*16000)) - s.sampleOffset
			endSample := int(math.Round(seg.End*16000)) - s.sampleOffset
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
			mergedWAV = slice.WAVBytes(mergedPCM, 16000)
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
	s.writeJSON(resp)
}

func (s *wsSession) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	logger.Debug("session reset", "buffered_samples", len(s.pcmBuf))
	s.detector.Reset()
	s.pcmBuf = s.pcmBuf[:0]
	s.sampleOffset = 0
	s.triggered = false
}

// writeJSON sends a JSON message to the WebSocket client. Write errors are logged
// and the connection is assumed dead (the caller should avoid further writes).
func (s *wsSession) writeJSON(v interface{}) {
	if err := s.conn.WriteJSON(v); err != nil {
		logger.Debug("websocket write failed, client likely disconnected", "error", err)
	}
}

func (s *wsSession) writeErr(context string, err error) {
	s.writeJSON(map[string]string{"type": "error", "message": context + ": " + err.Error()})
}

// ---- Analyze handler ----

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}

	// Rate limit: 10 req/min per IP
	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.Split(fwd, ",")[0]
	}
	if !limiter.allow(ip, 10.0/60.0, 5) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	logger.Debug("analyze request", "remote", r.RemoteAddr)

	// Limit upload size
	maxBodyBytes := int64(cfg.MaxUploadMB) * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	file, _, err := r.FormFile("audio")
	if err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			http.Error(w, fmt.Sprintf("upload too large (max %d MB)", cfg.MaxUploadMB), http.StatusRequestEntityTooLarge)
			return
		}
		logger.Warn("analyze: missing audio file", "error", err)
		http.Error(w, "missing audio file", 400)
		return
	}
	defer file.Close()

	tmpDir, err := os.MkdirTemp("", "smart-vad-*")
	if err != nil {
		logger.Error("create temp dir failed", "error", err)
		http.Error(w, "temp dir failed", 500)
		return
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "input.wav")
	f, err := os.Create(tmpFile)
	if err != nil {
		logger.Error("create temp file failed", "error", err)
		http.Error(w, "create temp failed", 500)
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		f.Close()
		logger.Error("write temp file failed", "error", err)
		http.Error(w, "write temp failed", 500)
		return
	}
	f.Close()

	af, err := os.Open(tmpFile)
	if err != nil {
		logger.Error("open temp file failed", "error", err)
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
	if buf == nil {
		http.Error(w, "empty audio", 400)
		return
	}

	pcm := buf.AsFloat32Buffer().Data
	sr := int(dec.SampleRate)

	// Limit PCM duration
	maxSamples := cfg.MaxPCMDurSec * sr
	if len(pcm) > maxSamples {
		http.Error(w, fmt.Sprintf("audio too long: max %d seconds", cfg.MaxPCMDurSec), http.StatusRequestEntityTooLarge)
		return
	}

	targetSR := 0
	fmt.Sscanf(r.FormValue("samplerate"), "%d", &targetSR)

	if targetSR != 0 && targetSR != 16000 && targetSR != 8000 {
		http.Error(w, fmt.Sprintf("unsupported target sample rate: %d", targetSR), 400)
		return
	}

	if targetSR != 0 && sr != targetSR {
		logger.Info("resampling audio", "from", sr, "to", targetSR)
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

	logger.Debug("analyze params",
		"sample_rate", sr,
		"samples", len(pcm),
		"adaptive", adaptiveOn,
		"disable_rms", disableRMS)

	var result vad.Result
	var filteredSegments []vad.Segment
	var adaptDetector *vad.AdaptiveDetector

	start := time.Now()
	if adaptiveOn {
		adaptDetector, err = vad.NewAdaptiveDetector(vad.AdaptiveConfig{
			DetectorConfig: vad.Config{
				ModelPath:            cfg.ModelPath,
				SampleRate:           16000,
				Threshold:            float32(cfg.Threshold),
				MinSilenceDurationMs: 100,
				MinSpeechDurationMs:  100,
				SpeechPadMs:          30,
			},
			DisableRMSPostFilter: disableRMS,
		})
		if err != nil {
			logger.Error("create adaptive detector failed", "error", err)
			http.Error(w, fmt.Sprintf("adaptive detector: %v", err), 500)
			return
		}
		defer adaptDetector.Destroy()

		result, err = adaptDetector.Detect(pcm)
		if err != nil {
			logger.Error("detection failed", "error", err)
			http.Error(w, fmt.Sprintf("detect: %v", err), 500)
			return
		}
		filteredSegments = adaptDetector.FilteredSegments()
		logger.Debug("adaptive VAD params",
			"baseline_db", adaptDetector.BaselineDB(),
			"energy_offset_db", adaptDetector.EnergyOffsetDB())
	} else {
		detector, err := vad.NewDetector(vad.Config{
			ModelPath:            cfg.ModelPath,
			SampleRate:           16000,
			Threshold:            float32(cfg.Threshold),
			MinSilenceDurationMs: 100,
			MinSpeechDurationMs:  100,
			SpeechPadMs:          30,
		})
		if err != nil {
			logger.Error("create detector failed", "error", err)
			http.Error(w, fmt.Sprintf("detector: %v", err), 500)
			return
		}
		defer detector.Destroy()

		result, err = detector.Detect(pcm)
		if err != nil {
			logger.Error("detection failed", "error", err)
			http.Error(w, fmt.Sprintf("detect: %v", err), 500)
			return
		}
	}

	logger.Info("analyze detection done",
		"segments", len(result.Segments),
		"filtered", len(filteredSegments),
		"duration_ms", time.Since(start).Milliseconds())

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
	if err := os.MkdirAll(segDir, 0755); err != nil {
		logger.Error("create segments dir failed", "error", err)
		http.Error(w, "create segments dir failed", 500)
		return
	}
	for i, seg := range segPCMs {
		fname := filepath.Join(segDir, fmt.Sprintf("seg-%03d.wav", i+1))
		if err := slice.WriteWAV(fname, seg, srInt); err != nil {
			logger.Error("write segment failed", "error", err)
			continue
		}
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

	var baselineDB, energyOffsetDB, windowDuration, noiseFloorFrac float64
	if adaptDetector != nil {
		baselineDB = adaptDetector.BaselineDB()
		energyOffsetDB = adaptDetector.EnergyOffsetDB()
		windowDuration = 30
		noiseFloorFrac = 0.1
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
		Threshold:          float32(cfg.Threshold),
		MinSpeechMs:        100,
		MinSilenceMs:       100,
		SpeechPadMs:        30,
		WindowDuration:     windowDuration,
		NoiseFloorFrac:     noiseFloorFrac,
		EnergyOffsetDB:     energyOffsetDB,
		BaselineDB:         baselineDB,
	}, &reportBuf); err != nil {
		logger.Error("render report failed", "error", err)
		http.Error(w, fmt.Sprintf("render: %v", err), 500)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(reportBuf.Bytes())
}
