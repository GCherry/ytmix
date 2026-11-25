package render

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSegmentValidators(t *testing.T) {
	t.Run("ValidateTrackLayerMode", func(t *testing.T) {
		cases := []struct {
			in      TrackLayerMode
			want    TrackLayerMode
			wantErr bool
		}{
			{"", LayerBackdrop, false},
			{"  FULLSCREEN ", LayerFullscreen, false},
			{"card", LayerArtCard, false},
			{"Backdrop", LayerBackdrop, false},
			{"pip", "", true},
		}
		for _, tc := range cases {
			got, err := ValidateTrackLayerMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ValidateTrackLayerMode(%q) expected error, got %q", tc.in, got)
				}
				continue
			}
			if err != nil || got != tc.want {
				t.Errorf("ValidateTrackLayerMode(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
			}
		}
	})

	t.Run("ValidateTrackTransition", func(t *testing.T) {
		cases := []struct {
			in      TrackTransition
			want    TrackTransition
			wantErr bool
		}{
			{"", TransitionCut, false},
			{" cut ", TransitionCut, false},
			{"DIP", TransitionDip, false},
			{"crossfade", "", true},
		}
		for _, tc := range cases {
			got, err := ValidateTrackTransition(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ValidateTrackTransition(%q) expected error, got %q", tc.in, got)
				}
				continue
			}
			if err != nil || got != tc.want {
				t.Errorf("ValidateTrackTransition(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
			}
		}
	})

	t.Run("ValidateLayer0Spec", func(t *testing.T) {
		cases := []struct {
			name     string
			in       Layer0Spec
			wantMode Layer0Mode
			wantKind BackgroundKind
			wantErr  bool
		}{
			{
				name:     "empty defaults to black",
				in:       Layer0Spec{},
				wantMode: Layer0Black,
			},
			{
				name:     "media classifies image extension automatically",
				in:       Layer0Spec{Mode: " MEDIA ", BGPath: "cover.PNG"},
				wantMode: Layer0Media,
				wantKind: BackgroundKindImage,
			},
			{
				name:     "media classifies video extension automatically",
				in:       Layer0Spec{Mode: Layer0Media, BGPath: "loop.mp4"},
				wantMode: Layer0Media,
				wantKind: BackgroundKindVideo,
			},
			{
				name:    "media without BGPath errors",
				in:      Layer0Spec{Mode: Layer0Media},
				wantErr: true,
			},
			{
				name:    "media with unsupported ext errors",
				in:      Layer0Spec{Mode: Layer0Media, BGPath: "notes.txt"},
				wantErr: true,
			},
			{
				name:     "visualizer normalizes defaults",
				in:       Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars"}},
				wantMode: Layer0Visualizer,
			},
			{
				name:    "visualizer with invalid preset errors",
				in:      Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bogus"}},
				wantErr: true,
			},
			{
				name:    "negative fade-to-black errors",
				in:      Layer0Spec{Mode: Layer0Black, Visualizer: VisualizerConfig{FadeToBlack: -1 * time.Second}},
				wantErr: true,
			},
			{
				name:    "unknown mode errors",
				in:      Layer0Spec{Mode: "gradient"},
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := ValidateLayer0Spec(tc.in)
				if tc.wantErr {
					if err == nil {
						t.Fatalf("expected error, got %+v", got)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Mode != tc.wantMode {
					t.Fatalf("Mode = %q, want %q", got.Mode, tc.wantMode)
				}
				if tc.wantKind != "" && got.BGKind != tc.wantKind {
					t.Fatalf("BGKind = %q, want %q", got.BGKind, tc.wantKind)
				}
			})
		}
	})
}

func TestResolveSegmentedFPSAndDimensions(t *testing.T) {
	fpsCases := []struct {
		name string
		opts SegmentedRenderOptions
		want int
	}{
		{
			name: "all still/black without overlays -> 2 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Black},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerFullscreen, MediaPath: "a.jpg", MediaKind: BackgroundKindImage},
					{LayerMode: LayerArtCard, MediaPath: "b.png", MediaKind: BackgroundKindImage},
					{LayerMode: LayerBackdrop},
				},
			},
			want: 2,
		},
		{
			name: "all still/black with any OverlayTitles -> 10 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Media, BGPath: "bg.png", BGKind: BackgroundKindImage},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerFullscreen, MediaPath: "a.jpg", MediaKind: BackgroundKindImage, OverlayTitles: false},
					{LayerMode: LayerBackdrop, OverlayTitles: true},
				},
			},
			want: 10,
		},
		{
			name: "black visualizer preset without overlays -> 2 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "black"}},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerBackdrop},
					{LayerMode: LayerArtCard, MediaPath: "cover.jpg", MediaKind: BackgroundKindImage},
				},
			},
			want: 2,
		},
		{
			name: "any fullscreen video -> 30 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Black},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerFullscreen, MediaPath: "a.jpg", MediaKind: BackgroundKindImage},
					{LayerMode: LayerFullscreen, MediaPath: "loop.mp4", MediaKind: BackgroundKindVideo},
				},
			},
			want: 30,
		},
		{
			name: "any art card video -> 30 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Black},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerArtCard, MediaPath: "anim.gif", MediaKind: BackgroundKindVideo},
				},
			},
			want: 30,
		},
		{
			name: "L0 video visible under backdrop or card -> 30 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Media, BGPath: "loop.mp4", BGKind: BackgroundKindVideo},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerFullscreen, MediaPath: "a.jpg", MediaKind: BackgroundKindImage},
					{LayerMode: LayerArtCard, MediaPath: "b.jpg", MediaKind: BackgroundKindImage},
				},
			},
			want: 30,
		},
		{
			name: "L0 audio-reactive visualizer visible under backdrop -> 30 fps",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars"}},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerBackdrop},
				},
			},
			want: 30,
		},
		{
			name: "all fullscreen static images cover L0 visualizer -> stays 10 fps when titled",
			opts: SegmentedRenderOptions{
				Layer0: Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars"}},
				Segments: []TrackSegmentSpec{
					{LayerMode: LayerFullscreen, MediaPath: "a.jpg", MediaKind: BackgroundKindImage, OverlayTitles: true},
					{LayerMode: LayerFullscreen, MediaPath: "b.png", MediaKind: BackgroundKindImage},
				},
			},
			want: 10,
		},
	}

	for _, tc := range fpsCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSegmentedFPS(tc.opts); got != tc.want {
				t.Fatalf("ResolveSegmentedFPS() = %d, want %d", got, tc.want)
			}
		})
	}

	dimCases := []struct {
		name  string
		opts  SegmentedRenderOptions
		wantW int
		wantH int
	}{
		{
			name:  "Layer0Black honors Visualizer.Resolution 4k and defaults to 1920x1080 when empty",
			opts:  SegmentedRenderOptions{Layer0: Layer0Spec{Mode: Layer0Black, Visualizer: VisualizerConfig{Resolution: Resolution4K}}},
			wantW: 3840,
			wantH: 2160,
		},
		{
			name:  "Layer0Black defaults to 1920x1080 when resolution omitted",
			opts:  SegmentedRenderOptions{Layer0: Layer0Spec{Mode: Layer0Black}},
			wantW: 1920,
			wantH: 1080,
		},
		{
			name:  "Layer0Media defaults to 1920x1080",
			opts:  SegmentedRenderOptions{Layer0: Layer0Spec{Mode: Layer0Media, BGPath: "bg.png"}},
			wantW: 1920,
			wantH: 1080,
		},
		{
			name:  "Layer0Visualizer uses 1440p",
			opts:  SegmentedRenderOptions{Layer0: Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Resolution: Resolution1440p}}},
			wantW: 2560,
			wantH: 1440,
		},
		{
			name:  "Layer0Visualizer uses 4k",
			opts:  SegmentedRenderOptions{Layer0: Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Resolution: Resolution4K}}},
			wantW: 3840,
			wantH: 2160,
		},
	}
	for _, tc := range dimCases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := ResolveSegmentedDimensions(tc.opts)
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("ResolveSegmentedDimensions() = (%d, %d), want (%d, %d)", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestBuildSegmentArgsMatrix(t *testing.T) {
	enc := []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}

	cases := []struct {
		name        string
		seg         TrackSegmentSpec
		layer0      Layer0Spec
		transition  TrackTransition
		fps         int
		w, h        int
		totalDur    float64
		wantSubstrs []string
		omitSubstrs []string
		wantErr     bool
	}{
		{
			name: "fullscreen static image over black L0",
			seg: TrackSegmentSpec{
				Index:          0,
				StartSec:       0,
				SlotDuration:   30,
				MediaPath:      "cover.png",
				MediaKind:      BackgroundKindImage,
				LayerMode:      LayerFullscreen,
				SegmentASSPath: `C:\tmp\seg_0000.ass`,
				OverlayTitles:  true,
			},
			layer0:     Layer0Spec{Mode: Layer0Black},
			transition: TransitionCut,
			fps:        10,
			w:          1920,
			h:          1080,
			totalDur:   90,
			wantSubstrs: []string{
				"-loop 1 -framerate 10 -i cover.png",
				"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=10",
				"subtitles='C\\:/tmp/seg_0000.ass'",
				"format=yuv420p",
				"-pix_fmt yuv420p",
				"-colorspace bt709",
				"-video_track_timescale 90000",
				"-t 30 -an seg0.mp4",
			},
			omitSubstrs: []string{"fade=t=in", "fade=t=out", "overlay="},
		},
		{
			name: "fullscreen looping video ignores L0 visualizer",
			seg: TrackSegmentSpec{
				Index:        1,
				StartSec:     30,
				SlotDuration: 45,
				MediaPath:    "loop.mp4",
				MediaKind:    BackgroundKindVideo,
				LayerMode:    LayerFullscreen,
			},
			layer0:     Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars"}},
			transition: TransitionCut,
			fps:        30,
			w:          1920,
			h:          1080,
			totalDur:   90,
			wantSubstrs: []string{
				"-stream_loop -1 -i loop.mp4",
				"fps=30,format=yuv420p",
				"-t 45 -an seg0.mp4",
			},
			omitSubstrs: []string{"showfreqs", "merged.m4a", "overlay="},
		},
		{
			name: "backdrop L0 black",
			seg: TrackSegmentSpec{
				Index:        0,
				StartSec:     0,
				SlotDuration: 20,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Black},
			transition: TransitionCut,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   20,
			wantSubstrs: []string{
				"-f lavfi -i color=c=black:s=1920x1080:r=2",
				"setsar=1,format=yuv420p",
				"-t 20 -an seg0.mp4",
			},
		},
		{
			name: "backdrop L0 media image",
			seg: TrackSegmentSpec{
				Index:        0,
				StartSec:     0,
				SlotDuration: 25,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Media, BGPath: "backdrop.jpg", BGKind: BackgroundKindImage},
			transition: TransitionCut,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   25,
			wantSubstrs: []string{
				"-loop 1 -framerate 2 -i backdrop.jpg",
				"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=2,format=yuv420p",
			},
		},
		{
			name: "backdrop L0 media video",
			seg: TrackSegmentSpec{
				Index:        0,
				StartSec:     0,
				SlotDuration: 25,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Media, BGPath: "backdrop.mp4", BGKind: BackgroundKindVideo},
			transition: TransitionCut,
			fps:        30,
			w:          1920,
			h:          1080,
			totalDur:   25,
			wantSubstrs: []string{
				"-stream_loop -1 -i backdrop.mp4",
				"fps=30,format=yuv420p",
			},
		},
		{
			name: "backdrop L0 audio-reactive visualizer slices merged audio",
			seg: TrackSegmentSpec{
				Index:          1,
				StartSec:       62.5,
				SlotDuration:   40,
				LayerMode:      LayerBackdrop,
				SegmentASSPath: "/tmp/seg_0001.ass",
				OverlayTitles:  true,
			},
			layer0: Layer0Spec{
				Mode:       Layer0Visualizer,
				Visualizer: VisualizerConfig{Preset: "bars", Theme: "studio-mint", Resolution: Resolution1440p, Glow: true, Brightness: 80},
			},
			transition: TransitionCut,
			fps:        30,
			w:          2560,
			h:          1440,
			totalDur:   120,
			wantSubstrs: []string{
				"-ss 62.5 -t 40 -i merged.m4a",
				"showfreqs=s=2560x1440:r=30",
				"format=gbrp",
				"blend=all_mode=screen",
				"colorchannelmixer=rr=0.80:gg=0.80:bb=0.80",
				"subtitles='/tmp/seg_0001.ass'",
				"format=yuv420p[vout]",
				"-map [vout]",
				"-t 40 -an seg0.mp4",
			},
		},
		{
			name: "art card image over L0 black",
			seg: TrackSegmentSpec{
				Index:        0,
				StartSec:     0,
				SlotDuration: 30,
				MediaPath:    "album.jpg",
				MediaKind:    BackgroundKindImage,
				LayerMode:    LayerArtCard,
			},
			layer0:     Layer0Spec{Mode: Layer0Black},
			transition: TransitionCut,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   30,
			wantSubstrs: []string{
				"-f lavfi -i color=c=black:s=1920x1080:r=2",
				"-loop 1 -framerate 2 -i album.jpg",
				"[0:v]setsar=1,format=gbrp[l0]",
				"[1:v]scale=1190:668:force_original_aspect_ratio=decrease,setsar=1,fps=2,format=gbrp,pad=iw+16:ih+16:8:8:color=0x111316@0.85[card]",
				"[l0][card]overlay=(W-w)/2:(H-h)/2:shortest=1:format=gbrp,format=yuv420p[vout]",
			},
		},
		{
			name: "art card video over L0 media video",
			seg: TrackSegmentSpec{
				Index:        0,
				StartSec:     0,
				SlotDuration: 15,
				MediaPath:    "clip.mp4",
				MediaKind:    BackgroundKindVideo,
				LayerMode:    LayerArtCard,
			},
			layer0:     Layer0Spec{Mode: Layer0Media, BGPath: "l0.gif", BGKind: BackgroundKindVideo},
			transition: TransitionCut,
			fps:        30,
			w:          1920,
			h:          1080,
			totalDur:   30,
			wantSubstrs: []string{
				"-stream_loop -1 -i l0.gif",
				"-stream_loop -1 -i clip.mp4",
				"scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=gbrp[l0]",
				"scale=1190:668:force_original_aspect_ratio=decrease,setsar=1,fps=30,format=gbrp,pad=iw+16:ih+16:8:8:color=0x111316@0.85[card]",
				"overlay=(W-w)/2:(H-h)/2:shortest=1:format=gbrp,format=yuv420p[vout]",
			},
		},
		{
			name: "art card over L0 audio-reactive visualizer stays in planar gbrp until final yuv420p",
			seg: TrackSegmentSpec{
				Index:          1,
				StartSec:       20,
				SlotDuration:   25,
				MediaPath:      "art.png",
				MediaKind:      BackgroundKindImage,
				LayerMode:      LayerArtCard,
				SegmentASSPath: "/tmp/s1.ass",
				OverlayTitles:  true,
			},
			layer0: Layer0Spec{
				Mode:       Layer0Visualizer,
				Visualizer: VisualizerConfig{Preset: "waves", Theme: "midnight-cyan", Glow: true, Brightness: 100},
			},
			transition: TransitionCut,
			fps:        30,
			w:          1920,
			h:          1080,
			totalDur:   60,
			wantSubstrs: []string{
				"-ss 20 -t 25 -i merged.m4a",
				"-loop 1 -framerate 30 -i art.png",
				"showwaves=s=1920x1080:r=30",
				"[vpost]fps=30[vl0];",
				"[vl0][card]overlay=(W-w)/2:(H-h)/2:shortest=1:format=gbrp,subtitles='/tmp/s1.ass',format=yuv420p[vout]",
			},
		},
		{
			name: "art card over L0 visualizer black preset uses color source",
			seg: TrackSegmentSpec{
				Index:        0,
				StartSec:     0,
				SlotDuration: 10,
				MediaPath:    "art.png",
				MediaKind:    BackgroundKindImage,
				LayerMode:    LayerArtCard,
			},
			layer0:     Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "black"}},
			transition: TransitionCut,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   10,
			wantSubstrs: []string{
				"-f lavfi -i color=c=black:s=1920x1080:r=2",
				"-loop 1 -framerate 2 -i art.png",
			},
			omitSubstrs: []string{"merged.m4a"},
		},
		{
			name: "dip transition middle segment applies fade-in and fade-out",
			seg: TrackSegmentSpec{
				Index:        1,
				StartSec:     10,
				SlotDuration: 20,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Black},
			transition: TransitionDip,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   50,
			wantSubstrs: []string{
				"fade=t=in:st=0:d=0.5:color=black,fade=t=out:st=19.5:d=0.5:color=black",
			},
		},
		{
			name: "dip transition final segment omits fade-out",
			seg: TrackSegmentSpec{
				Index:        2,
				StartSec:     30,
				SlotDuration: 20,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Black},
			transition: TransitionDip,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   50,
			wantSubstrs: []string{
				"fade=t=in:st=0:d=0.5:color=black",
			},
			omitSubstrs: []string{"fade=t=out"},
		},
		{
			name: "dip transition clamps cleanly when slotDuration < 1.2s",
			seg: TrackSegmentSpec{
				Index:        1,
				StartSec:     5,
				SlotDuration: 0.8,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Black},
			transition: TransitionDip,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   10,
			wantSubstrs: []string{
				"fade=t=in:st=0:d=0.28:color=black,fade=t=out:st=0.52:d=0.28:color=black",
			},
		},
		{
			name: "fade-to-black starting inside segment uses local start and duration",
			seg: TrackSegmentSpec{
				Index:          0,
				StartSec:       0,
				SlotDuration:   20,
				LayerMode:      LayerBackdrop,
				SegmentASSPath: "/tmp/s0.ass",
				OverlayTitles:  true,
			},
			layer0:     Layer0Spec{Mode: Layer0Black, Visualizer: VisualizerConfig{FadeToBlack: 12 * time.Second}},
			transition: TransitionCut,
			fps:        10,
			w:          1920,
			h:          1080,
			totalDur:   30,
			wantSubstrs: []string{
				"subtitles='/tmp/s0.ass',fade=t=out:st=12:d=15:color=black,format=yuv420p",
			},
		},
		{
			name: "fade-to-black spanning prior boundary preserves continuous brightness",
			seg: TrackSegmentSpec{
				Index:        1,
				StartSec:     20,
				SlotDuration: 15,
				LayerMode:    LayerBackdrop,
			},
			layer0:     Layer0Spec{Mode: Layer0Black, Visualizer: VisualizerConfig{FadeToBlack: 12 * time.Second}},
			transition: TransitionCut,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   40,
			wantSubstrs: []string{
				"format=gbrp,colorchannelmixer=rr=0.47:gg=0.47:bb=0.47,fade=t=out:st=0:d=7:color=black,format=yuv420p",
			},
		},
		{
			name: "post-fade segment emits solid black drawbox",
			seg: TrackSegmentSpec{
				Index:        2,
				StartSec:     35,
				SlotDuration: 15,
				LayerMode:    LayerFullscreen,
				MediaPath:    "cover.png",
				MediaKind:    BackgroundKindImage,
			},
			layer0:     Layer0Spec{Mode: Layer0Black, Visualizer: VisualizerConfig{FadeToBlack: 12 * time.Second}},
			transition: TransitionCut,
			fps:        2,
			w:          1920,
			h:          1080,
			totalDur:   50,
			wantSubstrs: []string{
				"drawbox=x=0:y=0:w=iw:h=ih:color=black:t=fill,format=yuv420p",
			},
		},
		{
			name: "validation errors",
			seg: TrackSegmentSpec{
				Index:        0,
				SlotDuration: 0,
				LayerMode:    LayerBackdrop,
			},
			layer0:   Layer0Spec{Mode: Layer0Black},
			fps:      2,
			w:        1920,
			h:        1080,
			totalDur: 10,
			wantErr:  true,
		},
		{
			name: "missing MediaPath for fullscreen errors",
			seg: TrackSegmentSpec{
				Index:        0,
				SlotDuration: 10,
				LayerMode:    LayerFullscreen,
			},
			layer0:   Layer0Spec{Mode: Layer0Black},
			fps:      2,
			w:        1920,
			h:        1080,
			totalDur: 10,
			wantErr:  true,
		},
		{
			name: "missing SegmentASSPath when OverlayTitles errors",
			seg: TrackSegmentSpec{
				Index:         0,
				SlotDuration:  10,
				LayerMode:     LayerBackdrop,
				OverlayTitles: true,
			},
			layer0:   Layer0Spec{Mode: Layer0Black},
			fps:      10,
			w:        1920,
			h:        1080,
			totalDur: 10,
			wantErr:  true,
		},
		{
			name: "missing mergedAudioPath for reactive visualizer errors",
			seg: TrackSegmentSpec{
				Index:        0,
				SlotDuration: 10,
				LayerMode:    LayerBackdrop,
			},
			layer0:   Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars"}},
			fps:      30,
			w:        1920,
			h:        1080,
			totalDur: 10,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			audioPath := "merged.m4a"
			if tc.name == "missing mergedAudioPath for reactive visualizer errors" {
				audioPath = ""
			}
			args, err := BuildSegmentArgs(tc.seg, tc.layer0, audioPath, "seg0.mp4", tc.transition, tc.fps, tc.w, tc.h, tc.totalDur, enc)
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

func TestBuildSegmentConcatArgs(t *testing.T) {
	got := BuildSegmentConcatArgs("tmp/seg_concat.txt", "tmp/merged_audio.m4a", "mix.mp4")
	want := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0",
		"-i", "tmp/seg_concat.txt",
		"-i", "tmp/merged_audio.m4a",
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", "copy", "-c:a", "copy",
		"-movflags", "+faststart",
		"mix.mp4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildSegmentConcatArgs() = %v\nwant: %v", got, want)
	}
}

func TestRenderSegmentedExecution(t *testing.T) {
	t.Run("renders all segments in bounded parallel, writes seg_concat.txt, and streams monotonic progress", func(t *testing.T) {
		tempDir := t.TempDir()
		outPath := filepath.Join(tempDir, "exports", "mix.mp4")

		var mu sync.Mutex
		var streamCalls [][]string
		streamRunner := func(_ context.Context, onLine func(string), name string, args ...string) error {
			if name != "ffmpeg" {
				t.Fatalf("unexpected binary %q", name)
			}
			mu.Lock()
			streamCalls = append(streamCalls, append([]string(nil), args...))
			mu.Unlock()

			joined := strings.Join(args, " ")
			if strings.Contains(joined, "-f concat") {
				onLine("out_time_us=15000000")
				onLine("speed=20.0x")
				onLine("progress=continue")
				onLine("out_time_us=30000000")
				onLine("progress=end")
			} else {
				onLine("out_time_us=5000000")
				onLine("speed=3.0x")
				onLine("progress=continue")
				onLine("out_time_us=10000000")
				onLine("progress=end")
			}
			return nil
		}

		var progressMu sync.Mutex
		var updates []ProgressUpdate
		err := RenderSegmented(context.Background(), SegmentedRenderOptions{
			Layer0: Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars"}},
			Segments: []TrackSegmentSpec{
				{Index: 0, StartSec: 0, SlotDuration: 10, LayerMode: LayerFullscreen, MediaPath: "a.jpg", MediaKind: BackgroundKindImage},
				{Index: 1, StartSec: 10, SlotDuration: 10, LayerMode: LayerArtCard, MediaPath: "b.png", MediaKind: BackgroundKindImage},
				{Index: 2, StartSec: 20, SlotDuration: 10, LayerMode: LayerBackdrop},
			},
			MergedAudioPath: filepath.Join(tempDir, "merged_audio.m4a"),
			TempDir:         tempDir,
			OutPath:         outPath,
			Transition:      TransitionDip,
			TotalDuration:   30,
			GOOS:            "linux",
			Encoder:         &EncoderInfo{Name: "libx264", Args: []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}},
			StreamRunner:    streamRunner,
			OnProgress: func(u ProgressUpdate) {
				progressMu.Lock()
				updates = append(updates, u)
				progressMu.Unlock()
			},
		})
		if err != nil {
			t.Fatalf("RenderSegmented() error: %v", err)
		}

		if len(streamCalls) != 4 {
			t.Fatalf("expected 3 segment calls + 1 concat call = 4, got %d", len(streamCalls))
		}
		concatListBytes, err := os.ReadFile(filepath.Join(tempDir, "seg_concat.txt"))
		if err != nil {
			t.Fatalf("read seg_concat.txt: %v", err)
		}
		concatList := string(concatListBytes)
		for _, segName := range []string{"seg_0000.mp4", "seg_0001.mp4", "seg_0002.mp4"} {
			if !strings.Contains(concatList, segName) {
				t.Fatalf("seg_concat.txt missing %q:\n%s", segName, concatList)
			}
		}

		if len(updates) == 0 {
			t.Fatal("expected progress updates")
		}
		last := updates[len(updates)-1]
		if !last.Done || last.Fraction != 1.0 || last.Substage != "concat" {
			t.Fatalf("last update = %+v, want Done=true Fraction=1.0 Substage=concat", last)
		}
		for i, u := range updates {
			if u.Fraction < 0 || u.Fraction > 1.0 {
				t.Fatalf("update[%d] out of range [0,1]: %+v", i, u)
			}
		}
	})

	t.Run("returns segment error without running concat", func(t *testing.T) {
		tempDir := t.TempDir()
		var calls [][]string
		var mu sync.Mutex
		runner := func(_ context.Context, _ string, args ...string) ([]byte, error) {
			mu.Lock()
			calls = append(calls, args)
			mu.Unlock()
			return nil, errors.New("encode failed")
		}

		err := RenderSegmented(context.Background(), SegmentedRenderOptions{
			Layer0: Layer0Spec{Mode: Layer0Black},
			Segments: []TrackSegmentSpec{
				{Index: 0, StartSec: 0, SlotDuration: 5, LayerMode: LayerBackdrop},
			},
			MergedAudioPath: filepath.Join(tempDir, "merged.m4a"),
			TempDir:         tempDir,
			OutPath:         filepath.Join(tempDir, "out.mp4"),
			Runner:          runner,
		})
		if err == nil || !strings.Contains(err.Error(), "render track segment 0") {
			t.Fatalf("expected segment error, got: %v", err)
		}
		for _, c := range calls {
			if strings.Contains(strings.Join(c, " "), "-f concat") {
				t.Fatalf("concat should not execute after segment failure")
			}
		}
	})

	t.Run("validates empty segments and missing paths", func(t *testing.T) {
		err := RenderSegmented(context.Background(), SegmentedRenderOptions{
			MergedAudioPath: "merged.m4a",
			TempDir:         t.TempDir(),
			OutPath:         "out.mp4",
		})
		if err == nil {
			t.Fatal("expected error for empty segments")
		}
	})
}

func TestRenderSegmentedFFmpegSmoke(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("skipping real FFmpeg segmented smoke test: %v", err)
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skipf("skipping real FFmpeg segmented smoke test: %v", err)
	}

	tmp := t.TempDir()
	ctx := context.Background()
	mergedAudio := filepath.Join(tmp, "merged.m4a")
	img1 := filepath.Join(tmp, "img1.png")
	img2 := filepath.Join(tmp, "img2.png")
	outMP4 := filepath.Join(tmp, "mix.mp4")

	prepAudio := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1.8:sample_rate=48000",
		"-c:a", "aac", "-b:a", "128k", mergedAudio,
	)
	if out, err := prepAudio.CombinedOutput(); err != nil {
		t.Fatalf("prepare audio failed: %v\n%s", err, string(out))
	}

	for _, img := range []struct {
		path  string
		color string
	}{
		{img1, "color=c=navy:s=640x360"},
		{img2, "color=c=teal:s=400x400"},
	} {
		prepImg := exec.CommandContext(ctx, "ffmpeg",
			"-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", img.color,
			"-frames:v", "1", img.path,
		)
		if out, err := prepImg.CombinedOutput(); err != nil {
			t.Fatalf("prepare image %q failed: %v\n%s", img.path, err, string(out))
		}
	}

	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}

	err := RenderSegmented(ctx, SegmentedRenderOptions{
		Layer0: Layer0Spec{
			Mode:       Layer0Visualizer,
			Visualizer: VisualizerConfig{Preset: "bars", Theme: "studio-mint", Resolution: Resolution1080p, Glow: true, Brightness: 100},
		},
		Segments: []TrackSegmentSpec{
			{Index: 0, StartSec: 0.0, SlotDuration: 0.6, LayerMode: LayerFullscreen, MediaPath: img1, MediaKind: BackgroundKindImage},
			{Index: 1, StartSec: 0.6, SlotDuration: 0.6, LayerMode: LayerArtCard, MediaPath: img2, MediaKind: BackgroundKindImage},
			{Index: 2, StartSec: 1.2, SlotDuration: 0.6, LayerMode: LayerBackdrop},
		},
		MergedAudioPath: mergedAudio,
		TempDir:         tmp,
		OutPath:         outMP4,
		Transition:      TransitionDip,
		TotalDuration:   1.8,
		Encoder:         &EncoderInfo{Name: "libx264", Args: []string{"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28"}},
		Runner:          runner,
	})
	if err != nil {
		t.Fatalf("RenderSegmented real FFmpeg smoke test failed: %v", err)
	}

	probeOut, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,color_space:format=duration",
		"-of", "csv=p=0",
		outMP4,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe failed: %v\n%s", err, string(probeOut))
	}
	probeText := string(probeOut)
	if !strings.Contains(probeText, "h264,video,1920,1080,bt709") || !strings.Contains(probeText, "aac,audio") {
		t.Fatalf("unexpected ffprobe streams:\n%s", probeText)
	}
}
