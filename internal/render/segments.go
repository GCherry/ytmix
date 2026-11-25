package render

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// TrackLayerMode selects how an individual Track's visual composes against Layer 0.
type TrackLayerMode string

const (
	// LayerFullscreen fills the 16:9 canvas with the Track's visual, covering Layer 0.
	LayerFullscreen TrackLayerMode = "fullscreen"
	// LayerArtCard renders a centered framed Track visual card over Layer 0.
	LayerArtCard TrackLayerMode = "card"
	// LayerBackdrop shows Layer 0 uninterrupted without a per-Track visual overlay.
	LayerBackdrop TrackLayerMode = "backdrop"
)

// TrackTransition selects the visual transition applied at Track segment boundaries.
type TrackTransition string

const (
	// TransitionCut switches directly between consecutive Track segments.
	TransitionCut TrackTransition = "cut"
	// TransitionDip applies a 0.5s fade-to-black dip across Track segment boundaries.
	TransitionDip TrackTransition = "dip"
)

// Layer0Mode selects the Mix-wide Layer 0 backdrop source for segmented rendering.
type Layer0Mode string

const (
	// Layer0Black renders a solid #000000 Mix Backdrop.
	Layer0Black Layer0Mode = "black"
	// Layer0Media renders a static image or looping video/GIF Mix Backdrop (--bg).
	Layer0Media Layer0Mode = "media"
	// Layer0Visualizer renders a Path D Auto Visualizer Mix Backdrop (--visualizer).
	Layer0Visualizer Layer0Mode = "visualizer"
)

// Layer0Spec configures the Mix-wide Layer 0 backdrop for Engine Option A segmented rendering.
type Layer0Spec struct {
	Mode       Layer0Mode
	BGPath     string
	BGKind     BackgroundKind
	Visualizer VisualizerConfig
}

// TrackSegmentSpec describes one Track's time slot and visual layer configuration.
type TrackSegmentSpec struct {
	Index          int
	StartSec       float64
	SlotDuration   float64
	MediaPath      string
	MediaKind      BackgroundKind
	LayerMode      TrackLayerMode
	SegmentASSPath string
	OverlayTitles  bool
}

// SegmentedRenderOptions configures Engine Option A per-Track segment rendering and concat muxing.
type SegmentedRenderOptions struct {
	Layer0          Layer0Spec
	Segments        []TrackSegmentSpec
	MergedAudioPath string
	TempDir         string
	OutPath         string
	Transition      TrackTransition
	TotalDuration   float64
	GOOS            string
	Encoder         *EncoderInfo
	Runner          func(ctx context.Context, name string, args ...string) ([]byte, error)
	StreamRunner    StreamRunner
	OnProgress      ProgressFunc
}

var segmentConcatPathEscaper = strings.NewReplacer(
	`\`, `/`,
	`'`, `'\''`,
)

func escapeSegmentConcatPath(path string) string {
	return segmentConcatPathEscaper.Replace(path)
}

// ValidateTrackLayerMode normalizes a TrackLayerMode and defaults empty values to LayerBackdrop.
func ValidateTrackLayerMode(mode TrackLayerMode) (TrackLayerMode, error) {
	norm := TrackLayerMode(strings.ToLower(strings.TrimSpace(string(mode))))
	switch norm {
	case "":
		return LayerBackdrop, nil
	case LayerFullscreen, LayerArtCard, LayerBackdrop:
		return norm, nil
	default:
		return "", fmt.Errorf("unknown track layer mode %q (supported: fullscreen, card, backdrop)", mode)
	}
}

// ValidateTrackTransition normalizes a TrackTransition and defaults empty values to TransitionCut.
func ValidateTrackTransition(tr TrackTransition) (TrackTransition, error) {
	norm := TrackTransition(strings.ToLower(strings.TrimSpace(string(tr))))
	switch norm {
	case "":
		return TransitionCut, nil
	case TransitionCut, TransitionDip:
		return norm, nil
	default:
		return "", fmt.Errorf("unknown --track-transition %q (supported: cut, dip)", tr)
	}
}

// ValidateLayer0Spec normalizes and validates a Layer0Spec.
func ValidateLayer0Spec(spec Layer0Spec) (Layer0Spec, error) {
	mode := Layer0Mode(strings.ToLower(strings.TrimSpace(string(spec.Mode))))
	if mode == "" {
		mode = Layer0Black
	}
	if spec.Visualizer.FadeToBlack < 0 {
		return Layer0Spec{}, fmt.Errorf("--fade-to-black must be >= 0 (got %s)", spec.Visualizer.FadeToBlack)
	}

	out := spec
	out.Mode = mode
	switch mode {
	case Layer0Black:
		return out, nil
	case Layer0Media:
		if strings.TrimSpace(out.BGPath) == "" {
			return Layer0Spec{}, errors.New("layer 0 background path is required when mode is media")
		}
		if out.BGKind == "" {
			kind, _, err := ClassifyBackground(out.BGPath)
			if err != nil {
				return Layer0Spec{}, err
			}
			out.BGKind = kind
		} else if out.BGKind != BackgroundKindImage && out.BGKind != BackgroundKindVideo {
			return Layer0Spec{}, fmt.Errorf("invalid layer 0 background kind %q", out.BGKind)
		}
		return out, nil
	case Layer0Visualizer:
		vizCfg, err := ValidateVisualizerConfig(out.Visualizer)
		if err != nil {
			return Layer0Spec{}, err
		}
		out.Visualizer = vizCfg
		return out, nil
	default:
		return Layer0Spec{}, fmt.Errorf("unknown layer 0 mode %q (supported: black, media, visualizer)", spec.Mode)
	}
}

func resolveMediaKind(path string, kind BackgroundKind) BackgroundKind {
	if kind == BackgroundKindImage || kind == BackgroundKindVideo {
		return kind
	}
	if strings.TrimSpace(path) == "" {
		return ""
	}
	classified, _, err := ClassifyBackground(path)
	if err != nil {
		return ""
	}
	return classified
}

func normalizeLayer0Mode(mode Layer0Mode) Layer0Mode {
	norm := Layer0Mode(strings.ToLower(strings.TrimSpace(string(mode))))
	if norm == "" {
		return Layer0Black
	}
	return norm
}

type segmentedFPSItem struct {
	OverlayTitles bool
	LayerMode     TrackLayerMode
	MediaKind     BackgroundKind
}

func resolveUniformSegmentedFPS(layer0 Layer0Spec, l0BGKind BackgroundKind, defaultOverlayTitles bool, items []segmentedFPSItem) int {
	l0Mode := normalizeLayer0Mode(layer0.Mode)
	l0VizPreset := strings.ToLower(strings.TrimSpace(layer0.Visualizer.Preset))
	if l0VizPreset == "" {
		l0VizPreset = DefaultVisualizerPreset
	}
	l0IsVideo := l0Mode == Layer0Media && l0BGKind == BackgroundKindVideo
	l0IsReactiveViz := l0Mode == Layer0Visualizer && l0VizPreset != "black"

	anyTitles := defaultOverlayTitles
	for _, item := range items {
		if item.OverlayTitles {
			anyTitles = true
		}
		layerMode, err := ValidateTrackLayerMode(item.LayerMode)
		if err != nil {
			layerMode = LayerBackdrop
		}
		switch layerMode {
		case LayerFullscreen:
			if item.MediaKind == BackgroundKindVideo {
				return 30
			}
		case LayerArtCard:
			if item.MediaKind == BackgroundKindVideo || l0IsVideo || l0IsReactiveViz {
				return 30
			}
		case LayerBackdrop:
			if l0IsVideo || l0IsReactiveViz {
				return 30
			}
		}
	}
	if anyTitles {
		return 10
	}
	return 2
}

// ResolveAutoTrackLayerMode returns the default Layer 1 mode for auto-discovered media:
// Art Card for embedded ID3 artwork or near-square images (aspect ratio <= 1.25), Fullscreen otherwise.
func ResolveAutoTrackLayerMode(isEmbeddedID3 bool, width, height int) TrackLayerMode {
	if isEmbeddedID3 {
		return LayerArtCard
	}
	if width > 0 && height > 0 && float64(width)/float64(height) <= 1.25 {
		return LayerArtCard
	}
	return LayerFullscreen
}

// ResolveSegmentedFPS chooses the uniform frame rate across all segments in a segmented Mix:
// 30 fps if any segment effectively renders a video loop or audio-reactive Auto Visualizer,
// 10 fps if all segments are static/black and any has Title Overlays enabled, or 2 fps otherwise.
func ResolveSegmentedFPS(opts SegmentedRenderOptions) int {
	l0BGKind := resolveMediaKind(opts.Layer0.BGPath, opts.Layer0.BGKind)
	items := make([]segmentedFPSItem, len(opts.Segments))
	for i, seg := range opts.Segments {
		items[i] = segmentedFPSItem{
			OverlayTitles: seg.OverlayTitles,
			LayerMode:     seg.LayerMode,
			MediaKind:     resolveMediaKind(seg.MediaPath, seg.MediaKind),
		}
	}
	return resolveUniformSegmentedFPS(opts.Layer0, l0BGKind, false, items)
}

// ResolveSegmentedDimensions returns the uniform output width and height for a segmented Mix.
// It uses the Visualizer resolution preset when Layer 0 is Visualizer or Black, otherwise 1920x1080.
func ResolveSegmentedDimensions(opts SegmentedRenderOptions) (int, int) {
	l0Mode := normalizeLayer0Mode(opts.Layer0.Mode)
	if l0Mode == Layer0Visualizer || l0Mode == Layer0Black {
		res := resolveResolutionOrDefault(opts.Layer0.Visualizer.Resolution)
		return evenDim(res.Width), evenDim(res.Height)
	}
	return 1920, 1080
}

func buildMediaInputArgs(path string, kind BackgroundKind, fps int) []string {
	if kind == BackgroundKindVideo {
		return []string{"-stream_loop", "-1", "-i", path}
	}
	return []string{"-loop", "1", "-framerate", strconv.Itoa(fps), "-i", path}
}

func buildSegmentDipFilters(seg TrackSegmentSpec, transition TrackTransition, totalMixDuration float64) []string {
	if transition != TransitionDip {
		return nil
	}
	slotDur := seg.SlotDuration
	dipDur := 0.5
	if slotDur < 1.2 {
		dipDur = math.Round(slotDur*0.35*100) / 100
		if dipDur > 0.5 {
			dipDur = 0.5
		}
	}
	if dipDur <= 0.01 {
		return nil
	}

	var filters []string
	if seg.Index > 0 {
		filters = append(filters, fmt.Sprintf("fade=t=in:st=0:d=%s:color=black", formatFFmpegSeconds(dipDur)))
	}
	isNonFinal := totalMixDuration > 0 && seg.StartSec+slotDur < totalMixDuration-0.01
	if isNonFinal {
		fadeOutStart := math.Max(0, slotDur-dipDur)
		filters = append(filters, fmt.Sprintf("fade=t=out:st=%s:d=%s:color=black", formatFFmpegSeconds(fadeOutStart), formatFFmpegSeconds(dipDur)))
	}
	return filters
}

func buildSegmentFadeToBlackFilters(fadeToBlack time.Duration, segStart, slotDur, totalMixDuration float64) (needsGBRP bool, filters []string) {
	if fadeToBlack <= 0 {
		return false, nil
	}
	fadeStart := fadeToBlack.Seconds()
	if totalMixDuration > 0 && fadeStart >= totalMixDuration {
		return false, nil
	}
	fadeDur := 15.0
	if totalMixDuration > 0 {
		if rem := totalMixDuration - fadeStart; rem < fadeDur {
			fadeDur = rem
		}
	}
	if fadeDur < 1.0 {
		fadeDur = 1.0
	}
	fadeEnd := fadeStart + fadeDur
	segEnd := segStart + slotDur

	if fadeStart >= segEnd {
		return false, nil
	}
	if segStart >= fadeEnd {
		return false, []string{"drawbox=x=0:y=0:w=iw:h=ih:color=black:t=fill"}
	}
	if fadeStart >= segStart {
		localStart := fadeStart - segStart
		return false, []string{
			fmt.Sprintf("fade=t=out:st=%s:d=%s:color=black", formatFFmpegSeconds(localStart), formatFFmpegSeconds(fadeDur)),
		}
	}
	remDur := fadeEnd - segStart
	remFrac := remDur / fadeDur
	if remFrac < 0.01 {
		return false, []string{"drawbox=x=0:y=0:w=iw:h=ih:color=black:t=fill"}
	}
	return true, []string{
		fmt.Sprintf("colorchannelmixer=rr=%.2f:gg=%.2f:bb=%.2f", remFrac, remFrac, remFrac),
		fmt.Sprintf("fade=t=out:st=0:d=%s:color=black", formatFFmpegSeconds(remDur)),
	}
}

func buildSegmentPostFilters(seg TrackSegmentSpec, transition TrackTransition, fadeToBlack time.Duration, totalMixDuration float64, alreadyGBRP bool) []string {
	var filters []string
	if seg.OverlayTitles {
		filters = append(filters, fmt.Sprintf("subtitles='%s'", EscapeFFmpegFilterPath(seg.SegmentASSPath)))
	}
	filters = append(filters, buildSegmentDipFilters(seg, transition, totalMixDuration)...)
	needsGBRP, sleepFilters := buildSegmentFadeToBlackFilters(fadeToBlack, seg.StartSec, seg.SlotDuration, totalMixDuration)
	if needsGBRP && !alreadyGBRP {
		filters = append(filters, "format=gbrp")
	}
	filters = append(filters, sleepFilters...)
	filters = append(filters, "format=yuv420p")
	return filters
}

func buildLayer0VisualizerChain(cfg VisualizerConfig, width, height, fps int) (string, string) {
	spec, _ := LookupVisualizer(cfg.Preset)
	theme, _ := LookupTheme(cfg.Theme)
	res := ResolutionSpec{Width: width, Height: height}

	primary, hasBuiltInBlur := buildPrimaryPresetChain(spec, theme, res, cfg.Glow)
	parts := []string{primary}
	curLabel := "[vbase]"

	if cfg.Glow && !hasBuiltInBlur {
		glowSigma := scaleSigma(12, height)
		parts = append(parts, fmt.Sprintf("%ssplit[vmain][vglow];[vglow]gblur=sigma=%d[vblur];[vmain][vblur]blend=all_mode=screen:all_opacity=0.45[vpost]", curLabel, glowSigma))
		curLabel = "[vpost]"
	}

	// Always emit fps=%d even when fps == 30: filters like showfreqs ignore r=30
	// as CFR and produce VFR streams that drift in duration after concat -c:v copy.
	post := []string{fmt.Sprintf("fps=%d", fps)}
	if cfg.Brightness < MaxVisualizerBrightness {
		factor := float64(cfg.Brightness) / 100.0
		post = append(post, fmt.Sprintf("colorchannelmixer=rr=%.2f:gg=%.2f:bb=%.2f", factor, factor, factor))
	}
	if len(post) > 0 {
		parts = append(parts, fmt.Sprintf("%s%s[vl0]", curLabel, strings.Join(post, ",")))
		curLabel = "[vl0]"
	}

	return strings.Join(parts, ";"), curLabel
}

// BuildSegmentArgs builds the FFmpeg CLI arguments to render a single uniform H.264 video-only Track segment.
func BuildSegmentArgs(
	seg TrackSegmentSpec,
	layer0 Layer0Spec,
	mergedAudioPath string,
	segOutPath string,
	transition TrackTransition,
	fps int,
	width, height int,
	totalMixDuration float64,
	encoderArgs []string,
) ([]string, error) {
	if math.IsNaN(seg.SlotDuration) || math.IsInf(seg.SlotDuration, 0) || seg.SlotDuration <= 0 {
		return nil, fmt.Errorf("segment %d slotDuration must be finite and > 0 (got %v)", seg.Index, seg.SlotDuration)
	}
	if math.IsNaN(seg.StartSec) || math.IsInf(seg.StartSec, 0) || seg.StartSec < 0 {
		return nil, fmt.Errorf("segment %d startSec must be finite and >= 0 (got %v)", seg.Index, seg.StartSec)
	}
	if math.IsNaN(totalMixDuration) || math.IsInf(totalMixDuration, 0) || totalMixDuration < 0 {
		return nil, fmt.Errorf("totalMixDuration must be finite and >= 0 (got %v)", totalMixDuration)
	}
	if strings.TrimSpace(segOutPath) == "" {
		return nil, errors.New("segment output path is required")
	}
	if seg.OverlayTitles && strings.TrimSpace(seg.SegmentASSPath) == "" {
		return nil, fmt.Errorf("segment %d requires SegmentASSPath when OverlayTitles is true", seg.Index)
	}
	if fps <= 0 {
		return nil, fmt.Errorf("fps must be > 0 (got %d)", fps)
	}
	if width < 2 || height < 2 {
		return nil, fmt.Errorf("dimensions must be >= 2x2 (got %dx%d)", width, height)
	}
	width = evenDim(width)
	height = evenDim(height)

	layerMode, err := ValidateTrackLayerMode(seg.LayerMode)
	if err != nil {
		return nil, err
	}
	normTransition, err := ValidateTrackTransition(transition)
	if err != nil {
		return nil, err
	}
	if layer0.Visualizer.FadeToBlack < 0 {
		return nil, fmt.Errorf("--fade-to-black must be >= 0 (got %s)", layer0.Visualizer.FadeToBlack)
	}

	var segKind BackgroundKind
	if layerMode == LayerFullscreen || layerMode == LayerArtCard {
		if strings.TrimSpace(seg.MediaPath) == "" {
			return nil, fmt.Errorf("segment %d requires MediaPath for layer mode %q", seg.Index, layerMode)
		}
		segKind = resolveMediaKind(seg.MediaPath, seg.MediaKind)
		if segKind == "" {
			if _, _, err := ClassifyBackground(seg.MediaPath); err != nil {
				return nil, fmt.Errorf("segment %d media: %w", seg.Index, err)
			}
			return nil, fmt.Errorf("segment %d invalid media kind %q", seg.Index, seg.MediaKind)
		}
	}

	var normLayer0 Layer0Spec
	if layerMode != LayerFullscreen {
		normLayer0, err = ValidateLayer0Spec(layer0)
		if err != nil {
			return nil, err
		}
		if normLayer0.Mode == Layer0Visualizer && normLayer0.Visualizer.Preset != "black" && strings.TrimSpace(mergedAudioPath) == "" {
			return nil, errors.New("mergedAudioPath is required for audio-reactive visualizer Layer 0")
		}
	} else {
		normLayer0 = layer0
	}

	if len(encoderArgs) == 0 {
		encoderArgs = []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	fadeToBlack := normLayer0.Visualizer.FadeToBlack

	switch layerMode {
	case LayerFullscreen:
		args = append(args, buildMediaInputArgs(seg.MediaPath, segKind, fps)...)
		vfParts := []string{
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d", width, height, width, height, fps),
		}
		vfParts = append(vfParts, buildSegmentPostFilters(seg, normTransition, fadeToBlack, totalMixDuration, false)...)
		args = append(args, "-vf", strings.Join(vfParts, ","), "-map", "0:v:0")

	case LayerBackdrop:
		l0Args, isFC, graphArg, mapTarget := buildLayer0SegmentGraph(normLayer0, seg, mergedAudioPath, transition, fadeToBlack, totalMixDuration, width, height, fps, false)
		if isFC {
			return appendSegmentOutputFlags(append(l0Args, "-filter_complex", graphArg, "-map", mapTarget), encoderArgs, seg.SlotDuration, segOutPath), nil
		}
		return appendSegmentOutputFlags(append(l0Args, "-vf", graphArg, "-map", mapTarget), encoderArgs, seg.SlotDuration, segOutPath), nil

	case LayerArtCard:
		l0Args, _, l0Graph, l0Label := buildLayer0SegmentGraph(normLayer0, seg, mergedAudioPath, transition, fadeToBlack, totalMixDuration, width, height, fps, true)
		args = append(l0Args, buildMediaInputArgs(seg.MediaPath, segKind, fps)...)
		cardW := evenDim(width * 62 / 100)
		cardH := evenDim(height * 62 / 100)
		fcParts := []string{
			l0Graph,
			fmt.Sprintf("[1:v]scale=%d:%d:force_original_aspect_ratio=decrease,setsar=1,fps=%d,format=gbrp,pad=iw+16:ih+16:8:8:color=0x111316@0.85[card]", cardW, cardH, fps),
		}
		post := buildSegmentPostFilters(seg, normTransition, fadeToBlack, totalMixDuration, true)
		fcParts = append(fcParts, fmt.Sprintf("%s[card]overlay=(W-w)/2:(H-h)/2:shortest=1:format=gbrp,%s[vout]", l0Label, strings.Join(post, ",")))
		args = append(args, "-filter_complex", strings.Join(fcParts, ";"), "-map", "[vout]")
	}

	return appendSegmentOutputFlags(args, encoderArgs, seg.SlotDuration, segOutPath), nil
}

func appendSegmentOutputFlags(args, encoderArgs []string, slotDuration float64, segOutPath string) []string {
	args = append(args, encoderArgs...)
	args = append(args, "-pix_fmt", "yuv420p")
	args = append(args, bt709ColorArgs...)
	args = append(args, "-video_track_timescale", "90000")
	args = append(args, "-t", formatFFmpegSeconds(slotDuration), "-an", segOutPath)
	return args
}

func buildLayer0SegmentGraph(
	normLayer0 Layer0Spec,
	seg TrackSegmentSpec,
	mergedAudioPath string,
	transition TrackTransition,
	fadeToBlack time.Duration,
	totalMixDuration float64,
	width, height, fps int,
	forArtCard bool,
) (inArgs []string, isFilterComplex bool, graph string, outLabel string) {
	inArgs = []string{"-y", "-hide_banner", "-loglevel", "error"}
	useBlack := normLayer0.Mode == Layer0Black || (normLayer0.Mode == Layer0Visualizer && normLayer0.Visualizer.Preset == "black")

	switch {
	case useBlack:
		inArgs = append(inArgs, "-f", "lavfi", "-i", fmt.Sprintf("color=c=black:s=%dx%d:r=%d", width, height, fps))
		if forArtCard {
			return inArgs, true, "[0:v]setsar=1,format=gbrp[l0]", "[l0]"
		}
		vfParts := append([]string{"setsar=1"}, buildSegmentPostFilters(seg, transition, fadeToBlack, totalMixDuration, false)...)
		return inArgs, false, strings.Join(vfParts, ","), "0:v:0"

	case normLayer0.Mode == Layer0Media:
		inArgs = append(inArgs, buildMediaInputArgs(normLayer0.BGPath, normLayer0.BGKind, fps)...)
		scaleFilter := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%d", width, height, width, height, fps)
		if forArtCard {
			return inArgs, true, fmt.Sprintf("[0:v]%s,format=gbrp[l0]", scaleFilter), "[l0]"
		}
		vfParts := append([]string{scaleFilter}, buildSegmentPostFilters(seg, transition, fadeToBlack, totalMixDuration, false)...)
		return inArgs, false, strings.Join(vfParts, ","), "0:v:0"

	default:
		inArgs = append(inArgs,
			"-ss", formatFFmpegSeconds(seg.StartSec),
			"-t", formatFFmpegSeconds(seg.SlotDuration),
			"-i", mergedAudioPath,
		)
		vizChain, l0Label := buildLayer0VisualizerChain(normLayer0.Visualizer, width, height, fps)
		if forArtCard {
			return inArgs, true, vizChain, l0Label
		}
		post := buildSegmentPostFilters(seg, transition, fadeToBlack, totalMixDuration, true)
		return inArgs, true, fmt.Sprintf("%s;%s%s[vout]", vizChain, l0Label, strings.Join(post, ",")), "[vout]"
	}
}

// BuildSegmentConcatArgs constructs the FFmpeg arguments to stream-copy concatenated video segments
// together with merged_audio.m4a into the final Mix MP4.
func BuildSegmentConcatArgs(concatListPath, mergedAudioPath, outPath string) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "concat",
		"-safe", "0",
		"-i", concatListPath,
		"-i", mergedAudioPath,
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "copy",
		"-movflags", "+faststart",
		outPath,
	}
}

func buildSegmentConcatList(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("file '")
		b.WriteString(escapeSegmentConcatPath(p))
		b.WriteString("'\n")
	}
	return b.String()
}

func segmentedWorkerLimit() int {
	return min(max(runtime.GOMAXPROCS(0)/2, 1), 4)
}

// RenderSegmented executes Engine Option A per-Track rendering in bounded parallel workers,
// writes seg_concat.txt, and muxes the concatenated video stream with merged_audio.m4a.
func RenderSegmented(ctx context.Context, opts SegmentedRenderOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(opts.Segments) == 0 {
		return errors.New("at least one track segment is required")
	}
	if strings.TrimSpace(opts.MergedAudioPath) == "" {
		return errors.New("merged audio path is required")
	}
	if strings.TrimSpace(opts.TempDir) == "" {
		return errors.New("temp directory is required")
	}
	if strings.TrimSpace(opts.OutPath) == "" {
		return errors.New("output path is required")
	}

	transition, err := ValidateTrackTransition(opts.Transition)
	if err != nil {
		return err
	}
	layer0, err := ValidateLayer0Spec(opts.Layer0)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(opts.TempDir, 0750); err != nil {
		return fmt.Errorf("create temp directory %q: %w", opts.TempDir, err)
	}
	// #nosec G301 -- user-facing output directory uses standard 0755 permissions.
	if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0755); err != nil {
		return fmt.Errorf("create output directory for %q: %w", opts.OutPath, err)
	}

	normOpts := opts
	normOpts.Layer0 = layer0
	normOpts.Transition = transition

	totalDur := opts.TotalDuration
	if totalDur <= 0 {
		for _, seg := range opts.Segments {
			if end := seg.StartSec + seg.SlotDuration; end > totalDur {
				totalDur = end
			}
		}
	}

	fps := ResolveSegmentedFPS(normOpts)
	width, height := ResolveSegmentedDimensions(normOpts)

	var encoderArgs []string
	if opts.Encoder != nil && len(opts.Encoder.Args) > 0 {
		encoderArgs = opts.Encoder.Args
	} else if fps <= 10 {
		encoderArgs = []string{"-c:v", "libx264", "-tune", "stillimage", "-preset", "veryfast", "-crf", "18"}
	} else if layer0.Mode == Layer0Visualizer && layer0.Visualizer.Preset != "black" {
		encoderArgs = DetectPathDEncoder(ctx, opts.GOOS, layer0.Visualizer.Resolution, opts.Runner).Args
	} else {
		encoderArgs = DetectEncoder(ctx, opts.GOOS, opts.Runner).Args
	}

	segPaths := make([]string, len(opts.Segments))
	segArgsList := make([][]string, len(opts.Segments))
	segWeights := make([]float64, len(opts.Segments))
	var sumSlotDur float64
	for i, seg := range opts.Segments {
		segPath := filepath.Join(opts.TempDir, fmt.Sprintf("seg_%04d.mp4", i))
		segPaths[i] = segPath
		args, err := BuildSegmentArgs(seg, layer0, opts.MergedAudioPath, segPath, transition, fps, width, height, totalDur, encoderArgs)
		if err != nil {
			return fmt.Errorf("build segment %d args: %w", i, err)
		}
		segArgsList[i] = args
		sumSlotDur += seg.SlotDuration
	}
	for i, seg := range opts.Segments {
		if sumSlotDur > 0 {
			segWeights[i] = seg.SlotDuration / sumSlotDur
		} else {
			segWeights[i] = 1.0 / float64(len(opts.Segments))
		}
	}

	const segStageMaxFrac = 0.92
	segFracs := make([]float64, len(opts.Segments))
	var progressMu sync.Mutex
	emitSegProgress := func(substage string, segIdx int, subFrac float64, outTime float64, speed string) {
		if opts.OnProgress == nil {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		if subFrac > segFracs[segIdx] {
			segFracs[segIdx] = clamp01(subFrac)
		}
		var weighted float64
		for k, f := range segFracs {
			weighted += f * segWeights[k]
		}
		opts.OnProgress(ProgressUpdate{
			Substage:   substage,
			Fraction:   segStageMaxFrac * clamp01(weighted),
			OutTimeSec: outTime,
			Speed:      speed,
			Done:       false,
		})
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(segmentedWorkerLimit())
	for i, seg := range opts.Segments {
		segIdx := i
		segDur := seg.SlotDuration
		segArgs := segArgsList[i]
		substage := fmt.Sprintf("segment_%04d", segIdx)
		g.Go(func() error {
			var segProgress ProgressFunc
			if opts.OnProgress != nil {
				segProgress = func(u ProgressUpdate) {
					emitSegProgress(substage, segIdx, u.Fraction, u.OutTimeSec, u.Speed)
				}
			}
			if err := runFFmpegRender(gctx, substage, segArgs, segDur, 0, 1.0, opts.Runner, opts.StreamRunner, segProgress); err != nil {
				return fmt.Errorf("render track segment %d: %w", segIdx, err)
			}
			emitSegProgress(substage, segIdx, 1.0, segDur, "")
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	concatListPath := filepath.Join(opts.TempDir, "seg_concat.txt")
	if err := os.WriteFile(concatListPath, []byte(buildSegmentConcatList(segPaths)), 0600); err != nil {
		return fmt.Errorf("write segment concat list: %w", err)
	}

	concatArgs := BuildSegmentConcatArgs(concatListPath, opts.MergedAudioPath, opts.OutPath)
	if err := runFFmpegRender(ctx, "concat", concatArgs, totalDur, segStageMaxFrac, 1.0, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
		return fmt.Errorf("concat rendered segments: %w", err)
	}
	return nil
}
