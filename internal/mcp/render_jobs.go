package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/probe"
	"github.com/GCherry/ytmix/internal/render"
)

const (
	defaultMaxRenderJobs = 5
	cancelWaitGrace      = 500 * time.Millisecond
	maxTimestampsBytes   = 4 << 20 // 4 MiB safety cap for reading timestamps.txt
)

// RunFunc executes the full Mix pipeline from cfg.AudioDir.
type RunFunc func(ctx context.Context, cfg mixer.Config, sink mixer.TelemetrySink, lookPath func(string) (string, error), runner mixer.CommandRunner, streamRunner mixer.StreamRunner) error

// RunWithTracksFunc executes the full Mix pipeline using an explicit ordered Track slice.
type RunWithTracksFunc func(ctx context.Context, cfg mixer.Config, tracks []mixer.Track, sink mixer.TelemetrySink, lookPath func(string) (string, error), runner mixer.CommandRunner, streamRunner mixer.StreamRunner) error

// RenderDeps supplies host binary lookup, command runners, optional pipeline hooks, and job retention limits.
type RenderDeps struct {
	LookPath        func(string) (string, error)
	Runner          CommandRunner
	StreamRunner    mixer.StreamRunner
	RunFn           RunFunc
	RunWithTracksFn RunWithTracksFunc
	MaxJobs         int
}

// RenderJobStatus identifies the lifecycle state of an async MCP Mix render job.
type RenderJobStatus string

const (
	RenderJobRunning   RenderJobStatus = "running"
	RenderJobComplete  RenderJobStatus = "complete"
	RenderJobFailed    RenderJobStatus = "failed"
	RenderJobCancelled RenderJobStatus = "cancelled"
)

// RenderJobSnapshot is the live JSON status and telemetry payload exposed by get_render_status and ytmix://jobs/{id}.
type RenderJobSnapshot struct {
	JobID                 string              `json:"jobId"`
	Status                RenderJobStatus     `json:"status"`
	Step                  int                 `json:"step"`
	TotalSteps            int                 `json:"totalSteps"`
	Progress              int                 `json:"progress"`
	Message               string              `json:"message"`
	Detail                string              `json:"detail"`
	Substage              string              `json:"substage,omitempty"`
	OutTimeSec            float64             `json:"outTimeSec"`
	TotalDurationSec      float64             `json:"totalDurationSec"`
	Speed                 string              `json:"speed,omitempty"`
	RemainingSec          float64             `json:"remainingSec"`
	EstimatedTotalSec     float64             `json:"estimatedTotalSec"`
	Estimate              *render.MixEstimate `json:"estimate,omitempty"`
	RenderingPath         string              `json:"renderingPath"`
	Segmented             bool                `json:"segmented,omitempty"`
	OutPath               string              `json:"outPath"`
	TimestampsPath        string              `json:"timestampsPath"`
	TrackCount            int                 `json:"trackCount"`
	ResourceURI           string              `json:"resourceUri"`
	TimestampsResourceURI string              `json:"timestampsResourceUri"`
	CancelRequested       bool                `json:"cancelRequested,omitempty"`
	Error                 string              `json:"error,omitempty"`
	StartedAt             time.Time           `json:"startedAt"`
	FinishedAt            *time.Time          `json:"finishedAt,omitempty"`
}

// StartRenderResult is returned immediately by start_render after pre-flight validation and goroutine launch.
type StartRenderResult struct {
	JobID                 string             `json:"jobId"`
	Status                RenderJobStatus    `json:"status"`
	RenderingPath         string             `json:"renderingPath"`
	Segmented             bool               `json:"segmented,omitempty"`
	OutPath               string             `json:"outPath"`
	TimestampsPath        string             `json:"timestampsPath"`
	TrackCount            int                `json:"trackCount"`
	Estimate              render.MixEstimate `json:"estimate"`
	ResourceURI           string             `json:"resourceUri"`
	TimestampsResourceURI string             `json:"timestampsResourceUri"`
}

// CancelRenderResult extends RenderJobSnapshot with whether the job had already finished before cancellation.
type CancelRenderResult struct {
	RenderJobSnapshot
	AlreadyFinished bool `json:"alreadyFinished"`
}

// VerifiedVideoStream describes the video stream probed from a rendered Mix .mp4.
type VerifiedVideoStream struct {
	Index       int     `json:"index"`
	Codec       string  `json:"codec"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	AspectRatio string  `json:"aspectRatio,omitempty"`
	Is16x9      bool    `json:"is16x9"`
	PixFmt      string  `json:"pixFmt,omitempty"`
	FPS         float64 `json:"fps,omitempty"`
}

// VerifiedAudioStream describes the audio stream probed from a rendered Mix .mp4.
type VerifiedAudioStream struct {
	Index int    `json:"index"`
	Codec string `json:"codec"`
}

// VerifyMixResult is the structured output validation report returned by verify_mix.
type VerifyMixResult struct {
	Valid             bool                 `json:"valid"`
	OutPath           string               `json:"outPath"`
	TimestampsPath    string               `json:"timestampsPath,omitempty"`
	DurationSec       float64              `json:"durationSec"`
	Video             *VerifiedVideoStream `json:"video,omitempty"`
	Audio             *VerifiedAudioStream `json:"audio,omitempty"`
	AudioStreamCount  int                  `json:"audioStreamCount"`
	StrayAudioStreams int                  `json:"strayAudioStreams"`
	TimestampsValid   *bool                `json:"timestampsValid,omitempty"`
	ChapterCount      int                  `json:"chapterCount"`
	Warnings          []string             `json:"warnings"`
	Errors            []string             `json:"errors"`
}

type renderTrackInput struct {
	Path         string  `json:"path"`
	FileName     string  `json:"fileName"`
	DisplayTitle string  `json:"displayTitle"`
	DurationSec  float64 `json:"durationSec"`
}

type startRenderArgs struct {
	AudioDir             string               `json:"audioDir"`
	Tracks               []renderTrackInput   `json:"tracks"`
	BG                   string               `json:"bg"`
	OutDir               string               `json:"outDir"`
	Out                  string               `json:"out"`
	Timestamps           string               `json:"timestamps"`
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

type jobIDArgs struct {
	JobID string `json:"jobId"`
}

type verifyMixArgs struct {
	OutPath        string `json:"outPath"`
	TimestampsPath string `json:"timestampsPath"`
}

type renderJob struct {
	id                    string
	renderingPath         string
	segmented             bool
	outPath               string
	timestampsPath        string
	trackCount            int
	resourceURI           string
	timestampsResourceURI string
	startedAt             time.Time
	cancel                context.CancelFunc
	done                  chan struct{}

	mu                sync.Mutex
	status            RenderJobStatus
	step              int
	totalSteps        int
	progress          int
	message           string
	detail            string
	substage          string
	outTimeSec        float64
	totalDurationSec  float64
	speed             string
	remainingSec      float64
	estimatedTotalSec float64
	estimate          *render.MixEstimate
	cancelRequested   bool
	errMsg            string
	finishedAt        *time.Time
}

func (j *renderJob) snapshot() RenderJobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()

	var estCopy *render.MixEstimate
	if j.estimate != nil {
		cp := *j.estimate
		estCopy = &cp
	}
	var finCopy *time.Time
	if j.finishedAt != nil {
		t := *j.finishedAt
		finCopy = &t
	}
	return RenderJobSnapshot{
		JobID:                 j.id,
		Status:                j.status,
		Step:                  j.step,
		TotalSteps:            j.totalSteps,
		Progress:              j.progress,
		Message:               j.message,
		Detail:                j.detail,
		Substage:              j.substage,
		OutTimeSec:            j.outTimeSec,
		TotalDurationSec:      j.totalDurationSec,
		Speed:                 j.speed,
		RemainingSec:          j.remainingSec,
		EstimatedTotalSec:     j.estimatedTotalSec,
		Estimate:              estCopy,
		RenderingPath:         j.renderingPath,
		Segmented:             j.segmented,
		OutPath:               j.outPath,
		TimestampsPath:        j.timestampsPath,
		TrackCount:            j.trackCount,
		ResourceURI:           j.resourceURI,
		TimestampsResourceURI: j.timestampsResourceURI,
		CancelRequested:       j.cancelRequested,
		Error:                 j.errMsg,
		StartedAt:             j.startedAt,
		FinishedAt:            finCopy,
	}
}

func (j *renderJob) applyTelemetry(ev mixer.TelemetryEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status != RenderJobRunning {
		return
	}
	if ev.TotalSteps > 0 {
		j.totalSteps = ev.TotalSteps
	}
	switch ev.Kind {
	case mixer.EventStep:
		j.step = ev.Step
		j.progress = ev.Progress
		j.message = ev.Message
		j.detail = ev.Detail
		j.substage = ev.Substage
		if ev.RemainingSec >= 0 {
			j.remainingSec = ev.RemainingSec
		}
		if ev.EstimatedTotalSec > 0 {
			j.estimatedTotalSec = ev.EstimatedTotalSec
		}
		if ev.Estimate != nil {
			cp := *ev.Estimate
			j.estimate = &cp
			if cp.MixDurationSec > 0 {
				j.totalDurationSec = cp.MixDurationSec
			}
		}
	case mixer.EventProgress:
		j.step = ev.Step
		j.progress = ev.Progress
		if ev.Message != "" {
			j.message = ev.Message
		}
		if ev.Detail != "" {
			j.detail = ev.Detail
		}
		j.substage = ev.Substage
		j.outTimeSec = ev.OutTimeSec
		if ev.TotalDurationSec > 0 {
			j.totalDurationSec = ev.TotalDurationSec
		}
		j.speed = ev.Speed
		if ev.RemainingSec >= 0 {
			j.remainingSec = ev.RemainingSec
		}
		if ev.EstimatedTotalSec > 0 {
			j.estimatedTotalSec = ev.EstimatedTotalSec
		}
	case mixer.EventDone:
		j.step = ev.Step
		j.progress = ev.Progress
		j.message = ev.Message
		j.detail = ev.Message
		j.substage = ""
		if ev.TotalDurationSec > 0 {
			j.totalDurationSec = ev.TotalDurationSec
		}
		j.remainingSec = 0
		if ev.EstimatedTotalSec > 0 {
			j.estimatedTotalSec = ev.EstimatedTotalSec
		}
	}
}

func (j *renderJob) finish(err error) {
	now := time.Now()
	j.mu.Lock()
	if err == nil {
		j.status = RenderJobComplete
		j.step = 5
		j.totalSteps = 5
		j.progress = 100
		j.remainingSec = 0
		j.substage = ""
		if strings.TrimSpace(j.message) == "" || !strings.HasPrefix(j.message, "Done!") {
			j.message = fmt.Sprintf("Done! Rendered %s via %s.", j.outPath, j.renderingPath)
		}
		j.detail = j.message
	} else if j.cancelRequested || errors.Is(err, context.Canceled) {
		j.status = RenderJobCancelled
		j.errMsg = err.Error()
		j.message = "Mix render cancelled."
		j.detail = j.message
		j.substage = ""
	} else {
		j.status = RenderJobFailed
		j.errMsg = err.Error()
		j.message = fmt.Sprintf("Mix render failed: %v", err)
		j.detail = j.message
		j.substage = ""
	}
	j.finishedAt = &now
	j.mu.Unlock()
	close(j.done)
}

// RenderJobManager coordinates bounded in-memory async Mix render jobs and their dynamic MCP resources.
type RenderJobManager struct {
	registry *Registry
	deps     RenderDeps
	maxJobs  int

	baseCtx    context.Context
	baseCancel context.CancelFunc
	wg         sync.WaitGroup

	mu          sync.Mutex
	closed      bool
	jobs        map[string]*renderJob
	jobRing     []string
	activeJobID string
}

// RegisterRenderTools registers start_render, get_render_status, cancel_render, and verify_mix on r
// and returns a RenderJobManager whose Close method cancels and waits for active background renders.
func RegisterRenderTools(r *Registry, deps RenderDeps) *RenderJobManager {
	maxJobs := deps.MaxJobs
	if maxJobs <= 0 {
		maxJobs = defaultMaxRenderJobs
	}
	if deps.RunFn == nil {
		deps.RunFn = mixer.Run
	}
	if deps.RunWithTracksFn == nil {
		deps.RunWithTracksFn = mixer.RunWithTracks
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())

	mgr := &RenderJobManager{
		registry:   r,
		deps:       deps,
		maxJobs:    maxJobs,
		baseCtx:    baseCtx,
		baseCancel: baseCancel,
		jobs:       make(map[string]*renderJob),
	}

	if r == nil {
		return mgr
	}

	r.RegisterTool(startRenderToolDefinition(), func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return mgr.handleStartRender(ctx, args)
	})
	r.RegisterTool(getRenderStatusToolDefinition(), func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return mgr.handleGetRenderStatus(ctx, args)
	})
	r.RegisterTool(cancelRenderToolDefinition(), func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return mgr.handleCancelRender(ctx, args)
	})
	r.RegisterTool(verifyMixToolDefinition(), func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		return mgr.handleVerifyMix(ctx, args)
	})

	return mgr
}

// Close cancels any running background Mix render and waits for active render goroutines to exit cleanly.
func (m *RenderJobManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	alreadyClosed := m.closed
	m.closed = true
	m.mu.Unlock()
	if !alreadyClosed && m.baseCancel != nil {
		m.baseCancel()
	}
	m.wg.Wait()
}

func startRenderToolDefinition() Tool {
	props := map[string]any{
		"audioDir": map[string]any{
			"type":        "string",
			"description": "Directory containing input .mp3 Tracks (mutually exclusive with tracks).",
		},
		"tracks": map[string]any{
			"type":        "array",
			"description": "Explicit ordered list of .mp3 Tracks (mutually exclusive with audioDir). Preserves Track order and Display Title overrides without modifying files on disk.",
			"minItems":    1,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Local path to the .mp3 Track file.",
					},
					"fileName": map[string]any{
						"type":        "string",
						"description": "Optional filename used for fallback Display Title derivation.",
					},
					"displayTitle": map[string]any{
						"type":        "string",
						"description": "Optional Display Title override for Chapter Timestamps and burned .ass Title Overlays.",
					},
					"durationSec": map[string]any{
						"type":             "number",
						"description":      "Optional Track duration in seconds (> 0). Probed via ffprobe when omitted.",
						"exclusiveMinimum": 0,
					},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		"bg": map[string]any{
			"type":        "string",
			"description": "Optional Background Visual path (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif). Omit to use Path D Auto Visualizer.",
		},
		"outDir": map[string]any{
			"type":        "string",
			"description": "Optional directory where relative out and timestamps paths are written.",
		},
		"out": map[string]any{
			"type":        "string",
			"description": "Output .mp4 video path (default output.mp4).",
			"default":     "output.mp4",
		},
		"timestamps": map[string]any{
			"type":        "string",
			"description": "Output Chapter Timestamps file path (default timestamps.txt).",
			"default":     "timestamps.txt",
		},
		"crossfade": map[string]any{
			"type":        "number",
			"description": "Crossfade duration in seconds between Tracks (default 0).",
			"default":     0,
			"minimum":     0,
		},
		"normalize": map[string]any{
			"type":        "boolean",
			"description": "Normalize each Track to -14 LUFS before merging.",
			"default":     false,
		},
		"overlayTitles": map[string]any{
			"type":        "boolean",
			"description": "Burn Display Titles into the bottom-left corner via .ass Title Overlays.",
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
		Name:        "start_render",
		Description: "Pre-flight a Mix configuration (including optional per-Track media, Dual-Layer overrides, or manifest) and start an asynchronous 5-step Mix render in the background, returning a jobId, Rendering Path, MixEstimate, and live MCP resource URIs immediately without blocking on FFmpeg.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           props,
			"additionalProperties": false,
		},
		Annotations: &ToolAnnotations{
			Title:           "Start Async Mix Render",
			ReadOnlyHint:    new(false),
			DestructiveHint: new(false),
			IdempotentHint:  new(false),
			OpenWorldHint:   new(false),
		},
	}
}

func getRenderStatusToolDefinition() Tool {
	return Tool{
		Name:        "get_render_status",
		Description: "Poll live progress, pipeline step (1-5), FFmpeg speed, ETA, error details, and output paths for an async Mix render job.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"jobId": map[string]any{
					"type":        "string",
					"description": "Job ID returned by start_render.",
				},
			},
			"required":             []string{"jobId"},
			"additionalProperties": false,
		},
		Annotations: readOnlyLocalAnnotations("Get Mix Render Status"),
	}
}

func cancelRenderToolDefinition() Tool {
	return Tool{
		Name:        "cancel_render",
		Description: "Cancel a running async Mix render job via its context.Context and clean up its temporary workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"jobId": map[string]any{
					"type":        "string",
					"description": "Job ID returned by start_render.",
				},
			},
			"required":             []string{"jobId"},
			"additionalProperties": false,
		},
		Annotations: &ToolAnnotations{
			Title:           "Cancel Async Mix Render",
			ReadOnlyHint:    new(false),
			DestructiveHint: new(false),
			IdempotentHint:  new(true),
			OpenWorldHint:   new(false),
		},
	}
}

func verifyMixToolDefinition() Tool {
	return Tool{
		Name:        "verify_mix",
		Description: "Validate a rendered Mix .mp4 via ffprobe (1 H.264 video stream, 1 AAC audio stream, no stray audio, positive duration) and optionally verify Chapter Timestamps alignment.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"outPath": map[string]any{
					"type":        "string",
					"description": "Path to the rendered output .mp4 file.",
				},
				"timestampsPath": map[string]any{
					"type":        "string",
					"description": "Optional path to the generated Chapter Timestamps file (timestamps.txt).",
				},
			},
			"required":             []string{"outPath"},
			"additionalProperties": false,
		},
		Annotations: readOnlyLocalAnnotations("Verify Rendered Mix"),
	}
}

func newOpaqueJobID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate job id: %w", err)
	}
	return "job_" + hex.EncodeToString(raw[:]), nil
}

func jobResourceURI(jobID string) string {
	return "ytmix://jobs/" + jobID
}

func jobTimestampsResourceURI(jobID string) string {
	return "ytmix://jobs/" + jobID + "/timestamps"
}

func safeLookPathFunc(lookPath func(string) (string, error)) func(string) (string, error) {
	if lookPath != nil {
		return lookPath
	}
	return func(bin string) (string, error) {
		return "", fmt.Errorf("required binary %q lookup is unavailable", bin)
	}
}

func (m *RenderJobManager) checkCanAcceptJob() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("render job manager is shutting down")
	}
	if m.activeJobID != "" {
		if active, ok := m.jobs[m.activeJobID]; ok {
			active.mu.Lock()
			running := active.status == RenderJobRunning
			active.mu.Unlock()
			if running {
				return errors.New("a Mix render is already in progress")
			}
		}
	}
	return nil
}

func (m *RenderJobManager) claimJob(job *renderJob) (evictedIDs []string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("render job manager is shutting down")
	}
	if m.activeJobID != "" {
		if active, ok := m.jobs[m.activeJobID]; ok {
			active.mu.Lock()
			running := active.status == RenderJobRunning
			active.mu.Unlock()
			if running {
				return nil, errors.New("a Mix render is already in progress")
			}
		}
	}

	m.activeJobID = job.id
	m.jobs[job.id] = job
	m.jobRing = append(m.jobRing, job.id)
	m.wg.Add(1)

	for len(m.jobRing) > m.maxJobs {
		oldID := m.jobRing[0]
		if oldID == m.activeJobID {
			break
		}
		m.jobRing = m.jobRing[1:]
		if _, ok := m.jobs[oldID]; ok {
			delete(m.jobs, oldID)
			evictedIDs = append(evictedIDs, oldID)
		}
	}
	return evictedIDs, nil
}

func (m *RenderJobManager) releaseActive(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeJobID == jobID {
		m.activeJobID = ""
	}
}

func (m *RenderJobManager) getJob(jobID string) (*renderJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	return j, ok
}

func (m *RenderJobManager) registerJobResources(job *renderJob) {
	if m.registry == nil || job == nil {
		return
	}
	jobID := job.id
	m.registry.RegisterResource(Resource{
		URI:         job.resourceURI,
		Name:        "Render Job " + jobID,
		Description: "Live JSON status and telemetry snapshot for Mix render job " + jobID + ".",
		MimeType:    "application/json",
	}, func(_ context.Context, uri string) ([]ResourceContent, error) {
		j, ok := m.getJob(jobID)
		if !ok {
			return nil, ResourceNotFoundError(uri)
		}
		raw, err := json.MarshalIndent(j.snapshot(), "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal job status: %w", err)
		}
		return []ResourceContent{{
			URI:      uri,
			MimeType: "application/json",
			Text:     string(raw),
		}}, nil
	})

	m.registry.RegisterResource(Resource{
		URI:         job.timestampsResourceURI,
		Name:        "Render Job " + jobID + " Chapter Timestamps",
		Description: "Generated YouTube Chapter Timestamps file (timestamps.txt) once job " + jobID + " completes.",
		MimeType:    "text/plain",
	}, func(_ context.Context, uri string) ([]ResourceContent, error) {
		j, ok := m.getJob(jobID)
		if !ok {
			return nil, ResourceNotFoundError(uri)
		}
		snap := j.snapshot()
		if snap.Status != RenderJobComplete {
			return nil, &RPCError{
				Code:    ErrCodeResourceNotFound,
				Message: fmt.Sprintf("timestamps for job %s are not available yet (status: %s)", jobID, snap.Status),
			}
		}
		data, err := readBoundedFile(snap.TimestampsPath, maxTimestampsBytes)
		if err != nil {
			return nil, &RPCError{
				Code:    ErrCodeResourceNotFound,
				Message: fmt.Sprintf("cannot read timestamps for job %s (%s): %v", jobID, snap.TimestampsPath, err),
			}
		}
		return []ResourceContent{{
			URI:      uri,
			MimeType: "text/plain",
			Text:     string(data),
		}}, nil
	})
}

func (m *RenderJobManager) unregisterJobResources(jobIDs []string) {
	if m.registry == nil {
		return
	}
	for _, id := range jobIDs {
		m.registry.UnregisterResource(jobResourceURI(id))
		m.registry.UnregisterResource(jobTimestampsResourceURI(id))
	}
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)
	// #nosec G304 G703 -- path originates from validated mixer output config or verify_mix input; confined to parent directory via os.OpenRoot.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file %q exceeds %d bytes", path, maxBytes)
	}
	return data, nil
}

func resolveRenderTracks(ctx context.Context, inputs []renderTrackInput, deps RenderDeps, hasFFprobe bool) ([]probe.Track, error) {
	tracks := make([]probe.Track, len(inputs))
	type pendingProbe struct {
		index           int
		hasCustomTitle  bool
		customTitle     string
		namedTrackInput probe.NamedTrackFile
	}
	var pending []pendingProbe

	for i, item := range inputs {
		if math.IsNaN(item.DurationSec) || math.IsInf(item.DurationSec, 0) || item.DurationSec < 0 {
			return nil, InvalidParamsError(fmt.Sprintf("tracks[%d].durationSec must be a finite number >= 0", i))
		}
		trackPath := strings.TrimSpace(item.Path)
		if trackPath == "" {
			return nil, InvalidParamsError(fmt.Sprintf("tracks[%d].path is required", i))
		}
		if err := probe.ValidateTrackFile(trackPath); err != nil {
			return nil, fmt.Errorf("tracks[%d]: %w", i, err)
		}

		fileName := strings.TrimSpace(item.FileName)
		if fileName == "" {
			fileName = filepath.Base(trackPath)
		}
		displayTitle := strings.TrimSpace(item.DisplayTitle)

		if item.DurationSec == 0 {
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

		if displayTitle == "" {
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

func (m *RenderJobManager) handleStartRender(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[startRenderArgs](args)
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
	outPathArg := strings.TrimSpace(in.Out)
	if outPathArg == "" {
		outPathArg = "output.mp4"
	}
	tsPathArg := strings.TrimSpace(in.Timestamps)
	if tsPathArg == "" {
		tsPathArg = "timestamps.txt"
	}

	cfg := mixer.Config{
		AudioDir:             audioDir,
		BG:                   bg,
		OutDir:               strings.TrimSpace(in.OutDir),
		Out:                  outPathArg,
		Timestamps:           tsPathArg,
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

	if bg == "" {
		if _, err := render.ValidateVisualizerConfig(cfg.VisualizerConfig()); err != nil {
			return CallToolResult{}, InvalidParamsError(err.Error())
		}
	}

	lookPath := safeLookPathFunc(m.deps.LookPath)
	preparedCfg, pathKind, err := mixer.PreflightConfig(cfg, lookPath, false)
	if err != nil {
		return mapToolError(err)
	}

	if err := m.checkCanAcceptJob(); err != nil {
		return ErrorToolResult(err.Error()), nil
	}

	if pathKind == render.PathD {
		if err := mixer.ValidateVisualizerFilters(ctx, preparedCfg, m.deps.Runner, nil); err != nil {
			return mapToolError(err)
		}
	}

	hasFFprobe := lookPathAvailable(lookPath, "ffprobe")
	var tracks []probe.Track
	useExplicitTracks := !hasAudioDir
	if hasAudioDir {
		info, statErr := os.Stat(audioDir)
		if statErr != nil {
			return ErrorToolResult(fmt.Sprintf("cannot access audioDir %q: %v", audioDir, statErr)), nil
		}
		if !info.IsDir() {
			return ErrorToolResult(fmt.Sprintf("audioDir %q is not a directory", audioDir)), nil
		}
		tracks, err = probe.ProbeTracks(ctx, audioDir, preparedCfg.Shuffle, nil, m.deps.Runner)
		if err != nil {
			return mapToolError(err)
		}
	} else {
		tracks, err = resolveRenderTracks(ctx, in.Tracks, m.deps, hasFFprobe)
		if err != nil {
			return mapToolError(err)
		}
	}

	est, err := mixer.EstimateConfig(ctx, preparedCfg, tracks, nil, lookPath, m.deps.Runner)
	if err != nil {
		return mapToolError(err)
	}

	preparedCfg, err = mixer.PrepareOutputTargets(preparedCfg)
	if err != nil {
		return mapToolError(err)
	}

	jobID, err := newOpaqueJobID()
	if err != nil {
		return ErrorToolResult(err.Error()), nil
	}

	estCopy := est
	jobCtx, jobCancel := context.WithCancel(m.baseCtx)
	job := &renderJob{
		id:                    jobID,
		renderingPath:         string(pathKind),
		segmented:             preparedCfg.Segmented,
		outPath:               preparedCfg.Out,
		timestampsPath:        preparedCfg.Timestamps,
		trackCount:            len(tracks),
		resourceURI:           jobResourceURI(jobID),
		timestampsResourceURI: jobTimestampsResourceURI(jobID),
		startedAt:             time.Now(),
		cancel:                jobCancel,
		done:                  make(chan struct{}),
		status:                RenderJobRunning,
		totalSteps:            5,
		message:               "Queued Mix render...",
		detail:                "Queued Mix render...",
		totalDurationSec:      est.MixDurationSec,
		remainingSec:          est.PointSec,
		estimatedTotalSec:     est.PointSec,
		estimate:              &estCopy,
	}

	evictedIDs, err := m.claimJob(job)
	if err != nil {
		jobCancel()
		return ErrorToolResult(err.Error()), nil
	}

	m.registerJobResources(job)
	m.unregisterJobResources(evictedIDs)

	go func() {
		defer m.wg.Done()
		m.runJob(jobCtx, job, preparedCfg, tracks, useExplicitTracks)
	}()

	return JSONToolResult(StartRenderResult{
		JobID:                 job.id,
		Status:                RenderJobRunning,
		RenderingPath:         job.renderingPath,
		Segmented:             job.segmented,
		OutPath:               job.outPath,
		TimestampsPath:        job.timestampsPath,
		TrackCount:            job.trackCount,
		Estimate:              est,
		ResourceURI:           job.resourceURI,
		TimestampsResourceURI: job.timestampsResourceURI,
	})
}

func (m *RenderJobManager) runJob(ctx context.Context, job *renderJob, cfg mixer.Config, tracks []probe.Track, useExplicitTracks bool) {
	defer m.releaseActive(job.id)
	defer job.cancel()

	sink := func(ev mixer.TelemetryEvent) {
		job.applyTelemetry(ev)
	}
	lookPath := safeLookPathFunc(m.deps.LookPath)

	var err error
	if useExplicitTracks {
		err = m.deps.RunWithTracksFn(ctx, cfg, tracks, sink, lookPath, m.deps.Runner, m.deps.StreamRunner)
	} else {
		err = m.deps.RunFn(ctx, cfg, sink, lookPath, m.deps.Runner, m.deps.StreamRunner)
	}
	job.finish(err)
}

func (m *RenderJobManager) handleGetRenderStatus(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[jobIDArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}
	jobID := strings.TrimSpace(in.JobID)
	if jobID == "" {
		return CallToolResult{}, InvalidParamsError("jobId is required and must be a non-empty string")
	}

	job, ok := m.getJob(jobID)
	if !ok {
		return ErrorToolResult(fmt.Sprintf("unknown render job %q", jobID)), nil
	}
	return JSONToolResult(job.snapshot())
}

func (m *RenderJobManager) handleCancelRender(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[jobIDArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}
	jobID := strings.TrimSpace(in.JobID)
	if jobID == "" {
		return CallToolResult{}, InvalidParamsError("jobId is required and must be a non-empty string")
	}

	job, ok := m.getJob(jobID)
	if !ok {
		return ErrorToolResult(fmt.Sprintf("unknown render job %q", jobID)), nil
	}

	job.mu.Lock()
	alreadyFinished := job.status != RenderJobRunning
	if !alreadyFinished {
		job.cancelRequested = true
	}
	cancelFn := job.cancel
	job.mu.Unlock()

	if alreadyFinished {
		return JSONToolResult(CancelRenderResult{
			RenderJobSnapshot: job.snapshot(),
			AlreadyFinished:   true,
		})
	}

	if cancelFn != nil {
		cancelFn()
	}

	timer := time.NewTimer(cancelWaitGrace)
	defer timer.Stop()
	select {
	case <-job.done:
	case <-timer.C:
	case <-ctx.Done():
		return CallToolResult{}, ctx.Err()
	}

	return JSONToolResult(CancelRenderResult{
		RenderJobSnapshot: job.snapshot(),
		AlreadyFinished:   false,
	})
}

type ffprobeVerifyOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		Index        int    `json:"index"`
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		PixFmt       string `json:"pix_fmt"`
		RFrameRate   string `json:"r_frame_rate"`
		AvgFrameRate string `json:"avg_frame_rate"`
	} `json:"streams"`
}

func (m *RenderJobManager) handleVerifyMix(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if ctx != nil && ctx.Err() != nil {
		return CallToolResult{}, ctx.Err()
	}

	in, err := DecodeToolArgs[verifyMixArgs](args)
	if err != nil {
		return CallToolResult{}, err
	}

	outPath := strings.TrimSpace(in.OutPath)
	if outPath == "" {
		return CallToolResult{}, InvalidParamsError("outPath is required and must be a non-empty string")
	}
	timestampsPath := strings.TrimSpace(in.TimestampsPath)

	// #nosec G703 -- outPath is explicitly supplied by the MCP caller to verify an output file on disk.
	info, err := os.Stat(outPath)
	if err != nil {
		return ErrorToolResult(fmt.Sprintf("cannot access output video %q: %v", outPath, err)), nil
	}
	if info.IsDir() {
		return ErrorToolResult(fmt.Sprintf("outPath %q is a directory, expected an .mp4 file", outPath)), nil
	}
	if !info.Mode().IsRegular() {
		return ErrorToolResult(fmt.Sprintf("outPath %q is not a regular file", outPath)), nil
	}

	if !lookPathAvailable(m.deps.LookPath, "ffprobe") {
		return ErrorToolResult(`required binary "ffprobe" not found on PATH`), nil
	}
	if m.deps.Runner == nil {
		return ErrorToolResult("ffprobe runner is required for verify_mix"), nil
	}

	raw, err := m.deps.Runner(
		ctx,
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-show_entries", "stream=index,codec_type,codec_name,width,height,pix_fmt,r_frame_rate,avg_frame_rate",
		"-of", "json",
		outPath,
	)
	if err != nil {
		return mapToolError(fmt.Errorf("ffprobe %q: %w", outPath, err))
	}

	var parsed ffprobeVerifyOutput
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ErrorToolResult(fmt.Sprintf("parse ffprobe JSON for %q: %v", outPath, err)), nil
	}

	warnings := []string{}
	errs := []string{}

	var durSec float64
	parsedDur, durErr := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if durErr != nil || math.IsNaN(parsedDur) || math.IsInf(parsedDur, 0) || parsedDur <= 0 {
		errs = append(errs, fmt.Sprintf("container duration must be positive (got %q)", parsed.Format.Duration))
	} else {
		durSec = parsedDur
	}

	var videoStreams, audioStreams []int
	for i, st := range parsed.Streams {
		switch strings.ToLower(strings.TrimSpace(st.CodecType)) {
		case "video":
			videoStreams = append(videoStreams, i)
		case "audio":
			audioStreams = append(audioStreams, i)
		}
	}

	var videoInfo *VerifiedVideoStream
	if len(videoStreams) == 0 {
		errs = append(errs, "missing video stream")
	} else {
		if len(videoStreams) > 1 {
			errs = append(errs, fmt.Sprintf("expected exactly 1 video stream, found %d", len(videoStreams)))
		}
		vs := parsed.Streams[videoStreams[0]]
		codec := strings.TrimSpace(vs.CodecName)
		if !strings.EqualFold(codec, "h264") {
			errs = append(errs, fmt.Sprintf("expected h264 video stream, got %q", codec))
		}
		fps := probe.ParseFrameRate(vs.AvgFrameRate)
		if fps == 0 {
			fps = probe.ParseFrameRate(vs.RFrameRate)
		}
		var aspectRatio string
		var is16x9 bool
		if vs.Width <= 0 || vs.Height <= 0 {
			errs = append(errs, fmt.Sprintf("video stream has invalid dimensions %dx%d", vs.Width, vs.Height))
		} else {
			aspectRatio = formatAspectRatio(vs.Width, vs.Height)
			ratio := float64(vs.Width) / float64(vs.Height)
			is16x9 = math.Abs(ratio-targetAspect16x9) <= aspect16x9Tolerance
			if !is16x9 {
				warnings = append(warnings, fmt.Sprintf("video aspect ratio %s (%dx%d) is not 16:9", aspectRatio, vs.Width, vs.Height))
			}
			if vs.Width < minFullHDWidth || vs.Height < minFullHDHeight {
				warnings = append(warnings, fmt.Sprintf("video resolution %dx%d is below 1080p (1920x1080)", vs.Width, vs.Height))
			}
		}
		videoInfo = &VerifiedVideoStream{
			Index:       vs.Index,
			Codec:       codec,
			Width:       vs.Width,
			Height:      vs.Height,
			AspectRatio: aspectRatio,
			Is16x9:      is16x9,
			PixFmt:      strings.TrimSpace(vs.PixFmt),
			FPS:         fps,
		}
	}

	audioStreamCount := len(audioStreams)
	strayAudioStreams := 0
	if audioStreamCount > 1 {
		strayAudioStreams = audioStreamCount - 1
	}

	var audioInfo *VerifiedAudioStream
	if audioStreamCount == 0 {
		errs = append(errs, "missing audio stream")
	} else {
		if audioStreamCount > 1 {
			errs = append(errs, fmt.Sprintf("expected exactly 1 audio stream, found %d (%d stray audio stream(s))", audioStreamCount, strayAudioStreams))
		}
		as := parsed.Streams[audioStreams[0]]
		codec := strings.TrimSpace(as.CodecName)
		if !strings.EqualFold(codec, "aac") {
			errs = append(errs, fmt.Sprintf("expected aac audio stream, got %q", codec))
		}
		audioInfo = &VerifiedAudioStream{
			Index: as.Index,
			Codec: codec,
		}
	}

	var timestampsValidPtr *bool
	chapterCount := 0
	if timestampsPath != "" {
		tsValid, count, tsErrs := verifyTimestampsFile(timestampsPath, durSec)
		timestampsValidPtr = new(tsValid)
		chapterCount = count
		errs = append(errs, tsErrs...)
	}

	result := VerifyMixResult{
		Valid:             len(errs) == 0,
		OutPath:           outPath,
		TimestampsPath:    timestampsPath,
		DurationSec:       durSec,
		Video:             videoInfo,
		Audio:             audioInfo,
		AudioStreamCount:  audioStreamCount,
		StrayAudioStreams: strayAudioStreams,
		TimestampsValid:   timestampsValidPtr,
		ChapterCount:      chapterCount,
		Warnings:          warnings,
		Errors:            errs,
	}
	return JSONToolResult(result)
}

func verifyTimestampsFile(path string, videoDurationSec float64) (bool, int, []string) {
	// #nosec G703 -- path is explicitly supplied by the MCP caller to verify timestamps.txt.
	info, err := os.Stat(path)
	if err != nil {
		return false, 0, []string{fmt.Sprintf("cannot access timestampsPath %q: %v", path, err)}
	}
	if info.IsDir() {
		return false, 0, []string{fmt.Sprintf("timestampsPath %q is a directory, expected a file", path)}
	}

	data, err := readBoundedFile(path, maxTimestampsBytes)
	if err != nil {
		return false, 0, []string{fmt.Sprintf("cannot read timestampsPath %q: %v", path, err)}
	}

	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	rawLines := strings.Split(normalized, "\n")

	var lines []string
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) == 0 {
		return false, 0, []string{fmt.Sprintf("timestampsPath %q is empty", path)}
	}

	var errs []string
	var prevSec float64
	var lastSec float64
	var lastTS string
	for i, line := range lines {
		tsStr, title, hasSpace := strings.Cut(line, " ")
		if !hasSpace || strings.TrimSpace(title) == "" {
			errs = append(errs, fmt.Sprintf("timestamps line %d (%q) must be formatted as '<timestamp> <title>'", i+1, line))
			continue
		}
		sec, ok := parseChapterTimestamp(tsStr)
		if !ok {
			errs = append(errs, fmt.Sprintf("timestamps line %d has invalid timestamp %q", i+1, tsStr))
			continue
		}
		if i == 0 && (sec != 0 || (tsStr != "00:00" && tsStr != "00:00:00")) {
			errs = append(errs, fmt.Sprintf("first chapter timestamp must start at 00:00 or 00:00:00 (got %q)", tsStr))
		}
		if i > 0 && sec < prevSec {
			errs = append(errs, fmt.Sprintf("timestamps line %d (%s) is earlier than previous chapter", i+1, tsStr))
		}
		prevSec = sec
		lastSec = sec
		lastTS = tsStr
	}

	if videoDurationSec > 0 && lastTS != "" && lastSec >= videoDurationSec+1.0 {
		errs = append(errs, fmt.Sprintf("last chapter start (%s, %.0fs) is inconsistent with video duration (%.2fs)", lastTS, lastSec, videoDurationSec))
	}

	return len(errs) == 0, len(lines), errs
}

func parseChapterTimestamp(raw string) (float64, bool) {
	parts := strings.Split(raw, ":")
	switch len(parts) {
	case 2:
		if len(parts[0]) < 2 || len(parts[1]) != 2 {
			return 0, false
		}
		mins, err1 := parseDigits(parts[0])
		secs, err2 := parseDigits(parts[1])
		if err1 != nil || err2 != nil || secs >= 60 {
			return 0, false
		}
		return float64(mins*60 + secs), true
	case 3:
		if len(parts[0]) < 2 || len(parts[1]) != 2 || len(parts[2]) != 2 {
			return 0, false
		}
		hours, err1 := parseDigits(parts[0])
		mins, err2 := parseDigits(parts[1])
		secs, err3 := parseDigits(parts[2])
		if err1 != nil || err2 != nil || err3 != nil || mins >= 60 || secs >= 60 {
			return 0, false
		}
		return float64(hours*3600 + mins*60 + secs), true
	default:
		return 0, false
	}
}

func parseDigits(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("non-digit")
		}
	}
	return strconv.Atoi(s)
}
