package render

import (
	"fmt"
	"os"
	"path/filepath"
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
)

const scalePadFilter = "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1"

var filterPathEscaper = strings.NewReplacer(
	`\`, `/`,
	`:`, `\:`,
	`'`, `\'`,
)

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
func DetectEncoderArgs(goos string, runner func(name string, args ...string) ([]byte, error)) []string {
	probeEncoder := func(encoder string) bool {
		if runner == nil {
			return false
		}
		_, err := runner(
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

// Render selects the Rendering Path and executes FFmpeg to produce the final Mix video at outPath.
func Render(bgPath, mergedAudioPath, assPath, tempDir, outPath string, overlayTitles bool, goos string, runner func(name string, args ...string) ([]byte, error)) (Path, error) {
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
		normLoopPath := filepath.Join(tempDir, "norm_loop.mp4")
		if _, err := runner("ffmpeg", BuildPathANormalizeArgs(bgPath, normLoopPath)...); err != nil {
			return "", fmt.Errorf("normalize background loop (%s): %w", PathA, err)
		}
		if _, err := runner("ffmpeg", BuildPathAMuxArgs(normLoopPath, mergedAudioPath, outPath)...); err != nil {
			return "", fmt.Errorf("mux background loop (%s): %w", PathA, err)
		}
	case PathB:
		encoderArgs := DetectEncoderArgs(goos, runner)
		if _, err := runner("ffmpeg", BuildPathBArgs(bgPath, mergedAudioPath, assPath, outPath, encoderArgs)...); err != nil {
			return "", fmt.Errorf("render overlay loop (%s): %w", PathB, err)
		}
	case PathC:
		if _, err := runner("ffmpeg", BuildPathCArgs(bgPath, mergedAudioPath, assPath, outPath, overlayTitles)...); err != nil {
			return "", fmt.Errorf("render still image (%s): %w", PathC, err)
		}
	}

	return pathKind, nil
}
