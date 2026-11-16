package main

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFlagsAndValidation(t *testing.T) {
	t.Run("parses defaults and all 10 flags", func(t *testing.T) {
		cfg, err := parseFlags([]string{
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

		defCfg, err := parseFlags([]string{"--bg", "cover.png"})
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

	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("rejects missing bg", func(t *testing.T) {
		cfg := Config{AudioDir: "./tracks", Out: "output.mp4", Timestamps: "timestamps.txt"}
		err := validateConfig(cfg, okLookPath)
		if err == nil || !strings.Contains(err.Error(), "--bg") {
			t.Fatalf("expected --bg error, got: %v", err)
		}
	})

	t.Run("rejects unsupported bg extension and accepts supported case-insensitively", func(t *testing.T) {
		for _, valid := range []string{"a.jpg", "a.JPEG", "a.png", "a.webp", "a.mp4", "a.MOV", "a.gif"} {
			cfg := Config{BG: valid}
			if err := validateConfig(cfg, okLookPath); err != nil {
				t.Fatalf("expected %s to be valid, got: %v", valid, err)
			}
		}
		for _, invalid := range []string{"a.bmp", "a.mkv", "a.txt", "noext"} {
			cfg := Config{BG: invalid}
			if err := validateConfig(cfg, okLookPath); err == nil {
				t.Fatalf("expected %s to be rejected", invalid)
			}
		}
	})

	t.Run("rejects negative crossfade and negative title-duration", func(t *testing.T) {
		if err := validateConfig(Config{BG: "a.png", Crossfade: -0.5}, okLookPath); err == nil {
			t.Fatal("expected error for negative --crossfade")
		}
		if err := validateConfig(Config{BG: "a.png", TitleDuration: -1}, okLookPath); err == nil {
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
		if err := validateConfig(Config{BG: "a.png"}, missingFFmpeg); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
			t.Fatalf("expected ffmpeg error, got: %v", err)
		}

		missingFFprobe := func(name string) (string, error) {
			if name == "ffprobe" {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + name, nil
		}
		if err := validateConfig(Config{BG: "a.png"}, missingFFprobe); err == nil || !strings.Contains(err.Error(), "ffprobe") {
			t.Fatalf("expected ffprobe error, got: %v", err)
		}
	})
}

func TestTitleAndTrackDiscovery(t *testing.T) {
	t.Run("Display Title prefers trimmed ID3 title over filename", func(t *testing.T) {
		got := resolveDisplayTitle("  Midnight Breeze  ", "01 - ignored.mp3")
		if got != "Midnight Breeze" {
			t.Fatalf("got %q, want %q", got, "Midnight Breeze")
		}
	})

	t.Run("Display Title falls back to cleaned filename when ID3 title is empty or whitespace", func(t *testing.T) {
		cases := []struct {
			id3      string
			filename string
			want     string
		}{
			{"", "01 - Midnight Breeze.mp3", "Midnight Breeze"},
			{"   \t\n ", "02.Lofi_Rain.MP3", "Lofi_Rain"},
			{"", "003_Sunbeam (Mix).mp3", "Sunbeam (Mix)"},
			{"", "12   Morning Dew.Mp3", "Morning Dew"},
			{"", "NoNumber.mp3", "NoNumber"},
			{"", "01.mp3", "01"},
		}
		for _, tc := range cases {
			got := resolveDisplayTitle(tc.id3, tc.filename)
			if got != tc.want {
				t.Errorf("resolveDisplayTitle(%q, %q) = %q, want %q", tc.id3, tc.filename, got, tc.want)
			}
		}
	})

	t.Run("discovers mp3 case-insensitively, sorts alphabetically, and shuffles when enabled", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"03_c.MP3", "01_a.mp3", "02_b.Mp3", "notes.txt", "cover.png"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(dir, "subdir.mp3"), 0755); err != nil {
			t.Fatal(err)
		}

		sorted, err := discoverAudioFiles(dir, false, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		wantSorted := []string{
			filepath.Join(dir, "01_a.mp3"),
			filepath.Join(dir, "02_b.Mp3"),
			filepath.Join(dir, "03_c.MP3"),
		}
		if !reflect.DeepEqual(sorted, wantSorted) {
			t.Fatalf("sorted = %v, want %v", sorted, wantSorted)
		}

		rng := rand.New(rand.NewPCG(42, 99))
		shuffled, err := discoverAudioFiles(dir, true, rng.Shuffle)
		if err != nil {
			t.Fatalf("unexpected shuffle error: %v", err)
		}
		if len(shuffled) != 3 {
			t.Fatalf("expected 3 tracks after shuffle, got %d", len(shuffled))
		}
		if reflect.DeepEqual(shuffled, wantSorted) {
			t.Fatalf("expected shuffled order to differ from sorted order for seed (42,99), got %v", shuffled)
		}
	})

	t.Run("returns error when audio-dir has fewer than 1 mp3", func(t *testing.T) {
		emptyDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(emptyDir, "readme.txt"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := discoverAudioFiles(emptyDir, false, nil); err == nil {
			t.Fatal("expected error for directory with zero .mp3 files")
		}
	})

	t.Run("probes duration and ID3 title via ffprobe JSON", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "01 - First.mp3")
		p2 := filepath.Join(dir, "02 - Second.MP3")
		for _, p := range []string{p1, p2} {
			if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		var calls [][]string
		stubRunner := func(name string, args ...string) ([]byte, error) {
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

		tracks, err := probeTracks(dir, false, nil, stubRunner)
		if err != nil {
			t.Fatalf("probeTracks error: %v", err)
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
	})
}

func TestConcatListEscaping(t *testing.T) {
	paths := []string{
		`C:\Music\01 Track.mp3`,
		`D:\Audio\O'Brien's Mix.mp3`,
	}
	got := buildConcatList(paths)
	want := "file 'C:/Music/01 Track.mp3'\nfile 'D:/Audio/O'\\''Brien'\\''s Mix.mp3'\n"
	if got != want {
		t.Fatalf("buildConcatList =\n%s\nwant:\n%s", got, want)
	}
}

func TestAcrossfadeFilterGraph(t *testing.T) {
	if got := buildAcrossfadeFilter(2, 2); got != "[0:a][1:a]acrossfade=d=2:c1=tri:c2=tri[aout]" {
		t.Fatalf("2-track filter = %q", got)
	}
	want3 := "[0:a][1:a]acrossfade=d=1.5:c1=tri:c2=tri[a1];[a1][2:a]acrossfade=d=1.5:c1=tri:c2=tri[aout]"
	if got := buildAcrossfadeFilter(3, 1.5); got != want3 {
		t.Fatalf("3-track filter = %q, want %q", got, want3)
	}
}

func TestAudioNormalizationAndMerge(t *testing.T) {
	t.Run("rejects invalid crossfade against multi-track durations", func(t *testing.T) {
		tracks := []Track{
			{Path: "a.mp3", FileName: "a.mp3", Duration: 5.0, DisplayTitle: "A"},
			{Path: "b.mp3", FileName: "b.mp3", Duration: 3.0, DisplayTitle: "B"},
		}
		if err := validateCrossfade(tracks, -1); err == nil {
			t.Fatal("expected error for negative crossfade")
		}
		if err := validateCrossfade(tracks, 3.0); err == nil {
			t.Fatal("expected error when crossfade == min track duration")
		}
		if err := validateCrossfade(tracks, 4.0); err == nil {
			t.Fatal("expected error when crossfade > min track duration")
		}
		if err := validateCrossfade(tracks, 2.9); err != nil {
			t.Fatalf("expected valid crossfade < min duration, got: %v", err)
		}
	})

	t.Run("normalizes to wav, re-probes duration preserving DisplayTitle, and checks post-norm crossfade", func(t *testing.T) {
		tempDir := t.TempDir()
		tracks := []Track{
			{Path: filepath.Join(tempDir, "01.mp3"), FileName: "01.mp3", Duration: 5.0, DisplayTitle: "Keep Me"},
			{Path: filepath.Join(tempDir, "02.mp3"), FileName: "02.mp3", Duration: 5.0, DisplayTitle: "Second Track"},
		}

		var commands [][]string
		runner := func(name string, args ...string) ([]byte, error) {
			commands = append(commands, append([]string{name}, args...))
			if name == "ffprobe" {
				target := args[len(args)-1]
				if strings.HasSuffix(target, "norm_0000.wav") {
					return []byte(`{"format":{"duration":"4.800000","tags":{}}}`), nil
				}
				return []byte(`{"format":{"duration":"3.800000","tags":{}}}`), nil
			}
			return nil, nil
		}

		normTracks, err := normalizeTracks(tracks, tempDir, runner)
		if err != nil {
			t.Fatalf("normalizeTracks failed: %v", err)
		}
		if normTracks[0].DisplayTitle != "Keep Me" || normTracks[0].Duration != 4.8 {
			t.Fatalf("unexpected normTracks[0]: %+v", normTracks[0])
		}
		if !strings.HasSuffix(normTracks[0].Path, "norm_0000.wav") {
			t.Fatalf("expected wav path, got %q", normTracks[0].Path)
		}

		wantNormCmd := []string{
			"ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
			"-i", tracks[0].Path,
			"-c:a", "pcm_s16le", "-ar", "48000",
			"-af", "loudnorm=I=-14:TP=-1.5:LRA=11",
			filepath.Join(tempDir, "norm_0000.wav"),
		}
		if !reflect.DeepEqual(commands[0], wantNormCmd) {
			t.Fatalf("normalize cmd = %v, want %v", commands[0], wantNormCmd)
		}

		// Crossfade 4.0 was < 5.0 pre-norm, but >= 3.8 post-norm -> must fail in processAndMergeAudio.
		if _, _, err := processAndMergeAudio(tracks, tempDir, true, 4.0, runner); err == nil {
			t.Fatal("expected post-normalization crossfade error")
		}
	})

	t.Run("uses concat_list for single track even with crossfade > 0 and uses acrossfade for multi-track", func(t *testing.T) {
		tempDir := t.TempDir()
		single := []Track{{Path: filepath.Join(tempDir, "only.mp3"), FileName: "only.mp3", Duration: 10.0, DisplayTitle: "Only"}}

		var singleArgs []string
		runner := func(name string, args ...string) ([]byte, error) {
			singleArgs = append([]string{name}, args...)
			return nil, nil
		}
		mergedPath, _, err := processAndMergeAudio(single, tempDir, false, 3.0, runner)
		if err != nil {
			t.Fatalf("single track merge error: %v", err)
		}
		if filepath.Base(mergedPath) != "merged_audio.m4a" {
			t.Fatalf("mergedPath = %q", mergedPath)
		}
		joined := strings.Join(singleArgs, " ")
		if !strings.Contains(joined, "-f concat -safe 0") || strings.Contains(joined, "-filter_complex") {
			t.Fatalf("expected concat demuxer for single track, got: %s", joined)
		}

		twoTracks := []Track{
			{Path: filepath.Join(tempDir, "1.mp3"), FileName: "1.mp3", Duration: 10.0, DisplayTitle: "One"},
			{Path: filepath.Join(tempDir, "2.mp3"), FileName: "2.mp3", Duration: 10.0, DisplayTitle: "Two"},
		}
		var multiArgs []string
		multiRunner := func(name string, args ...string) ([]byte, error) {
			multiArgs = append([]string{name}, args...)
			return nil, nil
		}
		if _, _, err := processAndMergeAudio(twoTracks, tempDir, false, 2.0, multiRunner); err != nil {
			t.Fatalf("multi track merge error: %v", err)
		}
		multiJoined := strings.Join(multiArgs, " ")
		if !strings.Contains(multiJoined, "-filter_complex [0:a][1:a]acrossfade=d=2:c1=tri:c2=tri[aout]") ||
			!strings.Contains(multiJoined, "-map [aout] -c:a aac -b:a 320k -ar 48000") {
			t.Fatalf("unexpected acrossfade args: %s", multiJoined)
		}
	})

	t.Run("propagates ffmpeg stderr errors on normalize and merge", func(t *testing.T) {
		tempDir := t.TempDir()
		tracks := []Track{{Path: filepath.Join(tempDir, "1.mp3"), FileName: "1.mp3", Duration: 5.0, DisplayTitle: "One"}}
		failFFmpeg := func(name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" {
				return nil, errors.New("ffmpeg failed: exit status 1\nstderr:\ninvalid audio stream")
			}
			return nil, nil
		}
		if _, _, err := processAndMergeAudio(tracks, tempDir, true, 0, failFFmpeg); err == nil || !strings.Contains(err.Error(), "invalid audio stream") {
			t.Fatalf("expected stderr propagation on normalize, got: %v", err)
		}
		if _, _, err := processAndMergeAudio(tracks, tempDir, false, 0, failFFmpeg); err == nil || !strings.Contains(err.Error(), "invalid audio stream") {
			t.Fatalf("expected stderr propagation on merge, got: %v", err)
		}
	})
}

func TestTimestampsCalculationAndFormatting(t *testing.T) {
	t.Run("cumulative start times without and with crossfade", func(t *testing.T) {
		tracks := []Track{
			{Duration: 125.0, DisplayTitle: "Alpha"},
			{Duration: 90.0, DisplayTitle: "Beta"},
			{Duration: 60.0, DisplayTitle: "Gamma"},
		}

		noFade := buildChapterSchedule(tracks, 0)
		wantStartsNoFade := []float64{0, 125, 215}
		if !reflect.DeepEqual(noFade.Starts, wantStartsNoFade) || noFade.TotalDuration != 275.0 {
			t.Fatalf("noFade = %+v", noFade)
		}
		wantTextNoFade := "00:00 Alpha\n02:05 Beta\n03:35 Gamma\n"
		if noFade.Content != wantTextNoFade {
			t.Fatalf("noFade.Content =\n%s\nwant:\n%s", noFade.Content, wantTextNoFade)
		}

		withFade := buildChapterSchedule(tracks, 2.0)
		wantStartsFade := []float64{0, 123, 211}
		if !reflect.DeepEqual(withFade.Starts, wantStartsFade) || withFade.TotalDuration != 271.0 {
			t.Fatalf("withFade = %+v", withFade)
		}
		wantTextFade := "00:00 Alpha\n02:03 Beta\n03:31 Gamma\n"
		if withFade.Content != wantTextFade {
			t.Fatalf("withFade.Content =\n%s\nwant:\n%s", withFade.Content, wantTextFade)
		}
	})

	t.Run("switches from MM:SS to HH:MM:SS at exact 3600s boundary", func(t *testing.T) {
		underHour := []Track{
			{Duration: 3540.0, DisplayTitle: "Part One"},
			{Duration: 59.0, DisplayTitle: "Part Two"},
		}
		schedUnder := buildChapterSchedule(underHour, 0)
		if schedUnder.TotalDuration != 3599.0 {
			t.Fatalf("expected 3599.0, got %v", schedUnder.TotalDuration)
		}
		wantUnder := "00:00 Part One\n59:00 Part Two\n"
		if schedUnder.Content != wantUnder {
			t.Fatalf("underHour =\n%s\nwant:\n%s", schedUnder.Content, wantUnder)
		}

		atHour := []Track{
			{Duration: 3540.0, DisplayTitle: "Part One"},
			{Duration: 60.0, DisplayTitle: "Part Two"},
		}
		schedAt := buildChapterSchedule(atHour, 0)
		if schedAt.TotalDuration != 3600.0 {
			t.Fatalf("expected 3600.0, got %v", schedAt.TotalDuration)
		}
		wantAt := "00:00:00 Part One\n00:59:00 Part Two\n"
		if schedAt.Content != wantAt {
			t.Fatalf("atHour =\n%s\nwant:\n%s", schedAt.Content, wantAt)
		}
	})

	t.Run("writes formatted chapters to --timestamps file", func(t *testing.T) {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "custom_timestamps.txt")
		tracks := []Track{
			{Duration: 4.0, DisplayTitle: "Intro"},
			{Duration: 4.0, DisplayTitle: "Outro"},
		}
		sched, err := writeChapters(outPath, tracks, 1.0)
		if err != nil {
			t.Fatalf("writeChapters error: %v", err)
		}
		if sched.TotalDuration != 7.0 {
			t.Fatalf("TotalDuration = %v, want 7.0", sched.TotalDuration)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read written timestamps: %v", err)
		}
		want := "00:00 Intro\n00:03 Outro\n"
		if string(data) != want {
			t.Fatalf("written timestamps =\n%s\nwant:\n%s", string(data), want)
		}
	})
}

func TestRunPipelineSteps1To3AndPhase2Guard(t *testing.T) {
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
	bgPath := filepath.Join(workDir, "bg.png")
	if err := os.WriteFile(bgPath, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}
	tsPath := filepath.Join(workDir, "timestamps.txt")

	cfg := Config{
		AudioDir:      audioDir,
		BG:            bgPath,
		Out:           filepath.Join(workDir, "output.mp4"),
		Timestamps:    tsPath,
		Crossfade:     1.0,
		Normalize:     false,
		OverlayTitles: true,
		TitleDuration: 5,
		FontName:      "Arial",
	}

	okLookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	runner := func(name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"format":{"duration":"4.000000","tags":{}}}`), nil
		}
		return nil, nil
	}

	var stdout bytes.Buffer
	err := run(cfg, &stdout, okLookPath, runner)
	if err == nil || !strings.Contains(err.Error(), "Phase 2") {
		t.Fatalf("expected Phase 2 guard error, got: %v", err)
	}

	logOut := stdout.String()
	for _, step := range []string{"[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]"} {
		if !strings.Contains(logOut, step) {
			t.Errorf("expected %s in stdout, got:\n%s", step, logOut)
		}
	}

	tsBytes, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("expected timestamps file at %s: %v", tsPath, err)
	}
	if string(tsBytes) != "00:00 First\n00:03 Second\n" {
		t.Fatalf("timestamps =\n%s", string(tsBytes))
	}
}
