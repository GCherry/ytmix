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
)

type ffprobeMediaInfo struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func assertOutputMP4AndTimestamps(t *testing.T, mp4Path, tsPath string, wantDuration float64, wantTimestamps string) {
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
			if s.CodecName == "h264" && s.Width == 1920 && s.Height == 1080 {
				hasH264Video = true
			} else {
				t.Errorf("%s unexpected video stream: %+v", mp4Path, s)
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
		t.Errorf("%s missing 1920x1080 h264 video stream (streams: %+v)", mp4Path, media.Streams)
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
}
