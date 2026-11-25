package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func callToolRPC(t *testing.T, reg *Registry, name string, args any) (CallToolResult, *RPCError, string) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	rawReq, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rawResp := reg.HandleMessage(context.Background(), rawReq)
	var env struct {
		Result CallToolResult `json:"result"`
		Error  *RPCError      `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &env); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, rawResp)
	}
	return env.Result, env.Error, string(rawResp)
}

func TestInspectToolsRegistration(t *testing.T) {
	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	RegisterInspectTools(reg, InspectDeps{})

	// Nil registry should be a safe no-op.
	RegisterInspectTools(nil, InspectDeps{})

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
	if listEnv.Result.Tools[0].Name != "inspect_background" || listEnv.Result.Tools[1].Name != "inspect_tracks" {
		t.Fatalf("unexpected tools: %+v", listEnv.Result.Tools)
	}
	for _, tool := range listEnv.Result.Tools {
		ann := tool.Annotations
		if ann == nil || ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint ||
			ann.DestructiveHint == nil || *ann.DestructiveHint ||
			ann.IdempotentHint == nil || !*ann.IdempotentHint {
			t.Fatalf("expected read-only non-destructive idempotent annotations on %s, got %+v", tool.Name, ann)
		}
	}
}

func TestInspectTracks(t *testing.T) {
	t.Run("audits sorted tracks, ID3 vs filename titles, crossfade duration, and empty warnings JSON array", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"02_Alpha.mp3", "01_Beta.MP3", "notes.txt"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterInspectTools(reg, InspectDeps{
			Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "ffprobe" {
					return nil, errors.New("unexpected binary: " + name)
				}
				target := args[len(args)-1]
				if strings.HasSuffix(target, "01_Beta.MP3") {
					return []byte(`{"format":{"duration":"120.0","tags":{"title":"  Beta Waves  "}}}`), nil
				}
				return []byte(`{"format":{"duration":"180.0","tags":{"title":"Alpha Horizon"}}}`), nil
			},
		})

		res, rpcErr, raw := callToolRPC(t, reg, "inspect_tracks", map[string]any{
			"audioDir":  dir,
			"crossfade": 5,
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
		}

		var got InspectTracksResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
			t.Fatalf("unmarshal text JSON: %v", err)
		}
		if got.AudioDir != dir || got.TrackCount != 2 || got.CrossfadeSec != 5 {
			t.Fatalf("unexpected summary: %+v", got)
		}
		if got.TotalDurationSec != 300 || got.EstimatedMixDurationSec != 295 {
			t.Fatalf("durations: total=%v estMix=%v, want 300 and 295", got.TotalDurationSec, got.EstimatedMixDurationSec)
		}
		if len(got.Warnings) != 0 {
			t.Fatalf("expected 0 warnings, got %v", got.Warnings)
		}
		if !strings.Contains(res.Content[0].Text, `"warnings": []`) {
			t.Fatalf("expected non-null empty warnings array in JSON, got:\n%s", res.Content[0].Text)
		}
		if got.Tracks[0].Index != 0 || got.Tracks[0].FileName != "01_Beta.MP3" || got.Tracks[0].RawID3Title != "Beta Waves" || got.Tracks[0].DisplayTitle != "Beta Waves" {
			t.Fatalf("tracks[0] = %+v", got.Tracks[0])
		}
		if got.Tracks[1].Index != 1 || got.Tracks[1].FileName != "02_Alpha.mp3" || got.Tracks[1].RawID3Title != "Alpha Horizon" || got.Tracks[1].DisplayTitle != "Alpha Horizon" {
			t.Fatalf("tracks[1] = %+v", got.Tracks[1])
		}
	})

	t.Run("emits crossfade safety and messy filename warnings accurately", func(t *testing.T) {
		dir := t.TempDir()
		files := []string{
			"01_midnight_final_v2.mp3",
			"02 - Meditation.mp3",
			"Clean Song.mp3",
			"Tagged_final_v3_copy.mp3",
		}
		for _, name := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("mp3"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterInspectTools(reg, InspectDeps{
			Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				target := filepath.Base(args[len(args)-1])
				switch target {
				case "01_midnight_final_v2.mp3":
					// <= crossfade (5s)
					return []byte(`{"format":{"duration":"4.5","tags":{}}}`), nil
				case "02 - Meditation.mp3":
					// > crossfade (5s) and <= 2*crossfade (10s), contains "edit" inside "Meditation" (should NOT trigger draft marker)
					return []byte(`{"format":{"duration":"9.0","tags":{}}}`), nil
				case "Clean Song.mp3":
					return []byte(`{"format":{"duration":"60.0","tags":{}}}`), nil
				default:
					// Has ID3 title, so messy filename warnings should not fire.
					return []byte(`{"format":{"duration":"60.0","tags":{"title":"Tagged Clean"}}}`), nil
				}
			},
		})

		res, rpcErr, raw := callToolRPC(t, reg, "inspect_tracks", map[string]any{
			"audioDir":  dir,
			"crossfade": 5,
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
		}

		var got InspectTracksResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
			t.Fatalf("unmarshal text JSON: %v", err)
		}
		if got.TrackCount != 4 {
			t.Fatalf("expected 4 tracks, got %d", got.TrackCount)
		}

		// Track 0: 01_midnight_final_v2.mp3 -> crossfade limit + numeric prefix + multiple underscores + draft/version marker
		tr0 := got.Tracks[0]
		if len(tr0.Warnings) != 2 {
			t.Fatalf("expected 2 track warnings on %q, got %v", tr0.FileName, tr0.Warnings)
		}
		if !strings.Contains(tr0.Warnings[0], "does not exceed crossfade") {
			t.Fatalf("expected acrossfade limit warning, got %q", tr0.Warnings[0])
		}
		for _, want := range []string{"numeric prefix", "multiple underscores", "draft/version marker"} {
			if !strings.Contains(tr0.Warnings[1], want) {
				t.Fatalf("expected %q in filename warning %q", want, tr0.Warnings[1])
			}
		}

		// Track 1: 02 - Meditation.mp3 -> <= 2x crossfade + numeric prefix ONLY (not draft/version marker from "Meditation")
		tr1 := got.Tracks[1]
		if len(tr1.Warnings) != 2 {
			t.Fatalf("expected 2 track warnings on %q, got %v", tr1.FileName, tr1.Warnings)
		}
		if !strings.Contains(tr1.Warnings[0], "<= 2x crossfade") {
			t.Fatalf("expected <= 2x crossfade warning, got %q", tr1.Warnings[0])
		}
		if strings.Contains(tr1.Warnings[1], "draft/version marker") {
			t.Fatalf("unexpected false-positive draft/version marker in %q", tr1.Warnings[1])
		}

		// Track 2: Clean Song.mp3 -> 0 warnings, rawId3Title and warnings omitted in JSON
		if len(got.Tracks[2].Warnings) != 0 {
			t.Fatalf("expected 0 warnings on Clean Song.mp3, got %v", got.Tracks[2].Warnings)
		}
		// Track 3: Tagged_final_v3_copy.mp3 -> ID3 title present, 0 warnings
		if len(got.Tracks[3].Warnings) != 0 || got.Tracks[3].RawID3Title != "Tagged Clean" {
			t.Fatalf("expected 0 warnings on tagged track, got %+v", got.Tracks[3])
		}
	})

	t.Run("notes single-track acrossfade skip when crossfade > 0", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "Solo.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterInspectTools(reg, InspectDeps{
			Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return []byte(`{"format":{"duration":"95.0","tags":{"title":"Solo"}}}`), nil
			},
		})

		res, rpcErr, _ := callToolRPC(t, reg, "inspect_tracks", map[string]any{
			"audioDir":  dir,
			"crossfade": 4,
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v", rpcErr, res)
		}
		var got InspectTracksResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.EstimatedMixDurationSec != 95.0 {
			t.Fatalf("single-track estimatedMixDurationSec = %v, want 95.0", got.EstimatedMixDurationSec)
		}
		if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "skips acrossfade") {
			t.Fatalf("expected single-track acrossfade skip warning, got %v", got.Warnings)
		}
	})

	t.Run("rejects invalid params via JSON-RPC error and returns ErrorToolResult for filesystem or ffprobe errors", func(t *testing.T) {
		dir := t.TempDir()
		emptyDir := t.TempDir()
		notDirFile := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(notDirFile, []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}
		mp3Dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(mp3Dir, "01.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterInspectTools(reg, InspectDeps{
			Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return nil, errors.New("ffprobe failed")
			},
		})

		for _, badArgs := range []any{
			map[string]any{},
			map[string]any{"audioDir": "   "},
			map[string]any{"audioDir": dir, "crossfade": -1},
			map[string]any{"audioDir": dir, "extra": true},
		} {
			_, rpcErr, raw := callToolRPC(t, reg, "inspect_tracks", badArgs)
			if rpcErr == nil || rpcErr.Code != ErrCodeInvalidParams {
				t.Fatalf("expected -32602 for %v, got %s", badArgs, raw)
			}
		}

		for _, errArgs := range []map[string]any{
			{"audioDir": filepath.Join(dir, "does-not-exist")},
			{"audioDir": notDirFile},
			{"audioDir": emptyDir},
			{"audioDir": mp3Dir},
		} {
			res, rpcErr, raw := callToolRPC(t, reg, "inspect_tracks", errArgs)
			if rpcErr != nil {
				t.Fatalf("expected clean ErrorToolResult (not RPC error) for %v, got %s", errArgs, raw)
			}
			if !res.IsError || len(res.Content) == 0 || strings.TrimSpace(res.Content[0].Text) == "" {
				t.Fatalf("expected IsError=true with message for %v, got %+v", errArgs, res)
			}
		}
	})
}

func TestInspectBackground(t *testing.T) {
	t.Run("audits static image, 1080p video, GIF, sub-1080p non-16:9, stray audio, and zero dimensions", func(t *testing.T) {
		dir := t.TempDir()

		cases := []struct {
			name            string
			fileName        string
			ffprobeJSON     string
			wantKind        string
			wantExt         string
			wantAspect      string
			wantIs16x9      bool
			wantHasAudio    bool
			wantWarnSubstrs []string
			wantZeroWarns   bool
		}{
			{
				name:          "1080p png image has no warnings",
				fileName:      "Cover.PNG",
				ffprobeJSON:   `{"format":{"duration":"N/A"},"streams":[{"codec_type":"video","codec_name":"png","width":1920,"height":1080,"r_frame_rate":"25/1","avg_frame_rate":"0/0","pix_fmt":"rgb24"}]}`,
				wantKind:      "image",
				wantExt:       ".png",
				wantAspect:    "16:9",
				wantIs16x9:    true,
				wantZeroWarns: true,
			},
			{
				name:            "4:3 sub-1080p video with stray audio stream",
				fileName:        "loop.mp4",
				ffprobeJSON:     `{"format":{"duration":"12.5"},"streams":[{"codec_type":"audio","codec_name":"aac"},{"codec_type":"video","codec_name":"h264","width":1280,"height":960,"avg_frame_rate":"30/1","nb_frames":"375","pix_fmt":"yuv420p"}]}`,
				wantKind:        "video",
				wantExt:         ".mp4",
				wantAspect:      "4:3",
				wantIs16x9:      false,
				wantHasAudio:    true,
				wantWarnSubstrs: []string{"not 16:9", "below 1080p", "audio stream"},
			},
			{
				name:            "vertical 1080x1920 video warns non-16:9 and sub-1080p width",
				fileName:        "portrait.mov",
				ffprobeJSON:     `{"format":{"duration":"8.0"},"streams":[{"codec_type":"video","codec_name":"h264","width":1080,"height":1920,"avg_frame_rate":"30/1"}]}`,
				wantKind:        "video",
				wantExt:         ".mov",
				wantAspect:      "9:16",
				wantIs16x9:      false,
				wantWarnSubstrs: []string{"not 16:9", "below 1080p"},
			},
			{
				name:            "high-res 2560x1920 4:3 image warns non-16:9 only",
				fileName:        "highres.jpg",
				ffprobeJSON:     `{"format":{"duration":"N/A"},"streams":[{"codec_type":"video","codec_name":"mjpeg","width":2560,"height":1920}]}`,
				wantKind:        "image",
				wantExt:         ".jpg",
				wantAspect:      "4:3",
				wantIs16x9:      false,
				wantWarnSubstrs: []string{"not 16:9"},
			},
			{
				name:            "720p gif infers duration and warns sub-1080p only",
				fileName:        "anim.gif",
				ffprobeJSON:     `{"format":{"duration":"N/A"},"streams":[{"codec_type":"video","codec_name":"gif","width":1280,"height":720,"avg_frame_rate":"15/1","nb_frames":"75","pix_fmt":"bgra"}]}`,
				wantKind:        "video",
				wantExt:         ".gif",
				wantAspect:      "16:9",
				wantIs16x9:      true,
				wantWarnSubstrs: []string{"below 1080p"},
			},
			{
				name:          "4k 16:9 image has zero warnings",
				fileName:      "uhd.webp",
				ffprobeJSON:   `{"format":{"duration":"N/A"},"streams":[{"codec_type":"video","codec_name":"webp","width":3840,"height":2160,"pix_fmt":"yuva420p"}]}`,
				wantKind:      "image",
				wantExt:       ".webp",
				wantAspect:    "16:9",
				wantIs16x9:    true,
				wantZeroWarns: true,
			},
			{
				name:            "missing dimensions emits single missing dimensions warning",
				fileName:        "broken.jpg",
				ffprobeJSON:     `{"format":{"duration":"N/A"},"streams":[{"codec_type":"video","codec_name":"mjpeg"}]}`,
				wantKind:        "image",
				wantExt:         ".jpg",
				wantAspect:      "",
				wantIs16x9:      false,
				wantWarnSubstrs: []string{"missing or zero video dimensions"},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				bgPath := filepath.Join(dir, tc.fileName)
				if err := os.WriteFile(bgPath, []byte("bg"), 0644); err != nil {
					t.Fatal(err)
				}

				reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
				RegisterInspectTools(reg, InspectDeps{
					Runner: func(_ context.Context, name string, _ ...string) ([]byte, error) {
						if name != "ffprobe" {
							return nil, errors.New("unexpected binary")
						}
						return []byte(tc.ffprobeJSON), nil
					},
				})

				res, rpcErr, raw := callToolRPC(t, reg, "inspect_background", map[string]any{
					"bgPath": bgPath,
				})
				if rpcErr != nil || res.IsError {
					t.Fatalf("unexpected error: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
				}

				var got InspectBackgroundResult
				if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
					t.Fatalf("unmarshal text JSON: %v", err)
				}
				if got.BGPath != bgPath || got.FileName != tc.fileName || got.Kind != tc.wantKind || got.Extension != tc.wantExt {
					t.Fatalf("unexpected identity/kind: %+v", got)
				}
				if got.AspectRatio != tc.wantAspect || got.Is16x9 != tc.wantIs16x9 || got.HasAudio != tc.wantHasAudio {
					t.Fatalf("unexpected aspect/audio: %+v", got)
				}
				if tc.wantZeroWarns {
					if len(got.Warnings) != 0 {
						t.Fatalf("expected 0 warnings, got %v", got.Warnings)
					}
					if !strings.Contains(res.Content[0].Text, `"warnings": []`) {
						t.Fatalf("expected non-null empty warnings array in JSON, got:\n%s", res.Content[0].Text)
					}
				} else {
					if len(got.Warnings) != len(tc.wantWarnSubstrs) {
						t.Fatalf("warnings count = %d (%v), want %d (%v)", len(got.Warnings), got.Warnings, len(tc.wantWarnSubstrs), tc.wantWarnSubstrs)
					}
					joined := strings.Join(got.Warnings, "\n")
					for _, sub := range tc.wantWarnSubstrs {
						if !strings.Contains(joined, sub) {
							t.Fatalf("expected warning containing %q, got %v", sub, got.Warnings)
						}
					}
				}
			})
		}
	})

	t.Run("rejects invalid params and returns clean ErrorToolResult on bad files or ffprobe failures", func(t *testing.T) {
		dir := t.TempDir()
		unsupported := filepath.Join(dir, "clip.avi")
		validBG := filepath.Join(dir, "cover.png")
		for _, p := range []string{unsupported, validBG} {
			if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterInspectTools(reg, InspectDeps{
			Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				return nil, errors.New("ffprobe crashed")
			},
		})

		for _, badArgs := range []any{
			map[string]any{},
			map[string]any{"bgPath": "   "},
			map[string]any{"bgPath": validBG, "unexpected": 1},
		} {
			_, rpcErr, raw := callToolRPC(t, reg, "inspect_background", badArgs)
			if rpcErr == nil || rpcErr.Code != ErrCodeInvalidParams {
				t.Fatalf("expected -32602 for %v, got %s", badArgs, raw)
			}
		}

		for _, errArgs := range []map[string]any{
			{"bgPath": filepath.Join(dir, "missing.png")},
			{"bgPath": dir},
			{"bgPath": unsupported},
			{"bgPath": validBG},
		} {
			res, rpcErr, raw := callToolRPC(t, reg, "inspect_background", errArgs)
			if rpcErr != nil {
				t.Fatalf("expected clean ErrorToolResult for %v, got %s", errArgs, raw)
			}
			if !res.IsError || len(res.Content) == 0 || strings.TrimSpace(res.Content[0].Text) == "" {
				t.Fatalf("expected IsError=true with message for %v, got %+v", errArgs, res)
			}
		}
	})
}
