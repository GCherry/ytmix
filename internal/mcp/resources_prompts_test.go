package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/GCherry/ytmix/internal/render"
)

func TestResourcesAndPrompts(t *testing.T) {
	allFiltersExcept3DScope := []byte(`Filters:
 .. color |->V Color
 .. showwaves A->V Wave
 TS gblur V->V Blur
 .. scale V->V Scale
 .. pad V->V Pad
 .. showfreqs A->V Freqs
 .S avectorscope A->V Scope
 .. showcqt A->V CQT
 .S showspectrum A->V Spectrum
 .. ahistogram A->V Histogram
 .. aphasemeter A->N Phase
 .. anullsink A->| Sink
 .. showvolume A->V Volume
`)

	t.Run("resources/list exposes all 3 discovery resources with application/json", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{})

		resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
		var env struct {
			Result struct {
				Resources []Resource `json:"resources"`
			} `json:"result"`
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(resp, &env); err != nil {
			t.Fatalf("unmarshal resources/list: %v (%s)", err, resp)
		}
		if env.Error != nil {
			t.Fatalf("unexpected RPC error: %+v", env.Error)
		}
		if len(env.Result.Resources) != 3 {
			t.Fatalf("expected 3 resources, got %d: %+v", len(env.Result.Resources), env.Result.Resources)
		}

		byURI := make(map[string]Resource, len(env.Result.Resources))
		for _, res := range env.Result.Resources {
			byURI[res.URI] = res
			if res.MimeType != "application/json" {
				t.Fatalf("resource %s mimeType = %q, want application/json", res.URI, res.MimeType)
			}
			if strings.TrimSpace(res.Name) == "" || strings.TrimSpace(res.Description) == "" {
				t.Fatalf("resource %s missing name or description: %+v", res.URI, res)
			}
		}
		for _, uri := range []string{resourceURIPresets, resourceURIThemes, resourceURIResolutions} {
			if _, ok := byURI[uri]; !ok {
				t.Fatalf("resources/list missing %s", uri)
			}
		}
	})

	t.Run("resources/read ytmix://presets handles live ffmpeg, missing filter, degraded no-ffmpeg, and caching", func(t *testing.T) {
		tests := []struct {
			name            string
			lookPath        func(string) (string, error)
			runner          CommandRunner
			wantFFmpeg      bool
			wantBarsAvail   bool
			want3DAvail     bool
			want3DMissing   string
			wantRunnerCalls int
		}{
			{
				name:     "ffmpeg present with missing a3dscope filter",
				lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
				runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
					if name != "ffmpeg" || strings.Join(args, " ") != "-hide_banner -filters" {
						return nil, errors.New("unexpected command")
					}
					return allFiltersExcept3DScope, nil
				},
				wantFFmpeg:      true,
				wantBarsAvail:   true,
				want3DAvail:     false,
				want3DMissing:   "a3dscope",
				wantRunnerCalls: 1,
			},
			{
				name:     "ffmpeg present with all 14 preset filters available",
				lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
				runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
					return append(append([]byte(nil), allFiltersExcept3DScope...), []byte(" .. a3dscope A->V 3D\n")...), nil
				},
				wantFFmpeg:      true,
				wantBarsAvail:   true,
				want3DAvail:     true,
				want3DMissing:   "",
				wantRunnerCalls: 1,
			},
			{
				name: "degraded when ffmpeg is missing from PATH",
				lookPath: func(string) (string, error) {
					return "", errors.New("not found")
				},
				runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
					t.Fatal("runner must not be called when ffmpeg is missing")
					return nil, nil
				},
				wantFFmpeg:      false,
				wantBarsAvail:   false,
				want3DAvail:     false,
				want3DMissing:   "",
				wantRunnerCalls: 0,
			},
			{
				name:     "degraded filter probe when ffmpeg -filters fails",
				lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
				runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
					return nil, errors.New("ffmpeg error")
				},
				wantFFmpeg:      true,
				wantBarsAvail:   false,
				want3DAvail:     false,
				want3DMissing:   "",
				wantRunnerCalls: 1,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				var runnerCalls atomic.Int32
				wrappedRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
					runnerCalls.Add(1)
					return tc.runner(ctx, name, args...)
				}

				reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
				RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{
					LookPath: tc.lookPath,
					Runner:   wrappedRunner,
				})

				for readIdx := range 3 {
					resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ytmix://presets"}}`))
					var env struct {
						Result struct {
							Contents []ResourceContent `json:"contents"`
						} `json:"result"`
						Error *RPCError `json:"error"`
					}
					if err := json.Unmarshal(resp, &env); err != nil {
						t.Fatalf("read %d unmarshal: %v (%s)", readIdx, err, resp)
					}
					if env.Error != nil {
						t.Fatalf("read %d unexpected error: %+v", readIdx, env.Error)
					}
					if len(env.Result.Contents) != 1 {
						t.Fatalf("read %d expected 1 content item, got %d", readIdx, len(env.Result.Contents))
					}
					item := env.Result.Contents[0]
					if item.URI != resourceURIPresets || item.MimeType != "application/json" {
						t.Fatalf("read %d unexpected metadata: %+v", readIdx, item)
					}

					var payload PresetsResource
					if err := json.Unmarshal([]byte(item.Text), &payload); err != nil {
						t.Fatalf("read %d unmarshal presets JSON: %v", readIdx, err)
					}
					if payload.FFmpeg != tc.wantFFmpeg {
						t.Fatalf("read %d ffmpeg = %v, want %v", readIdx, payload.FFmpeg, tc.wantFFmpeg)
					}

					wantSpecs := render.ListVisualizers()
					if len(payload.Presets) != len(wantSpecs) || len(payload.Presets) != 14 {
						t.Fatalf("read %d presets len = %d, want 14", readIdx, len(payload.Presets))
					}
					byID := make(map[string]VisualizerCapability, len(payload.Presets))
					for i, got := range payload.Presets {
						want := wantSpecs[i]
						if got.ID != want.ID || got.Name != want.Name || got.Filter != want.Filter ||
							got.Category != want.Category || got.IsDark != want.IsDark || got.Description != want.Description {
							t.Fatalf("read %d preset[%d] metadata mismatch: got=%+v want=%+v", readIdx, i, got, want)
						}
						byID[got.ID] = got
					}
					if byID["bars"].Available != tc.wantBarsAvail {
						t.Fatalf("read %d bars = %+v, want available=%v", readIdx, byID["bars"], tc.wantBarsAvail)
					}
					if byID["3dscope"].Available != tc.want3DAvail || byID["3dscope"].MissingFilter != tc.want3DMissing {
						t.Fatalf("read %d 3dscope = %+v, want avail=%v missing=%q", readIdx, byID["3dscope"], tc.want3DAvail, tc.want3DMissing)
					}
					if byID["3dscope"].Available && strings.Contains(item.Text, `"missingFilter"`) {
						t.Fatalf("expected missingFilter to be omitted when all presets are available")
					}
				}

				if int(runnerCalls.Load()) != tc.wantRunnerCalls {
					t.Fatalf("expected %d runner call(s) due to caching, got %d", tc.wantRunnerCalls, runnerCalls.Load())
				}
			})
		}
	})

	t.Run("concurrent ytmix://presets reads are race-free and share cached filter probe", func(t *testing.T) {
		var runnerCalls atomic.Int32
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{
			LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				runnerCalls.Add(1)
				return allFiltersExcept3DScope, nil
			},
		})

		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":10,"method":"resources/read","params":{"uri":"ytmix://presets"}}`))
				var env struct {
					Result struct {
						Contents []ResourceContent `json:"contents"`
					} `json:"result"`
					Error *RPCError `json:"error"`
				}
				if err := json.Unmarshal(resp, &env); err != nil || env.Error != nil || len(env.Result.Contents) != 1 {
					t.Errorf("unexpected presets response: %s (err=%v)", resp, err)
					return
				}
				var payload PresetsResource
				if err := json.Unmarshal([]byte(env.Result.Contents[0].Text), &payload); err != nil || len(payload.Presets) != 14 {
					t.Errorf("unexpected presets payload: %s (err=%v)", env.Result.Contents[0].Text, err)
				}
			})
		}
		wg.Wait()

		if runnerCalls.Load() != 1 {
			t.Fatalf("expected 1 cached runner call across concurrent reads, got %d", runnerCalls.Load())
		}
	})

	t.Run("resources/read ytmix://themes and ytmix://resolutions match internal/render catalogs", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{})

		// Themes
		themesResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"ytmix://themes"}}`))
		var themesEnv struct {
			Result struct {
				Contents []ResourceContent `json:"contents"`
			} `json:"result"`
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(themesResp, &themesEnv); err != nil || themesEnv.Error != nil || len(themesEnv.Result.Contents) != 1 {
			t.Fatalf("unexpected ytmix://themes response: %s (err=%v)", themesResp, err)
		}
		var gotThemes ThemesResource
		if err := json.Unmarshal([]byte(themesEnv.Result.Contents[0].Text), &gotThemes); err != nil {
			t.Fatalf("unmarshal themes JSON: %v", err)
		}
		wantThemes := render.ListThemes()
		if len(gotThemes.Themes) != 7 || len(gotThemes.Themes) != len(wantThemes) {
			t.Fatalf("themes len = %d, want 7", len(gotThemes.Themes))
		}
		for i, got := range gotThemes.Themes {
			want := wantThemes[i]
			if got.ID != want.ID || got.Name != want.Name || got.BG != want.BG ||
				got.Primary != want.Primary || got.Secondary != want.Secondary || got.Glow != want.Glow {
				t.Fatalf("theme[%d] mismatch: got=%+v want=%+v", i, got, want)
			}
		}
		// Verify exact JSON keys on raw objects.
		var rawThemes map[string][]map[string]any
		if err := json.Unmarshal([]byte(themesEnv.Result.Contents[0].Text), &rawThemes); err != nil {
			t.Fatal(err)
		}
		for _, item := range rawThemes["themes"] {
			for _, key := range []string{"id", "name", "bg", "primary", "secondary", "glow"} {
				if _, ok := item[key]; !ok {
					t.Fatalf("theme entry missing key %q: %+v", key, item)
				}
			}
		}

		// Resolutions
		resResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"ytmix://resolutions"}}`))
		var resEnv struct {
			Result struct {
				Contents []ResourceContent `json:"contents"`
			} `json:"result"`
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(resResp, &resEnv); err != nil || resEnv.Error != nil || len(resEnv.Result.Contents) != 1 {
			t.Fatalf("unexpected ytmix://resolutions response: %s (err=%v)", resResp, err)
		}
		var gotRes ResolutionsResource
		if err := json.Unmarshal([]byte(resEnv.Result.Contents[0].Text), &gotRes); err != nil {
			t.Fatalf("unmarshal resolutions JSON: %v", err)
		}
		wantRes := render.ListResolutions()
		if len(gotRes.Resolutions) != 3 || !reflect.DeepEqual(gotRes.Resolutions, wantRes) {
			t.Fatalf("resolutions mismatch: got=%+v want=%+v", gotRes.Resolutions, wantRes)
		}
		if gotRes.Resolutions[0].ID != "1080p" || gotRes.Resolutions[1].ID != "1440p" || gotRes.Resolutions[2].ID != "4k" {
			t.Fatalf("unexpected resolution IDs: %+v", gotRes.Resolutions)
		}
	})

	t.Run("prompts/list exposes create-sleep-mix and clean-youtube-release", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{})

		resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":5,"method":"prompts/list"}`))
		var env struct {
			Result struct {
				Prompts []Prompt `json:"prompts"`
			} `json:"result"`
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(resp, &env); err != nil || env.Error != nil {
			t.Fatalf("prompts/list error: %s (err=%v)", resp, env.Error)
		}
		if len(env.Result.Prompts) != 2 {
			t.Fatalf("expected 2 prompts, got %d: %+v", len(env.Result.Prompts), env.Result.Prompts)
		}

		byName := make(map[string]Prompt, len(env.Result.Prompts))
		for _, p := range env.Result.Prompts {
			byName[p.Name] = p
		}

		sleepPrompt, ok := byName[promptCreateSleepMix]
		if !ok {
			t.Fatalf("missing %s in prompts/list", promptCreateSleepMix)
		}
		if len(sleepPrompt.Arguments) != 2 ||
			sleepPrompt.Arguments[0].Name != "audioDir" || sleepPrompt.Arguments[0].Required ||
			sleepPrompt.Arguments[1].Name != "targetDuration" || sleepPrompt.Arguments[1].Required {
			t.Fatalf("unexpected create-sleep-mix arguments: %+v", sleepPrompt.Arguments)
		}

		releasePrompt, ok := byName[promptCleanYouTubeRelease]
		if !ok {
			t.Fatalf("missing %s in prompts/list", promptCleanYouTubeRelease)
		}
		if len(releasePrompt.Arguments) != 2 ||
			releasePrompt.Arguments[0].Name != "audioDir" || releasePrompt.Arguments[0].Required ||
			releasePrompt.Arguments[1].Name != "bgPath" || releasePrompt.Arguments[1].Required {
			t.Fatalf("unexpected clean-youtube-release arguments: %+v", releasePrompt.Arguments)
		}
	})

	t.Run("prompts/get substitution and content checks", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{})

		tests := []struct {
			name         string
			req          string
			wantContains []string
			wantAbsent   []string
		}{
			{
				name: "create-sleep-mix with omitted arguments",
				req:  `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"create-sleep-mix"}}`,
				wantContains: []string{
					"get_capabilities",
					"inspect_tracks",
					"plan_mix",
					"preview_visualizer_frame",
					"start_render",
					"get_render_status",
					"verify_mix",
					"Prompt the user for `audioDir`",
					"black", "ember", "night-line", "night-fog", "lunar-halo",
					"oled-ember", "night-indigo", "monochrome",
					"--fade-to-black", "--dim-titles",
					"Chapter Timestamps",
				},
			},
			{
				name: "create-sleep-mix with audioDir and targetDuration substituted",
				req:  `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"create-sleep-mix","arguments":{"audioDir":"/music/sleep_stems","targetDuration":"90m"}}}`,
				wantContains: []string{
					"/music/sleep_stems",
					"90m",
					"preview_visualizer_frame",
					"Chapter Timestamps",
				},
				wantAbsent: []string{
					"Prompt the user for `audioDir`",
				},
			},
			{
				name: "clean-youtube-release with omitted arguments",
				req:  `{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"clean-youtube-release","arguments":{}}}`,
				wantContains: []string{
					"get_capabilities",
					"inspect_tracks",
					"inspect_background",
					"plan_mix",
					"start_render",
					"get_render_status",
					"verify_mix",
					"Prompt the user for `audioDir`",
					"Path D",
					"ytmix://presets",
					"does NOT modify the `.mp3` files on disk",
					"Path A", "Path B",
					"Display Title",
					"Chapter Timestamps",
				},
			},
			{
				name: "clean-youtube-release with audioDir and bgPath substituted",
				req:  `{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"clean-youtube-release","arguments":{"audioDir":"/music/lofi","bgPath":"/art/loop.mp4"}}}`,
				wantContains: []string{
					"/music/lofi",
					"/art/loop.mp4",
					"inspect_background",
					"Path A", "Path B",
					"does NOT modify the `.mp3` files on disk",
				},
				wantAbsent: []string{
					"Prompt the user for `audioDir`",
					"No `bgPath` Background Visual was supplied",
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				resp := reg.HandleMessage(context.Background(), []byte(tc.req))
				var env struct {
					Result PromptResult `json:"result"`
					Error  *RPCError    `json:"error"`
				}
				if err := json.Unmarshal(resp, &env); err != nil {
					t.Fatalf("unmarshal prompts/get: %v (%s)", err, resp)
				}
				if env.Error != nil {
					t.Fatalf("unexpected RPC error: %+v", env.Error)
				}
				if len(env.Result.Messages) != 1 || env.Result.Messages[0].Role != "user" || env.Result.Messages[0].Content.Type != "text" {
					t.Fatalf("unexpected prompt messages: %+v", env.Result.Messages)
				}
				text := env.Result.Messages[0].Content.Text
				for _, want := range tc.wantContains {
					if !strings.Contains(text, want) {
						t.Fatalf("prompt output missing %q:\n%s", want, text)
					}
				}
				for _, absent := range tc.wantAbsent {
					if strings.Contains(text, absent) {
						t.Fatalf("prompt output unexpectedly contained %q:\n%s", absent, text)
					}
				}
			})
		}
	})

	t.Run("error handling for unknown URI, unknown prompt, and unknown prompt argument key", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{})

		tests := []struct {
			name     string
			req      string
			wantCode int
		}{
			{
				name:     "unknown resource URI returns -32002",
				req:      `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ytmix://unknown"}}`,
				wantCode: ErrCodeResourceNotFound,
			},
			{
				name:     "unknown prompt returns -32602",
				req:      `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"unknown-prompt"}}`,
				wantCode: ErrCodeInvalidParams,
			},
			{
				name:     "unknown argument key on create-sleep-mix returns -32602",
				req:      `{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"create-sleep-mix","arguments":{"bgPath":"/tmp/a.png"}}}`,
				wantCode: ErrCodeInvalidParams,
			},
			{
				name:     "unknown argument key on clean-youtube-release returns -32602",
				req:      `{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"clean-youtube-release","arguments":{"targetDuration":"1h"}}}`,
				wantCode: ErrCodeInvalidParams,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				resp := reg.HandleMessage(context.Background(), []byte(tc.req))
				var env struct {
					Error *RPCError `json:"error"`
				}
				if err := json.Unmarshal(resp, &env); err != nil {
					t.Fatalf("unmarshal error response: %v (%s)", err, resp)
				}
				if env.Error == nil || env.Error.Code != tc.wantCode {
					t.Fatalf("got error %+v, want code %d (%s)", env.Error, tc.wantCode, resp)
				}
			})
		}
	})

	t.Run("strict GLOSSARY.md vocabulary check on all resource metadata and prompt templates", func(t *testing.T) {
		forbiddenTerms := []string{
			"song",
			"clip",
			"audio segment",
			"track name",
			"filename",
			"label",
			"cover file",
			"backdrop",
			"wallpaper",
			"screensaver",
			"audio animation",
			"equalizer widget",
			"project",
			"playlist",
			"export",
			"transition",
			"blend",
			"fade-out",
			"volume leveling",
			"gain boost",
			"compression",
			"tracklist",
			"cue sheet",
			"description file",
			"watermark",
			"lower-third",
			"drawtext",
			"render mode",
			"profile",
		}

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{})

		var texts []struct {
			source string
			body   string
		}

		// Collect resource metadata.
		resListResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
		var resList struct {
			Result struct {
				Resources []Resource `json:"resources"`
			} `json:"result"`
		}
		if err := json.Unmarshal(resListResp, &resList); err != nil {
			t.Fatal(err)
		}
		for _, res := range resList.Result.Resources {
			texts = append(texts,
				struct{ source, body string }{"resource.name:" + res.URI, res.Name},
				struct{ source, body string }{"resource.description:" + res.URI, res.Description},
			)
		}

		// Collect prompt metadata.
		promptListResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`))
		var promptList struct {
			Result struct {
				Prompts []Prompt `json:"prompts"`
			} `json:"result"`
		}
		if err := json.Unmarshal(promptListResp, &promptList); err != nil {
			t.Fatal(err)
		}
		for _, p := range promptList.Result.Prompts {
			texts = append(texts,
				struct{ source, body string }{"prompt.name:" + p.Name, p.Name},
				struct{ source, body string }{"prompt.description:" + p.Name, p.Description},
			)
			for _, arg := range p.Arguments {
				texts = append(texts, struct{ source, body string }{"prompt.arg:" + p.Name + "." + arg.Name, arg.Description})
			}
		}

		// Collect rendered prompts across omitted and populated variants.
		for _, tc := range []struct {
			label string
			req   string
		}{
			{"sleep-omitted", `{"jsonrpc":"2.0","id":10,"method":"prompts/get","params":{"name":"create-sleep-mix"}}`},
			{"sleep-populated", `{"jsonrpc":"2.0","id":11,"method":"prompts/get","params":{"name":"create-sleep-mix","arguments":{"audioDir":"/tracks","targetDuration":"1h"}}}`},
			{"release-omitted", `{"jsonrpc":"2.0","id":12,"method":"prompts/get","params":{"name":"clean-youtube-release"}}`},
			{"release-populated", `{"jsonrpc":"2.0","id":13,"method":"prompts/get","params":{"name":"clean-youtube-release","arguments":{"audioDir":"/tracks","bgPath":"/art/loop.mp4"}}}`},
		} {
			resp := reg.HandleMessage(context.Background(), []byte(tc.req))
			var env struct {
				Result PromptResult `json:"result"`
			}
			if err := json.Unmarshal(resp, &env); err != nil {
				t.Fatal(err)
			}
			texts = append(texts, struct{ source, body string }{tc.label + ".description", env.Result.Description})
			for i, msg := range env.Result.Messages {
				texts = append(texts, struct{ source, body string }{tc.label + ".msg[" + string(rune('0'+i)) + "]", msg.Content.Text})
			}
		}

		for _, item := range texts {
			lower := strings.ToLower(item.body)
			for _, bad := range forbiddenTerms {
				if strings.Contains(lower, bad) {
					t.Fatalf("forbidden GLOSSARY _Avoid_ term %q found in %s:\n%s", bad, item.source, item.body)
				}
			}
		}
	})

	t.Run("nil registry and canceled context are handled cleanly", func(t *testing.T) {
		RegisterResourcesAndPrompts(nil, ResourcesPromptsDeps{})

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterResourcesAndPrompts(reg, ResourcesPromptsDeps{
			LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			Runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				t.Fatal("runner must not be called with canceled context")
				return nil, nil
			},
		})

		canceled, cancel := context.WithCancel(context.Background())
		cancel()

		for _, req := range []string{
			`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ytmix://presets"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ytmix://themes"}}`,
			`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"ytmix://resolutions"}}`,
			`{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"create-sleep-mix"}}`,
			`{"jsonrpc":"2.0","id":5,"method":"prompts/get","params":{"name":"clean-youtube-release"}}`,
		} {
			resp := reg.HandleMessage(canceled, []byte(req))
			var env struct {
				Error *RPCError `json:"error"`
			}
			if err := json.Unmarshal(resp, &env); err != nil || env.Error == nil {
				t.Fatalf("expected RPC error on canceled context for %s, got: %s", req, resp)
			}
		}
	})
}
