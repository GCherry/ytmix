package probe

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

var leadingNumberPattern = regexp.MustCompile(`^\d+[\s._-]+`)

// DiscoverAudioFiles scans audioDir for .mp3 files (case-insensitively), sorts them
// alphabetically by filename, and optionally shuffles them.
func DiscoverAudioFiles(audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int))) ([]string, error) {
	entries, err := os.ReadDir(audioDir)
	if err != nil {
		return nil, fmt.Errorf("read audio directory %q: %w", audioDir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".mp3") {
			files = append(files, filepath.Join(audioDir, entry.Name()))
		}
	}

	if len(files) < 1 {
		return nil, fmt.Errorf("no .mp3 Tracks found in %q", audioDir)
	}

	sort.Slice(files, func(i, j int) bool {
		return filepath.Base(files[i]) < filepath.Base(files[j])
	})

	if shuffle {
		if shuffleFn == nil {
			// #nosec G404 -- non-cryptographic randomness used solely for --shuffle Track playback ordering.
			shuffleFn = rand.Shuffle
		}
		shuffleFn(len(files), func(i, j int) {
			files[i], files[j] = files[j], files[i]
		})
	}

	return files, nil
}

// ResolveDisplayTitle returns the trimmed ID3 title if non-empty, otherwise cleans the filename.
func ResolveDisplayTitle(id3Title, filename string) string {
	if trimmed := strings.TrimSpace(id3Title); trimmed != "" {
		return trimmed
	}

	base := filepath.Base(filename)
	if ext := filepath.Ext(base); strings.EqualFold(ext, ".mp3") {
		base = base[:len(base)-len(ext)]
	}
	cleaned := strings.TrimSpace(leadingNumberPattern.ReplaceAllString(base, ""))
	if cleaned == "" {
		return strings.TrimSpace(base)
	}
	return cleaned
}

// ProbeFile runs ffprobe on path and extracts its duration in seconds and ID3 title tag.
func ProbeFile(path string, runner func(name string, args ...string) ([]byte, error)) (duration float64, id3Title string, err error) {
	out, err := runner(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-show_entries", "format_tags=title",
		"-of", "json",
		path,
	)
	if err != nil {
		return 0, "", fmt.Errorf("ffprobe %q: %w", path, err)
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0, "", fmt.Errorf("parse ffprobe JSON for %q: %w", path, err)
	}

	dur, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if err != nil || dur <= 0 {
		return 0, "", fmt.Errorf("invalid duration %q for %q", parsed.Format.Duration, path)
	}

	for k, v := range parsed.Format.Tags {
		if strings.EqualFold(k, "title") {
			id3Title = v
			break
		}
	}

	return dur, id3Title, nil
}
