package slice

import (
	"math"
	"os"
	"testing"
)

func TestSplit(t *testing.T) {
	pcm := make([]float32, 16000)
	for i := 0; i < 16000; i++ {
		if i >= 2000 && i < 4000 {
			pcm[i] = 0.5
		}
		if i >= 8000 && i < 10000 {
			pcm[i] = 0.5
		}
	}

	segments := Split(pcm, []float64{0.125, 0.5}, []float64{0.25, 0.625}, 16000)
	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if len(segments[0]) != 2000 {
		t.Errorf("segment 0 length = %d, want 2000", len(segments[0]))
	}
	if len(segments[1]) != 2000 {
		t.Errorf("segment 1 length = %d, want 2000", len(segments[1]))
	}
}

func TestSplitBounds(t *testing.T) {
	pcm := make([]float32, 16000)
	segments := Split(pcm, []float64{-0.5, 0.5}, []float64{0.1, 1.5}, 16000)
	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if segments[0][0] != 0 {
		t.Error("first sample should be 0 (clamped)")
	}
}

func TestWriteWAV(t *testing.T) {
	pcm := make([]float32, 16000)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / 16000))
	}

	tmpFile := t.TempDir() + "/test.wav"
	if err := WriteWAV(tmpFile, pcm, 16000); err != nil {
		t.Fatalf("WriteWAV failed: %v", err)
	}

	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	expectedSize := int64(44 + 16000*2)
	if info.Size() != expectedSize {
		t.Errorf("file size = %d, want %d", info.Size(), expectedSize)
	}
}
