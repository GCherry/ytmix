package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func escapeConcatPath(path string) string {
	slashed := filepath.ToSlash(path)
	return strings.ReplaceAll(slashed, "'", `'\''`)
}

func buildConcatList(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		b.WriteString("file '")
		b.WriteString(escapeConcatPath(p))
		b.WriteString("'\n")
	}
	return b.String()
}

func buildAcrossfadeFilter(trackCount int, crossfade float64) string {
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

func validateCrossfade(tracks []Track, crossfade float64) error {
	if math.IsNaN(crossfade) || math.IsInf(crossfade, 0) || crossfade < 0 {
		return fmt.Errorf("--crossfade must be >= 0 (got %v)", crossfade)
	}
	if crossfade == 0 || len(tracks) <= 1 {
		return nil
	}
	for _, tr := range tracks {
		if tr.Duration <= crossfade {
			return fmt.Errorf("Track %q duration (%.2fs) must exceed --crossfade (%.2fs)", tr.FileName, tr.Duration, crossfade)
		}
	}
	return nil
}

func normalizeTracks(tracks []Track, tempDir string, runner CommandRunner) ([]Track, error) {
	updated := make([]Track, len(tracks))
	for i, tr := range tracks {
		wavPath := filepath.Join(tempDir, fmt.Sprintf("norm_%04d.wav", i))
		if _, err := runner(
			"ffmpeg",
			"-y",
			"-hide_banner",
			"-loglevel", "error",
			"-i", tr.Path,
			"-c:a", "pcm_s16le",
			"-ar", "48000",
			"-af", "loudnorm=I=-14:TP=-1.5:LRA=11",
			wavPath,
		); err != nil {
			return nil, fmt.Errorf("normalize Track %q: %w", tr.FileName, err)
		}

		newDur, _, err := probeFile(wavPath, runner)
		if err != nil {
			return nil, fmt.Errorf("re-probe normalized Track %q: %w", tr.FileName, err)
		}

		updated[i] = Track{
			Path:         wavPath,
			FileName:     tr.FileName,
			Duration:     newDur,
			DisplayTitle: tr.DisplayTitle,
		}
	}
	return updated, nil
}

func processAndMergeAudio(tracks []Track, tempDir string, normalize bool, crossfade float64, runner CommandRunner) (string, []Track, error) {
	if len(tracks) == 0 {
		return "", nil, fmt.Errorf("cannot merge 0 Tracks")
	}

	working := tracks
	if normalize {
		var err error
		working, err = normalizeTracks(tracks, tempDir, runner)
		if err != nil {
			return "", nil, err
		}
	}

	if err := validateCrossfade(working, crossfade); err != nil {
		return "", nil, err
	}

	mergedPath := filepath.Join(tempDir, "merged_audio.m4a")
	if crossfade == 0 || len(working) == 1 {
		paths := make([]string, len(working))
		for i, tr := range working {
			absPath, err := filepath.Abs(tr.Path)
			if err != nil {
				absPath = tr.Path
			}
			paths[i] = absPath
		}
		concatPath := filepath.Join(tempDir, "concat_list.txt")
		if err := os.WriteFile(concatPath, []byte(buildConcatList(paths)), 0644); err != nil {
			return "", nil, fmt.Errorf("write concat_list.txt: %w", err)
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
			return "", nil, fmt.Errorf("concat merge audio: %w", err)
		}
		return mergedPath, working, nil
	}

	args := []string{"-y", "-hide_banner", "-loglevel", "error"}
	for _, tr := range working {
		args = append(args, "-i", tr.Path)
	}
	args = append(
		args,
		"-filter_complex", buildAcrossfadeFilter(len(working), crossfade),
		"-map", "[aout]",
		"-c:a", "aac",
		"-b:a", "320k",
		"-ar", "48000",
		mergedPath,
	)
	if _, err := runner("ffmpeg", args...); err != nil {
		return "", nil, fmt.Errorf("acrossfade merge audio: %w", err)
	}
	return mergedPath, working, nil
}
