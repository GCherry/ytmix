package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type ffprobeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

var leadingNumberPattern = regexp.MustCompile(`^\d+[\s._-]+`)

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
	if err != nil || dur <= 0 {
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

// BackgroundMeta holds video stream and container metadata probed from a Background Visual.
type BackgroundMeta struct {
	DurationSec float64 `json:"durationSec"`
	FPS         float64 `json:"fps"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	CodecName   string  `json:"codecName"`
	PixFmt      string  `json:"pixFmt"`
	NBFrames    int64   `json:"nbFrames"`
}

type ffprobeBGOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
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
	if fps <= 0 || fps > 240 {
		return 0
	}
	return fps
}

// ProbeBackgroundVisual runs ffprobe on a Background Visual file to extract dimensions, frame rate,
// codec, pixel format, frame count, and duration (inferring GIF duration from nb_frames/FPS when N/A).
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
		"-select_streams", "v:0",
		"-show_entries", "format=duration:stream=codec_name,width,height,r_frame_rate,avg_frame_rate,nb_frames,pix_fmt",
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
	if dur, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64); err == nil && dur > 0 {
		meta.DurationSec = dur
	}

	if len(parsed.Streams) > 0 {
		st := parsed.Streams[0]
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

	return meta, nil
}
