package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/GCherry/ytmix/internal/probe"
	"github.com/GCherry/ytmix/internal/render"
)

const (
	targetAspect16x9    = 16.0 / 9.0
	aspect16x9Tolerance = 0.02
	minFullHDWidth      = 1920
	minFullHDHeight     = 1080
)

var (
	numericPrefixPattern = regexp.MustCompile(`^\d+[\s._-]+`)
	draftMarkerPattern   = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(final|v\d+|copy|draft|edit|master)(?:$|[^a-z0-9])`)
)

// InspectDeps supplies command execution for read-only media inspection tools.
type InspectDeps struct {
	Runner CommandRunner
}

// InspectedTrack describes one probed .mp3 Track and any Track-level pre-flight warnings.
type InspectedTrack struct {
	Index        int      `json:"index"`
	Path         string   `json:"path"`
	FileName     string   `json:"fileName"`
	DurationSec  float64  `json:"durationSec"`
	RawID3Title  string   `json:"rawId3Title,omitempty"`
	DisplayTitle string   `json:"displayTitle"`
	Warnings     []string `json:"warnings,omitempty"`
}

// InspectTracksResult is the structured payload returned by the inspect_tracks MCP tool.
type InspectTracksResult struct {
	AudioDir                string           `json:"audioDir"`
	TrackCount              int              `json:"trackCount"`
	TotalDurationSec        float64          `json:"totalDurationSec"`
	EstimatedMixDurationSec float64          `json:"estimatedMixDurationSec"`
	CrossfadeSec            float64          `json:"crossfadeSec"`
	Tracks                  []InspectedTrack `json:"tracks"`
	Warnings                []string         `json:"warnings"`
}

// InspectBackgroundResult is the structured payload returned by the inspect_background MCP tool.
type InspectBackgroundResult struct {
	BGPath      string   `json:"bgPath"`
	FileName    string   `json:"fileName"`
	Kind        string   `json:"kind"`
	Extension   string   `json:"extension"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	AspectRatio string   `json:"aspectRatio"`
	Is16x9      bool     `json:"is16x9"`
	DurationSec float64  `json:"durationSec"`
	FPS         float64  `json:"fps"`
	Codec       string   `json:"codec"`
	PixFmt      string   `json:"pixFmt"`
	NBFrames    int64    `json:"nbFrames"`
	HasAudio    bool     `json:"hasAudio"`
	Warnings    []string `json:"warnings"`
}

type inspectTracksArgs struct {
	AudioDir  string  `json:"audioDir"`
	Crossfade float64 `json:"crossfade"`
}

type inspectBackgroundArgs struct {
	BGPath string `json:"bgPath"`
}

// RegisterInspectTools registers the read-only inspect_tracks and inspect_background tools on r.
func RegisterInspectTools(r *Registry, deps InspectDeps) {
	if r == nil {
		return
	}

	r.RegisterTool(Tool{
		Name:        "inspect_tracks",
		Description: "Audit an audio directory of .mp3 Tracks in default sorted order, returning durations, ID3 vs. resolved Display Titles, estimated Mix duration, and Crossfade or filename pre-flight warnings.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"audioDir": map[string]any{
					"type":        "string",
					"description": "Path to the directory containing input .mp3 Tracks.",
				},
				"crossfade": map[string]any{
					"type":        "number",
					"description": "Optional Crossfade duration in seconds between Tracks (default 0).",
					"default":     0,
					"minimum":     0,
				},
			},
			"required":             []string{"audioDir"},
			"additionalProperties": false,
		},
		Annotations: &ToolAnnotations{
			Title:           "Inspect Audio Tracks",
			ReadOnlyHint:    new(true),
			DestructiveHint: new(false),
			IdempotentHint:  new(true),
			OpenWorldHint:   new(false),
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return handleInspectTracks(ctx, args, deps)
	})

	r.RegisterTool(Tool{
		Name:        "inspect_background",
		Description: "Audit a Background Visual (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif) via ffprobe, returning dimensions, aspect ratio, media kind, frame rate, duration, codec, audio stream presence, and pre-flight warnings.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bgPath": map[string]any{
					"type":        "string",
					"description": "Path to the Background Visual image, video, or GIF file.",
				},
			},
			"required":             []string{"bgPath"},
			"additionalProperties": false,
		},
		Annotations: &ToolAnnotations{
			Title:           "Inspect Background Visual",
			ReadOnlyHint:    new(true),
			DestructiveHint: new(false),
			IdempotentHint:  new(true),
			OpenWorldHint:   new(false),
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return handleInspectBackground(ctx, args, deps)
	})
}

func handleInspectTracks(ctx context.Context, args json.RawMessage, deps InspectDeps) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[inspectTracksArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}

	audioDir := strings.TrimSpace(in.AudioDir)
	if audioDir == "" {
		return CallToolResult{}, InvalidParamsError("audioDir is required and must be a non-empty string")
	}
	if math.IsNaN(in.Crossfade) || math.IsInf(in.Crossfade, 0) || in.Crossfade < 0 {
		return CallToolResult{}, InvalidParamsError("crossfade must be a finite number >= 0")
	}

	info, err := os.Stat(audioDir)
	if err != nil {
		return ErrorToolResult(fmt.Sprintf("cannot access audioDir %q: %v", audioDir, err)), nil
	}
	if !info.IsDir() {
		return ErrorToolResult(fmt.Sprintf("audioDir %q is not a directory", audioDir)), nil
	}

	tracks, err := probe.ProbeTracks(ctx, audioDir, false, nil, deps.Runner)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CallToolResult{}, err
		}
		return ErrorToolResult(err.Error()), nil
	}

	warnings := []string{}
	if in.Crossfade > 0 && len(tracks) == 1 {
		warnings = append(warnings, fmt.Sprintf("only 1 Track found in %q; ytmix skips acrossfade (%.2fs) for single-track Mixes", audioDir, in.Crossfade))
	}

	inspected := make([]InspectedTrack, len(tracks))
	var totalDuration float64
	for i, tr := range tracks {
		totalDuration += tr.Duration
		var trackWarnings []string

		if in.Crossfade > 0 && len(tracks) > 1 {
			if tr.Duration <= in.Crossfade {
				trackWarnings = append(trackWarnings, fmt.Sprintf("duration %.2fs does not exceed crossfade %.2fs (exceeds acrossfade limit)", tr.Duration, in.Crossfade))
				warnings = append(warnings, fmt.Sprintf("Track %q duration (%.2fs) must exceed crossfade (%.2fs)", tr.FileName, tr.Duration, in.Crossfade))
			} else if tr.Duration <= 2*in.Crossfade {
				trackWarnings = append(trackWarnings, fmt.Sprintf("duration %.2fs is <= 2x crossfade (%.2fs); overlaps leave little or no un-faded audio", tr.Duration, 2*in.Crossfade))
				warnings = append(warnings, fmt.Sprintf("Track %q duration (%.2fs) is <= 2x crossfade (%.2fs)", tr.FileName, tr.Duration, 2*in.Crossfade))
			}
		}

		if tr.RawID3Title == "" {
			if artifacts := detectFilenameArtifacts(tr.FileName); len(artifacts) > 0 {
				artifactList := strings.Join(artifacts, ", ")
				trackWarnings = append(trackWarnings, fmt.Sprintf("missing ID3 title; filename contains %s (resolved Display Title: %q)", artifactList, tr.DisplayTitle))
				warnings = append(warnings, fmt.Sprintf("Track %q has no ID3 title and filename contains %s (resolved Display Title: %q)", tr.FileName, artifactList, tr.DisplayTitle))
			}
		}

		inspected[i] = InspectedTrack{
			Index:        i,
			Path:         tr.Path,
			FileName:     tr.FileName,
			DurationSec:  tr.Duration,
			RawID3Title:  tr.RawID3Title,
			DisplayTitle: tr.DisplayTitle,
			Warnings:     trackWarnings,
		}
	}

	result := InspectTracksResult{
		AudioDir:                audioDir,
		TrackCount:              len(tracks),
		TotalDurationSec:        totalDuration,
		EstimatedMixDurationSec: estimateMixDuration(tracks, in.Crossfade),
		CrossfadeSec:            in.Crossfade,
		Tracks:                  inspected,
		Warnings:                warnings,
	}
	return JSONToolResult(result)
}

func estimateMixDuration(tracks []probe.Track, crossfade float64) float64 {
	if len(tracks) == 0 {
		return 0
	}
	if len(tracks) == 1 || crossfade <= 0 {
		var total float64
		for _, tr := range tracks {
			total += tr.Duration
		}
		return total
	}
	start := 0.0
	for i := 0; i < len(tracks)-1; i++ {
		start += tracks[i].Duration - crossfade
		if start < 0 {
			start = 0
		}
	}
	return start + tracks[len(tracks)-1].Duration
}

func detectFilenameArtifacts(fileName string) []string {
	base := filepath.Base(fileName)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	var artifacts []string
	if numericPrefixPattern.MatchString(stem) {
		artifacts = append(artifacts, "numeric prefix")
	}
	if strings.Count(stem, "_") >= 2 {
		artifacts = append(artifacts, "multiple underscores")
	}
	if draftMarkerPattern.MatchString(stem) {
		artifacts = append(artifacts, "draft/version marker")
	}
	return artifacts
}

func handleInspectBackground(ctx context.Context, args json.RawMessage, deps InspectDeps) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[inspectBackgroundArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}

	bgPath := strings.TrimSpace(in.BGPath)
	if bgPath == "" {
		return CallToolResult{}, InvalidParamsError("bgPath is required and must be a non-empty string")
	}

	info, err := os.Stat(bgPath)
	if err != nil {
		return ErrorToolResult(fmt.Sprintf("cannot access Background Visual %q: %v", bgPath, err)), nil
	}
	if info.IsDir() {
		return ErrorToolResult(fmt.Sprintf("bgPath %q is a directory, expected a Background Visual file", bgPath)), nil
	}
	if !info.Mode().IsRegular() {
		return ErrorToolResult(fmt.Sprintf("bgPath %q is not a regular file", bgPath)), nil
	}

	kind, ext, err := render.ClassifyBackground(bgPath)
	if err != nil {
		return ErrorToolResult(err.Error()), nil
	}

	meta, err := probe.ProbeBackgroundVisual(ctx, bgPath, deps.Runner)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CallToolResult{}, err
		}
		return ErrorToolResult(err.Error()), nil
	}

	warnings := []string{}
	aspectRatio := ""
	is16x9 := false
	if meta.Width <= 0 || meta.Height <= 0 {
		warnings = append(warnings, "missing or zero video dimensions in Background Visual metadata")
	} else {
		aspectRatio = formatAspectRatio(meta.Width, meta.Height)
		ratio := float64(meta.Width) / float64(meta.Height)
		is16x9 = math.Abs(ratio-targetAspect16x9) <= aspect16x9Tolerance
		if !is16x9 {
			warnings = append(warnings, fmt.Sprintf("aspect ratio %s (%dx%d) is not 16:9; ytmix scales and pads Background Visuals to 16:9 (1920x1080)", aspectRatio, meta.Width, meta.Height))
		}
		if meta.Width < minFullHDWidth || meta.Height < minFullHDHeight {
			warnings = append(warnings, fmt.Sprintf("resolution %dx%d is below 1080p (1920x1080); ytmix will upscale to 1920x1080", meta.Width, meta.Height))
		}
	}

	if kind == render.BackgroundKindVideo && meta.HasAudio {
		warnings = append(warnings, "Background Visual contains an audio stream; ytmix strips stray background audio via -map 0:v:0 -map 1:a:0")
	}

	result := InspectBackgroundResult{
		BGPath:      bgPath,
		FileName:    filepath.Base(bgPath),
		Kind:        string(kind),
		Extension:   ext,
		Width:       meta.Width,
		Height:      meta.Height,
		AspectRatio: aspectRatio,
		Is16x9:      is16x9,
		DurationSec: meta.DurationSec,
		FPS:         meta.FPS,
		Codec:       meta.CodecName,
		PixFmt:      meta.PixFmt,
		NBFrames:    meta.NBFrames,
		HasAudio:    meta.HasAudio,
		Warnings:    warnings,
	}
	return JSONToolResult(result)
}

func formatAspectRatio(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	g := gcdInt(width, height)
	return fmt.Sprintf("%d:%d", width/g, height/g)
}

func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}
