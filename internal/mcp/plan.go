package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/probe"
	"github.com/GCherry/ytmix/internal/render"
	"github.com/GCherry/ytmix/internal/subtitle"
)

const defaultPreviewOffsetSec = 5.0

// PlanDeps supplies host binary lookup and command execution for dry-run planning and preview tools.
type PlanDeps struct {
	LookPath func(string) (string, error)
	Runner   CommandRunner
}

type planTrackInput struct {
	FileName     string  `json:"fileName"`
	DisplayTitle string  `json:"displayTitle"`
	DurationSec  float64 `json:"durationSec"`
	Path         string  `json:"path"`
}

type planMixArgs struct {
	AudioDir             string               `json:"audioDir"`
	Tracks               []planTrackInput     `json:"tracks"`
	BG                   string               `json:"bg"`
	Crossfade            float64              `json:"crossfade"`
	Normalize            bool                 `json:"normalize"`
	OverlayTitles        bool                 `json:"overlayTitles"`
	TitleDuration        int                  `json:"titleDuration"`
	FontName             string               `json:"fontName"`
	Shuffle              bool                 `json:"shuffle"`
	Visualizer           string               `json:"visualizer"`
	VisualizerTheme      string               `json:"visualizerTheme"`
	VisualizerResolution string               `json:"visualizerResolution"`
	VisualizerGlow       *bool                `json:"visualizerGlow"`
	VisualizerBrightness *int                 `json:"visualizerBrightness"`
	FadeToBlack          string               `json:"fadeToBlack"`
	DimTitles            bool                 `json:"dimTitles"`
	TrackMedia           string               `json:"trackMedia"`
	Manifest             string               `json:"manifest"`
	TrackMediaLayout     string               `json:"trackMediaLayout"`
	TrackTransition      string               `json:"trackTransition"`
	TrackOverrides       []trackOverrideInput `json:"trackOverrides"`
}

// PlannedChapter represents one Crossfade-adjusted entry in the predicted Chapter Timestamps schedule.
type PlannedChapter struct {
	Index       int     `json:"index"`
	StartSec    float64 `json:"startSec"`
	Timestamp   string  `json:"timestamp"`
	Title       string  `json:"title"`
	DurationSec float64 `json:"durationSec"`
}

// TitleOverlayPreview summarizes the .ass Title Overlay configuration and cue count for a planned Mix.
type TitleOverlayPreview struct {
	Enabled          bool   `json:"enabled"`
	CueCount         int    `json:"cueCount"`
	DimTitles        bool   `json:"dimTitles"`
	TitleDurationSec int    `json:"titleDurationSec"`
	FontName         string `json:"fontName"`
}

// PlanMixResult is the structured dry-run simulation payload returned by the plan_mix MCP tool.
type PlanMixResult struct {
	RenderingPath       string              `json:"renderingPath"`
	RenderingPathDetail string              `json:"renderingPathDetail"`
	Segmented           bool                `json:"segmented,omitempty"`
	TrackMedia          string              `json:"trackMedia,omitempty"`
	TrackMediaLayout    string              `json:"trackMediaLayout,omitempty"`
	TrackTransition     string              `json:"trackTransition,omitempty"`
	TrackCount          int                 `json:"trackCount"`
	TotalDurationSec    float64             `json:"totalDurationSec"`
	CrossfadeSec        float64             `json:"crossfadeSec"`
	Normalize           bool                `json:"normalize"`
	Estimate            render.MixEstimate  `json:"estimate"`
	EstimateSummary     string              `json:"estimateSummary"`
	Chapters            []PlannedChapter    `json:"chapters"`
	TimestampsText      string              `json:"timestampsText"`
	TitleOverlayPreview TitleOverlayPreview `json:"titleOverlayPreview"`
	TradeOffNotes       []string            `json:"tradeOffNotes"`
	Warnings            []string            `json:"warnings"`
}

type previewVisualizerFrameArgs struct {
	Preset        string   `json:"preset"`
	Theme         string   `json:"theme"`
	Resolution    string   `json:"resolution"`
	Glow          *bool    `json:"glow"`
	Brightness    *int     `json:"brightness"`
	DimTitles     bool     `json:"dimTitles"`
	OverlayTitles bool     `json:"overlayTitles"`
	TitleText     string   `json:"titleText"`
	FadeToBlack   string   `json:"fadeToBlack"`
	AudioPath     string   `json:"audioPath"`
	OffsetSec     *float64 `json:"offsetSec"`
}

// PreviewVisualizerResult is the structured metadata returned alongside the PNG frame by preview_visualizer_frame.
type PreviewVisualizerResult struct {
	Preset        string  `json:"preset"`
	Theme         string  `json:"theme"`
	Resolution    string  `json:"resolution"`
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	Glow          bool    `json:"glow"`
	Brightness    int     `json:"brightness"`
	DimTitles     bool    `json:"dimTitles"`
	OverlayTitles bool    `json:"overlayTitles"`
	AudioSource   string  `json:"audioSource"`
	OffsetSec     float64 `json:"offsetSec"`
	ByteSize      int     `json:"byteSize"`
	MimeType      string  `json:"mimeType"`
}

// RegisterPlanTools registers the read-only plan_mix and preview_visualizer_frame tools on r.
func RegisterPlanTools(r *Registry, deps PlanDeps) {
	if r == nil {
		return
	}

	r.RegisterTool(planMixToolDefinition(), func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return handlePlanMix(ctx, args, deps)
	})

	r.RegisterTool(previewVisualizerToolDefinition(), func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return handlePreviewVisualizerFrame(ctx, args, deps)
	})
}

func readOnlyLocalAnnotations(title string) *ToolAnnotations {
	return &ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    new(true),
		DestructiveHint: new(false),
		IdempotentHint:  new(true),
		OpenWorldHint:   new(false),
	}
}

func planMixToolDefinition() Tool {
	props := map[string]any{
		"audioDir": map[string]any{
			"type":        "string",
			"description": "Directory containing input .mp3 Tracks (mutually exclusive with tracks).",
		},
		"tracks": map[string]any{
			"type":        "array",
			"description": "Explicit ordered list of Tracks to simulate (mutually exclusive with audioDir). Each item requires durationSec > 0 or a local .mp3 path to probe.",
			"minItems":    1,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fileName": map[string]any{
						"type":        "string",
						"description": "Track filename (used to derive Display Title when displayTitle is omitted).",
					},
					"displayTitle": map[string]any{
						"type":        "string",
						"description": "Optional override Display Title for Chapter Timestamps and Title Overlays.",
					},
					"durationSec": map[string]any{
						"type":             "number",
						"description":      "Track duration in seconds (> 0). Optional when path is provided for ffprobe.",
						"exclusiveMinimum": 0,
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Optional local path to the .mp3 Track (probed via ffprobe if durationSec is omitted).",
					},
				},
				"additionalProperties": false,
			},
		},
		"bg": map[string]any{
			"type":        "string",
			"description": "Optional Background Visual path (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif). Omit to use Path D Auto Visualizer.",
		},
		"crossfade": map[string]any{
			"type":        "number",
			"description": "Crossfade duration in seconds between Tracks (default 0).",
			"default":     0,
			"minimum":     0,
		},
		"normalize": map[string]any{
			"type":        "boolean",
			"description": "Simulate -14 LUFS Loudness Normalization per Track.",
			"default":     false,
		},
		"overlayTitles": map[string]any{
			"type":        "boolean",
			"description": "Simulate burned-in .ass Title Overlays (switches video Background Visuals from Path A to Path B).",
			"default":     false,
		},
		"titleDuration": map[string]any{
			"type":        "integer",
			"description": "Seconds to display each Title Overlay (0 = full Track).",
			"default":     0,
			"minimum":     0,
		},
		"fontName": map[string]any{
			"type":        "string",
			"description": "Font family name for .ass Title Overlays (default Arial).",
			"default":     "Arial",
		},
		"shuffle": map[string]any{
			"type":        "boolean",
			"description": "Randomize discovered audioDir Track order.",
			"default":     false,
		},
		"visualizer": map[string]any{
			"type":        "string",
			"description": "Path D Auto Visualizer preset ID when bg is omitted (default bars).",
		},
		"visualizerTheme": map[string]any{
			"type":        "string",
			"description": "Path D Color Theme ID (default studio-mint).",
		},
		"visualizerResolution": map[string]any{
			"type":        "string",
			"description": "Path D output resolution (1080p, 1440p, 4k; default 1080p).",
		},
		"visualizerGlow": map[string]any{
			"type":        "boolean",
			"description": "Apply subtle glow blur on Path D Auto Visualizer presets (default true).",
			"default":     true,
		},
		"visualizerBrightness": map[string]any{
			"type":        "integer",
			"description": "Global Path D visualizer brightness percentage in 10..100 (default 100).",
			"default":     100,
			"minimum":     10,
			"maximum":     100,
		},
		"fadeToBlack": map[string]any{
			"type":        "string",
			"description": "Go duration string (e.g. \"15m\") to fade Path D video and titles to #000000 black (0 disables).",
		},
		"dimTitles": map[string]any{
			"type":        "boolean",
			"description": "Use low-luminance translucent warm-gray ASS styling for Title Overlays.",
			"default":     false,
		},
	}
	addTrackMediaSchemaProperties(props)
	return Tool{
		Name:        "plan_mix",
		Description: "Dry-run simulator for a Mix without writing output files. Selects the exact Rendering Path (Path A/B/C/D) or segmented per-Track Dual-Layer pipeline, computes render.MixEstimate and speed trade-offs, and previews Crossfade-adjusted Chapter Timestamps and Title Overlay cue counts.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           props,
			"additionalProperties": false,
		},
		Annotations: readOnlyLocalAnnotations("Plan Mix (Dry Run)"),
	}
}

func previewVisualizerToolDefinition() Tool {
	return Tool{
		Name:        "preview_visualizer_frame",
		Description: "Render a single PNG preview frame of any available Path D Auto Visualizer preset and Color Theme using either a sample .mp3 Track or synthetic stereo audio, returning the image as a base64 PNG plus metadata.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"preset": map[string]any{
					"type":        "string",
					"description": "Path D Auto Visualizer preset ID (default bars).",
					"default":     render.DefaultVisualizerPreset,
				},
				"theme": map[string]any{
					"type":        "string",
					"description": "Path D Color Theme ID (default studio-mint).",
					"default":     render.DefaultVisualizerTheme,
				},
				"resolution": map[string]any{
					"type":        "string",
					"description": "Path D output resolution (1080p, 1440p, 4k; default 1080p).",
					"default":     render.DefaultVisualizerResolution,
				},
				"glow": map[string]any{
					"type":        "boolean",
					"description": "Apply subtle glow blur on supported presets (default true).",
					"default":     true,
				},
				"brightness": map[string]any{
					"type":        "integer",
					"description": "Global Path D visualizer brightness percentage in 10..100 (default 100).",
					"default":     render.DefaultVisualizerBrightness,
					"minimum":     10,
					"maximum":     100,
				},
				"dimTitles": map[string]any{
					"type":        "boolean",
					"description": "Use low-luminance translucent warm-gray ASS styling when overlayTitles is true.",
					"default":     false,
				},
				"overlayTitles": map[string]any{
					"type":        "boolean",
					"description": "Render a sample bottom-left .ass Title Overlay onto the preview frame.",
					"default":     false,
				},
				"titleText": map[string]any{
					"type":        "string",
					"description": "Sample Display Title text shown when overlayTitles is true.",
				},
				"fadeToBlack": map[string]any{
					"type":        "string",
					"description": "Optional Go duration string (e.g. \"15m\") for sleep fade-to-black.",
				},
				"audioPath": map[string]any{
					"type":        "string",
					"description": "Optional local .mp3 Track path to drive the visualizer instead of a synthetic stereo test tone.",
				},
				"offsetSec": map[string]any{
					"type":        "number",
					"description": "Timestamp offset in seconds for the preview frame (default 5.0).",
					"default":     defaultPreviewOffsetSec,
					"minimum":     0,
				},
			},
			"additionalProperties": false,
		},
		Annotations: readOnlyLocalAnnotations("Preview Path D Visualizer Frame"),
	}
}

func parseOptionalDuration(raw, fieldName string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, InvalidParamsError(fmt.Sprintf("%s must be a valid duration string (e.g. \"15m\"): %v", fieldName, err))
	}
	if d < 0 {
		return 0, InvalidParamsError(fmt.Sprintf("%s must be >= 0 (got %s)", fieldName, d))
	}
	return d, nil
}

func resolveGlowAndBrightness(glowPtr *bool, brightnessPtr *int, brightnessField string) (bool, int, error) {
	glow := true
	if glowPtr != nil {
		glow = *glowPtr
	}
	brightness := render.DefaultVisualizerBrightness
	if brightnessPtr != nil {
		brightness = *brightnessPtr
		if brightness < render.MinVisualizerBrightness || brightness > render.MaxVisualizerBrightness {
			return false, 0, InvalidParamsError(fmt.Sprintf("%s must be between %d and %d (got %d)", brightnessField, render.MinVisualizerBrightness, render.MaxVisualizerBrightness, brightness))
		}
	}
	return glow, brightness, nil
}

func lookPathAvailable(lookPath func(string) (string, error), bin string) bool {
	if lookPath == nil {
		return false
	}
	_, err := lookPath(bin)
	return err == nil
}

func mapToolError(err error) (CallToolResult, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CallToolResult{}, err
	}
	if rpcErr, ok := errors.AsType[*RPCError](err); ok {
		return CallToolResult{}, rpcErr
	}
	return ErrorToolResult(err.Error()), nil
}

func resolvePlanTracks(ctx context.Context, in planMixArgs, deps PlanDeps, hasFFprobe bool) ([]probe.Track, error) {
	audioDir := strings.TrimSpace(in.AudioDir)
	if audioDir != "" {
		info, statErr := os.Stat(audioDir)
		if statErr != nil {
			return nil, fmt.Errorf("cannot access audioDir %q: %w", audioDir, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("audioDir %q is not a directory", audioDir)
		}
		if deps.LookPath != nil && !hasFFprobe {
			return nil, errors.New(`required binary "ffprobe" not found on PATH`)
		}
		return probe.ProbeTracks(ctx, audioDir, in.Shuffle, nil, deps.Runner)
	}

	tracks := make([]probe.Track, len(in.Tracks))
	type pendingProbe struct {
		index           int
		hasCustomTitle  bool
		customTitle     string
		namedTrackInput probe.NamedTrackFile
	}
	var pending []pendingProbe

	for i, item := range in.Tracks {
		if math.IsNaN(item.DurationSec) || math.IsInf(item.DurationSec, 0) || item.DurationSec < 0 {
			return nil, InvalidParamsError(fmt.Sprintf("tracks[%d].durationSec must be a finite number >= 0", i))
		}
		fileName := strings.TrimSpace(item.FileName)
		trackPath := strings.TrimSpace(item.Path)
		displayTitle := strings.TrimSpace(item.DisplayTitle)

		if item.DurationSec == 0 {
			if trackPath == "" {
				return nil, InvalidParamsError(fmt.Sprintf("tracks[%d] must provide durationSec > 0 or a non-empty path", i))
			}
			if err := probe.ValidateTrackFile(trackPath); err != nil {
				return nil, fmt.Errorf("tracks[%d]: %w", i, err)
			}
			if fileName == "" {
				fileName = filepath.Base(trackPath)
			}
			pending = append(pending, pendingProbe{
				index:          i,
				hasCustomTitle: displayTitle != "",
				customTitle:    displayTitle,
				namedTrackInput: probe.NamedTrackFile{
					Path:     trackPath,
					FileName: fileName,
				},
			})
			continue
		}

		if fileName == "" && trackPath != "" {
			fileName = filepath.Base(trackPath)
		}
		if displayTitle == "" {
			if fileName == "" {
				return nil, InvalidParamsError(fmt.Sprintf("tracks[%d]: fileName, path, or displayTitle is required", i))
			}
			displayTitle = probe.ResolveDisplayTitle("", fileName)
		}
		tracks[i] = probe.Track{
			Path:         trackPath,
			FileName:     fileName,
			Duration:     item.DurationSec,
			DisplayTitle: displayTitle,
		}
	}

	if len(pending) > 0 {
		if deps.LookPath != nil && !hasFFprobe {
			return nil, errors.New(`required binary "ffprobe" not found on PATH`)
		}
		if deps.Runner == nil {
			return nil, errors.New("ffprobe runner is required when explicit tracks omit durationSec")
		}
		namedFiles := make([]probe.NamedTrackFile, len(pending))
		for i, p := range pending {
			namedFiles[i] = p.namedTrackInput
		}
		probed, err := probe.ProbeNamedTrackFiles(ctx, namedFiles, deps.Runner)
		if err != nil {
			return nil, err
		}
		for i, p := range pending {
			tr := probed[i]
			if p.hasCustomTitle {
				tr.DisplayTitle = p.customTitle
			}
			tracks[p.index] = tr
		}
	}

	return tracks, nil
}

func handlePlanMix(ctx context.Context, args json.RawMessage, deps PlanDeps) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[planMixArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}

	audioDir := strings.TrimSpace(in.AudioDir)
	hasAudioDir := audioDir != ""
	hasTracks := len(in.Tracks) > 0
	if hasAudioDir && (hasTracks || in.Tracks != nil) {
		return CallToolResult{}, InvalidParamsError("audioDir and tracks are mutually exclusive; provide only one")
	}
	if !hasAudioDir && !hasTracks {
		if in.Tracks != nil {
			return CallToolResult{}, InvalidParamsError("tracks must contain at least one Track")
		}
		return CallToolResult{}, InvalidParamsError("either audioDir or tracks is required")
	}

	if math.IsNaN(in.Crossfade) || math.IsInf(in.Crossfade, 0) || in.Crossfade < 0 {
		return CallToolResult{}, InvalidParamsError("crossfade must be a finite number >= 0")
	}
	if in.TitleDuration < 0 {
		return CallToolResult{}, InvalidParamsError("titleDuration must be >= 0")
	}

	bg := strings.TrimSpace(in.BG)
	viz := strings.TrimSpace(in.Visualizer)
	if bg != "" && viz != "" {
		return CallToolResult{}, InvalidParamsError("bg and visualizer cannot be used together (omit bg for Path D Auto Visualizer, or omit visualizer to use a Background Visual file)")
	}

	fadeToBlack, err := parseOptionalDuration(in.FadeToBlack, "fadeToBlack")
	if err != nil {
		return CallToolResult{}, err
	}

	vizGlow, vizBrightness, err := resolveGlowAndBrightness(in.VisualizerGlow, in.VisualizerBrightness, "visualizerBrightness")
	if err != nil {
		return CallToolResult{}, err
	}

	resolvedTrackMedia, resolvedLayout, resolvedTransition, resolvedOverrides, err := resolveTrackMediaInputs(
		in.TrackMedia,
		in.Manifest,
		in.TrackMediaLayout,
		in.TrackTransition,
		in.TrackOverrides,
	)
	if err != nil {
		return CallToolResult{}, err
	}

	fontName := strings.TrimSpace(in.FontName)
	if fontName == "" {
		fontName = "Arial"
	}

	cfg := mixer.Config{
		AudioDir:             audioDir,
		BG:                   bg,
		Crossfade:            in.Crossfade,
		Normalize:            in.Normalize,
		OverlayTitles:        in.OverlayTitles,
		TitleDuration:        in.TitleDuration,
		FontName:             fontName,
		Shuffle:              in.Shuffle,
		Visualizer:           viz,
		VisualizerTheme:      in.VisualizerTheme,
		VisualizerResolution: in.VisualizerResolution,
		VisualizerGlow:       vizGlow,
		VisualizerBrightness: vizBrightness,
		FadeToBlack:          fadeToBlack,
		DimTitles:            in.DimTitles,
		TrackMedia:           resolvedTrackMedia,
		TrackMediaLayout:     resolvedLayout,
		TrackTransition:      resolvedTransition,
		TrackOverrides:       resolvedOverrides,
	}

	var normVizCfg render.VisualizerConfig
	if bg == "" {
		normVizCfg, err = render.ValidateVisualizerConfig(cfg.VisualizerConfig())
		if err != nil {
			return CallToolResult{}, InvalidParamsError(err.Error())
		}
	}

	okLookPath := func(bin string) (string, error) {
		return "/usr/bin/" + bin, nil
	}
	preparedCfg, pathKind, err := mixer.PreflightConfig(cfg, okLookPath, false)
	if err != nil {
		return mapToolError(err)
	}
	if strings.TrimSpace(preparedCfg.BG) == "" {
		normVizCfg, err = render.ValidateVisualizerConfig(preparedCfg.VisualizerConfig())
		if err != nil {
			return CallToolResult{}, InvalidParamsError(err.Error())
		}
	}

	hasFFmpeg := lookPathAvailable(deps.LookPath, "ffmpeg")
	hasFFprobe := lookPathAvailable(deps.LookPath, "ffprobe")

	tracks, err := resolvePlanTracks(ctx, in, deps, hasFFprobe)
	if err != nil {
		return mapToolError(err)
	}

	var bgMetaPtr *probe.BackgroundMeta
	if (pathKind == render.PathA || pathKind == render.PathB) && deps.Runner != nil && (deps.LookPath == nil || hasFFprobe) {
		if probed, probeErr := probe.ProbeBackgroundVisual(ctx, preparedCfg.BG, deps.Runner); probeErr == nil {
			bgMetaPtr = &probed
		} else if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			return CallToolResult{}, probeErr
		}
	}

	var estRunner mixer.CommandRunner
	if hasFFmpeg && deps.Runner != nil {
		estRunner = deps.Runner
	}
	est, err := mixer.EstimateConfig(ctx, preparedCfg, tracks, bgMetaPtr, okLookPath, estRunner)
	if err != nil {
		return mapToolError(err)
	}

	chapterTracks := make([]subtitle.ChapterTrack, len(tracks))
	for i, tr := range tracks {
		chapterTracks[i] = subtitle.ChapterTrack{
			Title:    tr.DisplayTitle,
			Duration: tr.Duration,
		}
	}
	sched := subtitle.BuildChapterSchedule(chapterTracks, in.Crossfade)
	includeHours := sched.TotalDuration >= 3600.0
	chapters := make([]PlannedChapter, len(tracks))
	for i, tr := range tracks {
		chapters[i] = PlannedChapter{
			Index:       i,
			StartSec:    sched.Starts[i],
			Timestamp:   subtitle.FormatChapterTime(sched.Starts[i], includeHours),
			Title:       tr.DisplayTitle,
			DurationSec: tr.Duration,
		}
	}

	cueCount := 0
	if in.OverlayTitles {
		cueCount = len(sched.Cues)
	}

	var outTrackMedia, outTrackLayout, outTrackTransition string
	if preparedCfg.Segmented {
		outTrackMedia = preparedCfg.TrackMedia
		if outTrackMedia == "" {
			outTrackMedia = "overrides"
		}
		outTrackLayout = preparedCfg.TrackMediaLayout
		outTrackTransition = preparedCfg.TrackTransition
		if strings.EqualFold(filepath.Ext(strings.TrimSpace(preparedCfg.TrackMedia)), ".json") {
			if mf, mfErr := mixer.ParseTrackMediaManifest(preparedCfg.TrackMedia); mfErr == nil {
				if (outTrackLayout == "" || outTrackLayout == string(mixer.TrackMediaLayoutAuto)) && strings.TrimSpace(mf.Layout) != "" {
					outTrackLayout = mf.Layout
				}
				if (outTrackTransition == "" || outTrackTransition == string(render.TransitionCut)) && strings.TrimSpace(mf.Transition) != "" {
					outTrackTransition = mf.Transition
				}
			}
		}
		if outTrackLayout == "" {
			outTrackLayout = string(mixer.TrackMediaLayoutAuto)
		}
		if outTrackTransition == "" {
			outTrackTransition = string(render.TransitionCut)
		}
		preparedCfg.TrackMediaLayout = outTrackLayout
		preparedCfg.TrackTransition = outTrackTransition
	}

	result := PlanMixResult{
		RenderingPath:       string(pathKind),
		RenderingPathDetail: describeRenderingPath(pathKind),
		Segmented:           preparedCfg.Segmented,
		TrackMedia:          outTrackMedia,
		TrackMediaLayout:    outTrackLayout,
		TrackTransition:     outTrackTransition,
		TrackCount:          len(tracks),
		TotalDurationSec:    sched.TotalDuration,
		CrossfadeSec:        in.Crossfade,
		Normalize:           in.Normalize,
		Estimate:            est,
		EstimateSummary:     mixer.FormatEstimateSummary(est),
		Chapters:            chapters,
		TimestampsText:      sched.Content,
		TitleOverlayPreview: TitleOverlayPreview{
			Enabled:          in.OverlayTitles,
			CueCount:         cueCount,
			DimTitles:        in.DimTitles,
			TitleDurationSec: in.TitleDuration,
			FontName:         fontName,
		},
		TradeOffNotes: buildPlanTradeOffNotes(tracks, preparedCfg, normVizCfg, pathKind),
		Warnings:      buildPlanWarnings(tracks, preparedCfg, pathKind, hasFFmpeg),
	}
	return JSONToolResult(result)
}

func describeRenderingPath(pathKind render.Path) string {
	switch pathKind {
	case render.PathA:
		return "Path A (Stream Copy Loop)"
	case render.PathB:
		return "Path B (Hardware-Accelerated Overlay Loop)"
	case render.PathC:
		return "Path C (Low-FPS Still Image)"
	case render.PathD:
		return "Path D (Auto Visualizer)"
	default:
		return string(pathKind)
	}
}

func buildPlanWarnings(tracks []probe.Track, cfg mixer.Config, pathKind render.Path, hasFFmpeg bool) []string {
	warnings := []string{}
	if cfg.Crossfade > 0 {
		if len(tracks) == 1 {
			warnings = append(warnings, fmt.Sprintf("only 1 Track provided; ytmix skips acrossfade (%.2fs) for single-track Mixes", cfg.Crossfade))
		} else {
			for _, tr := range tracks {
				label := tr.FileName
				if label == "" {
					label = tr.DisplayTitle
				}
				if tr.Duration <= cfg.Crossfade {
					warnings = append(warnings, fmt.Sprintf("Track %q duration (%.2fs) must exceed crossfade (%.2fs)", label, tr.Duration, cfg.Crossfade))
				} else if tr.Duration <= 2*cfg.Crossfade {
					warnings = append(warnings, fmt.Sprintf("Track %q duration (%.2fs) is <= 2x crossfade (%.2fs)", label, tr.Duration, 2*cfg.Crossfade))
				}
			}
		}
	}
	if cfg.DimTitles && !cfg.OverlayTitles {
		warnings = append(warnings, "dimTitles is true while overlayTitles is false; Title Overlays will not be burned")
	}
	if cfg.FadeToBlack > 0 && pathKind != render.PathD && !cfg.Segmented {
		warnings = append(warnings, fmt.Sprintf("fadeToBlack (%s) only applies to Path D Auto Visualizer and is ignored when bg is provided", cfg.FadeToBlack))
	}
	if !hasFFmpeg {
		warnings = append(warnings, "ffmpeg not detected on PATH; encoder probe and Path D filter checks were skipped (estimate uses libx264 prior)")
	}
	return warnings
}

func buildPlanTradeOffNotes(tracks []probe.Track, cfg mixer.Config, normVizCfg render.VisualizerConfig, pathKind render.Path) []string {
	notes := []string{}
	switch pathKind {
	case render.PathA:
		notes = append(notes,
			"Path A pre-normalizes the looping Background Visual once and muxes with -stream_loop -1 -c:v copy for near-instant video output.",
			"Trade-off: enabling overlayTitles on a video/GIF Background Visual switches from fast Path A stream-copy to Path B full video re-encoding, substantially increasing render time.",
		)
	case render.PathB:
		notes = append(notes,
			"Path B burns .ass Title Overlays across the full Mix duration using full video re-encoding (much slower than Path A stream-copy).",
			"Trade-off: disabling overlayTitles on a video/GIF Background Visual switches to fast Path A stream-copy loop.",
		)
	case render.PathC:
		if cfg.OverlayTitles {
			notes = append(notes, "Path C encodes a static image with -tune stillimage at 10 fps because overlayTitles is enabled; disabling overlayTitles drops to 2 fps for faster encoding.")
		} else {
			notes = append(notes, "Path C encodes a static image with -tune stillimage at 2 fps for minimal encode cost; enabling overlayTitles increases framerate to 10 fps for smooth 500ms subtitle fades.")
		}
	case render.PathD:
		if normVizCfg.Preset == "black" {
			if cfg.OverlayTitles {
				notes = append(notes, "Path D preset \"black\" encodes a pure #000000 stillimage screen at 10 fps because overlayTitles is enabled; disabling overlayTitles drops to 2 fps.")
			} else {
				notes = append(notes, "Path D preset \"black\" encodes a pure #000000 stillimage screen at 2 fps for ultra-fast blackout rendering.")
			}
		} else {
			notes = append(notes, "Path D generates a 30 fps audio-reactive FFmpeg filtergraph across the full Mix duration (heavier CPU/GPU load than Path A stream-copy or Path C stillimage; preset \"black\" uses 2 fps/10 fps stillimage).")
			switch normVizCfg.Resolution {
			case render.Resolution4K:
				notes = append(notes, "4k resolution (3840x2160) increases Path D filter and encoder load roughly 3.4x compared to 1080p.")
			case render.Resolution1440p:
				notes = append(notes, "1440p resolution (2560x1440) increases Path D filter and encoder load roughly 1.65x compared to 1080p.")
			}
		}
		if normVizCfg.FadeToBlack > 0 {
			notes = append(notes, fmt.Sprintf("fadeToBlack (%s) fades the Path D visual and any burned Title Overlays to #000000 black.", normVizCfg.FadeToBlack))
		}
	}

	if cfg.Segmented {
		notes = append(notes, "Per-Track media activates segmented rendering (Engine Option A): each Track slot renders an independent uniform-FPS H.264 segment in bounded-parallel workers and stitches via -c:v copy concat.")
		if strings.EqualFold(strings.TrimSpace(cfg.TrackTransition), string(render.TransitionDip)) {
			notes = append(notes, "Track transition \"dip\" inserts a 0.5s dip-to-black at consecutive Track segment boundaries.")
		}
	}
	if cfg.Normalize {
		notes = append(notes, "Loudness Normalization (-14 LUFS) runs EBU R128 loudnorm per Track in parallel before merging.")
	}
	if cfg.Crossfade > 0 && len(tracks) > 1 {
		notes = append(notes, fmt.Sprintf("Crossfade (%.2fs) overlaps consecutive Tracks via acrossfade and shifts subsequent Chapter Timestamps earlier.", cfg.Crossfade))
	}
	return notes
}

func handlePreviewVisualizerFrame(ctx context.Context, args json.RawMessage, deps PlanDeps) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[previewVisualizerFrameArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}

	offsetSec := defaultPreviewOffsetSec
	if in.OffsetSec != nil {
		offsetSec = *in.OffsetSec
		if math.IsNaN(offsetSec) || math.IsInf(offsetSec, 0) || offsetSec < 0 {
			return CallToolResult{}, InvalidParamsError("offsetSec must be a finite number >= 0")
		}
	}

	glow, brightness, err := resolveGlowAndBrightness(in.Glow, in.Brightness, "brightness")
	if err != nil {
		return CallToolResult{}, err
	}

	fadeToBlack, err := parseOptionalDuration(in.FadeToBlack, "fadeToBlack")
	if err != nil {
		return CallToolResult{}, err
	}

	normCfg, err := render.ValidateVisualizerConfig(render.VisualizerConfig{
		Preset:      in.Preset,
		Theme:       in.Theme,
		Resolution:  in.Resolution,
		Glow:        glow,
		Brightness:  brightness,
		FadeToBlack: fadeToBlack,
	})
	if err != nil {
		return CallToolResult{}, InvalidParamsError(err.Error())
	}

	audioPath := strings.TrimSpace(in.AudioPath)
	audioSource := "synthetic"
	if audioPath != "" {
		if err := probe.ValidateTrackFile(audioPath); err != nil {
			return ErrorToolResult(err.Error()), nil
		}
		audioSource = "file"
	}

	if !lookPathAvailable(deps.LookPath, "ffmpeg") {
		return ErrorToolResult(`required binary "ffmpeg" not found on PATH`), nil
	}
	if deps.Runner == nil {
		return ErrorToolResult("command runner is required for preview_visualizer_frame"), nil
	}

	filters, err := render.DetectFilters(ctx, deps.Runner)
	if err != nil {
		return mapToolError(err)
	}
	if ok, missing := render.PresetAvailable(normCfg.Preset, filters); !ok {
		return ErrorToolResult(fmt.Sprintf("auto visualizer %q requires FFmpeg filter %q which is not available in this ffmpeg build", normCfg.Preset, missing)), nil
	}

	tempDir, err := os.MkdirTemp("", "ytmix-mcp-preview-*")
	if err != nil {
		return ErrorToolResult(fmt.Sprintf("create temp preview workspace: %v", err)), nil
	}
	defer os.RemoveAll(tempDir)

	totalDuration := math.Max(offsetSec+30.0, normCfg.FadeToBlack.Seconds()+30.0)
	var assPath string
	if in.OverlayTitles {
		assPath = filepath.Join(tempDir, "titles.ass")
		titleText := strings.TrimSpace(in.TitleText)
		if titleText == "" {
			titleText = "Sample Track Title"
		}
		cues := []subtitle.TitleCue{{Title: titleText, Start: 0}}
		if err := subtitle.WriteASSWithOptions(assPath, cues, totalDuration, subtitle.ASSOptions{
			FontName:  "Arial",
			DimTitles: in.DimTitles,
		}); err != nil {
			return ErrorToolResult(err.Error()), nil
		}
	}

	var audioInput string
	isLavfi := false
	if audioPath != "" {
		audioInput = audioPath
	} else {
		isLavfi = true
		lavfiDur := math.Max(1.0, offsetSec+0.5)
		audioInput = fmt.Sprintf(
			"aevalsrc=0.35*sin(2*PI*110*t)+0.25*sin(2*PI*440*t)+0.15*sin(2*PI*1760*t)|0.35*sin(2*PI*110*t+0.6)+0.25*sin(2*PI*660*t)+0.15*sin(2*PI*2200*t):c=stereo:s=48000:d=%s",
			strconv.FormatFloat(lavfiDur, 'f', 2, 64),
		)
	}

	outPNGPath := filepath.Join(tempDir, "preview.png")
	ffmpegArgs, err := render.BuildPathDPreviewArgs(render.PreviewOptions{
		AudioInput:    audioInput,
		IsLavfi:       isLavfi,
		ASSPath:       assPath,
		OutPNGPath:    outPNGPath,
		OverlayTitles: in.OverlayTitles,
		OffsetSec:     offsetSec,
		TotalDuration: totalDuration,
		Config:        normCfg,
	})
	if err != nil {
		return ErrorToolResult(err.Error()), nil
	}

	if _, err := deps.Runner(ctx, "ffmpeg", ffmpegArgs...); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CallToolResult{}, err
		}
		return ErrorToolResult(fmt.Sprintf("render preview frame: %v", err)), nil
	}

	// #nosec G304 G703 -- outPNGPath is a fixed literal filename inside a process-owned os.MkdirTemp directory.
	pngBytes, err := os.ReadFile(outPNGPath)
	if err != nil {
		return ErrorToolResult(fmt.Sprintf("read preview PNG: %v", err)), nil
	}
	if len(pngBytes) == 0 {
		return ErrorToolResult("ffmpeg produced an empty preview PNG"), nil
	}

	resSpec, _ := render.LookupResolution(normCfg.Resolution)
	meta := PreviewVisualizerResult{
		Preset:        normCfg.Preset,
		Theme:         normCfg.Theme,
		Resolution:    normCfg.Resolution,
		Width:         resSpec.Width,
		Height:        resSpec.Height,
		Glow:          normCfg.Glow,
		Brightness:    normCfg.Brightness,
		DimTitles:     in.DimTitles,
		OverlayTitles: in.OverlayTitles,
		AudioSource:   audioSource,
		OffsetSec:     offsetSec,
		ByteSize:      len(pngBytes),
		MimeType:      "image/png",
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return CallToolResult{}, fmt.Errorf("marshal preview metadata: %w", err)
	}

	return CallToolResult{
		Content: []ContentBlock{
			{
				Type:     "image",
				Data:     base64.StdEncoding.EncodeToString(pngBytes),
				MimeType: "image/png",
			},
			{
				Type: "text",
				Text: string(metaJSON),
			},
		},
		StructuredContent: meta,
	}, nil
}
