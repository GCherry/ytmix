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
	AudioDir             string
	BG                   string
	OutDir               string
	Out                  string
	Timestamps           string
	Crossfade            float64
	Normalize            bool
	OverlayTitles        bool
	TitleDuration        int
	FontName             string
	Shuffle              bool
	Visualizer           string
	VisualizerTheme      string
	VisualizerResolution string
	VisualizerGlow       bool
	VisualizerBrightness int
	FadeToBlack          time.Duration
	DimTitles            bool
}

// VisualizerConfig constructs the Path D Auto Visualizer configuration implied by c.
func (c Config) VisualizerConfig() render.VisualizerConfig {
	return render.VisualizerConfig{
		Preset:      c.Visualizer,
		Theme:       c.VisualizerTheme,
		Resolution:  c.VisualizerResolution,
		Glow:        c.VisualizerGlow,
		Brightness:  c.VisualizerBrightness,
		FadeToBlack: c.FadeToBlack,
	}
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
	Kind              EventKind
	Step              int
	TotalSteps        int
	Progress          int
	Message           string
	Detail            string
	Substage          string
	OutTimeSec        float64
	TotalDurationSec  float64
	Speed             string
	RemainingSec      float64
	EstimatedTotalSec float64
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
	bg := strings.TrimSpace(cfg.BG)
	viz := strings.TrimSpace(cfg.Visualizer)
	if bg != "" {
		if viz != "" {
			return errors.New("--bg and --visualizer cannot be used together (omit --bg for Path D Auto Visualizer, or omit --visualizer to use a Background Visual file)")
		}
		if _, _, err := render.ClassifyBackground(bg); err != nil {
			return err
		}
	} else {
		if _, err := render.ValidateVisualizerConfig(cfg.VisualizerConfig()); err != nil {
			return err
		}
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

// PreflightConfig validates cfg, checks Background Visual file accessibility when provided, selects the Rendering Path,
// and optionally prepares output directories and writability checks.
func PreflightConfig(cfg Config, lookPath func(string) (string, error), prepareOutputs bool) (Config, render.Path, error) {
	if err := ValidateConfig(cfg, lookPath); err != nil {
		return cfg, "", err
	}
	if strings.TrimSpace(cfg.BG) != "" {
		if err := ValidateBackgroundFile(cfg.BG); err != nil {
			return cfg, "", err
		}
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

// ValidateVisualizerFilters verifies that all FFmpeg filters required for cfg's Path D preset exist.
// When cachedFilters is nil, it probes `ffmpeg -hide_banner -filters` via runner.
func ValidateVisualizerFilters(ctx context.Context, cfg Config, runner CommandRunner, cachedFilters map[string]bool) error {
	vizCfg, err := render.ValidateVisualizerConfig(cfg.VisualizerConfig())
	if err != nil {
		return err
	}
	filters := cachedFilters
	if filters == nil {
		if runner == nil {
			return errors.New("command runner is required to probe FFmpeg visualizer filters")
		}
		filters, err = render.DetectFilters(ctx, runner)
		if err != nil {
			return err
		}
	}
	if ok, missing := render.PresetAvailable(vizCfg.Preset, filters); !ok {
		return fmt.Errorf("auto visualizer %q requires FFmpeg filter %q which is not available in this ffmpeg build (run --list-visualizers to inspect available presets)", vizCfg.Preset, missing)
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

func prepareRun(ctx context.Context, cfg Config, lookPath func(string) (string, error), runner CommandRunner) (context.Context, Config, time.Time, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ctx, cfg, time.Time{}, err
	}

	start := time.Now()
	preparedCfg, pathKind, err := PreflightConfig(cfg, lookPath, false)
	if err != nil {
		return ctx, cfg, start, err
	}
	if pathKind == render.PathD && runner != nil {
		if err := ValidateVisualizerFilters(ctx, preparedCfg, runner, nil); err != nil {
			return ctx, cfg, start, err
		}
	}
	preparedCfg, err = PrepareOutputTargets(preparedCfg)
	if err != nil {
		return ctx, cfg, start, err
	}
	return ctx, preparedCfg, start, nil
}

func emitStep(sink TelemetrySink, step, total, progress int, remainingSec, estTotalSec float64, detail string) {
	emitTelemetry(sink, TelemetryEvent{
		Kind:              EventStep,
		Step:              step,
		TotalSteps:        total,
		Progress:          progress,
		Message:           fmt.Sprintf("[%d/%d] %s", step, total, detail),
		Detail:            detail,
		RemainingSec:      remainingSec,
		EstimatedTotalSec: estTotalSec,
	})
}

type stageBounds struct {
	step2Start int
	audioSpan  int
	step3Start int
	step4Start int
	step5Start int
	step5Span  int
}

func computeStageBounds(weights render.StageWeights) stageBounds {
	audioSpan := int(math.Round(93.0 * weights.Audio))
	if audioSpan < 4 {
		audioSpan = 4
	}
	if audioSpan > 89 {
		audioSpan = 89
	}
	step2Start := 4
	step3Start := step2Start + audioSpan
	step4Start := step3Start + 1
	step5Start := step4Start + 1
	return stageBounds{
		step2Start: step2Start,
		audioSpan:  audioSpan,
		step3Start: step3Start,
		step4Start: step4Start,
		step5Start: step5Start,
		step5Span:  99 - step5Start,
	}
}

func buildEstimateContext(
	ctx context.Context,
	cfg Config,
	tracks []Track,
	bgMeta *probe.BackgroundMeta,
	runner CommandRunner,
) (render.MixEstimate, *render.EncoderInfo, string, map[string]render.CalibrationEntry) {
	pathKind, err := render.SelectPath(cfg.BG, cfg.OverlayTitles)
	if err != nil {
		pathKind = render.PathD
	}

	var totalAudioSec float64
	durations := make([]float64, len(tracks))
	titles := make([]string, len(tracks))
	for i, tr := range tracks {
		totalAudioSec += tr.Duration
		durations[i] = tr.Duration
		titles[i] = tr.DisplayTitle
	}
	mixDurSec := totalAudioSec
	if len(tracks) > 0 {
		sched := subtitle.BuildChapterSchedule(titles, durations, cfg.Crossfade)
		mixDurSec = sched.TotalDuration
	}

	var resolvedBG probe.BackgroundMeta
	if bgMeta != nil {
		resolvedBG = *bgMeta
	} else if runner != nil && strings.TrimSpace(cfg.BG) != "" && (pathKind == render.PathA || pathKind == render.PathB) {
		if probed, err := probe.ProbeBackgroundVisual(ctx, cfg.BG, runner); err == nil {
			resolvedBG = probed
		}
	}

	vizCfg := cfg.VisualizerConfig()
	var encPtr *render.EncoderInfo
	encName := "libx264"
	switch pathKind {
	case render.PathB:
		if runner != nil {
			enc := render.DetectEncoder(ctx, runtime.GOOS, runner)
			encPtr = &enc
			encName = enc.Name
		}
	case render.PathD:
		preset := strings.ToLower(strings.TrimSpace(vizCfg.Preset))
		if preset == "" {
			preset = render.DefaultVisualizerPreset
		}
		if preset != "black" && runner != nil {
			enc := render.DetectPathDEncoder(ctx, runtime.GOOS, vizCfg.Resolution, runner)
			encPtr = &enc
			encName = enc.Name
		}
	}

	calPath, _ := DefaultCalibrationPath()
	cal := LoadCalibration(calPath)

	est := render.EstimateMix(render.EstimateInput{
		Path:           pathKind,
		EncoderName:    encName,
		MixDurationSec: mixDurSec,
		TotalAudioSec:  totalAudioSec,
		TrackCount:     len(tracks),
		Normalize:      cfg.Normalize,
		Crossfade:      cfg.Crossfade,
		OverlayTitles:  cfg.OverlayTitles,
		Visualizer:     vizCfg,
		CPUCores:       runtime.NumCPU(),
		BG: render.BackgroundEstimateInput{
			DurationSec: resolvedBG.DurationSec,
			FPS:         resolvedBG.FPS,
			Width:       resolvedBG.Width,
			Height:      resolvedBG.Height,
			CodecName:   resolvedBG.CodecName,
			PixFmt:      resolvedBG.PixFmt,
		},
	}, cal)

	return est, encPtr, calPath, cal
}

// EstimateConfig computes a pre-render wall-time estimate for cfg and tracks without mutating files.
func EstimateConfig(
	ctx context.Context,
	cfg Config,
	tracks []Track,
	bgMeta *probe.BackgroundMeta,
	lookPath func(string) (string, error),
	runner CommandRunner,
) (render.MixEstimate, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	preparedCfg, pathKind, err := PreflightConfig(cfg, lookPath, false)
	if err != nil {
		return render.MixEstimate{}, err
	}
	if pathKind == render.PathD && runner != nil {
		if err := ValidateVisualizerFilters(ctx, preparedCfg, runner, nil); err != nil {
			return render.MixEstimate{}, err
		}
	}
	if len(tracks) < 1 {
		return render.MixEstimate{}, errors.New("at least one .mp3 Track is required")
	}

	resolvedBG := bgMeta
	if resolvedBG == nil && runner != nil && strings.TrimSpace(preparedCfg.BG) != "" {
		if probed, err := probe.ProbeBackgroundVisual(ctx, preparedCfg.BG, runner); err == nil {
			resolvedBG = &probed
		}
	}

	est, _, _, _ := buildEstimateContext(ctx, preparedCfg, tracks, resolvedBG, runner)
	return est, nil
}

type etaTracker struct {
	est           render.MixEstimate
	mixDuration   float64
	lastSubstage  string
	substageStart time.Time
	emaSpeed      float64
	muxWallSec    float64
	renderWallSec float64
}

func (t *etaTracker) update(u render.ProgressUpdate) float64 {
	now := time.Now()
	if u.Substage != t.lastSubstage {
		t.lastSubstage = u.Substage
		t.substageStart = now
		t.emaSpeed = 0
	}
	elapsedSub := now.Sub(t.substageStart).Seconds()
	if u.Substage == "mux" {
		t.muxWallSec = elapsedSub
	} else if u.Substage == "render" {
		t.renderWallSec = elapsedSub
	}

	if sp, ok := render.ParseFFmpegSpeed(u.Speed); ok {
		if t.emaSpeed <= 0 {
			t.emaSpeed = sp
		} else {
			t.emaSpeed = 0.25*sp + 0.75*t.emaSpeed
		}
	}

	targetDur := t.mixDuration
	preSubDur := t.est.VideoSec
	switch u.Substage {
	case "norm_loop":
		if t.est.LoopDurationSec > 0 {
			targetDur = t.est.LoopDurationSec
		} else {
			targetDur = 0
		}
		preSubDur = t.est.PathANormSec
	case "mux":
		preSubDur = t.est.PathAMuxSec
	}

	subFrac := 0.0
	if targetDur > 0 && u.OutTimeSec > 0 {
		subFrac = u.OutTimeSec / targetDur
		if subFrac > 1 {
			subFrac = 1
		}
	} else if u.Done {
		subFrac = 1
	}
	preRem := math.Max(0, preSubDur*(1.0-subFrac))

	rem := preRem
	if t.emaSpeed > 0 && targetDur > 0 {
		liveRem := math.Max(0, (targetDur-u.OutTimeSec)/t.emaSpeed)
		rampFloor := math.Max(5.0, 0.05*targetDur)
		blend := math.Min(1.0, u.OutTimeSec/rampFloor)
		rem = (1.0-blend)*preRem + blend*liveRem
	}
	if u.Substage == "norm_loop" {
		rem += t.est.PathAMuxSec
	}
	if u.Done && u.Substage != "norm_loop" {
		rem = 0
	}
	return rem
}

func recordCalibrationResults(
	calPath string,
	cal map[string]render.CalibrationEntry,
	cfg Config,
	est render.MixEstimate,
	mixDur float64,
	normElapsed float64,
	mergeElapsed float64,
	tracker *etaTracker,
) {
	if calPath == "" || cal == nil || mixDur < minCalibratedMixSec {
		return
	}
	now := time.Now().UTC()
	changed := false
	if cfg.Normalize && normElapsed > 0.2 {
		if RecordObservedSpeed(cal, render.AudioCalibrationKey("loudnorm"), est.MixDurationSec/normElapsed, mixDur, now) {
			changed = true
		}
	}
	if mergeElapsed > 0.2 {
		mergeKey := "concat"
		if cfg.Crossfade > 0 {
			mergeKey = "acrossfade"
		}
		if RecordObservedSpeed(cal, render.AudioCalibrationKey(mergeKey), mixDur/mergeElapsed, mixDur, now) {
			changed = true
		}
	}

	switch est.Path {
	case render.PathA:
		if tracker.muxWallSec > 0.05 {
			if RecordObservedSpeed(cal, render.CalibrationKey(render.PathA, "mux", render.Resolution1080p), mixDur/tracker.muxWallSec, mixDur, now) {
				changed = true
			}
		}
	case render.PathB:
		if tracker.renderWallSec > 0.2 {
			if RecordObservedSpeed(cal, render.CalibrationKey(render.PathB, est.EncoderName, render.Resolution1080p), mixDur/tracker.renderWallSec, mixDur, now) {
				changed = true
			}
		}
	case render.PathC:
		if tracker.renderWallSec > 0.2 {
			tag := "2fps"
			if cfg.OverlayTitles {
				tag = "10fps"
			}
			if RecordObservedSpeed(cal, render.CalibrationKey(render.PathC, "libx264", tag), mixDur/tracker.renderWallSec, mixDur, now) {
				changed = true
			}
		}
	case render.PathD:
		if tracker.renderWallSec > 0.2 {
			encKey := est.EncoderName
			if strings.EqualFold(strings.TrimSpace(cfg.Visualizer), "black") {
				encKey = "black"
			}
			if RecordObservedSpeed(cal, render.CalibrationKey(render.PathD, encKey, cfg.VisualizerResolution), mixDur/tracker.renderWallSec, mixDur, now) {
				changed = true
			}
		}
	}

	if changed {
		_ = SaveCalibration(calPath, cal)
	}
}

// Run executes the full 5-step ytmix Mix pipeline from cfg.AudioDir.
func Run(ctx context.Context, cfg Config, sink TelemetrySink, lookPath func(string) (string, error), runner CommandRunner, streamRunner StreamRunner) error {
	ctx, cfg, start, err := prepareRun(ctx, cfg, lookPath, runner)
	if err != nil {
		return err
	}

	emitStep(sink, 1, 5, 4, -1, 0, fmt.Sprintf("Probing Tracks in %s...", cfg.AudioDir))
	tracks, err := ProbeTracks(ctx, cfg.AudioDir, cfg.Shuffle, nil, runner)
	if err != nil {
		return err
	}
	return runPipeline(ctx, cfg, tracks, sink, start, runner, streamRunner)
}

// RunWithTracks executes the 5-step ytmix Mix pipeline using an explicit, caller-ordered Track slice.
func RunWithTracks(ctx context.Context, cfg Config, tracks []Track, sink TelemetrySink, lookPath func(string) (string, error), runner CommandRunner, streamRunner StreamRunner) error {
	ctx, cfg, start, err := prepareRun(ctx, cfg, lookPath, runner)
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

	emitStep(sink, 1, 5, 4, -1, 0, fmt.Sprintf("Using %d selected Track(s)...", len(resolved)))
	return runPipeline(ctx, cfg, resolved, sink, start, runner, streamRunner)
}

func runPipeline(ctx context.Context, cfg Config, tracks []Track, sink TelemetrySink, start time.Time, runner CommandRunner, streamRunner StreamRunner) error {
	tempDir, err := os.MkdirTemp("", "ytmix-*")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	est, encPtr, calPath, cal := buildEstimateContext(ctx, cfg, tracks, nil, runner)
	bounds := computeStageBounds(est.Weights)

	paths := make([]string, len(tracks))
	fileNames := make([]string, len(tracks))
	durations := make([]float64, len(tracks))
	for i, tr := range tracks {
		paths[i] = tr.Path
		fileNames[i] = tr.FileName
		durations[i] = tr.Duration
	}

	emitStep(sink, 2, 5, bounds.step2Start, est.PointSec, est.PointSec, fmt.Sprintf("Processing & merging %d Track(s) (normalize=%v, crossfade=%.2fs)...", len(tracks), cfg.Normalize, cfg.Crossfade))
	audioStart := time.Now()
	var normDoneAt time.Time
	var onNormProgress func(completed, total int)
	if sink != nil && cfg.Normalize {
		onNormProgress = func(completed, total int) {
			if completed == total {
				normDoneAt = time.Now()
			}
			if total <= 0 {
				return
			}
			normShare := 0.5
			if est.AudioSec > 0 {
				normShare = est.NormalizeSec / est.AudioSec
			}
			pct := bounds.step2Start + int(math.Round(float64(bounds.audioSpan)*normShare*(float64(completed)/float64(total))))
			if pct > bounds.step3Start-1 {
				pct = bounds.step3Start - 1
			}
			rem := math.Max(0, est.PointSec-time.Since(start).Seconds())
			emitTelemetry(sink, TelemetryEvent{
				Kind:              EventProgress,
				Step:              2,
				TotalSteps:        5,
				Progress:          pct,
				Substage:          "normalize",
				TotalDurationSec:  est.MixDurationSec,
				RemainingSec:      rem,
				EstimatedTotalSec: est.PointSec,
			})
		}
	}

	mergedAudioPath, updatedPaths, updatedDurations, err := audio.ProcessAndMergeWithProgress(ctx, paths, fileNames, durations, tempDir, cfg.Normalize, cfg.Crossfade, runner, onNormProgress)
	if err != nil {
		return err
	}
	audioDoneAt := time.Now()
	var normElapsed, mergeElapsed float64
	if cfg.Normalize && !normDoneAt.IsZero() {
		normElapsed = normDoneAt.Sub(audioStart).Seconds()
		mergeElapsed = audioDoneAt.Sub(normDoneAt).Seconds()
	} else {
		mergeElapsed = audioDoneAt.Sub(audioStart).Seconds()
	}
	for i := range tracks {
		tracks[i].Path = updatedPaths[i]
		tracks[i].Duration = updatedDurations[i]
	}

	titles := make([]string, len(tracks))
	for i, tr := range tracks {
		titles[i] = tr.DisplayTitle
	}

	emitStep(sink, 3, 5, bounds.step3Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, fmt.Sprintf("Writing Chapter Timestamps to %s...", cfg.Timestamps))
	sched, err := subtitle.WriteChapters(cfg.Timestamps, titles, updatedDurations, cfg.Crossfade)
	if err != nil {
		return err
	}

	var assPath string
	if cfg.OverlayTitles {
		assPath = filepath.Join(tempDir, "titles.ass")
		emitStep(sink, 4, 5, bounds.step4Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, fmt.Sprintf("Generating Title Overlays (.ass) at %s...", assPath))
		if err := subtitle.WriteASSWithOptions(assPath, titles, sched.Starts, sched.TotalDuration, subtitle.ASSOptions{
			TitleDuration: cfg.TitleDuration,
			FontName:      cfg.FontName,
			DimTitles:     cfg.DimTitles,
		}); err != nil {
			return err
		}
	} else {
		emitStep(sink, 4, 5, bounds.step4Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, "Skipping Title Overlays (--overlay-titles=false).")
	}

	emitStep(sink, 5, 5, bounds.step5Start, math.Max(0, est.VideoSec), est.PointSec, fmt.Sprintf("Rendering Mix video to %s...", cfg.Out))
	var renderStream render.StreamRunner
	if streamRunner != nil {
		renderStream = render.StreamRunner(streamRunner)
	}
	tracker := &etaTracker{
		est:         est,
		mixDuration: sched.TotalDuration,
	}
	var onRenderProgress render.ProgressFunc
	if sink != nil || sched.TotalDuration >= minCalibratedMixSec {
		onRenderProgress = func(u render.ProgressUpdate) {
			rem := tracker.update(u)
			if sink == nil {
				return
			}
			pct := bounds.step5Start + int(math.Round(u.Fraction*float64(bounds.step5Span)))
			if pct < bounds.step5Start {
				pct = bounds.step5Start
			}
			if pct > 99 {
				pct = 99
			}
			emitTelemetry(sink, TelemetryEvent{
				Kind:              EventProgress,
				Step:              5,
				TotalSteps:        5,
				Progress:          pct,
				Substage:          u.Substage,
				OutTimeSec:        u.OutTimeSec,
				TotalDurationSec:  sched.TotalDuration,
				Speed:             u.Speed,
				RemainingSec:      rem,
				EstimatedTotalSec: est.PointSec,
			})
		}
	}

	renderStart := time.Now()
	pathKind, err := render.RenderWithOptions(ctx, render.RenderOptions{
		BGPath:          cfg.BG,
		MergedAudioPath: mergedAudioPath,
		ASSPath:         assPath,
		TempDir:         tempDir,
		OutPath:         cfg.Out,
		OverlayTitles:   cfg.OverlayTitles,
		TotalDuration:   sched.TotalDuration,
		GOOS:            runtime.GOOS,
		Visualizer:      cfg.VisualizerConfig(),
		Encoder:         encPtr,
		NormWeight:      est.Weights.PathANormSub,
		Runner:          runner,
		StreamRunner:    renderStream,
		OnProgress:      onRenderProgress,
	})
	if err != nil {
		return err
	}
	if tracker.renderWallSec <= 0 && pathKind != render.PathA {
		tracker.renderWallSec = time.Since(renderStart).Seconds()
	}
	recordCalibrationResults(calPath, cal, cfg, est, sched.TotalDuration, normElapsed, mergeElapsed, tracker)

	doneMsg := fmt.Sprintf("Done! Rendered %s via %s in %s (audio duration %.2fs, chapters: %s).",
		cfg.Out, pathKind, time.Since(start).Round(time.Millisecond), sched.TotalDuration, cfg.Timestamps)
	emitTelemetry(sink, TelemetryEvent{
		Kind:              EventDone,
		Step:              5,
		TotalSteps:        5,
		Progress:          100,
		Message:           doneMsg,
		TotalDurationSec:  sched.TotalDuration,
		RemainingSec:      0,
		EstimatedTotalSec: est.PointSec,
	})
	return nil
}
