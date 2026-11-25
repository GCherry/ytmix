package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPlanToolsRegistration(t *testing.T) {
	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	RegisterPlanTools(reg, PlanDeps{})

	// Nil registry should be a safe no-op.
	RegisterPlanTools(nil, PlanDeps{})

	resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	var listEnv struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &listEnv); err != nil {
		t.Fatalf("unmarshal tools/list: %v (%s)", err, resp)
	}
	if len(listEnv.Result.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d (%s)", len(listEnv.Result.Tools), resp)
	}
	if listEnv.Result.Tools[0].Name != "plan_mix" || listEnv.Result.Tools[1].Name != "preview_visualizer_frame" {
		t.Fatalf("unexpected tools: %+v", listEnv.Result.Tools)
	}
	for _, tool := range listEnv.Result.Tools {
		ann := tool.Annotations
		if ann == nil || ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint ||
			ann.DestructiveHint == nil || *ann.DestructiveHint ||
			ann.IdempotentHint == nil || !*ann.IdempotentHint ||
			ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Fatalf("expected read-only non-destructive idempotent local annotations on %s, got %+v", tool.Name, ann)
		}
	}
}

func TestPlanMix(t *testing.T) {
	okLookPath := func(bin string) (string, error) { return "/usr/bin/" + bin, nil }
	allFiltersOutput := []byte("Filters:\n .. color |->V c\n .. showfreqs A->V f\n TS gblur V->V b\n .. showwaves A->V w\n .. scale V->V s\n")

	t.Run("explicit tracks resolves titles, computes crossfade chapters and HH:MM:SS, and flags Path A vs Path B trade-off", func(t *testing.T) {
		dir := t.TempDir()
		bgVideo := filepath.Join(dir, "loop.mp4")
		if err := os.WriteFile(bgVideo, []byte("mp4"), 0644); err != nil {
			t.Fatal(err)
		}

		var ffmpegCalled bool
		var ffprobeTrackCalls int
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(reg, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "ffprobe" {
					if strings.Contains(strings.Join(args, " "), "format_tags=title") {
						ffprobeTrackCalls++
					}
					return []byte(`{"format":{"duration":"12.0"},"streams":[{"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"30/1"}]}`), nil
				}
				ffmpegCalled = true
				return nil, errors.New("no hwaccel")
			},
		})

		// Path A: looping video without overlayTitles
		resA, rpcErr, rawA := callToolRPC(t, reg, "plan_mix", map[string]any{
			"tracks": []map[string]any{
				{"fileName": "01 - Dawn_Song.mp3", "durationSec": 1800},
				{"fileName": "02 - Dusk.mp3", "displayTitle": "Dusk (Extended)", "durationSec": 1900},
			},
			"bg":            bgVideo,
			"crossfade":     5,
			"normalize":     true,
			"overlayTitles": false,
		})
		if rpcErr != nil || resA.IsError {
			t.Fatalf("Path A unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, resA, rawA)
		}
		if ffmpegCalled {
			t.Fatal("Path A plan_mix should not probe ffmpeg hw encoders")
		}
		if ffprobeTrackCalls != 0 {
			t.Fatalf("expected zero track ffprobe calls when durationSec > 0, got %d", ffprobeTrackCalls)
		}

		var gotA PlanMixResult
		if err := json.Unmarshal([]byte(resA.Content[0].Text), &gotA); err != nil {
			t.Fatalf("unmarshal Path A result: %v", err)
		}
		if gotA.RenderingPath != "Path A" || gotA.TrackCount != 2 {
			t.Fatalf("unexpected Path A summary: %+v", gotA)
		}
		// 1800 + 1900 - 5 = 3695s (>= 3600s -> HH:MM:SS format)
		if gotA.TotalDurationSec != 3695 {
			t.Fatalf("totalDurationSec = %v, want 3695", gotA.TotalDurationSec)
		}
		if gotA.Chapters[0].Title != "Dawn_Song" || gotA.Chapters[0].Timestamp != "00:00:00" || gotA.Chapters[0].StartSec != 0 {
			t.Fatalf("unexpected chapter 0: %+v", gotA.Chapters[0])
		}
		if gotA.Chapters[1].Title != "Dusk (Extended)" || gotA.Chapters[1].Timestamp != "00:29:55" || gotA.Chapters[1].StartSec != 1795 {
			t.Fatalf("unexpected chapter 1: %+v", gotA.Chapters[1])
		}
		if !strings.Contains(gotA.TimestampsText, "00:00:00 Dawn_Song\n00:29:55 Dusk (Extended)\n") {
			t.Fatalf("unexpected timestampsText:\n%s", gotA.TimestampsText)
		}
		if gotA.TitleOverlayPreview.Enabled || gotA.TitleOverlayPreview.CueCount != 0 || gotA.TitleOverlayPreview.FontName != "Arial" {
			t.Fatalf("unexpected titleOverlayPreview for Path A: %+v", gotA.TitleOverlayPreview)
		}
		if gotA.Estimate.LoopDurationSec != 12.0 || gotA.Estimate.LoopCycles == 0 {
			t.Fatalf("expected probed LoopDurationSec and LoopCycles, got %+v", gotA.Estimate)
		}
		if !strings.Contains(gotA.EstimateSummary, "Path A") {
			t.Fatalf("unexpected estimateSummary: %q", gotA.EstimateSummary)
		}
		joinedNotesA := strings.Join(gotA.TradeOffNotes, "\n")
		if !strings.Contains(joinedNotesA, "Path A") || !strings.Contains(joinedNotesA, "Path B") || !strings.Contains(joinedNotesA, "stream-copy") {
			t.Fatalf("expected Path A vs Path B stream-copy trade-off note, got:\n%s", joinedNotesA)
		}
		if len(gotA.Warnings) != 0 || !strings.Contains(resA.Content[0].Text, `"warnings": []`) {
			t.Fatalf("expected non-null empty warnings array, got: %v\n%s", gotA.Warnings, resA.Content[0].Text)
		}

		// Path B: same looping video with overlayTitles toggled on
		resB, rpcErr, rawB := callToolRPC(t, reg, "plan_mix", map[string]any{
			"tracks": []map[string]any{
				{"fileName": "01 - Dawn_Song.mp3", "durationSec": 1800},
				{"fileName": "02 - Dusk.mp3", "durationSec": 1900},
			},
			"bg":            bgVideo,
			"overlayTitles": true,
			"titleDuration": 10,
			"fontName":      "Inter",
			"dimTitles":     true,
		})
		if rpcErr != nil || resB.IsError {
			t.Fatalf("Path B unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, resB, rawB)
		}
		var gotB PlanMixResult
		if err := json.Unmarshal([]byte(resB.Content[0].Text), &gotB); err != nil {
			t.Fatalf("unmarshal Path B result: %v", err)
		}
		if gotB.RenderingPath != "Path B" {
			t.Fatalf("renderingPath = %q, want Path B", gotB.RenderingPath)
		}
		if !gotB.TitleOverlayPreview.Enabled || gotB.TitleOverlayPreview.CueCount != 2 ||
			!gotB.TitleOverlayPreview.DimTitles || gotB.TitleOverlayPreview.TitleDurationSec != 10 ||
			gotB.TitleOverlayPreview.FontName != "Inter" {
			t.Fatalf("unexpected titleOverlayPreview for Path B: %+v", gotB.TitleOverlayPreview)
		}
		joinedNotesB := strings.Join(gotB.TradeOffNotes, "\n")
		if !strings.Contains(joinedNotesB, "Path B") || !strings.Contains(joinedNotesB, "Path A") {
			t.Fatalf("expected Path B vs Path A trade-off note, got:\n%s", joinedNotesB)
		}
		if gotB.Estimate.VideoSec <= gotA.Estimate.VideoSec {
			t.Fatalf("expected Path B VideoSec (%v) > Path A VideoSec (%v)", gotB.Estimate.VideoSec, gotA.Estimate.VideoSec)
		}
	})

	t.Run("audioDir probing, Path C 2fps/10fps, and Path D black/4k planning perform zero disk writes", func(t *testing.T) {
		dir := t.TempDir()
		audioDir := filepath.Join(dir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"01_First.mp3", "02_Second.mp3"} {
			if err := os.WriteFile(filepath.Join(audioDir, name), []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		coverPNG := filepath.Join(dir, "cover.png")
		if err := os.WriteFile(coverPNG, []byte("png"), 0644); err != nil {
			t.Fatal(err)
		}

		var filterProbes int
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(reg, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "ffprobe" {
					return []byte(`{"format":{"duration":"90.0","tags":{"title":"Probed Title"}}}`), nil
				}
				if strings.Contains(strings.Join(args, " "), "-filters") {
					filterProbes++
					return allFiltersOutput, nil
				}
				return nil, errors.New("no nvenc")
			},
		})

		// Path C without overlayTitles (2 fps)
		resC, rpcErr, rawC := callToolRPC(t, reg, "plan_mix", map[string]any{
			"audioDir": audioDir,
			"bg":       coverPNG,
		})
		if rpcErr != nil || resC.IsError {
			t.Fatalf("Path C unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, resC, rawC)
		}
		var gotC PlanMixResult
		if err := json.Unmarshal([]byte(resC.Content[0].Text), &gotC); err != nil {
			t.Fatalf("unmarshal Path C: %v", err)
		}
		if gotC.RenderingPath != "Path C" || gotC.TrackCount != 2 || gotC.TotalDurationSec != 180 {
			t.Fatalf("unexpected Path C result: %+v", gotC)
		}
		if !strings.Contains(strings.Join(gotC.TradeOffNotes, "\n"), "2 fps") {
			t.Fatalf("expected Path C 2 fps note, got %v", gotC.TradeOffNotes)
		}

		// Path D 4k ember with fadeToBlack (probes -filters once)
		filterProbes = 0
		resD, rpcErr, rawD := callToolRPC(t, reg, "plan_mix", map[string]any{
			"audioDir":             audioDir,
			"visualizer":           "ember",
			"visualizerTheme":      "oled-ember",
			"visualizerResolution": "4k",
			"visualizerGlow":       false,
			"visualizerBrightness": 45,
			"fadeToBlack":          "1m",
			"overlayTitles":        true,
			"dimTitles":            true,
		})
		if rpcErr != nil || resD.IsError {
			t.Fatalf("Path D unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, resD, rawD)
		}
		if filterProbes != 1 {
			t.Fatalf("expected exactly 1 ffmpeg -filters probe for Path D plan_mix, got %d", filterProbes)
		}
		var gotD PlanMixResult
		if err := json.Unmarshal([]byte(resD.Content[0].Text), &gotD); err != nil {
			t.Fatalf("unmarshal Path D: %v", err)
		}
		if gotD.RenderingPath != "Path D" || gotD.TitleOverlayPreview.CueCount != 2 {
			t.Fatalf("unexpected Path D result: %+v", gotD)
		}
		joinedNotesD := strings.Join(gotD.TradeOffNotes, "\n")
		if !strings.Contains(joinedNotesD, "4k resolution") || !strings.Contains(joinedNotesD, "fadeToBlack") {
			t.Fatalf("expected 4k and fadeToBlack trade-off notes, got:\n%s", joinedNotesD)
		}

		// Path D black preset
		resBlack, rpcErr, _ := callToolRPC(t, reg, "plan_mix", map[string]any{
			"tracks":     []map[string]any{{"fileName": "01.mp3", "durationSec": 60}},
			"visualizer": "black",
		})
		if rpcErr != nil || resBlack.IsError {
			t.Fatalf("Path D black unexpected error: rpcErr=%v res=%+v", rpcErr, resBlack)
		}
		var gotBlack PlanMixResult
		if err := json.Unmarshal([]byte(resBlack.Content[0].Text), &gotBlack); err != nil {
			t.Fatalf("unmarshal black: %v", err)
		}
		if !strings.Contains(strings.Join(gotBlack.TradeOffNotes, "\n"), `preset "black"`) {
			t.Fatalf("expected black preset note, got %v", gotBlack.TradeOffNotes)
		}

		// Verify zero output artifacts were created anywhere in dir.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 { // tracks/ and cover.png only
			t.Fatalf("expected zero side-effect files in %s, got %d entries", dir, len(entries))
		}
	})

	t.Run("explicit tracks omitting durationSec probes path via ffprobe and preserves displayTitle overrides", func(t *testing.T) {
		dir := t.TempDir()
		track1 := filepath.Join(dir, "01_Night_Rain.mp3")
		track2 := filepath.Join(dir, "02_Dawn.mp3")
		notMP3 := filepath.Join(dir, "bad.wav")
		for _, p := range []string{track1, track2, notMP3} {
			if err := os.WriteFile(p, []byte("audio"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		var ffprobeCalls atomic.Int32
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(reg, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "ffprobe" {
					ffprobeCalls.Add(1)
					target := args[len(args)-1]
					if target == track1 {
						return []byte(`{"format":{"duration":"120.0","tags":{"title":"ID3 Night Rain"}}}`), nil
					}
					return []byte(`{"format":{"duration":"80.0","tags":{"title":"ID3 Dawn"}}}`), nil
				}
				if strings.Contains(strings.Join(args, " "), "-filters") {
					return allFiltersOutput, nil
				}
				return nil, errors.New("no nvenc")
			},
		})

		res, rpcErr, raw := callToolRPC(t, reg, "plan_mix", map[string]any{
			"tracks": []map[string]any{
				{"path": track1},
				{"path": track2, "displayTitle": "Custom Dawn Override"},
				{"fileName": "03 - Known Duration.mp3", "durationSec": 50},
			},
			"crossfade": 5,
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
		}
		if gotCalls := ffprobeCalls.Load(); gotCalls != 2 {
			t.Fatalf("expected exactly 2 ffprobe calls for path-only tracks, got %d", gotCalls)
		}

		var got PlanMixResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// 120 + 80 + 50 - 2*5 = 240
		if got.TrackCount != 3 || got.TotalDurationSec != 240 {
			t.Fatalf("unexpected trackCount/duration: %+v", got)
		}
		if got.Chapters[0].Title != "ID3 Night Rain" || got.Chapters[0].DurationSec != 120 || got.Chapters[0].StartSec != 0 {
			t.Fatalf("chapter 0 = %+v", got.Chapters[0])
		}
		if got.Chapters[1].Title != "Custom Dawn Override" || got.Chapters[1].DurationSec != 80 || got.Chapters[1].StartSec != 115 {
			t.Fatalf("chapter 1 = %+v", got.Chapters[1])
		}
		if got.Chapters[2].Title != "Known Duration" || got.Chapters[2].DurationSec != 50 || got.Chapters[2].StartSec != 190 {
			t.Fatalf("chapter 2 = %+v", got.Chapters[2])
		}

		// Invalid path-only track returns ErrorToolResult cleanly.
		for _, badTrack := range []map[string]any{
			{"path": filepath.Join(dir, "missing.mp3")},
			{"path": notMP3},
		} {
			eRes, eRPCErr, eRaw := callToolRPC(t, reg, "plan_mix", map[string]any{
				"tracks": []map[string]any{badTrack},
			})
			if eRPCErr != nil || !eRes.IsError {
				t.Fatalf("expected ErrorToolResult for %v, got rpcErr=%v raw=%s", badTrack, eRPCErr, eRaw)
			}
		}
	})

	t.Run("works gracefully with explicit tracks when ffmpeg is absent and returns errors cleanly", func(t *testing.T) {
		dir := t.TempDir()
		audioDir := filepath.Join(dir, "tracks")
		if err := os.Mkdir(audioDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(audioDir, "01.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		badBG := filepath.Join(dir, "clip.avi")
		if err := os.WriteFile(badBG, []byte("avi"), 0644); err != nil {
			t.Fatal(err)
		}

		regNoFFmpeg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(regNoFFmpeg, PlanDeps{
			LookPath: func(string) (string, error) { return "", errors.New("not found") },
		})

		res, rpcErr, raw := callToolRPC(t, regNoFFmpeg, "plan_mix", map[string]any{
			"tracks": []map[string]any{{"fileName": "01 - Ambient.mp3", "durationSec": 120}},
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("expected fallback estimate without ffmpeg, got rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
		}
		var got PlanMixResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Estimate.EncoderName != "libx264" || len(got.Warnings) == 0 {
			t.Fatalf("expected libx264 fallback and warning, got %+v", got)
		}

		regMissingFilter := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(regMissingFilter, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "-filters") {
					return allFiltersOutput, nil // lacks a3dscope
				}
				return nil, errors.New("boom")
			},
		})

		for _, badParams := range []any{
			map[string]any{},
			map[string]any{"tracks": []map[string]any{}},
			map[string]any{"audioDir": audioDir, "tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 0}}},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": -1}}},
			map[string]any{"tracks": []map[string]any{{"durationSec": 10}}},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "bg": badBG, "visualizer": "bars"},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "crossfade": -1},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "titleDuration": -2},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "visualizer": "unknown-preset"},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "visualizerTheme": "nope"},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "visualizerResolution": "720p"},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "visualizerBrightness": 0},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "visualizerBrightness": 101},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "fadeToBlack": "not-a-duration"},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "fadeToBlack": "-5s"},
			map[string]any{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "unknownField": true},
		} {
			_, rErr, rRaw := callToolRPC(t, regMissingFilter, "plan_mix", badParams)
			if rErr == nil || rErr.Code != ErrCodeInvalidParams {
				t.Fatalf("expected -32602 for %v, got %s", badParams, rRaw)
			}
		}

		for _, opErrParams := range []map[string]any{
			{"audioDir": filepath.Join(dir, "missing")},
			{"audioDir": badBG},
			{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "bg": filepath.Join(dir, "missing.png")},
			{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "bg": badBG},
			{"tracks": []map[string]any{{"fileName": "a.mp3", "durationSec": 10}}, "visualizer": "3dscope"},
		} {
			eRes, eRPCErr, eRaw := callToolRPC(t, regMissingFilter, "plan_mix", opErrParams)
			if eRPCErr != nil {
				t.Fatalf("expected ErrorToolResult (not RPC error) for %v, got %s", opErrParams, eRaw)
			}
			if !eRes.IsError || len(eRes.Content) == 0 || strings.TrimSpace(eRes.Content[0].Text) == "" {
				t.Fatalf("expected IsError=true with message for %v, got %+v", opErrParams, eRes)
			}
		}
	})
}

func TestPreviewVisualizerFrame(t *testing.T) {
	okLookPath := func(bin string) (string, error) { return "/usr/bin/" + bin, nil }
	allFiltersOutput := []byte("Filters:\n .. color |->V c\n .. showfreqs A->V f\n TS gblur V->V b\n .. showwaves A->V w\n .. scale V->V s\n")
	fakePNG := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01, 0x02}

	t.Run("renders synthetic preview frame with ASS overlay, returns base64 PNG + metadata, and cleans temp dir", func(t *testing.T) {
		var capturedArgs []string
		var seenTempDir string
		var sawASSFile bool

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(reg, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "ffmpeg" {
					return nil, errors.New("unexpected binary: " + name)
				}
				if strings.Contains(strings.Join(args, " "), "-filters") {
					return allFiltersOutput, nil
				}
				capturedArgs = append([]string(nil), args...)
				outPNG := args[len(args)-1]
				seenTempDir = filepath.Dir(outPNG)
				if _, err := os.Stat(filepath.Join(seenTempDir, "titles.ass")); err == nil {
					sawASSFile = true
				}
				if err := os.WriteFile(outPNG, fakePNG, 0600); err != nil {
					return nil, err
				}
				return nil, nil
			},
		})

		res, rpcErr, raw := callToolRPC(t, reg, "preview_visualizer_frame", map[string]any{
			"preset":        "ember",
			"theme":         "oled-ember",
			"resolution":    "1440p",
			"glow":          false,
			"brightness":    45,
			"overlayTitles": true,
			"dimTitles":     true,
			"titleText":     "Midnight Rain",
			"fadeToBlack":   "15m",
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
		}
		if !sawASSFile {
			t.Fatal("expected titles.ass to be written inside tempDir when overlayTitles=true")
		}
		if seenTempDir == "" {
			t.Fatal("expected tempDir to be created")
		}
		if _, err := os.Stat(seenTempDir); !os.IsNotExist(err) {
			t.Fatalf("expected tempDir %q to be cleaned up, stat err=%v", seenTempDir, err)
		}

		joined := strings.Join(capturedArgs, " ")
		for _, want := range []string{
			"-f lavfi -i aevalsrc=",
			"c=stereo:s=48000:d=5.50",
			"-filter_complex",
			"scale=2560:1440:flags=bicubic",
			"colorchannelmixer=rr=0.45:gg=0.45:bb=0.45",
			"titles.ass",
			"fade=t=out:st=900:d=15:color=black",
			"-map [vout] -ss 5 -frames:v 1",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("expected %q in ffmpeg preview args:\n%s", want, joined)
			}
		}

		if len(res.Content) != 2 {
			t.Fatalf("expected 2 content blocks (image + text), got %d", len(res.Content))
		}
		if res.Content[0].Type != "image" || res.Content[0].MimeType != "image/png" {
			t.Fatalf("unexpected image content block: %+v", res.Content[0])
		}
		decoded, err := base64.StdEncoding.DecodeString(res.Content[0].Data)
		if err != nil || string(decoded) != string(fakePNG) {
			t.Fatalf("decoded PNG mismatch: err=%v bytes=%v", err, decoded)
		}
		if res.Content[1].Type != "text" {
			t.Fatalf("expected text metadata block, got %+v", res.Content[1])
		}

		var meta PreviewVisualizerResult
		if err := json.Unmarshal([]byte(res.Content[1].Text), &meta); err != nil {
			t.Fatalf("unmarshal metadata JSON: %v", err)
		}
		if meta.Preset != "ember" || meta.Theme != "oled-ember" || meta.Resolution != "1440p" ||
			meta.Width != 2560 || meta.Height != 1440 || meta.Glow || meta.Brightness != 45 ||
			!meta.DimTitles || !meta.OverlayTitles || meta.AudioSource != "synthetic" ||
			meta.OffsetSec != 5.0 || meta.ByteSize != len(fakePNG) || meta.MimeType != "image/png" {
			t.Fatalf("unexpected metadata: %+v", meta)
		}
	})

	t.Run("supports real audioPath, black preset, zero offset, and cleans up temp dir on failure", func(t *testing.T) {
		dir := t.TempDir()
		mp3Path := filepath.Join(dir, "sample.mp3")
		if err := os.WriteFile(mp3Path, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}

		var capturedArgs []string
		var capturedASS string
		var failNext bool
		var lastTempDir string

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(reg, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "-filters") {
					return allFiltersOutput, nil
				}
				capturedArgs = append([]string(nil), args...)
				outPNG := args[len(args)-1]
				lastTempDir = filepath.Dir(outPNG)
				if assBytes, err := os.ReadFile(filepath.Join(lastTempDir, "titles.ass")); err == nil {
					capturedASS = string(assBytes)
				}
				if failNext {
					return nil, errors.New("ffmpeg exploded")
				}
				if err := os.WriteFile(outPNG, fakePNG, 0600); err != nil {
					return nil, err
				}
				return nil, nil
			},
		})

		// Real mp3 file with overlayTitles, dimTitles, and fadeToBlack
		resMP3, rpcErr, _ := callToolRPC(t, reg, "preview_visualizer_frame", map[string]any{
			"audioPath":     mp3Path,
			"offsetSec":     12.5,
			"overlayTitles": true,
			"dimTitles":     true,
			"titleText":     "Sleep Horizon",
			"fadeToBlack":   "15s",
		})
		if rpcErr != nil || resMP3.IsError {
			t.Fatalf("mp3 preview error: rpcErr=%v res=%+v", rpcErr, resMP3)
		}
		joinedMP3 := strings.Join(capturedArgs, " ")
		for _, want := range []string{
			"-ss 10.5 -i " + mp3Path,
			"titles.ass",
			"fade=t=out:st=4.5:d=15:color=black",
			"-map [vout] -ss 2 -frames:v 1",
		} {
			if !strings.Contains(joinedMP3, want) {
				t.Fatalf("expected %q in mp3 preview args:\n%s", want, joinedMP3)
			}
		}
		if strings.Contains(joinedMP3, "-f lavfi") {
			t.Fatalf("unexpected -f lavfi for mp3 preview:\n%s", joinedMP3)
		}
		if !strings.Contains(capturedASS, "&H789EB2AF") || !strings.Contains(capturedASS, "Sleep Horizon") {
			t.Fatalf("expected dim ASS style and title text in titles.ass:\n%s", capturedASS)
		}
		var metaMP3 PreviewVisualizerResult
		if err := json.Unmarshal([]byte(resMP3.Content[1].Text), &metaMP3); err != nil {
			t.Fatal(err)
		}
		if metaMP3.AudioSource != "file" || metaMP3.OffsetSec != 12.5 || metaMP3.Preset != "bars" || !metaMP3.Glow || !metaMP3.DimTitles || !metaMP3.OverlayTitles {
			t.Fatalf("unexpected mp3 metadata: %+v", metaMP3)
		}

		// Black preset with zero offset
		resBlack, rpcErr, _ := callToolRPC(t, reg, "preview_visualizer_frame", map[string]any{
			"preset":    "black",
			"offsetSec": 0,
		})
		if rpcErr != nil || resBlack.IsError {
			t.Fatalf("black preview error: rpcErr=%v res=%+v", rpcErr, resBlack)
		}
		joinedBlack := strings.Join(capturedArgs, " ")
		if !strings.Contains(joinedBlack, "color=c=black:s=1920x1080:r=10") || strings.Contains(joinedBlack, "-ss") {
			t.Fatalf("unexpected black preview args:\n%s", joinedBlack)
		}

		// FFmpeg failure cleans up tempDir and returns ErrorToolResult
		failNext = true
		resFail, rpcErr, _ := callToolRPC(t, reg, "preview_visualizer_frame", map[string]any{})
		if rpcErr != nil || !resFail.IsError {
			t.Fatalf("expected ErrorToolResult on ffmpeg failure, got rpcErr=%v res=%+v", rpcErr, resFail)
		}
		if _, err := os.Stat(lastTempDir); !os.IsNotExist(err) {
			t.Fatalf("expected tempDir %q to be removed after failure, stat err=%v", lastTempDir, err)
		}
	})

	t.Run("rejects invalid params and returns ErrorToolResult for missing ffmpeg, missing filter, or invalid audioPath", func(t *testing.T) {
		dir := t.TempDir()
		notMP3 := filepath.Join(dir, "clip.wav")
		if err := os.WriteFile(notMP3, []byte("wav"), 0644); err != nil {
			t.Fatal(err)
		}

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(reg, PlanDeps{
			LookPath: okLookPath,
			Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "-filters") {
					return allFiltersOutput, nil // lacks a3dscope
				}
				return nil, nil
			},
		})

		for _, badArgs := range []any{
			map[string]any{"preset": "nope"},
			map[string]any{"theme": "nope"},
			map[string]any{"resolution": "720p"},
			map[string]any{"brightness": 0},
			map[string]any{"brightness": 101},
			map[string]any{"offsetSec": -0.5},
			map[string]any{"fadeToBlack": "bad"},
			map[string]any{"fadeToBlack": "-1s"},
			map[string]any{"unknown": 1},
		} {
			_, rpcErr, raw := callToolRPC(t, reg, "preview_visualizer_frame", badArgs)
			if rpcErr == nil || rpcErr.Code != ErrCodeInvalidParams {
				t.Fatalf("expected -32602 for %v, got %s", badArgs, raw)
			}
		}

		for _, errArgs := range []map[string]any{
			{"preset": "3dscope"},
			{"audioPath": filepath.Join(dir, "missing.mp3")},
			{"audioPath": notMP3},
		} {
			res, rpcErr, raw := callToolRPC(t, reg, "preview_visualizer_frame", errArgs)
			if rpcErr != nil || !res.IsError {
				t.Fatalf("expected ErrorToolResult for %v, got rpcErr=%v raw=%s", errArgs, rpcErr, raw)
			}
		}

		regNoFFmpeg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterPlanTools(regNoFFmpeg, PlanDeps{
			LookPath: func(string) (string, error) { return "", errors.New("missing") },
		})
		resNoFFmpeg, rpcErr, _ := callToolRPC(t, regNoFFmpeg, "preview_visualizer_frame", map[string]any{})
		if rpcErr != nil || !resNoFFmpeg.IsError || !strings.Contains(resNoFFmpeg.Content[0].Text, "ffmpeg") {
			t.Fatalf("expected missing ffmpeg ErrorToolResult, got rpcErr=%v res=%+v", rpcErr, resNoFFmpeg)
		}
	})
}
