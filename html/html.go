package html

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"

	templates "github.com/liushunshun/smart-vad/template"

	"github.com/liushunshun/smart-vad/slice"
)

var reportTmpl = template.Must(template.New("report").Parse(templates.Report))

type ReportData struct {
	SampleRate         int
	Duration           float64
	PCM                []float32
	VADProbs           []float32
	Segments           []Segment
	FilteredSegments   []Segment
	SegmentFiles       []string
	SegmentPCM         [][]float32
	FilteredSegmentPCM [][]float32
	BackURL            string
	HasResults         bool
	AdaptiveVAD        bool
	AdaptiveChecked    bool
	DisableRMSChecked  bool
	Threshold          float32
	MinSpeechMs        int
	MinSilenceMs       int
	SpeechPadMs        int
	WindowDuration     float64
	NoiseFloorFrac     float64
	EnergyOffsetDB     float64
	BaselineDB         float64
}

type Segment struct {
	Start float64
	End   float64
	RMS   float64
}

type reportTmplData struct {
	HasResults                bool
	SampleRate                int
	Duration                  string
	PCMLength                 int
	SegmentCount              int
	WindowMs                  string
	SpeechRatio               string
	TotalTime                 string
	BackURL                   string
	WaveformJSON              template.JS
	VADProbsJSON              template.JS
	SegmentsJSON              template.JS
	FilteredSegmentsJSON      template.JS
	SegmentAudiosJSON         template.JS
	FilteredSegmentAudiosJSON template.JS
	SegmentFilesJSON          template.JS
	FilteredCount             int
	AdaptiveVAD               bool
	AdaptiveChecked           bool
	DisableRMSChecked         bool
	Threshold                 float32
	MinSpeechMs               int
	MinSilenceMs              int
	SpeechPadMs               int
	WindowDuration            string
	NoiseFloorFrac            string
	EnergyOffsetDB            float64
	BaselineDB                float64
}

func Render(data ReportData, w io.Writer) error {
	tmplData := reportTmplData{
		HasResults: data.HasResults,
	}

	if data.HasResults {
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

		tmplData.SampleRate = data.SampleRate
		tmplData.Duration = fmt.Sprintf("%.2f", dur)
		tmplData.PCMLength = len(data.PCM)
		tmplData.SegmentCount = len(data.Segments)
		tmplData.WindowMs = fmt.Sprintf("%.0f", windowMs)
		tmplData.SpeechRatio = speechRatio
		tmplData.TotalTime = fmt.Sprintf("%d:%02d", int(dur)/60, int(dur)%60)
		tmplData.BackURL = data.BackURL
		tmplData.WaveformJSON = template.JS(encodeFloat32Array(data.PCM))
		tmplData.VADProbsJSON = template.JS(encodeFloat32Array(data.VADProbs))
		tmplData.SegmentsJSON = template.JS(encodeSegments(data.Segments))
		tmplData.FilteredSegmentsJSON = template.JS(encodeSegments(data.FilteredSegments))
		tmplData.SegmentAudiosJSON = template.JS(encodeSegmentAudios(data.SegmentPCM, data.SampleRate))
		tmplData.FilteredSegmentAudiosJSON = template.JS(encodeSegmentAudios(data.FilteredSegmentPCM, data.SampleRate))
		tmplData.SegmentFilesJSON = template.JS(encodeStringArray(data.SegmentFiles))
		tmplData.FilteredCount = len(data.FilteredSegments)
		tmplData.AdaptiveVAD = data.AdaptiveVAD
		tmplData.AdaptiveChecked = data.AdaptiveChecked
		tmplData.DisableRMSChecked = data.DisableRMSChecked
		tmplData.Threshold = data.Threshold
		tmplData.MinSpeechMs = data.MinSpeechMs
		tmplData.MinSilenceMs = data.MinSilenceMs
		tmplData.SpeechPadMs = data.SpeechPadMs
		tmplData.WindowDuration = fmt.Sprintf("%.0f", data.WindowDuration)
		tmplData.NoiseFloorFrac = fmt.Sprintf("%.1f", data.NoiseFloorFrac)
		tmplData.EnergyOffsetDB = data.EnergyOffsetDB
		tmplData.BaselineDB = data.BaselineDB
	}

	return reportTmpl.Execute(w, tmplData)
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
		fmt.Fprintf(&b, `{"start":%.4f,"end":%.4f,"rms":%.2f}`, s.Start, s.End, s.RMS)
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
		wavData := slice.WAVBytes(seg, sampleRate)
		b64 := base64.StdEncoding.EncodeToString(wavData)
		fmt.Fprintf(&b, `"data:audio/wav;base64,%s"`, b64)
	}
	b.WriteByte(']')
	return b.String()
}
