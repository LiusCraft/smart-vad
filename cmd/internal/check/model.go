package check

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const modelURL = "https://github.com/snakers4/silero-vad/raw/master/files/silero_vad.onnx"

func ModelExists(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}

	fmt.Fprintf(os.Stderr, "\nError: model file not found: %s\n\n", path)

	if !isTerminal() {
		fmt.Fprintf(os.Stderr, "Download it manually:\n  curl -LO %s\n\n", modelURL)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "The Silero VAD model (silero_vad.onnx) is required to run.\n")

	ans := prompt("Download it now?")
	switch ans {
	case true:
		if err := downloadModel(path); err != nil {
			fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "Try manual download:\n  curl -LO %s\n", modelURL)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Model saved to: %s\n\n", path)

	case false:
		fmt.Fprintf(os.Stderr, "\nDownload it manually:\n  curl -LO %s\n", modelURL)
		fmt.Fprintf(os.Stderr, "Or use -model to point to an existing file:\n  %s -model /path/to/silero_vad.onnx\n\n", os.Args[0])
		os.Exit(1)
	}
}

func isTerminal() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func prompt(q string) bool {
	fmt.Fprintf(os.Stderr, "%s [Y/n] ", q)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	return line == "" || strings.EqualFold(line, "y") || strings.EqualFold(line, "yes")
}

func downloadModel(path string) error {
	fmt.Fprintf(os.Stderr, "Downloading silero_vad.onnx...\n")

	resp, err := http.Get(modelURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Downloaded %.1f MB\n", float64(written)/1024/1024)
	return nil
}
