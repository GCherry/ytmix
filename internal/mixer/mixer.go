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
	OutDir        string
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
	return probeFiles(ctx, files, runner)
}

func validateTrackFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("track file path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access Track %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("track path %q is a directory, expected an .mp3 file", path)
	}
	if !strings.EqualFold(filepath.Ext(path), ".mp3") {
		return fmt.Errorf("unsupported Track extension %q for %q (expected .mp3)", filepath.Ext(path), filepath.Base(path))
	}
	return nil
}

// NamedTrackFile pairs a local .mp3 file path with its original display filename.
type NamedTrackFile struct {
	Path     string
	FileName string
}

// ProbeTrackFiles probes an ordered list of .mp3 file paths without re-scanning or re-sorting a directory.
func ProbeTrackFiles(ctx context.Context, files []string, runner CommandRunner) ([]Track, error) {
	named := make([]NamedTrackFile, len(files))
	for i, p := range files {
		named[i] = NamedTrackFile{Path: p, FileName: filepath.Base(p)}
	}
	return ProbeNamedTrackFiles(ctx, named, runner)
}

// ProbeNamedTrackFiles probes an ordered list of .mp3 files while resolving Display Titles from FileName.
func ProbeNamedTrackFiles(ctx context.Context, files []NamedTrackFile, runner CommandRunner) ([]Track, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(files) < 1 {
		return nil, errors.New("at least one .mp3 Track is required")
	}
	for _, f := range files {
		if err := validateTrackFile(f.Path); err != nil {
			return nil, err
		}
	}
	return probeNamedFiles(ctx, files, runner)
}

func probeFiles(ctx context.Context, files []string, runner CommandRunner) ([]Track, error) {
	named := make([]NamedTrackFile, len(files))
	for i, p := range files {
		named[i] = NamedTrackFile{Path: p, FileName: filepath.Base(p)}
	}
	return probeNamedFiles(ctx, named, runner)
}

func probeNamedFiles(ctx context.Context, files []NamedTrackFile, runner CommandRunner) ([]Track, error) {
	ctxRunner := func(name string, args ...string) ([]byte, error) {
		return runner(ctx, name, args...)
	}

	tracks := make([]Track, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dur, id3Title, err := probe.ProbeFile(f.Path, ctxRunner)
		if err != nil {
			return nil, err
		}
		displaySource := f.FileName
		if strings.TrimSpace(displaySource) == "" {
			displaySource = filepath.Base(f.Path)
		}
		tracks = append(tracks, Track{
			Path:         f.Path,
			FileName:     displaySource,
			Duration:     dur,
			DisplayTitle: probe.ResolveDisplayTitle(id3Title, displaySource),
		})
	}
	return tracks, nil
}

// ResolveOutputPaths resolves effective --out and --timestamps file paths using --out-dir when set.
// Relative --out and --timestamps paths resolve inside --out-dir, while absolute paths override it.
func ResolveOutputPaths(cfg Config) (string, string, error) {
	if strings.ContainsRune(cfg.OutDir, '\x00') || strings.ContainsRune(cfg.Out, '\x00') || strings.ContainsRune(cfg.Timestamps, '\x00') {
		return "", "", errors.New("output path must not contain null bytes")
	}

	outDir := strings.TrimSpace(cfg.OutDir)
	rawOut := strings.TrimSpace(cfg.Out)
	if rawOut == "" {
		rawOut = "output.mp4"
	}
	if hasTrailingPathSeparator(rawOut) {
		return "", "", fmt.Errorf("--out path %q must reference a file, not a directory", rawOut)
	}

	rawTS := strings.TrimSpace(cfg.Timestamps)
	if rawTS == "" {
		rawTS = "timestamps.txt"
	}
	if hasTrailingPathSeparator(rawTS) {
		return "", "", fmt.Errorf("--timestamps path %q must reference a file, not a directory", rawTS)
	}

	outPath := rawOut
	tsPath := rawTS
	if outDir != "" {
		if !filepath.IsAbs(outPath) {
			outPath = filepath.Join(outDir, outPath)
		}
		if !filepath.IsAbs(tsPath) {
			tsPath = filepath.Join(outDir, tsPath)
		}
	}

	outPath = filepath.Clean(outPath)
	tsPath = filepath.Clean(tsPath)
	if outPath == "." || outPath == ".." {
		return "", "", fmt.Errorf("--out path %q must reference a file, not a directory", rawOut)
	}
	if tsPath == "." || tsPath == ".." {
		return "", "", fmt.Errorf("--timestamps path %q must reference a file, not a directory", rawTS)
	}
	return outPath, tsPath, nil
}

func hasTrailingPathSeparator(p string) bool {
	return strings.HasSuffix(p, "/") || strings.HasSuffix(p, `\`)
}

// PrepareOutputTargets resolves --out and --timestamps, validates they do not target existing
// directories, creates missing parent directories, and verifies writability upfront.
func PrepareOutputTargets(cfg Config) (Config, error) {
	if outDir := strings.TrimSpace(cfg.OutDir); outDir != "" {
		if info, err := os.Stat(outDir); err == nil && !info.IsDir() {
			return cfg, fmt.Errorf("--out-dir path %q exists and is not a directory", outDir)
		}
	}

	outPath, tsPath, err := ResolveOutputPaths(cfg)
	if err != nil {
		return cfg, err
	}
	if err := ensureOutputTarget("--out", outPath); err != nil {
		return cfg, err
	}
	if err := ensureOutputTarget("--timestamps", tsPath); err != nil {
		return cfg, err
	}

	cfg.Out = outPath
	cfg.Timestamps = tsPath
	return cfg, nil
}

func ensureOutputTarget(flagName, targetPath string) error {
	if info, err := os.Stat(targetPath); err == nil && info.IsDir() {
		return fmt.Errorf("%s path %q is a directory, expected a file", flagName, targetPath)
	}

	parent := filepath.Dir(targetPath)
	if pInfo, err := os.Stat(parent); err == nil && !pInfo.IsDir() {
		return fmt.Errorf("parent path %q for %s exists and is not a directory", parent, flagName)
	}
	// #nosec G301 -- user-facing output directory uses standard 0755 permissions.
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("create parent directory %q for %s: %w", parent, flagName, err)
	}

	probeFile, err := os.CreateTemp(parent, ".ytmix-write-check-*")
	if err != nil {
		return fmt.Errorf("output directory %q for %s is not writable: %w", parent, flagName, err)
	}
	probeName := probeFile.Name()
	_ = probeFile.Close()
	_ = os.Remove(probeName)
	return nil
}

func prepareRun(ctx context.Context, cfg Config, stdout io.Writer, lookPath func(string) (string, error)) (context.Context, Config, io.Writer, time.Time, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if err := ctx.Err(); err != nil {
		return ctx, cfg, stdout, time.Time{}, err
	}

	start := time.Now()
	if err := ValidateConfig(cfg, lookPath); err != nil {
		return ctx, cfg, stdout, start, err
	}
	bgInfo, err := os.Stat(cfg.BG)
	if err != nil {
		return ctx, cfg, stdout, start, fmt.Errorf("cannot access Background Visual %q: %w", cfg.BG, err)
	}
	if bgInfo.IsDir() {
		return ctx, cfg, stdout, start, fmt.Errorf("background visual path %q is a directory, expected a file", cfg.BG)
	}
	preparedCfg, err := PrepareOutputTargets(cfg)
	if err != nil {
		return ctx, cfg, stdout, start, err
	}
	return ctx, preparedCfg, stdout, start, nil
}

// Run executes the full 5-step ytmix Mix pipeline from cfg.AudioDir.
func Run(ctx context.Context, cfg Config, stdout io.Writer, lookPath func(string) (string, error), runner CommandRunner) error {
	ctx, cfg, stdout, start, err := prepareRun(ctx, cfg, stdout, lookPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "[1/5] Probing Tracks in %s...\n", cfg.AudioDir)
	tracks, err := ProbeTracks(ctx, cfg.AudioDir, cfg.Shuffle, nil, runner)
	if err != nil {
		return err
	}
	return runPipeline(ctx, cfg, tracks, stdout, start, runner)
}

// RunWithTracks executes the 5-step ytmix Mix pipeline using an explicit, caller-ordered Track slice.
func RunWithTracks(ctx context.Context, cfg Config, tracks []Track, stdout io.Writer, lookPath func(string) (string, error), runner CommandRunner) error {
	ctx, cfg, stdout, start, err := prepareRun(ctx, cfg, stdout, lookPath)
	if err != nil {
		return err
	}
	if len(tracks) < 1 {
		return errors.New("at least one .mp3 Track is required")
	}

	resolved := make([]Track, len(tracks))
	for i, tr := range tracks {
		if err := validateTrackFile(tr.Path); err != nil {
			return fmt.Errorf("track %d: %w", i+1, err)
		}
		if tr.FileName == "" {
			tr.FileName = filepath.Base(tr.Path)
		}
		if tr.Duration <= 0 || math.IsNaN(tr.Duration) || math.IsInf(tr.Duration, 0) {
			return fmt.Errorf("track %q must have a positive duration (got %v)", tr.FileName, tr.Duration)
		}
		if strings.TrimSpace(tr.DisplayTitle) == "" {
			tr.DisplayTitle = probe.ResolveDisplayTitle("", tr.Path)
		}
		resolved[i] = tr
	}

	fmt.Fprintf(stdout, "[1/5] Using %d selected Track(s)...\n", len(resolved))
	return runPipeline(ctx, cfg, resolved, stdout, start, runner)
}

func runPipeline(ctx context.Context, cfg Config, tracks []Track, stdout io.Writer, start time.Time, runner CommandRunner) error {
	tempDir, err := os.MkdirTemp("", "ytmix-*")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	ctxRunner := func(name string, args ...string) ([]byte, error) {
		return runner(ctx, name, args...)
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
