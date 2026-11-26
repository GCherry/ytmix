package mixer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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
type Track = probe.Track

// TrackMediaMode selects how per-Track visuals are discovered and enabled.
type TrackMediaMode string

const (
	// TrackMediaOff disables per-Track media and uses standard Paths A–D rendering.
	TrackMediaOff TrackMediaMode = "off"
	// TrackMediaAuto discovers sidecar media and embedded ID3 cover art per Track.
	TrackMediaAuto TrackMediaMode = "auto"
)

// TrackMediaLayout selects how discovered or overridden per-Track visuals are framed.
type TrackMediaLayout string

const (
	// TrackMediaLayoutAuto selects Art Card for ID3/near-square media and Fullscreen for widescreen media.
	TrackMediaLayoutAuto TrackMediaLayout = "auto"
	// TrackMediaLayoutFullscreen fills the 16:9 canvas with each Track's visual.
	TrackMediaLayoutFullscreen TrackMediaLayout = "fullscreen"
	// TrackMediaLayoutCard frames each Track's visual as a centered card over Layer 0.
	TrackMediaLayoutCard TrackMediaLayout = "card"
)

// TrackMediaOverride configures a per-Track visual override for programmatic callers and ytmix ui.
type TrackMediaOverride struct {
	TrackIndex    int
	TrackFile     string
	MediaPath     string
	MediaKind     render.BackgroundKind
	LayerMode     render.TrackLayerMode
	ExplicitEmpty bool
}

// ManifestLayer0 configures optional Mix Backdrop (Layer 0) settings inside a track-media JSON manifest.
type ManifestLayer0 struct {
	Mode                 string        `json:"mode,omitempty"`
	BGPath               string        `json:"bgPath,omitempty"`
	Visualizer           string        `json:"visualizer,omitempty"`
	VisualizerTheme      string        `json:"visualizerTheme,omitempty"`
	VisualizerResolution string        `json:"visualizerResolution,omitempty"`
	VisualizerGlow       *bool         `json:"visualizerGlow,omitempty"`
	VisualizerBrightness *int          `json:"visualizerBrightness,omitempty"`
	FadeToBlack          time.Duration `json:"-"`
}

type manifestLayer0JSON struct {
	Mode                 string          `json:"mode,omitempty"`
	BGPath               string          `json:"bgPath,omitempty"`
	Visualizer           string          `json:"visualizer,omitempty"`
	VisualizerTheme      string          `json:"visualizerTheme,omitempty"`
	VisualizerResolution string          `json:"visualizerResolution,omitempty"`
	VisualizerGlow       *bool           `json:"visualizerGlow,omitempty"`
	VisualizerBrightness *int            `json:"visualizerBrightness,omitempty"`
	FadeToBlack          json.RawMessage `json:"fadeToBlack,omitempty"`
}

// MarshalJSON encodes ManifestLayer0, serializing FadeToBlack as a duration string when positive.
func (m ManifestLayer0) MarshalJSON() ([]byte, error) {
	out := manifestLayer0JSON{
		Mode:                 m.Mode,
		BGPath:               m.BGPath,
		Visualizer:           m.Visualizer,
		VisualizerTheme:      m.VisualizerTheme,
		VisualizerResolution: m.VisualizerResolution,
		VisualizerGlow:       m.VisualizerGlow,
		VisualizerBrightness: m.VisualizerBrightness,
	}
	if m.FadeToBlack > 0 {
		raw, err := json.Marshal(m.FadeToBlack.String())
		if err != nil {
			return nil, err
		}
		out.FadeToBlack = raw
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes ManifestLayer0, accepting FadeToBlack as either a duration string (e.g. "15m") or nanoseconds.
func (m *ManifestLayer0) UnmarshalJSON(data []byte) error {
	var raw manifestLayer0JSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Mode = raw.Mode
	m.BGPath = raw.BGPath
	m.Visualizer = raw.Visualizer
	m.VisualizerTheme = raw.VisualizerTheme
	m.VisualizerResolution = raw.VisualizerResolution
	m.VisualizerGlow = raw.VisualizerGlow
	m.VisualizerBrightness = raw.VisualizerBrightness
	m.FadeToBlack = 0
	if len(raw.FadeToBlack) > 0 && string(raw.FadeToBlack) != "null" {
		var durStr string
		if err := json.Unmarshal(raw.FadeToBlack, &durStr); err == nil {
			if strings.TrimSpace(durStr) != "" {
				d, err := time.ParseDuration(strings.TrimSpace(durStr))
				if err != nil {
					return fmt.Errorf("invalid layer0 fadeToBlack duration %q: %w", durStr, err)
				}
				m.FadeToBlack = d
			}
			return nil
		}
		var durNs int64
		if err := json.Unmarshal(raw.FadeToBlack, &durNs); err == nil {
			m.FadeToBlack = time.Duration(durNs)
			return nil
		}
		return fmt.Errorf("invalid layer0 fadeToBlack value %s", string(raw.FadeToBlack))
	}
	return nil
}

// ManifestTrackEntry configures a single Track's visual attachment in a track-media JSON manifest.
type ManifestTrackEntry struct {
	File      string `json:"file"`
	Media     string `json:"media,omitempty"`
	Mode      string `json:"mode,omitempty"`
	LayerMode string `json:"layerMode,omitempty"`
}

// TrackMediaManifest is the JSON manifest schema loaded by `--track-media <manifest.json>`.
type TrackMediaManifest struct {
	Layer0     *ManifestLayer0      `json:"layer0,omitempty"`
	Auto       bool                 `json:"auto,omitempty"`
	Layout     string               `json:"layout,omitempty"`
	Transition string               `json:"transition,omitempty"`
	Tracks     []ManifestTrackEntry `json:"tracks,omitempty"`
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
	TrackMedia           string
	TrackMediaLayout     string
	TrackTransition      string
	TrackOverrides       []TrackMediaOverride
	Segmented            bool
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
	Estimate          *render.MixEstimate
}

// TelemetrySink receives structured telemetry events during a Mix build.
type TelemetrySink func(TelemetryEvent)

func formatCompactDuration(sec float64) string {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec <= 0 {
		return "0s"
	}
	total := int(math.Round(sec))
	if total <= 0 {
		return "0s"
	}
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	if total < 3600 {
		return fmt.Sprintf("%dm %02ds", total/60, total%60)
	}
	return fmt.Sprintf("%dh %02dm %02ds", total/3600, (total%3600)/60, total%60)
}

// FormatDurationRange formats an estimated [lowSec, highSec] confidence band into a compact range string.
func FormatDurationRange(lowSec, highSec float64) string {
	if math.IsNaN(lowSec) || math.IsInf(lowSec, 0) || lowSec < 0 {
		lowSec = 0
	}
	if math.IsNaN(highSec) || math.IsInf(highSec, 0) || highSec < lowSec {
		highSec = lowSec
	}
	lowStr := formatCompactDuration(lowSec)
	highStr := formatCompactDuration(highSec)
	if lowStr == highStr {
		return "~" + lowStr
	}
	return fmt.Sprintf("~%s – %s", lowStr, highStr)
}

// FormatEstimateSummary formats a concise single-line pre-render Mix duration summary.
func FormatEstimateSummary(est render.MixEstimate) string {
	enc := strings.TrimSpace(est.EncoderName)
	if enc == "" {
		enc = "libx264"
	}
	return fmt.Sprintf("Estimated Mix render time: %s (%s · %s · Audio ~%s, Video ~%s)",
		FormatDurationRange(est.LowSec, est.HighSec),
		est.Path,
		enc,
		formatCompactDuration(est.AudioSec),
		formatCompactDuration(est.VideoSec),
	)
}

func pathDescription(pathKind render.Path) string {
	switch pathKind {
	case render.PathA:
		return "Path A (Stream Copy Loop)"
	case render.PathB:
		return "Path B (Hardware-Accelerated Overlay Loop)"
	case render.PathC:
		return "Path C (Low-FPS Still Image)"
	case render.PathD:
		return "Path D (Auto Visualizer)"
	default:
		return string(pathKind)
	}
}

// FormatEstimateReport formats a multi-line dry-run breakdown for `ytmix --estimate`.
func FormatEstimateReport(cfg Config, trackCount int, est render.MixEstimate) string {
	var b strings.Builder
	normStatus := "off"
	if cfg.Normalize {
		normStatus = "on"
	}

	b.WriteString("Mix Estimate (--estimate dry-run)\n")
	fmt.Fprintf(&b, "  Tracks:            %d, Mix duration %s (Loudness Normalization %s, Crossfade %.3gs)\n",
		trackCount, formatCompactDuration(est.MixDurationSec), normStatus, cfg.Crossfade)

	switch {
	case cfg.Segmented && strings.TrimSpace(cfg.BG) == "" && strings.EqualFold(strings.TrimSpace(cfg.Visualizer), "black"):
		b.WriteString("  Background Visual: Black (#000000) Mix Backdrop\n")
	case est.Path == render.PathA || est.Path == render.PathB:
		bgName := filepath.Base(strings.TrimSpace(cfg.BG))
		var metaParts []string
		if est.LoopDurationSec > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%.1fs loop", est.LoopDurationSec))
		} else {
			metaParts = append(metaParts, "looping video/GIF")
		}
		if est.LoopFPS > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%.4g fps", est.LoopFPS))
		}
		if est.LoopCycles > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%d cycles", est.LoopCycles))
		}
		fmt.Fprintf(&b, "  Background Visual: %s (%s)\n", bgName, strings.Join(metaParts, " · "))
	case est.Path == render.PathC:
		bgName := filepath.Base(strings.TrimSpace(cfg.BG))
		fps := 2
		if cfg.OverlayTitles {
			fps = 10
		}
		fmt.Fprintf(&b, "  Background Visual: %s (static image · %d fps)\n", bgName, fps)
	case est.Path == render.PathD:
		preset := strings.TrimSpace(cfg.Visualizer)
		if preset == "" {
			preset = render.DefaultVisualizerPreset
		}
		theme := strings.TrimSpace(cfg.VisualizerTheme)
		if theme == "" {
			theme = render.DefaultVisualizerTheme
		}
		res := strings.TrimSpace(cfg.VisualizerResolution)
		if res == "" {
			res = render.DefaultVisualizerResolution
		}
		fmt.Fprintf(&b, "  Background Visual: Auto Visualizer (%s · %s · %s)\n", preset, theme, res)
	}

	enc := strings.TrimSpace(est.EncoderName)
	if enc == "" {
		enc = "libx264"
	}
	encLabel := enc
	if !cfg.Segmented && est.Path == render.PathA {
		encLabel = fmt.Sprintf("%s (norm_loop) + stream copy (mux)", enc)
	}
	pathLabel := pathDescription(est.Path)
	if cfg.Segmented {
		pathLabel = "Segmented Per-Track (Option A)"
	}
	fmt.Fprintf(&b, "  Rendering Path:    %s\n", pathLabel)
	fmt.Fprintf(&b, "  Encoder:           %s\n", encLabel)

	audioPct := int(math.Round(est.Weights.Audio * 100))
	videoPct := 100 - audioPct
	if cfg.Normalize {
		fmt.Fprintf(&b, "  Audio Stage:       ~%s (normalize ~%s, merge ~%s · %d%%)\n",
			formatCompactDuration(est.AudioSec),
			formatCompactDuration(est.NormalizeSec),
			formatCompactDuration(est.MergeSec),
			audioPct)
	} else {
		fmt.Fprintf(&b, "  Audio Stage:       ~%s (merge ~%s · %d%%)\n",
			formatCompactDuration(est.AudioSec),
			formatCompactDuration(est.MergeSec),
			audioPct)
	}

	if !cfg.Segmented && est.Path == render.PathA {
		fmt.Fprintf(&b, "  Video Stage:       ~%s (norm_loop ~%s, mux ~%s · %d%%)\n",
			formatCompactDuration(est.VideoSec),
			formatCompactDuration(est.PathANormSec),
			formatCompactDuration(est.PathAMuxSec),
			videoPct)
	} else {
		fmt.Fprintf(&b, "  Video Stage:       ~%s (%d%%)\n",
			formatCompactDuration(est.VideoSec),
			videoPct)
	}

	fmt.Fprintf(&b, "  Estimated Time:    %s (point ~%s)\n",
		FormatDurationRange(est.LowSec, est.HighSec),
		formatCompactDuration(est.PointSec))

	calStatus := "uncalibrated (hardware prior)"
	if est.Calibrated {
		calStatus = "calibrated (local EMA cache)"
	}
	fmt.Fprintf(&b, "  Calibration:       %s", calStatus)
	return b.String()
}

// NewCLITelemetry returns a TelemetrySink that formats clean [1/5]–[5/5] milestones for w,
// overwriting a single live status line with optional speed and remaining ETA when isTTY is true.
func NewCLITelemetry(w io.Writer, isTTY bool) TelemetrySink {
	if w == nil {
		return nil
	}
	var dirtyTTY bool
	var lastLineLen int
	var lastStep int
	lastPct := -1
	var lastEmit time.Time
	return func(ev TelemetryEvent) {
		switch ev.Kind {
		case EventStep:
			if dirtyTTY {
				fmt.Fprintln(w)
				dirtyTTY = false
				lastLineLen = 0
			}
			if ev.Estimate != nil {
				fmt.Fprintln(w, FormatEstimateSummary(*ev.Estimate))
			}
			fmt.Fprintln(w, ev.Message)
		case EventProgress:
			if !isTTY {
				return
			}
			now := time.Now()
			if ev.Step == lastStep && ev.Progress == lastPct && now.Sub(lastEmit) < 250*time.Millisecond {
				return
			}
			lastStep = ev.Step
			lastPct = ev.Progress
			lastEmit = now

			label := "[5/5] Rendering Mix..."
			if ev.Step == 2 && ev.Substage == "normalize" {
				label = "[2/5] Normalizing Tracks..."
			}

			var parts []string
			if ev.Speed != "" {
				parts = append(parts, ev.Speed)
			}
			if ev.RemainingSec > 0 && !math.IsNaN(ev.RemainingSec) && !math.IsInf(ev.RemainingSec, 0) {
				rem := ev.RemainingSec
				if rem < 1 {
					rem = 1
				}
				parts = append(parts, fmt.Sprintf("~%s left", formatCompactDuration(rem)))
			}
			suffix := ""
			if len(parts) > 0 {
				suffix = fmt.Sprintf(" (%s)", strings.Join(parts, " · "))
			}

			line := fmt.Sprintf("%s %d%%%s", label, ev.Progress, suffix)
			pad := ""
			if len(line) < lastLineLen {
				pad = strings.Repeat(" ", lastLineLen-len(line))
			}
			lastLineLen = len(line)
			fmt.Fprintf(w, "\r%s%s", line, pad)
			dirtyTTY = true
		case EventDone:
			if dirtyTTY {
				fmt.Fprintln(w)
				dirtyTTY = false
				lastLineLen = 0
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

func isManifestPath(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, string(TrackMediaOff)) || strings.EqualFold(trimmed, string(TrackMediaAuto)) {
		return false
	}
	return true
}

func isSegmentedConfig(cfg Config) bool {
	if cfg.Segmented || len(cfg.TrackOverrides) > 0 {
		return true
	}
	return isManifestPath(cfg.TrackMedia) || strings.EqualFold(strings.TrimSpace(cfg.TrackMedia), string(TrackMediaAuto))
}

// ValidateTrackMediaLayout validates and normalizes a --track-media-layout value.
func ValidateTrackMediaLayout(layout string) (TrackMediaLayout, error) {
	norm := TrackMediaLayout(strings.ToLower(strings.TrimSpace(layout)))
	switch norm {
	case "", TrackMediaLayoutAuto:
		return TrackMediaLayoutAuto, nil
	case TrackMediaLayoutFullscreen, TrackMediaLayoutCard:
		return norm, nil
	default:
		return "", fmt.Errorf("unknown --track-media-layout %q (supported: auto, fullscreen, card)", layout)
	}
}

// ValidateTrackMedia validates the --track-media value (off, auto, or a .json manifest path).
func ValidateTrackMedia(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, string(TrackMediaOff)) || strings.EqualFold(trimmed, string(TrackMediaAuto)) {
		return nil
	}
	if strings.ContainsRune(trimmed, '\x00') || hasTrailingPathSeparator(trimmed) {
		return fmt.Errorf("invalid --track-media manifest path %q", raw)
	}
	if !strings.EqualFold(filepath.Ext(trimmed), ".json") {
		return fmt.Errorf("invalid --track-media %q (expected off, auto, or a .json manifest path)", raw)
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
	if err := ValidateTrackMedia(cfg.TrackMedia); err != nil {
		return err
	}
	if _, err := ValidateTrackMediaLayout(cfg.TrackMediaLayout); err != nil {
		return err
	}
	if _, err := render.ValidateTrackTransition(render.TrackTransition(cfg.TrackTransition)); err != nil {
		return err
	}
	for i, ov := range cfg.TrackOverrides {
		if ov.LayerMode != "" {
			if _, err := render.ValidateTrackLayerMode(ov.LayerMode); err != nil {
				return fmt.Errorf("track override %d: %w", i, err)
			}
		}
		if strings.ContainsRune(ov.MediaPath, '\x00') {
			return fmt.Errorf("track override %d media path must not contain null bytes", i)
		}
		if strings.TrimSpace(ov.MediaPath) != "" {
			if _, _, err := render.ClassifyBackground(ov.MediaPath); err != nil {
				return fmt.Errorf("track override %d media: %w", i, err)
			}
		}
		if ov.MediaKind != "" && ov.MediaKind != render.BackgroundKindImage && ov.MediaKind != render.BackgroundKindVideo {
			return fmt.Errorf("track override %d invalid media kind %q", i, ov.MediaKind)
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

func resolveManifestPathRef(baseDir, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Clean(filepath.Join(baseDir, trimmed))
}

func resolveManifestEntryLayerMode(entry ManifestTrackEntry) (render.TrackLayerMode, error) {
	m1 := strings.TrimSpace(entry.LayerMode)
	m2 := strings.TrimSpace(entry.Mode)
	if m1 != "" && m2 != "" && !strings.EqualFold(m1, m2) {
		return "", fmt.Errorf("track %q specifies conflicting layerMode %q and mode %q", entry.File, m1, m2)
	}
	raw := m1
	if raw == "" {
		raw = m2
	}
	if raw == "" {
		return "", nil
	}
	return render.ValidateTrackLayerMode(render.TrackLayerMode(raw))
}

// ParseTrackMediaManifest reads, normalizes, and validates a per-Track media JSON manifest file.
func ParseTrackMediaManifest(path string) (TrackMediaManifest, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return TrackMediaManifest{}, errors.New("track media manifest path is empty")
	}
	if strings.ContainsRune(trimmedPath, '\x00') {
		return TrackMediaManifest{}, errors.New("track media manifest path must not contain null bytes")
	}
	// #nosec G703 -- manifest path comes from explicit CLI/config input and is checked as a regular file before read.
	info, err := os.Stat(trimmedPath)
	if err != nil {
		return TrackMediaManifest{}, fmt.Errorf("cannot access track media manifest %q: %w", trimmedPath, err)
	}
	if info.IsDir() {
		return TrackMediaManifest{}, fmt.Errorf("track media manifest path %q is a directory, expected a .json file", trimmedPath)
	}
	// #nosec G304 -- user-specified track media manifest file.
	data, err := os.ReadFile(trimmedPath)
	if err != nil {
		return TrackMediaManifest{}, fmt.Errorf("read track media manifest %q: %w", trimmedPath, err)
	}

	var manifest TrackMediaManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return TrackMediaManifest{}, fmt.Errorf("parse track media manifest %q: %w", trimmedPath, err)
	}

	baseDir := filepath.Dir(trimmedPath)
	if strings.TrimSpace(manifest.Layout) != "" {
		layout, err := ValidateTrackMediaLayout(manifest.Layout)
		if err != nil {
			return TrackMediaManifest{}, fmt.Errorf("manifest layout: %w", err)
		}
		manifest.Layout = string(layout)
	}
	if strings.TrimSpace(manifest.Transition) != "" {
		tr, err := render.ValidateTrackTransition(render.TrackTransition(manifest.Transition))
		if err != nil {
			return TrackMediaManifest{}, fmt.Errorf("manifest transition: %w", err)
		}
		manifest.Transition = string(tr)
	}

	if manifest.Layer0 != nil {
		l0 := *manifest.Layer0
		l0.Mode = strings.ToLower(strings.TrimSpace(l0.Mode))
		l0.BGPath = strings.TrimSpace(l0.BGPath)
		l0.Visualizer = strings.TrimSpace(l0.Visualizer)
		if strings.ContainsRune(l0.BGPath, '\x00') {
			return TrackMediaManifest{}, errors.New("manifest layer0 bgPath must not contain null bytes")
		}
		if l0.BGPath != "" {
			l0.BGPath = resolveManifestPathRef(baseDir, l0.BGPath)
			if _, _, err := render.ClassifyBackground(l0.BGPath); err != nil {
				return TrackMediaManifest{}, fmt.Errorf("manifest layer0 bgPath: %w", err)
			}
		}
		if l0.BGPath != "" && l0.Visualizer != "" {
			return TrackMediaManifest{}, errors.New("manifest layer0 cannot specify both bgPath and visualizer")
		}
		switch l0.Mode {
		case "":
		case string(render.Layer0Black):
			if l0.BGPath != "" || (l0.Visualizer != "" && !strings.EqualFold(l0.Visualizer, "black")) {
				return TrackMediaManifest{}, errors.New("manifest layer0 mode \"black\" cannot be used with bgPath or visualizer")
			}
		case string(render.Layer0Media):
			if l0.Visualizer != "" {
				return TrackMediaManifest{}, errors.New("manifest layer0 mode \"media\" cannot be used with visualizer")
			}
			if l0.BGPath == "" {
				return TrackMediaManifest{}, errors.New("manifest layer0 mode \"media\" requires bgPath")
			}
		case string(render.Layer0Visualizer):
			if l0.BGPath != "" {
				return TrackMediaManifest{}, errors.New("manifest layer0 mode \"visualizer\" cannot be used with bgPath")
			}
		default:
			return TrackMediaManifest{}, fmt.Errorf("unknown manifest layer0 mode %q (supported: black, media, visualizer)", manifest.Layer0.Mode)
		}
		if l0.FadeToBlack < 0 {
			return TrackMediaManifest{}, fmt.Errorf("manifest layer0 fadeToBlack must be >= 0 (got %s)", l0.FadeToBlack)
		}
		if l0.Mode == "" && l0.BGPath != "" {
			// Media Layer 0 does not need visualizer validation beyond FadeToBlack.
		} else if l0.Mode != string(render.Layer0Media) {
			brightness := render.DefaultVisualizerBrightness
			if l0.VisualizerBrightness != nil {
				brightness = *l0.VisualizerBrightness
				if brightness < render.MinVisualizerBrightness || brightness > render.MaxVisualizerBrightness {
					return TrackMediaManifest{}, fmt.Errorf("manifest layer0 visualizerBrightness must be between %d and %d (got %d)",
						render.MinVisualizerBrightness, render.MaxVisualizerBrightness, brightness)
				}
			}
			glow := true
			if l0.VisualizerGlow != nil {
				glow = *l0.VisualizerGlow
			}
			preset := l0.Visualizer
			if l0.Mode == string(render.Layer0Black) && preset == "" {
				preset = "black"
			}
			if _, err := render.ValidateVisualizerConfig(render.VisualizerConfig{
				Preset:      preset,
				Theme:       l0.VisualizerTheme,
				Resolution:  l0.VisualizerResolution,
				Glow:        glow,
				Brightness:  brightness,
				FadeToBlack: l0.FadeToBlack,
			}); err != nil {
				return TrackMediaManifest{}, fmt.Errorf("manifest layer0: %w", err)
			}
		}
		manifest.Layer0 = &l0
	}

	seenFiles := make(map[string]struct{}, len(manifest.Tracks))
	for i, entry := range manifest.Tracks {
		fileRef := strings.TrimSpace(entry.File)
		if fileRef == "" {
			return TrackMediaManifest{}, fmt.Errorf("manifest tracks[%d].file must not be empty", i)
		}
		if strings.ContainsRune(fileRef, '\x00') || strings.ContainsRune(entry.Media, '\x00') {
			return TrackMediaManifest{}, fmt.Errorf("manifest tracks[%d] must not contain null bytes", i)
		}
		normKey := strings.ToLower(filepath.Clean(fileRef))
		if _, dup := seenFiles[normKey]; dup {
			return TrackMediaManifest{}, fmt.Errorf("duplicate track %q in track media manifest", entry.File)
		}
		seenFiles[normKey] = struct{}{}

		mode, err := resolveManifestEntryLayerMode(entry)
		if err != nil {
			return TrackMediaManifest{}, err
		}
		mediaRef := strings.TrimSpace(entry.Media)
		if mediaRef != "" {
			mediaRef = resolveManifestPathRef(baseDir, mediaRef)
			if _, _, err := render.ClassifyBackground(mediaRef); err != nil {
				return TrackMediaManifest{}, fmt.Errorf("manifest tracks[%d] (%s) media: %w", i, entry.File, err)
			}
		}
		manifest.Tracks[i].File = fileRef
		manifest.Tracks[i].Media = mediaRef
		if mode != "" {
			manifest.Tracks[i].LayerMode = string(mode)
		}
	}

	return manifest, nil
}

func applyManifestLayer0(cfg Config, l0 *ManifestLayer0) Config {
	if l0 == nil {
		return cfg
	}
	if l0.FadeToBlack > 0 && cfg.FadeToBlack == 0 {
		cfg.FadeToBlack = l0.FadeToBlack
	}
	if strings.TrimSpace(cfg.BG) != "" || strings.TrimSpace(cfg.Visualizer) != "" {
		return cfg
	}
	mode := strings.ToLower(strings.TrimSpace(l0.Mode))
	switch mode {
	case string(render.Layer0Black):
		cfg.Visualizer = "black"
	case string(render.Layer0Media):
		cfg.BG = l0.BGPath
	default:
		if strings.TrimSpace(l0.BGPath) != "" && strings.TrimSpace(l0.Visualizer) == "" {
			cfg.BG = l0.BGPath
			return cfg
		}
		if strings.TrimSpace(l0.Visualizer) != "" {
			cfg.Visualizer = l0.Visualizer
		}
		if strings.TrimSpace(l0.VisualizerTheme) != "" && (strings.TrimSpace(cfg.VisualizerTheme) == "" || cfg.VisualizerTheme == render.DefaultVisualizerTheme) {
			cfg.VisualizerTheme = l0.VisualizerTheme
		}
		if strings.TrimSpace(l0.VisualizerResolution) != "" && (strings.TrimSpace(cfg.VisualizerResolution) == "" || cfg.VisualizerResolution == render.DefaultVisualizerResolution) {
			cfg.VisualizerResolution = l0.VisualizerResolution
		}
		if l0.VisualizerGlow != nil {
			cfg.VisualizerGlow = *l0.VisualizerGlow
		}
		if l0.VisualizerBrightness != nil {
			cfg.VisualizerBrightness = *l0.VisualizerBrightness
		}
	}
	return cfg
}

func buildLayer0SpecFromConfig(cfg Config) (render.Layer0Spec, error) {
	bg := strings.TrimSpace(cfg.BG)
	viz := strings.TrimSpace(cfg.Visualizer)
	if bg != "" && viz != "" {
		return render.Layer0Spec{}, errors.New("--bg and --visualizer cannot be used together (omit --bg for Path D Auto Visualizer, or omit --visualizer to use a Background Visual file)")
	}
	if bg != "" {
		kind, _, err := render.ClassifyBackground(bg)
		if err != nil {
			return render.Layer0Spec{}, err
		}
		return render.ValidateLayer0Spec(render.Layer0Spec{
			Mode:   render.Layer0Media,
			BGPath: bg,
			BGKind: kind,
			Visualizer: render.VisualizerConfig{
				FadeToBlack: cfg.FadeToBlack,
			},
		})
	}
	if strings.EqualFold(viz, "black") {
		return render.ValidateLayer0Spec(render.Layer0Spec{
			Mode:       render.Layer0Black,
			Visualizer: cfg.VisualizerConfig(),
		})
	}
	vizCfg, err := render.ValidateVisualizerConfig(cfg.VisualizerConfig())
	if err != nil {
		return render.Layer0Spec{}, err
	}
	return render.ValidateLayer0Spec(render.Layer0Spec{
		Mode:       render.Layer0Visualizer,
		Visualizer: vizCfg,
	})
}

// PreflightConfig validates cfg, checks Background Visual file accessibility when provided, selects the Rendering Path,
// and optionally prepares output directories and writability checks.
func PreflightConfig(cfg Config, lookPath func(string) (string, error), prepareOutputs bool) (Config, render.Path, error) {
	if err := ValidateConfig(cfg, lookPath); err != nil {
		return cfg, "", err
	}
	if isSegmentedConfig(cfg) {
		cfg.Segmented = true
		if isManifestPath(cfg.TrackMedia) {
			manifest, err := ParseTrackMediaManifest(cfg.TrackMedia)
			if err != nil {
				return cfg, "", err
			}
			cfg = applyManifestLayer0(cfg, manifest.Layer0)
		}
		for i, ov := range cfg.TrackOverrides {
			if strings.TrimSpace(ov.MediaPath) != "" {
				if err := ValidateBackgroundFile(ov.MediaPath); err != nil {
					return cfg, "", fmt.Errorf("track override %d: %w", i, err)
				}
			}
		}
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

// NamedTrackFile pairs a local .mp3 file path with its original display filename.
type NamedTrackFile = probe.NamedTrackFile

// ProbeTracks discovers .mp3 files in audioDir and probes each Track's duration and Display Title.
func ProbeTracks(ctx context.Context, audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner CommandRunner) ([]Track, error) {
	return probe.ProbeTracks(ctx, audioDir, shuffle, shuffleFn, runner)
}

// ProbeTrackFiles probes an ordered list of .mp3 file paths without re-scanning or re-sorting a directory.
func ProbeTrackFiles(ctx context.Context, files []string, runner CommandRunner) ([]Track, error) {
	return probe.ProbeTrackFiles(ctx, files, runner)
}

// ProbeNamedTrackFiles probes an ordered list of .mp3 files while resolving Display Titles from FileName.
func ProbeNamedTrackFiles(ctx context.Context, files []NamedTrackFile, runner CommandRunner) ([]Track, error) {
	return probe.ProbeNamedTrackFiles(ctx, files, runner)
}

// ProbeTracksWithArt discovers .mp3 files in audioDir and probes each Track's duration, Display Title,
// and embedded ID3 cover art metadata.
func ProbeTracksWithArt(ctx context.Context, audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner CommandRunner) ([]Track, error) {
	return probe.ProbeTracksWithArt(ctx, audioDir, shuffle, shuffleFn, runner)
}

// ProbeTrackFilesWithArt probes an ordered list of .mp3 file paths including embedded ID3 cover art metadata.
func ProbeTrackFilesWithArt(ctx context.Context, files []string, runner CommandRunner) ([]Track, error) {
	return probe.ProbeTrackFilesWithArt(ctx, files, runner)
}

// ProbeNamedTrackFilesWithArt probes an ordered list of .mp3 files while resolving Display Titles from FileName
// and probing embedded ID3 cover art metadata.
func ProbeNamedTrackFilesWithArt(ctx context.Context, files []NamedTrackFile, runner CommandRunner) ([]Track, error) {
	return probe.ProbeNamedTrackFilesWithArt(ctx, files, runner)
}

func matchTrackIndex(fileRef string, tracks []Track) (int, error) {
	trimmed := strings.TrimSpace(fileRef)
	if trimmed == "" {
		return -1, errors.New("track file reference is empty")
	}
	cleaned := filepath.Clean(trimmed)

	for i, tr := range tracks {
		if filepath.Clean(tr.Path) == cleaned {
			return i, nil
		}
	}

	var exactMatches []int
	targetBase := filepath.Base(cleaned)
	for i, tr := range tracks {
		if tr.FileName == trimmed || filepath.Base(tr.Path) == targetBase || tr.FileName == targetBase {
			exactMatches = append(exactMatches, i)
		}
	}
	if len(exactMatches) == 1 {
		return exactMatches[0], nil
	}
	if len(exactMatches) > 1 {
		return -1, fmt.Errorf("ambiguous track reference %q matched %d tracks", fileRef, len(exactMatches))
	}

	var foldMatches []int
	for i, tr := range tracks {
		if strings.EqualFold(tr.FileName, trimmed) || strings.EqualFold(filepath.Base(tr.Path), targetBase) || strings.EqualFold(tr.FileName, targetBase) {
			foldMatches = append(foldMatches, i)
		}
	}
	if len(foldMatches) == 1 {
		return foldMatches[0], nil
	}
	if len(foldMatches) > 1 {
		return -1, fmt.Errorf("ambiguous track reference %q matched %d tracks", fileRef, len(foldMatches))
	}
	return -1, fmt.Errorf("unknown track %q not found in Mix", fileRef)
}

func decodeImageDimensions(path string) (int, int) {
	// #nosec G304 -- validated background image path.
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func effectiveTrackLayout(cfg Config, manifest TrackMediaManifest) (TrackMediaLayout, error) {
	cliLayout, err := ValidateTrackMediaLayout(cfg.TrackMediaLayout)
	if err != nil {
		return "", err
	}
	if (strings.TrimSpace(cfg.TrackMediaLayout) == "" || cliLayout == TrackMediaLayoutAuto) && strings.TrimSpace(manifest.Layout) != "" {
		return ValidateTrackMediaLayout(manifest.Layout)
	}
	return cliLayout, nil
}

func effectiveTrackTransition(cfg Config, manifest TrackMediaManifest) (render.TrackTransition, error) {
	cliTr, err := render.ValidateTrackTransition(render.TrackTransition(cfg.TrackTransition))
	if err != nil {
		return "", err
	}
	if (strings.TrimSpace(cfg.TrackTransition) == "" || cliTr == render.TransitionCut) && strings.TrimSpace(manifest.Transition) != "" {
		return render.ValidateTrackTransition(render.TrackTransition(manifest.Transition))
	}
	return cliTr, nil
}

// ResolveTrackSegments resolves Layer 0 and per-Track visual segments according to precedence:
// (1) explicit TrackOverrides / manifest entry, (2) sidecar media when auto-discovery is active,
// (3) embedded ID3 attached_pic (extracted into tempDir when non-empty), and (4) LayerBackdrop (L0 Only).
func ResolveTrackSegments(ctx context.Context, cfg Config, tracks []Track, tempDir string, runner CommandRunner) (render.Layer0Spec, []render.TrackSegmentSpec, error) {
	layer0, segments, _, _, err := resolveTrackSegmentsWithMeta(ctx, cfg, tracks, tempDir, false, runner)
	return layer0, segments, err
}

func resolveTrackSegmentsWithMeta(
	ctx context.Context,
	cfg Config,
	tracks []Track,
	tempDir string,
	artProbed bool,
	runner CommandRunner,
) (render.Layer0Spec, []render.TrackSegmentSpec, []render.SegmentEstimateInput, render.TrackTransition, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return render.Layer0Spec{}, nil, nil, "", err
	}
	if len(tracks) == 0 {
		return render.Layer0Spec{}, nil, nil, "", errors.New("at least one .mp3 Track is required")
	}

	var manifest TrackMediaManifest
	if isManifestPath(cfg.TrackMedia) {
		var err error
		manifest, err = ParseTrackMediaManifest(cfg.TrackMedia)
		if err != nil {
			return render.Layer0Spec{}, nil, nil, "", err
		}
		cfg = applyManifestLayer0(cfg, manifest.Layer0)
	}

	layer0, err := buildLayer0SpecFromConfig(cfg)
	if err != nil {
		return render.Layer0Spec{}, nil, nil, "", err
	}
	if layer0.Mode == render.Layer0Media {
		if err := ValidateBackgroundFile(layer0.BGPath); err != nil {
			return render.Layer0Spec{}, nil, nil, "", err
		}
	}

	layout, err := effectiveTrackLayout(cfg, manifest)
	if err != nil {
		return render.Layer0Spec{}, nil, nil, "", err
	}
	transition, err := effectiveTrackTransition(cfg, manifest)
	if err != nil {
		return render.Layer0Spec{}, nil, nil, "", err
	}

	overridesByIdx := make(map[int]TrackMediaOverride, len(manifest.Tracks)+len(cfg.TrackOverrides))
	for _, entry := range manifest.Tracks {
		idx, err := matchTrackIndex(entry.File, tracks)
		if err != nil {
			return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track media manifest: %w", err)
		}
		layerMode, err := resolveManifestEntryLayerMode(entry)
		if err != nil {
			return render.Layer0Spec{}, nil, nil, "", err
		}
		var kind render.BackgroundKind
		if strings.TrimSpace(entry.Media) != "" {
			k, _, err := render.ClassifyBackground(entry.Media)
			if err != nil {
				return render.Layer0Spec{}, nil, nil, "", err
			}
			kind = k
		}
		overridesByIdx[idx] = TrackMediaOverride{
			TrackIndex:    idx,
			TrackFile:     entry.File,
			MediaPath:     entry.Media,
			MediaKind:     kind,
			LayerMode:     layerMode,
			ExplicitEmpty: strings.TrimSpace(entry.Media) == "" && layerMode == render.LayerBackdrop,
		}
	}

	for _, ov := range cfg.TrackOverrides {
		idx := ov.TrackIndex
		if strings.TrimSpace(ov.TrackFile) != "" {
			matched, err := matchTrackIndex(ov.TrackFile, tracks)
			if err != nil {
				return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track override: %w", err)
			}
			idx = matched
		} else if idx < 0 || idx >= len(tracks) {
			return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track override index %d out of range [0, %d)", idx, len(tracks))
		}
		if ov.LayerMode != "" {
			normMode, err := render.ValidateTrackLayerMode(ov.LayerMode)
			if err != nil {
				return render.Layer0Spec{}, nil, nil, "", err
			}
			ov.LayerMode = normMode
		}
		ov.TrackIndex = idx
		overridesByIdx[idx] = ov
	}

	autoDiscover := strings.EqualFold(strings.TrimSpace(cfg.TrackMedia), string(TrackMediaAuto)) || manifest.Auto
	sched := subtitle.BuildChapterSchedule(toChapterTracks(tracks), cfg.Crossfade)

	segments := make([]render.TrackSegmentSpec, len(tracks))
	estInputs := make([]render.SegmentEstimateInput, len(tracks))

	for i, tr := range tracks {
		var slotDur float64
		if i < len(tracks)-1 {
			slotDur = sched.Starts[i+1] - sched.Starts[i]
		} else {
			slotDur = tr.Duration
		}
		if math.IsNaN(slotDur) || math.IsInf(slotDur, 0) || slotDur <= 0 {
			return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track %d (%s) computed slot duration must be > 0 (got %v)", i+1, tr.FileName, slotDur)
		}

		var (
			mediaPath     string
			mediaKind     render.BackgroundKind
			layerMode     render.TrackLayerMode
			overrideMode  render.TrackLayerMode
			isEmbeddedID3 bool
			mediaW        int
			mediaH        int
			mediaFPS      float64
		)

		ov, hasOv := overridesByIdx[i]
		if hasOv {
			if ov.ExplicitEmpty || ov.LayerMode == render.LayerBackdrop {
				layerMode = render.LayerBackdrop
			} else if strings.TrimSpace(ov.MediaPath) != "" {
				mediaPath = strings.TrimSpace(ov.MediaPath)
				if err := ValidateBackgroundFile(mediaPath); err != nil {
					return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track %d (%s): %w", i+1, tr.FileName, err)
				}
				if ov.MediaKind != "" {
					mediaKind = ov.MediaKind
				} else {
					k, _, err := render.ClassifyBackground(mediaPath)
					if err != nil {
						return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track %d (%s): %w", i+1, tr.FileName, err)
					}
					mediaKind = k
				}
				overrideMode = ov.LayerMode
			} else {
				overrideMode = ov.LayerMode
			}
		}

		if layerMode != render.LayerBackdrop && mediaPath == "" && autoDiscover {
			if sidecarPath, sidecarKind, ok := probe.DiscoverSidecarMedia(tr.Path); ok {
				mediaPath = sidecarPath
				mediaKind = render.BackgroundKind(sidecarKind)
			} else {
				hasArt := tr.HasEmbeddedArt
				artW := tr.EmbeddedArtWidth
				artH := tr.EmbeddedArtHeight
				if !hasArt && !artProbed && runner != nil {
					art, err := probe.ProbeEmbeddedArt(ctx, tr.Path, runner)
					if err != nil {
						return render.Layer0Spec{}, nil, nil, "", err
					}
					hasArt = art.HasArt
					artW = art.Width
					artH = art.Height
				}
				if hasArt {
					isEmbeddedID3 = true
					mediaKind = render.BackgroundKindImage
					mediaW = artW
					mediaH = artH
					if tempDir != "" {
						artOut := filepath.Join(tempDir, fmt.Sprintf("art_%d.jpg", i))
						if err := probe.ExtractEmbeddedArt(ctx, tr.Path, artOut, runner); err != nil {
							return render.Layer0Spec{}, nil, nil, "", err
						}
						mediaPath = artOut
					}
				}
			}
		}

		if layerMode == "" {
			if mediaPath == "" && !isEmbeddedID3 {
				if overrideMode == render.LayerFullscreen || overrideMode == render.LayerArtCard {
					return render.Layer0Spec{}, nil, nil, "", fmt.Errorf("track %d (%s) requires visual media for layer mode %q", i+1, tr.FileName, overrideMode)
				}
				layerMode = render.LayerBackdrop
			} else {
				if mediaPath != "" && runner != nil && (mediaKind == render.BackgroundKindVideo || (overrideMode == "" && layout == TrackMediaLayoutAuto && !isEmbeddedID3)) {
					if meta, err := probe.ProbeBackgroundVisual(ctx, mediaPath, runner); err == nil {
						if meta.Width > 0 {
							mediaW = meta.Width
						}
						if meta.Height > 0 {
							mediaH = meta.Height
						}
						if meta.FPS > 0 {
							mediaFPS = meta.FPS
						}
					}
				}
				if (mediaW <= 0 || mediaH <= 0) && mediaPath != "" && mediaKind == render.BackgroundKindImage && overrideMode == "" && layout == TrackMediaLayoutAuto && !isEmbeddedID3 {
					mediaW, mediaH = decodeImageDimensions(mediaPath)
				}

				switch {
				case overrideMode != "":
					layerMode = overrideMode
				case layout == TrackMediaLayoutFullscreen:
					layerMode = render.LayerFullscreen
				case layout == TrackMediaLayoutCard:
					layerMode = render.LayerArtCard
				default:
					layerMode = render.ResolveAutoTrackLayerMode(isEmbeddedID3, mediaW, mediaH)
				}
			}
		}

		var segASSPath string
		if cfg.OverlayTitles && tempDir != "" {
			segASSPath = filepath.Join(tempDir, fmt.Sprintf("seg_%d.ass", i))
			if err := subtitle.WriteSegmentASSWithOptions(segASSPath, tr.DisplayTitle, slotDur, subtitle.ASSOptions{
				TitleDuration: cfg.TitleDuration,
				FontName:      cfg.FontName,
				DimTitles:     cfg.DimTitles,
			}); err != nil {
				return render.Layer0Spec{}, nil, nil, "", err
			}
		}

		segments[i] = render.TrackSegmentSpec{
			Index:          i,
			StartSec:       sched.Starts[i],
			SlotDuration:   slotDur,
			MediaPath:      mediaPath,
			MediaKind:      mediaKind,
			LayerMode:      layerMode,
			SegmentASSPath: segASSPath,
			OverlayTitles:  cfg.OverlayTitles,
		}
		estInputs[i] = render.SegmentEstimateInput{
			SlotDurationSec: slotDur,
			LayerMode:       layerMode,
			MediaKind:       mediaKind,
			OverlayTitles:   cfg.OverlayTitles,
			MediaFPS:        mediaFPS,
			MediaWidth:      mediaW,
			MediaHeight:     mediaH,
		}
	}

	return layer0, segments, estInputs, transition, nil
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
	audioSpan := max(int(math.Round(93.0*weights.Audio)), 4)
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

func toChapterTracks(tracks []Track) []subtitle.ChapterTrack {
	out := make([]subtitle.ChapterTrack, len(tracks))
	for i, tr := range tracks {
		out[i] = subtitle.ChapterTrack{
			Title:    tr.DisplayTitle,
			Duration: tr.Duration,
		}
	}
	return out
}

func buildEstimateContext(
	ctx context.Context,
	cfg Config,
	tracks []Track,
	bgMeta *probe.BackgroundMeta,
	artProbed bool,
	runner CommandRunner,
) (render.MixEstimate, *render.EncoderInfo, string, map[string]render.CalibrationEntry, error) {
	var totalAudioSec float64
	for _, tr := range tracks {
		totalAudioSec += tr.Duration
	}
	mixDurSec := totalAudioSec
	if len(tracks) > 0 {
		sched := subtitle.BuildChapterSchedule(toChapterTracks(tracks), cfg.Crossfade)
		mixDurSec = sched.TotalDuration
	}

	vizCfg := cfg.VisualizerConfig()
	calPathFn := calibrationPathFn
	calPath, _ := calPathFn()
	cal := LoadCalibration(calPath)

	if isSegmentedConfig(cfg) {
		layer0, segs, segInputs, _, err := resolveTrackSegmentsWithMeta(ctx, cfg, tracks, "", artProbed, runner)
		if err != nil {
			return render.MixEstimate{}, nil, "", nil, err
		}

		pathKind := render.PathD
		if layer0.Mode == render.Layer0Media {
			if p, err := render.SelectPath(layer0.BGPath, cfg.OverlayTitles); err == nil {
				pathKind = p
			}
		}

		var resolvedBG probe.BackgroundMeta
		if bgMeta != nil {
			resolvedBG = *bgMeta
		} else if runner != nil && layer0.Mode == render.Layer0Media && layer0.BGKind == render.BackgroundKindVideo {
			if probed, err := probe.ProbeBackgroundVisual(ctx, layer0.BGPath, runner); err == nil {
				resolvedBG = probed
			}
		}

		uniformFPS := render.ResolveSegmentedFPS(render.SegmentedRenderOptions{
			Layer0:   layer0,
			Segments: segs,
		})
		var encPtr *render.EncoderInfo
		encName := "libx264"
		if uniformFPS > 10 && runner != nil {
			if layer0.Mode == render.Layer0Visualizer && layer0.Visualizer.Preset != "black" {
				enc := render.DetectPathDEncoder(ctx, runtime.GOOS, layer0.Visualizer.Resolution, runner)
				encPtr = &enc
				encName = enc.Name
			} else {
				enc := render.DetectEncoder(ctx, runtime.GOOS, runner)
				encPtr = &enc
				encName = enc.Name
			}
		}

		est := render.EstimateMix(render.EstimateInput{
			Path:           pathKind,
			EncoderName:    encName,
			MixDurationSec: mixDurSec,
			TotalAudioSec:  totalAudioSec,
			TrackCount:     len(tracks),
			Normalize:      cfg.Normalize,
			Crossfade:      cfg.Crossfade,
			OverlayTitles:  cfg.OverlayTitles,
			Visualizer:     layer0.Visualizer,
			CPUCores:       runtime.NumCPU(),
			BG: render.BackgroundEstimateInput{
				DurationSec: resolvedBG.DurationSec,
				FPS:         resolvedBG.FPS,
				Width:       resolvedBG.Width,
				Height:      resolvedBG.Height,
				CodecName:   resolvedBG.CodecName,
				PixFmt:      resolvedBG.PixFmt,
			},
			Segmented: true,
			Layer0:    layer0,
			Segments:  segInputs,
		}, cal)
		return est, encPtr, calPath, cal, nil
	}

	pathKind, err := render.SelectPath(cfg.BG, cfg.OverlayTitles)
	if err != nil {
		pathKind = render.PathD
	}

	var resolvedBG probe.BackgroundMeta
	if bgMeta != nil {
		resolvedBG = *bgMeta
	} else if runner != nil && strings.TrimSpace(cfg.BG) != "" && (pathKind == render.PathA || pathKind == render.PathB) {
		if probed, err := probe.ProbeBackgroundVisual(ctx, cfg.BG, runner); err == nil {
			resolvedBG = probed
		}
	}

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

	return est, encPtr, calPath, cal, nil
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

	est, _, _, _, err := buildEstimateContext(ctx, preparedCfg, tracks, resolvedBG, false, runner)
	if err != nil {
		return render.MixEstimate{}, err
	}
	return est, nil
}

var calibrationPathFn = DefaultCalibrationPath

type etaTracker struct {
	est             render.MixEstimate
	mixDuration     float64
	renderStart     time.Time
	muxStart        time.Time
	lastSubstage    string
	substageStart   time.Time
	emaSpeed        float64
	normLoopWallSec float64
	muxWallSec      float64
	renderWallSec   float64
}

func (t *etaTracker) update(u render.ProgressUpdate) float64 {
	now := time.Now()
	if u.Substage != t.lastSubstage {
		if t.lastSubstage == "" && !t.renderStart.IsZero() {
			t.substageStart = t.renderStart
		} else if u.Substage == "mux" && !t.muxStart.IsZero() {
			t.substageStart = t.muxStart
		} else {
			t.substageStart = now
		}
		t.lastSubstage = u.Substage
		t.emaSpeed = 0
	}
	elapsedSub := now.Sub(t.substageStart).Seconds()
	switch u.Substage {
	case "norm_loop":
		t.normLoopWallSec = elapsedSub
		if u.Done {
			t.muxStart = now
		}
	case "mux":
		if !t.muxStart.IsZero() {
			elapsedSub = now.Sub(t.muxStart).Seconds()
		}
		t.muxWallSec = elapsedSub
	case "render":
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
		audioWork := est.TotalAudioSec
		if audioWork <= 0 {
			audioWork = mixDur
		}
		if RecordObservedSpeed(cal, render.AudioCalibrationKey("loudnorm"), audioWork/normElapsed, mixDur, now) {
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

	if !cfg.Segmented {
		videoWork := est.VideoWorkSec
		if videoWork <= 0 {
			videoWork = mixDur
		}

		switch est.Path {
		case render.PathA:
			if est.PathANormWorkSec > 0 && tracker.normLoopWallSec > 0.05 {
				if RecordObservedSpeed(cal, render.CalibrationKey(render.PathA, "norm_loop", render.Resolution1080p), est.PathANormWorkSec/tracker.normLoopWallSec, mixDur, now) {
					changed = true
				}
			}
			if tracker.muxWallSec > 0.05 {
				if RecordObservedSpeed(cal, render.CalibrationKey(render.PathA, "mux", render.Resolution1080p), mixDur/tracker.muxWallSec, mixDur, now) {
					changed = true
				}
			}
		case render.PathB:
			if tracker.renderWallSec > 0.2 {
				if RecordObservedSpeed(cal, render.CalibrationKey(render.PathB, est.EncoderName, render.Resolution1080p), videoWork/tracker.renderWallSec, mixDur, now) {
					changed = true
				}
			}
		case render.PathC:
			if tracker.renderWallSec > 0.2 {
				tag := "2fps"
				if cfg.OverlayTitles {
					tag = "10fps"
				}
				if RecordObservedSpeed(cal, render.CalibrationKey(render.PathC, "libx264", tag), videoWork/tracker.renderWallSec, mixDur, now) {
					changed = true
				}
			}
		case render.PathD:
			if tracker.renderWallSec > 0.2 {
				encKey := est.EncoderName
				if strings.EqualFold(strings.TrimSpace(cfg.Visualizer), "black") {
					encKey = "black-2fps"
					if cfg.OverlayTitles {
						encKey = "black-10fps"
					}
				}
				if RecordObservedSpeed(cal, render.CalibrationKey(render.PathD, encKey, cfg.VisualizerResolution), videoWork/tracker.renderWallSec, mixDur, now) {
					changed = true
				}
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
	var tracks []Track
	if cfg.Segmented {
		tracks, err = ProbeTracksWithArt(ctx, cfg.AudioDir, cfg.Shuffle, nil, runner)
	} else {
		tracks, err = ProbeTracks(ctx, cfg.AudioDir, cfg.Shuffle, nil, runner)
	}
	if err != nil {
		return err
	}
	return runPipeline(ctx, cfg, tracks, cfg.Segmented, sink, start, runner, streamRunner)
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
		if err := probe.ValidateTrackFile(tr.Path); err != nil {
			return fmt.Errorf("track %d: %w", i+1, err)
		}
		if strings.TrimSpace(tr.FileName) == "" {
			tr.FileName = filepath.Base(tr.Path)
		}
		if tr.Duration <= 0 || math.IsNaN(tr.Duration) || math.IsInf(tr.Duration, 0) {
			return fmt.Errorf("track %q must have a positive duration (got %v)", tr.FileName, tr.Duration)
		}
		if strings.TrimSpace(tr.DisplayTitle) == "" {
			tr.DisplayTitle = probe.ResolveDisplayTitle("", tr.FileName)
		}
		resolved[i] = tr
	}

	emitStep(sink, 1, 5, 4, -1, 0, fmt.Sprintf("Using %d selected Track(s)...", len(resolved)))
	return runPipeline(ctx, cfg, resolved, false, sink, start, runner, streamRunner)
}

func runPipeline(ctx context.Context, cfg Config, tracks []Track, artProbed bool, sink TelemetrySink, start time.Time, runner CommandRunner, streamRunner StreamRunner) error {
	tempDir, err := os.MkdirTemp("", "ytmix-*")
	if err != nil {
		return fmt.Errorf("create temp workspace: %w", err)
	}
	defer os.RemoveAll(tempDir)

	est, encPtr, calPath, cal, err := buildEstimateContext(ctx, cfg, tracks, nil, artProbed, runner)
	if err != nil {
		return err
	}
	bounds := computeStageBounds(est.Weights)

	step2Detail := fmt.Sprintf("Processing & merging %d Track(s) (normalize=%v, crossfade=%.2fs)...", len(tracks), cfg.Normalize, cfg.Crossfade)
	estCopy := est
	emitTelemetry(sink, TelemetryEvent{
		Kind:              EventStep,
		Step:              2,
		TotalSteps:        5,
		Progress:          bounds.step2Start,
		Message:           fmt.Sprintf("[2/5] %s", step2Detail),
		Detail:            step2Detail,
		RemainingSec:      est.PointSec,
		EstimatedTotalSec: est.PointSec,
		Estimate:          &estCopy,
	})
	audioStart := time.Now()
	var normDoneAt time.Time
	var onNormProgress func(completed, total int)
	if cfg.Normalize {
		onNormProgress = func(completed, total int) {
			if completed == total {
				normDoneAt = time.Now()
			}
			if sink == nil || total <= 0 {
				return
			}
			normShare := 0.5
			if est.AudioSec > 0 {
				normShare = est.NormalizeSec / est.AudioSec
			}
			pct := min(bounds.step2Start+int(math.Round(float64(bounds.audioSpan)*normShare*(float64(completed)/float64(total)))), bounds.step3Start-1)
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

	mergedAudioPath, updatedTracks, err := audio.ProcessAndMergeWithProgress(ctx, tracks, tempDir, cfg.Normalize, cfg.Crossfade, runner, onNormProgress)
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

	emitStep(sink, 3, 5, bounds.step3Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, fmt.Sprintf("Writing Chapter Timestamps to %s...", cfg.Timestamps))
	sched, err := subtitle.WriteChapters(cfg.Timestamps, toChapterTracks(updatedTracks), cfg.Crossfade)
	if err != nil {
		return err
	}

	var assPath string
	var layer0 render.Layer0Spec
	var segments []render.TrackSegmentSpec
	var transition render.TrackTransition

	if cfg.Segmented {
		// Preserve original .mp3 paths & embedded art metadata while adopting re-probed normalized durations.
		tracksForSegs := make([]Track, len(tracks))
		for i, tr := range tracks {
			if i < len(updatedTracks) {
				tr.Duration = updatedTracks[i].Duration
				if strings.TrimSpace(updatedTracks[i].DisplayTitle) != "" {
					tr.DisplayTitle = updatedTracks[i].DisplayTitle
				}
			}
			tracksForSegs[i] = tr
		}
		if cfg.OverlayTitles {
			emitStep(sink, 4, 5, bounds.step4Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, fmt.Sprintf("Preparing per-Track segments & Title Overlays (.ass) in %s...", tempDir))
		} else {
			emitStep(sink, 4, 5, bounds.step4Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, "Resolving per-Track segments (--overlay-titles=false).")
		}
		layer0, segments, _, transition, err = resolveTrackSegmentsWithMeta(ctx, cfg, tracksForSegs, tempDir, artProbed, runner)
		if err != nil {
			return err
		}
	} else {
		if cfg.OverlayTitles {
			assPath = filepath.Join(tempDir, "titles.ass")
			emitStep(sink, 4, 5, bounds.step4Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, fmt.Sprintf("Generating Title Overlays (.ass) at %s...", assPath))
			if err := subtitle.WriteASSWithOptions(assPath, sched.Cues, sched.TotalDuration, subtitle.ASSOptions{
				TitleDuration: cfg.TitleDuration,
				FontName:      cfg.FontName,
				DimTitles:     cfg.DimTitles,
			}); err != nil {
				return err
			}
		} else {
			emitStep(sink, 4, 5, bounds.step4Start, math.Max(0, est.PointSec-time.Since(start).Seconds()), est.PointSec, "Skipping Title Overlays (--overlay-titles=false).")
		}
	}

	emitStep(sink, 5, 5, bounds.step5Start, math.Max(0, est.VideoSec), est.PointSec, fmt.Sprintf("Rendering Mix video to %s...", cfg.Out))
	var renderStream render.StreamRunner
	if streamRunner != nil {
		renderStream = render.StreamRunner(streamRunner)
	}
	renderStart := time.Now()
	tracker := &etaTracker{
		est:           est,
		mixDuration:   sched.TotalDuration,
		renderStart:   renderStart,
		substageStart: renderStart,
	}
	var onRenderProgress render.ProgressFunc
	if sink != nil || sched.TotalDuration >= minCalibratedMixSec {
		onRenderProgress = func(u render.ProgressUpdate) {
			var rem float64
			if cfg.Segmented {
				frac := u.Fraction
				if frac < 0 {
					frac = 0
				} else if frac > 1 {
					frac = 1
				}
				rem = math.Max(0, est.VideoSec*(1.0-frac))
			} else {
				rem = tracker.update(u)
			}
			if sink == nil {
				return
			}
			pct := max(bounds.step5Start+int(math.Round(u.Fraction*float64(bounds.step5Span))), bounds.step5Start)
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

	var renderLabel string
	if cfg.Segmented {
		if err := render.RenderSegmented(ctx, render.SegmentedRenderOptions{
			Layer0:          layer0,
			Segments:        segments,
			MergedAudioPath: mergedAudioPath,
			TempDir:         tempDir,
			OutPath:         cfg.Out,
			Transition:      transition,
			TotalDuration:   sched.TotalDuration,
			GOOS:            runtime.GOOS,
			Encoder:         encPtr,
			Runner:          runner,
			StreamRunner:    renderStream,
			OnProgress:      onRenderProgress,
		}); err != nil {
			return err
		}
		renderLabel = "Segmented (Option A)"
	} else {
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
		if pathKind == render.PathA {
			if !tracker.muxStart.IsZero() {
				tracker.muxWallSec = time.Since(tracker.muxStart).Seconds()
			}
		} else {
			tracker.renderWallSec = time.Since(renderStart).Seconds()
		}
		renderLabel = string(pathKind)
	}
	if calPath != "" && sched.TotalDuration >= minCalibratedMixSec {
		recordCalibrationResults(calPath, cal, cfg, est, sched.TotalDuration, normElapsed, mergeElapsed, tracker)
	}

	doneMsg := fmt.Sprintf("Done! Rendered %s via %s in %s (audio duration %.2fs, chapters: %s).",
		cfg.Out, renderLabel, time.Since(start).Round(time.Millisecond), sched.TotalDuration, cfg.Timestamps)
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
