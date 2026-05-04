package check

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	owner        = "snakers4"
	repo         = "silero-vad"
	tagsAPI      = "https://api.github.com/repos/snakers4/silero-vad/tags"
	modelPathFmt = "https://github.com/snakers4/silero-vad/raw/%s/src/silero_vad/data/silero_vad.onnx"
)

func ModelExists(path string) {
	if _, err := os.Stat(path); err == nil {
		return
	}

	fmt.Fprintf(os.Stderr, "\nError: model file not found: %s\n\n", path)

	if !isTerminal() {
		printManualDownload()
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "The Silero VAD model (silero_vad.onnx) is required to run.\n")

	tags := fetchTags()
	if len(tags) == 0 {
		fmt.Fprintf(os.Stderr, "\nCould not fetch available versions.\n")
		if prompt("Download the latest version anyway?") {
			if err := downloadModel(path, "master"); err != nil {
				fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
			} else {
				return
			}
		}
		printManualDownload()
		os.Exit(1)
	}

	selected := askVersion(tags)
	if err := downloadModel(path, tags[selected]); err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		printManualDownload()
		os.Exit(1)
	}
}

func fetchTags() []string {
	resp, err := http.Get(tagsAPI)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var result []struct{ Name string }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil
	}

	tags := make([]string, 0, len(result))
	for _, t := range result {
		tags = append(tags, t.Name)
	}
	return tags
}

func askVersion(tags []string) int {
	fmt.Fprintf(os.Stderr, "\nAvailable versions:\n")
	showCount := len(tags)
	if showCount > 20 {
		showCount = 20
	}
	for i, tag := range tags[:showCount] {
		fmt.Fprintf(os.Stderr, "  %2d. %s\n", i+1, tag)
	}
	if len(tags) > 20 {
		fmt.Fprintf(os.Stderr, "  ... (showing top 20)\n")
	}

	fmt.Fprintf(os.Stderr, "\nSelect version [default: 1, latest: %s]: ", tags[0])

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return 0
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(tags) {
		fmt.Fprintf(os.Stderr, "Invalid selection, using latest: %s\n", tags[0])
		return 0
	}
	return n - 1
}

func downloadModel(path, version string) error {
	url := fmt.Sprintf(modelPathFmt, version)
	fmt.Fprintf(os.Stderr, "Downloading silero_vad.onnx (version %s)...\n", version)

	resp, err := http.Get(url)
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

	fmt.Fprintf(os.Stderr, "✓ Downloaded %.1f MB → %s\n", float64(written)/1024/1024, path)
	return nil
}

func printManualDownload() {
	fmt.Fprintf(os.Stderr, "\nDownload it manually:\n")
	fmt.Fprintf(os.Stderr, "  curl -LO https://github.com/%s/%s/raw/v6.2.1/src/silero_vad/data/silero_vad.onnx\n", owner, repo)
	fmt.Fprintf(os.Stderr, "Or use -model to point to an existing file:\n")
	fmt.Fprintf(os.Stderr, "  %s -model /path/to/silero_vad.onnx\n\n", os.Args[0])
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
