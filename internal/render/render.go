package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Path identifies one of the three FFmpeg video Rendering Paths.
type Path string

const (
	// PathA pre-normalizes a looping video/GIF once and muxes infinitely via -c:v copy.
	PathA Path = "Path A"
	// PathB burns Title Overlays into a looping video/GIF using hardware acceleration with libx264 fallback.
	PathB Path = "Path B"
	// PathC encodes a static image Background Visual with -loop 1 -tune stillimage.
	PathC Path = "Path C"

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

// SelectPath chooses the Rendering Path from the Background Visual file extension and overlayTitles flag.
func SelectPath(bgPath string, overlayTitles bool) (Path, error) {
	ext := strings.ToLower(filepath.Ext(bgPath))
	switch ext {
	case ".mp4", ".mov", ".gif":
		if overlayTitles {
			return PathB, nil
		}
		return PathA, nil
	case ".jpg", ".jpeg", ".png", ".webp":
		return PathC, nil
	default:
		return "", fmt.Errorf("unsupported Background Visual extension %q", filepath.Ext(bgPath))
	}
}

// EscapeFFmpegFilterPath normalizes slashes and escapes colons and apostrophes for FFmpeg's subtitles= filter.
func EscapeFFmpegFilterPath(path string) string {
	return filterPathEscaper.Replace(filepath.ToSlash(path))
}

// DetectEncoderArgs probes hardware H.264 encoder usability via a 1-frame null encode
// and falls back to libx264 software encoding when hardware acceleration is unavailable.
func DetectEncoderArgs(ctx context.Context, goos string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	probeEncoder := func(encoder string) bool {
		if runner == nil || ctx.Err() != nil {
			return false
		}
		_, err := runner(
			ctx,
			"ffmpeg",
			"-v", "error",
			"-f", "lavfi",
			"-i", "color=c=black:s=64x64:d=0.1",
			"-c:v", encoder,
			"-f", "null", "-",
		)
		return err == nil
	}

	switch goos {
	case "darwin":
		if probeEncoder("h264_videotoolbox") {
			return []string{"-c:v", "h264_videotoolbox", "-b:v", "6000k"}
		}
	case "windows", "linux":
		if probeEncoder("h264_nvenc") {
			return []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "20"}
		}
	}
	return []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}
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
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	pathKind, err := SelectPath(bgPath, overlayTitles)
	if err != nil {
		return "", err
	}
	if overlayTitles && strings.TrimSpace(assPath) == "" {
		return "", fmt.Errorf("titles.ass path is required when --overlay-titles is enabled")
	}
	// #nosec G301 -- user-facing output directory uses standard 0755 permissions.
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return "", fmt.Errorf("create output directory for %q: %w", outPath, err)
	}

	switch pathKind {
	case PathA:
		var bgDuration float64
		if streamRunner != nil {
			bgDuration = probeMediaDuration(ctx, bgPath, runner)
		}
		normLoopPath := filepath.Join(tempDir, "norm_loop.mp4")
		if err := runFFmpegRender(ctx, "norm_loop", BuildPathANormalizeArgs(bgPath, normLoopPath), bgDuration, 0, PathANormWeight, runner, streamRunner, onProgress); err != nil {
			return "", fmt.Errorf("normalize background loop (%s): %w", PathA, err)
		}
		if err := runFFmpegRender(ctx, "mux", BuildPathAMuxArgs(normLoopPath, mergedAudioPath, outPath), totalDuration, PathANormWeight, 1.0, runner, streamRunner, onProgress); err != nil {
			return "", fmt.Errorf("mux background loop (%s): %w", PathA, err)
		}
	case PathB:
		encoderArgs := DetectEncoderArgs(ctx, goos, runner)
		if err := runFFmpegRender(ctx, "render", BuildPathBArgs(bgPath, mergedAudioPath, assPath, outPath, encoderArgs), totalDuration, 0, 1.0, runner, streamRunner, onProgress); err != nil {
			return "", fmt.Errorf("render overlay loop (%s): %w", PathB, err)
		}
	case PathC:
		if err := runFFmpegRender(ctx, "render", BuildPathCArgs(bgPath, mergedAudioPath, assPath, outPath, overlayTitles), totalDuration, 0, 1.0, runner, streamRunner, onProgress); err != nil {
			return "", fmt.Errorf("render still image (%s): %w", PathC, err)
		}
	}

	return pathKind, nil
}
