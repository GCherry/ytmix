package mixer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GCherry/ytmix/internal/audio"
	"github.com/GCherry/ytmix/internal/probe"
	"github.com/GCherry/ytmix/internal/render"
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
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecCommandRunner invokes an external binary via os/exec and includes stderr in returned errors.
func ExecCommandRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// #nosec G204 -- ADR-0001: intentional ffmpeg/ffprobe subprocess wrapper; callers pass hardcoded binary names and discrete arg slices.
	cmd := exec.CommandContext(ctx, name, args...)
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
func ProbeTracks(ctx context.Context, audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner CommandRunner) ([]Track, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	files, err := probe.DiscoverAudioFiles(audioDir, shuffle, shuffleFn)
	if err != nil {
		return nil, err
	}

	ctxRunner := func(name string, args ...string) ([]byte, error) {
		return runner(ctx, name, args...)
	}

	tracks := make([]Track, 0, len(files))
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dur, id3Title, err := probe.ProbeFile(path, ctxRunner)
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

// Run executes the full 5-step ytmix Mix pipeline.
func Run(ctx context.Context, cfg Config, stdout io.Writer, lookPath func(string) (string, error), runner CommandRunner) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	start := time.Now()
	if err := ValidateConfig(cfg, lookPath); err != nil {
		return err
	}
	bgInfo, err := os.Stat(cfg.BG)
	if err != nil {
		return fmt.Errorf("cannot access Background Visual %q: %w", cfg.BG, err)
	}
	if bgInfo.IsDir() {
		return fmt.Errorf("background visual path %q is a directory, expected a file", cfg.BG)
	}

	tempDir, err := os.MkdirTemp("", "ytmix-*")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	ctxRunner := func(name string, args ...string) ([]byte, error) {
		return runner(ctx, name, args...)
	}

	fmt.Fprintf(stdout, "[1/5] Probing Tracks in %s...\n", cfg.AudioDir)
	tracks, err := ProbeTracks(ctx, cfg.AudioDir, cfg.Shuffle, nil, runner)
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
	mergedAudioPath, updatedPaths, updatedDurations, err := audio.ProcessAndMerge(paths, fileNames, durations, tempDir, cfg.Normalize, cfg.Crossfade, ctxRunner)
	if err != nil {
		return err
	}
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

	var assPath string
	if cfg.OverlayTitles {
		assPath = filepath.Join(tempDir, "titles.ass")
		fmt.Fprintf(stdout, "[4/5] Generating Title Overlays (.ass) at %s...\n", assPath)
		if err := subtitle.WriteASS(assPath, titles, sched.Starts, sched.TotalDuration, cfg.TitleDuration, cfg.FontName); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "[4/5] Skipping Title Overlays (--overlay-titles=false).\n")
	}

	fmt.Fprintf(stdout, "[5/5] Rendering Mix video to %s...\n", cfg.Out)
	pathKind, err := render.Render(cfg.BG, mergedAudioPath, assPath, tempDir, cfg.Out, cfg.OverlayTitles, runtime.GOOS, ctxRunner)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Done! Rendered %s via %s in %s (audio duration %.2fs, chapters: %s).\n",
		cfg.Out, pathKind, time.Since(start).Round(time.Millisecond), sched.TotalDuration, cfg.Timestamps)
	return nil
}
