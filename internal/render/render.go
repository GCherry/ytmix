package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Path identifies one of the four FFmpeg video Rendering Paths.
type Path string

const (
	// PathA pre-normalizes a looping video/GIF once and muxes infinitely via -c:v copy.
	PathA Path = "Path A"
	// PathB burns Title Overlays into a looping video/GIF using hardware acceleration with libx264 fallback.
	PathB Path = "Path B"
	// PathC encodes a static image Background Visual with -loop 1 -tune stillimage.
	PathC Path = "Path C"
	// PathD generates a 1080p Auto Visualizer (or pure black sleep screen) directly via FFmpeg without a Background Visual file.
	PathD Path = "Path D"

	// PathANormWeight allocates 20%-35% of overall pipeline progress (15 of 79 Step-5 percentage points)
	// to Path A's norm_loop pre-normalization, leaving 35%-99% for the full-duration mux.
	PathANormWeight = 15.0 / 79.0
)

const scalePadFilter = "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1"

var filterPathEscaper = strings.NewReplacer(
	`\`, `/`,
	`:`, `\:`,
	`'`, `\'`,
)

// StreamRunner executes an external command and delivers each stdout line to onStdoutLine.
type StreamRunner func(ctx context.Context, onStdoutLine func(string), name string, args ...string) error

// ProgressUpdate reports normalized Step 5 rendering progress in [0.0, 1.0].
type ProgressUpdate struct {
	Substage   string
	Fraction   float64
	OutTimeSec float64
	Speed      string
	Done       bool
}

// ProgressFunc receives live Step 5 progress updates during Render.
type ProgressFunc func(ProgressUpdate)

// ProgressSample accumulates key=value fields from a single FFmpeg -progress block.
type ProgressSample struct {
	OutTimeSec float64
	Speed      string
	Done       bool
}

// ParseProgressLine parses one FFmpeg -progress pipe:1 key=value line into sample.
// It returns true when a complete progress block boundary (progress=continue or progress=end) arrives.
func ParseProgressLine(line string, sample *ProgressSample) bool {
	if sample == nil {
		return false
	}
	key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return false
	}
	key = strings.TrimSpace(key)
	val = strings.TrimSpace(val)

	switch key {
	case "out_time_us":
		if val == "" || strings.EqualFold(val, "N/A") {
			return false
		}
		if us, err := strconv.ParseInt(val, 10, 64); err == nil {
			if us < 0 {
				us = 0
			}
			sample.OutTimeSec = float64(us) / 1_000_000.0
		}
	case "speed":
		if val == "" || strings.EqualFold(val, "N/A") {
			sample.Speed = ""
			return false
		}
		sample.Speed = val
	case "progress":
		sample.Done = val == "end"
		return true
	}
	return false
}

// ParseFFmpegSpeed parses an FFmpeg -progress speed value (e.g. "2.45x", "1.44e+03x")
// into a positive multiplier, returning false for empty or "N/A" values.
func ParseFFmpegSpeed(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0, false
	}
	if strings.HasSuffix(s, "x") || strings.HasSuffix(s, "X") {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// BackgroundKind identifies whether a Background Visual is a static image or looping video/GIF.
type BackgroundKind string

const (
	// BackgroundKindImage identifies static image Background Visuals (.jpg, .jpeg, .png, .webp).
	BackgroundKindImage BackgroundKind = "image"
	// BackgroundKindVideo identifies looping video/GIF Background Visuals (.mp4, .mov, .gif).
	BackgroundKindVideo BackgroundKind = "video"
)

// BackgroundExts lists the canonical supported Background Visual file extensions grouped by kind.
type BackgroundExts struct {
	Image []string `json:"image"`
	Video []string `json:"video"`
}

// SupportedBackgroundExts returns fresh slices of supported Background Visual extensions.
func SupportedBackgroundExts() BackgroundExts {
	return BackgroundExts{
		Image: []string{".jpg", ".jpeg", ".png", ".webp"},
		Video: []string{".mp4", ".mov", ".gif"},
	}
}

// CanonicalBackgroundExt validates ext case-insensitively and returns a canonical literal extension string.
func CanonicalBackgroundExt(ext string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg":
		return ".jpg", true
	case ".jpeg":
		return ".jpeg", true
	case ".png":
		return ".png", true
	case ".webp":
		return ".webp", true
	case ".mp4":
		return ".mp4", true
	case ".mov":
		return ".mov", true
	case ".gif":
		return ".gif", true
	default:
		return "", false
	}
}

// ClassifyBackground classifies bgPath as a static image or looping video/GIF Background Visual
// and returns its canonical extension.
func ClassifyBackground(bgPath string) (BackgroundKind, string, error) {
	rawExt := filepath.Ext(bgPath)
	canonical, ok := CanonicalBackgroundExt(rawExt)
	if !ok {
		return "", "", fmt.Errorf("unsupported Background Visual extension %q (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)", rawExt)
	}
	switch canonical {
	case ".mp4", ".mov", ".gif":
		return BackgroundKindVideo, canonical, nil
	default:
		return BackgroundKindImage, canonical, nil
	}
}

// SelectPath chooses the Rendering Path from the Background Visual path and overlayTitles flag.
// When bgPath is empty or whitespace-only, Path D (Auto Visualizer) is selected.
func SelectPath(bgPath string, overlayTitles bool) (Path, error) {
	if strings.TrimSpace(bgPath) == "" {
		return PathD, nil
	}
	kind, _, err := ClassifyBackground(bgPath)
	if err != nil {
		return "", err
	}
	if kind == BackgroundKindVideo {
		if overlayTitles {
			return PathB, nil
		}
		return PathA, nil
	}
	return PathC, nil
}

// EscapeFFmpegFilterPath normalizes slashes and escapes colons and apostrophes for FFmpeg's subtitles= filter.
func EscapeFFmpegFilterPath(path string) string {
	return filterPathEscaper.Replace(filepath.ToSlash(path))
}

// EncoderInfo describes the selected H.264 video encoder name and FFmpeg encoder flags.
type EncoderInfo struct {
	Name string
	Args []string
}

func probeEncoderArgs(ctx context.Context, runner func(ctx context.Context, name string, args ...string) ([]byte, error), extraArgs ...string) bool {
	if runner == nil || ctx.Err() != nil {
		return false
	}
	args := []string{
		"-v", "error",
		"-f", "lavfi",
		"-i", "color=c=black:s=256x256:d=0.1",
	}
	args = append(args, extraArgs...)
	args = append(args, "-f", "null", "-")
	_, err := runner(ctx, "ffmpeg", args...)
	return err == nil
}

// DetectEncoder probes hardware H.264 encoder usability via a 1-frame null encode
// and returns structured encoder metadata with libx264 software fallback.
func DetectEncoder(ctx context.Context, goos string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) EncoderInfo {
	if ctx == nil {
		ctx = context.Background()
	}

	switch goos {
	case "darwin":
		if probeEncoderArgs(ctx, runner, "-c:v", "h264_videotoolbox") {
			return EncoderInfo{
				Name: "h264_videotoolbox",
				Args: []string{"-c:v", "h264_videotoolbox", "-b:v", "6000k"},
			}
		}
	case "windows", "linux":
		if probeEncoderArgs(ctx, runner, "-c:v", "h264_nvenc") {
			return EncoderInfo{
				Name: "h264_nvenc",
				Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "20"},
			}
		}
	}
	return EncoderInfo{
		Name: "libx264",
		Args: []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"},
	}
}

// DetectEncoderArgs probes hardware H.264 encoder usability via a 1-frame null encode
// and falls back to libx264 software encoding when hardware acceleration is unavailable.
func DetectEncoderArgs(ctx context.Context, goos string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) []string {
	return DetectEncoder(ctx, goos, runner).Args
}

func videoToolboxFallbackBitrate(resolution string) string {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case Resolution4K:
		return "28000k"
	case Resolution1440p:
		return "18000k"
	default:
		return "12000k"
	}
}

// DetectPathDEncoder probes hardware H.264 encoder usability with high-quality Path D settings
// (Apple Silicon VideoToolbox constant quality -q:v 65 with resolution-scaled bitrate fallback for Intel Macs,
// NVENC -cq 18, and libx264 -preset fast -crf 18 -profile:v high fallback).
func DetectPathDEncoder(ctx context.Context, goos, resolution string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) EncoderInfo {
	if ctx == nil {
		ctx = context.Background()
	}

	switch goos {
	case "darwin":
		if probeEncoderArgs(ctx, runner, "-c:v", "h264_videotoolbox", "-q:v", "65", "-profile:v", "high") {
			return EncoderInfo{
				Name: "h264_videotoolbox",
				Args: []string{"-c:v", "h264_videotoolbox", "-q:v", "65", "-profile:v", "high"},
			}
		}
		if probeEncoderArgs(ctx, runner, "-c:v", "h264_videotoolbox") {
			return EncoderInfo{
				Name: "h264_videotoolbox",
				Args: []string{"-c:v", "h264_videotoolbox", "-b:v", videoToolboxFallbackBitrate(resolution), "-profile:v", "high"},
			}
		}
	case "windows", "linux":
		if probeEncoderArgs(ctx, runner, "-c:v", "h264_nvenc") {
			return EncoderInfo{
				Name: "h264_nvenc",
				Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "18"},
			}
		}
	}
	return EncoderInfo{
		Name: "libx264",
		Args: []string{"-c:v", "libx264", "-preset", "fast", "-crf", "18", "-profile:v", "high"},
	}
}

func buildVideoFilter(assPath string, overlayTitles bool, extraFilters ...string) string {
	parts := []string{scalePadFilter}
	parts = append(parts, extraFilters...)
	if overlayTitles {
		parts = append(parts, fmt.Sprintf("subtitles='%s'", EscapeFFmpegFilterPath(assPath)))
	}
	parts = append(parts, "format=yuv420p")
	return strings.Join(parts, ",")
}

// BuildPathANormalizeArgs constructs the FFmpeg arguments to pre-normalize a looping video/GIF into 1080p30 H.264.
func BuildPathANormalizeArgs(bgPath, normLoopPath string) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", bgPath,
		"-vf", buildVideoFilter("", false, "fps=30"),
		"-c:v", "libx264", "-preset", "fast", "-crf", "18",
		"-an",
		normLoopPath,
	}
}

// BuildPathAMuxArgs constructs the FFmpeg arguments to infinitely loop-mux a pre-normalized video with merged audio.
func BuildPathAMuxArgs(normLoopPath, mergedAudioPath, outPath string) []string {
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-stream_loop", "-1",
		"-i", normLoopPath,
		"-i", mergedAudioPath,
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "copy",
		"-shortest",
		outPath,
	}
}

// BuildPathBArgs constructs the FFmpeg arguments to render a looping video/GIF with burned-in .ass Title Overlays.
func BuildPathBArgs(bgPath, mergedAudioPath, assPath, outPath string, encoderArgs []string) []string {
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-stream_loop", "-1",
		"-i", bgPath,
		"-i", mergedAudioPath,
		"-vf", buildVideoFilter(assPath, true),
		"-map", "0:v:0",
		"-map", "1:a:0",
	}
	args = append(args, encoderArgs...)
	args = append(args, "-c:a", "copy", "-shortest", outPath)
	return args
}

// BuildPathCArgs constructs the FFmpeg arguments to render a static image Background Visual with -tune stillimage.
func BuildPathCArgs(bgPath, mergedAudioPath, assPath, outPath string, overlayTitles bool) []string {
	fps := "2"
	if overlayTitles {
		fps = "10"
	}
	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-loop", "1",
		"-framerate", fps,
		"-i", bgPath,
		"-i", mergedAudioPath,
		"-vf", buildVideoFilter(assPath, overlayTitles),
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "libx264", "-tune", "stillimage", "-preset", "veryfast", "-crf", "18",
		"-c:a", "copy",
		"-shortest",
		outPath,
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func probeMediaDuration(ctx context.Context, mediaPath string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) float64 {
	if runner == nil || ctx.Err() != nil {
		return 0
	}
	out, err := runner(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		mediaPath,
	)
	if err != nil {
		return 0
	}
	var parsed struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if err != nil || dur <= 0 {
		return 0
	}
	return dur
}

func runFFmpegRender(
	ctx context.Context,
	substage string,
	args []string,
	targetDuration float64,
	minFrac float64,
	maxFrac float64,
	runner func(ctx context.Context, name string, args ...string) ([]byte, error),
	streamRunner StreamRunner,
	onProgress ProgressFunc,
) error {
	if streamRunner == nil {
		if runner == nil {
			return errors.New("render command runner is nil")
		}
		_, err := runner(ctx, "ffmpeg", args...)
		return err
	}

	progressArgs := make([]string, 0, len(args)+2)
	progressArgs = append(progressArgs, "-progress", "pipe:1")
	progressArgs = append(progressArgs, args...)

	var sample ProgressSample
	span := maxFrac - minFrac
	return streamRunner(ctx, func(line string) {
		if !ParseProgressLine(line, &sample) || onProgress == nil {
			return
		}
		var subFrac float64
		switch {
		case sample.Done:
			subFrac = 1.0
		case targetDuration > 0:
			subFrac = clamp01(sample.OutTimeSec / targetDuration)
		default:
			subFrac = 0.33
		}
		onProgress(ProgressUpdate{
			Substage:   substage,
			Fraction:   minFrac + span*subFrac,
			OutTimeSec: sample.OutTimeSec,
			Speed:      sample.Speed,
			Done:       sample.Done,
		})
	}, "ffmpeg", progressArgs...)
}

// RenderOptions configures Step 5 FFmpeg Mix rendering across Paths A, B, C, and D.
type RenderOptions struct {
	BGPath          string
	MergedAudioPath string
	ASSPath         string
	TempDir         string
	OutPath         string
	OverlayTitles   bool
	TotalDuration   float64
	GOOS            string
	Visualizer      VisualizerConfig
	Encoder         *EncoderInfo
	NormWeight      float64
	Runner          func(ctx context.Context, name string, args ...string) ([]byte, error)
	StreamRunner    StreamRunner
	OnProgress      ProgressFunc
}

// Render selects the Rendering Path and executes FFmpeg to produce the final Mix video at outPath.
func Render(
	ctx context.Context,
	bgPath, mergedAudioPath, assPath, tempDir, outPath string,
	overlayTitles bool,
	totalDuration float64,
	goos string,
	runner func(ctx context.Context, name string, args ...string) ([]byte, error),
	streamRunner StreamRunner,
	onProgress ProgressFunc,
) (Path, error) {
	return RenderWithOptions(ctx, RenderOptions{
		BGPath:          bgPath,
		MergedAudioPath: mergedAudioPath,
		ASSPath:         assPath,
		TempDir:         tempDir,
		OutPath:         outPath,
		OverlayTitles:   overlayTitles,
		TotalDuration:   totalDuration,
		GOOS:            goos,
		Visualizer:      DefaultVisualizerConfig(),
		Runner:          runner,
		StreamRunner:    streamRunner,
		OnProgress:      onProgress,
	})
}

// RenderWithOptions selects the Rendering Path and executes FFmpeg using explicit Visualizer options for Path D.
func RenderWithOptions(ctx context.Context, opts RenderOptions) (Path, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	pathKind, err := SelectPath(opts.BGPath, opts.OverlayTitles)
	if err != nil {
		return "", err
	}
	if opts.OverlayTitles && strings.TrimSpace(opts.ASSPath) == "" {
		return "", fmt.Errorf("titles.ass path is required when --overlay-titles is enabled")
	}
	// #nosec G301 -- user-facing output directory uses standard 0755 permissions.
	if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0755); err != nil {
		return "", fmt.Errorf("create output directory for %q: %w", opts.OutPath, err)
	}

	switch pathKind {
	case PathA:
		var bgDuration float64
		if opts.StreamRunner != nil {
			bgDuration = probeMediaDuration(ctx, opts.BGPath, opts.Runner)
		}
		normWeight := PathANormWeight
		if opts.NormWeight > 0.05 && opts.NormWeight < 0.95 {
			normWeight = opts.NormWeight
		}
		normLoopPath := filepath.Join(opts.TempDir, "norm_loop.mp4")
		if err := runFFmpegRender(ctx, "norm_loop", BuildPathANormalizeArgs(opts.BGPath, normLoopPath), bgDuration, 0, normWeight, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
			return "", fmt.Errorf("normalize background loop (%s): %w", PathA, err)
		}
		if err := runFFmpegRender(ctx, "mux", BuildPathAMuxArgs(normLoopPath, opts.MergedAudioPath, opts.OutPath), opts.TotalDuration, normWeight, 1.0, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
			return "", fmt.Errorf("mux background loop (%s): %w", PathA, err)
		}
	case PathB:
		encoder := EncoderInfo{}
		if opts.Encoder != nil && len(opts.Encoder.Args) > 0 {
			encoder = *opts.Encoder
		} else {
			encoder = DetectEncoder(ctx, opts.GOOS, opts.Runner)
		}
		if err := runFFmpegRender(ctx, "render", BuildPathBArgs(opts.BGPath, opts.MergedAudioPath, opts.ASSPath, opts.OutPath, encoder.Args), opts.TotalDuration, 0, 1.0, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
			return "", fmt.Errorf("render overlay loop (%s): %w", PathB, err)
		}
	case PathC:
		if err := runFFmpegRender(ctx, "render", BuildPathCArgs(opts.BGPath, opts.MergedAudioPath, opts.ASSPath, opts.OutPath, opts.OverlayTitles), opts.TotalDuration, 0, 1.0, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
			return "", fmt.Errorf("render still image (%s): %w", PathC, err)
		}
	case PathD:
		vizCfg, err := ValidateVisualizerConfig(opts.Visualizer)
		if err != nil {
			return "", err
		}
		if vizCfg.Preset == "black" {
			args := BuildPathDBlackArgs(opts.MergedAudioPath, opts.ASSPath, opts.OutPath, opts.OverlayTitles, opts.TotalDuration, vizCfg)
			if err := runFFmpegRender(ctx, "render", args, opts.TotalDuration, 0, 1.0, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
				return "", fmt.Errorf("render blackout screen (%s): %w", PathD, err)
			}
		} else {
			encoder := EncoderInfo{}
			if opts.Encoder != nil && len(opts.Encoder.Args) > 0 {
				encoder = *opts.Encoder
			} else {
				encoder = DetectPathDEncoder(ctx, opts.GOOS, vizCfg.Resolution, opts.Runner)
			}
			args, err := BuildPathDArgs(opts.MergedAudioPath, opts.ASSPath, opts.OutPath, opts.OverlayTitles, opts.TotalDuration, vizCfg, encoder.Args)
			if err != nil {
				return "", err
			}
			if err := runFFmpegRender(ctx, "render", args, opts.TotalDuration, 0, 1.0, opts.Runner, opts.StreamRunner, opts.OnProgress); err != nil {
				return "", fmt.Errorf("render auto visualizer %q (%s): %w", vizCfg.Preset, PathD, err)
			}
		}
	}

	return pathKind, nil
}
