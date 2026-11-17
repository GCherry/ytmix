package main

import (
	"reflect"
	"testing"
)

func TestParseFlags(t *testing.T) {
	t.Run("parses defaults and all 11 flags", func(t *testing.T) {
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

		defCfg, err := ParseFlags([]string{"--bg", "cover.png"})
		if err != nil {
			t.Fatalf("unexpected error for defaults: %v", err)
		}
		if defCfg.AudioDir != "./tracks" || defCfg.OutDir != "" || defCfg.Out != "output.mp4" ||
			defCfg.Timestamps != "timestamps.txt" || defCfg.Crossfade != 0 ||
			defCfg.Normalize || defCfg.OverlayTitles || defCfg.TitleDuration != 0 ||
			defCfg.FontName != "Arial" || defCfg.Shuffle {
			t.Fatalf("unexpected default config: %+v", defCfg)
		}
	})
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
