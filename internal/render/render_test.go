package render

import (
	"errors"
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
		runner := func(name string, args ...string) ([]byte, error) {
			if name != "ffmpeg" {
				t.Fatalf("unexpected binary %q", name)
			}
			probedArgs = args
			return nil, nil
		}
		got := DetectEncoderArgs("darwin", runner)
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
			runner := func(name string, args ...string) ([]byte, error) {
				probedArgs = args
				return nil, nil
			}
			got := DetectEncoderArgs(goos, runner)
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
		failRunner := func(name string, args ...string) ([]byte, error) {
			return nil, errors.New("no gpu")
		}
		wantFallback := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}
		for _, goos := range []string{"darwin", "windows", "linux"} {
			if got := DetectEncoderArgs(goos, failRunner); !reflect.DeepEqual(got, wantFallback) {
				t.Fatalf("%s: got %v, want %v", goos, got, wantFallback)
			}
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

func TestRenderExecution(t *testing.T) {
	t.Run("Path A runs pre-normalize then mux", func(t *testing.T) {
		tempDir := t.TempDir()
		var cmds [][]string
		runner := func(name string, args ...string) ([]byte, error) {
			cmds = append(cmds, append([]string{name}, args...))
			return nil, nil
		}
		p, err := Render("loop.mp4", filepath.Join(tempDir, "merged_audio.m4a"), "", tempDir, "out.mp4", false, "windows", runner)
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

	t.Run("Path B probes HW encoder and falls back cleanly on probe failure", func(t *testing.T) {
		tempDir := t.TempDir()
		assPath := filepath.Join(tempDir, "titles.ass")
		var cmds [][]string
		runner := func(name string, args ...string) ([]byte, error) {
			cmds = append(cmds, append([]string{name}, args...))
			if len(args) > 0 && args[0] == "-v" {
				return nil, errors.New("nvenc unavailable")
			}
			return nil, nil
		}
		p, err := Render("loop.gif", filepath.Join(tempDir, "merged_audio.m4a"), assPath, tempDir, "out.mp4", true, "linux", runner)
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

	t.Run("errors when overlayTitles is true without assPath", func(t *testing.T) {
		runner := func(name string, args ...string) ([]byte, error) { return nil, nil }
		if _, err := Render("bg.png", "merged.m4a", "", t.TempDir(), "out.mp4", true, "windows", runner); err == nil {
			t.Fatal("expected error when assPath is empty with overlayTitles=true")
		}
	})

	t.Run("creates missing parent directory for outPath", func(t *testing.T) {
		tempDir := t.TempDir()
		nestedOut := filepath.Join(tempDir, "exports", "2026", "mix.mp4")
		runner := func(name string, args ...string) ([]byte, error) { return nil, nil }
		if _, err := Render("cover.png", filepath.Join(tempDir, "merged.m4a"), "", tempDir, nestedOut, false, "linux", runner); err != nil {
			t.Fatalf("Render nested outPath error: %v", err)
		}
		if _, err := os.Stat(filepath.Dir(nestedOut)); err != nil {
			t.Fatalf("expected Render to create parent directory %q: %v", filepath.Dir(nestedOut), err)
		}
	})
}
