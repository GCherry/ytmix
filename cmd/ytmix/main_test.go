package main

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
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
