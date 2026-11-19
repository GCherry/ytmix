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

	"github.com/GCherry/ytmix/internal/render"
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
		for _, step := range []string{"[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]", "Path A"} {
			if !strings.Contains(logOut, step) {
				t.Errorf("expected %q in telemetry output, got:\n%s", step, logOut)
			}
		}

		wantStepProgress := []int{5, 10, 15, 20, 20, 100}
		if len(events) != len(wantStepProgress) {
			t.Fatalf("expected %d events, got %d: %+v", len(wantStepProgress), len(events), events)
		}
		for i, wantPct := range wantStepProgress {
			if events[i].Progress != wantPct {
				t.Errorf("event[%d].Progress = %d, want %d (%+v)", i, events[i].Progress, wantPct, events[i])
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

		var progressVals []int
		for _, ev := range events {
			if ev.Kind == EventProgress {
				progressVals = append(progressVals, ev.Progress)
			}
		}
		if !reflect.DeepEqual(progressVals, []int{60, 99}) {
			t.Fatalf("Step 5 EventProgress = %v, want [60 99] (events: %+v)", progressVals, events)
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
}

func TestCLITelemetryFormatting(t *testing.T) {
	t.Run("non-TTY suppresses EventProgress and prints milestones cleanly", func(t *testing.T) {
		var buf bytes.Buffer
		sink := NewCLITelemetry(&buf, false)
		sink(TelemetryEvent{Kind: EventStep, Step: 5, TotalSteps: 5, Progress: 20, Message: "[5/5] Rendering Mix video..."})
		sink(TelemetryEvent{Kind: EventProgress, Step: 5, TotalSteps: 5, Progress: 55, Speed: "2.4x"})
		sink(TelemetryEvent{Kind: EventDone, Step: 5, TotalSteps: 5, Progress: 100, Message: "Done! Rendered out.mp4."})

		got := buf.String()
		if strings.Contains(got, "\r") || strings.Contains(got, "55%") {
			t.Fatalf("non-TTY should not emit \\r or progress spam, got: %q", got)
		}
		want := "[5/5] Rendering Mix video...\nDone! Rendered out.mp4.\n"
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
		probeCalled := false
		runner := func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffprobe" {
				probeCalled = true
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
		if probeCalled {
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
