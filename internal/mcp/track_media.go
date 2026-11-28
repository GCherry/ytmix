package mcp

import (
	"fmt"
	"strings"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/render"
)

type trackOverrideInput struct {
	TrackIndex    *int   `json:"trackIndex,omitempty"`
	TrackFile     string `json:"trackFile,omitempty"`
	MediaPath     string `json:"mediaPath,omitempty"`
	LayerMode     string `json:"layerMode,omitempty"`
	ExplicitEmpty bool   `json:"explicitEmpty,omitempty"`
}

func addTrackMediaSchemaProperties(props map[string]any) {
	props["trackMedia"] = map[string]any{
		"type":        "string",
		"description": "Per-Track visual discovery mode: \"off\" (default, standard Paths A-D), \"auto\" (discover sidecar images/videos and embedded ID3 cover art), or a local .json track-media manifest path. Mutually exclusive with manifest.",
		"default":     string(mixer.TrackMediaOff),
	}
	props["manifest"] = map[string]any{
		"type":        "string",
		"description": "Optional local .json track-media manifest file path (shorthand for passing a .json manifest via trackMedia). Mutually exclusive with trackMedia.",
	}
	props["trackMediaLayout"] = map[string]any{
		"type":        "string",
		"description": "Default Layer 1 framing layout for per-Track visuals: \"auto\" (Art Card for ID3/near-square, Fullscreen for widescreen), \"fullscreen\", or \"card\" (default auto).",
		"enum": []string{
			string(mixer.TrackMediaLayoutAuto),
			string(mixer.TrackMediaLayoutFullscreen),
			string(mixer.TrackMediaLayoutCard),
		},
		"default": string(mixer.TrackMediaLayoutAuto),
	}
	props["trackTransition"] = map[string]any{
		"type":        "string",
		"description": "Visual transition between consecutive Track segments when per-Track media is active: \"cut\" (default) or \"dip\" (0.5s black dip).",
		"enum": []string{
			string(render.TransitionCut),
			string(render.TransitionDip),
		},
		"default": string(render.TransitionCut),
	}
	props["trackOverrides"] = map[string]any{
		"type":        "array",
		"description": "Optional per-Track Layer 1 visual overrides applied on top of trackMedia/manifest discovery. Each entry targets a Track by 0-based trackIndex or trackFile.",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"trackIndex": map[string]any{
					"type":        "integer",
					"minimum":     0,
					"description": "0-based index of the target Track (alternative to trackFile).",
				},
				"trackFile": map[string]any{
					"type":        "string",
					"description": "Filename or path of the target Track (alternative to trackIndex).",
				},
				"mediaPath": map[string]any{
					"type":        "string",
					"description": "Optional local image, video, or GIF path attached to this Track.",
				},
				"layerMode": map[string]any{
					"type":        "string",
					"description": "Layer 1 composition mode: \"fullscreen\", \"card\" (Art Card over Layer 0), or \"backdrop\" (L0 Only).",
					"enum": []string{
						string(render.LayerFullscreen),
						string(render.LayerArtCard),
						string(render.LayerBackdrop),
					},
				},
				"explicitEmpty": map[string]any{
					"type":        "boolean",
					"description": "When true, forces this Track to L0 Only (backdrop) and suppresses auto-discovered sidecar/ID3 media.",
					"default":     false,
				},
			},
			"additionalProperties": false,
		},
	}
}

func resolveTrackMediaInputs(
	trackMediaRaw string,
	manifestRaw string,
	layoutRaw string,
	transitionRaw string,
	overrides []trackOverrideInput,
) (resolvedTrackMedia, resolvedLayout, resolvedTransition string, resolvedOverrides []mixer.TrackMediaOverride, err error) {
	trackMedia := strings.TrimSpace(trackMediaRaw)
	manifest := strings.TrimSpace(manifestRaw)
	if trackMedia != "" && manifest != "" {
		return "", "", "", nil, InvalidParamsError("trackMedia and manifest are mutually exclusive; provide only one")
	}

	if manifest != "" {
		if strings.EqualFold(manifest, string(mixer.TrackMediaOff)) || strings.EqualFold(manifest, string(mixer.TrackMediaAuto)) {
			return "", "", "", nil, InvalidParamsError("manifest must be a .json file path (use trackMedia for \"off\" or \"auto\")")
		}
		if err := mixer.ValidateTrackMedia(manifest); err != nil {
			return "", "", "", nil, InvalidParamsError(err.Error())
		}
		resolvedTrackMedia = manifest
	} else if trackMedia != "" {
		if err := mixer.ValidateTrackMedia(trackMedia); err != nil {
			return "", "", "", nil, InvalidParamsError(err.Error())
		}
		resolvedTrackMedia = trackMedia
	}

	if strings.TrimSpace(layoutRaw) != "" {
		normLayout, err := mixer.ValidateTrackMediaLayout(layoutRaw)
		if err != nil {
			return "", "", "", nil, InvalidParamsError(err.Error())
		}
		resolvedLayout = string(normLayout)
	}

	if strings.TrimSpace(transitionRaw) != "" {
		normTransition, err := render.ValidateTrackTransition(render.TrackTransition(transitionRaw))
		if err != nil {
			return "", "", "", nil, InvalidParamsError(err.Error())
		}
		resolvedTransition = string(normTransition)
	}

	if len(overrides) > 0 {
		resolvedOverrides = make([]mixer.TrackMediaOverride, len(overrides))
		for i, ov := range overrides {
			trackFile := strings.TrimSpace(ov.TrackFile)
			if ov.TrackIndex == nil && trackFile == "" {
				return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d] must specify trackIndex or trackFile", i))
			}
			if ov.TrackIndex != nil && *ov.TrackIndex < 0 {
				return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d].trackIndex must be >= 0", i))
			}
			if strings.ContainsRune(trackFile, '\x00') {
				return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d].trackFile must not contain null bytes", i))
			}

			mediaPath := strings.TrimSpace(ov.MediaPath)
			if strings.ContainsRune(mediaPath, '\x00') {
				return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d].mediaPath must not contain null bytes", i))
			}
			if ov.ExplicitEmpty && mediaPath != "" {
				return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d] cannot set explicitEmpty=true when mediaPath is provided", i))
			}

			var layerMode render.TrackLayerMode
			if strings.TrimSpace(ov.LayerMode) != "" {
				normMode, err := render.ValidateTrackLayerMode(render.TrackLayerMode(ov.LayerMode))
				if err != nil {
					return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d].layerMode: %v", i, err))
				}
				layerMode = normMode
			}

			var mediaKind render.BackgroundKind
			if mediaPath != "" {
				kind, _, err := render.ClassifyBackground(mediaPath)
				if err != nil {
					return "", "", "", nil, InvalidParamsError(fmt.Sprintf("trackOverrides[%d].mediaPath: %v", i, err))
				}
				mediaKind = kind
			}

			trackIdx := 0
			if ov.TrackIndex != nil {
				trackIdx = *ov.TrackIndex
			}
			explicitEmpty := ov.ExplicitEmpty || (mediaPath == "" && layerMode == render.LayerBackdrop)
			resolvedOverrides[i] = mixer.TrackMediaOverride{
				TrackIndex:    trackIdx,
				TrackFile:     trackFile,
				MediaPath:     mediaPath,
				MediaKind:     mediaKind,
				LayerMode:     layerMode,
				ExplicitEmpty: explicitEmpty,
			}
		}
	}

	return resolvedTrackMedia, resolvedLayout, resolvedTransition, resolvedOverrides, nil
}
