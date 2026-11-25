package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
			defCfg.FadeToBlack != 0 || defCfg.DimTitles {
			t.Fatalf("unexpected default config: %+v", defCfg)
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
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSON lines on stdout, got %d:\n%s", len(lines), out.String())
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
}
