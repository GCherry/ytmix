package mcp

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"

	"github.com/GCherry/ytmix/internal/render"
)

// CapabilitiesDeps supplies host binary lookup, command execution, and OS metadata for get_capabilities.
type CapabilitiesDeps struct {
	LookPath func(string) (string, error)
	Runner   CommandRunner
	GOOS     string
}

// EncoderCapability describes a detected H.264 video encoder and its FFmpeg CLI flags.
type EncoderCapability struct {
	Name string   `json:"name"`
	Args []string `json:"args,omitempty"`
}

// VisualizerCapability describes one Path D Auto Visualizer preset and its live FFmpeg filter status.
type VisualizerCapability struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Filter        string `json:"filter"`
	Category      string `json:"category"`
	Description   string `json:"description"`
	IsDark        bool   `json:"isDark"`
	Available     bool   `json:"available"`
	MissingFilter string `json:"missingFilter,omitempty"`
}

// CapabilitiesResult is the structured payload returned by the get_capabilities MCP tool.
type CapabilitiesResult struct {
	Status                  string                  `json:"status"`
	FFmpeg                  bool                    `json:"ffmpeg"`
	FFprobe                 bool                    `json:"ffprobe"`
	GOOS                    string                  `json:"goos"`
	Encoder                 EncoderCapability       `json:"encoder"`
	PathDEncoder            EncoderCapability       `json:"pathDEncoder"`
	SupportedBackgroundExts render.BackgroundExts   `json:"supportedBackgroundExts"`
	Visualizers             []VisualizerCapability  `json:"visualizers"`
	VisualizerThemes        []render.ThemeSpec      `json:"visualizerThemes"`
	VisualizerResolutions   []render.ResolutionSpec `json:"visualizerResolutions"`
}

// RegisterCapabilitiesTool registers the read-only get_capabilities discovery tool on r.
func RegisterCapabilitiesTool(r *Registry, deps CapabilitiesDeps) {
	if r == nil {
		return
	}
	r.RegisterTool(Tool{
		Name:        "get_capabilities",
		Description: "Inspect local FFmpeg/FFprobe availability, hardware H.264 encoders, Path D Auto Visualizer presets, Color Themes, resolutions, and supported Background Visual extensions.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		if ctx != nil && ctx.Err() != nil {
			return CallToolResult{}, ctx.Err()
		}
		if len(args) > 0 {
			var extra map[string]any
			if err := json.Unmarshal(args, &extra); err != nil {
				return CallToolResult{}, InvalidParamsError("invalid get_capabilities arguments")
			}
			if len(extra) > 0 {
				return CallToolResult{}, InvalidParamsError("get_capabilities does not accept any arguments")
			}
		}
		caps := BuildCapabilities(ctx, deps)
		return JSONToolResult(caps)
	})
}

// BuildCapabilities probes host binaries, encoders, and FFmpeg filters and returns ytmix capabilities.
func BuildCapabilities(ctx context.Context, deps CapabilitiesDeps) CapabilitiesResult {
	if ctx == nil {
		ctx = context.Background()
	}
	goos := strings.TrimSpace(deps.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}

	var hasFFmpeg, hasFFprobe bool
	if deps.LookPath != nil {
		_, errFFmpeg := deps.LookPath("ffmpeg")
		_, errFFprobe := deps.LookPath("ffprobe")
		hasFFmpeg = errFFmpeg == nil
		hasFFprobe = errFFprobe == nil
	}

	status := "ok"
	if !hasFFmpeg || !hasFFprobe {
		status = "degraded"
	}

	enc := EncoderCapability{Name: "unavailable"}
	pathDEnc := EncoderCapability{Name: "unavailable"}
	var availFilters map[string]bool
	var filtersProbed bool

	if hasFFmpeg && deps.Runner != nil {
		detectedEnc := render.DetectEncoder(ctx, goos, deps.Runner)
		enc = EncoderCapability{
			Name: detectedEnc.Name,
			Args: append([]string(nil), detectedEnc.Args...),
		}

		detectedPathDEnc := render.DetectPathDEncoder(ctx, goos, render.DefaultVisualizerResolution, deps.Runner)
		pathDEnc = EncoderCapability{
			Name: detectedPathDEnc.Name,
			Args: append([]string(nil), detectedPathDEnc.Args...),
		}

		if filters, err := render.DetectFilters(ctx, deps.Runner); err == nil {
			availFilters = filters
			filtersProbed = true
		}
	}

	specs := render.ListVisualizers()
	visualizers := make([]VisualizerCapability, len(specs))
	for i, spec := range specs {
		var ok bool
		var missing string
		if filtersProbed {
			ok, missing = render.PresetAvailable(spec.ID, availFilters)
		}
		visualizers[i] = VisualizerCapability{
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

	return CapabilitiesResult{
		Status:                  status,
		FFmpeg:                  hasFFmpeg,
		FFprobe:                 hasFFprobe,
		GOOS:                    goos,
		Encoder:                 enc,
		PathDEncoder:            pathDEnc,
		SupportedBackgroundExts: render.SupportedBackgroundExts(),
		Visualizers:             visualizers,
		VisualizerThemes:        render.ListThemes(),
		VisualizerResolutions:   render.ListResolutions(),
	}
}
