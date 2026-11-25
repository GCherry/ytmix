package mixer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/render"
)

func TestValidateConfig(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("accepts omitted bg for Path D Auto Visualizer and validates visualizer bounds", func(t *testing.T) {
		cfg := Config{AudioDir: "./tracks", Out: "output.mp4", Timestamps: "timestamps.txt"}
		if err := ValidateConfig(cfg, okLookPath); err != nil {
			t.Fatalf("expected omitted --bg to enter Path D cleanly, got: %v", err)
		}

		for _, bad := range []Config{
			{Visualizer: "unknown-preset"},
			{VisualizerTheme: "unknown-theme"},
			{VisualizerResolution: "720p"},
			{VisualizerBrightness: 9},
			{VisualizerBrightness: 101},
			{FadeToBlack: -1 * time.Second},
		} {
			if err := ValidateConfig(bad, okLookPath); err == nil {
				t.Fatalf("expected error for invalid Path D config %+v", bad)
			}
		}
		resCfg := Config{VisualizerResolution: "1440p"}
		if got := resCfg.VisualizerConfig().Resolution; got != "1440p" {
			t.Fatalf("VisualizerConfig().Resolution = %q, want 1440p", got)
		}
	})

	t.Run("rejects simultaneous bg and explicit visualizer", func(t *testing.T) {
		err := ValidateConfig(Config{BG: "cover.png", Visualizer: "bars"}, okLookPath)
		if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
			t.Fatalf("expected mutual exclusion error, got: %v", err)
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

	t.Run("ValidateBackgroundFile checks existence and rejects directories", func(t *testing.T) {
		tmp := t.TempDir()
		if err := ValidateBackgroundFile(filepath.Join(tmp, "missing.png")); err == nil {
			t.Fatal("expected error for missing file")
		}
		if err := ValidateBackgroundFile(tmp); err == nil || !strings.Contains(err.Error(), "directory") {
			t.Fatalf("expected directory error, got: %v", err)
		}
		bg := filepath.Join(tmp, "cover.png")
		if err := os.WriteFile(bg, []byte("png"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateBackgroundFile(bg); err != nil {
			t.Fatalf("ValidateBackgroundFile valid file error: %v", err)
		}
	})

	t.Run("PreflightConfig selects path and respects prepareOutputs flag", func(t *testing.T) {
		tmp := t.TempDir()
		bg := filepath.Join(tmp, "loop.mp4")
		if err := os.WriteFile(bg, []byte("mp4"), 0644); err != nil {
			t.Fatal(err)
		}

		outSub := filepath.Join(tmp, "nested")
		cfg := Config{
			BG:         bg,
			OutDir:     outSub,
			Out:        "mix.mp4",
			Timestamps: "chapters.txt",
		}
		resCfg, pathKind, err := PreflightConfig(cfg, okLookPath, false)
		if err != nil {
			t.Fatalf("PreflightConfig(prepareOutputs=false) error: %v", err)
		}
		if pathKind != render.PathA {
			t.Fatalf("pathKind = %q, want %q", pathKind, render.PathA)
		}
		if resCfg.Out != "mix.mp4" {
			t.Fatalf("expected unresolved Out when prepareOutputs=false, got %q", resCfg.Out)
		}
		if _, err := os.Stat(outSub); !os.IsNotExist(err) {
			t.Fatalf("expected outSub not created when prepareOutputs=false, err=%v", err)
		}

		resCfg, pathKind, err = PreflightConfig(Config{
			BG:            bg,
			OutDir:        outSub,
			Out:           "mix.mp4",
			Timestamps:    "chapters.txt",
			OverlayTitles: true,
		}, okLookPath, true)
		if err != nil {
			t.Fatalf("PreflightConfig(prepareOutputs=true) error: %v", err)
		}
		if pathKind != render.PathB {
			t.Fatalf("pathKind = %q, want %q", pathKind, render.PathB)
		}
		if resCfg.Out != filepath.Join(outSub, "mix.mp4") {
			t.Fatalf("expected resolved Out inside outSub, got %q", resCfg.Out)
		}
		if info, err := os.Stat(outSub); err != nil || !info.IsDir() {
			t.Fatalf("expected outSub directory created when prepareOutputs=true, err=%v", err)
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

	var mu sync.Mutex
	var calls [][]string
	stubRunner := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ffprobe" {
			return nil, errors.New("unexpected binary: " + name)
		}
		mu.Lock()
		calls = append(calls, args)
		mu.Unlock()
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
	foundP1Args := false
	for _, c := range calls {
		if reflect.DeepEqual(c, wantArgs) {
			foundP1Args = true
			break
		}
	}
	if !foundP1Args {
		t.Fatalf("ffprobe calls %v did not include %v", calls, wantArgs)
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

		var events []TelemetryEvent
		sink := func(ev TelemetryEvent) {
			events = append(events, ev)
		}
		if err := Run(context.Background(), cfg, sink, okLookPath, runner, nil); err != nil {
			t.Fatalf("Run Path A error: %v", err)
		}

		var stdout bytes.Buffer
		cliSink := NewCLITelemetry(&stdout, false)
		for _, ev := range events {
			cliSink(ev)
		}
		logOut := stdout.String()
		for _, step := range []string{"[1/5]", "Estimated Mix render time:", "[2/5]", "[3/5]", "[4/5]", "[5/5]", "Path A"} {
			if !strings.Contains(logOut, step) {
				t.Errorf("expected %q in telemetry output, got:\n%s", step, logOut)
			}
		}

		for i := 1; i < len(events); i++ {
			if events[i].Progress < events[i-1].Progress {
				t.Errorf("progress went backwards at %d: %d < %d (%+v)", i, events[i].Progress, events[i-1].Progress, events)
			}
		}
		if len(events) != 6 || events[0].Progress != 4 || events[5].Progress != 100 {
			t.Fatalf("unexpected step events: %+v", events)
		}
		if events[1].Step != 2 || events[1].Estimate == nil {
			t.Fatalf("expected Step 2 event to attach Estimate, got: %+v", events[1])
		}
		if events[4].Step != 5 || events[4].Progress >= 100 || events[4].EstimatedTotalSec <= 0 {
			t.Fatalf("expected Step 5 pre-render estimate and <100%% progress, got: %+v", events[4])
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
		if err := Run(context.Background(), cfg, NewCLITelemetry(&stdout, false), okLookPath, runner, nil); err != nil {
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

	t.Run("Path C static image with --overlay-titles and live -progress stream", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		cfg := Config{
			AudioDir:      audioDir,
			BG:            bgPath,
			Out:           filepath.Join(workDir, "out_c.mp4"),
			Timestamps:    filepath.Join(workDir, "timestamps.txt"),
			OverlayTitles: true,
			FontName:      "Arial",
		}

		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			return nil, nil
		}
		var lastStreamArgs []string
		streamRunner := func(_ context.Context, onLine func(string), name string, args ...string) error {
			lastStreamArgs = args
			for _, l := range []string{
				"out_time_us=4000000", "speed=2.00x", "progress=continue",
				"out_time_us=8000000", "speed=2.50x", "progress=end",
			} {
				onLine(l)
			}
			return nil
		}

		var events []TelemetryEvent
		if err := Run(context.Background(), cfg, func(ev TelemetryEvent) { events = append(events, ev) }, okLookPath, runner, streamRunner); err != nil {
			t.Fatalf("Run Path C error: %v", err)
		}
		joined := strings.Join(lastStreamArgs, " ")
		if !strings.Contains(joined, "-progress pipe:1") || !strings.Contains(joined, "-loop 1 -framerate 10") || !strings.Contains(joined, "-tune stillimage") {
			t.Fatalf("expected Path C streamed args, got: %s", joined)
		}

		var step5Start int
		var progressEvents []TelemetryEvent
		for _, ev := range events {
			if ev.Kind == EventStep && ev.Step == 5 {
				step5Start = ev.Progress
			}
			if ev.Kind == EventProgress {
				progressEvents = append(progressEvents, ev)
			}
		}
		if len(progressEvents) != 2 {
			t.Fatalf("expected 2 Step 5 EventProgress events, got %d (%+v)", len(progressEvents), events)
		}
		if progressEvents[0].Progress <= step5Start || progressEvents[0].Progress >= 99 || progressEvents[0].RemainingSec < 0 {
			t.Fatalf("unexpected mid Step 5 progress event: %+v (step5Start=%d)", progressEvents[0], step5Start)
		}
		if progressEvents[1].Progress != 99 {
			t.Fatalf("expected final Step 5 progress = 99, got %+v", progressEvents[1])
		}
		if events[len(events)-1].Kind != EventDone || events[len(events)-1].Progress != 100 || !strings.Contains(events[len(events)-1].Message, "Path C") {
			t.Fatalf("unexpected final event: %+v", events[len(events)-1])
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

		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("runner should not be invoked when context is cancelled")
			return nil, nil
		}
		if err := Run(ctx, cfg, nil, okLookPath, runner, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("Path D omitted --bg wires DimTitles ASS, VisualizerConfig, and filter preflight", func(t *testing.T) {
		workDir, audioDir, _ := setupStubMixWorkspace(t, "unused.png")
		tsPath := filepath.Join(workDir, "timestamps_d.txt")
		cfg := Config{
			AudioDir:             audioDir,
			Out:                  filepath.Join(workDir, "out_d.mp4"),
			Timestamps:           tsPath,
			OverlayTitles:        true,
			DimTitles:            true,
			Visualizer:           "ember",
			VisualizerTheme:      "oled-ember",
			VisualizerGlow:       true,
			VisualizerBrightness: 45,
			FadeToBlack:          15 * time.Minute,
		}

		var capturedASS string
		var renderArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "-filters") {
				return []byte(" .. showwaves A->V Wave\n TS gblur V->V Blur\n .. scale V->V Scale\n .. color |->V Color\n"), nil
			}
			if strings.Contains(joined, "-filter_complex") {
				renderArgs = args
				for _, a := range args {
					if idx := strings.Index(a, "subtitles='"); idx >= 0 {
						rest := a[idx+len("subtitles='"):]
						if end := strings.Index(rest, "'"); end >= 0 {
							assPath := strings.ReplaceAll(rest[:end], `\:`, `:`)
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
		if err := Run(context.Background(), cfg, NewCLITelemetry(&stdout, false), okLookPath, runner, nil); err != nil {
			t.Fatalf("Run Path D error: %v", err)
		}
		if len(renderArgs) == 0 {
			t.Fatal("expected Path D render command with -filter_complex")
		}
		if !strings.Contains(capturedASS, "&H789EB2AF") || !strings.Contains(capturedASS, "&H90000000") {
			t.Fatalf("expected dimmed ASS style, got:\n%s", capturedASS)
		}
		joinedRender := strings.Join(renderArgs, " ")
		if !strings.Contains(joinedRender, "colorchannelmixer=rr=0.45:gg=0.45:bb=0.45") || !strings.Contains(joinedRender, "gblur=sigma=18") {
			t.Fatalf("unexpected Path D render args: %s", joinedRender)
		}
		if !strings.Contains(stdout.String(), "Path D") {
			t.Fatalf("expected Path D in stdout:\n%s", stdout.String())
		}
	})

	t.Run("Path D fails upfront before ffprobe when required FFmpeg filter is missing", func(t *testing.T) {
		workDir, audioDir, _ := setupStubMixWorkspace(t, "unused.png")
		cfg := Config{
			AudioDir:   audioDir,
			Out:        filepath.Join(workDir, "out_3d.mp4"),
			Timestamps: filepath.Join(workDir, "timestamps_3d.txt"),
			Visualizer: "3dscope",
		}
		var probedTrack atomic.Bool
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				probedTrack.Store(true)
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			if strings.Contains(strings.Join(args, " "), "-filters") {
				return []byte(" .. showfreqs A->V Freqs\n"), nil
			}
			return nil, nil
		}
		err := Run(context.Background(), cfg, nil, okLookPath, runner, nil)
		if err == nil || !strings.Contains(err.Error(), "a3dscope") {
			t.Fatalf("expected missing a3dscope error, got: %v", err)
		}
		if probedTrack.Load() {
			t.Fatal("expected missing visualizer filter check to fail upfront before probing tracks")
		}
	})
}

func TestCLITelemetryFormatting(t *testing.T) {
	t.Run("non-TTY suppresses EventProgress and prints milestones and pre-render estimate cleanly", func(t *testing.T) {
		var buf bytes.Buffer
		sink := NewCLITelemetry(&buf, false)
		est := render.MixEstimate{
			Path:        render.PathB,
			EncoderName: "libx264",
			AudioSec:    35,
			VideoSec:    50,
			LowSec:      85,
			HighSec:     125,
		}
		sink(TelemetryEvent{Kind: EventStep, Step: 1, TotalSteps: 5, Progress: 4, Message: "[1/5] Probing Tracks..."})
		sink(TelemetryEvent{Kind: EventStep, Step: 2, TotalSteps: 5, Progress: 12, Message: "[2/5] Processing & merging 2 Track(s)...", Estimate: &est})
		sink(TelemetryEvent{Kind: EventStep, Step: 5, TotalSteps: 5, Progress: 45, Message: "[5/5] Rendering Mix video..."})
		sink(TelemetryEvent{Kind: EventProgress, Step: 5, TotalSteps: 5, Progress: 55, Speed: "2.4x", RemainingSec: 72})
		sink(TelemetryEvent{Kind: EventDone, Step: 5, TotalSteps: 5, Progress: 100, Message: "Done! Rendered out.mp4."})

		got := buf.String()
		if strings.Contains(got, "\r") || strings.Contains(got, "55%") || strings.Contains(got, "left") {
			t.Fatalf("non-TTY should not emit \\r or progress spam, got: %q", got)
		}
		want := "[1/5] Probing Tracks...\nEstimated Mix render time: ~1m 25s – 2m 05s (Path B · libx264 · Audio ~35s, Video ~50s)\n[2/5] Processing & merging 2 Track(s)...\n[5/5] Rendering Mix video...\nDone! Rendered out.mp4.\n"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("TTY overwrites single status line with speed and emits newline before Done", func(t *testing.T) {
		var buf bytes.Buffer
		sink := NewCLITelemetry(&buf, true)
		sink(TelemetryEvent{Kind: EventStep, Step: 5, TotalSteps: 5, Progress: 20, Message: "[5/5] Rendering Mix video..."})
		sink(TelemetryEvent{Kind: EventProgress, Step: 5, TotalSteps: 5, Progress: 55, Speed: "2.4x"})
		sink(TelemetryEvent{Kind: EventDone, Step: 5, TotalSteps: 5, Progress: 100, Message: "Done! Rendered out.mp4."})

		got := buf.String()
		if !strings.Contains(got, "\r[5/5] Rendering Mix... 55% (2.4x)") || !strings.Contains(got, "\nDone! Rendered out.mp4.\n") {
			t.Fatalf("unexpected TTY output: %q", got)
		}
	})

	t.Run("TTY formats remaining ETA countdown, normalize progress, and clears trailing characters", func(t *testing.T) {
		var buf bytes.Buffer
		sink := NewCLITelemetry(&buf, true)
		sink(TelemetryEvent{Kind: EventStep, Step: 2, TotalSteps: 5, Progress: 4, Message: "[2/5] Processing..."})
		sink(TelemetryEvent{Kind: EventProgress, Step: 2, TotalSteps: 5, Progress: 18, Substage: "normalize", RemainingSec: 65})
		sink(TelemetryEvent{Kind: EventStep, Step: 5, TotalSteps: 5, Progress: 40, Message: "[5/5] Rendering Mix video..."})
		sink(TelemetryEvent{Kind: EventProgress, Step: 5, TotalSteps: 5, Progress: 42, Speed: "2.4x", RemainingSec: 72})
		sink(TelemetryEvent{Kind: EventProgress, Step: 5, TotalSteps: 5, Progress: 90, Speed: "2.4x", RemainingSec: 9})
		sink(TelemetryEvent{Kind: EventDone, Step: 5, TotalSteps: 5, Progress: 100, Message: "Done!"})

		got := buf.String()
		for _, want := range []string{
			"\r[2/5] Normalizing Tracks... 18% (~1m 05s left)",
			"\r[5/5] Rendering Mix... 42% (2.4x · ~1m 12s left)",
			"\r[5/5] Rendering Mix... 90% (2.4x · ~9s left)   ",
			"\nDone!\n",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected TTY output to contain %q, got %q", want, got)
			}
		}
	})
}

func TestFormatEstimateHelpers(t *testing.T) {
	t.Run("FormatDurationRange formats compact bounds across seconds, minutes, hours, and edge values", func(t *testing.T) {
		cases := []struct {
			low, high float64
			want      string
		}{
			{12, 24, "~12s – 24s"},
			{85, 125, "~1m 25s – 2m 05s"},
			{3665, 4200, "~1h 01m 05s – 1h 10m 00s"},
			{15.2, 15.4, "~15s"},
			{math.NaN(), -5, "~0s"},
		}
		for _, tc := range cases {
			if got := FormatDurationRange(tc.low, tc.high); got != tc.want {
				t.Errorf("FormatDurationRange(%v, %v) = %q, want %q", tc.low, tc.high, got, tc.want)
			}
		}
	})

	t.Run("FormatEstimateReport covers Path A, Path C, and calibrated Path D", func(t *testing.T) {
		repA := FormatEstimateReport(Config{BG: "/tmp/loop.mp4", Crossfade: 2.5}, 4, render.MixEstimate{
			Path:            render.PathA,
			EncoderName:     "libx264",
			MixDurationSec:  360,
			AudioSec:        12,
			MergeSec:        12,
			VideoSec:        3,
			PathANormSec:    2,
			PathAMuxSec:     1,
			PointSec:        15,
			LowSec:          10,
			HighSec:         22,
			LoopDurationSec: 12,
			LoopFPS:         30,
			LoopCycles:      30,
			Weights:         render.StageWeights{Audio: 0.80, Video: 0.20},
		})
		for _, want := range []string{"4, Mix duration 6m 00s", "loop.mp4 (12.0s loop · 30 fps · 30 cycles)", "Path A (Stream Copy Loop)", "norm_loop ~2s, mux ~1s", "uncalibrated (hardware prior)"} {
			if !strings.Contains(repA, want) {
				t.Fatalf("Path A report missing %q:\n%s", want, repA)
			}
		}

		repC := FormatEstimateReport(Config{BG: "cover.png", OverlayTitles: true}, 2, render.MixEstimate{
			Path:           render.PathC,
			EncoderName:    "libx264",
			MixDurationSec: 90,
			AudioSec:       5,
			MergeSec:       5,
			VideoSec:       4,
			PointSec:       9,
			LowSec:         7,
			HighSec:        11,
			Calibrated:     true,
			Weights:        render.StageWeights{Audio: 0.56, Video: 0.44},
		})
		for _, want := range []string{"cover.png (static image · 10 fps)", "Path C (Low-FPS Still Image)", "calibrated (local EMA cache)"} {
			if !strings.Contains(repC, want) {
				t.Fatalf("Path C report missing %q:\n%s", want, repC)
			}
		}
	})
}

func TestProbeTrackFilesAndRunWithTracks(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("ProbeTrackFiles preserves explicit non-alphabetical order and resolves Display Titles", func(t *testing.T) {
		dir := t.TempDir()
		pZ := filepath.Join(dir, "02 - Zeta.mp3")
		pA := filepath.Join(dir, "01 - Alpha.mp3")
		for _, p := range []string{pZ, pA} {
			if err := os.WriteFile(p, []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		stubRunner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "ffprobe" {
				return nil, errors.New("unexpected binary: " + name)
			}
			target := args[len(args)-1]
			if strings.HasSuffix(target, "02 - Zeta.mp3") {
				return []byte(`{"format":{"duration":"12.500000","tags":{"title":"Zeta Custom"}}}`), nil
			}
			return []byte(`{"format":{"duration":"8.250000","tags":{}}}`), nil
		}

		tracks, err := ProbeTrackFiles(context.Background(), []string{pZ, pA}, stubRunner)
		if err != nil {
			t.Fatalf("ProbeTrackFiles error: %v", err)
		}
		if len(tracks) != 2 {
			t.Fatalf("expected 2 tracks, got %d", len(tracks))
		}
		if tracks[0].FileName != "02 - Zeta.mp3" || tracks[0].DisplayTitle != "Zeta Custom" || tracks[0].Duration != 12.5 {
			t.Fatalf("unexpected tracks[0]: %+v", tracks[0])
		}
		if tracks[1].FileName != "01 - Alpha.mp3" || tracks[1].DisplayTitle != "Alpha" || tracks[1].Duration != 8.25 {
			t.Fatalf("unexpected tracks[1]: %+v", tracks[1])
		}

		namedTracks, err := ProbeNamedTrackFiles(context.Background(), []NamedTrackFile{
			{Path: pZ, FileName: "09 - Custom Upload Name.mp3"},
		}, stubRunner)
		if err != nil {
			t.Fatalf("ProbeNamedTrackFiles error: %v", err)
		}
		if namedTracks[0].FileName != "09 - Custom Upload Name.mp3" || namedTracks[0].DisplayTitle != "Zeta Custom" {
			t.Fatalf("unexpected namedTracks[0]: %+v", namedTracks[0])
		}
	})

	t.Run("RunWithTracks honors caller Track ordering without re-scanning AudioDir", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		tsPath := filepath.Join(workDir, "timestamps.txt")
		cfg := Config{
			AudioDir:   filepath.Join(workDir, "nonexistent-dir"),
			BG:         bgPath,
			Out:        filepath.Join(workDir, "out_custom.mp4"),
			Timestamps: tsPath,
		}
		tracks := []Track{
			{
				Path:         filepath.Join(audioDir, "02_Second.mp3"),
				FileName:     "02_Second.mp3",
				Duration:     10.0,
				DisplayTitle: "Second First",
			},
			{
				Path:         filepath.Join(audioDir, "01_First.mp3"),
				FileName:     "01_First.mp3",
				Duration:     5.0,
				DisplayTitle: "First Second",
			},
		}

		runner := func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffprobe" {
				t.Fatal("ffprobe should not be called when tracks are already probed and normalize=false")
			}
			return nil, nil
		}

		var stdout bytes.Buffer
		if err := RunWithTracks(context.Background(), cfg, tracks, NewCLITelemetry(&stdout, false), okLookPath, runner, nil); err != nil {
			t.Fatalf("RunWithTracks error: %v", err)
		}

		logOut := stdout.String()
		for _, step := range []string{"[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]", "Path C"} {
			if !strings.Contains(logOut, step) {
				t.Errorf("expected %q in stdout, got:\n%s", step, logOut)
			}
		}

		tsBytes, err := os.ReadFile(tsPath)
		if err != nil {
			t.Fatalf("read timestamps: %v", err)
		}
		if string(tsBytes) != "00:00 Second First\n00:10 First Second\n" {
			t.Fatalf("timestamps =\n%s", string(tsBytes))
		}
	})

	t.Run("RunWithTracks resolves blank DisplayTitle from FileName rather than opaque Path", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		opaquePath := filepath.Join(audioDir, "track_0001.mp3")
		if err := os.WriteFile(opaquePath, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		tsPath := filepath.Join(workDir, "timestamps_upload.txt")
		cfg := Config{
			BG:         bgPath,
			Out:        filepath.Join(workDir, "out_upload.mp4"),
			Timestamps: tsPath,
		}
		tracks := []Track{
			{
				Path:         opaquePath,
				FileName:     "03 - Midnight Breeze.mp3",
				Duration:     12.0,
				DisplayTitle: "   ",
			},
		}
		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, nil
		}
		if err := RunWithTracks(context.Background(), cfg, tracks, nil, okLookPath, runner, nil); err != nil {
			t.Fatalf("RunWithTracks error: %v", err)
		}
		tsBytes, err := os.ReadFile(tsPath)
		if err != nil {
			t.Fatalf("read timestamps: %v", err)
		}
		if string(tsBytes) != "00:00 Midnight Breeze\n" {
			t.Fatalf("timestamps = %q, want %q", string(tsBytes), "00:00 Midnight Breeze\n")
		}
	})

	t.Run("RunWithTracks rejects empty Track slice", func(t *testing.T) {
		workDir, _, bgPath := setupStubMixWorkspace(t, "cover.png")
		cfg := Config{BG: bgPath, Out: filepath.Join(workDir, "out.mp4"), Timestamps: filepath.Join(workDir, "ts.txt")}
		err := RunWithTracks(context.Background(), cfg, nil, nil, okLookPath, func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, nil
		}, nil)
		if err == nil {
			t.Fatal("expected error for empty track slice")
		}
	})

	t.Run("EstimateConfig and weighted progress handle long Path A normalize Mixes", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "loop.mp4")
		origCalFn := calibrationPathFn
		calPath := filepath.Join(workDir, "cache", "calibration.json")
		calibrationPathFn = func() (string, error) { return calPath, nil }
		t.Cleanup(func() { calibrationPathFn = origCalFn })

		cfg := Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			Out:        filepath.Join(workDir, "out_long.mp4"),
			Timestamps: filepath.Join(workDir, "timestamps.txt"),
			Normalize:  true,
		}
		tracks := []Track{
			{Path: filepath.Join(audioDir, "01_First.mp3"), FileName: "01_First.mp3", Duration: 1800, DisplayTitle: "First"},
			{Path: filepath.Join(audioDir, "02_Second.mp3"), FileName: "02_Second.mp3", Duration: 1800, DisplayTitle: "Second"},
		}
		runner := func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"12.0"},"streams":[{"width":1920,"height":1080,"r_frame_rate":"30/1"}]}`), nil
			}
			return nil, nil
		}

		est, err := EstimateConfig(context.Background(), cfg, tracks, nil, okLookPath, runner)
		if err != nil {
			t.Fatalf("EstimateConfig error: %v", err)
		}
		if est.Path != render.PathA || est.LoopCycles != 300 || est.Weights.Audio <= 0.75 {
			t.Fatalf("unexpected long Path A estimate: %+v", est)
		}

		var events []TelemetryEvent
		streamRunner := func(_ context.Context, onLine func(string), _ string, args ...string) error {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "norm_loop.mp4") && !strings.Contains(joined, "-stream_loop") {
				onLine("out_time_us=6000000")
				onLine("speed=10.0x")
				onLine("progress=continue")
				onLine("out_time_us=12000000")
				onLine("speed=10.0x")
				onLine("progress=end")
			} else {
				onLine("out_time_us=1800000000")
				onLine("speed=500.0x")
				onLine("progress=continue")
				onLine("out_time_us=3600000000")
				onLine("speed=500.0x")
				onLine("progress=end")
			}
			return nil
		}
		if err := RunWithTracks(context.Background(), cfg, tracks, func(ev TelemetryEvent) { events = append(events, ev) }, okLookPath, runner, streamRunner); err != nil {
			t.Fatalf("RunWithTracks long Path A error: %v", err)
		}

		var sawNormProgress bool
		var normLoopRem, muxStartRem float64
		var step5Start int
		for _, ev := range events {
			if ev.Kind == EventProgress && ev.Step == 2 && ev.Substage == "normalize" {
				sawNormProgress = true
			}
			if ev.Kind == EventStep && ev.Step == 5 {
				step5Start = ev.Progress
			}
			if ev.Kind == EventProgress && ev.Substage == "norm_loop" && normLoopRem == 0 {
				normLoopRem = ev.RemainingSec
			}
			if ev.Kind == EventProgress && ev.Substage == "mux" && muxStartRem == 0 {
				muxStartRem = ev.RemainingSec
			}
		}
		if !sawNormProgress {
			t.Fatalf("expected per-track normalize EventProgress events: %+v", events)
		}
		if step5Start < 70 {
			t.Fatalf("expected long Path A --normalize to allocate >=70%% before Step 5, got step5Start=%d", step5Start)
		}
		if normLoopRem <= muxStartRem {
			t.Fatalf("expected norm_loop RemainingSec (%v) > mux RemainingSec (%v)", normLoopRem, muxStartRem)
		}
	})

	t.Run("recordCalibrationResults records PathA norm_loop+mux, PathC 2fps vs 10fps, PathB/D work speeds, and headless loudnorm", func(t *testing.T) {
		tmp := t.TempDir()
		calPath := filepath.Join(tmp, "cal.json")
		cal := make(map[string]render.CalibrationEntry)

		// Path A norm_loop + mux + headless loudnorm
		estA := render.MixEstimate{
			Path:             render.PathA,
			MixDurationSec:   600,
			TotalAudioSec:    620,
			PathANormWorkSec: 12,
		}
		recordCalibrationResults(calPath, cal, Config{Normalize: true}, estA, 600, 10.0, 20.0, &etaTracker{
			normLoopWallSec: 1.5,
			muxWallSec:      1.0,
		})
		if _, ok := cal[render.CalibrationKey(render.PathA, "norm_loop", render.Resolution1080p)]; !ok {
			t.Fatalf("expected PathA norm_loop calibration entry, got %+v", cal)
		}
		if _, ok := cal[render.CalibrationKey(render.PathA, "mux", render.Resolution1080p)]; !ok {
			t.Fatalf("expected PathA mux calibration entry, got %+v", cal)
		}
		if got := cal[render.AudioCalibrationKey("loudnorm")].Speed; math.Abs(got-62.0) > 1e-6 {
			t.Fatalf("loudnorm speed = %v, want 62.0 (TotalAudioSec/normElapsed)", got)
		}
		if got := cal[render.AudioCalibrationKey("concat")].Speed; math.Abs(got-30.0) > 1e-6 {
			t.Fatalf("concat speed = %v, want 30.0", got)
		}

		// Path C 2fps vs 10fps distinct keys
		estC := render.MixEstimate{Path: render.PathC, VideoWorkSec: 600}
		recordCalibrationResults(calPath, cal, Config{OverlayTitles: false}, estC, 600, 0, 1.0, &etaTracker{renderWallSec: 6.0})
		recordCalibrationResults(calPath, cal, Config{OverlayTitles: true}, estC, 600, 0, 1.0, &etaTracker{renderWallSec: 15.0})
		k2 := render.CalibrationKey(render.PathC, "libx264", "2fps")
		k10 := render.CalibrationKey(render.PathC, "libx264", "10fps")
		if cal[k2].Speed != 100.0 || cal[k10].Speed != 40.0 {
			t.Fatalf("unexpected PathC calibration entries: 2fps=%+v 10fps=%+v", cal[k2], cal[k10])
		}

		// Path B 60fps uses VideoWorkSec (1200) rather than raw MixDurationSec (600)
		estB := render.MixEstimate{Path: render.PathB, EncoderName: "libx264", VideoWorkSec: 1200}
		recordCalibrationResults(calPath, cal, Config{OverlayTitles: true}, estB, 600, 0, 1.0, &etaTracker{renderWallSec: 120.0})
		kB := render.CalibrationKey(render.PathB, "libx264", render.Resolution1080p)
		if math.Abs(cal[kB].Speed-10.0) > 1e-6 {
			t.Fatalf("PathB work speed = %v, want 10.0 (VideoWorkSec/renderWallSec)", cal[kB].Speed)
		}

		// Path D black 2fps vs 10fps distinct keys
		estD := render.MixEstimate{Path: render.PathD, EncoderName: "libx264", VideoWorkSec: 600}
		recordCalibrationResults(calPath, cal, Config{Visualizer: "black", VisualizerResolution: render.Resolution1080p, OverlayTitles: false}, estD, 600, 0, 1.0, &etaTracker{renderWallSec: 3.0})
		recordCalibrationResults(calPath, cal, Config{Visualizer: "black", VisualizerResolution: render.Resolution1080p, OverlayTitles: true}, estD, 600, 0, 1.0, &etaTracker{renderWallSec: 10.0})
		kd2 := render.CalibrationKey(render.PathD, "black-2fps", render.Resolution1080p)
		kd10 := render.CalibrationKey(render.PathD, "black-10fps", render.Resolution1080p)
		if cal[kd2].Speed != 200.0 || cal[kd10].Speed != 60.0 {
			t.Fatalf("unexpected PathD black entries: 2fps=%+v 10fps=%+v", cal[kd2], cal[kd10])
		}
	})

	t.Run("etaTracker preserves pre-first-progress wall time for norm_loop and single-block mux", func(t *testing.T) {
		renderStart := time.Now().Add(-750 * time.Millisecond)
		tr := &etaTracker{
			est:         render.MixEstimate{Path: render.PathA, LoopDurationSec: 10, PathANormSec: 1.0, PathAMuxSec: 0.2},
			mixDuration: 120,
			renderStart: renderStart,
		}
		// Single progress=end block after 750ms of norm_loop work
		tr.update(render.ProgressUpdate{Substage: "norm_loop", OutTimeSec: 10, Speed: "10.0x", Done: true})
		if tr.normLoopWallSec < 0.70 {
			t.Fatalf("expected normLoopWallSec >= 0.70s (including pre-first-progress time), got %v", tr.normLoopWallSec)
		}
		if tr.muxStart.IsZero() {
			t.Fatal("expected muxStart to be recorded when norm_loop finishes")
		}
		// Simulate fast sub-0.5s mux emitting only progress=end 100ms later
		tr.muxStart = time.Now().Add(-100 * time.Millisecond)
		tr.update(render.ProgressUpdate{Substage: "mux", OutTimeSec: 120, Speed: "1200x", Done: true})
		if tr.muxWallSec < 0.08 {
			t.Fatalf("expected muxWallSec >= 0.08s on single-block mux, got %v", tr.muxWallSec)
		}
	})
}

func TestResolveAndPrepareOutputTargets(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("ResolveOutputPaths joins relative Out and Timestamps under OutDir and honors absolute overrides", func(t *testing.T) {
		absOut := filepath.Join(t.TempDir(), "custom_abs.mp4")
		absTS := filepath.Join(t.TempDir(), "custom_abs.txt")

		outDir := filepath.Join("renders", "2026")
		gotOut, gotTS, err := ResolveOutputPaths(Config{OutDir: outDir})
		if err != nil {
			t.Fatalf("ResolveOutputPaths error: %v", err)
		}
		if gotOut != filepath.Join(outDir, "output.mp4") {
			t.Fatalf("gotOut = %q, want %q", gotOut, filepath.Join(outDir, "output.mp4"))
		}
		if gotTS != filepath.Join(outDir, "timestamps.txt") {
			t.Fatalf("gotTS = %q, want %q", gotTS, filepath.Join(outDir, "timestamps.txt"))
		}

		gotOut2, gotTS2, err := ResolveOutputPaths(Config{
			OutDir:     outDir,
			Out:        absOut,
			Timestamps: "chapters/set.txt",
		})
		if err != nil {
			t.Fatalf("ResolveOutputPaths override error: %v", err)
		}
		if gotOut2 != filepath.Clean(absOut) {
			t.Fatalf("gotOut2 = %q, want %q", gotOut2, filepath.Clean(absOut))
		}
		if gotTS2 != filepath.Join(outDir, "chapters", "set.txt") {
			t.Fatalf("gotTS2 = %q, want %q", gotTS2, filepath.Join(outDir, "chapters", "set.txt"))
		}

		gotOut3, gotTS3, err := ResolveOutputPaths(Config{
			OutDir:     outDir,
			Out:        "mix.mp4",
			Timestamps: absTS,
		})
		if err != nil {
			t.Fatalf("ResolveOutputPaths ts override error: %v", err)
		}
		if gotOut3 != filepath.Join(outDir, "mix.mp4") || gotTS3 != filepath.Clean(absTS) {
			t.Fatalf("got (%q, %q), want (%q, %q)", gotOut3, gotTS3, filepath.Join(outDir, "mix.mp4"), filepath.Clean(absTS))
		}
	})

	t.Run("PrepareOutputTargets creates nested parent directories and rejects directory targets", func(t *testing.T) {
		base := t.TempDir()
		nestedOutDir := filepath.Join(base, "exports", "lofi")
		resolved, err := PrepareOutputTargets(Config{
			OutDir:     nestedOutDir,
			Out:        filepath.Join("video", "mix.mp4"),
			Timestamps: filepath.Join("notes", "chapters.txt"),
		})
		if err != nil {
			t.Fatalf("PrepareOutputTargets error: %v", err)
		}
		wantOut := filepath.Join(nestedOutDir, "video", "mix.mp4")
		wantTS := filepath.Join(nestedOutDir, "notes", "chapters.txt")
		if resolved.Out != wantOut || resolved.Timestamps != wantTS {
			t.Fatalf("resolved = (%q, %q), want (%q, %q)", resolved.Out, resolved.Timestamps, wantOut, wantTS)
		}
		if info, err := os.Stat(filepath.Dir(wantOut)); err != nil || !info.IsDir() {
			t.Fatalf("expected parent dir %q to exist, err=%v", filepath.Dir(wantOut), err)
		}
		if info, err := os.Stat(filepath.Dir(wantTS)); err != nil || !info.IsDir() {
			t.Fatalf("expected parent dir %q to exist, err=%v", filepath.Dir(wantTS), err)
		}

		existingDir := filepath.Join(base, "already_dir")
		if err := os.Mkdir(existingDir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareOutputTargets(Config{Out: existingDir, Timestamps: filepath.Join(base, "ts.txt")}); err == nil {
			t.Fatal("expected error when --out points to an existing directory")
		}
		if _, err := PrepareOutputTargets(Config{Out: filepath.Join(base, "out.mp4"), Timestamps: existingDir}); err == nil {
			t.Fatal("expected error when --timestamps points to an existing directory")
		}

		regularFile := filepath.Join(base, "not_a_dir.txt")
		if err := os.WriteFile(regularFile, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareOutputTargets(Config{OutDir: regularFile}); err == nil {
			t.Fatal("expected error when --out-dir points to a regular file")
		}
	})

	t.Run("Run resolves OutDir and fails upfront before ffprobe when Out is a directory", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		outDir := filepath.Join(workDir, "nested", "release")
		var probeCalled atomic.Bool
		runner := func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffprobe" {
				probeCalled.Store(true)
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			return nil, nil
		}

		// Upfront directory target error must fail before ffprobe is ever invoked.
		badCfg := Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			Out:        workDir,
			Timestamps: filepath.Join(workDir, "ts.txt"),
		}
		if err := Run(context.Background(), badCfg, nil, okLookPath, runner, nil); err == nil {
			t.Fatal("expected upfront error for directory --out")
		}
		if probeCalled.Load() {
			t.Fatal("expected Run to fail upfront before probing tracks")
		}

		// Valid OutDir creates missing directories and writes Chapter Timestamps inside OutDir.
		goodCfg := Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			OutDir:     outDir,
			Out:        "final_mix.mp4",
			Timestamps: "chapters.txt",
		}
		var stdout bytes.Buffer
		if err := Run(context.Background(), goodCfg, NewCLITelemetry(&stdout, false), okLookPath, runner, nil); err != nil {
			t.Fatalf("Run with OutDir error: %v", err)
		}
		if _, err := os.Stat(filepath.Join(outDir, "chapters.txt")); err != nil {
			t.Fatalf("expected chapters.txt inside OutDir: %v", err)
		}
	})
}

func TestExecStreamRunner(t *testing.T) {
	if os.Getenv("YTMIX_TEST_STREAM_HELPER") == "1" {
		if os.Getenv("YTMIX_TEST_STREAM_FAIL") == "1" {
			_, _ = os.Stderr.WriteString("simulated ffmpeg error\n")
			os.Exit(1)
		}
		_, _ = os.Stdout.WriteString("out_time_us=1000000\nprogress=continue\nout_time_us=2000000\nprogress=end\n")
		os.Exit(0)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("streams stdout lines sequentially", func(t *testing.T) {
		t.Setenv("YTMIX_TEST_STREAM_HELPER", "1")
		t.Setenv("YTMIX_TEST_STREAM_FAIL", "0")
		var lines []string
		err := ExecStreamRunner(context.Background(), func(line string) {
			lines = append(lines, line)
		}, exe, "-test.run=^TestExecStreamRunner$")
		if err != nil {
			t.Fatalf("ExecStreamRunner error: %v", err)
		}
		want := []string{"out_time_us=1000000", "progress=continue", "out_time_us=2000000", "progress=end"}
		if !reflect.DeepEqual(lines, want) {
			t.Fatalf("lines = %v, want %v", lines, want)
		}
	})

	t.Run("captures stderr when subprocess exits non-zero", func(t *testing.T) {
		t.Setenv("YTMIX_TEST_STREAM_HELPER", "1")
		t.Setenv("YTMIX_TEST_STREAM_FAIL", "1")
		err := ExecStreamRunner(context.Background(), nil, exe, "-test.run=^TestExecStreamRunner$")
		if err == nil || !strings.Contains(err.Error(), "simulated ffmpeg error") {
			t.Fatalf("expected stderr in error, got: %v", err)
		}
	})
}

func writeTestPNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, G: 128, B: 64, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestTrackMediaValidationAndPreflight(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("ValidateConfig validates track-media, layout, transition, and mutual exclusion", func(t *testing.T) {
		validCases := []Config{
			{TrackMedia: "off"},
			{TrackMedia: "auto", TrackMediaLayout: "auto", TrackTransition: "cut"},
			{TrackMedia: "auto", TrackMediaLayout: "fullscreen", TrackTransition: "dip"},
			{TrackMedia: "mix.json", TrackMediaLayout: "card", TrackTransition: "dip"},
			{TrackMedia: "auto", BG: "backdrop.png"},
			{TrackMedia: "auto", Visualizer: "ember"},
			{TrackOverrides: []TrackMediaOverride{{TrackIndex: 0, MediaPath: "a.png", LayerMode: render.LayerArtCard}}},
		}
		for _, cfg := range validCases {
			if err := ValidateConfig(cfg, okLookPath); err != nil {
				t.Fatalf("expected valid config %+v, got: %v", cfg, err)
			}
		}

		invalidCases := []Config{
			{TrackMedia: "invalid"},
			{TrackMedia: "manifest.txt"},
			{TrackMedia: "auto", TrackMediaLayout: "tile"},
			{TrackMedia: "auto", TrackTransition: "wipe"},
			{TrackMedia: "auto", BG: "bg.png", Visualizer: "bars"},
			{TrackOverrides: []TrackMediaOverride{{TrackIndex: 0, LayerMode: "invalid"}}},
			{TrackOverrides: []TrackMediaOverride{{TrackIndex: 0, MediaPath: "bad.bmp"}}},
			{TrackOverrides: []TrackMediaOverride{{TrackIndex: 0, MediaKind: "audio"}}},
		}
		for _, cfg := range invalidCases {
			if err := ValidateConfig(cfg, okLookPath); err == nil {
				t.Fatalf("expected error for invalid config %+v", cfg)
			}
		}
	})

	t.Run("PreflightConfig sets Segmented and applies manifest Layer0", func(t *testing.T) {
		tmp := t.TempDir()
		bgPath := filepath.Join(tmp, "layer0.png")
		writeTestPNG(t, bgPath, 1920, 1080)

		manifestPath := filepath.Join(tmp, "manifest.json")
		if err := os.WriteFile(manifestPath, []byte(fmt.Sprintf(`{
			"layer0": {"mode": "media", "bgPath": %q},
			"layout": "card",
			"transition": "dip"
		}`, "layer0.png")), 0644); err != nil {
			t.Fatal(err)
		}

		resCfg, pathKind, err := PreflightConfig(Config{TrackMedia: manifestPath}, okLookPath, false)
		if err != nil {
			t.Fatalf("PreflightConfig manifest error: %v", err)
		}
		if !resCfg.Segmented || resCfg.BG != bgPath || pathKind != render.PathC {
			t.Fatalf("unexpected preflight result: cfg=%+v path=%q", resCfg, pathKind)
		}

		offCfg, _, err := PreflightConfig(Config{TrackMedia: "off"}, okLookPath, false)
		if err != nil {
			t.Fatalf("PreflightConfig off error: %v", err)
		}
		if offCfg.Segmented {
			t.Fatal("expected Segmented=false when --track-media off")
		}
	})
}

func TestParseTrackMediaManifest(t *testing.T) {
	t.Run("parses valid manifest, resolves relative paths, and round-trips FadeToBlack", func(t *testing.T) {
		tmp := t.TempDir()
		sub := filepath.Join(tmp, "assets")
		if err := os.Mkdir(sub, 0755); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(tmp, "mix.json")
		raw := `{
			"layer0": {
				"mode": "visualizer",
				"visualizer": "ember",
				"visualizerTheme": "oled-ember",
				"visualizerResolution": "1440p",
				"visualizerGlow": false,
				"visualizerBrightness": 55,
				"fadeToBlack": "15m"
			},
			"auto": true,
			"layout": "card",
			"transition": "dip",
			"tracks": [
				{"file": "01.mp3", "media": "assets/cover.png", "layerMode": "card"},
				{"file": "02.mp3", "mode": "backdrop"}
			]
		}`
		if err := os.WriteFile(manifestPath, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}

		m, err := ParseTrackMediaManifest(manifestPath)
		if err != nil {
			t.Fatalf("ParseTrackMediaManifest error: %v", err)
		}
		if !m.Auto || m.Layout != "card" || m.Transition != "dip" || m.Layer0 == nil {
			t.Fatalf("unexpected manifest header: %+v", m)
		}
		if m.Layer0.FadeToBlack != 15*time.Minute || m.Layer0.VisualizerGlow == nil || *m.Layer0.VisualizerGlow {
			t.Fatalf("unexpected layer0: %+v", m.Layer0)
		}
		if m.Layer0.VisualizerBrightness == nil || *m.Layer0.VisualizerBrightness != 55 {
			t.Fatalf("unexpected layer0 brightness: %+v", m.Layer0)
		}
		if len(m.Tracks) != 2 || m.Tracks[0].Media != filepath.Join(tmp, "assets", "cover.png") || m.Tracks[1].LayerMode != "backdrop" {
			t.Fatalf("unexpected manifest tracks: %+v", m.Tracks)
		}

		encoded, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("json.Marshal manifest error: %v", err)
		}
		if !strings.Contains(string(encoded), `"fadeToBlack":"15m0s"`) {
			t.Fatalf("expected fadeToBlack duration string in JSON, got: %s", string(encoded))
		}
	})

	t.Run("rejects invalid manifest payloads", func(t *testing.T) {
		tmp := t.TempDir()
		if _, err := ParseTrackMediaManifest(filepath.Join(tmp, "missing.json")); err == nil {
			t.Fatal("expected error for missing manifest")
		}
		if _, err := ParseTrackMediaManifest(tmp); err == nil {
			t.Fatal("expected error for directory manifest path")
		}

		badPayloads := []struct {
			name string
			body string
		}{
			{"invalid json", `{not-json`},
			{"bad layout", `{"layout":"grid"}`},
			{"bad transition", `{"transition":"crossfade"}`},
			{"bad layer0 mode", `{"layer0":{"mode":"neon"}}`},
			{"layer0 media missing bgPath", `{"layer0":{"mode":"media"}}`},
			{"layer0 bgPath + visualizer", `{"layer0":{"bgPath":"a.png","visualizer":"bars"}}`},
			{"layer0 bad fadeToBlack", `{"layer0":{"mode":"black","fadeToBlack":"not-a-duration"}}`},
			{"empty track file", `{"tracks":[{"file":"  ","media":"a.png"}]}`},
			{"duplicate track file", `{"tracks":[{"file":"01.mp3","media":"a.png"},{"file":"01.mp3","media":"b.png"}]}`},
			{"conflicting mode vs layerMode", `{"tracks":[{"file":"01.mp3","media":"a.png","mode":"card","layerMode":"fullscreen"}]}`},
			{"bad track media ext", `{"tracks":[{"file":"01.mp3","media":"a.bmp"}]}`},
		}
		for i, tc := range badPayloads {
			p := filepath.Join(tmp, fmt.Sprintf("bad_%d.json", i))
			if err := os.WriteFile(p, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseTrackMediaManifest(p); err == nil {
				t.Fatalf("case %q: expected error for %s", tc.name, tc.body)
			}
		}
	})
}

func TestResolveTrackSegments(t *testing.T) {
	t.Run("enforces precedence: Override/Manifest > Sidecar > ID3 attached_pic > L0 Only", func(t *testing.T) {
		workDir := t.TempDir()
		audioDir := filepath.Join(workDir, "tracks")
		tempDir := filepath.Join(workDir, "temp")
		if err := os.MkdirAll(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			t.Fatal(err)
		}

		t0 := filepath.Join(audioDir, "01 - Override.mp3")
		t1 := filepath.Join(audioDir, "02 - Sidecar.mp3")
		t2 := filepath.Join(audioDir, "03 - Embedded.mp3")
		t3 := filepath.Join(audioDir, "04 - Fallback.mp3")
		t4 := filepath.Join(audioDir, "05 - Suppressed.mp3")
		for _, p := range []string{t0, t1, t2, t3, t4} {
			if err := os.WriteFile(p, []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		// Sidecars for t0 (should be beaten by override), t1 (should win over ID3), and t4 (should be suppressed by ExplicitEmpty).
		writeTestPNG(t, filepath.Join(audioDir, "01 - Override.png"), 1920, 1080)
		writeTestPNG(t, filepath.Join(audioDir, "02 - Sidecar.png"), 1920, 1080)
		writeTestPNG(t, filepath.Join(audioDir, "05 - Suppressed.png"), 1920, 1080)

		overrideMedia := filepath.Join(workDir, "custom_square.png")
		writeTestPNG(t, overrideMedia, 800, 800)

		tracks := []Track{
			{Path: t0, FileName: "01 - Override.mp3", Duration: 10, DisplayTitle: "Override", HasEmbeddedArt: true},
			{Path: t1, FileName: "02 - Sidecar.mp3", Duration: 12, DisplayTitle: "Sidecar", HasEmbeddedArt: true},
			{Path: t2, FileName: "03 - Embedded.mp3", Duration: 8, DisplayTitle: "Embedded", HasEmbeddedArt: true, EmbeddedArtWidth: 600, EmbeddedArtHeight: 600},
			{Path: t3, FileName: "04 - Fallback.mp3", Duration: 9, DisplayTitle: "Fallback", HasEmbeddedArt: false},
			{Path: t4, FileName: "05 - Suppressed.mp3", Duration: 7, DisplayTitle: "Suppressed", HasEmbeddedArt: true},
		}

		var extractedCalls int
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" {
				extractedCalls++
				outPath := args[len(args)-1]
				writeTestPNG(t, outPath, 600, 600)
				return nil, nil
			}
			if name == "ffprobe" {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "attached_pic") {
					return []byte(`{"streams":[]}`), nil
				}
				target := args[len(args)-1]
				if target == overrideMedia {
					return []byte(`{"streams":[{"codec_type":"video","width":800,"height":800}]}`), nil
				}
				return []byte(`{"streams":[{"codec_type":"video","width":1920,"height":1080}]}`), nil
			}
			return nil, nil
		}

		cfg := Config{
			TrackMedia:    "auto",
			Crossfade:     2.0,
			OverlayTitles: true,
			TitleDuration: 4,
			FontName:      "Inter",
			DimTitles:     true,
			TrackOverrides: []TrackMediaOverride{
				{TrackFile: "01 - Override.mp3", MediaPath: overrideMedia},
				{TrackIndex: 4, ExplicitEmpty: true},
			},
		}

		layer0, segs, err := ResolveTrackSegments(context.Background(), cfg, tracks, tempDir, runner)
		if err != nil {
			t.Fatalf("ResolveTrackSegments error: %v", err)
		}
		if layer0.Mode != render.Layer0Visualizer || layer0.Visualizer.Preset != render.DefaultVisualizerPreset {
			t.Fatalf("expected default Layer0 Visualizer bars, got: %+v", layer0)
		}
		if len(segs) != 5 {
			t.Fatalf("expected 5 segments, got %d", len(segs))
		}
		if extractedCalls != 1 {
			t.Fatalf("expected exactly 1 embedded art extraction (for Track 2), got %d", extractedCalls)
		}

		// Verify precedence & auto layout results:
		if segs[0].MediaPath != overrideMedia || segs[0].LayerMode != render.LayerArtCard {
			t.Fatalf("seg[0] expected override square Art Card, got: %+v", segs[0])
		}
		if segs[1].MediaPath != filepath.Join(audioDir, "02 - Sidecar.png") || segs[1].LayerMode != render.LayerFullscreen {
			t.Fatalf("seg[1] expected widescreen sidecar Fullscreen, got: %+v", segs[1])
		}
		wantArtPath := filepath.Join(tempDir, "art_2.jpg")
		if segs[2].MediaPath != wantArtPath || segs[2].LayerMode != render.LayerArtCard {
			t.Fatalf("seg[2] expected extracted ID3 Art Card at %q, got: %+v", wantArtPath, segs[2])
		}
		if segs[3].LayerMode != render.LayerBackdrop || segs[3].MediaPath != "" {
			t.Fatalf("seg[3] expected L0 Only fallback, got: %+v", segs[3])
		}
		if segs[4].LayerMode != render.LayerBackdrop || segs[4].MediaPath != "" {
			t.Fatalf("seg[4] expected ExplicitEmpty L0 Only, got: %+v", segs[4])
		}

		// Verify crossfade-aware slot durations: [10,12,8,9,7] with crossfade 2 -> starts [0,8,18,24,31], slots [8,10,6,7,7].
		wantStarts := []float64{0, 8, 18, 24, 31}
		wantSlots := []float64{8, 10, 6, 7, 7}
		for i, seg := range segs {
			if seg.StartSec != wantStarts[i] || seg.SlotDuration != wantSlots[i] {
				t.Errorf("seg[%d] timing = (start=%v, slot=%v), want (%v, %v)", i, seg.StartSec, seg.SlotDuration, wantStarts[i], wantSlots[i])
			}
			wantASS := filepath.Join(tempDir, fmt.Sprintf("seg_%d.ass", i))
			if seg.SegmentASSPath != wantASS {
				t.Errorf("seg[%d].SegmentASSPath = %q, want %q", i, seg.SegmentASSPath, wantASS)
			}
			assBytes, err := os.ReadFile(wantASS)
			if err != nil {
				t.Fatalf("read %s: %v", wantASS, err)
			}
			if !strings.Contains(string(assBytes), "Style: Default,Inter,42,&H789EB2AF") || !strings.Contains(string(assBytes), "Dialogue: 0,0:00:00.00,") {
				t.Fatalf("unexpected segment ASS content for seg[%d]:\n%s", i, string(assBytes))
			}
		}
	})

	t.Run("auto aspect heuristic, layout overrides, Layer0 black, and unknown-track error", func(t *testing.T) {
		tmp := t.TempDir()
		mp3Path := filepath.Join(tmp, "01 - Track.mp3")
		if err := os.WriteFile(mp3Path, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}

		cases := []struct {
			name         string
			w, h         int
			layout       string
			overrideMode render.TrackLayerMode
			want         render.TrackLayerMode
		}{
			{"square 1:1 auto -> card", 1000, 1000, "auto", "", render.LayerArtCard},
			{"boundary 1.25 auto -> card", 1000, 800, "auto", "", render.LayerArtCard},
			{"4:3 1.33 auto -> fullscreen", 1280, 960, "auto", "", render.LayerFullscreen},
			{"16:9 auto -> fullscreen", 1920, 1080, "auto", "", render.LayerFullscreen},
			{"square with layout=fullscreen -> fullscreen", 1000, 1000, "fullscreen", "", render.LayerFullscreen},
			{"16:9 with layout=card -> card", 1920, 1080, "card", "", render.LayerArtCard},
			{"per-track override beats layout", 1000, 1000, "card", render.LayerFullscreen, render.LayerFullscreen},
		}
		for i, tc := range cases {
			imgPath := filepath.Join(tmp, fmt.Sprintf("img_%d.png", i))
			writeTestPNG(t, imgPath, tc.w, tc.h)

			layer0, segs, err := ResolveTrackSegments(context.Background(), Config{
				Visualizer:       "black",
				TrackMediaLayout: tc.layout,
				TrackOverrides: []TrackMediaOverride{
					{TrackIndex: 0, MediaPath: imgPath, LayerMode: tc.overrideMode},
				},
			}, []Track{{Path: mp3Path, FileName: "01 - Track.mp3", Duration: 10, DisplayTitle: "Track"}}, "", nil)
			if err != nil {
				t.Fatalf("%s: ResolveTrackSegments error: %v", tc.name, err)
			}
			if layer0.Mode != render.Layer0Black {
				t.Fatalf("%s: expected Layer0Black when Visualizer==black, got %+v", tc.name, layer0)
			}
			if segs[0].LayerMode != tc.want {
				t.Fatalf("%s: LayerMode = %q, want %q", tc.name, segs[0].LayerMode, tc.want)
			}
		}

		manifestPath := filepath.Join(tmp, "unknown.json")
		if err := os.WriteFile(manifestPath, []byte(`{"tracks":[{"file":"missing.mp3","mode":"backdrop"}]}`), 0644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveTrackSegments(context.Background(), Config{TrackMedia: manifestPath}, []Track{{Path: mp3Path, FileName: "01 - Track.mp3", Duration: 10}}, "", nil); err == nil || !strings.Contains(err.Error(), "unknown track") {
			t.Fatalf("expected unknown track error, got: %v", err)
		}
	})
}

func TestSegmentedEstimateAndRunWithTracks(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("EstimateConfig and RunWithTracks execute segmented pipeline while preserving original Track paths under Normalize", func(t *testing.T) {
		workDir := t.TempDir()
		audioDir := filepath.Join(workDir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		t1 := filepath.Join(audioDir, "01_First.mp3")
		t2 := filepath.Join(audioDir, "02_Second.mp3")
		for _, p := range []string{t1, t2} {
			if err := os.WriteFile(p, []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		sidecar1 := filepath.Join(audioDir, "01_First.png")
		writeTestPNG(t, sidecar1, 1920, 1080)

		tracks := []Track{
			{Path: t1, FileName: "01_First.mp3", Duration: 10.0, DisplayTitle: "First"},
			{Path: t2, FileName: "02_Second.mp3", Duration: 12.0, DisplayTitle: "Second", HasEmbeddedArt: true, EmbeddedArtWidth: 600, EmbeddedArtHeight: 600},
		}

		var (
			mu            sync.Mutex
			ffmpegCmds    [][]string
			extractedFrom string
			sawSegConcat  bool
			sawDipFade    bool
		)
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if name == "ffprobe" {
				target := args[len(args)-1]
				if strings.HasSuffix(target, ".wav") {
					return []byte(`{"format":{"duration":"10.000000","tags":{}}}`), nil
				}
				if target == sidecar1 {
					return []byte(`{"streams":[{"codec_type":"video","width":1920,"height":1080}]}`), nil
				}
				return []byte(`{"streams":[]}`), nil
			}

			mu.Lock()
			ffmpegCmds = append(ffmpegCmds, args)
			if strings.Contains(joined, "-frames:v 1") {
				for i, a := range args {
					if a == "-i" && i+1 < len(args) {
						extractedFrom = args[i+1]
						break
					}
				}
				writeTestPNG(t, args[len(args)-1], 600, 600)
			}
			if strings.Contains(joined, "seg_concat.txt") {
				sawSegConcat = true
			}
			if strings.Contains(joined, "fade=t=") {
				sawDipFade = true
			}
			mu.Unlock()

			if strings.Contains(joined, "-filters") {
				return []byte(" .. showfreqs A->V Freqs\n .. scale V->V Scale\n .. color |->V Color\n"), nil
			}
			if strings.Contains(joined, "h264_nvenc") {
				return nil, errors.New("no nvenc")
			}
			return nil, nil
		}

		cfg := Config{
			BG:              "",
			Out:             filepath.Join(workDir, "seg_mix.mp4"),
			Timestamps:      filepath.Join(workDir, "seg_chapters.txt"),
			Crossfade:       1.0,
			Normalize:       true,
			OverlayTitles:   true,
			TrackMedia:      "auto",
			TrackTransition: "dip",
		}

		est, err := EstimateConfig(context.Background(), cfg, tracks, nil, okLookPath, runner)
		if err != nil {
			t.Fatalf("EstimateConfig segmented error: %v", err)
		}
		if est.VideoSec <= 0 || est.AudioSec <= 0 || est.MixDurationSec != 21.0 {
			t.Fatalf("unexpected segmented estimate: %+v", est)
		}

		var events []TelemetryEvent
		if err := RunWithTracks(context.Background(), cfg, tracks, func(ev TelemetryEvent) { events = append(events, ev) }, okLookPath, runner, nil); err != nil {
			t.Fatalf("RunWithTracks segmented error: %v", err)
		}

		if extractedFrom != t2 {
			t.Fatalf("expected embedded art extraction from original MP3 %q even with Normalize=true, got %q", t2, extractedFrom)
		}
		if !sawSegConcat {
			t.Fatalf("expected seg_concat.txt concat command in ffmpeg calls: %v", ffmpegCmds)
		}
		if !sawDipFade {
			t.Fatalf("expected fade=t= dip transition in segment commands: %v", ffmpegCmds)
		}

		tsBytes, err := os.ReadFile(cfg.Timestamps)
		if err != nil {
			t.Fatalf("read timestamps: %v", err)
		}
		if string(tsBytes) != "00:00 First\n00:09 Second\n" {
			t.Fatalf("unexpected timestamps:\n%s", string(tsBytes))
		}

		lastEv := events[len(events)-1]
		if lastEv.Kind != EventDone || lastEv.Progress != 100 || !strings.Contains(lastEv.Message, "Segmented (Option A)") {
			t.Fatalf("unexpected completion telemetry: %+v", lastEv)
		}
	})

	t.Run("TrackMedia off routes identically through non-segmented Path C without segment concat", func(t *testing.T) {
		workDir, audioDir, bgPath := setupStubMixWorkspace(t, "cover.png")
		cfg := Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			Out:        filepath.Join(workDir, "compat.mp4"),
			Timestamps: filepath.Join(workDir, "compat_ts.txt"),
			TrackMedia: "off",
		}
		var sawSegConcat bool
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
			}
			if strings.Contains(strings.Join(args, " "), "seg_concat.txt") {
				sawSegConcat = true
			}
			return nil, nil
		}
		var events []TelemetryEvent
		if err := Run(context.Background(), cfg, func(ev TelemetryEvent) { events = append(events, ev) }, okLookPath, runner, nil); err != nil {
			t.Fatalf("Run TrackMedia=off error: %v", err)
		}
		if sawSegConcat {
			t.Fatal("expected TrackMedia=off not to invoke segmented concat")
		}
		if !strings.Contains(events[len(events)-1].Message, "Path C") {
			t.Fatalf("expected standard Path C completion message, got: %+v", events[len(events)-1])
		}
	})
}
