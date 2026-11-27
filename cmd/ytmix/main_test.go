package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/mcp"
	"github.com/GCherry/ytmix/internal/mixer"
)

func TestIsCharDevice(t *testing.T) {
	if isCharDevice(nil) {
		t.Fatal("expected isCharDevice(nil) to be false")
	}
	f, err := os.CreateTemp(t.TempDir(), "not-tty-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isCharDevice(f) {
		t.Fatal("expected regular file not to be treated as char device TTY")
	}
}

func TestParseFlags(t *testing.T) {
	t.Run("parses defaults and all flags including Path D controls", func(t *testing.T) {
		cfg, err := ParseFlags([]string{
			"--audio-dir", "my_tracks",
			"--bg", "loop.MP4",
			"--out-dir", "exports/2026",
			"--out", "mix.mp4",
			"--timestamps", "chapters.txt",
			"--crossfade", "2.5",
			"--normalize",
			"--overlay-titles",
			"--title-duration", "10",
			"--font-name", "Inter",
			"--shuffle",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AudioDir != "my_tracks" || cfg.BG != "loop.MP4" || cfg.OutDir != "exports/2026" || cfg.Out != "mix.mp4" ||
			cfg.Timestamps != "chapters.txt" || cfg.Crossfade != 2.5 || !cfg.Normalize ||
			!cfg.OverlayTitles || cfg.TitleDuration != 10 || cfg.FontName != "Inter" || !cfg.Shuffle {
			t.Fatalf("unexpected parsed config: %+v", cfg)
		}

		defCfg, err := ParseFlags(nil)
		if err != nil {
			t.Fatalf("unexpected error for omitted --bg defaults: %v", err)
		}
		if defCfg.AudioDir != "./tracks" || defCfg.BG != "" || defCfg.OutDir != "" || defCfg.Out != "output.mp4" ||
			defCfg.Timestamps != "timestamps.txt" || defCfg.Crossfade != 0 ||
			defCfg.Normalize || defCfg.OverlayTitles || defCfg.TitleDuration != 0 ||
			defCfg.FontName != "Arial" || defCfg.Shuffle ||
			defCfg.Visualizer != "" || defCfg.VisualizerTheme != "studio-mint" ||
			defCfg.VisualizerResolution != "1080p" ||
			!defCfg.VisualizerGlow || defCfg.VisualizerBrightness != 100 ||
			defCfg.FadeToBlack != 0 || defCfg.DimTitles ||
			defCfg.TrackMedia != "off" || defCfg.TrackMediaLayout != "auto" ||
			defCfg.TrackTransition != "cut" || defCfg.Segmented {
			t.Fatalf("unexpected default config: %+v", defCfg)
		}

		tmAutoCfg, err := ParseFlags([]string{
			"--track-media", "auto",
			"--track-media-layout", "card",
			"--track-transition", "dip",
		})
		if err != nil {
			t.Fatalf("ParseFlags --track-media auto error: %v", err)
		}
		if tmAutoCfg.TrackMedia != "auto" || tmAutoCfg.TrackMediaLayout != "card" || tmAutoCfg.TrackTransition != "dip" || !tmAutoCfg.Segmented {
			t.Fatalf("unexpected --track-media auto config: %+v", tmAutoCfg)
		}

		tmManifestCfg, err := ParseFlags([]string{
			"--track-media", "mix.json",
			"--track-media-layout", "fullscreen",
			"--track-transition", "cut",
		})
		if err != nil {
			t.Fatalf("ParseFlags --track-media manifest error: %v", err)
		}
		if tmManifestCfg.TrackMedia != "mix.json" || tmManifestCfg.TrackMediaLayout != "fullscreen" || tmManifestCfg.TrackTransition != "cut" || !tmManifestCfg.Segmented {
			t.Fatalf("unexpected --track-media manifest config: %+v", tmManifestCfg)
		}

		vizCfg, listViz, err := ParseCLIFlags([]string{
			"--visualizer", "ember",
			"--visualizer-theme", "oled-ember",
			"--visualizer-resolution", "1440p",
			"--visualizer-glow=false",
			"--visualizer-brightness", "45",
			"--fade-to-black", "15m",
			"--overlay-titles",
			"--dim-titles",
		})
		if err != nil || listViz {
			t.Fatalf("ParseCLIFlags Path D unexpected err=%v listViz=%v", err, listViz)
		}
		if vizCfg.Visualizer != "ember" || vizCfg.VisualizerTheme != "oled-ember" ||
			vizCfg.VisualizerResolution != "1440p" ||
			vizCfg.VisualizerGlow || vizCfg.VisualizerBrightness != 45 ||
			vizCfg.FadeToBlack != 15*time.Minute || !vizCfg.DimTitles {
			t.Fatalf("unexpected Path D config: %+v", vizCfg)
		}

		for _, badArgs := range [][]string{
			{"--visualizer-resolution", "720p"},
			{"--visualizer-brightness", "0"},
			{"--visualizer-brightness", "9"},
			{"--visualizer-brightness", "101"},
			{"--fade-to-black", "-5s"},
			{"--track-media", "invalid"},
			{"--track-media", "bad.txt"},
			{"--track-media-layout", "invalid"},
			{"--track-transition", "invalid"},
		} {
			if _, _, err := ParseCLIFlags(badArgs); err == nil {
				t.Fatalf("expected error for %v", badArgs)
			}
		}

		_, listOnly, err := ParseCLIFlags([]string{"--list-visualizers"})
		if err != nil || !listOnly {
			t.Fatalf("expected listOnly=true for --list-visualizers, got (%v, %v)", listOnly, err)
		}

		_, defOpts, err := ParseCLIOptions(nil)
		if err != nil || defOpts.Estimate || defOpts.ListVisualizers {
			t.Fatalf("unexpected default CLIOptions: %+v err=%v", defOpts, err)
		}

		estCfg, estOpts, err := ParseCLIOptions([]string{"--estimate", "--normalize", "--out-dir", "dry-run-out"})
		if err != nil || !estOpts.Estimate || estOpts.ListVisualizers || !estCfg.Normalize || estCfg.OutDir != "dry-run-out" {
			t.Fatalf("unexpected --estimate parse result: cfg=%+v opts=%+v err=%v", estCfg, estOpts, err)
		}

		for _, stray := range [][]string{{"help"}, {"--bg", "cover.png", "extra"}} {
			if _, _, err := ParseCLIOptions(stray); err == nil {
				t.Fatalf("expected positional argument error for %v", stray)
			}
		}

		var rootHelp bytes.Buffer
		rootFS := NewCLIFlagSetWithOptions(&mixer.Config{}, &CLIOptions{}, &rootHelp)
		rootFS.Usage()
		for _, want := range []string{"ytmix [flags]", "ytmix ui [flags]", "ytmix mcp", "ytmix help", "-audio-dir", "-out-dir"} {
			if !strings.Contains(rootHelp.String(), want) {
				t.Fatalf("root usage missing %q:\n%s", want, rootHelp.String())
			}
		}
	})
}

func TestRunEstimate(t *testing.T) {
	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("Path A dry-run prints structured breakdown and never creates out-dir or runs ffmpeg render", func(t *testing.T) {
		workDir := t.TempDir()
		audioDir := filepath.Join(workDir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"01 - Alpha.mp3", "02 - Beta.mp3"} {
			if err := os.WriteFile(filepath.Join(audioDir, name), []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		bgPath := filepath.Join(workDir, "loop.mp4")
		if err := os.WriteFile(bgPath, []byte("mp4"), 0644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(workDir, "uncreated-out", "nested")

		cfg := mixer.Config{
			AudioDir:   audioDir,
			BG:         bgPath,
			OutDir:     outDir,
			Out:        "mix.mp4",
			Timestamps: "chapters.txt",
			Crossfade:  2.0,
		}

		ffmpegRenderCalled := false
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				target := args[len(args)-1]
				if target == bgPath {
					return []byte(`{"format":{"duration":"12.0"},"streams":[{"codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"30/1"}]}`), nil
				}
				return []byte(`{"format":{"duration":"180.0"},"streams":[]}`), nil
			}
			ffmpegRenderCalled = true
			return nil, errors.New("unexpected ffmpeg invocation during --estimate")
		}

		var out bytes.Buffer
		if err := RunEstimate(context.Background(), cfg, &out, okLookPath, runner); err != nil {
			t.Fatalf("RunEstimate Path A error: %v", err)
		}
		if ffmpegRenderCalled {
			t.Fatal("expected ffmpeg never to be invoked during Path A --estimate")
		}
		if _, err := os.Stat(filepath.Join(workDir, "uncreated-out")); !os.IsNotExist(err) {
			t.Fatalf("expected --out-dir not to be created on --estimate dry-run, stat err=%v", err)
		}

		text := out.String()
		for _, want := range []string{
			"Tracks:",
			"2, Mix duration 5m 58s",
			"Background Visual:",
			"loop.mp4",
			"12.0s loop",
			"30 fps",
			"30 cycles",
			"Path A (Stream Copy Loop)",
			"Encoder:",
			"Audio Stage:",
			"Video Stage:",
			"Estimated Time:",
			"Calibration:",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("RunEstimate Path A output missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("Path D dry-run validates filters, detects encoder, prints breakdown, and leaves out-dir uncreated", func(t *testing.T) {
		workDir := t.TempDir()
		audioDir := filepath.Join(workDir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(audioDir, "01 - Night.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(workDir, "never-created")

		cfg := mixer.Config{
			AudioDir:             audioDir,
			OutDir:               outDir,
			Out:                  "mix.mp4",
			Timestamps:           "timestamps.txt",
			Normalize:            true,
			Visualizer:           "ember",
			VisualizerTheme:      "oled-ember",
			VisualizerResolution: "1440p",
			VisualizerGlow:       true,
			VisualizerBrightness: 80,
		}

		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"120.0"},"streams":[]}`), nil
			}
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "-filters") {
				return []byte(" .. showwaves A->V Wave\n TS gblur V->V Blur\n .. scale V->V Scale\n .. color |->V Color\n"), nil
			}
			if strings.Contains(joined, "h264_nvenc") {
				return nil, nil
			}
			return nil, errors.New("unexpected ffmpeg command: " + joined)
		}

		var out bytes.Buffer
		if err := RunEstimate(context.Background(), cfg, &out, okLookPath, runner); err != nil {
			t.Fatalf("RunEstimate Path D error: %v", err)
		}
		if _, err := os.Stat(outDir); !os.IsNotExist(err) {
			t.Fatalf("expected --out-dir not to be created on Path D --estimate, stat err=%v", err)
		}

		text := out.String()
		for _, want := range []string{
			"1, Mix duration 2m 00s",
			"Loudness Normalization on",
			"Auto Visualizer (ember · oled-ember · 1440p)",
			"Path D (Auto Visualizer)",
			"Audio Stage:",
			"normalize ~",
			"Video Stage:",
			"Estimated Time:",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("RunEstimate Path D output missing %q:\n%s", want, text)
			}
		}
	})

	t.Run("Path D dry-run fails upfront before ffprobe when required visualizer filter is missing", func(t *testing.T) {
		workDir := t.TempDir()
		audioDir := filepath.Join(workDir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(audioDir, "01 - Track.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}

		probed := false
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				probed = true
				return []byte(`{"format":{"duration":"10.0"}}`), nil
			}
			if strings.Contains(strings.Join(args, " "), "-filters") {
				return []byte(" .. showfreqs A->V Freqs\n"), nil
			}
			return nil, nil
		}

		var out bytes.Buffer
		err := RunEstimate(context.Background(), mixer.Config{
			AudioDir:   audioDir,
			OutDir:     filepath.Join(workDir, "missing"),
			Visualizer: "3dscope",
		}, &out, okLookPath, runner)
		if err == nil || !strings.Contains(err.Error(), "a3dscope") {
			t.Fatalf("expected missing a3dscope error, got: %v", err)
		}
		if probed {
			t.Fatal("expected missing filter preflight to fail before probing tracks")
		}
	})

	t.Run("segmented --track-media auto dry-run probes embedded art and prints segmented estimate without creating out-dir", func(t *testing.T) {
		workDir := t.TempDir()
		audioDir := filepath.Join(workDir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(audioDir, "01 - Cover.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(audioDir, "02 - Sidecar.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(audioDir, "02 - Sidecar.png"), []byte("png"), 0644); err != nil {
			t.Fatal(err)
		}
		outDir := filepath.Join(workDir, "never-created-seg")

		var sawAttachedPicProbe atomic.Bool
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			if name == "ffprobe" {
				if strings.Contains(joined, "attached_pic") {
					sawAttachedPicProbe.Store(true)
				}
				target := args[len(args)-1]
				if strings.HasSuffix(target, "01 - Cover.mp3") {
					return []byte(`{"format":{"duration":"90.0"},"streams":[{"index":1,"codec_type":"video","codec_name":"mjpeg","width":600,"height":600,"disposition":{"attached_pic":1}}]}`), nil
				}
				if strings.HasSuffix(target, "02 - Sidecar.png") {
					return []byte(`{"streams":[{"codec_type":"video","width":1920,"height":1080}]}`), nil
				}
				return []byte(`{"format":{"duration":"90.0"},"streams":[]}`), nil
			}
			if strings.Contains(joined, "-filters") {
				return []byte(" .. showfreqs A->V Freqs\n .. scale V->V Scale\n .. color |->V Color\n"), nil
			}
			if strings.Contains(joined, "h264_nvenc") {
				return nil, errors.New("no nvenc")
			}
			return nil, errors.New("unexpected ffmpeg command: " + joined)
		}

		var out bytes.Buffer
		err := RunEstimate(context.Background(), mixer.Config{
			AudioDir:   audioDir,
			OutDir:     outDir,
			TrackMedia: "auto",
		}, &out, okLookPath, runner)
		if err != nil {
			t.Fatalf("RunEstimate segmented error: %v", err)
		}
		if !sawAttachedPicProbe.Load() {
			t.Fatal("expected segmented RunEstimate to probe tracks with attached_pic")
		}
		if _, err := os.Stat(outDir); !os.IsNotExist(err) {
			t.Fatalf("expected --out-dir not to be created on segmented --estimate, err=%v", err)
		}
		text := out.String()
		for _, want := range []string{
			"2, Mix duration 3m 00s",
			"Segmented Per-Track (Option A)",
			"Auto Visualizer (bars · studio-mint · 1080p)",
			"Audio Stage:",
			"Video Stage:",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("segmented RunEstimate output missing %q:\n%s", want, text)
			}
		}
	})
}

func TestPrintVisualizers(t *testing.T) {
	var out bytes.Buffer
	err := PrintVisualizers(
		context.Background(),
		&out,
		func(name string) (string, error) { return "/usr/bin/" + name, nil },
		func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(" .. color |->V c\n .. showwaves A->V w\n TS gblur V->V b\n .. scale V->V s\n .. pad V->V p\n .. showfreqs A->V f\n"), nil
		},
	)
	if err != nil {
		t.Fatalf("PrintVisualizers error: %v", err)
	}
	text := out.String()
	for _, want := range []string{"black", "ember", "bars", "3dscope", "studio-mint", "oled-ember", "1080p", "1440p", "4k", "ready", "unavailable (missing a3dscope)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("PrintVisualizers missing %q:\n%s", want, text)
		}
	}
}

func TestParseUIFlags(t *testing.T) {
	t.Run("detects ui subcommand and parses defaults and overrides", func(t *testing.T) {
		if !IsUISubcommand([]string{"ui"}) || !IsUISubcommand([]string{"ui", "--port", "9090"}) {
			t.Fatal("expected IsUISubcommand to return true for ui subcommand")
		}
		if IsUISubcommand(nil) || IsUISubcommand([]string{"--bg", "cover.png"}) {
			t.Fatal("expected IsUISubcommand to return false for root Mix CLI args")
		}

		defCfg, err := ParseUIFlags(nil)
		if err != nil {
			t.Fatalf("unexpected default ui flags error: %v", err)
		}
		if defCfg.Host != "127.0.0.1" || defCfg.Port != 8080 || defCfg.NoOpen || defCfg.AudioDir != "./tracks" ||
			defCfg.BG != "" || defCfg.OutDir != "" || defCfg.Out != "output.mp4" ||
			defCfg.Timestamps != "timestamps.txt" || len(defCfg.AllowDirs) != 0 {
			t.Fatalf("unexpected default ui config: %+v", defCfg)
		}

		customCfg, err := ParseUIFlags([]string{
			"--host", "0.0.0.0",
			"--port", "9091",
			"--no-open",
			"--audio-dir", "./custom_tracks",
			"--bg", "loop.mp4",
			"--out-dir", "./renders",
			"--out", "custom.mp4",
			"--timestamps", "custom_chapters.txt",
			"--allow-dir", "/mnt/media",
			"--allow-dir", "/mnt/exports",
		})
		if err != nil {
			t.Fatalf("unexpected custom ui flags error: %v", err)
		}
		if customCfg.Host != "0.0.0.0" || customCfg.Port != 9091 || !customCfg.NoOpen ||
			customCfg.AudioDir != "./custom_tracks" || customCfg.BG != "loop.mp4" ||
			customCfg.OutDir != "./renders" || customCfg.Out != "custom.mp4" ||
			customCfg.Timestamps != "custom_chapters.txt" ||
			!reflect.DeepEqual(customCfg.AllowDirs, []string{"/mnt/media", "/mnt/exports"}) {
			t.Fatalf("unexpected custom ui config: %+v", customCfg)
		}
	})

	t.Run("rejects invalid port and empty host", func(t *testing.T) {
		if _, err := ParseUIFlags([]string{"--port", "-1"}); err == nil {
			t.Fatal("expected error for negative port")
		}
		if _, err := ParseUIFlags([]string{"--port", "70000"}); err == nil {
			t.Fatal("expected error for port > 65535")
		}
		if _, err := ParseUIFlags([]string{"--host", "   "}); err == nil {
			t.Fatal("expected error for empty host")
		}
		if _, err := ParseUIFlags([]string{"extra"}); err == nil {
			t.Fatal("expected error for unexpected positional argument")
		}
		var uiHelp bytes.Buffer
		uiFS := NewUIFlagSet(&UIConfig{}, &uiHelp)
		uiFS.Usage()
		for _, want := range []string{"Usage: ytmix ui [flags]", "-out-dir", "-host"} {
			if !strings.Contains(uiHelp.String(), want) {
				t.Fatalf("ui usage missing %q:\n%s", want, uiHelp.String())
			}
		}
	})
}

func TestParseMCPFlags(t *testing.T) {
	if !IsMCPSubcommand([]string{"mcp"}) || !IsMCPSubcommand([]string{"mcp", "--help"}) {
		t.Fatal("expected IsMCPSubcommand to return true for mcp subcommand")
	}
	if IsMCPSubcommand(nil) || IsMCPSubcommand([]string{"ui"}) || IsMCPSubcommand([]string{"--bg", "cover.png"}) {
		t.Fatal("expected IsMCPSubcommand to return false for non-mcp args")
	}

	if err := ParseMCPFlags(nil); err != nil {
		t.Fatalf("unexpected ParseMCPFlags(nil) error: %v", err)
	}
	if err := ParseMCPFlags([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp for --help, got: %v", err)
	}
	if err := ParseMCPFlags([]string{"--unknown"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if err := ParseMCPFlags([]string{"extra"}); err == nil {
		t.Fatal("expected error for unexpected positional argument")
	}

	var helpOut bytes.Buffer
	fs := NewMCPFlagSet(&helpOut)
	fs.Usage()
	if !strings.Contains(helpOut.String(), "ytmix mcp") {
		t.Fatalf("expected mcp usage text, got: %q", helpOut.String())
	}
}

func TestRunMCPServer(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_capabilities","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"prompts/list"}`,
	}, "\n") + "\n"

	var out, errOut bytes.Buffer
	err := runMCPServer(
		context.Background(),
		strings.NewReader(input),
		&out,
		&errOut,
		func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
		func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "-filters") {
				return []byte(" .. color |->V c\n .. showfreqs A->V f\n TS gblur V->V b\n"), nil
			}
			return nil, errors.New("no hwaccel")
		},
		"linux",
	)
	if err != nil {
		t.Fatalf("runMCPServer error: %v", err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected zero stderr bytes, got %q", errOut.String())
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 JSON lines on stdout, got %d:\n%s", len(lines), out.String())
	}
	for i, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatalf("stdout line %d is not valid JSON: %q", i, line)
		}
	}
	for _, toolName := range []string{
		"get_capabilities",
		"inspect_background",
		"inspect_tracks",
		"plan_mix",
		"preview_visualizer_frame",
		"start_render",
		"get_render_status",
		"cancel_render",
		"verify_mix",
	} {
		if !strings.Contains(lines[1], `"`+toolName+`"`) {
			t.Fatalf("tools/list missing %s: %s", toolName, lines[1])
		}
	}
	if !strings.Contains(lines[2], `"libx264"`) || !strings.Contains(lines[2], `"studio-mint"`) {
		t.Fatalf("get_capabilities response missing expected fields: %s", lines[2])
	}
	for _, uri := range []string{
		"ytmix://presets",
		"ytmix://themes",
		"ytmix://resolutions",
	} {
		if !strings.Contains(lines[3], `"`+uri+`"`) {
			t.Fatalf("resources/list missing %s: %s", uri, lines[3])
		}
	}
	for _, promptName := range []string{
		"create-sleep-mix",
		"clean-youtube-release",
	} {
		if !strings.Contains(lines[4], `"`+promptName+`"`) {
			t.Fatalf("prompts/list missing %s: %s", promptName, lines[4])
		}
	}
}

func TestRunCLI(t *testing.T) {
	t.Run("help flags and help subcommand route cleanly", func(t *testing.T) {
		cases := []struct {
			name     string
			args     []string
			wantCode int
			wantOut  []string
			wantErr  []string
			emptyOut bool
			emptyErr bool
		}{
			{
				name:     "root --help prints unified usage to stderr",
				args:     []string{"--help"},
				wantCode: 0,
				wantErr:  []string{"ytmix [flags]", "ytmix ui [flags]", "ytmix mcp", "ytmix help [command]", "-out-dir"},
				emptyOut: true,
			},
			{
				name:     "root -h prints unified usage to stderr",
				args:     []string{"-h"},
				wantCode: 0,
				wantErr:  []string{"ytmix ui [flags]", "ytmix mcp"},
				emptyOut: true,
			},
			{
				name:     "help subcommand prints unified usage to stdout",
				args:     []string{"help"},
				wantCode: 0,
				wantOut:  []string{"ytmix [flags]", "ytmix ui [flags]", "ytmix mcp", "ytmix help [command]"},
				emptyErr: true,
			},
			{
				name:     "help --help prints unified usage to stdout",
				args:     []string{"help", "--help"},
				wantCode: 0,
				wantOut:  []string{"ytmix [flags]", "ytmix ui [flags]"},
				emptyErr: true,
			},
			{
				name:     "help ui prints ui usage to stdout",
				args:     []string{"help", "ui"},
				wantCode: 0,
				wantOut:  []string{"Usage: ytmix ui [flags]", "-host", "-out-dir"},
				emptyErr: true,
			},
			{
				name:     "help mcp prints mcp usage to stdout",
				args:     []string{"help", "mcp"},
				wantCode: 0,
				wantOut:  []string{"Usage: ytmix mcp"},
				emptyErr: true,
			},
			{
				name:     "ui --help prints ui usage to stderr",
				args:     []string{"ui", "--help"},
				wantCode: 0,
				wantErr:  []string{"Usage: ytmix ui [flags]", "-port"},
				emptyOut: true,
			},
			{
				name:     "mcp --help prints mcp usage to stderr",
				args:     []string{"mcp", "--help"},
				wantCode: 0,
				wantErr:  []string{"Usage: ytmix mcp"},
				emptyOut: true,
			},
			{
				name:     "help unknown topic exits 2",
				args:     []string{"help", "unknown"},
				wantCode: 2,
				wantErr:  []string{`unknown help topic "unknown"`, "ytmix ui [flags]"},
				emptyOut: true,
			},
			{
				name:     "help extra args exits 2",
				args:     []string{"help", "ui", "extra"},
				wantCode: 2,
				wantErr:  []string{"unexpected arguments: extra"},
				emptyOut: true,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var out, errOut bytes.Buffer
				code := run(context.Background(), tc.args, cliDeps{
					stdout: &out,
					stderr: &errOut,
				})
				if code != tc.wantCode {
					t.Fatalf("exit code = %d, want %d (stdout=%q stderr=%q)", code, tc.wantCode, out.String(), errOut.String())
				}
				if tc.emptyOut && out.Len() != 0 {
					t.Fatalf("expected empty stdout, got %q", out.String())
				}
				if tc.emptyErr && errOut.Len() != 0 {
					t.Fatalf("expected empty stderr, got %q", errOut.String())
				}
				for _, w := range tc.wantOut {
					if !strings.Contains(out.String(), w) {
						t.Fatalf("stdout missing %q:\n%s", w, out.String())
					}
				}
				for _, w := range tc.wantErr {
					if !strings.Contains(errOut.String(), w) {
						t.Fatalf("stderr missing %q:\n%s", w, errOut.String())
					}
				}
			})
		}
	})

	t.Run("flag and positional argument errors exit 2", func(t *testing.T) {
		for _, args := range [][]string{
			{"--unknown-flag"},
			{"stray"},
			{"--bg", "cover.png", "stray"},
			{"ui", "--unknown-flag"},
			{"ui", "stray"},
			{"mcp", "--unknown-flag"},
			{"mcp", "stray"},
		} {
			var out, errOut bytes.Buffer
			code := run(context.Background(), args, cliDeps{
				stdout: &out,
				stderr: &errOut,
			})
			if code != 2 {
				t.Fatalf("args %v: code = %d, want 2 (stderr=%q)", args, code, errOut.String())
			}
			if !strings.Contains(errOut.String(), "Error:") {
				t.Fatalf("args %v: expected Error prefix on stderr, got %q", args, errOut.String())
			}
		}
	})

	t.Run("dispatches mix, list-visualizers, estimate, ui, and mcp", func(t *testing.T) {
		// Mix success + backwards compatible flags
		var gotMixCfg mixer.Config
		var out, errOut bytes.Buffer
		code := run(context.Background(), []string{
			"--audio-dir", "albums/jazz",
			"--bg", "cover.png",
			"--out-dir", "dist",
			"--out", "jazz.mp4",
			"--timestamps", "jazz.txt",
		}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runMix: func(_ context.Context, cfg mixer.Config, _ mixer.TelemetrySink, _ func(string) (string, error), _ mixer.CommandRunner, _ mixer.StreamRunner) error {
				gotMixCfg = cfg
				return nil
			},
		})
		if code != 0 || gotMixCfg.AudioDir != "albums/jazz" || gotMixCfg.BG != "cover.png" || gotMixCfg.OutDir != "dist" {
			t.Fatalf("mix dispatch failed: code=%d cfg=%+v stderr=%q", code, gotMixCfg, errOut.String())
		}

		// Mix error with TTY prints trailing newline to stdout
		out.Reset()
		errOut.Reset()
		code = run(context.Background(), nil, cliDeps{
			stdout: &out,
			stderr: &errOut,
			isTTY:  true,
			runMix: func(context.Context, mixer.Config, mixer.TelemetrySink, func(string) (string, error), mixer.CommandRunner, mixer.StreamRunner) error {
				return errors.New("mix boom")
			},
		})
		if code != 1 || out.String() != "\n" || !strings.Contains(errOut.String(), "mix boom") {
			t.Fatalf("mix TTY error unexpected: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
		}

		// --list-visualizers success and error
		out.Reset()
		errOut.Reset()
		code = run(context.Background(), []string{"--list-visualizers"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			lookPath: func(string) (string, error) {
				return "", errors.New("no ffmpeg")
			},
		})
		if code != 0 || !strings.Contains(out.String(), "Path D Auto Visualizer Presets") {
			t.Fatalf("--list-visualizers failed: code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
		}

		errOut.Reset()
		code = run(context.Background(), []string{"--list-visualizers"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			printVisualizers: func(context.Context, io.Writer, func(string) (string, error), mixer.CommandRunner) error {
				return errors.New("write failed")
			},
		})
		if code != 1 || !strings.Contains(errOut.String(), "write failed") {
			t.Fatalf("--list-visualizers error path unexpected: code=%d stderr=%q", code, errOut.String())
		}

		// --estimate dispatch (success & error)
		out.Reset()
		errOut.Reset()
		var estCfg mixer.Config
		code = run(context.Background(), []string{"--estimate", "--out-dir", "preview"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runEstimate: func(_ context.Context, cfg mixer.Config, w io.Writer, _ func(string) (string, error), _ mixer.CommandRunner) error {
				estCfg = cfg
				_, err := io.WriteString(w, "estimate ok\n")
				return err
			},
		})
		if code != 0 || estCfg.OutDir != "preview" || !strings.Contains(out.String(), "estimate ok") {
			t.Fatalf("--estimate dispatch failed: code=%d cfg=%+v stdout=%q", code, estCfg, out.String())
		}

		errOut.Reset()
		code = run(context.Background(), []string{"--estimate"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runEstimate: func(context.Context, mixer.Config, io.Writer, func(string) (string, error), mixer.CommandRunner) error {
				return errors.New("estimate boom")
			},
		})
		if code != 1 || !strings.Contains(errOut.String(), "estimate boom") {
			t.Fatalf("--estimate error unexpected: code=%d stderr=%q", code, errOut.String())
		}

		// UI dispatch (success & error)
		var gotUICfg UIConfig
		out.Reset()
		errOut.Reset()
		code = run(context.Background(), []string{"ui", "--host", "127.0.0.1", "--port", "9191", "--no-open", "--out-dir", "exports"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runUI: func(_ context.Context, cfg UIConfig, _ io.Writer) error {
				gotUICfg = cfg
				return nil
			},
		})
		if code != 0 || gotUICfg.Port != 9191 || !gotUICfg.NoOpen || gotUICfg.OutDir != "exports" {
			t.Fatalf("ui dispatch failed: code=%d cfg=%+v stderr=%q", code, gotUICfg, errOut.String())
		}

		errOut.Reset()
		code = run(context.Background(), []string{"ui"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runUI: func(context.Context, UIConfig, io.Writer) error {
				return errors.New("ui listen boom")
			},
		})
		if code != 1 || !strings.Contains(errOut.String(), "ui listen boom") {
			t.Fatalf("ui error unexpected: code=%d stderr=%q", code, errOut.String())
		}

		// MCP dispatch (success, context.Canceled, error)
		errOut.Reset()
		code = run(context.Background(), []string{"mcp"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runMCP: func(context.Context, io.Reader, io.Writer, io.Writer, func(string) (string, error), mcp.CommandRunner, string) error {
				return context.Canceled
			},
		})
		if code != 0 || errOut.Len() != 0 {
			t.Fatalf("mcp cancel unexpected: code=%d stderr=%q", code, errOut.String())
		}

		errOut.Reset()
		code = run(context.Background(), []string{"mcp"}, cliDeps{
			stdout: &out,
			stderr: &errOut,
			runMCP: func(context.Context, io.Reader, io.Writer, io.Writer, func(string) (string, error), mcp.CommandRunner, string) error {
				return errors.New("mcp boom")
			},
		})
		if code != 1 || !strings.Contains(errOut.String(), "mcp boom") {
			t.Fatalf("mcp error unexpected: code=%d stderr=%q", code, errOut.String())
		}
	})

	t.Run("NewFlagSet and NewCLIFlagSet remain backwards compatible", func(t *testing.T) {
		var cfg mixer.Config
		var out bytes.Buffer
		fs := NewFlagSet(&cfg, &out)
		if err := fs.Parse([]string{"--bg", "art.png"}); err != nil {
			t.Fatalf("NewFlagSet parse error: %v", err)
		}
		if cfg.BG != "art.png" {
			t.Fatalf("expected cfg.BG=art.png, got %q", cfg.BG)
		}
		openBrowserBestEffort(context.Background(), "ftp://not-http")
	})
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestRunUIServerLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- runUIServer(ctx, UIConfig{
			Host:       "127.0.0.1",
			Port:       0,
			NoOpen:     true,
			AudioDir:   t.TempDir(),
			OutDir:     t.TempDir(),
			Out:        "output.mp4",
			Timestamps: "timestamps.txt",
		}, &out)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), "Editorial Mix Studio listening on http://127.0.0.1:") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("runUIServer returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Editorial Mix Studio listening on http://127.0.0.1:") {
		t.Fatalf("expected listening banner, got %q", out.String())
	}
}
