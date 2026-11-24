package audio

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

	"golang.org/x/sync/errgroup"

	"github.com/GCherry/ytmix/internal/probe"
)

var concatPathEscaper = strings.NewReplacer(
	`\`, `/`,
	`'`, `'\''`,
)

func escapeConcatPath(path string) string {
	return concatPathEscaper.Replace(path)
}

func normalizeConcurrencyLimit() int {
	return max(1, min(runtime.GOMAXPROCS(0), 4))
}

func trackLabel(tr probe.Track) string {
	if strings.TrimSpace(tr.FileName) != "" {
		return tr.FileName
	}
	return filepath.Base(tr.Path)
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
func ValidateCrossfade(tracks []probe.Track, crossfade float64) error {
	if math.IsNaN(crossfade) || math.IsInf(crossfade, 0) || crossfade < 0 {
		return fmt.Errorf("--crossfade must be >= 0 (got %v)", crossfade)
	}
	if crossfade == 0 || len(tracks) <= 1 {
		return nil
	}
	for _, tr := range tracks {
		if tr.Duration <= crossfade {
			return fmt.Errorf("track %q duration (%.2fs) must exceed --crossfade (%.2fs)", trackLabel(tr), tr.Duration, crossfade)
		}
	}
	return nil
}

// NormalizeTracks transcodes each Track to a temporary -14 LUFS .wav file and re-probes its duration.
func NormalizeTracks(ctx context.Context, tracks []probe.Track, tempDir string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]probe.Track, error) {
	return NormalizeTracksWithProgress(ctx, tracks, tempDir, runner, nil)
}

// NormalizeTracksWithProgress transcodes each Track concurrently to a temporary -14 LUFS .wav file,
// re-probes its duration, preserves input Track order, and reports monotonic completed Track counts via onProgress.
func NormalizeTracksWithProgress(ctx context.Context, tracks []probe.Track, tempDir string, runner func(ctx context.Context, name string, args ...string) ([]byte, error), onProgress func(completed, total int)) ([]probe.Track, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return []probe.Track{}, nil
	}
	if runner == nil {
		return nil, errors.New("ffmpeg runner is nil")
	}

	normalized := make([]probe.Track, len(tracks))
	var progressMu sync.Mutex
	completed := 0

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(normalizeConcurrencyLimit())
	for i, tr := range tracks {
		g.Go(func() error {
			wavPath := filepath.Join(tempDir, fmt.Sprintf("norm_%04d.wav", i))
			if _, err := runner(
				gctx,
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
				return fmt.Errorf("normalize Track %q: %w", trackLabel(tr), err)
			}

			newDur, _, err := probe.ProbeFile(gctx, wavPath, runner)
			if err != nil {
				return fmt.Errorf("re-probe normalized Track %q: %w", trackLabel(tr), err)
			}

			updated := tr
			updated.Path = wavPath
			updated.Duration = newDur
			normalized[i] = updated

			if onProgress != nil {
				progressMu.Lock()
				completed++
				onProgress(completed, len(tracks))
				progressMu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return normalized, nil
}

// ProcessAndMerge optionally normalizes Tracks and merges them into tempDir/merged_audio.m4a.
func ProcessAndMerge(ctx context.Context, tracks []probe.Track, tempDir string, normalize bool, crossfade float64, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) (string, []probe.Track, error) {
	return ProcessAndMergeWithProgress(ctx, tracks, tempDir, normalize, crossfade, runner, nil)
}

// ProcessAndMergeWithProgress optionally normalizes Tracks (reporting progress per Track) and merges them into tempDir/merged_audio.m4a.
func ProcessAndMergeWithProgress(ctx context.Context, tracks []probe.Track, tempDir string, normalize bool, crossfade float64, runner func(ctx context.Context, name string, args ...string) ([]byte, error), onNormalizeProgress func(completed, total int)) (string, []probe.Track, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if len(tracks) == 0 {
		return "", nil, fmt.Errorf("cannot merge 0 Tracks")
	}
	if runner == nil {
		return "", nil, errors.New("ffmpeg runner is nil")
	}

	working := make([]probe.Track, len(tracks))
	copy(working, tracks)
	if normalize {
		var err error
		working, err = NormalizeTracksWithProgress(ctx, working, tempDir, runner, onNormalizeProgress)
		if err != nil {
			return "", nil, err
		}
	}

	if err := ValidateCrossfade(working, crossfade); err != nil {
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}

	mergedPath := filepath.Join(tempDir, "merged_audio.m4a")
	if crossfade == 0 || len(working) == 1 {
		concatPaths := make([]string, len(working))
		for i, tr := range working {
			absPath, err := filepath.Abs(tr.Path)
			if err != nil {
				absPath = tr.Path
			}
			concatPaths[i] = absPath
		}
		concatPath := filepath.Join(tempDir, "concat_list.txt")
		if err := os.WriteFile(concatPath, []byte(BuildConcatList(concatPaths)), 0600); err != nil {
			return "", nil, fmt.Errorf("write concat_list.txt: %w", err)
		}
		if _, err := runner(
			ctx,
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
		"-filter_complex", BuildAcrossfadeFilter(len(working), crossfade),
		"-map", "[aout]",
		"-c:a", "aac",
		"-b:a", "320k",
		"-ar", "48000",
		mergedPath,
	)
	if _, err := runner(ctx, "ffmpeg", args...); err != nil {
		return "", nil, fmt.Errorf("acrossfade merge audio: %w", err)
	}
	return mergedPath, working, nil
}
