package mixer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GCherry/ytmix/internal/audio"
	"github.com/GCherry/ytmix/internal/probe"
	"github.com/GCherry/ytmix/internal/subtitle"
)

// Track represents a single discovered .mp3 audio file, its probed duration in seconds,
// and its resolved Display Title.
type Track struct {
	Path         string
	FileName     string
	Duration     float64
	DisplayTitle string
}

// Config holds the parsed CLI flags for a ytmix Mix build.
type Config struct {
	AudioDir      string
	BG            string
	Out           string
	Timestamps    string
	Crossfade     float64
	Normalize     bool
	OverlayTitles bool
	TitleDuration int
	FontName      string
	Shuffle       bool
}

// CommandRunner executes an external binary and returns stdout or an error containing stderr.
type CommandRunner func(name string, args ...string) ([]byte, error)

// ExecCommandRunner invokes an external binary via os/exec and includes stderr in returned errors.
func ExecCommandRunner(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("%s failed: %w\nstderr:\n%s", name, err, errMsg)
		}
		return nil, fmt.Errorf("%s failed: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// IsSupportedBackgroundExt reports whether ext is a supported Background Visual extension.
func IsSupportedBackgroundExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp", ".mp4", ".mov", ".gif":
		return true
	default:
		return false
	}
}

// ValidateConfig validates CLI configuration and checks ffmpeg/ffprobe availability.
func ValidateConfig(cfg Config, lookPath func(string) (string, error)) error {
	if strings.TrimSpace(cfg.BG) == "" {
		return errors.New("--bg is required (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)")
	}
	if !IsSupportedBackgroundExt(filepath.Ext(cfg.BG)) {
		return fmt.Errorf("unsupported Background Visual extension %q (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)", filepath.Ext(cfg.BG))
	}
	if math.IsNaN(cfg.Crossfade) || math.IsInf(cfg.Crossfade, 0) || cfg.Crossfade < 0 {
		return fmt.Errorf("--crossfade must be >= 0 (got %v)", cfg.Crossfade)
	}
	if cfg.TitleDuration < 0 {
		return fmt.Errorf("--title-duration must be >= 0 (got %d)", cfg.TitleDuration)
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := lookPath(bin); err != nil {
			return fmt.Errorf("required binary %q not found on PATH: %w", bin, err)
		}
	}
	return nil
}

// ProbeTracks discovers .mp3 files in audioDir and probes each Track's duration and Display Title.
func ProbeTracks(audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner CommandRunner) ([]Track, error) {
	files, err := probe.DiscoverAudioFiles(audioDir, shuffle, shuffleFn)
	if err != nil {
		return nil, err
	}

	tracks := make([]Track, 0, len(files))
	for _, path := range files {
		dur, id3Title, err := probe.ProbeFile(path, runner)
		if err != nil {
			return nil, err
		}
		tracks = append(tracks, Track{
			Path:         path,
			FileName:     filepath.Base(path),
			Duration:     dur,
			DisplayTitle: probe.ResolveDisplayTitle(id3Title, path),
		})
	}
	return tracks, nil
}

// Run executes the ytmix Mix pipeline.
func Run(cfg Config, stdout io.Writer, lookPath func(string) (string, error), runner CommandRunner) error {
	start := time.Now()
	if err := ValidateConfig(cfg, lookPath); err != nil {
		return err
	}
	bgInfo, err := os.Stat(cfg.BG)
	if err != nil {
		return fmt.Errorf("cannot access Background Visual %q: %w", cfg.BG, err)
	}
	if bgInfo.IsDir() {
		return fmt.Errorf("Background Visual path %q is a directory, expected a file", cfg.BG)
	}

	tempDir, err := os.MkdirTemp("", "ytmix-*")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	fmt.Fprintf(stdout, "[1/5] Probing Tracks in %s...\n", cfg.AudioDir)
	tracks, err := ProbeTracks(cfg.AudioDir, cfg.Shuffle, nil, runner)
	if err != nil {
		return err
	}

	paths := make([]string, len(tracks))
	fileNames := make([]string, len(tracks))
	durations := make([]float64, len(tracks))
	for i, tr := range tracks {
		paths[i] = tr.Path
		fileNames[i] = tr.FileName
		durations[i] = tr.Duration
	}

	fmt.Fprintf(stdout, "[2/5] Processing & merging %d Track(s) (normalize=%v, crossfade=%.2fs)...\n", len(tracks), cfg.Normalize, cfg.Crossfade)
	mergedAudioPath, updatedPaths, updatedDurations, err := audio.ProcessAndMerge(paths, fileNames, durations, tempDir, cfg.Normalize, cfg.Crossfade, runner)
	if err != nil {
		return err
	}
	_ = mergedAudioPath
	for i := range tracks {
		tracks[i].Path = updatedPaths[i]
		tracks[i].Duration = updatedDurations[i]
	}

	titles := make([]string, len(tracks))
	for i, tr := range tracks {
		titles[i] = tr.DisplayTitle
	}

	fmt.Fprintf(stdout, "[3/5] Writing Chapter Timestamps to %s...\n", cfg.Timestamps)
	sched, err := subtitle.WriteChapters(cfg.Timestamps, titles, updatedDurations, cfg.Crossfade)
	if err != nil {
		return err
	}

	if cfg.OverlayTitles {
		fmt.Fprintf(stdout, "[4/5] Title Overlays requested (pending Phase 2)...\n")
	} else {
		fmt.Fprintf(stdout, "[4/5] Skipping Title Overlays (--overlay-titles=false).\n")
	}
	fmt.Fprintf(stdout, "[5/5] Video rendering to %s pending Phase 2 (Mix audio duration %.2fs, elapsed %s).\n",
		cfg.Out, sched.TotalDuration, time.Since(start).Round(time.Millisecond))

	return fmt.Errorf("steps 1-3 complete (Chapter Timestamps written to %s); subtitle and video rendering (steps 4-5) pending Phase 2", cfg.Timestamps)
}
