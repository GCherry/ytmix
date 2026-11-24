package audio

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/probe"
)

func TestConcatListEscaping(t *testing.T) {
	paths := []string{
		`C:\Music\01 Track.mp3`,
		`D:\Audio\O'Brien's Mix.mp3`,
	}
	got := BuildConcatList(paths)
	want := "file 'C:/Music/01 Track.mp3'\nfile 'D:/Audio/O'\\''Brien'\\''s Mix.mp3'\n"
	if got != want {
		t.Fatalf("BuildConcatList =\n%s\nwant:\n%s", got, want)
	}
}

func TestAcrossfadeFilterGraph(t *testing.T) {
	if got := BuildAcrossfadeFilter(2, 2); got != "[0:a][1:a]acrossfade=d=2:c1=tri:c2=tri[aout]" {
		t.Fatalf("2-track filter = %q", got)
	}
	want3 := "[0:a][1:a]acrossfade=d=1.5:c1=tri:c2=tri[a1];[a1][2:a]acrossfade=d=1.5:c1=tri:c2=tri[aout]"
	if got := BuildAcrossfadeFilter(3, 1.5); got != want3 {
		t.Fatalf("3-track filter = %q, want %q", got, want3)
	}
}

func TestAudioNormalizationAndMerge(t *testing.T) {
	t.Run("rejects invalid crossfade against multi-track durations", func(t *testing.T) {
		tracks := []probe.Track{
			{FileName: "a.mp3", Duration: 5.0},
			{FileName: "b.mp3", Duration: 3.0},
		}
		if err := ValidateCrossfade(tracks, -1); err == nil {
			t.Fatal("expected error for negative crossfade")
		}
		if err := ValidateCrossfade(tracks, 3.0); err == nil {
			t.Fatal("expected error when crossfade == min track duration")
		}
		if err := ValidateCrossfade(tracks, 4.0); err == nil {
			t.Fatal("expected error when crossfade > min track duration")
		}
		if err := ValidateCrossfade(tracks, 2.9); err != nil {
			t.Fatalf("expected valid crossfade < min duration, got: %v", err)
		}
	})

	t.Run("normalizes to wav, re-probes duration, and checks post-norm crossfade", func(t *testing.T) {
		tempDir := t.TempDir()
		tracks := []probe.Track{
			{Path: filepath.Join(tempDir, "01.mp3"), FileName: "01.mp3", Duration: 5.0, DisplayTitle: "One"},
			{Path: filepath.Join(tempDir, "02.mp3"), FileName: "02.mp3", Duration: 5.0, DisplayTitle: "Two"},
		}

		var mu sync.Mutex
		var commands [][]string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			mu.Lock()
			commands = append(commands, append([]string{name}, args...))
			mu.Unlock()
			if name == "ffprobe" {
				target := args[len(args)-1]
				if strings.HasSuffix(target, "norm_0000.wav") {
					return []byte(`{"format":{"duration":"4.800000","tags":{}}}`), nil
				}
				return []byte(`{"format":{"duration":"3.800000","tags":{}}}`), nil
			}
			return nil, nil
		}

		normTracks, err := NormalizeTracks(context.Background(), tracks, tempDir, runner)
		if err != nil {
			t.Fatalf("NormalizeTracks failed: %v", err)
		}
		if normTracks[0].Duration != 4.8 || normTracks[1].Duration != 3.8 {
			t.Fatalf("unexpected normTracks durations: %+v", normTracks)
		}
		if !strings.HasSuffix(normTracks[0].Path, "norm_0000.wav") || !strings.HasSuffix(normTracks[1].Path, "norm_0001.wav") {
			t.Fatalf("expected wav paths, got %+v", normTracks)
		}
		if normTracks[0].DisplayTitle != "One" || normTracks[1].DisplayTitle != "Two" {
			t.Fatalf("expected DisplayTitle preserved, got %+v", normTracks)
		}
		if tracks[0].Duration != 5.0 {
			t.Fatalf("expected caller input tracks slice not mutated, got %+v", tracks[0])
		}

		wantNormCmd := []string{
			"ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
			"-i", tracks[0].Path,
			"-c:a", "pcm_s16le", "-ar", "48000",
			"-af", "loudnorm=I=-14:TP=-1.5:LRA=11",
			filepath.Join(tempDir, "norm_0000.wav"),
		}
		foundNorm0 := false
		for _, cmd := range commands {
			if reflect.DeepEqual(cmd, wantNormCmd) {
				foundNorm0 = true
				break
			}
		}
		if !foundNorm0 {
			t.Fatalf("commands %v did not include %v", commands, wantNormCmd)
		}

		// Crossfade 4.0 was < 5.0 pre-norm, but >= 3.8 post-norm -> must fail in ProcessAndMerge.
		if _, _, err := ProcessAndMerge(context.Background(), tracks, tempDir, true, 4.0, runner); err == nil {
			t.Fatal("expected post-normalization crossfade error")
		}
	})

	t.Run("NormalizeTracksWithProgress preserves Track order, bounds concurrency, and emits sequential progress", func(t *testing.T) {
		tempDir := t.TempDir()
		const n = 8
		tracks := make([]probe.Track, n)
		for i := 0; i < n; i++ {
			tracks[i] = probe.Track{
				Path:         filepath.Join(tempDir, fmt.Sprintf("%02d.mp3", i)),
				FileName:     fmt.Sprintf("%02d.mp3", i),
				Duration:     10.0,
				DisplayTitle: fmt.Sprintf("Track %d", i),
			}
		}

		prevProcs := runtime.GOMAXPROCS(4)
		defer runtime.GOMAXPROCS(prevProcs)
		wantLimit := normalizeConcurrencyLimit()

		var active atomic.Int32
		var maxActive atomic.Int32
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" {
				cur := active.Add(1)
				for {
					prev := maxActive.Load()
					if cur <= prev || maxActive.CompareAndSwap(prev, cur) {
						break
					}
				}
				// Make earlier tracks slower so workers finish out of order.
				if strings.HasSuffix(args[len(args)-1], "norm_0000.wav") {
					time.Sleep(25 * time.Millisecond)
				} else {
					time.Sleep(5 * time.Millisecond)
				}
				active.Add(-1)
				return nil, nil
			}
			target := args[len(args)-1]
			var idx int
			if _, err := fmt.Sscanf(filepath.Base(target), "norm_%04d.wav", &idx); err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf(`{"format":{"duration":"%d.500000","tags":{}}}`, 10+idx)), nil
		}

		var progressCalls []int
		got, err := NormalizeTracksWithProgress(context.Background(), tracks, tempDir, runner, func(completed, total int) {
			if total != n {
				t.Errorf("total = %d, want %d", total, n)
			}
			progressCalls = append(progressCalls, completed)
		})
		if err != nil {
			t.Fatalf("NormalizeTracksWithProgress error: %v", err)
		}
		if gotMax := int(maxActive.Load()); gotMax > wantLimit || gotMax < 2 {
			t.Fatalf("max concurrent normalize workers = %d (want 2..%d)", gotMax, wantLimit)
		}
		wantProgress := []int{1, 2, 3, 4, 5, 6, 7, 8}
		if !reflect.DeepEqual(progressCalls, wantProgress) {
			t.Fatalf("progressCalls = %v, want %v", progressCalls, wantProgress)
		}
		for i := 0; i < n; i++ {
			wantDur := float64(10+i) + 0.5
			if got[i].Duration != wantDur || got[i].DisplayTitle != fmt.Sprintf("Track %d", i) || !strings.HasSuffix(got[i].Path, fmt.Sprintf("norm_%04d.wav", i)) {
				t.Fatalf("got[%d] = %+v, want dur %v", i, got[i], wantDur)
			}
		}
	})

	t.Run("NormalizeTracks fails fast and cancels context on error or pre-cancelled ctx", func(t *testing.T) {
		tempDir := t.TempDir()
		tracks := []probe.Track{
			{Path: filepath.Join(tempDir, "01.mp3"), FileName: "01.mp3"},
			{Path: filepath.Join(tempDir, "02.mp3"), FileName: "02.mp3"},
		}

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		var called atomic.Bool
		_, err := NormalizeTracks(cancelledCtx, tracks, tempDir, func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			called.Store(true)
			return nil, nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if called.Load() {
			t.Fatal("expected no runner calls for pre-cancelled ctx")
		}

		ctx, cancel2 := context.WithCancel(context.Background())
		runner := func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffprobe" {
				cancel2()
				return []byte(`{"format":{"duration":"4.800000","tags":{}}}`), nil
			}
			return nil, nil
		}
		_, err = NormalizeTracks(ctx, tracks, tempDir, runner)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled after cancel during normalize, got %v", err)
		}
	})

	t.Run("uses concat_list for single track even with crossfade > 0 and uses acrossfade for multi-track", func(t *testing.T) {
		tempDir := t.TempDir()
		single := []probe.Track{
			{Path: filepath.Join(tempDir, "only.mp3"), FileName: "only.mp3", Duration: 10.0},
		}

		var singleArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			singleArgs = append([]string{name}, args...)
			return nil, nil
		}
		mergedPath, _, err := ProcessAndMerge(context.Background(), single, tempDir, false, 3.0, runner)
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

		two := []probe.Track{
			{Path: filepath.Join(tempDir, "1.mp3"), FileName: "1.mp3", Duration: 10.0},
			{Path: filepath.Join(tempDir, "2.mp3"), FileName: "2.mp3", Duration: 10.0},
		}
		var multiArgs []string
		multiRunner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			multiArgs = append([]string{name}, args...)
			return nil, nil
		}
		if _, _, err := ProcessAndMerge(context.Background(), two, tempDir, false, 2.0, multiRunner); err != nil {
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
		tracks := []probe.Track{
			{Path: filepath.Join(tempDir, "1.mp3"), FileName: "1.mp3", Duration: 5.0},
		}
		failFFmpeg := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" {
				return nil, errors.New("ffmpeg failed: exit status 1\nstderr:\ninvalid audio stream")
			}
			return nil, nil
		}
		if _, _, err := ProcessAndMerge(context.Background(), tracks, tempDir, true, 0, failFFmpeg); err == nil || !strings.Contains(err.Error(), "invalid audio stream") {
			t.Fatalf("expected stderr propagation on normalize, got: %v", err)
		}
		if _, _, err := ProcessAndMerge(context.Background(), tracks, tempDir, false, 0, failFFmpeg); err == nil || !strings.Contains(err.Error(), "invalid audio stream") {
			t.Fatalf("expected stderr propagation on merge, got: %v", err)
		}
	})
}
