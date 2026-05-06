package slice

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/liushunshun/smart-vad/logger"
)

func Split(pcm []float32, starts, ends []float64, sampleRate int) [][]float32 {
	var result [][]float32
	for i := range starts {
		startSample := int(math.Round(starts[i] * float64(sampleRate)))
		endSample := int(math.Round(ends[i] * float64(sampleRate)))
		if startSample < 0 {
			logger.Debug("segment start clamped", "index", i, "original", starts[i], "clamped", 0)
			startSample = 0
		}
		if endSample > len(pcm) {
			logger.Debug("segment end clamped", "index", i, "original", ends[i], "clamped", float64(len(pcm))/float64(sampleRate))
			endSample = len(pcm)
		}
		if startSample >= endSample {
			logger.Debug("segment skipped: start >= end", "index", i, "start", starts[i], "end", ends[i])
			continue
		}
		seg := make([]float32, endSample-startSample)
		copy(seg, pcm[startSample:endSample])
		result = append(result, seg)
	}
	return result
}

func WriteWAV(filename string, pcm []float32, sampleRate int) error {
	logger.Debug("writing WAV", "path", filename, "samples", len(pcm), "rate", sampleRate)

	if err := os.MkdirAll(dirname(filename), 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	numSamples := len(pcm)
	bitsPerSample := 16
	numChannels := 1
	bytesPerSample := bitsPerSample / 8
	blockAlign := numChannels * bytesPerSample
	byteRate := sampleRate * blockAlign
	dataSize := numSamples * blockAlign

	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(44+dataSize-8))
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], uint16(numChannels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bitsPerSample))
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	if _, err := f.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	samples := make([]int16, numSamples)
	for i, s := range pcm {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		samples[i] = int16(s * math.MaxInt16)
	}
	if err := binary.Write(f, binary.LittleEndian, samples); err != nil {
		return fmt.Errorf("write samples: %w", err)
	}

	return nil
}

func Resample(pcm []float32, srcRate, dstRate int) []float32 {
	if srcRate == dstRate {
		return pcm
	}
	ratio := float64(srcRate) / float64(dstRate)
	outLen := int(math.Round(float64(len(pcm)) / ratio))
	logger.Debug("resampling", "from", srcRate, "to", dstRate, "in_samples", len(pcm), "out_samples", outLen)
	out := make([]float32, outLen)
	for i := 0; i < outLen; i++ {
		srcIdx := float64(i) * ratio
		idx := int(srcIdx)
		frac := srcIdx - float64(idx)
		if idx+1 < len(pcm) {
			out[i] = float32(float64(pcm[idx])*(1-frac) + float64(pcm[idx+1])*frac)
		} else {
			out[i] = pcm[idx]
		}
	}
	return out
}

// WAVBytes encodes PCM float32 samples to a WAV byte slice (16-bit mono).
// Samples are clamped to [-1.0, 1.0] before conversion.
func WAVBytes(pcm []float32, sampleRate int) []byte {
	n := len(pcm)
	buf := make([]byte, 44+n*2)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+n*2))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(n*2))
	for i, s := range pcm {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(int16(s*math.MaxInt16)))
	}
	return buf
}

func dirname(path string) string {
	return filepath.Dir(path)
}
