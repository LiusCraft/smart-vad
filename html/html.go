package html

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"html/template"
	"io"
	"math"

	"github.com/liushunshun/smart-vad/template"
)

type ReportData struct {
	SampleRate   int
	Duration     float64
	PCM          []float32
	VADProbs     []float32
	Segments     []Segment
	SegmentFiles []string
	SegmentPCM   [][]float32
	BackURL      string
}

type Segment struct {
	Start float64
	End   float64
}

type reportTmplData struct {
	SampleRate        int
	Duration          string
	PCMLength         int
	SegmentCount      int
	WindowMs          string
	SpeechRatio       string
	TotalTime         string
	BackURL           string
	WaveformJSON      template.JS
	VADProbsJSON      template.JS
	SegmentsJSON      template.JS
	SegmentAudiosJSON template.JS
	SegmentFilesJSON  template.JS
}

func Render(data ReportData, w io.Writer) error {
	dur := float64(len(data.PCM)) / float64(data.SampleRate)
	windowMs := 1000.0 * 512 / float64(data.SampleRate)

	speechRatio := "0%"
	if len(data.Segments) > 0 && dur > 0 {
		totalSpeech := 0.0
		for _, s := range data.Segments {
			totalSpeech += s.End - s.Start
		}
		speechRatio = fmt.Sprintf("%.1f%%", totalSpeech/dur*100)
	}

	tmplData := reportTmplData{
		SampleRate:        data.SampleRate,
		Duration:          fmt.Sprintf("%.2f", dur),
		PCMLength:         len(data.PCM),
		SegmentCount:      len(data.Segments),
		WindowMs:          fmt.Sprintf("%.0f", windowMs),
		SpeechRatio:       speechRatio,
		TotalTime:         fmt.Sprintf("%d:%02d", int(dur)/60, int(dur)%60),
		BackURL:           data.BackURL,
		WaveformJSON:      template.JS(encodeFloat32Array(data.PCM)),
		VADProbsJSON:      template.JS(encodeFloat32Array(data.VADProbs)),
		SegmentsJSON:      template.JS(encodeSegments(data.Segments)),
		SegmentAudiosJSON: template.JS(encodeSegmentAudios(data.SegmentPCM, data.SampleRate)),
		SegmentFilesJSON:  template.JS(encodeStringArray(data.SegmentFiles)),
	}

	tmpl, err := template.New("report").Parse(templates.Report)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	return tmpl.Execute(w, tmplData)
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
