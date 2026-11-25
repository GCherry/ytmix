package mcp

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"sync"

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

type capabilitiesProbeCache struct {
	mu            sync.Mutex
	probed        bool
	encoder       EncoderCapability
	pathDEncoder  EncoderCapability
	availFilters  map[string]bool
	filtersProbed bool
}

func (c *capabilitiesProbeCache) probeFFmpeg(ctx context.Context, goos string, runner CommandRunner) (EncoderCapability, EncoderCapability, map[string]bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.probed {
		return cloneEncoderCapability(c.encoder), cloneEncoderCapability(c.pathDEncoder), cloneFilterMap(c.availFilters), c.filtersProbed
	}

	enc, pathDEnc, availFilters, filtersProbed := probeFFmpegDetails(ctx, goos, runner)
	if ctx.Err() == nil {
		c.encoder = cloneEncoderCapability(enc)
		c.pathDEncoder = cloneEncoderCapability(pathDEnc)
		c.availFilters = cloneFilterMap(availFilters)
		c.filtersProbed = filtersProbed
		c.probed = true
	}
	return enc, pathDEnc, availFilters, filtersProbed
}

func cloneEncoderCapability(enc EncoderCapability) EncoderCapability {
	return EncoderCapability{
		Name: enc.Name,
		Args: append([]string(nil), enc.Args...),
	}
}

func cloneFilterMap(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func probeFFmpegDetails(ctx context.Context, goos string, runner CommandRunner) (EncoderCapability, EncoderCapability, map[string]bool, bool) {
	detectedEnc := render.DetectEncoder(ctx, goos, runner)
	enc := EncoderCapability{
		Name: detectedEnc.Name,
		Args: append([]string(nil), detectedEnc.Args...),
	}

	detectedPathDEnc := render.DetectPathDEncoder(ctx, goos, render.DefaultVisualizerResolution, runner)
	pathDEnc := EncoderCapability{
		Name: detectedPathDEnc.Name,
		Args: append([]string(nil), detectedPathDEnc.Args...),
	}

	var availFilters map[string]bool
	var filtersProbed bool
	if filters, err := render.DetectFilters(ctx, runner); err == nil {
		availFilters = filters
		filtersProbed = true
	}
	return enc, pathDEnc, availFilters, filtersProbed
}

// RegisterCapabilitiesTool registers the read-only get_capabilities discovery tool on r.
func RegisterCapabilitiesTool(r *Registry, deps CapabilitiesDeps) {
	if r == nil {
		return
	}
	var cache capabilitiesProbeCache
	r.RegisterTool(Tool{
		Name:        "get_capabilities",
		Description: "Inspect local FFmpeg/FFprobe availability, hardware H.264 encoders, Path D Auto Visualizer presets, Color Themes, resolutions, and supported Background Visual extensions.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
		Annotations: &ToolAnnotations{
			Title:           "Get ytmix Capabilities",
			ReadOnlyHint:    BoolPtr(true),
			DestructiveHint: BoolPtr(false),
			IdempotentHint:  BoolPtr(true),
			OpenWorldHint:   BoolPtr(false),
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		if ctx != nil && ctx.Err() != nil {
			return CallToolResult{}, ctx.Err()
		}
		if _, err := DecodeToolArgs[struct{}](args); err != nil {
			return CallToolResult{}, err
		}
		caps := buildCapabilitiesWithCache(ctx, deps, &cache)
		return JSONToolResult(caps)
	})
}

// BuildCapabilities probes host binaries, encoders, and FFmpeg filters and returns ytmix capabilities.
func BuildCapabilities(ctx context.Context, deps CapabilitiesDeps) CapabilitiesResult {
	return buildCapabilitiesWithCache(ctx, deps, nil)
}

func buildCapabilitiesWithCache(ctx context.Context, deps CapabilitiesDeps, cache *capabilitiesProbeCache) CapabilitiesResult {
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
		if cache != nil {
			enc, pathDEnc, availFilters, filtersProbed = cache.probeFFmpeg(ctx, goos, deps.Runner)
		} else {
			enc, pathDEnc, availFilters, filtersProbed = probeFFmpegDetails(ctx, goos, deps.Runner)
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
