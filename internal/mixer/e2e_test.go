package mixer

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/render"
)

type ffprobeMediaInfo struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		ColorSpace string `json:"color_space"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func assertOutputMP4AndTimestamps(t *testing.T, mp4Path, tsPath string, wantDuration float64, wantTimestamps string) {
	t.Helper()
	assertOutputMP4WithDimensionsAndTimestamps(t, mp4Path, tsPath, 1920, 1080, wantDuration, wantTimestamps)
}

func assertOutputMP4WithDimensionsAndTimestamps(t *testing.T, mp4Path, tsPath string, wantW, wantH int, wantDuration float64, wantTimestamps string) ffprobeMediaInfo {
	t.Helper()

	info, err := os.Stat(mp4Path)
	if err != nil {
		t.Fatalf("expected output %s: %v", mp4Path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("output %s is empty", mp4Path)
	}

	raw, err := ExecCommandRunner(
		context.Background(),
		"ffprobe",
		"-v", "error",
		"-show_format",
		"-show_streams",
		"-of", "json",
		mp4Path,
	)
	if err != nil {
		t.Fatalf("ffprobe %s failed: %v", mp4Path, err)
	}

	var media ffprobeMediaInfo
	if err := json.Unmarshal(raw, &media); err != nil {
		t.Fatalf("unmarshal ffprobe JSON for %s: %v", mp4Path, err)
	}

	var hasH264Video, hasAACAudio bool
	for _, s := range media.Streams {
		switch s.CodecType {
		case "video":
			if s.CodecName == "h264" && s.Width == wantW && s.Height == wantH {
				hasH264Video = true
			} else {
				t.Errorf("%s unexpected video stream (want %dx%d): %+v", mp4Path, wantW, wantH, s)
			}
		case "audio":
			if s.CodecName == "aac" {
				hasAACAudio = true
			} else {
				t.Errorf("%s unexpected audio stream: %+v", mp4Path, s)
			}
		}
	}
	if !hasH264Video {
		t.Errorf("%s missing %dx%d h264 video stream (streams: %+v)", mp4Path, wantW, wantH, media.Streams)
	}
	if !hasAACAudio {
		t.Errorf("%s missing aac audio stream (streams: %+v)", mp4Path, media.Streams)
	}
	if len(media.Streams) != 2 {
		t.Errorf("%s expected exactly 2 streams (video + audio), got %d", mp4Path, len(media.Streams))
	}

	gotDuration, err := strconv.ParseFloat(strings.TrimSpace(media.Format.Duration), 64)
	if err != nil {
		t.Fatalf("parse output duration %q for %s: %v", media.Format.Duration, mp4Path, err)
	}
	if math.Abs(gotDuration-wantDuration) > 0.6 {
		t.Errorf("%s duration = %.3fs, want ~%.1fs", mp4Path, gotDuration, wantDuration)
	}

	tsBytes, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("read timestamps %s: %v", tsPath, err)
	}
	if string(tsBytes) != wantTimestamps {
		t.Errorf("%s timestamps =\n%s\nwant:\n%s", tsPath, string(tsBytes), wantTimestamps)
	}
	return media
}

func TestE2EPipeline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("skipping E2E pipeline test: ffmpeg not on PATH (%v)", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("skipping E2E pipeline test: ffprobe not on PATH (%v)", err)
	}

	workDir := t.TempDir()
	assetsDir := filepath.Join(workDir, "test_assets")
	audioDir := filepath.Join(assetsDir, "tracks")
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(assetsDir) })

	track1 := filepath.Join(audioDir, "01_Sine_One.mp3")
	track2 := filepath.Join(audioDir, "02_Sine_Two.mp3")
	loopMP4 := filepath.Join(assetsDir, "bg_loop.mp4")
	bgPNG := filepath.Join(assetsDir, "bg_still.png")

	ctx := context.Background()
	synthCmds := [][]string{
		{"-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=4", "-af", "volume=0.3", "-c:a", "libmp3lame", "-b:a", "192k", track1},
		{"-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=523.25:duration=4", "-af", "volume=0.9", "-c:a", "libmp3lame", "-b:a", "192k", track2},
		{"-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=1280x720:rate=30", "-c:v", "libx264", "-pix_fmt", "yuv420p", loopMP4},
		{"-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=navy:s=1280x720:d=1", "-frames:v", "1", bgPNG},
	}
	for _, args := range synthCmds {
		if _, err := ExecCommandRunner(ctx, "ffmpeg", args...); err != nil {
			t.Fatalf("generate synthetic test asset (%v): %v", args, err)
		}
	}

	t.Run("Test 1 Path A looping video without overlays and 1s crossfade", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_a.mp4")
		tsPath := filepath.Join(workDir, "timestamps_a.txt")
		var stdout bytes.Buffer
		var sawProgress bool
		cliSink := NewCLITelemetry(&stdout, false)
		err := Run(ctx, Config{
			AudioDir:   audioDir,
			BG:         loopMP4,
			Out:        outMP4,
			Timestamps: tsPath,
			Crossfade:  1.0,
		}, func(ev TelemetryEvent) {
			if ev.Kind == EventProgress {
				sawProgress = true
			}
			cliSink(ev)
		}, exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path A Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		if !sawProgress {
			t.Errorf("expected live EventProgress telemetry during Path A render")
		}
		assertOutputMP4AndTimestamps(t, outMP4, tsPath, 7.0, "00:00 Sine_One\n00:03 Sine_Two\n")
	})

	t.Run("Test 2 Path B looping video with overlays and normalize", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_b.mp4")
		tsPath := filepath.Join(workDir, "timestamps_b.txt")
		var stdout bytes.Buffer
		err := Run(ctx, Config{
			AudioDir:      audioDir,
			BG:            loopMP4,
			Out:           outMP4,
			Timestamps:    tsPath,
			Normalize:     true,
			OverlayTitles: true,
			TitleDuration: 3,
			FontName:      "Arial",
		}, NewCLITelemetry(&stdout, false), exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path B Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		assertOutputMP4AndTimestamps(t, outMP4, tsPath, 8.0, "00:00 Sine_One\n00:04 Sine_Two\n")
	})

	t.Run("Test 3 Path C static image with overlays", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_c.mp4")
		tsPath := filepath.Join(workDir, "timestamps_c.txt")
		var stdout bytes.Buffer
		err := Run(ctx, Config{
			AudioDir:      audioDir,
			BG:            bgPNG,
			Out:           outMP4,
			Timestamps:    tsPath,
			OverlayTitles: true,
			FontName:      "Arial",
		}, NewCLITelemetry(&stdout, false), exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path C Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		assertOutputMP4AndTimestamps(t, outMP4, tsPath, 8.0, "00:00 Sine_One\n00:04 Sine_Two\n")
	})

	t.Run("Test 4 Path D default Auto Visualizer bars with omitted BG", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_d_bars.mp4")
		tsPath := filepath.Join(workDir, "timestamps_d_bars.txt")
		var stdout bytes.Buffer
		var sawProgress bool
		cliSink := NewCLITelemetry(&stdout, false)
		err := Run(ctx, Config{
			AudioDir:       audioDir,
			Out:            outMP4,
			Timestamps:     tsPath,
			VisualizerGlow: true,
		}, func(ev TelemetryEvent) {
			if ev.Kind == EventProgress {
				sawProgress = true
			}
			cliSink(ev)
		}, exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path D default bars Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		if !sawProgress {
			t.Errorf("expected live EventProgress telemetry during Path D render")
		}
		if !strings.Contains(stdout.String(), "Path D") {
			t.Errorf("expected Path D in CLI output:\n%s", stdout.String())
		}
		media := assertOutputMP4WithDimensionsAndTimestamps(t, outMP4, tsPath, 1920, 1080, 8.0, "00:00 Sine_One\n00:04 Sine_Two\n")
		if media.Streams[0].CodecType == "video" && media.Streams[0].ColorSpace != "bt709" {
			t.Errorf("expected bt709 color_space on Path D output, got %q", media.Streams[0].ColorSpace)
		}

		// Verify gbrp glow blend keeps the unlit upper canvas pure black (Y<=17, U≈128, V≈128) rather than yuv420p purple.
		sigJSON, err := ExecCommandRunner(
			ctx,
			"ffprobe",
			"-v", "error",
			"-f", "lavfi",
			"-i", "movie='"+render.EscapeFFmpegFilterPath(outMP4)+"',trim=start=1:end=2,crop=1920:300:0:0,signalstats",
			"-show_entries", "frame_tags=lavfi.signalstats.YAVG,lavfi.signalstats.UAVG,lavfi.signalstats.VAVG",
			"-read_intervals", "%+#1",
			"-of", "json",
		)
		if err != nil {
			t.Fatalf("ffprobe signalstats for Path D bars failed: %v", err)
		}
		var sigParsed struct {
			Frames []struct {
				Tags map[string]string `json:"tags"`
			} `json:"frames"`
		}
		if err := json.Unmarshal(sigJSON, &sigParsed); err != nil || len(sigParsed.Frames) == 0 {
			t.Fatalf("parse signalstats JSON (%v): %s", err, string(sigJSON))
		}
		tags := sigParsed.Frames[0].Tags
		for _, check := range []struct {
			key string
			min float64
			max float64
		}{
			{"lavfi.signalstats.YAVG", 14.0, 17.0},
			{"lavfi.signalstats.UAVG", 126.0, 130.0},
			{"lavfi.signalstats.VAVG", 126.0, 130.0},
		} {
			val, err := strconv.ParseFloat(tags[check.key], 64)
			if err != nil {
				t.Fatalf("parse %s=%q: %v", check.key, tags[check.key], err)
			}
			if val < check.min || val > check.max {
				t.Fatalf("Path D bars background %s = %.2f (want %.1f..%.1f; all=%v)", check.key, val, check.min, check.max, tags)
			}
		}
	})

	t.Run("Test 5 Path D black 2fps stillimage screen emits pure dark frames", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_d_black.mp4")
		tsPath := filepath.Join(workDir, "timestamps_d_black.txt")
		var stdout bytes.Buffer
		err := Run(ctx, Config{
			AudioDir:   audioDir,
			Out:        outMP4,
			Timestamps: tsPath,
			Visualizer: "black",
		}, NewCLITelemetry(&stdout, false), exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path D black Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		assertOutputMP4AndTimestamps(t, outMP4, tsPath, 8.0, "00:00 Sine_One\n00:04 Sine_Two\n")

		// Verify blackout frames remain near-zero luma (BT.709 limited-range black Y=16).
		sigOut, err := ExecCommandRunner(
			ctx,
			"ffprobe",
			"-v", "error",
			"-f", "lavfi",
			"-i", "movie='"+render.EscapeFFmpegFilterPath(outMP4)+"',signalstats",
			"-show_entries", "frame_tags=lavfi.signalstats.YAVG",
			"-read_intervals", "%+#2",
			"-of", "default=noprint_wrappers=1:nokey=1",
		)
		if err != nil {
			t.Fatalf("ffprobe signalstats for blackout video failed: %v", err)
		}
		lines := strings.Fields(string(sigOut))
		if len(lines) == 0 {
			t.Fatalf("expected signalstats YAVG readings for %s", outMP4)
		}
		for _, valStr := range lines {
			yavg, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				t.Fatalf("parse YAVG %q: %v", valStr, err)
			}
			if yavg > 17.0 {
				t.Fatalf("expected blackout YAVG <= 17.0, got %.2f (all=%v)", yavg, lines)
			}
		}
	})

	t.Run("Test 6 Path D ember with brightness dimmer, short-duration fade omission, overlay, and dim titles", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_d_ember.mp4")
		tsPath := filepath.Join(workDir, "timestamps_d_ember.txt")
		var stdout bytes.Buffer
		err := Run(ctx, Config{
			AudioDir:             audioDir,
			Out:                  outMP4,
			Timestamps:           tsPath,
			Visualizer:           "ember",
			VisualizerTheme:      "oled-ember",
			VisualizerGlow:       true,
			VisualizerBrightness: 40,
			FadeToBlack:          1 * time.Minute,
			OverlayTitles:        true,
			DimTitles:            true,
			FontName:             "Arial",
		}, NewCLITelemetry(&stdout, false), exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path D ember Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		assertOutputMP4AndTimestamps(t, outMP4, tsPath, 8.0, "00:00 Sine_One\n00:04 Sine_Two\n")
	})

	t.Run("Test 7 Path D waves at 1440p QHD with glow and title overlays", func(t *testing.T) {
		outMP4 := filepath.Join(workDir, "out_path_d_1440p.mp4")
		tsPath := filepath.Join(workDir, "timestamps_d_1440p.txt")
		var stdout bytes.Buffer
		err := Run(ctx, Config{
			AudioDir:             audioDir,
			Out:                  outMP4,
			Timestamps:           tsPath,
			Visualizer:           "waves",
			VisualizerTheme:      "midnight-cyan",
			VisualizerResolution: "1440p",
			VisualizerGlow:       true,
			OverlayTitles:        true,
			FontName:             "Arial",
		}, NewCLITelemetry(&stdout, false), exec.LookPath, ExecCommandRunner, ExecStreamRunner)
		if err != nil {
			t.Fatalf("Path D 1440p waves Run failed: %v\nstdout:\n%s", err, stdout.String())
		}
		media := assertOutputMP4WithDimensionsAndTimestamps(t, outMP4, tsPath, 2560, 1440, 8.0, "00:00 Sine_One\n00:04 Sine_Two\n")
		if media.Streams[0].CodecType == "video" && media.Streams[0].ColorSpace != "bt709" {
			t.Errorf("expected bt709 color_space on 1440p Path D output, got %q", media.Streams[0].ColorSpace)
		}
	})
}
