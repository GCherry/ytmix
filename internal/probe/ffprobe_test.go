package probe

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTitleAndTrackDiscovery(t *testing.T) {
	t.Run("Display Title prefers trimmed ID3 title over filename", func(t *testing.T) {
		got := ResolveDisplayTitle("  Midnight Breeze  ", "01 - ignored.mp3")
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
			got := ResolveDisplayTitle(tc.id3, tc.filename)
			if got != tc.want {
				t.Errorf("ResolveDisplayTitle(%q, %q) = %q, want %q", tc.id3, tc.filename, got, tc.want)
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

		sorted, err := DiscoverAudioFiles(dir, false, nil)
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
		shuffled, err := DiscoverAudioFiles(dir, true, rng.Shuffle)
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
		if _, err := DiscoverAudioFiles(emptyDir, false, nil); err == nil {
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

		dur1, title1, err := ProbeFile(context.Background(), p1, stubRunner)
		if err != nil {
			t.Fatalf("ProbeFile p1 error: %v", err)
		}
		dur2, title2, err := ProbeFile(context.Background(), p2, stubRunner)
		if err != nil {
			t.Fatalf("ProbeFile p2 error: %v", err)
		}

		wantArgs := []string{"-v", "error", "-show_entries", "format=duration", "-show_entries", "format_tags=title", "-of", "json", p1}
		if !reflect.DeepEqual(calls[0], wantArgs) {
			t.Fatalf("ffprobe args = %v, want %v", calls[0], wantArgs)
		}
		if ResolveDisplayTitle(title1, p1) != "Dawn Horizon" || dur1 != 125.5 {
			t.Fatalf("p1 = (%v, %q)", dur1, title1)
		}
		if ResolveDisplayTitle(title2, p2) != "Second" || dur2 != 90.25 {
			t.Fatalf("p2 = (%v, %q)", dur2, title2)
		}
	})

	t.Run("stops immediately when context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		called := false
		_, _, err := ProbeFile(ctx, "01.mp3", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			called = true
			return nil, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if called {
			t.Fatal("expected runner not to be invoked on cancelled context")
		}
	})
}
