package mixer

import (
	"bytes"
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

	tracks, err := ProbeTracks(dir, false, nil, stubRunner)
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
	err := Run(cfg, &stdout, okLookPath, runner)
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
