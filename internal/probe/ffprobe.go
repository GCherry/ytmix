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
// its optional trimmed ID3 title tag, its resolved Display Title, and optional embedded cover art metadata.
type Track struct {
	Path              string
	FileName          string
	Duration          float64
	RawID3Title       string
	DisplayTitle      string
	HasEmbeddedArt    bool
	EmbeddedArtWidth  int
	EmbeddedArtHeight int
	EmbeddedArtCodec  string
}

// EmbeddedArt describes the primary attached picture stream found inside an audio file.
type EmbeddedArt struct {
	HasArt bool
	Width  int
	Height int
	Codec  string
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

type ffprobeStreamsOutput struct {
	Streams []struct {
		Index       int    `json:"index"`
		CodecType   string `json:"codec_type"`
		CodecName   string `json:"codec_name"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
}

type sidecarCandidate struct {
	ext  string
	kind string
}

// sidecarPriorities defines deterministic extension precedence: video/animation formats first,
// then lossless/lossy still image formats.
var sidecarPriorities = []sidecarCandidate{
	{ext: ".mp4", kind: "video"},
	{ext: ".mov", kind: "video"},
	{ext: ".gif", kind: "video"},
	{ext: ".png", kind: "image"},
	{ext: ".jpg", kind: "image"},
	{ext: ".jpeg", kind: "image"},
	{ext: ".webp", kind: "image"},
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
	return parseProbeFormat(out, path)
}

// ProbeFileWithArt runs a single ffprobe command on path to extract duration, ID3 title,
// and attached picture stream metadata. Missing or empty streams arrays return zero EmbeddedArt.
func ProbeFileWithArt(ctx context.Context, path string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) (duration float64, id3Title string, art EmbeddedArt, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, "", EmbeddedArt{}, err
	}
	if runner == nil {
		return 0, "", EmbeddedArt{}, errors.New("ffprobe runner is nil")
	}
	out, err := runner(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration:format_tags=title:stream=index,codec_type,codec_name,width,height:stream_disposition=attached_pic",
		"-of", "json",
		path,
	)
	if err != nil {
		return 0, "", EmbeddedArt{}, fmt.Errorf("ffprobe %q: %w", path, err)
	}
	dur, title, err := parseProbeFormat(out, path)
	if err != nil {
		return 0, "", EmbeddedArt{}, err
	}
	art, err = parseEmbeddedArt(out, path)
	if err != nil {
		return 0, "", EmbeddedArt{}, err
	}
	return dur, title, art, nil
}

func parseProbeFormat(out []byte, path string) (duration float64, id3Title string, err error) {
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

func parseEmbeddedArt(out []byte, path string) (EmbeddedArt, error) {
	var parsed ffprobeStreamsOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return EmbeddedArt{}, fmt.Errorf("parse ffprobe streams JSON for %q: %w", path, err)
	}

	for _, st := range parsed.Streams {
		if strings.EqualFold(strings.TrimSpace(st.CodecType), "video") && st.Disposition.AttachedPic == 1 {
			return EmbeddedArt{
				HasArt: true,
				Width:  max(st.Width, 0),
				Height: max(st.Height, 0),
				Codec:  strings.TrimSpace(st.CodecName),
			}, nil
		}
	}
	return EmbeddedArt{}, nil
}

// ProbeEmbeddedArt inspects streams in path and returns metadata for the first video stream
// marked with disposition.attached_pic == 1. Missing or empty streams arrays return zero EmbeddedArt.
func ProbeEmbeddedArt(ctx context.Context, path string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) (EmbeddedArt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return EmbeddedArt{}, err
	}
	if runner == nil {
		return EmbeddedArt{}, errors.New("ffprobe runner is nil")
	}

	out, err := runner(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "stream=index,codec_type,codec_name,width,height:stream_disposition=attached_pic",
		"-of", "json",
		path,
	)
	if err != nil {
		return EmbeddedArt{}, fmt.Errorf("ffprobe streams %q: %w", path, err)
	}
	return parseEmbeddedArt(out, path)
}

// DiscoverSidecarMedia searches the directory of trackPath for a matching <track_stem>.<ext>
// visual file (case-insensitive extension) using deterministic priority order:
// .mp4, .mov, .gif ("video"), then .png, .jpg, .jpeg, .webp ("image").
func DiscoverSidecarMedia(trackPath string) (mediaPath string, kind string, ok bool) {
	if strings.TrimSpace(trackPath) == "" {
		return "", "", false
	}
	dir := filepath.Dir(trackPath)
	base := filepath.Base(trackPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		return "", "", false
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}

	for _, candidate := range sidecarPriorities {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			ext := filepath.Ext(name)
			if strings.TrimSuffix(name, ext) != stem || !strings.EqualFold(ext, candidate.ext) {
				continue
			}
			fullPath := filepath.Join(dir, name)
			info, statErr := os.Stat(fullPath)
			if statErr != nil || info.IsDir() {
				continue
			}
			return fullPath, candidate.kind, true
		}
	}
	return "", "", false
}

// ExtractEmbeddedArt extracts the first attached picture stream from trackPath to outPath via ffmpeg.
func ExtractEmbeddedArt(ctx context.Context, trackPath, outPath string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if runner == nil {
		return errors.New("ffmpeg runner is nil")
	}
	if strings.TrimSpace(trackPath) == "" {
		return errors.New("track file path is empty")
	}
	if strings.TrimSpace(outPath) == "" {
		return errors.New("output art path is empty")
	}

	if _, err := runner(
		ctx,
		"ffmpeg",
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", trackPath,
		"-map", "0:v:0",
		"-frames:v", "1",
		outPath,
	); err != nil {
		return fmt.Errorf("extract embedded art from %q to %q: %w", trackPath, outPath, err)
	}
	return nil
}

// ProbeTracks discovers .mp3 files in audioDir and probes each Track's duration and Display Title concurrently.
func ProbeTracks(ctx context.Context, audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	files, err := DiscoverAudioFiles(audioDir, shuffle, shuffleFn)
	if err != nil {
		return nil, err
	}
	return ProbeTrackFiles(ctx, files, runner)
}

// ProbeTracksWithArt discovers .mp3 files in audioDir and probes each Track's duration, Display Title,
// and embedded cover art metadata concurrently in a single ffprobe invocation per Track.
func ProbeTracksWithArt(ctx context.Context, audioDir string, shuffle bool, shuffleFn func(n int, swap func(i, j int)), runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	files, err := DiscoverAudioFiles(audioDir, shuffle, shuffleFn)
	if err != nil {
		return nil, err
	}
	return ProbeTrackFilesWithArt(ctx, files, runner)
}

// ProbeTrackFiles probes an ordered list of .mp3 file paths without re-scanning or re-sorting a directory.
func ProbeTrackFiles(ctx context.Context, files []string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	return probeNamedTrackFiles(ctx, toNamedTrackFiles(files), false, runner)
}

// ProbeTrackFilesWithArt probes an ordered list of .mp3 file paths including embedded cover art metadata.
func ProbeTrackFilesWithArt(ctx context.Context, files []string, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	return probeNamedTrackFiles(ctx, toNamedTrackFiles(files), true, runner)
}

func toNamedTrackFiles(files []string) []NamedTrackFile {
	named := make([]NamedTrackFile, len(files))
	for i, p := range files {
		named[i] = NamedTrackFile{Path: p, FileName: filepath.Base(p)}
	}
	return named
}

// ProbeNamedTrackFiles validates and probes an ordered list of .mp3 files concurrently using a bounded errgroup,
// preserving input Track order and resolving Display Titles from FileName.
func ProbeNamedTrackFiles(ctx context.Context, files []NamedTrackFile, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	return probeNamedTrackFiles(ctx, files, false, runner)
}

// ProbeNamedTrackFilesWithArt validates and probes an ordered list of .mp3 files concurrently,
// including embedded cover art metadata, while preserving input Track order.
func ProbeNamedTrackFilesWithArt(ctx context.Context, files []NamedTrackFile, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
	return probeNamedTrackFiles(ctx, files, true, runner)
}

func probeNamedTrackFiles(ctx context.Context, files []NamedTrackFile, includeArt bool, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) ([]Track, error) {
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
			var (
				dur      float64
				id3Title string
				art      EmbeddedArt
				err      error
			)
			if includeArt {
				dur, id3Title, art, err = ProbeFileWithArt(gctx, f.Path, runner)
			} else {
				dur, id3Title, err = ProbeFile(gctx, f.Path, runner)
			}
			if err != nil {
				return err
			}
			displaySource := f.FileName
			if strings.TrimSpace(displaySource) == "" {
				displaySource = filepath.Base(f.Path)
			}
			tracks[i] = Track{
				Path:              f.Path,
				FileName:          displaySource,
				Duration:          dur,
				RawID3Title:       strings.TrimSpace(id3Title),
				DisplayTitle:      ResolveDisplayTitle(id3Title, displaySource),
				HasEmbeddedArt:    art.HasArt,
				EmbeddedArtWidth:  art.Width,
				EmbeddedArtHeight: art.Height,
				EmbeddedArtCodec:  art.Codec,
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
