package slice

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

func Split(pcm []float32, starts, ends []float64, sampleRate int) [][]float32 {
	var result [][]float32
	for i := range starts {
		startSample := int(math.Round(starts[i] * float64(sampleRate)))
		endSample := int(math.Round(ends[i] * float64(sampleRate)))
		if startSample < 0 {
			startSample = 0
		}
		if endSample > len(pcm) {
			endSample = len(pcm)
		}
		if startSample >= endSample {
			continue
		}
		seg := make([]float32, endSample-startSample)
		copy(seg, pcm[startSample:endSample])
		result = append(result, seg)
	}
	return result
}

func WriteWAV(filename string, pcm []float32, sampleRate int) error {
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
		samples[i] = int16(math.MaxInt16 * s)
	}
	if err := binary.Write(f, binary.LittleEndian, samples); err != nil {
		return fmt.Errorf("write samples: %w", err)
	}

	return nil
}

func dirname(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}
