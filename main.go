package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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

func execCommandRunner(name string, args ...string) ([]byte, error) {
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

func newFlagSet(cfg *Config, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ytmix", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&cfg.AudioDir, "audio-dir", "./tracks", "Directory containing input .mp3 files.")
	fs.StringVar(&cfg.BG, "bg", "", "Path to Background Visual (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif).")
	fs.StringVar(&cfg.Out, "out", "output.mp4", "Output .mp4 video path.")
	fs.StringVar(&cfg.Timestamps, "timestamps", "timestamps.txt", "Output file path for YouTube Chapter Timestamps.")
	fs.Float64Var(&cfg.Crossfade, "crossfade", 0, "Crossfade duration in seconds between Tracks.")
	fs.BoolVar(&cfg.Normalize, "normalize", false, "Normalize each Track to -14 LUFS.")
	fs.BoolVar(&cfg.OverlayTitles, "overlay-titles", false, "Burn Display Titles into the bottom-left corner via .ass subtitles.")
	fs.IntVar(&cfg.TitleDuration, "title-duration", 0, "Seconds to display each Display Title (0 = full Track).")
	fs.StringVar(&cfg.FontName, "font-name", "Arial", "Font family name used in the .ass subtitle script.")
	fs.BoolVar(&cfg.Shuffle, "shuffle", false, "Randomize Track order instead of sorting alphabetically.")
	return fs
}

func parseFlags(args []string) (Config, error) {
	var cfg Config
	fs := newFlagSet(&cfg, io.Discard)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func isSupportedBackgroundExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".webp", ".mp4", ".mov", ".gif":
		return true
	default:
		return false
	}
}

func validateConfig(cfg Config, lookPath func(string) (string, error)) error {
	if strings.TrimSpace(cfg.BG) == "" {
		return errors.New("--bg is required (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)")
	}
	if !isSupportedBackgroundExt(filepath.Ext(cfg.BG)) {
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

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs := newFlagSet(&Config{}, os.Stderr)
			fs.Usage()
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}
	if err := run(cfg, os.Stdout, exec.LookPath, execCommandRunner); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cfg Config, stdout io.Writer, lookPath func(string) (string, error), runner CommandRunner) error {
	start := time.Now()
	if err := validateConfig(cfg, lookPath); err != nil {
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
	tracks, err := probeTracks(cfg.AudioDir, cfg.Shuffle, nil, runner)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "[2/5] Processing & merging %d Track(s) (normalize=%v, crossfade=%.2fs)...\n", len(tracks), cfg.Normalize, cfg.Crossfade)
	mergedAudioPath, tracks, err := processAndMergeAudio(tracks, tempDir, cfg.Normalize, cfg.Crossfade, runner)
	if err != nil {
		return err
	}
	_ = mergedAudioPath

	fmt.Fprintf(stdout, "[3/5] Writing Chapter Timestamps to %s...\n", cfg.Timestamps)
	sched, err := writeChapters(cfg.Timestamps, tracks, cfg.Crossfade)
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
