package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/GCherry/ytmix/internal/render"
)

const (
	resourceURIPresets     = "ytmix://presets"
	resourceURIThemes      = "ytmix://themes"
	resourceURIResolutions = "ytmix://resolutions"

	promptCreateSleepMix      = "create-sleep-mix"
	promptCleanYouTubeRelease = "clean-youtube-release"
)

// ResourcesPromptsDeps supplies host binary lookup and command execution for static MCP discovery resources and prompts.
type ResourcesPromptsDeps struct {
	LookPath func(string) (string, error)
	Runner   CommandRunner
}

// PresetsResource is the JSON payload returned by the ytmix://presets MCP resource.
type PresetsResource struct {
	FFmpeg  bool                   `json:"ffmpeg"`
	Presets []VisualizerCapability `json:"presets"`
}

// ThemesResource is the JSON payload returned by the ytmix://themes MCP resource.
type ThemesResource struct {
	Themes []render.ThemeSpec `json:"themes"`
}

// ResolutionsResource is the JSON payload returned by the ytmix://resolutions MCP resource.
type ResolutionsResource struct {
	Resolutions []render.ResolutionSpec `json:"resolutions"`
}

type presetsFilterCache struct {
	mu            sync.Mutex
	probed        bool
	availFilters  map[string]bool
	filtersProbed bool
}

func (c *presetsFilterCache) detectFilters(ctx context.Context, runner CommandRunner) (map[string]bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.probed {
		return cloneFilterMap(c.availFilters), c.filtersProbed
	}

	var availFilters map[string]bool
	var filtersProbed bool
	if filters, err := render.DetectFilters(ctx, runner); err == nil {
		availFilters = filters
		filtersProbed = true
	}
	if ctx.Err() == nil {
		c.availFilters = cloneFilterMap(availFilters)
		c.filtersProbed = filtersProbed
		c.probed = true
	}
	return availFilters, filtersProbed
}

// RegisterResourcesAndPrompts registers static discovery MCP resources and guided workflow prompts on r.
func RegisterResourcesAndPrompts(r *Registry, deps ResourcesPromptsDeps) {
	if r == nil {
		return
	}

	var filterCache presetsFilterCache

	r.RegisterResource(Resource{
		URI:         resourceURIPresets,
		Name:        "Path D Auto Visualizer Presets",
		Description: "JSON catalog of all 14 built-in Path D Auto Visualizer presets with live FFmpeg filter availability.",
		MimeType:    "application/json",
	}, func(ctx context.Context, uri string) ([]ResourceContent, error) {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if ctx == nil {
			ctx = context.Background()
		}
		return readPresetsResource(ctx, uri, deps, &filterCache)
	})

	r.RegisterResource(Resource{
		URI:         resourceURIThemes,
		Name:        "Path D Color Themes",
		Description: "JSON catalog of all 7 built-in Path D Auto Visualizer Color Themes.",
		MimeType:    "application/json",
	}, func(ctx context.Context, uri string) ([]ResourceContent, error) {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return jsonResourceContents(uri, ThemesResource{
			Themes: render.ListThemes(),
		})
	})

	r.RegisterResource(Resource{
		URI:         resourceURIResolutions,
		Name:        "Path D Output Resolutions",
		Description: "JSON catalog of supported Path D Auto Visualizer output resolutions (1080p, 1440p, 4k).",
		MimeType:    "application/json",
	}, func(ctx context.Context, uri string) ([]ResourceContent, error) {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return jsonResourceContents(uri, ResolutionsResource{
			Resolutions: render.ListResolutions(),
		})
	})

	r.RegisterPrompt(Prompt{
		Name:        promptCreateSleepMix,
		Description: "Guide the creation of a calm, low-luminance sleep Mix using Path D Auto Visualizer darkness controls, dry-run estimation, rendering, verification, and YouTube Chapter Timestamps.",
		Arguments: []PromptArgument{
			{
				Name:        "audioDir",
				Description: "Optional directory path containing input .mp3 Tracks.",
				Required:    false,
			},
			{
				Name:        "targetDuration",
				Description: "Optional target Mix duration (e.g., 1h, 90m, or 3600s).",
				Required:    false,
			},
		},
	}, func(ctx context.Context, args map[string]string) (PromptResult, error) {
		if ctx != nil && ctx.Err() != nil {
			return PromptResult{}, ctx.Err()
		}
		if err := validatePromptArgs(args, "audioDir", "targetDuration"); err != nil {
			return PromptResult{}, err
		}
		audioDir := strings.TrimSpace(args["audioDir"])
		targetDuration := strings.TrimSpace(args["targetDuration"])
		return PromptResult{
			Description: "Guided workflow for planning, previewing, rendering, and verifying a calm sleep Mix.",
			Messages: []PromptMessage{{
				Role: "user",
				Content: PromptContent{
					Type: "text",
					Text: buildCreateSleepMixPrompt(audioDir, targetDuration),
				},
			}},
		}, nil
	})

	r.RegisterPrompt(Prompt{
		Name:        promptCleanYouTubeRelease,
		Description: "Guide a release-ready YouTube Mix: audit Display Titles and Background Visual, confirm cleaned titles and Track ordering, preview Rendering Path trade-offs, render, verify, and draft YouTube copy.",
		Arguments: []PromptArgument{
			{
				Name:        "audioDir",
				Description: "Optional directory path containing input .mp3 Tracks.",
				Required:    false,
			},
			{
				Name:        "bgPath",
				Description: "Optional Background Visual path (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif).",
				Required:    false,
			},
		},
	}, func(ctx context.Context, args map[string]string) (PromptResult, error) {
		if ctx != nil && ctx.Err() != nil {
			return PromptResult{}, ctx.Err()
		}
		if err := validatePromptArgs(args, "audioDir", "bgPath"); err != nil {
			return PromptResult{}, err
		}
		audioDir := strings.TrimSpace(args["audioDir"])
		bgPath := strings.TrimSpace(args["bgPath"])
		return PromptResult{
			Description: "Guided workflow for auditing Display Titles, planning Rendering Paths, rendering, verifying, and drafting YouTube release copy.",
			Messages: []PromptMessage{{
				Role: "user",
				Content: PromptContent{
					Type: "text",
					Text: buildCleanYouTubeReleasePrompt(audioDir, bgPath),
				},
			}},
		}, nil
	})
}

func readPresetsResource(ctx context.Context, uri string, deps ResourcesPromptsDeps, cache *presetsFilterCache) ([]ResourceContent, error) {
	var hasFFmpeg bool
	if deps.LookPath != nil {
		_, err := deps.LookPath("ffmpeg")
		hasFFmpeg = err == nil
	}

	var availFilters map[string]bool
	var filtersProbed bool
	if hasFFmpeg && deps.Runner != nil {
		if cache != nil {
			availFilters, filtersProbed = cache.detectFilters(ctx, deps.Runner)
		} else if filters, err := render.DetectFilters(ctx, deps.Runner); err == nil {
			availFilters = filters
			filtersProbed = true
		}
	}

	specs := render.ListVisualizers()
	presets := make([]VisualizerCapability, len(specs))
	for i, spec := range specs {
		var ok bool
		var missing string
		if filtersProbed {
			ok, missing = render.PresetAvailable(spec.ID, availFilters)
		}
		presets[i] = VisualizerCapability{
			ID:            spec.ID,
			Name:          spec.Name,
			Filter:        spec.Filter,
			Category:      spec.Category,
			Description:   spec.Description,
			IsDark:        spec.IsDark,
			Available:     ok,
			MissingFilter: missing,
		}
	}

	return jsonResourceContents(uri, PresetsResource{
		FFmpeg:  hasFFmpeg,
		Presets: presets,
	})
}

func jsonResourceContents(uri string, payload any) ([]ResourceContent, error) {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal resource %s: %w", uri, err)
	}
	return []ResourceContent{{
		URI:      uri,
		MimeType: "application/json",
		Text:     string(raw),
	}}, nil
}

func validatePromptArgs(args map[string]string, allowed ...string) error {
	if len(args) == 0 {
		return nil
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	var unknown []string
	for k := range args {
		if !allowedSet[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return InvalidParamsError(fmt.Sprintf("unexpected prompt argument(s): %s", strings.Join(unknown, ", ")))
	}
	return nil
}

func buildCreateSleepMixPrompt(audioDir, targetDuration string) string {
	var b strings.Builder
	b.WriteString("Create a calm, low-luminance YouTube sleep Mix using ytmix MCP tools and resources.\n\n")
	b.WriteString("Step 1 — Inspect Capabilities and Curate Tracks:\n")
	b.WriteString("- Call `get_capabilities` (and optionally read `ytmix://presets`, `ytmix://themes`, and `ytmix://resolutions`) to verify `ffmpeg`/`ffprobe` availability and which dark Path D Auto Visualizer presets have their required FFmpeg filters installed.\n")
	if audioDir != "" {
		fmt.Fprintf(&b, "- Call `inspect_tracks` on `audioDir` = %q to inspect Track durations, `rawId3Title` vs. resolved `displayTitle`, and any Crossfade or messy source file-stem warnings.\n", audioDir)
	} else {
		b.WriteString("- Prompt the user for `audioDir` (the folder containing input `.mp3` Tracks), then call `inspect_tracks` on `audioDir` to inspect Track durations, `rawId3Title` vs. resolved `displayTitle`, and any pre-flight warnings.\n")
	}
	if targetDuration != "" {
		fmt.Fprintf(&b, "- Curate a calm, cohesive Track sequence whose Crossfade-adjusted total Mix duration aligns with the target duration of %q (reordering or trimming the Track list if needed, and recommending gentle `crossfade` smoothing and `normalize` = true for `-14 LUFS` Loudness Normalization).\n\n", targetDuration)
	} else {
		b.WriteString("- Curate a calm, cohesive Track sequence (confirming desired Mix length with the user, recommending gentle `crossfade` smoothing, and enabling `normalize` = true for `-14 LUFS` Loudness Normalization when Track loudness varies).\n\n")
	}

	b.WriteString("Step 2 — Configure Dark/Sleep Path D Auto Visualizer & Preview:\n")
	b.WriteString("- Recommend Dark/Sleep `Path D` Auto Visualizer presets (`black` for a 2 fps pure `#000000` blackout screen, or `ember`, `night-line`, `night-fog`, and `lunar-halo` for ultra-dim ambient motion) paired with dark Color Themes (`oled-ember`, `night-indigo`, or `monochrome`).\n")
	b.WriteString("- Configure Darkness & Sleep controls: `fadeToBlack` (`--fade-to-black`, for example `15m`) to gently fade the visual and Title Overlays to `#000000` black, `dimTitles` = true (`--dim-titles`) for low-luminance warm-gray `.ass` Title Overlays when `overlayTitles` is enabled, and optional reduced `visualizerBrightness` (`10`–`100`).\n")
	b.WriteString("- Optionally run `preview_visualizer_frame` with the chosen `preset`, `theme`, `brightness`, `overlayTitles`, and `dimTitles` so the user can visually confirm the darkness level and Title Overlay legibility before rendering.\n\n")

	b.WriteString("Step 3 — Dry-Run Plan, Render, Verify, and YouTube Copy:\n")
	b.WriteString("- Call `plan_mix` with the curated `tracks` (or `audioDir`) and Path D Auto Visualizer settings to compute the dry-run `estimate` and Crossfade-adjusted `Chapter Timestamps` (`timestampsText`) with zero disk writes.\n")
	b.WriteString("- Present the complete Mix plan (Track order, any Display Title overrides, Path D Auto Visualizer settings, estimated render duration, and Chapter Timestamps) and confirm with the user before starting the render.\n")
	b.WriteString("- Execute `start_render`, poll `get_render_status` (or read `ytmix://jobs/{id}`) until `status` is `complete`, and run `verify_mix` on `outPath` and `timestampsPath` to confirm valid H.264/AAC streams and `00:00` Chapter Timestamps.\n")
	b.WriteString("- Draft a calm sleep-Mix YouTube video title and description, and include the verified `Chapter Timestamps` block.\n")
	return b.String()
}

func buildCleanYouTubeReleasePrompt(audioDir, bgPath string) string {
	var b strings.Builder
	b.WriteString("Prepare a release-ready YouTube Mix using ytmix MCP tools and resources.\n\n")
	b.WriteString("Step 1 — Inspect Media and Audit Display Titles:\n")
	b.WriteString("- Call `get_capabilities` to check `ffmpeg`/`ffprobe` availability, detected H.264 encoder, and Path D Auto Visualizer support.\n")
	if audioDir != "" {
		fmt.Fprintf(&b, "- Call `inspect_tracks` on `audioDir` = %q to inspect every Track's duration, `rawId3Title`, resolved `displayTitle`, and pre-flight warnings.\n", audioDir)
	} else {
		b.WriteString("- Prompt the user for `audioDir` (the directory containing input `.mp3` Tracks), then call `inspect_tracks` on `audioDir` to inspect every Track's duration, `rawId3Title`, resolved `displayTitle`, and pre-flight warnings.\n")
	}
	if bgPath != "" {
		fmt.Fprintf(&b, "- Call `inspect_background` on `bgPath` = %q to verify media kind (`image` or `video`), 16:9 aspect ratio, 1080p+ dimensions, frame rate, and whether a looping video contains stray audio streams.\n", bgPath)
	} else {
		b.WriteString("- No `bgPath` Background Visual was supplied: suggest using `Path D` Auto Visualizer (and inspect `ytmix://presets`, `ytmix://themes`, and `ytmix://resolutions` to pick a preset, Color Theme, and resolution) or ask the user for a `bgPath` and inspect it with `inspect_background`.\n")
	}
	b.WriteString("- Audit every Track's `rawId3Title` and resolved `displayTitle` for messy source file-stem artifacts such as numeric prefixes (`01 - `), underscores, redundant delimiters, or draft markers (`final`, `v2`, `copy`, `draft`, `edit`, `master`).\n\n")

	b.WriteString("Step 2 — Confirm Cleaned Display Titles & Track Order:\n")
	b.WriteString("- Present the proposed cleaned `Display Title` overrides and curated `Track` ordering to the user for confirmation.\n")
	b.WriteString("- Confirm explicitly that passing `displayTitle` overrides inside the `tracks` array to `plan_mix` and `start_render` does NOT modify the `.mp3` files on disk.\n\n")

	b.WriteString("Step 3 — Simulate Rendering Path, Render, and Verify:\n")
	b.WriteString("- Call `plan_mix` with the confirmed `tracks` order, `Display Title` overrides, `bg` (if provided) or Path D Auto Visualizer options, `crossfade`, `normalize` (`-14 LUFS` Loudness Normalization), and `overlayTitles`.\n")
	b.WriteString("- Review `renderingPath` and `tradeOffNotes` with the user—especially when using a looping video/GIF Background Visual (`Path A` stream-copy speed vs. `Path B` full re-encode when `Title Overlay`s are enabled)—along with the dry-run time estimate and Crossfade-adjusted `Chapter Timestamps`.\n")
	b.WriteString("- After user approval, call `start_render` with the confirmed `tracks` and `Display Title` overrides, monitor progress via `get_render_status`, and run `verify_mix` on `outPath` and `timestampsPath`.\n\n")

	b.WriteString("Step 4 — Draft YouTube Release Copy:\n")
	b.WriteString("- Produce compelling YouTube video title ideas, release-ready YouTube description copy, and the verified `Chapter Timestamps` block (starting at `00:00`) ready to paste into YouTube.\n")
	return b.String()
}
