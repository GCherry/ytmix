package mixer

import (
	"bufio"
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

// StreamRunner executes an external binary and streams each stdout line to onStdoutLine.
type StreamRunner func(ctx context.Context, onStdoutLine func(string), name string, args ...string) error

// EventKind classifies structured pipeline telemetry emitted by mixer.Run and mixer.RunWithTracks.
type EventKind string

const (
	EventStep     EventKind = "step"
	EventProgress EventKind = "progress"
	EventDone     EventKind = "done"
)

// TelemetryEvent describes a structured pipeline milestone, live render progress sample, or completion event.
type TelemetryEvent struct {
	Kind             EventKind
	Step             int
	TotalSteps       int
	Progress         int
	Message          string
	Detail           string
	Substage         string
	OutTimeSec       float64
	TotalDurationSec float64
	Speed            string
}

// TelemetrySink receives structured telemetry events during a Mix build.
type TelemetrySink func(TelemetryEvent)

// NewCLITelemetry returns a TelemetrySink that formats clean [1/5]–[5/5] milestones for w,
// overwriting a single Step 5 status line when isTTY is true.
func NewCLITelemetry(w io.Writer, isTTY bool) TelemetrySink {
	if w == nil {
		return nil
	}
	var dirtyTTY bool
	var lastPct int
	var lastEmit time.Time
	return func(ev TelemetryEvent) {
		switch ev.Kind {
		case EventStep:
			if dirtyTTY {
				fmt.Fprintln(w)
				dirtyTTY = false
			}
			fmt.Fprintln(w, ev.Message)
		case EventProgress:
			if !isTTY {
				return
			}
			now := time.Now()
			if ev.Progress == lastPct && now.Sub(lastEmit) < 250*time.Millisecond {
				return
			}
			lastPct = ev.Progress
			lastEmit = now
			speedPart := ""
			if ev.Speed != "" {
				speedPart = fmt.Sprintf(" (%s)", ev.Speed)
			}
			fmt.Fprintf(w, "\r[5/5] Rendering Mix... %d%%%s", ev.Progress, speedPart)
			dirtyTTY = true
		case EventDone:
			if dirtyTTY {
				fmt.Fprintln(w)
				dirtyTTY = false
			}
			fmt.Fprintln(w, ev.Message)
		}
	}
}

func emitTelemetry(sink TelemetrySink, ev TelemetryEvent) {
	if sink != nil {
		sink(ev)
	}
}

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

// ExecStreamRunner invokes an external binary via os/exec, streaming stdout lines to onStdoutLine
// while capturing stderr for failure reporting.
func ExecStreamRunner(ctx context.Context, onStdoutLine func(string), name string, args ...string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// #nosec G204 -- ADR-0001: intentional ffmpeg/ffprobe subprocess wrapper; callers pass hardcoded binary names and discrete arg slices.
	cmd := exec.CommandContext(ctx, name, args...)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s stdout pipe: %w", name, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s start failed: %w", name, err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	for scanner.Scan() {
		if onStdoutLine != nil {
			onStdoutLine(scanner.Text())
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if waitErr != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return fmt.Errorf("%s failed: %w\nstderr:\n%s", name, waitErr, errMsg)
		}
		return fmt.Errorf("%s failed: %w", name, waitErr)
	}
	if scanErr != nil {
		return fmt.Errorf("%s read stdout: %w", name, scanErr)
	}
	return nil
}

// ValidateConfig validates CLI configuration and checks ffmpeg/ffprobe availability.
func ValidateConfig(cfg Config, lookPath func(string) (string, error)) error {
	if strings.TrimSpace(cfg.BG) == "" {
		return errors.New("--bg is required (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)")
	}
	if _, _, err := render.ClassifyBackground(cfg.BG); err != nil {
		return err
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

// ValidateBackgroundFile verifies bgPath exists and references a regular file rather than a directory.
func ValidateBackgroundFile(bgPath string) error {
	// #nosec G703 -- callers validate and confine bgPath (CLI flag or ui allowedRoots/server temp file) before preflight.
	bgInfo, err := os.Stat(bgPath)
	if err != nil {
		return fmt.Errorf("cannot access Background Visual %q: %w", bgPath, err)
	}
	if bgInfo.IsDir() {
		return fmt.Errorf("background visual path %q is a directory, expected a file", bgPath)
	}
	return nil
}

// PreflightConfig validates cfg, checks Background Visual file accessibility, selects the Rendering Path,
// and optionally prepares output directories and writability checks.
func PreflightConfig(cfg Config, lookPath func(string) (string, error), prepareOutputs bool) (Config, render.Path, error) {
	if err := ValidateConfig(cfg, lookPath); err != nil {
		return cfg, "", err
	}
	if err := ValidateBackgroundFile(cfg.BG); err != nil {
		return cfg, "", err
	}
	pathKind, err := render.SelectPath(cfg.BG, cfg.OverlayTitles)
	if err != nil {
		return cfg, "", err
	}
	if prepareOutputs {
		preparedCfg, err := PrepareOutputTargets(cfg)
		if err != nil {
			return cfg, "", err
		}
		cfg = preparedCfg
	}
	return cfg, pathKind, nil
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
	tracks := make([]Track, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dur, id3Title, err := probe.ProbeFile(ctx, f.Path, runner)
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

func prepareRun(ctx context.Context, cfg Config, lookPath func(string) (string, error)) (context.Context, Config, time.Time, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ctx, cfg, time.Time{}, err
	}

	start := time.Now()
	preparedCfg, _, err := PreflightConfig(cfg, lookPath, true)
	if err != nil {
		return ctx, cfg, start, err
	}
	return ctx, preparedCfg, start, nil
}

func emitStep(sink TelemetrySink, step, total, progress int, detail string) {
	emitTelemetry(sink, TelemetryEvent{
		Kind:       EventStep,
		Step:       step,
		TotalSteps: total,
		Progress:   progress,
		Message:    fmt.Sprintf("[%d/%d] %s", step, total, detail),
		Detail:     detail,
	})
}

// Run executes the full 5-step ytmix Mix pipeline from cfg.AudioDir.
func Run(ctx context.Context, cfg Config, sink TelemetrySink, lookPath func(string) (string, error), runner CommandRunner, streamRunner StreamRunner) error {
	ctx, cfg, start, err := prepareRun(ctx, cfg, lookPath)
	if err != nil {
		return err
	}

	emitStep(sink, 1, 5, 5, fmt.Sprintf("Probing Tracks in %s...", cfg.AudioDir))
	tracks, err := ProbeTracks(ctx, cfg.AudioDir, cfg.Shuffle, nil, runner)
	if err != nil {
		return err
	}
	return runPipeline(ctx, cfg, tracks, sink, start, runner, streamRunner)
}

// RunWithTracks executes the 5-step ytmix Mix pipeline using an explicit, caller-ordered Track slice.
func RunWithTracks(ctx context.Context, cfg Config, tracks []Track, sink TelemetrySink, lookPath func(string) (string, error), runner CommandRunner, streamRunner StreamRunner) error {
	ctx, cfg, start, err := prepareRun(ctx, cfg, lookPath)
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

	emitStep(sink, 1, 5, 5, fmt.Sprintf("Using %d selected Track(s)...", len(resolved)))
	return runPipeline(ctx, cfg, resolved, sink, start, runner, streamRunner)
}

func runPipeline(ctx context.Context, cfg Config, tracks []Track, sink TelemetrySink, start time.Time, runner CommandRunner, streamRunner StreamRunner) error {
	tempDir, err := os.MkdirTemp("", "ytmix-*")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	paths := make([]string, len(tracks))
	fileNames := make([]string, len(tracks))
	durations := make([]float64, len(tracks))
	for i, tr := range tracks {
		paths[i] = tr.Path
		fileNames[i] = tr.FileName
		durations[i] = tr.Duration
	}

	emitStep(sink, 2, 5, 10, fmt.Sprintf("Processing & merging %d Track(s) (normalize=%v, crossfade=%.2fs)...", len(tracks), cfg.Normalize, cfg.Crossfade))
	mergedAudioPath, updatedPaths, updatedDurations, err := audio.ProcessAndMerge(ctx, paths, fileNames, durations, tempDir, cfg.Normalize, cfg.Crossfade, runner)
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

	emitStep(sink, 3, 5, 15, fmt.Sprintf("Writing Chapter Timestamps to %s...", cfg.Timestamps))
	sched, err := subtitle.WriteChapters(cfg.Timestamps, titles, updatedDurations, cfg.Crossfade)
	if err != nil {
		return err
	}

	var assPath string
	if cfg.OverlayTitles {
		assPath = filepath.Join(tempDir, "titles.ass")
		emitStep(sink, 4, 5, 20, fmt.Sprintf("Generating Title Overlays (.ass) at %s...", assPath))
		if err := subtitle.WriteASS(assPath, titles, sched.Starts, sched.TotalDuration, cfg.TitleDuration, cfg.FontName); err != nil {
			return err
		}
	} else {
		emitStep(sink, 4, 5, 20, "Skipping Title Overlays (--overlay-titles=false).")
	}

	emitStep(sink, 5, 5, 20, fmt.Sprintf("Rendering Mix video to %s...", cfg.Out))
	var renderStream render.StreamRunner
	if streamRunner != nil {
		renderStream = render.StreamRunner(streamRunner)
	}
	var onRenderProgress render.ProgressFunc
	if sink != nil {
		onRenderProgress = func(u render.ProgressUpdate) {
			pct := 20 + int(math.Round(u.Fraction*79))
			if pct < 20 {
				pct = 20
			}
			if pct > 99 {
				pct = 99
			}
			emitTelemetry(sink, TelemetryEvent{
				Kind:             EventProgress,
				Step:             5,
				TotalSteps:       5,
				Progress:         pct,
				Substage:         u.Substage,
				OutTimeSec:       u.OutTimeSec,
				TotalDurationSec: sched.TotalDuration,
				Speed:            u.Speed,
			})
		}
	}
	pathKind, err := render.Render(ctx, cfg.BG, mergedAudioPath, assPath, tempDir, cfg.Out, cfg.OverlayTitles, sched.TotalDuration, runtime.GOOS, runner, renderStream, onRenderProgress)
	if err != nil {
		return err
	}

	doneMsg := fmt.Sprintf("Done! Rendered %s via %s in %s (audio duration %.2fs, chapters: %s).",
		cfg.Out, pathKind, time.Since(start).Round(time.Millisecond), sched.TotalDuration, cfg.Timestamps)
	emitTelemetry(sink, TelemetryEvent{
		Kind:             EventDone,
		Step:             5,
		TotalSteps:       5,
		Progress:         100,
		Message:          doneMsg,
		TotalDurationSec: sched.TotalDuration,
	})
	return nil
}
