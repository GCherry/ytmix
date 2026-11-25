package render

import (
	"math"
	"testing"
)

func TestParseFFmpegSpeed(t *testing.T) {
	cases := []struct {
		in     string
		want   float64
		wantOK bool
	}{
		{"2.45x", 2.45, true},
		{" 1.44e+03x ", 1440, true},
		{"0.85X", 0.85, true},
		{"N/A", 0, false},
		{"", 0, false},
		{"0x", 0, false},
		{"-1.2x", 0, false},
		{"NaNx", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseFFmpegSpeed(tc.in)
		if ok != tc.wantOK || (ok && math.Abs(got-tc.want) > 1e-6) {
			t.Errorf("ParseFFmpegSpeed(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestEstimateMix(t *testing.T) {
	t.Run("Path A long Mix shifts weight to Step 2 audio and computes loop cycles", func(t *testing.T) {
		est := EstimateMix(EstimateInput{
			Path:           PathA,
			EncoderName:    "libx264",
			MixDurationSec: 3600,
			TotalAudioSec:  3610,
			TrackCount:     10,
			Normalize:      true,
			Crossfade:      1,
			CPUCores:       8,
			BG: BackgroundEstimateInput{
				DurationSec: 12,
				FPS:         30,
				Width:       1920,
				Height:      1080,
			},
		}, nil)

		if est.AudioSec <= est.VideoSec {
			t.Fatalf("expected Path A 60m Mix audio (%v) > video (%v)", est.AudioSec, est.VideoSec)
		}
		if est.LoopCycles != 300 {
			t.Fatalf("LoopCycles = %d, want 300", est.LoopCycles)
		}
		if est.Weights.Audio <= 0.75 {
			t.Fatalf("expected Audio weight > 0.75 on Path A 60m Mix, got %v", est.Weights.Audio)
		}
		if est.LowSec >= est.PointSec || est.PointSec >= est.HighSec {
			t.Fatalf("expected Low < Point < High, got %+v", est)
		}
	})

	t.Run("Path B scales linearly with Background Visual FPS", func(t *testing.T) {
		est30 := EstimateMix(EstimateInput{
			Path:           PathB,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     4,
			OverlayTitles:  true,
			CPUCores:       8,
			BG:             BackgroundEstimateInput{DurationSec: 10, FPS: 30, Width: 1920, Height: 1080},
		}, nil)
		est60 := EstimateMix(EstimateInput{
			Path:           PathB,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     4,
			OverlayTitles:  true,
			CPUCores:       8,
			BG:             BackgroundEstimateInput{DurationSec: 10, FPS: 60, Width: 1920, Height: 1080},
		}, nil)
		if est60.VideoSec <= est30.VideoSec*1.7 {
			t.Fatalf("expected 60fps Path B video (%v) to be ~2x 30fps (%v)", est60.VideoSec, est30.VideoSec)
		}
	})

	t.Run("Path C 10fps overlay is slower than 2fps stillimage", func(t *testing.T) {
		est2 := EstimateMix(EstimateInput{
			Path:           PathC,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     3,
			OverlayTitles:  false,
			CPUCores:       8,
		}, nil)
		est10 := EstimateMix(EstimateInput{
			Path:           PathC,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     3,
			OverlayTitles:  true,
			CPUCores:       8,
		}, nil)
		if est10.VideoSec <= est2.VideoSec*1.8 {
			t.Fatalf("expected Path C 10fps (%v) significantly slower than 2fps (%v)", est10.VideoSec, est2.VideoSec)
		}
	})

	t.Run("Path D scales with resolution and black preset stays fast", func(t *testing.T) {
		estBlack := EstimateMix(EstimateInput{
			Path:           PathD,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     3,
			CPUCores:       8,
			Visualizer:     VisualizerConfig{Preset: "black", Resolution: Resolution1080p},
		}, nil)
		est1080 := EstimateMix(EstimateInput{
			Path:           PathD,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     3,
			CPUCores:       8,
			Visualizer:     VisualizerConfig{Preset: "bars", Resolution: Resolution1080p, Glow: true},
		}, nil)
		est4K := EstimateMix(EstimateInput{
			Path:           PathD,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     3,
			CPUCores:       8,
			Visualizer:     VisualizerConfig{Preset: "bars", Resolution: Resolution4K, Glow: true},
		}, nil)
		if est1080.VideoSec <= estBlack.VideoSec*3 {
			t.Fatalf("expected reactive Path D (%v) much slower than black (%v)", est1080.VideoSec, estBlack.VideoSec)
		}
		if est4K.VideoSec <= est1080.VideoSec*2.5 {
			t.Fatalf("expected 4K Path D (%v) much slower than 1080p (%v)", est4K.VideoSec, est1080.VideoSec)
		}
	})

	t.Run("calibrated speeds tighten confidence bounds and shift PointSec", func(t *testing.T) {
		cal := map[string]CalibrationEntry{
			CalibrationKey(PathB, "libx264", Resolution1080p): {Speed: 5.0, Samples: 3},
		}
		uncal := EstimateMix(EstimateInput{
			Path:           PathB,
			EncoderName:    "libx264",
			MixDurationSec: 300,
			TotalAudioSec:  300,
			TrackCount:     2,
			OverlayTitles:  true,
			CPUCores:       8,
			BG:             BackgroundEstimateInput{DurationSec: 10, FPS: 30, Width: 1920, Height: 1080},
		}, nil)
		withCal := EstimateMix(EstimateInput{
			Path:           PathB,
			EncoderName:    "libx264",
			MixDurationSec: 300,
			TotalAudioSec:  300,
			TrackCount:     2,
			OverlayTitles:  true,
			CPUCores:       8,
			BG:             BackgroundEstimateInput{DurationSec: 10, FPS: 30, Width: 1920, Height: 1080},
		}, cal)

		if !withCal.Calibrated || uncal.Calibrated {
			t.Fatalf("expected calibrated flags (uncal=%v, withCal=%v)", uncal.Calibrated, withCal.Calibrated)
		}
		if withCal.VideoSec <= uncal.VideoSec {
			t.Fatalf("expected slower calibrated speed (5x) to raise VideoSec (%v > %v)", withCal.VideoSec, uncal.VideoSec)
		}
		uncalSpread := (uncal.HighSec - uncal.LowSec) / uncal.PointSec
		calSpread := (withCal.HighSec - withCal.LowSec) / withCal.PointSec
		if calSpread >= uncalSpread {
			t.Fatalf("expected narrower spread when calibrated (%v < %v)", calSpread, uncalSpread)
		}
	})

	t.Run("CalibrationKey separates PathC 2fps vs 10fps and PathD black tiers", func(t *testing.T) {
		k2 := CalibrationKey(PathC, "libx264", "2fps")
		k10 := CalibrationKey(PathC, "libx264", "10fps")
		if k2 == k10 {
			t.Fatalf("PathC keys collided: %q == %q", k2, k10)
		}
		b2 := CalibrationKey(PathD, "black-2fps", Resolution1080p)
		b10 := CalibrationKey(PathD, "black-10fps", Resolution1080p)
		if b2 == b10 {
			t.Fatalf("PathD black keys collided: %q == %q", b2, b10)
		}
	})

	t.Run("PathB 60fps, PathD 4K, and PathA norm_loop+mux converge without double-counting", func(t *testing.T) {
		inB := EstimateInput{
			Path:           PathB,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     2,
			OverlayTitles:  true,
			CPUCores:       8,
			BG:             BackgroundEstimateInput{DurationSec: 10, FPS: 60, Width: 1920, Height: 1080},
		}
		preB := EstimateMix(inB, nil)
		observedWallB := 120.0 // 5.0x realtime -> normalized work speed = VideoWorkSec / 120 = 10.0
		calB := map[string]CalibrationEntry{
			CalibrationKey(PathB, "libx264", Resolution1080p): {Speed: preB.VideoWorkSec / observedWallB, Samples: 2},
		}
		postB := EstimateMix(inB, calB)
		if !postB.Calibrated || math.Abs(postB.VideoSec-observedWallB) > 1.0 {
			t.Fatalf("PathB 60fps converged VideoSec=%v, want ~%v (calibrated=%v)", postB.VideoSec, observedWallB, postB.Calibrated)
		}

		inD := EstimateInput{
			Path:           PathD,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     2,
			CPUCores:       8,
			Visualizer:     VisualizerConfig{Preset: "bars", Resolution: Resolution4K, Glow: true},
		}
		preD := EstimateMix(inD, nil)
		observedWallD := 300.0
		calD := map[string]CalibrationEntry{
			CalibrationKey(PathD, "libx264", Resolution4K): {Speed: preD.VideoWorkSec / observedWallD, Samples: 2},
		}
		postD := EstimateMix(inD, calD)
		if !postD.Calibrated || math.Abs(postD.VideoSec-observedWallD) > 1.0 {
			t.Fatalf("PathD 4K converged VideoSec=%v, want ~%v (calibrated=%v)", postD.VideoSec, observedWallD, postD.Calibrated)
		}

		inA := EstimateInput{
			Path:           PathA,
			EncoderName:    "libx264",
			MixDurationSec: 600,
			TotalAudioSec:  600,
			TrackCount:     2,
			CPUCores:       8,
			BG:             BackgroundEstimateInput{DurationSec: 12, FPS: 30, Width: 1920, Height: 1080},
		}
		calA := map[string]CalibrationEntry{
			CalibrationKey(PathA, "norm_loop", Resolution1080p): {Speed: 8.0, Samples: 2},
			CalibrationKey(PathA, "mux", Resolution1080p):       {Speed: 600.0, Samples: 2},
		}
		postA := EstimateMix(inA, calA)
		if !postA.Calibrated {
			t.Fatalf("expected PathA to be Calibrated=true when norm_loop and mux both have >=2 samples")
		}
	})

	t.Run("Segmented estimation sums per-segment render estimates and respects uniform FPS", func(t *testing.T) {
		estStill2 := EstimateMix(EstimateInput{
			EncoderName:    "libx264",
			MixDurationSec: 300,
			TotalAudioSec:  300,
			TrackCount:     2,
			CPUCores:       8,
			Segmented:      true,
			Layer0:         Layer0Spec{Mode: Layer0Black},
			Segments: []SegmentEstimateInput{
				{SlotDurationSec: 150, LayerMode: LayerFullscreen, MediaKind: BackgroundKindImage},
				{SlotDurationSec: 150, LayerMode: LayerBackdrop},
			},
		}, nil)

		estStill10 := EstimateMix(EstimateInput{
			EncoderName:    "libx264",
			MixDurationSec: 300,
			TotalAudioSec:  300,
			TrackCount:     2,
			CPUCores:       8,
			Segmented:      true,
			Layer0:         Layer0Spec{Mode: Layer0Black},
			Segments: []SegmentEstimateInput{
				{SlotDurationSec: 150, LayerMode: LayerFullscreen, MediaKind: BackgroundKindImage, OverlayTitles: true},
				{SlotDurationSec: 150, LayerMode: LayerBackdrop},
			},
		}, nil)

		estReactiveViz := EstimateMix(EstimateInput{
			EncoderName:    "libx264",
			MixDurationSec: 300,
			TotalAudioSec:  300,
			TrackCount:     2,
			CPUCores:       8,
			Segmented:      true,
			Layer0:         Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars", Resolution: Resolution1080p, Glow: true}},
			Segments: []SegmentEstimateInput{
				{SlotDurationSec: 150, LayerMode: LayerArtCard, MediaKind: BackgroundKindImage},
				{SlotDurationSec: 150, LayerMode: LayerBackdrop},
			},
		}, nil)

		estReactiveViz4K := EstimateMix(EstimateInput{
			EncoderName:    "libx264",
			MixDurationSec: 300,
			TotalAudioSec:  300,
			TrackCount:     2,
			CPUCores:       8,
			Segmented:      true,
			Layer0:         Layer0Spec{Mode: Layer0Visualizer, Visualizer: VisualizerConfig{Preset: "bars", Resolution: Resolution4K, Glow: true}},
			Segments: []SegmentEstimateInput{
				{SlotDurationSec: 150, LayerMode: LayerArtCard, MediaKind: BackgroundKindImage},
				{SlotDurationSec: 150, LayerMode: LayerBackdrop},
			},
		}, nil)

		if estStill10.VideoSec <= estStill2.VideoSec*1.5 {
			t.Fatalf("expected 10fps segmented (%v) slower than 2fps segmented (%v)", estStill10.VideoSec, estStill2.VideoSec)
		}
		if estReactiveViz.VideoSec <= estStill10.VideoSec*2.0 {
			t.Fatalf("expected reactive visualizer segmented (%v) slower than 10fps still (%v)", estReactiveViz.VideoSec, estStill10.VideoSec)
		}
		if estReactiveViz4K.VideoSec <= estReactiveViz.VideoSec*2.5 {
			t.Fatalf("expected 4K segmented (%v) slower than 1080p segmented (%v)", estReactiveViz4K.VideoSec, estReactiveViz.VideoSec)
		}
		if estReactiveViz.LoopCycles != 0 {
			t.Fatalf("expected LoopCycles=0 in segmented mode, got %d", estReactiveViz.LoopCycles)
		}
		if estReactiveViz.LowSec >= estReactiveViz.PointSec || estReactiveViz.PointSec >= estReactiveViz.HighSec {
			t.Fatalf("expected Low < Point < High, got %+v", estReactiveViz)
		}
	})

	t.Run("Segmented estimation scales proportionally with summed segment durations and supports calibration", func(t *testing.T) {
		in := EstimateInput{
			EncoderName:    "libx264",
			MixDurationSec: 200,
			TotalAudioSec:  200,
			TrackCount:     2,
			CPUCores:       8,
			Segmented:      true,
			Layer0:         Layer0Spec{Mode: Layer0Media, BGKind: BackgroundKindVideo},
			Segments: []SegmentEstimateInput{
				{SlotDurationSec: 100, LayerMode: LayerArtCard, MediaKind: BackgroundKindImage},
				{SlotDurationSec: 100, LayerMode: LayerBackdrop},
			},
		}
		inDouble := in
		inDouble.MixDurationSec = 400
		inDouble.TotalAudioSec = 400
		inDouble.Segments = []SegmentEstimateInput{
			{SlotDurationSec: 200, LayerMode: LayerArtCard, MediaKind: BackgroundKindImage},
			{SlotDurationSec: 200, LayerMode: LayerBackdrop},
		}

		est1 := EstimateMix(in, nil)
		est2 := EstimateMix(inDouble, nil)
		if est2.VideoSec <= est1.VideoSec*1.8 || est2.VideoSec >= est1.VideoSec*2.1 {
			t.Fatalf("expected doubled segment durations to ~2x VideoSec (est1=%v, est2=%v)", est1.VideoSec, est2.VideoSec)
		}

		cal := map[string]CalibrationEntry{
			CalibrationKey(PathB, "libx264", Resolution1080p): {Speed: 6.0, Samples: 3},
		}
		estCal := EstimateMix(in, cal)
		if !estCal.Calibrated {
			t.Fatalf("expected Calibrated=true when PathB key has >=2 samples")
		}
		if estCal.VideoSec <= est1.VideoSec {
			t.Fatalf("expected slower calibrated speed (6x) to raise VideoSec (%v > %v)", estCal.VideoSec, est1.VideoSec)
		}
	})
}
