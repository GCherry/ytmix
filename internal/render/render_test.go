package render

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSelectPath(t *testing.T) {
	cases := []struct {
		bg      string
		overlay bool
		want    Path
		wantErr bool
	}{
		{"loop.mp4", false, PathA, false},
		{"loop.MOV", false, PathA, false},
		{"anim.gif", false, PathA, false},
		{"loop.mp4", true, PathB, false},
		{"loop.mov", true, PathB, false},
		{"anim.GIF", true, PathB, false},
		{"cover.jpg", false, PathC, false},
		{"cover.JPEG", true, PathC, false},
		{"cover.png", false, PathC, false},
		{"cover.webp", true, PathC, false},
		{"", false, PathD, false},
		{"   ", true, PathD, false},
		{"cover.bmp", false, "", true},
		{"noext", true, "", true},
	}

	for _, tc := range cases {
		got, err := SelectPath(tc.bg, tc.overlay)
		if tc.wantErr {
			if err == nil {
				t.Errorf("SelectPath(%q, %v) expected error, got %q", tc.bg, tc.overlay, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SelectPath(%q, %v) unexpected error: %v", tc.bg, tc.overlay, err)
			continue
		}
		if got != tc.want {
			t.Errorf("SelectPath(%q, %v) = %q, want %q", tc.bg, tc.overlay, got, tc.want)
		}
	}
}

func TestBackgroundVisualClassification(t *testing.T) {
	t.Run("SupportedBackgroundExts returns expected canonical lists and fresh slices", func(t *testing.T) {
		got := SupportedBackgroundExts()
		wantImage := []string{".jpg", ".jpeg", ".png", ".webp"}
		wantVideo := []string{".mp4", ".mov", ".gif"}
		if !reflect.DeepEqual(got.Image, wantImage) || !reflect.DeepEqual(got.Video, wantVideo) {
			t.Fatalf("SupportedBackgroundExts() = %+v", got)
		}
		got.Image[0] = ".mutated"
		got.Video[0] = ".mutated"
		fresh := SupportedBackgroundExts()
		if fresh.Image[0] != ".jpg" || fresh.Video[0] != ".mp4" {
			t.Fatalf("SupportedBackgroundExts() returned shared slice: %+v", fresh)
		}
	})

	t.Run("CanonicalBackgroundExt normalizes case and whitespace", func(t *testing.T) {
		for _, tc := range []struct {
			in     string
			want   string
			wantOK bool
		}{
			{".jpg", ".jpg", true},
			{" .JPEG ", ".jpeg", true},
			{".PNG", ".png", true},
			{".WeBp", ".webp", true},
			{".MP4", ".mp4", true},
			{".mov", ".mov", true},
			{".GIF", ".gif", true},
			{".bmp", "", false},
			{"", "", false},
		} {
			got, ok := CanonicalBackgroundExt(tc.in)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("CanonicalBackgroundExt(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
			}
		}
	})

	t.Run("ClassifyBackground returns kind and canonical extension", func(t *testing.T) {
		for _, tc := range []struct {
			path     string
			wantKind Path
			wantBG   BackgroundKind
			wantExt  string
			wantErr  bool
		}{
			{"cover.PNG", PathC, BackgroundKindImage, ".png", false},
			{"cover.jpeg", PathC, BackgroundKindImage, ".jpeg", false},
			{"loop.MOV", PathA, BackgroundKindVideo, ".mov", false},
			{"anim.gif", PathA, BackgroundKindVideo, ".gif", false},
			{"notes.txt", "", "", "", true},
		} {
			kind, ext, err := ClassifyBackground(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ClassifyBackground(%q) expected error", tc.path)
				}
				continue
			}
			if err != nil {
				t.Errorf("ClassifyBackground(%q) unexpected error: %v", tc.path, err)
				continue
			}
			if kind != tc.wantBG || ext != tc.wantExt {
				t.Errorf("ClassifyBackground(%q) = (%q, %q), want (%q, %q)", tc.path, kind, ext, tc.wantBG, tc.wantExt)
			}
		}
	})
}

func TestEscapeFFmpegFilterPath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "windows drive letter and backslashes",
			in:   `C:\Users\chill\AppData\Local\Temp\ytmix-123\titles.ass`,
			want: `C\:/Users/chill/AppData/Local/Temp/ytmix-123/titles.ass`,
		},
		{
			name: "windows path with apostrophe and colon",
			in:   `D:\O'Brien's Mix\tmp\titles.ass`,
			want: `D\:/O\'Brien\'s Mix/tmp/titles.ass`,
		},
		{
			name: "posix path with apostrophe",
			in:   `/tmp/Bob's/titles.ass`,
			want: `/tmp/Bob\'s/titles.ass`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscapeFFmpegFilterPath(tc.in); got != tc.want {
				t.Fatalf("EscapeFFmpegFilterPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDetectEncoderArgs(t *testing.T) {
	t.Run("darwin uses h264_videotoolbox when 1-frame probe succeeds", func(t *testing.T) {
		var probedArgs []string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "ffmpeg" {
				t.Fatalf("unexpected binary %q", name)
			}
			probedArgs = args
			return nil, nil
		}
		got := DetectEncoderArgs(context.Background(), "darwin", runner)
		want := []string{"-c:v", "h264_videotoolbox", "-b:v", "6000k"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		wantProbe := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=0.1", "-c:v", "h264_videotoolbox", "-f", "null", "-"}
		if !reflect.DeepEqual(probedArgs, wantProbe) {
			t.Fatalf("probedArgs = %v, want %v", probedArgs, wantProbe)
		}
	})

	t.Run("windows and linux use h264_nvenc when 1-frame probe succeeds", func(t *testing.T) {
		for _, goos := range []string{"windows", "linux"} {
			var probedArgs []string
			runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
				probedArgs = args
				return nil, nil
			}
			got := DetectEncoderArgs(context.Background(), goos, runner)
			want := []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "20"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: got %v, want %v", goos, got, want)
			}
			wantProbe := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=0.1", "-c:v", "h264_nvenc", "-f", "null", "-"}
			if !reflect.DeepEqual(probedArgs, wantProbe) {
				t.Fatalf("%s: probedArgs = %v, want %v", goos, probedArgs, wantProbe)
			}
		}
	})

	t.Run("falls back to libx264 when hardware probe fails", func(t *testing.T) {
		failRunner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			return nil, errors.New("no gpu")
		}
		wantFallback := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}
		for _, goos := range []string{"darwin", "windows", "linux"} {
			if got := DetectEncoderArgs(context.Background(), goos, failRunner); !reflect.DeepEqual(got, wantFallback) {
				t.Fatalf("%s: got %v, want %v", goos, got, wantFallback)
			}
		}
	})

	t.Run("DetectEncoder returns structured name and args across platforms and edge cases", func(t *testing.T) {
		okRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, nil }
		failRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, errors.New("unavailable") }

		darwinInfo := DetectEncoder(context.Background(), "darwin", okRunner)
		if darwinInfo.Name != "h264_videotoolbox" || !reflect.DeepEqual(darwinInfo.Args, []string{"-c:v", "h264_videotoolbox", "-b:v", "6000k"}) {
			t.Fatalf("darwin EncoderInfo = %+v", darwinInfo)
		}

		winInfo := DetectEncoder(context.Background(), "windows", okRunner)
		if winInfo.Name != "h264_nvenc" || !reflect.DeepEqual(winInfo.Args, []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "20"}) {
			t.Fatalf("windows EncoderInfo = %+v", winInfo)
		}

		nilRunnerInfo := DetectEncoder(context.Background(), "windows", nil)
		if nilRunnerInfo.Name != "libx264" {
			t.Fatalf("nil runner EncoderInfo = %+v, want libx264", nilRunnerInfo)
		}

		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel()
		cancelledInfo := DetectEncoder(cancelledCtx, "linux", okRunner)
		if cancelledInfo.Name != "libx264" {
			t.Fatalf("cancelled ctx EncoderInfo = %+v, want libx264", cancelledInfo)
		}

		fallbackInfo := DetectEncoder(context.Background(), "linux", failRunner)
		if fallbackInfo.Name != "libx264" || !reflect.DeepEqual(fallbackInfo.Args, []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}) {
			t.Fatalf("fallback EncoderInfo = %+v", fallbackInfo)
		}
	})
}

func TestDetectPathDEncoder(t *testing.T) {
	t.Run("darwin probes VideoToolbox -q:v 65 first and uses it on Apple Silicon", func(t *testing.T) {
		var probes [][]string
		runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
			probes = append(probes, args)
			return nil, nil
		}
		got := DetectPathDEncoder(context.Background(), "darwin", Resolution1440p, runner)
		want := []string{"-c:v", "h264_videotoolbox", "-q:v", "65", "-profile:v", "high"}
		if got.Name != "h264_videotoolbox" || !reflect.DeepEqual(got.Args, want) {
			t.Fatalf("got %+v, want Args=%v", got, want)
		}
		if len(probes) != 1 || !strings.Contains(strings.Join(probes[0], " "), "-q:v 65") {
			t.Fatalf("expected single -q:v 65 probe, got %v", probes)
		}
	})

	t.Run("darwin falls back to resolution-scaled bitrate when -q:v probe fails", func(t *testing.T) {
		for _, tc := range []struct {
			res  string
			want string
		}{
			{Resolution1080p, "12000k"},
			{Resolution1440p, "18000k"},
			{Resolution4K, "28000k"},
		} {
			runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "-q:v") {
					return nil, errors.New("qscale not available on Intel")
				}
				return nil, nil
			}
			got := DetectPathDEncoder(context.Background(), "darwin", tc.res, runner)
			want := []string{"-c:v", "h264_videotoolbox", "-b:v", tc.want, "-profile:v", "high"}
			if got.Name != "h264_videotoolbox" || !reflect.DeepEqual(got.Args, want) {
				t.Fatalf("%s: got %+v, want Args=%v", tc.res, got, want)
			}
		}
	})

	t.Run("windows and linux use cq 18 on nvenc and crf 18 fast high on libx264 fallback", func(t *testing.T) {
		okRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, nil }
		nvenc := DetectPathDEncoder(context.Background(), "linux", Resolution1080p, okRunner)
		if nvenc.Name != "h264_nvenc" || !reflect.DeepEqual(nvenc.Args, []string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "18"}) {
			t.Fatalf("nvenc = %+v", nvenc)
		}

		failRunner := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, errors.New("no gpu") }
		x264 := DetectPathDEncoder(context.Background(), "linux", Resolution1080p, failRunner)
		if x264.Name != "libx264" || !reflect.DeepEqual(x264.Args, []string{"-c:v", "libx264", "-preset", "fast", "-crf", "18", "-profile:v", "high"}) {
			t.Fatalf("x264 = %+v", x264)
		}
	})
}

func TestBuildRenderArgs(t *testing.T) {
	t.Run("Path A normalize and mux args", func(t *testing.T) {
		norm := BuildPathANormalizeArgs("bg.mp4", "tmp/norm_loop.mp4")
		wantNorm := []string{
			"-y", "-hide_banner", "-loglevel", "error",
			"-i", "bg.mp4",
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p",
			"-c:v", "libx264", "-preset", "fast", "-crf", "18", "-an",
			"tmp/norm_loop.mp4",
		}
		if !reflect.DeepEqual(norm, wantNorm) {
			t.Fatalf("Path A norm = %v\nwant:       %v", norm, wantNorm)
		}

		mux := BuildPathAMuxArgs("tmp/norm_loop.mp4", "tmp/merged_audio.m4a", "out.mp4")
		wantMux := []string{
			"-y", "-hide_banner", "-loglevel", "error",
			"-stream_loop", "-1",
			"-i", "tmp/norm_loop.mp4",
			"-i", "tmp/merged_audio.m4a",
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "copy", "-c:a", "copy", "-shortest",
			"out.mp4",
		}
		if !reflect.DeepEqual(mux, wantMux) {
			t.Fatalf("Path A mux = %v\nwant:      %v", mux, wantMux)
		}
	})

	t.Run("Path B args escape Windows ASS path and include encoder args", func(t *testing.T) {
		args := BuildPathBArgs(
			"loop.mp4",
			"merged.m4a",
			`C:\tmp\O'Brien\titles.ass`,
			"out.mp4",
			[]string{"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "20"},
		)
		want := []string{
			"-y", "-hide_banner", "-loglevel", "error",
			"-stream_loop", "-1",
			"-i", "loop.mp4",
			"-i", "merged.m4a",
			"-vf", `scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,subtitles='C\:/tmp/O\'Brien/titles.ass',format=yuv420p`,
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "h264_nvenc", "-preset", "p4", "-cq", "20",
			"-c:a", "copy", "-shortest",
			"out.mp4",
		}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("Path B args = %v\nwant:        %v", args, want)
		}
	})

	t.Run("Path C args use framerate 2 without overlays and 10 with overlays", func(t *testing.T) {
		noSub := BuildPathCArgs("bg.png", "merged.m4a", "", "out.mp4", false)
		wantNoSub := []string{
			"-y", "-hide_banner", "-loglevel", "error",
			"-loop", "1", "-framerate", "2",
			"-i", "bg.png",
			"-i", "merged.m4a",
			"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p",
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "libx264", "-tune", "stillimage", "-preset", "veryfast", "-crf", "18",
			"-c:a", "copy", "-shortest",
			"out.mp4",
		}
		if !reflect.DeepEqual(noSub, wantNoSub) {
			t.Fatalf("Path C (no overlay) = %v\nwant:                 %v", noSub, wantNoSub)
		}

		withSub := BuildPathCArgs("bg.png", "merged.m4a", `C:\tmp\titles.ass`, "out.mp4", true)
		wantWithSub := []string{
			"-y", "-hide_banner", "-loglevel", "error",
			"-loop", "1", "-framerate", "10",
			"-i", "bg.png",
			"-i", "merged.m4a",
			"-vf", `scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,subtitles='C\:/tmp/titles.ass',format=yuv420p`,
			"-map", "0:v:0", "-map", "1:a:0",
			"-c:v", "libx264", "-tune", "stillimage", "-preset", "veryfast", "-crf", "18",
			"-c:a", "copy", "-shortest",
			"out.mp4",
		}
		if !reflect.DeepEqual(withSub, wantWithSub) {
			t.Fatalf("Path C (overlay) = %v\nwant:              %v", withSub, wantWithSub)
		}
	})
}

func TestParseProgressLine(t *testing.T) {
	var sample ProgressSample
	lines := []string{
		"frame=12",
		"out_time_us=N/A",
		"speed=N/A",
		"progress=continue",
		"out_time_us=5000000",
		"speed=2.45x",
		"progress=continue",
		"out_time_us=10000000",
		"speed=3.10x",
		"progress=end",
	}

	var readySamples []ProgressSample
	for _, line := range lines {
		if ParseProgressLine(line, &sample) {
			readySamples = append(readySamples, sample)
		}
	}
	if len(readySamples) != 3 {
		t.Fatalf("expected 3 ready samples, got %d: %+v", len(readySamples), readySamples)
	}
	if readySamples[0].OutTimeSec != 0 || readySamples[0].Speed != "" || readySamples[0].Done {
		t.Fatalf("sample[0] = %+v", readySamples[0])
	}
	if readySamples[1].OutTimeSec != 5.0 || readySamples[1].Speed != "2.45x" || readySamples[1].Done {
		t.Fatalf("sample[1] = %+v", readySamples[1])
	}
	if readySamples[2].OutTimeSec != 10.0 || readySamples[2].Speed != "3.10x" || !readySamples[2].Done {
		t.Fatalf("sample[2] = %+v", readySamples[2])
	}
}

func TestRenderExecution(t *testing.T) {
	t.Run("Path A runs pre-normalize then mux via CommandRunner fallback", func(t *testing.T) {
		tempDir := t.TempDir()
		var cmds [][]string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			cmds = append(cmds, append([]string{name}, args...))
			return nil, nil
		}
		p, err := Render(context.Background(), "loop.mp4", filepath.Join(tempDir, "merged_audio.m4a"), "", tempDir, "out.mp4", false, 10.0, "windows", runner, nil, nil)
		if err != nil {
			t.Fatalf("Render Path A error: %v", err)
		}
		if p != PathA {
			t.Fatalf("path = %q, want %q", p, PathA)
		}
		if len(cmds) != 2 {
			t.Fatalf("expected 2 ffmpeg calls, got %d: %v", len(cmds), cmds)
		}
		if !strings.HasSuffix(cmds[0][len(cmds[0])-1], "norm_loop.mp4") {
			t.Fatalf("first command should output norm_loop.mp4: %v", cmds[0])
		}
		if !strings.Contains(strings.Join(cmds[1], " "), "-c:v copy") {
			t.Fatalf("second command should stream copy video: %v", cmds[1])
		}
	})

	t.Run("Path A streams -progress pipe:1 across norm_loop (20-35%) and mux (35-99%)", func(t *testing.T) {
		tempDir := t.TempDir()
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"4.0"}}`), nil
			}
			return nil, nil
		}

		var streamCmds [][]string
		streamRunner := func(_ context.Context, onLine func(string), name string, args ...string) error {
			streamCmds = append(streamCmds, append([]string{name}, args...))
			if len(streamCmds) == 1 {
				for _, l := range []string{"out_time_us=2000000", "speed=1.50x", "progress=continue", "out_time_us=4000000", "speed=1.80x", "progress=end"} {
					onLine(l)
				}
			} else {
				for _, l := range []string{"out_time_us=5000000", "speed=8.00x", "progress=continue", "out_time_us=15000000", "speed=9.00x", "progress=end"} {
					onLine(l)
				}
			}
			return nil
		}

		var updates []ProgressUpdate
		p, err := Render(context.Background(), "loop.mp4", filepath.Join(tempDir, "merged_audio.m4a"), "", tempDir, "out.mp4", false, 10.0, "linux", runner, streamRunner, func(u ProgressUpdate) {
			updates = append(updates, u)
		})
		if err != nil {
			t.Fatalf("Render Path A stream error: %v", err)
		}
		if p != PathA {
			t.Fatalf("path = %q, want %q", p, PathA)
		}
		if len(streamCmds) != 2 {
			t.Fatalf("expected 2 streamed ffmpeg calls, got %d", len(streamCmds))
		}
		for i, cmd := range streamCmds {
			joined := strings.Join(cmd, " ")
			if !strings.Contains(joined, "-progress pipe:1") {
				t.Fatalf("cmd[%d] missing -progress pipe:1: %v", i, cmd)
			}
		}
		if len(updates) != 4 {
			t.Fatalf("expected 4 progress updates, got %d: %+v", len(updates), updates)
		}
		if math.Abs(updates[0].Fraction-(0.5*PathANormWeight)) > 0.001 || updates[0].Substage != "norm_loop" {
			t.Fatalf("update[0] = %+v", updates[0])
		}
		if math.Abs(updates[1].Fraction-PathANormWeight) > 0.001 || !updates[1].Done {
			t.Fatalf("update[1] = %+v", updates[1])
		}
		wantMidMux := PathANormWeight + 0.5*(1.0-PathANormWeight)
		if math.Abs(updates[2].Fraction-wantMidMux) > 0.001 || updates[2].Substage != "mux" || updates[2].Speed != "8.00x" {
			t.Fatalf("update[2] = %+v", updates[2])
		}
		if updates[3].Fraction != 1.0 || !updates[3].Done {
			t.Fatalf("update[3] = %+v (expected clamped 1.0 on overflow)", updates[3])
		}
	})

	t.Run("Path A handles unavailable GIF loop duration gracefully", func(t *testing.T) {
		tempDir := t.TempDir()
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"N/A"}}`), nil
			}
			return nil, nil
		}
		call := 0
		streamRunner := func(_ context.Context, onLine func(string), name string, args ...string) error {
			call++
			if call == 1 {
				onLine("out_time_us=1000000")
				onLine("progress=continue")
				onLine("progress=end")
			} else {
				onLine("out_time_us=10000000")
				onLine("progress=end")
			}
			return nil
		}

		var updates []ProgressUpdate
		_, err := Render(context.Background(), "anim.gif", filepath.Join(tempDir, "merged_audio.m4a"), "", tempDir, "out.mp4", false, 10.0, "linux", runner, streamRunner, func(u ProgressUpdate) {
			updates = append(updates, u)
		})
		if err != nil {
			t.Fatalf("Render Path A GIF error: %v", err)
		}
		if len(updates) != 3 {
			t.Fatalf("expected 3 updates, got %d: %+v", len(updates), updates)
		}
		if updates[0].Fraction <= 0 || updates[0].Fraction >= PathANormWeight {
			t.Fatalf("expected fallback mid-norm fraction, got %+v", updates[0])
		}
		if math.Abs(updates[1].Fraction-PathANormWeight) > 0.001 {
			t.Fatalf("expected PathANormWeight on norm end, got %+v", updates[1])
		}
		if updates[2].Fraction != 1.0 {
			t.Fatalf("expected 1.0 on mux end, got %+v", updates[2])
		}
	})

	t.Run("Path B probes HW encoder and falls back cleanly on probe failure", func(t *testing.T) {
		tempDir := t.TempDir()
		assPath := filepath.Join(tempDir, "titles.ass")
		var cmds [][]string
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) {
			cmds = append(cmds, append([]string{name}, args...))
			if len(args) > 0 && args[0] == "-v" {
				return nil, errors.New("nvenc unavailable")
			}
			return nil, nil
		}
		p, err := Render(context.Background(), "loop.gif", filepath.Join(tempDir, "merged_audio.m4a"), assPath, tempDir, "out.mp4", true, 20.0, "linux", runner, nil, nil)
		if err != nil {
			t.Fatalf("Render Path B error: %v", err)
		}
		if p != PathB {
			t.Fatalf("path = %q, want %q", p, PathB)
		}
		if len(cmds) != 2 {
			t.Fatalf("expected probe + render calls, got %d", len(cmds))
		}
		joined := strings.Join(cmds[1], " ")
		if !strings.Contains(joined, "-c:v libx264 -preset veryfast -crf 20") {
			t.Fatalf("expected libx264 fallback in %q", joined)
		}
	})

	t.Run("Path C streams progress monotonically from 0.0 to 1.0", func(t *testing.T) {
		tempDir := t.TempDir()
		streamRunner := func(_ context.Context, onLine func(string), name string, args ...string) error {
			for _, l := range []string{
				"out_time_us=5000000", "speed=2.00x", "progress=continue",
				"out_time_us=10000000", "speed=2.20x", "progress=continue",
				"out_time_us=20000000", "speed=2.50x", "progress=end",
			} {
				onLine(l)
			}
			return nil
		}
		var updates []ProgressUpdate
		p, err := Render(context.Background(), "cover.png", filepath.Join(tempDir, "merged_audio.m4a"), "", tempDir, "out.mp4", false, 20.0, "linux", nil, streamRunner, func(u ProgressUpdate) {
			updates = append(updates, u)
		})
		if err != nil {
			t.Fatalf("Render Path C error: %v", err)
		}
		if p != PathC {
			t.Fatalf("path = %q, want %q", p, PathC)
		}
		if len(updates) != 3 {
			t.Fatalf("expected 3 updates, got %d", len(updates))
		}
		if updates[0].Fraction != 0.25 || updates[1].Fraction != 0.5 || updates[2].Fraction != 1.0 || !updates[2].Done {
			t.Fatalf("unexpected updates: %+v", updates)
		}
	})

	t.Run("errors when overlayTitles is true without assPath", func(t *testing.T) {
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) { return nil, nil }
		if _, err := Render(context.Background(), "bg.png", "merged.m4a", "", t.TempDir(), "out.mp4", true, 10.0, "windows", runner, nil, nil); err == nil {
			t.Fatal("expected error when assPath is empty with overlayTitles=true")
		}
	})

	t.Run("creates missing parent directory for outPath", func(t *testing.T) {
		tempDir := t.TempDir()
		nestedOut := filepath.Join(tempDir, "exports", "2026", "mix.mp4")
		runner := func(_ context.Context, name string, args ...string) ([]byte, error) { return nil, nil }
		if _, err := Render(context.Background(), "cover.png", filepath.Join(tempDir, "merged.m4a"), "", tempDir, nestedOut, false, 10.0, "linux", runner, nil, nil); err != nil {
			t.Fatalf("Render nested outPath error: %v", err)
		}
		if _, err := os.Stat(filepath.Dir(nestedOut)); err != nil {
			t.Fatalf("expected Render to create parent directory %q: %v", filepath.Dir(nestedOut), err)
		}
	})
}
