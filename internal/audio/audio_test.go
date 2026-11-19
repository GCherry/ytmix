package audio

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
		fileNames := []string{"a.mp3", "b.mp3"}
		durations := []float64{5.0, 3.0}
		if err := ValidateCrossfade(fileNames, durations, -1); err == nil {
			t.Fatal("expected error for negative crossfade")
		}
		if err := ValidateCrossfade(fileNames, durations, 3.0); err == nil {
			t.Fatal("expected error when crossfade == min track duration")
		}
		if err := ValidateCrossfade(fileNames, durations, 4.0); err == nil {
			t.Fatal("expected error when crossfade > min track duration")
		}
		if err := ValidateCrossfade(fileNames, durations, 2.9); err != nil {
			t.Fatalf("expected valid crossfade < min duration, got: %v", err)
		}
	})

	t.Run("normalizes to wav, re-probes duration, and checks post-norm crossfade", func(t *testing.T) {
		tempDir := t.TempDir()
		paths := []string{
			filepath.Join(tempDir, "01.mp3"),
			filepath.Join(tempDir, "02.mp3"),
		}
		fileNames := []string{"01.mp3", "02.mp3"}
		durations := []float64{5.0, 5.0}

		var commands [][]string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
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

		normPaths, normDurations, err := NormalizeTracks(context.Background(), paths, fileNames, tempDir, runner)
		if err != nil {
			t.Fatalf("NormalizeTracks failed: %v", err)
		}
		if normDurations[0] != 4.8 {
			t.Fatalf("unexpected normDurations[0]: %v", normDurations[0])
		}
		if !strings.HasSuffix(normPaths[0], "norm_0000.wav") {
			t.Fatalf("expected wav path, got %q", normPaths[0])
		}

		wantNormCmd := []string{
			"ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
			"-i", paths[0],
			"-c:a", "pcm_s16le", "-ar", "48000",
			"-af", "loudnorm=I=-14:TP=-1.5:LRA=11",
			filepath.Join(tempDir, "norm_0000.wav"),
		}
		if !reflect.DeepEqual(commands[0], wantNormCmd) {
			t.Fatalf("normalize cmd = %v, want %v", commands[0], wantNormCmd)
		}

		// Crossfade 4.0 was < 5.0 pre-norm, but >= 3.8 post-norm -> must fail in ProcessAndMerge.
		if _, _, _, err := ProcessAndMerge(context.Background(), paths, fileNames, durations, tempDir, true, 4.0, runner); err == nil {
			t.Fatal("expected post-normalization crossfade error")
		}
	})

	t.Run("NormalizeTracks aborts between tracks when context is cancelled", func(t *testing.T) {
		tempDir := t.TempDir()
		paths := []string{
			filepath.Join(tempDir, "01.mp3"),
			filepath.Join(tempDir, "02.mp3"),
		}
		fileNames := []string{"01.mp3", "02.mp3"}

		ctx, cancel := context.WithCancel(context.Background())
		var calls int
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls++
			if name == "ffprobe" {
				cancel()
				return []byte(`{"format":{"duration":"4.800000","tags":{}}}`), nil
			}
			return nil, nil
		}

		_, _, err := NormalizeTracks(ctx, paths, fileNames, tempDir, runner)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected only track 1 (ffmpeg + ffprobe = 2 calls) before cancel, got %d calls", calls)
		}
	})

	t.Run("uses concat_list for single track even with crossfade > 0 and uses acrossfade for multi-track", func(t *testing.T) {
		tempDir := t.TempDir()
		singlePaths := []string{filepath.Join(tempDir, "only.mp3")}
		singleNames := []string{"only.mp3"}
		singleDurs := []float64{10.0}

		var singleArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			singleArgs = append([]string{name}, args...)
			return nil, nil
		}
		mergedPath, _, _, err := ProcessAndMerge(context.Background(), singlePaths, singleNames, singleDurs, tempDir, false, 3.0, runner)
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

		twoPaths := []string{
			filepath.Join(tempDir, "1.mp3"),
			filepath.Join(tempDir, "2.mp3"),
		}
		twoNames := []string{"1.mp3", "2.mp3"}
		twoDurs := []float64{10.0, 10.0}
		var multiArgs []string
		multiRunner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			multiArgs = append([]string{name}, args...)
			return nil, nil
		}
		if _, _, _, err := ProcessAndMerge(context.Background(), twoPaths, twoNames, twoDurs, tempDir, false, 2.0, multiRunner); err != nil {
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
		paths := []string{filepath.Join(tempDir, "1.mp3")}
		fileNames := []string{"1.mp3"}
		durations := []float64{5.0}
		failFFmpeg := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" {
				return nil, errors.New("ffmpeg failed: exit status 1\nstderr:\ninvalid audio stream")
			}
			return nil, nil
		}
		if _, _, _, err := ProcessAndMerge(context.Background(), paths, fileNames, durations, tempDir, true, 0, failFFmpeg); err == nil || !strings.Contains(err.Error(), "invalid audio stream") {
			t.Fatalf("expected stderr propagation on normalize, got: %v", err)
		}
		if _, _, _, err := ProcessAndMerge(context.Background(), paths, fileNames, durations, tempDir, false, 0, failFFmpeg); err == nil || !strings.Contains(err.Error(), "invalid audio stream") {
			t.Fatalf("expected stderr propagation on merge, got: %v", err)
		}
	})
}
