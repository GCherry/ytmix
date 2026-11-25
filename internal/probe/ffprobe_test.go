package probe

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestProbeBackgroundVisual(t *testing.T) {
	t.Run("parses video loop dimensions, fractional avg_frame_rate, codec, and duration", func(t *testing.T) {
		var gotArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "ffprobe" {
				t.Fatalf("unexpected binary %q", name)
			}
			gotArgs = args
			return []byte(`{
				"format": {"duration": "12.500000"},
				"streams": [{
					"codec_name": "h264",
					"width": 1920,
					"height": 1080,
					"r_frame_rate": "30/1",
					"avg_frame_rate": "30000/1001",
					"nb_frames": "375",
					"pix_fmt": "yuv420p"
				}]
			}`), nil
		}

		meta, err := ProbeBackgroundVisual(context.Background(), "loop.mp4", runner)
		if err != nil {
			t.Fatalf("ProbeBackgroundVisual error: %v", err)
		}
		if len(gotArgs) == 0 || gotArgs[len(gotArgs)-1] != "loop.mp4" {
			t.Fatalf("unexpected ffprobe args: %v", gotArgs)
		}
		if strings.Contains(strings.Join(gotArgs, " "), "-select_streams") {
			t.Fatalf("expected ProbeBackgroundVisual not to pass -select_streams so audio streams are detected, got %v", gotArgs)
		}
		if meta.Width != 1920 || meta.Height != 1080 || meta.CodecName != "h264" || meta.PixFmt != "yuv420p" {
			t.Fatalf("unexpected stream metadata: %+v", meta)
		}
		if meta.DurationSec != 12.5 || meta.NBFrames != 375 || meta.HasAudio {
			t.Fatalf("unexpected duration/frames/hasAudio: %+v", meta)
		}
		if meta.FPS < 29.96 || meta.FPS > 29.98 {
			t.Fatalf("FPS = %v, want ~29.97", meta.FPS)
		}
	})

	t.Run("detects HasAudio across stream orders and selects first video stream", func(t *testing.T) {
		cases := []struct {
			name         string
			payload      string
			wantHasAudio bool
			wantWidth    int
			wantHeight   int
			wantCodec    string
		}{
			{
				name: "audio stream before video stream",
				payload: `{
					"format": {"duration": "8.0"},
					"streams": [
						{"codec_type": "audio", "codec_name": "aac"},
						{"codec_type": "video", "codec_name": "h264", "width": 1280, "height": 720, "avg_frame_rate": "24/1", "pix_fmt": "yuv420p"}
					]
				}`,
				wantHasAudio: true,
				wantWidth:    1280,
				wantHeight:   720,
				wantCodec:    "h264",
			},
			{
				name: "video stream before audio stream",
				payload: `{
					"format": {"duration": "15.0"},
					"streams": [
						{"codec_type": "video", "codec_name": "hevc", "width": 1920, "height": 1080, "avg_frame_rate": "30/1", "pix_fmt": "yuv420p"},
						{"codec_type": "audio", "codec_name": "aac"}
					]
				}`,
				wantHasAudio: true,
				wantWidth:    1920,
				wantHeight:   1080,
				wantCodec:    "hevc",
			},
			{
				name: "video stream only",
				payload: `{
					"format": {"duration": "10.0"},
					"streams": [
						{"codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080, "avg_frame_rate": "30/1"}
					]
				}`,
				wantHasAudio: false,
				wantWidth:    1920,
				wantHeight:   1080,
				wantCodec:    "h264",
			},
			{
				name: "audio stream only leaves video dimensions zero",
				payload: `{
					"format": {"duration": "10.0"},
					"streams": [
						{"codec_type": "audio", "codec_name": "mp3"}
					]
				}`,
				wantHasAudio: true,
				wantWidth:    0,
				wantHeight:   0,
				wantCodec:    "",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				meta, err := ProbeBackgroundVisual(context.Background(), "bg.mp4", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
					return []byte(tc.payload), nil
				})
				if err != nil {
					t.Fatalf("ProbeBackgroundVisual error: %v", err)
				}
				if meta.HasAudio != tc.wantHasAudio || meta.Width != tc.wantWidth || meta.Height != tc.wantHeight || meta.CodecName != tc.wantCodec {
					t.Fatalf("got %+v, want hasAudio=%v width=%d height=%d codec=%q", meta, tc.wantHasAudio, tc.wantWidth, tc.wantHeight, tc.wantCodec)
				}
			})
		}
	})

	t.Run("infers GIF duration from nb_frames and FPS when format.duration is N/A", func(t *testing.T) {
		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{
				"format": {"duration": "N/A"},
				"streams": [{
					"codec_name": "gif",
					"width": 1280,
					"height": 720,
					"r_frame_rate": "15/1",
					"avg_frame_rate": "15/1",
					"nb_frames": "75",
					"pix_fmt": "bgra"
				}]
			}`), nil
		}

		meta, err := ProbeBackgroundVisual(context.Background(), "anim.gif", runner)
		if err != nil {
			t.Fatalf("ProbeBackgroundVisual GIF error: %v", err)
		}
		if meta.DurationSec != 5.0 || meta.FPS != 15.0 || meta.NBFrames != 75 || meta.CodecName != "gif" {
			t.Fatalf("unexpected GIF meta: %+v", meta)
		}
	})

	t.Run("handles GIF with unknown duration and static image cleanly", func(t *testing.T) {
		gifRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{"format":{"duration":"N/A"},"streams":[{"codec_name":"gif","width":800,"height":600,"r_frame_rate":"12/1","avg_frame_rate":"0/0"}]}`), nil
		}
		gifMeta, err := ProbeBackgroundVisual(context.Background(), "anim.gif", gifRunner)
		if err != nil {
			t.Fatalf("unknown duration GIF error: %v", err)
		}
		if gifMeta.DurationSec != 0 || gifMeta.FPS != 12.0 {
			t.Fatalf("unexpected unknown-duration GIF meta: %+v", gifMeta)
		}

		imgRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{"format":{"duration":"N/A"},"streams":[{"codec_name":"png","width":1920,"height":1080,"r_frame_rate":"25/1","avg_frame_rate":"0/0","pix_fmt":"rgb24"}]}`), nil
		}
		imgMeta, err := ProbeBackgroundVisual(context.Background(), "cover.png", imgRunner)
		if err != nil {
			t.Fatalf("image probe error: %v", err)
		}
		if imgMeta.Width != 1920 || imgMeta.Height != 1080 || imgMeta.DurationSec != 0 {
			t.Fatalf("unexpected image meta: %+v", imgMeta)
		}
	})

	t.Run("ParseFrameRate handles invalid and fractional rates", func(t *testing.T) {
		for _, tc := range []struct {
			in   string
			want float64
		}{
			{"30/1", 30},
			{"60", 60},
			{"0/0", 0},
			{"N/A", 0},
			{"NaN", 0},
			{"Inf", 0},
			{"+Inf/1", 0},
			{"300/1", 0},
			{"-5/1", 0},
			{"abc/1", 0},
		} {
			if got := ParseFrameRate(tc.in); got != tc.want {
				t.Errorf("ParseFrameRate(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	})

	t.Run("infers NBFrames when stream nb_frames is omitted and rejects Inf duration", func(t *testing.T) {
		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{"format":{"duration":"10.0"},"streams":[{"codec_name":"h264","width":1920,"height":1080,"r_frame_rate":"30/1","avg_frame_rate":"30/1"}]}`), nil
		}
		meta, err := ProbeBackgroundVisual(context.Background(), "loop.mp4", runner)
		if err != nil {
			t.Fatalf("ProbeBackgroundVisual error: %v", err)
		}
		if meta.NBFrames != 300 {
			t.Fatalf("NBFrames = %d, want 300", meta.NBFrames)
		}

		infRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{"format":{"duration":"Inf"},"streams":[{"codec_name":"h264","width":1920,"height":1080}]}`), nil
		}
		infMeta, err := ProbeBackgroundVisual(context.Background(), "inf.mp4", infRunner)
		if err != nil {
			t.Fatalf("ProbeBackgroundVisual Inf error: %v", err)
		}
		if infMeta.DurationSec != 0 {
			t.Fatalf("expected Inf duration rejected to 0, got %v", infMeta.DurationSec)
		}
	})

	t.Run("stops on cancelled context and propagates runner errors", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ProbeBackgroundVisual(ctx, "loop.mp4", func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			t.Fatal("runner should not run on cancelled ctx")
			return nil, nil
		}); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}

		if _, err := ProbeBackgroundVisual(context.Background(), "loop.mp4", nil); err == nil {
			t.Fatal("expected error for nil runner")
		}
	})
}

func TestTrackIntakeAndConcurrency(t *testing.T) {
	t.Run("ValidateTrackFile rejects empty, missing, directory, and non-mp3 files", func(t *testing.T) {
		dir := t.TempDir()
		mp3Path := filepath.Join(dir, "01 - Valid.MP3")
		wavPath := filepath.Join(dir, "02 - Invalid.wav")
		subDir := filepath.Join(dir, "folder.mp3")
		for _, p := range []string{mp3Path, wavPath} {
			if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(subDir, 0755); err != nil {
			t.Fatal(err)
		}

		if err := ValidateTrackFile("   "); err == nil {
			t.Fatal("expected error for empty path")
		}
		if err := ValidateTrackFile(filepath.Join(dir, "missing.mp3")); err == nil {
			t.Fatal("expected error for missing file")
		}
		if err := ValidateTrackFile(subDir); err == nil {
			t.Fatal("expected error for directory")
		}
		if err := ValidateTrackFile(wavPath); err == nil {
			t.Fatal("expected error for non-mp3 extension")
		}
		if err := ValidateTrackFile(mp3Path); err != nil {
			t.Fatalf("expected valid mp3 path, got %v", err)
		}
	})

	t.Run("ProbeNamedTrackFiles preserves input order under out-of-order completion and uses FileName fallback", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "track_0001.mp3")
		p2 := filepath.Join(dir, "track_0002.mp3")
		p3 := filepath.Join(dir, "03 - Direct.mp3")
		for _, p := range []string{p1, p2, p3} {
			if err := os.WriteFile(p, []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "ffprobe" {
				return nil, errors.New("unexpected binary: " + name)
			}
			target := args[len(args)-1]
			switch target {
			case p1:
				time.Sleep(20 * time.Millisecond)
				return []byte(`{"format":{"duration":"10.0","tags":{}}}`), nil
			case p2:
				time.Sleep(5 * time.Millisecond)
				return []byte(`{"format":{"duration":"20.0","tags":{"title":"  Tagged Two  "}}}`), nil
			default:
				return []byte(`{"format":{"duration":"30.0","tags":{"title":"   \t "}}}`), nil
			}
		}

		tracks, err := ProbeNamedTrackFiles(context.Background(), []NamedTrackFile{
			{Path: p1, FileName: "01 - Uploaded First.mp3"},
			{Path: p2, FileName: "02 - Uploaded Second.mp3"},
			{Path: p3, FileName: ""},
		}, runner)
		if err != nil {
			t.Fatalf("ProbeNamedTrackFiles error: %v", err)
		}
		if len(tracks) != 3 {
			t.Fatalf("expected 3 tracks, got %d", len(tracks))
		}
		if tracks[0].Path != p1 || tracks[0].FileName != "01 - Uploaded First.mp3" || tracks[0].RawID3Title != "" || tracks[0].DisplayTitle != "Uploaded First" || tracks[0].Duration != 10.0 {
			t.Fatalf("tracks[0] = %+v", tracks[0])
		}
		if tracks[1].Path != p2 || tracks[1].FileName != "02 - Uploaded Second.mp3" || tracks[1].RawID3Title != "Tagged Two" || tracks[1].DisplayTitle != "Tagged Two" || tracks[1].Duration != 20.0 {
			t.Fatalf("tracks[1] = %+v", tracks[1])
		}
		if tracks[2].Path != p3 || tracks[2].FileName != "03 - Direct.mp3" || tracks[2].RawID3Title != "" || tracks[2].DisplayTitle != "Direct" || tracks[2].Duration != 30.0 {
			t.Fatalf("tracks[2] = %+v", tracks[2])
		}
	})

	t.Run("ProbeTrackFiles bounds concurrency and fails fast on error", func(t *testing.T) {
		dir := t.TempDir()
		const n = 16
		files := make([]string, n)
		for i := 0; i < n; i++ {
			p := filepath.Join(dir, "t"+string(rune('a'+i))+".mp3")
			if err := os.WriteFile(p, []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
			files[i] = p
		}

		prevProcs := runtime.GOMAXPROCS(4)
		defer runtime.GOMAXPROCS(prevProcs)
		wantLimit := probeConcurrencyLimit()

		var active atomic.Int32
		var maxActive atomic.Int32
		runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			cur := active.Add(1)
			for {
				prev := maxActive.Load()
				if cur <= prev || maxActive.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			active.Add(-1)
			return []byte(`{"format":{"duration":"5.0","tags":{}}}`), nil
		}

		tracks, err := ProbeTrackFiles(context.Background(), files, runner)
		if err != nil {
			t.Fatalf("ProbeTrackFiles error: %v", err)
		}
		if len(tracks) != n {
			t.Fatalf("expected %d tracks, got %d", n, len(tracks))
		}
		if gotMax := int(maxActive.Load()); gotMax > wantLimit || gotMax < 2 {
			t.Fatalf("max concurrent probes = %d (want 2..%d)", gotMax, wantLimit)
		}

		failErr := errors.New("probe boom")
		failRunner := func(ctx context.Context, _ string, args ...string) ([]byte, error) {
			if strings.HasSuffix(args[len(args)-1], "ta.mp3") {
				return nil, failErr
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Millisecond):
				return []byte(`{"format":{"duration":"5.0","tags":{}}}`), nil
			}
		}
		if _, err := ProbeTrackFiles(context.Background(), files, failRunner); !errors.Is(err, failErr) {
			t.Fatalf("expected failErr, got %v", err)
		}
	})

	t.Run("ProbeTracks discovers and probes directory and rejects nil runner", func(t *testing.T) {
		dir := t.TempDir()
		p1 := filepath.Join(dir, "01 - Alpha.mp3")
		if err := os.WriteFile(p1, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ProbeTracks(context.Background(), dir, false, nil, nil); err == nil {
			t.Fatal("expected error for nil runner")
		}
		tracks, err := ProbeTracks(context.Background(), dir, false, nil, func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return []byte(`{"format":{"duration":"7.5","tags":{}}}`), nil
		})
		if err != nil {
			t.Fatalf("ProbeTracks error: %v", err)
		}
		if len(tracks) != 1 || tracks[0].DisplayTitle != "Alpha" || tracks[0].Duration != 7.5 {
			t.Fatalf("unexpected tracks: %+v", tracks)
		}
	})
}
