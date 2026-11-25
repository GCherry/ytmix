package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Track represents a single discovered .mp3 audio file, its probed duration in seconds,
// its optional trimmed ID3 title tag, and its resolved Display Title.
type Track struct {
	Path         string
	FileName     string
	Duration     float64
	RawID3Title  string
	DisplayTitle string
}

// NamedTrackFile pairs a local .mp3 file path with its original display filename.
type NamedTrackFile struct {
	Path     string
	FileName string
}

type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

var leadingNumberPattern = regexp.MustCompile(`^\d+[\s._-]+`)

func probeConcurrencyLimit() int {
	return min(max(runtime.GOMAXPROCS(0), 4), 8)
}

// ValidateTrackFile verifies path references an accessible regular .mp3 file.
func ValidateTrackFile(path string) error {
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

// DiscoverAudioFiles scans audioDir for .mp3 files (case-insensitively), sorts them
// alphabetically by filename, and optionally shuffles them.
func DiscoverAudioFiles(audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int))) ([]string, error) {
	entries, err := os.ReadDir(audioDir)
	if err != nil {
		return nil, fmt.Errorf("read audio directory %q: %w", audioDir, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".mp3") {
			files = append(files, filepath.Join(audioDir, entry.Name()))
		}
	}

	if len(files) < 1 {
		return nil, fmt.Errorf("no .mp3 Tracks found in %q", audioDir)
	}

	sort.Slice(files, func(i, j int) bool {
		return filepath.Base(files[i]) < filepath.Base(files[j])
	})

	if shuffle {
		if shuffleFn == nil {
			// #nosec G404 -- non-cryptographic randomness used solely for --shuffle Track playback ordering.
			shuffleFn = rand.Shuffle
		}
		shuffleFn(len(files), func(i, j int) {
			files[i], files[j] = files[j], files[i]
		})
	}

	return files, nil
}

// ResolveDisplayTitle returns the trimmed ID3 title if non-empty, otherwise cleans the filename.
func ResolveDisplayTitle(id3Title, filename string) string {
	if trimmed := strings.TrimSpace(id3Title); trimmed != "" {
		return trimmed
	}

	base := filepath.Base(filename)
	if ext := filepath.Ext(base); strings.EqualFold(ext, ".mp3") {
		base = base[:len(base)-len(ext)]
	}
	cleaned := strings.TrimSpace(leadingNumberPattern.ReplaceAllString(base, ""))
	if cleaned == "" {
		return strings.TrimSpace(base)
	}
	return cleaned
}

// ProbeFile runs ffprobe on path and extracts its duration in seconds and ID3 title tag.
func ProbeFile(ctx context.Context, path string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) (duration float64, id3Title string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	if runner == nil {
		return 0, "", errors.New("ffprobe runner is nil")
	}
	out, err := runner(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-show_entries", "format_tags=title",
		"-of", "json",
		path,
	)
	if err != nil {
		return 0, "", fmt.Errorf("ffprobe %q: %w", path, err)
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0, "", fmt.Errorf("parse ffprobe JSON for %q: %w", path, err)
	}

	dur, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if err != nil || math.IsNaN(dur) || math.IsInf(dur, 0) || dur <= 0 {
		return 0, "", fmt.Errorf("invalid duration %q for %q", parsed.Format.Duration, path)
	}

	for k, v := range parsed.Format.Tags {
		if strings.EqualFold(k, "title") {
			id3Title = v
			break
		}
	}

	return dur, id3Title, nil
}

// ProbeTracks discovers .mp3 files in audioDir and probes each Track's duration and Display Title concurrently.
func ProbeTracks(ctx context.Context, audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	files, err := DiscoverAudioFiles(audioDir, shuffle, shuffleFn)
	if err != nil {
		return nil, err
	}
	return ProbeTrackFiles(ctx, files, runner)
}

// ProbeTrackFiles probes an ordered list of .mp3 file paths without re-scanning or re-sorting a directory.
func ProbeTrackFiles(ctx context.Context, files []string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	named := make([]NamedTrackFile, len(files))
	for i, p := range files {
		named[i] = NamedTrackFile{Path: p, FileName: filepath.Base(p)}
	}
	return ProbeNamedTrackFiles(ctx, named, runner)
}

// ProbeNamedTrackFiles validates and probes an ordered list of .mp3 files concurrently using a bounded errgroup,
// preserving input Track order and resolving Display Titles from FileName.
func ProbeNamedTrackFiles(ctx context.Context, files []NamedTrackFile, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) < 1 {
		return nil, errors.New("at least one .mp3 Track is required")
	}
	if runner == nil {
		return nil, errors.New("ffprobe runner is nil")
	}
	for _, f := range files {
		if err := ValidateTrackFile(f.Path); err != nil {
			return nil, err
		}
	}

	tracks := make([]Track, len(files))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(probeConcurrencyLimit())
	for i, f := range files {
		g.Go(func() error {
			dur, id3Title, err := ProbeFile(gctx, f.Path, runner)
			if err != nil {
				return err
			}
			displaySource := f.FileName
			if strings.TrimSpace(displaySource) == "" {
				displaySource = filepath.Base(f.Path)
			}
			tracks[i] = Track{
				Path:         f.Path,
				FileName:     displaySource,
				Duration:     dur,
				RawID3Title:  strings.TrimSpace(id3Title),
				DisplayTitle: ResolveDisplayTitle(id3Title, displaySource),
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return tracks, nil
}

// BackgroundMeta holds video stream and container metadata probed from a Background Visual.
type BackgroundMeta struct {
	DurationSec float64 `json:"durationSec"`
	FPS         float64 `json:"fps"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	CodecName   string  `json:"codecName"`
	PixFmt      string  `json:"pixFmt"`
	NBFrames    int64   `json:"nbFrames"`
	HasAudio    bool    `json:"hasAudio"`
}

type ffprobeBGOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
		NBFrames     string `json:"nb_frames"`
		PixFmt       string `json:"pix_fmt"`
	} `json:"streams"`
}

// ParseFrameRate parses an FFprobe fractional or decimal frame rate (e.g. "30000/1001", "30/1", "25")
// and returns frames per second when valid in (0, 240], or 0 otherwise.
func ParseFrameRate(raw string) float64 {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "N/A") {
		return 0
	}
	var fps float64
	if numStr, denStr, ok := strings.Cut(s, "/"); ok {
		num, errNum := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
		den, errDen := strconv.ParseFloat(strings.TrimSpace(denStr), 64)
		if errNum != nil || errDen != nil || den <= 0 {
			return 0
		}
		fps = num / den
	} else {
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0
		}
		fps = val
	}
	if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > 240 {
		return 0
	}
	return fps
}

// ProbeBackgroundVisual runs ffprobe on a Background Visual file to extract dimensions, frame rate,
// codec, pixel format, frame count, audio stream presence, and duration (inferring GIF duration from nb_frames/FPS when N/A).
func ProbeBackgroundVisual(ctx context.Context, path string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) (BackgroundMeta, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return BackgroundMeta{}, err
	}
	if runner == nil {
		return BackgroundMeta{}, fmt.Errorf("ffprobe runner is nil")
	}

	out, err := runner(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration:stream=codec_type,codec_name,width,height,r_frame_rate,avg_frame_rate,nb_frames,pix_fmt",
		"-of", "json",
		path,
	)
	if err != nil {
		return BackgroundMeta{}, fmt.Errorf("ffprobe Background Visual %q: %w", path, err)
	}

	var parsed ffprobeBGOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return BackgroundMeta{}, fmt.Errorf("parse ffprobe Background Visual JSON for %q: %w", path, err)
	}

	var meta BackgroundMeta
	if dur, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64); err == nil && !math.IsNaN(dur) && !math.IsInf(dur, 0) && dur > 0 {
		meta.DurationSec = dur
	}

	videoIdx := -1
	fallbackIdx := -1
	for i, st := range parsed.Streams {
		codecType := strings.ToLower(strings.TrimSpace(st.CodecType))
		if codecType == "audio" {
			meta.HasAudio = true
		}
		if videoIdx < 0 && codecType == "video" {
			videoIdx = i
		}
		if fallbackIdx < 0 && codecType == "" {
			fallbackIdx = i
		}
	}
	if videoIdx < 0 {
		videoIdx = fallbackIdx
	}

	if videoIdx >= 0 {
		st := parsed.Streams[videoIdx]
		meta.CodecName = strings.TrimSpace(st.CodecName)
		meta.Width = st.Width
		meta.Height = st.Height
		meta.PixFmt = strings.TrimSpace(st.PixFmt)
		if nb, err := strconv.ParseInt(strings.TrimSpace(st.NBFrames), 10, 64); err == nil && nb > 0 {
			meta.NBFrames = nb
		}
		if fps := ParseFrameRate(st.AvgFrameRate); fps > 0 {
			meta.FPS = fps
		} else if fps := ParseFrameRate(st.RFrameRate); fps > 0 {
			meta.FPS = fps
		}
	}

	if meta.DurationSec <= 0 && meta.NBFrames > 0 && meta.FPS > 0 {
		meta.DurationSec = float64(meta.NBFrames) / meta.FPS
	}
	if meta.NBFrames == 0 && meta.DurationSec > 0 && meta.FPS > 0 {
		meta.NBFrames = int64(math.Round(meta.DurationSec * meta.FPS))
	}

	return meta, nil
}
