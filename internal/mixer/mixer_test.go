package mixer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateConfig(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("rejects missing bg", func(t *testing.T) {
		cfg := Config{AudioDir: "./tracks", Out: "output.mp4", Timestamps: "timestamps.txt"}
		err := ValidateConfig(cfg, okLookPath)
		if err == nil || !strings.Contains(err.Error(), "--bg") {
			t.Fatalf("expected --bg error, got: %v", err)
		}
	})

	t.Run("rejects unsupported bg extension and accepts supported case-insensitively", func(t *testing.T) {
		for _, valid := range []string{"a.jpg", "a.JPEG", "a.png", "a.webp", "a.mp4", "a.MOV", "a.gif"} {
			cfg := Config{BG: valid}
			if err := ValidateConfig(cfg, okLookPath); err != nil {
				t.Fatalf("expected %s to be valid, got: %v", valid, err)
			}
		}
		for _, invalid := range []string{"a.bmp", "a.mkv", "a.txt", "noext"} {
			cfg := Config{BG: invalid}
			if err := ValidateConfig(cfg, okLookPath); err == nil {
				t.Fatalf("expected %s to be rejected", invalid)
			}
		}
	})

	t.Run("rejects negative crossfade and negative title-duration", func(t *testing.T) {
		if err := ValidateConfig(Config{BG: "a.png", Crossfade: -0.5}, okLookPath); err == nil {
			t.Fatal("expected error for negative --crossfade")
		}
		if err := ValidateConfig(Config{BG: "a.png", TitleDuration: -1}, okLookPath); err == nil {
			t.Fatal("expected error for negative --title-duration")
		}
	})

	t.Run("checks ffmpeg and ffprobe availability", func(t *testing.T) {
		missingFFmpeg := func(name string) (string, error) {
			if name == "ffmpeg" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		}
		if err := ValidateConfig(Config{BG: "a.png"}, missingFFmpeg); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
			t.Fatalf("expected ffmpeg error, got: %v", err)
		}

		missingFFprobe := func(name string) (string, error) {
			if name == "ffprobe" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		}
		if err := ValidateConfig(Config{BG: "a.png"}, missingFFprobe); err == nil || !strings.Contains(err.Error(), "ffprobe") {
			t.Fatalf("expected ffprobe error, got: %v", err)
		}
	})
}

func TestProbeTracks(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "01 - First.mp3")
	p2 := filepath.Join(dir, "02 - Second.MP3")
	for _, p := range []string{p1, p2} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var calls [][]string
	stubRunner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ffprobe" {
			return nil, errors.New("unexpected binary: " + name)
		}
		calls = append(calls, args)
		target := args[len(args)-1]
		if strings.HasSuffix(target, "01 - First.mp3") {
			return []byte(`{"format":{"duration":"125.500000","tags":{"TITLE":"  Dawn Horizon  "}}}`), nil
		}
		return []byte(`{"format":{"duration":"90.250000","tags":{}}}`), nil
	}

	tracks, err := ProbeTracks(context.Background(), dir, false, nil, stubRunner)
	if err != nil {
		t.Fatalf("ProbeTracks error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	wantArgs := []string{"-v", "error", "-show_entries", "format=duration", "-show_entries", "format_tags=title", "-of", "json", p1}
	if !reflect.DeepEqual(calls[0], wantArgs) {
		t.Fatalf("ffprobe args = %v, want %v", calls[0], wantArgs)
	}
	if tracks[0].DisplayTitle != "Dawn Horizon" || tracks[0].Duration != 125.5 {
		t.Fatalf("track[0] = %+v", tracks[0])
	}
	if tracks[1].DisplayTitle != "Second" || tracks[1].Duration != 90.25 {
		t.Fatalf("track[1] = %+v", tracks[1])
	}
}

func setupStubMixWorkspace(t *testing.T, bgName string) (string, string, string) {
	t.Helper()
	workDir := t.TempDir()
	audioDir := filepath.Join(workDir, "tracks")
	if err := os.Mkdir(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01_First.mp3", "02_Second.mp3"} {
		if err := os.WriteFile(filepath.Join(audioDir, name), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bgPath := filepath.Join(workDir, bgName)
	if err := os.WriteFile(bgPath, []byte("bg"), 0644); err != nil {
		t.Fatal(err)
	}
	return workDir, audioDir, bgPath
}

func TestRunPipelineSteps1To5(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("Path A looping video without overlays", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "loop.mp4")
		tsPath := filepath.Join(workDir, "timestamps.txt")
		cfg := Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			Out:        filepath.Join(workDir, "out_a.mp4"),
			Timestamps: tsPath,
			Crossfade:  1.0,
		}

		var ffmpegCmds [][]string
		runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if ctx == nil {
				t.Fatal("expected non-nil context")
			}
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			ffmpegCmds = append(ffmpegCmds, args)
			return nil, nil
		}

		var stdout bytes.Buffer
		if err := Run(context.Background(), cfg, &stdout, okLookPath, runner); err != nil {
			t.Fatalf("Run Path A error: %v", err)
		}

		logOut := stdout.String()
		for _, step := range []string{"[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]", "Path A"} {
			if !strings.Contains(logOut, step) {
				t.Errorf("expected %q in stdout, got:\n%s", step, logOut)
			}
		}

		tsBytes, err := os.ReadFile(tsPath)
		if err != nil {
			t.Fatalf("read timestamps: %v", err)
		}
		if string(tsBytes) != "00:00 First\n00:03 Second\n" {
			t.Fatalf("timestamps =\n%s", string(tsBytes))
		}

		// Expect 3 ffmpeg calls: acrossfade merge, Path A norm_loop, Path A mux.
		if len(ffmpegCmds) != 3 {
			t.Fatalf("expected 3 ffmpeg calls, got %d: %v", len(ffmpegCmds), ffmpegCmds)
		}
		muxJoined := strings.Join(ffmpegCmds[2], " ")
		if !strings.Contains(muxJoined, "-stream_loop -1") || !strings.Contains(muxJoined, "-c:v copy") {
			t.Fatalf("expected Path A mux args, got: %s", muxJoined)
		}
	})

	t.Run("Path B looping video with --overlay-titles writes titles.ass and burns subtitles", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "loop.mov")
		tsPath := filepath.Join(workDir, "timestamps.txt")
		cfg := Config{
			AudioDir:      audioDir,
			BG:            bgPath,
			Out:           filepath.Join(workDir, "out_b.mp4"),
			Timestamps:    tsPath,
			Normalize:     true,
			OverlayTitles: true,
			TitleDuration: 2,
			FontName:      "Inter",
		}

		var capturedASS string
		var renderArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "subtitles=") {
				renderArgs = args
				for _, a := range args {
					if idx := strings.Index(a, "subtitles='"); idx >= 0 {
						// Read the titles.ass from tempDir before Run returns and cleans it up.
						rest := a[idx+len("subtitles='"):]
						if end := strings.Index(rest, "'"); end >= 0 {
							escapedPath := rest[:end]
							// Unescape for test verification on Windows/POSIX.
							assPath := strings.ReplaceAll(escapedPath, `\:`, `:`)
							assPath = strings.ReplaceAll(assPath, `\'`, `'`)
							data, err := os.ReadFile(assPath)
							if err != nil {
								t.Fatalf("read titles.ass at %q: %v", assPath, err)
							}
							capturedASS = string(data)
						}
					}
				}
			}
			return nil, nil
		}

		var stdout bytes.Buffer
		if err := Run(context.Background(), cfg, &stdout, okLookPath, runner); err != nil {
			t.Fatalf("Run Path B error: %v", err)
		}
		if len(renderArgs) == 0 {
			t.Fatal("expected Path B render command with subtitles= filter")
		}
		if !strings.Contains(capturedASS, "Style: Default,Inter,42,") ||
			!strings.Contains(capturedASS, `Dialogue: 0,0:00:00.00,0:00:02.00,Default,,0,0,0,,{\fad(500,500)}♪ First`) ||
			!strings.Contains(capturedASS, `Dialogue: 0,0:00:04.00,0:00:06.00,Default,,0,0,0,,{\fad(500,500)}♪ Second`) {
			t.Fatalf("unexpected titles.ass content:\n%s", capturedASS)
		}
		if !strings.Contains(stdout.String(), "Path B") {
			t.Errorf("expected Path B in stdout:\n%s", stdout.String())
		}
	})

	t.Run("Path C static image with --overlay-titles", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		cfg := Config{
			AudioDir:      audioDir,
			BG:            bgPath,
			Out:           filepath.Join(workDir, "out_c.mp4"),
			Timestamps:    filepath.Join(workDir, "timestamps.txt"),
			OverlayTitles: true,
			FontName:      "Arial",
		}

		var lastArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			lastArgs = args
			return nil, nil
		}

		var stdout bytes.Buffer
		if err := Run(context.Background(), cfg, &stdout, okLookPath, runner); err != nil {
			t.Fatalf("Run Path C error: %v", err)
		}
		joined := strings.Join(lastArgs, " ")
		if !strings.Contains(joined, "-loop 1 -framerate 10") || !strings.Contains(joined, "-tune stillimage") {
			t.Fatalf("expected Path C args, got: %s", joined)
		}
		if !strings.Contains(stdout.String(), "Path C") {
			t.Errorf("expected Path C in stdout:\n%s", stdout.String())
		}
	})

	t.Run("stops immediately when context is already cancelled", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		cfg := Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			Out:        filepath.Join(workDir, "out.mp4"),
			Timestamps: filepath.Join(workDir, "timestamps.txt"),
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var stdout bytes.Buffer
		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("runner should not be invoked when context is cancelled")
			return nil, nil
		}
		if err := Run(ctx, cfg, &stdout, okLookPath, runner); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})
}
