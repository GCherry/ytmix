package main

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

// Track represents a single discovered .mp3 audio file, its probed duration in seconds,
// and its resolved Display Title.
type Track struct {
	Path         string
	FileName     string
	Duration     float64
	DisplayTitle string
}

type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

var leadingNumberPattern = regexp.MustCompile(`^\d+[\s._-]+`)

func discoverAudioFiles(audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int))) ([]string, error) {
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
			shuffleFn = rand.Shuffle
		}
		shuffleFn(len(files), func(i, j int) {
			files[i], files[j] = files[j], files[i]
		})
	}

	return files, nil
}

func resolveDisplayTitle(id3Title, filename string) string {
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

func probeFile(path string, runner CommandRunner) (duration float64, id3Title string, err error) {
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

func probeTracks(audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner CommandRunner) ([]Track, error) {
	files, err := discoverAudioFiles(audioDir, shuffle, shuffleFn)
	if err != nil {
		return nil, err
	}

	tracks := make([]Track, 0, len(files))
	for _, path := range files {
		dur, id3Title, err := probeFile(path, runner)
		if err != nil {
			return nil, err
		}
		tracks = append(tracks, Track{
			Path:         path,
			FileName:     filepath.Base(path),
			Duration:     dur,
			DisplayTitle: resolveDisplayTitle(id3Title, path),
		})
	}
	return tracks, nil
}
