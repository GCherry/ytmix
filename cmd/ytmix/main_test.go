package main

import "testing"

func TestParseFlags(t *testing.T) {
	t.Run("parses defaults and all 10 flags", func(t *testing.T) {
		cfg, err := ParseFlags([]string{
			"--audio-dir", "my_tracks",
			"--bg", "loop.MP4",
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
		if cfg.AudioDir != "my_tracks" || cfg.BG != "loop.MP4" || cfg.Out != "mix.mp4" ||
			cfg.Timestamps != "chapters.txt" || cfg.Crossfade != 2.5 || !cfg.Normalize ||
			!cfg.OverlayTitles || cfg.TitleDuration != 10 || cfg.FontName != "Inter" || !cfg.Shuffle {
			t.Fatalf("unexpected parsed config: %+v", cfg)
		}

		defCfg, err := ParseFlags([]string{"--bg", "cover.png"})
		if err != nil {
			t.Fatalf("unexpected error for defaults: %v", err)
		}
		if defCfg.AudioDir != "./tracks" || defCfg.Out != "output.mp4" ||
			defCfg.Timestamps != "timestamps.txt" || defCfg.Crossfade != 0 ||
			defCfg.Normalize || defCfg.OverlayTitles || defCfg.TitleDuration != 0 ||
			defCfg.FontName != "Arial" || defCfg.Shuffle {
			t.Fatalf("unexpected default config: %+v", defCfg)
		}
	})
}
