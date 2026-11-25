package render

import (
	"context"
	"errors"
	"math"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVisualizerCatalogAndValidation(t *testing.T) {
	t.Run("ListVisualizers returns all 14 presets cleanly and prevents shared mutation", func(t *testing.T) {
		list := ListVisualizers()
		if len(list) != 14 {
			t.Fatalf("expected 14 visualizers, got %d", len(list))
		}
		darkCount := 0
		seen := make(map[string]bool, len(list))
		for _, v := range list {
			if seen[v.ID] {
				t.Fatalf("duplicate preset id %q", v.ID)
			}
			seen[v.ID] = true
			if v.IsDark {
				darkCount++
			}
			if _, ok := LookupVisualizer(strings.ToUpper(" " + v.ID + " ")); !ok {
				t.Fatalf("LookupVisualizer failed for %q", v.ID)
			}
		}
		if darkCount != 5 {
			t.Fatalf("expected 5 Dark/Sleep presets, got %d", darkCount)
		}

		list[0].Required[0] = "mutated"
		fresh, ok := LookupVisualizer(list[0].ID)
		if !ok || fresh.Required[0] == "mutated" {
			t.Fatalf("ListVisualizers exposed internal Required slice: %+v", fresh)
		}
	})

	t.Run("ListThemes returns all 7 themes cleanly", func(t *testing.T) {
		themes := ListThemes()
		if len(themes) != 7 {
			t.Fatalf("expected 7 themes, got %d", len(themes))
		}
		seen := make(map[string]bool, len(themes))
		for _, th := range themes {
			if seen[th.ID] {
				t.Fatalf("duplicate theme id %q", th.ID)
			}
			seen[th.ID] = true
			if _, ok := LookupTheme(strings.ToUpper(th.ID)); !ok {
				t.Fatalf("LookupTheme failed for %q", th.ID)
			}
		}
	})

	t.Run("ListResolutions returns 1080p, 1440p, 4k cleanly", func(t *testing.T) {
		resList := ListResolutions()
		if len(resList) != 3 {
			t.Fatalf("expected 3 resolutions, got %d", len(resList))
		}
		for _, tc := range []struct {
			in    string
			want  string
			wantW int
			wantH int
		}{
			{"1080p", "1080p", 1920, 1080},
			{" 1440P ", "1440p", 2560, 1440},
			{"4K", "4k", 3840, 2160},
		} {
			got, ok := LookupResolution(tc.in)
			if !ok || got.ID != tc.want || got.Width != tc.wantW || got.Height != tc.wantH {
				t.Fatalf("LookupResolution(%q) = (%+v, %v)", tc.in, got, ok)
			}
		}
		if _, ok := LookupResolution("720p"); ok {
			t.Fatal("expected unsupported resolution 720p to return false")
		}
	})

	t.Run("ValidateVisualizerConfig defaults, accepts all valid presets/themes/resolutions, and rejects invalid inputs", func(t *testing.T) {
		defNorm, err := ValidateVisualizerConfig(VisualizerConfig{})
		if err != nil {
			t.Fatalf("ValidateVisualizerConfig(empty) error: %v", err)
		}
		if defNorm.Preset != "bars" || defNorm.Theme != "studio-mint" || defNorm.Resolution != "1080p" || defNorm.Brightness != 100 {
			t.Fatalf("unexpected normalized defaults: %+v", defNorm)
		}

		for _, v := range ListVisualizers() {
			for _, th := range ListThemes() {
				norm, err := ValidateVisualizerConfig(VisualizerConfig{
					Preset:     v.ID,
					Theme:      th.ID,
					Resolution: "1440P",
					Glow:       true,
					Brightness: 75,
				})
				if err != nil {
					t.Fatalf("preset=%q theme=%q failed validation: %v", v.ID, th.ID, err)
				}
				if norm.Preset != v.ID || norm.Theme != th.ID || norm.Resolution != "1440p" || norm.Brightness != 75 {
					t.Fatalf("unexpected normalized config: %+v", norm)
				}
			}
		}

		for _, bad := range []struct {
			name string
			cfg  VisualizerConfig
			want string
		}{
			{"unknown preset", VisualizerConfig{Preset: "lasers"}, "--visualizer"},
			{"unknown theme", VisualizerConfig{Theme: "rainbow-neon"}, "--visualizer-theme"},
			{"unknown resolution", VisualizerConfig{Resolution: "720p"}, "--visualizer-resolution"},
			{"brightness 9 below min", VisualizerConfig{Brightness: 9}, "--visualizer-brightness"},
			{"brightness 101 above max", VisualizerConfig{Brightness: 101}, "--visualizer-brightness"},
			{"brightness negative", VisualizerConfig{Brightness: -5}, "--visualizer-brightness"},
			{"negative fade-to-black", VisualizerConfig{FadeToBlack: -1 * time.Second}, "--fade-to-black"},
		} {
			t.Run(bad.name, func(t *testing.T) {
				_, err := ValidateVisualizerConfig(bad.cfg)
				if err == nil || !strings.Contains(err.Error(), bad.want) {
					t.Fatalf("expected error containing %q, got: %v", bad.want, err)
				}
			})
		}

		for _, validB := range []int{10, 45, 100} {
			norm, err := ValidateVisualizerConfig(VisualizerConfig{Brightness: validB})
			if err != nil || norm.Brightness != validB {
				t.Fatalf("Brightness=%d unexpected: norm=%+v err=%v", validB, norm, err)
			}
		}
	})
}

func TestDetectFiltersAndAvailability(t *testing.T) {
	sample := []byte(`Filters:
  T.. = Timeline support
  .S. = Slice threading
  ------
 .. anullsink         A->|       Do nothing
 TS blend             VV->V      Blend two video frames
 TS colorchannelmixer V->V       Adjust color channels
 .. color             |->V       Provide constant color
 TS fade              V->V       Fade in/out
 TS gblur             V->V       Gaussian blur
 .. pad               V->V       Pad input
 .. scale             V->V       Scale input
 .. a3dscope          A->V       3d scope
 .. ahistogram        A->V       Histogram
 .. aphasemeter       A->N       Phase meter
 .S avectorscope      A->V       Vectorscope
 .. showcqt           A->V       CQT spectrum
 .. showfreqs         A->V       Frequency bars
 .S showspectrum      A->V       Spectrum waterfall
 .. showvolume        A->V       Volume bars
 .. showwaves         A->V       Waveform
`)
	parsed := ParseFiltersOutput(sample)
	for _, want := range []string{"anullsink", "color", "showfreqs", "a3dscope", "aphasemeter", "showvolume", "fade", "colorchannelmixer"} {
		if !parsed[want] {
			t.Fatalf("expected parsed filter %q in %+v", want, parsed)
		}
	}
	if ok, missing := PresetAvailable("3dscope", parsed); !ok || missing != "" {
		t.Fatalf("expected 3dscope available, got (%v, %q)", ok, missing)
	}

	delete(parsed, "a3dscope")
	if ok, missing := PresetAvailable("3dscope", parsed); ok || missing != "a3dscope" {
		t.Fatalf("expected 3dscope unavailable due to missing a3dscope, got (%v, %q)", ok, missing)
	}

	// Verify DetectFilters parses FFmpeg 5/6/7 3-char flags too and invokes exact binary args.
	var gotArgs []string
	filters, err := DetectFilters(context.Background(), func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "ffmpeg" {
			t.Fatalf("unexpected binary %q", name)
		}
		gotArgs = args
		return []byte(" ... showfreqs A->V Desc\n T.C gblur V->V Blur\n"), nil
	})
	if err != nil {
		t.Fatalf("DetectFilters error: %v", err)
	}
	if !reflect.DeepEqual(gotArgs, []string{"-hide_banner", "-filters"}) {
		t.Fatalf("DetectFilters args = %v", gotArgs)
	}
	if !filters["showfreqs"] || !filters["gblur"] {
		t.Fatalf("expected 3-char filters parsed cleanly: %+v", filters)
	}

	if _, err := DetectFilters(context.Background(), func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("boom")
	}); err == nil {
		t.Fatal("expected error when runner fails")
	}
}

func TestPathDFadeAndFilterBuilders(t *testing.T) {
	t.Run("BuildPathDFadeFilter clamps and omits cleanly", func(t *testing.T) {
		cases := []struct {
			name     string
			fade     time.Duration
			duration float64
			want     string
		}{
			{"zero disabled", 0, 3600, ""},
			{"15m on 1h mix", 15 * time.Minute, 3600, "fade=t=out:st=900:d=15:color=black"},
			{"15m on 905s mix clamps d=5", 15 * time.Minute, 905, "fade=t=out:st=900:d=5:color=black"},
			{"15m on 900.5s mix clamps min d=1", 15 * time.Minute, 900.5, "fade=t=out:st=900:d=1:color=black"},
			{"15m on 60s mix omitted", 15 * time.Minute, 60, ""},
			{"15m equals totalDuration omitted", 15 * time.Minute, 900, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := BuildPathDFadeFilter(tc.fade, tc.duration); got != tc.want {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			})
		}
	})

	t.Run("Glow uses planar gbrp, Brightness, Fade-after-Subtitles, and Resolution/Preset Invariants", func(t *testing.T) {
		fcGlow, err := BuildPathDFilterComplex("", false, 3600, VisualizerConfig{
			Preset:     "bars",
			Theme:      "studio-mint",
			Glow:       true,
			Brightness: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcGlow, "format=gbrp") || strings.Contains(fcGlow, "format=rgb24") {
			t.Fatalf("expected planar format=gbrp (no rgb24) so blend stays in RGB, got:\n%s", fcGlow)
		}
		if !strings.Contains(fcGlow, "win_size=8192") || !strings.Contains(fcGlow, "ascale=log") {
			t.Fatalf("expected sharp bars win_size=8192 and ascale=log, got:\n%s", fcGlow)
		}
		if !strings.Contains(fcGlow, "gblur=sigma=12") || !strings.Contains(fcGlow, "blend=all_mode=screen") {
			t.Fatalf("expected glow stage on bars, got:\n%s", fcGlow)
		}
		if strings.Contains(fcGlow, "colorchannelmixer") {
			t.Fatalf("expected no colorchannelmixer at 100%% brightness:\n%s", fcGlow)
		}

		fcNoGlow, err := BuildPathDFilterComplex(`C:\tmp\O'Brien\titles.ass`, true, 3600, VisualizerConfig{
			Preset:      "bars",
			Theme:       "studio-mint",
			Resolution:  "1440p",
			Glow:        false,
			Brightness:  10,
			FadeToBlack: 15 * time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcNoGlow, "s=2560x1440") {
			t.Fatalf("expected 1440p 2560x1440 canvas, got:\n%s", fcNoGlow)
		}
		if strings.Contains(fcNoGlow, "gblur") {
			t.Fatalf("expected no gblur when Glow=false, got:\n%s", fcNoGlow)
		}
		if !strings.Contains(fcNoGlow, "colorchannelmixer=rr=0.10:gg=0.10:bb=0.10") {
			t.Fatalf("expected 10%% brightness colorchannelmixer, got:\n%s", fcNoGlow)
		}
		subIdx := strings.Index(fcNoGlow, "subtitles='C\\:/tmp/O\\'Brien/titles.ass'")
		fadeIdx := strings.Index(fcNoGlow, "fade=t=out:st=900:d=15:color=black")
		if subIdx < 0 || fadeIdx < 0 || fadeIdx < subIdx {
			t.Fatalf("expected fade filter AFTER subtitles= filter (subIdx=%d, fadeIdx=%d):\n%s", subIdx, fadeIdx, fcNoGlow)
		}

		fcEmber1080, err := BuildPathDFilterComplex("", false, 600, VisualizerConfig{
			Preset:     "ember",
			Theme:      "oled-ember",
			Glow:       true,
			Brightness: 45,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcEmber1080, "s=1920x1080") || strings.Contains(fcEmber1080, "s=960x540") || !strings.Contains(fcEmber1080, "format=gbrp") || !strings.Contains(fcEmber1080, "colorchannelmixer=rr=0.45:gg=0.45:bb=0.45") {
			t.Fatalf("expected native 1080p gbrp ember filtergraph, got:\n%s", fcEmber1080)
		}

		fcEmber4K, err := BuildPathDFilterComplex("", false, 600, VisualizerConfig{
			Preset:     "ember",
			Resolution: "4k",
			Glow:       true,
			Brightness: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcEmber4K, "s=1920x1080") || !strings.Contains(fcEmber4K, "scale=3840:2160:flags=bicubic") {
			t.Fatalf("expected 4K ember to cap pre-blur at 1080p and bicubic-upscale to 4K, got:\n%s", fcEmber4K)
		}

		fcWaves, err := BuildPathDFilterComplex("", false, 600, VisualizerConfig{Preset: "waves", Brightness: 100})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcWaves, "scale=cbrt:draw=full") {
			t.Fatalf("expected crisp waves draw=full and scale=cbrt:\n%s", fcWaves)
		}

		fcPhase, err := BuildPathDFilterComplex("", false, 600, VisualizerConfig{Preset: "phase", Brightness: 100})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcPhase, "[aout]anullsink") {
			t.Fatalf("expected [aout]anullsink in phase filtergraph:\n%s", fcPhase)
		}
		fcCQT, err := BuildPathDFilterComplex("", false, 600, VisualizerConfig{Preset: "cqt", Resolution: "1440p", Brightness: 100})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcCQT, "s=2560x1440") || !strings.Contains(fcCQT, "axis=0:text=0") || !strings.Contains(fcCQT, "csp=bt709") {
			t.Fatalf("expected 1440p axis=0:text=0 csp=bt709 in cqt filtergraph:\n%s", fcCQT)
		}
		fcVU, err := BuildPathDFilterComplex("", false, 600, VisualizerConfig{Preset: "vu", Brightness: 100})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fcVU, "t=0:v=0") {
			t.Fatalf("expected t=0:v=0 in vu filtergraph:\n%s", fcVU)
		}
	})

	t.Run("Blackout BuildPathDBlackArgs uses target resolution, BT.709 tags, faststart, and 2/10 fps", func(t *testing.T) {
		noSub := BuildPathDBlackArgs("merged.m4a", "", "out.mp4", false, 600, VisualizerConfig{Preset: "black", Resolution: "1440p", Brightness: 100})
		joinedNoSub := strings.Join(noSub, " ")
		if !strings.Contains(joinedNoSub, "color=c=black:s=2560x1440:r=2") ||
			!strings.Contains(joinedNoSub, "-tune stillimage") ||
			!strings.Contains(joinedNoSub, "-colorspace bt709") ||
			!strings.Contains(joinedNoSub, "-movflags +faststart") ||
			!strings.Contains(joinedNoSub, "-map 0:v:0 -map 1:a:0") {
			t.Fatalf("unexpected black no-sub args: %s", joinedNoSub)
		}

		withSub := BuildPathDBlackArgs("merged.m4a", `C:\tmp\titles.ass`, "out.mp4", true, 3600, VisualizerConfig{
			Preset:      "black",
			Brightness:  100,
			FadeToBlack: 15 * time.Minute,
		})
		joinedSub := strings.Join(withSub, " ")
		if !strings.Contains(joinedSub, "color=c=black:s=1920x1080:r=10") || !strings.Contains(joinedSub, "subtitles='C\\:/tmp/titles.ass',fade=t=out:st=900:d=15:color=black") {
			t.Fatalf("unexpected black overlay args: %s", joinedSub)
		}
	})

	t.Run("BuildPathDArgs emits BT.709 color signaling and faststart", func(t *testing.T) {
		args, err := BuildPathDArgs("merged.m4a", "", "out.mp4", false, 60, VisualizerConfig{Preset: "bars"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(args, " ")
		for _, want := range []string{"-colorspace bt709", "-color_primaries bt709", "-color_trc bt709", "-color_range tv", "-movflags +faststart"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("BuildPathDArgs missing %q in %s", want, joined)
			}
		}
	})
}

func TestRenderPathDExecution(t *testing.T) {
	t.Run("RenderWithOptions dispatches black without hardware encoder probe and streams -progress", func(t *testing.T) {
		tempDir := t.TempDir()
		var probedHW bool
		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			probedHW = true
			return nil, nil
		}
		var streamedArgs []string
		streamRunner := func(_ context.Context, onLine func(string), _ string, args ...string) error {
			streamedArgs = args
			onLine("out_time_us=5000000")
			onLine("speed=12.0x")
			onLine("progress=continue")
			onLine("out_time_us=10000000")
			onLine("progress=end")
			return nil
		}
		var updates []ProgressUpdate
		pathKind, err := RenderWithOptions(context.Background(), RenderOptions{
			BGPath:          "",
			MergedAudioPath: filepath.Join(tempDir, "merged_audio.m4a"),
			TempDir:         tempDir,
			OutPath:         filepath.Join(tempDir, "out.mp4"),
			TotalDuration:   10.0,
			GOOS:            "linux",
			Visualizer:      VisualizerConfig{Preset: "black", Theme: "oled-ember", Brightness: 100},
			Runner:          runner,
			StreamRunner:    streamRunner,
			OnProgress:      func(u ProgressUpdate) { updates = append(updates, u) },
		})
		if err != nil {
			t.Fatalf("RenderWithOptions(black) error: %v", err)
		}
		if pathKind != PathD {
			t.Fatalf("pathKind = %q, want Path D", pathKind)
		}
		if probedHW {
			t.Fatal("black preset should use libx264 -tune stillimage without probing GPU encoder")
		}
		if len(updates) != 2 || updates[0].Fraction != 0.5 || !updates[1].Done {
			t.Fatalf("unexpected updates: %+v", updates)
		}
		if !strings.Contains(strings.Join(streamedArgs, " "), "-tune stillimage") {
			t.Fatalf("expected -tune stillimage in %v", streamedArgs)
		}
	})

	t.Run("RenderWithOptions dispatches audio-reactive preset using DetectEncoder", func(t *testing.T) {
		tempDir := t.TempDir()
		var cmds [][]string
		runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
			cmds = append(cmds, args)
			if len(args) > 0 && args[0] == "-v" {
				return nil, errors.New("no nvenc")
			}
			return nil, nil
		}
		pathKind, err := RenderWithOptions(context.Background(), RenderOptions{
			BGPath:          "",
			MergedAudioPath: filepath.Join(tempDir, "merged_audio.m4a"),
			TempDir:         tempDir,
			OutPath:         filepath.Join(tempDir, "out_bars.mp4"),
			TotalDuration:   10.0,
			GOOS:            "linux",
			Visualizer:      VisualizerConfig{Preset: "bars", Theme: "midnight-cyan", Glow: true, Brightness: 45},
			Runner:          runner,
		})
		if err != nil {
			t.Fatalf("RenderWithOptions(bars) error: %v", err)
		}
		if pathKind != PathD || len(cmds) != 2 {
			t.Fatalf("pathKind=%q len(cmds)=%d, want Path D and 2 commands", pathKind, len(cmds))
		}
		joined := strings.Join(cmds[1], " ")
		if !strings.Contains(joined, "-filter_complex") || !strings.Contains(joined, "colorchannelmixer=rr=0.45:gg=0.45:bb=0.45") {
			t.Fatalf("unexpected bars render args: %s", joined)
		}
	})
}

func TestPathDAllPresetsFFmpegSmoke(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("skipping real FFmpeg visualizer smoke test: %v", err)
	}

	tmp := t.TempDir()
	sampleAudio := filepath.Join(tmp, "sample.m4a")
	ctx := context.Background()
	prepCmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anoisesrc=d=0.3:c=pink:r=48000:a=0.4",
		"-c:a", "aac", "-b:a", "192k", sampleAudio,
	)
	if out, err := prepCmd.CombinedOutput(); err != nil {
		t.Fatalf("prepare sample audio failed: %v\n%s", err, string(out))
	}

	filtersOut, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-filters").CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg -filters failed: %v", err)
	}
	avail := ParseFiltersOutput(filtersOut)

	for _, spec := range ListVisualizers() {
		t.Run(spec.ID, func(t *testing.T) {
			if ok, missing := PresetAvailable(spec.ID, avail); !ok {
				t.Skipf("ffmpeg build lacks required filter %q", missing)
			}
			args, err := BuildPathDArgs(sampleAudio, "", filepath.Join(tmp, "unused.mp4"), false, 0.3, VisualizerConfig{
				Preset:     spec.ID,
				Theme:      "studio-mint",
				Glow:       true,
				Brightness: 45,
			}, []string{"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28"})
			if err != nil {
				t.Fatal(err)
			}
			// Keep all flags (including -shortest on infinite color=c=black source) and direct output to -f null -.
			nullArgs := append([]string(nil), args[:len(args)-1]...)
			nullArgs = append(nullArgs, "-f", "null", "-")
			cmd := exec.CommandContext(ctx, "ffmpeg", nullArgs...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("ffmpeg failed for preset %q: %v\nargs: %v\noutput:\n%s", spec.ID, err, nullArgs, string(out))
			}
		})
	}
}

func TestBuildPathDPreviewArgs(t *testing.T) {
	cases := []struct {
		name        string
		opts        PreviewOptions
		wantSubstrs []string
		omitSubstrs []string
		wantErr     bool
	}{
		{
			name: "black preset defaults without subtitles or offset",
			opts: PreviewOptions{
				OutPNGPath: "preview.png",
				Config:     VisualizerConfig{Preset: "black", Resolution: "1440p", Brightness: 100},
			},
			wantSubstrs: []string{"-f lavfi -i color=c=black:s=2560x1440:r=10", "-vf format=yuv420p", "-frames:v 1 preview.png"},
			omitSubstrs: []string{"-ss", "-filter_complex", "subtitles="},
		},
		{
			name: "black preset with title overlay, fade, and offset",
			opts: PreviewOptions{
				ASSPath:       `C:\tmp\titles.ass`,
				OutPNGPath:    "black_sub.png",
				OverlayTitles: true,
				OffsetSec:     5.0,
				TotalDuration: 120.0,
				Config:        VisualizerConfig{Preset: "black", FadeToBlack: 60 * time.Second},
			},
			wantSubstrs: []string{"-f lavfi -i color=c=black:s=1920x1080:r=10", "subtitles='C\\:/tmp/titles.ass',fade=t=out:st=60:d=15:color=black,format=yuv420p", "-ss 5 -frames:v 1 black_sub.png"},
		},
		{
			name: "audio-reactive preset with lavfi source and output offset",
			opts: PreviewOptions{
				AudioInput:    "aevalsrc=0:c=stereo:s=48000:d=5.5",
				IsLavfi:       true,
				ASSPath:       "/tmp/titles.ass",
				OutPNGPath:    "/tmp/out.png",
				OverlayTitles: true,
				OffsetSec:     0.25,
				TotalDuration: 60.0,
				Config:        VisualizerConfig{Preset: "ember", Theme: "oled-ember", Resolution: "1080p", Glow: true, Brightness: 45},
			},
			wantSubstrs: []string{"-f lavfi -i aevalsrc=0:c=stereo:s=48000:d=5.5", "-filter_complex", "colorchannelmixer=rr=0.45:gg=0.45:bb=0.45", "subtitles='/tmp/titles.ass'", "-map [vout] -ss 0.25 -frames:v 1 /tmp/out.png"},
		},
		{
			name: "audio-reactive preset with lavfi source and zero offset omits -ss",
			opts: PreviewOptions{
				AudioInput: "aevalsrc=0:c=stereo:s=48000:d=1.0",
				IsLavfi:    true,
				OutPNGPath: "/tmp/out_zero.png",
				OffsetSec:  0,
				Config:     VisualizerConfig{Preset: "bars"},
			},
			wantSubstrs: []string{"-f lavfi -i aevalsrc=0:c=stereo:s=48000:d=1.0", "-map [vout] -frames:v 1 /tmp/out_zero.png"},
			omitSubstrs: []string{"-ss"},
		},
		{
			name: "audio-reactive preset with mp3 file at 5s uses 3s pre-input seek and 2s output seek with overlay and fade",
			opts: PreviewOptions{
				AudioInput:    "/music/track.mp3",
				IsLavfi:       false,
				ASSPath:       "/tmp/titles.ass",
				OutPNGPath:    "/tmp/mp3_5s.png",
				OverlayTitles: true,
				OffsetSec:     5.0,
				TotalDuration: 120.0,
				Config:        VisualizerConfig{Preset: "bars", FadeToBlack: 4 * time.Second},
			},
			wantSubstrs: []string{"-ss 3 -i /music/track.mp3", "subtitles='/tmp/titles.ass'", "fade=t=out:st=1:d=15:color=black", "-map [vout] -ss 2 -frames:v 1 /tmp/mp3_5s.png"},
			omitSubstrs: []string{"-f lavfi"},
		},
		{
			name: "audio-reactive preset with mp3 file and fade earlier than preSeek clamps fade to 10ms",
			opts: PreviewOptions{
				AudioInput:    "/music/track.mp3",
				IsLavfi:       false,
				OutPNGPath:    "/tmp/mp3_fade_clamped.png",
				OffsetSec:     12.5,
				TotalDuration: 18.0,
				Config:        VisualizerConfig{Preset: "waves", Theme: "midnight-cyan", Resolution: "4k", Glow: false, Brightness: 80, FadeToBlack: 3 * time.Second},
			},
			wantSubstrs: []string{"-ss 10.5 -i /music/track.mp3", "s=3840x2160", "fade=t=out:st=0.01:d=7.49:color=black", "-map [vout] -ss 2 -frames:v 1 /tmp/mp3_fade_clamped.png"},
			omitSubstrs: []string{"-f lavfi"},
		},
		{
			name: "audio-reactive preset with mp3 file at 1.5s uses output seek only",
			opts: PreviewOptions{
				AudioInput: "/music/track.mp3",
				IsLavfi:    false,
				OutPNGPath: "/tmp/mp3_1_5s.png",
				OffsetSec:  1.5,
				Config:     VisualizerConfig{Preset: "bars"},
			},
			wantSubstrs: []string{"-y -hide_banner -loglevel error -i /music/track.mp3", "-map [vout] -ss 1.5 -frames:v 1 /tmp/mp3_1_5s.png"},
		},
		{
			name: "audio-reactive preset with mp3 file and zero offset omits -ss",
			opts: PreviewOptions{
				AudioInput: "/music/track.mp3",
				IsLavfi:    false,
				OutPNGPath: "/tmp/mp3_zero.png",
				OffsetSec:  0,
				Config:     VisualizerConfig{Preset: "bars"},
			},
			omitSubstrs: []string{"-ss"},
		},
		{
			name: "overlayTitles enabled without assPath returns error",
			opts: PreviewOptions{
				AudioInput:    "song.mp3",
				OutPNGPath:    "out.png",
				OverlayTitles: true,
				Config:        VisualizerConfig{Preset: "bars"},
			},
			wantErr: true,
		},
		{
			name: "invalid preset returns error",
			opts: PreviewOptions{
				AudioInput: "song.mp3",
				OutPNGPath: "out.png",
				Config:     VisualizerConfig{Preset: "bogus"},
			},
			wantErr: true,
		},
		{
			name: "missing output PNG path returns error",
			opts: PreviewOptions{
				AudioInput: "song.mp3",
				OutPNGPath: "   ",
				Config:     VisualizerConfig{Preset: "bars"},
			},
			wantErr: true,
		},
		{
			name: "missing audio input for non-black preset returns error",
			opts: PreviewOptions{
				AudioInput: "",
				OutPNGPath: "out.png",
				Config:     VisualizerConfig{Preset: "bars"},
			},
			wantErr: true,
		},
		{
			name: "negative offsetSec returns error",
			opts: PreviewOptions{
				AudioInput: "song.mp3",
				OutPNGPath: "out.png",
				OffsetSec:  -1,
				Config:     VisualizerConfig{Preset: "bars"},
			},
			wantErr: true,
		},
		{
			name: "NaN offsetSec returns error",
			opts: PreviewOptions{
				AudioInput: "song.mp3",
				OutPNGPath: "out.png",
				OffsetSec:  math.NaN(),
				Config:     VisualizerConfig{Preset: "bars"},
			},
			wantErr: true,
		},
		{
			name: "negative totalDuration returns error",
			opts: PreviewOptions{
				AudioInput:    "song.mp3",
				OutPNGPath:    "out.png",
				TotalDuration: -1,
				Config:        VisualizerConfig{Preset: "bars"},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := BuildPathDPreviewArgs(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got args=%v", args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			joined := strings.Join(args, " ")
			for _, want := range tc.wantSubstrs {
				if !strings.Contains(joined, want) {
					t.Fatalf("expected %q in args:\n%s", want, joined)
				}
			}
			for _, omit := range tc.omitSubstrs {
				if strings.Contains(joined, omit) {
					t.Fatalf("did not expect %q in args:\n%s", omit, joined)
				}
			}
		})
	}
}
