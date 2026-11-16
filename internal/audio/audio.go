package audio

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GCherry/ytmix/internal/probe"
)

var concatPathEscaper = strings.NewReplacer(
	`\`, `/`,
	`'`, `'\''`,
)

func escapeConcatPath(path string) string {
	return concatPathEscaper.Replace(path)
}

// BuildConcatList formats an FFmpeg concat demuxer file list for paths.
func BuildConcatList(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("file '")
		b.WriteString(escapeConcatPath(p))
		b.WriteString("'\n")
	}
	return b.String()
}

// BuildAcrossfadeFilter builds the sequential triangular acrossfade filter_complex graph.
func BuildAcrossfadeFilter(trackCount int, crossfade float64) string {
	if trackCount < 2 {
		return ""
	}
	dur := strconv.FormatFloat(crossfade, 'f', -1, 64)
	segments := make([]string, 0, trackCount-1)
	for i := 1; i < trackCount; i++ {
		left := "[0:a]"
		if i > 1 {
			left = fmt.Sprintf("[a%d]", i-1)
		}
		right := fmt.Sprintf("[%d:a]", i)
		out := "[aout]"
		if i < trackCount-1 {
			out = fmt.Sprintf("[a%d]", i)
		}
		segments = append(segments, fmt.Sprintf("%s%sacrossfade=d=%s:c1=tri:c2=tri%s", left, right, dur, out))
	}
	return strings.Join(segments, ";")
}

// ValidateCrossfade verifies crossfade is non-negative and strictly less than every Track duration when merging multiple Tracks.
func ValidateCrossfade(fileNames []string, durations []float64, crossfade float64) error {
	if math.IsNaN(crossfade) || math.IsInf(crossfade, 0) || crossfade < 0 {
		return fmt.Errorf("--crossfade must be >= 0 (got %v)", crossfade)
	}
	if crossfade == 0 || len(durations) <= 1 {
		return nil
	}
	if len(fileNames) != len(durations) {
		return fmt.Errorf("mismatched Track fileNames (%d) and durations (%d)", len(fileNames), len(durations))
	}
	for i, dur := range durations {
		if dur <= crossfade {
			return fmt.Errorf("track %q duration (%.2fs) must exceed --crossfade (%.2fs)", fileNames[i], dur, crossfade)
		}
	}
	return nil
}

// NormalizeTracks transcodes each Track to a temporary -14 LUFS .wav file and re-probes its duration.
func NormalizeTracks(paths []string, fileNames []string, tempDir string, runner func(name string, args ...string) ([]byte, error)) ([]string, []float64, error) {
	if len(paths) != len(fileNames) {
		return nil, nil, fmt.Errorf("mismatched Track paths (%d) and fileNames (%d)", len(paths), len(fileNames))
	}

	normPaths := make([]string, len(paths))
	normDurations := make([]float64, len(paths))
	for i, p := range paths {
		wavPath := filepath.Join(tempDir, fmt.Sprintf("norm_%04d.wav", i))
		if _, err := runner(
			"ffmpeg",
			"-y",
			"-hide_banner",
			"-loglevel", "error",
			"-i", p,
			"-c:a", "pcm_s16le",
			"-ar", "48000",
			"-af", "loudnorm=I=-14:TP=-1.5:LRA=11",
			wavPath,
		); err != nil {
			return nil, nil, fmt.Errorf("normalize Track %q: %w", fileNames[i], err)
		}

		newDur, _, err := probe.ProbeFile(wavPath, runner)
		if err != nil {
			return nil, nil, fmt.Errorf("re-probe normalized Track %q: %w", fileNames[i], err)
		}

		normPaths[i] = wavPath
		normDurations[i] = newDur
	}
	return normPaths, normDurations, nil
}

// ProcessAndMerge optionally normalizes Tracks and merges them into tempDir/merged_audio.m4a.
func ProcessAndMerge(paths []string, fileNames []string, durations []float64, tempDir string, normalize bool, crossfade float64, runner func(name string, args ...string) ([]byte, error)) (string, []string, []float64, error) {
	if len(paths) == 0 {
		return "", nil, nil, fmt.Errorf("cannot merge 0 Tracks")
	}
	if len(paths) != len(fileNames) || len(paths) != len(durations) {
		return "", nil, nil, fmt.Errorf("mismatched Track slices: paths=%d fileNames=%d durations=%d", len(paths), len(fileNames), len(durations))
	}

	workingPaths := paths
	workingDurations := durations
	if normalize {
		var err error
		workingPaths, workingDurations, err = NormalizeTracks(paths, fileNames, tempDir, runner)
		if err != nil {
			return "", nil, nil, err
		}
	}

	if err := ValidateCrossfade(fileNames, workingDurations, crossfade); err != nil {
		return "", nil, nil, err
	}

	mergedPath := filepath.Join(tempDir, "merged_audio.m4a")
	if crossfade == 0 || len(workingPaths) == 1 {
		concatPaths := make([]string, len(workingPaths))
		for i, p := range workingPaths {
			absPath, err := filepath.Abs(p)
			if err != nil {
				absPath = p
			}
			concatPaths[i] = absPath
		}
		concatPath := filepath.Join(tempDir, "concat_list.txt")
		if err := os.WriteFile(concatPath, []byte(BuildConcatList(concatPaths)), 0600); err != nil {
			return "", nil, nil, fmt.Errorf("write concat_list.txt: %w", err)
		}
		if _, err := runner(
			"ffmpeg",
			"-y",
			"-hide_banner",
			"-loglevel", "error",
			"-f", "concat",
			"-safe", "0",
			"-i", concatPath,
			"-c:a", "aac",
			"-b:a", "320k",
			"-ar", "48000",
			mergedPath,
		); err != nil {
			return "", nil, nil, fmt.Errorf("concat merge audio: %w", err)
		}
		return mergedPath, workingPaths, workingDurations, nil
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, p := range workingPaths {
		args = append(args, "-i", p)
	}
	args = append(
		args,
		"-filter_complex", BuildAcrossfadeFilter(len(workingPaths), crossfade),
		"-map", "[aout]",
		"-c:a", "aac",
		"-b:a", "320k",
		"-ar", "48000",
		mergedPath,
	)
	if _, err := runner("ffmpeg", args...); err != nil {
		return "", nil, nil, fmt.Errorf("acrossfade merge audio: %w", err)
	}
	return mergedPath, workingPaths, workingDurations, nil
}
