package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/render"
	"github.com/GCherry/ytmix/internal/subtitle"
)

type renderConfigRequest struct {
	SessionID            string             `json:"sessionId"`
	TrackIDs             []string           `json:"trackIds"`
	AudioDir             string             `json:"audioDir"`
	BGMode               string             `json:"bgMode"`
	Layer0Mode           string             `json:"layer0Mode"`
	BGPath               string             `json:"bgPath"`
	OutDir               string             `json:"outDir"`
	Out                  string             `json:"out"`
	Timestamps           string             `json:"timestamps"`
	Crossfade            float64            `json:"crossfade"`
	Normalize            bool               `json:"normalize"`
	OverlayTitles        bool               `json:"overlayTitles"`
	TitleDuration        int                `json:"titleDuration"`
	FontName             string             `json:"fontName"`
	Shuffle              bool               `json:"shuffle"`
	Visualizer           string             `json:"visualizer"`
	VisualizerTheme      string             `json:"visualizerTheme"`
	VisualizerResolution string             `json:"visualizerResolution"`
	VisualizerGlow       *bool              `json:"visualizerGlow"`
	VisualizerBrightness *int               `json:"visualizerBrightness"`
	FadeToBlackMinutes   float64            `json:"fadeToBlackMinutes"`
	DimTitles            bool               `json:"dimTitles"`
	Segmented            bool               `json:"segmented"`
	TrackTransition      string             `json:"trackTransition"`
	TrackMediaLayout     string             `json:"trackMediaLayout"`
	TrackOverrides       []trackOverrideDTO `json:"trackOverrides"`
}

type renderStartResponse struct {
	JobID         string `json:"jobId"`
	RenderingPath string `json:"renderingPath"`
	EventsURL     string `json:"eventsUrl"`
	Segmented     bool   `json:"segmented,omitempty"`
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	jobDir, err := os.MkdirTemp("", "ytmix-ui-job-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create job workspace: %v", err))
		return
	}
	keepJobDir := false
	defer func() {
		if !keepJobDir {
			os.RemoveAll(jobDir)
		}
	}()

	req, uploadedBGPath, statusCode, err := s.parseRenderRequest(w, r, jobDir)
	if err != nil {
		writeError(w, statusCode, err.Error())
		return
	}

	localAllowed := s.localPathsAllowed(r)
	bgMode, err := resolveEffectiveBGMode(req.BGMode, req.Layer0Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	isBlackMode := bgMode == "black"
	isVisualizerMode := bgMode == "visualizer" || (bgMode == "" && uploadedBGPath == "" && strings.TrimSpace(req.BGPath) == "" && strings.TrimSpace(req.Visualizer) != "")

	bgPath := uploadedBGPath
	effectiveVisualizer := req.Visualizer
	switch {
	case isBlackMode:
		if bgPath != "" || strings.TrimSpace(req.BGPath) != "" {
			writeError(w, http.StatusBadRequest, "--bg cannot be used with black backdrop mode")
			return
		}
		if strings.TrimSpace(effectiveVisualizer) != "" && !strings.EqualFold(strings.TrimSpace(effectiveVisualizer), "black") {
			writeError(w, http.StatusBadRequest, "black backdrop mode conflicts with non-black visualizer preset")
			return
		}
		bgPath = ""
		effectiveVisualizer = "black"
	case isVisualizerMode:
		if bgPath != "" || strings.TrimSpace(req.BGPath) != "" {
			writeError(w, http.StatusBadRequest, "--bg and --visualizer cannot be used together")
			return
		}
		bgPath = ""
	default:
		if strings.TrimSpace(req.Visualizer) != "" {
			writeError(w, http.StatusBadRequest, "--bg and --visualizer cannot be used together")
			return
		}
		if bgPath == "" {
			if bgMode == "upload" {
				writeError(w, http.StatusBadRequest, "upload a Background Visual file or switch to Auto Visualizer")
				return
			}
			if !localAllowed {
				writeError(w, http.StatusForbidden, "local Background Visual paths are disabled on non-loopback or --host 0.0.0.0 sessions; upload a Background Visual file or use Auto Visualizer")
				return
			}
			rawBG := strings.TrimSpace(req.BGPath)
			if rawBG == "" {
				writeError(w, http.StatusBadRequest, "Background Visual path (--bg) is required in Local Path mode")
				return
			}
			if _, ok := render.CanonicalBackgroundExt(filepath.Ext(rawBG)); !ok {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Background Visual extension %q", filepath.Ext(rawBG)))
				return
			}
			safeBG, err := s.resolveAllowedPath(rawBG, true)
			if err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			bgPath = safeBG
		}
	}

	maxFadeMinutes := float64(math.MaxInt64) / float64(time.Minute)
	if math.IsNaN(req.FadeToBlackMinutes) || math.IsInf(req.FadeToBlackMinutes, 0) || req.FadeToBlackMinutes < 0 || req.FadeToBlackMinutes > maxFadeMinutes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("fadeToBlackMinutes must be between 0 and %g (got %v)", maxFadeMinutes, req.FadeToBlackMinutes))
		return
	}
	vizGlow := true
	if req.VisualizerGlow != nil {
		vizGlow = *req.VisualizerGlow
	}
	vizBrightness := render.DefaultVisualizerBrightness
	if req.VisualizerBrightness != nil {
		vizBrightness = *req.VisualizerBrightness
		if vizBrightness < render.MinVisualizerBrightness || vizBrightness > render.MaxVisualizerBrightness {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("visualizerBrightness must be between %d and %d (got %d)", render.MinVisualizerBrightness, render.MaxVisualizerBrightness, vizBrightness))
			return
		}
	}
	fadeToBlack := time.Duration(math.Round(req.FadeToBlackMinutes * float64(time.Minute)))

	var safeAudioDir string
	if localAllowed && strings.TrimSpace(req.AudioDir) != "" {
		safeAudioDir, err = s.resolveAllowedPath(req.AudioDir, true)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
	}

	var (
		tracks    []mixer.Track
		sessMedia []sessionTrackMedia
		tempDir   string
	)
	if strings.TrimSpace(req.SessionID) != "" {
		tracks, sessMedia, _, tempDir, err = s.store.resolveTracksWithMedia(req.SessionID, req.TrackIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if localAllowed && safeAudioDir != "" {
		if req.Segmented || len(req.TrackOverrides) > 0 {
			tracks, err = mixer.ProbeTracksWithArt(r.Context(), safeAudioDir, req.Shuffle, nil, s.cfg.Runner)
		} else {
			tracks, err = mixer.ProbeTracks(r.Context(), safeAudioDir, req.Shuffle, nil, s.cfg.Runner)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "sessionId and trackIds are required (probe Tracks first)")
		return
	}

	if strings.TrimSpace(req.TrackTransition) != "" {
		if _, err := render.ValidateTrackTransition(render.TrackTransition(req.TrackTransition)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if _, err := mixer.ValidateTrackMediaLayout(req.TrackMediaLayout); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	isSegmented := req.Segmented || len(req.TrackOverrides) > 0 || hasSessionMediaOverrides(sessMedia)
	var (
		trackMediaMode string
		trackOverrides []mixer.TrackMediaOverride
	)
	if isSegmented {
		if strings.TrimSpace(req.SessionID) == "" && len(req.TrackOverrides) > 0 {
			writeError(w, http.StatusBadRequest, "sessionId is required when trackOverrides are specified")
			return
		}
		trackMediaMode = string(mixer.TrackMediaAuto)
		if strings.TrimSpace(req.SessionID) != "" {
			var ovStatus int
			trackOverrides, ovStatus, err = s.resolveRenderTrackOverrides(
				r.Context(),
				req.SessionID,
				req.TrackIDs,
				tracks,
				sessMedia,
				tempDir,
				req.TrackOverrides,
				localAllowed,
			)
			if err != nil {
				writeError(w, ovStatus, err.Error())
				return
			}
		}
	}

	outName := sanitizeBaseName(req.Out, s.cfg.Out)
	tsName := sanitizeBaseName(req.Timestamps, s.cfg.Timestamps)
	outPath := filepath.Join(jobDir, "mix.mp4")
	tsPath := filepath.Join(jobDir, "timestamps.txt")
	if localAllowed {
		var safeOutDir string
		if trimmedOutDir := strings.TrimSpace(req.OutDir); trimmedOutDir != "" {
			resolvedDir, err := s.resolveAllowedPath(trimmedOutDir, false)
			if err != nil {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			safeOutDir = resolvedDir
		}

		safeOut, status, err := s.resolveRenderOutputTarget(safeOutDir, req.Out, s.cfg.Out)
		if err != nil {
			writeError(w, status, err.Error())
			return
		}
		safeTS, status, err := s.resolveRenderOutputTarget(safeOutDir, req.Timestamps, s.cfg.Timestamps)
		if err != nil {
			writeError(w, status, err.Error())
			return
		}
		outPath = safeOut
		tsPath = safeTS
		outName = sanitizeBaseName(outPath, s.cfg.Out)
		tsName = sanitizeBaseName(tsPath, s.cfg.Timestamps)
	}

	fontName := strings.TrimSpace(req.FontName)
	if fontName == "" {
		fontName = "Arial"
	}

	mixCfg := mixer.Config{
		AudioDir:             safeAudioDir,
		BG:                   bgPath,
		Out:                  outPath,
		Timestamps:           tsPath,
		Crossfade:            req.Crossfade,
		Normalize:            req.Normalize,
		OverlayTitles:        req.OverlayTitles,
		TitleDuration:        req.TitleDuration,
		FontName:             fontName,
		Shuffle:              req.Shuffle,
		Visualizer:           effectiveVisualizer,
		VisualizerTheme:      req.VisualizerTheme,
		VisualizerResolution: req.VisualizerResolution,
		VisualizerGlow:       vizGlow,
		VisualizerBrightness: vizBrightness,
		FadeToBlack:          fadeToBlack,
		DimTitles:            req.DimTitles,
		TrackMedia:           trackMediaMode,
		TrackMediaLayout:     req.TrackMediaLayout,
		TrackTransition:      req.TrackTransition,
		TrackOverrides:       trackOverrides,
		Segmented:            isSegmented,
	}

	preparedCfg, pathKind, err := mixer.PreflightConfig(mixCfg, s.cfg.LookPath, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if pathKind == render.PathD {
		if err := mixer.ValidateVisualizerFilters(r.Context(), preparedCfg, s.cfg.Runner, s.detectFilters(r.Context())); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if localAllowed {
		preparedCfg, err = mixer.PrepareOutputTargets(preparedCfg)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	mixCfg = preparedCfg
	outPath = preparedCfg.Out
	tsPath = preparedCfg.Timestamps

	jobID, err := newOpaqueID("job")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jobCtx, jobCancel := context.WithCancel(s.baseCtx)
	job := newRenderJob(jobID, jobDir, outPath, outName, tsPath, tsName, string(pathKind), jobCancel)
	if err := s.store.claimRenderSlot(job, req.SessionID); err != nil {
		jobCancel()
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.store.preserveCollidingArtifacts(job.id, outPath, tsPath); err != nil {
		s.store.discardUnstartedJob(job.id)
		jobCancel()
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("preserve prior render artifacts: %v", err))
		return
	}
	keepJobDir = true

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runRenderJob(jobCtx, job, mixCfg, tracks)
	}()

	writeJSON(w, http.StatusAccepted, renderStartResponse{
		JobID:         jobID,
		RenderingPath: string(pathKind),
		EventsURL:     fmt.Sprintf("/api/render/%s/events", jobID),
		Segmented:     mixCfg.Segmented,
	})
}

func resolveEffectiveBGMode(bgModeRaw, layer0ModeRaw string) (string, error) {
	bgMode := strings.ToLower(strings.TrimSpace(bgModeRaw))
	switch bgMode {
	case "", "black", "upload", "local", "visualizer":
	default:
		return "", fmt.Errorf("unsupported bgMode %q (expected black, upload, local, or visualizer)", bgModeRaw)
	}
	l0Mode := strings.ToLower(strings.TrimSpace(layer0ModeRaw))
	switch l0Mode {
	case "", "black", "upload", "local", "visualizer":
	default:
		return "", fmt.Errorf("unsupported layer0Mode %q (expected black, upload, local, or visualizer)", layer0ModeRaw)
	}
	if bgMode != "" && l0Mode != "" && bgMode != l0Mode {
		return "", fmt.Errorf("conflicting bgMode %q and layer0Mode %q", bgModeRaw, layer0ModeRaw)
	}
	if l0Mode != "" {
		return l0Mode, nil
	}
	return bgMode, nil
}

func hasSessionMediaOverrides(media []sessionTrackMedia) bool {
	for _, tm := range media {
		if tm.source == "upload" || tm.source == "local" || tm.explicitEmpty || tm.layerMode != "" {
			return true
		}
	}
	return false
}

func validateOverrideSource(raw string) (string, error) {
	norm := strings.ToLower(strings.TrimSpace(raw))
	switch norm {
	case "", "upload", "local", "sidecar", "id3", "inherit", "backdrop", "auto":
		return norm, nil
	default:
		return "", fmt.Errorf("unsupported track override source %q", raw)
	}
}

func indexTrackOverrides(trackIDs []string, overrides []trackOverrideDTO) (map[string]trackOverrideDTO, error) {
	if len(overrides) == 0 {
		return nil, nil
	}
	validIDs := make(map[string]struct{}, len(trackIDs))
	for _, id := range trackIDs {
		validIDs[id] = struct{}{}
	}
	byID := make(map[string]trackOverrideDTO, len(overrides))
	for _, ov := range overrides {
		tid := strings.TrimSpace(ov.TrackID)
		if tid == "" {
			return nil, errors.New("trackOverrides[].trackId is required")
		}
		if len(trackIDs) > 0 {
			if _, ok := validIDs[tid]; !ok {
				return nil, fmt.Errorf("unknown trackId %q in trackOverrides", tid)
			}
		}
		if _, err := validateOverrideSource(ov.Source); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ov.LayerMode) != "" {
			if _, err := render.ValidateTrackLayerMode(render.TrackLayerMode(ov.LayerMode)); err != nil {
				return nil, err
			}
		}
		byID[tid] = ov
	}
	return byID, nil
}

func resolveTrackSourceAndLayer(
	tm sessionTrackMedia,
	ov trackOverrideDTO,
	hasOv bool,
) (effSource string, effLayer render.TrackLayerMode, err error) {
	effLayer = tm.layerMode
	if hasOv && strings.TrimSpace(ov.LayerMode) != "" {
		effLayer, err = render.ValidateTrackLayerMode(render.TrackLayerMode(ov.LayerMode))
		if err != nil {
			return "", "", err
		}
	}
	effSource = tm.source
	if effSource == "" {
		effSource = "inherit"
	}
	if hasOv && strings.TrimSpace(ov.Source) != "" {
		effSource, err = validateOverrideSource(ov.Source)
		if err != nil {
			return "", "", err
		}
	}
	return effSource, effLayer, nil
}

func buildEstimateSegments(
	tracks []mixer.Track,
	trackIDs []string,
	sessMedia []sessionTrackMedia,
	overrides []trackOverrideDTO,
	layoutMode mixer.TrackMediaLayout,
	crossfade float64,
	overlayTitles bool,
	localAllowed bool,
) ([]render.TrackSegmentSpec, []render.SegmentEstimateInput, int, error) {
	byID, err := indexTrackOverrides(trackIDs, overrides)
	if err != nil {
		return nil, nil, http.StatusBadRequest, err
	}

	chapters := make([]subtitle.ChapterTrack, len(tracks))
	for i, tr := range tracks {
		chapters[i] = subtitle.ChapterTrack{
			Title:    tr.DisplayTitle,
			Duration: tr.Duration,
		}
	}
	sched := subtitle.BuildChapterSchedule(chapters, crossfade)

	segs := make([]render.TrackSegmentSpec, len(tracks))
	segInputs := make([]render.SegmentEstimateInput, len(tracks))

	for i, tr := range tracks {
		slotDur := tr.Duration
		if i < len(tracks)-1 {
			slotDur = sched.Starts[i+1] - sched.Starts[i]
		}
		var tm sessionTrackMedia
		if i < len(sessMedia) {
			tm = sessMedia[i]
		}
		var tid string
		if i < len(trackIDs) {
			tid = trackIDs[i]
		}
		ov, hasOv := byID[tid]
		effSource, effLayer, err := resolveTrackSourceAndLayer(tm, ov, hasOv)
		if err != nil {
			return nil, nil, http.StatusBadRequest, err
		}

		var (
			mediaKind render.BackgroundKind
			layerMode render.TrackLayerMode
			mediaPath string
			isID3     bool
		)

		isExplicitEmpty := effLayer == render.LayerBackdrop ||
			effSource == "backdrop" ||
			(tm.explicitEmpty && (!hasOv || (strings.TrimSpace(ov.Source) == "" && strings.TrimSpace(ov.LayerMode) == "")))

		switch {
		case isExplicitEmpty:
			layerMode = render.LayerBackdrop
		case effSource == "upload":
			if tm.source != "upload" || tm.filePath == "" {
				return nil, nil, http.StatusBadRequest, fmt.Errorf("track %q has no uploaded visual media", tr.DisplayTitle)
			}
			mediaKind = tm.mediaKind
			mediaPath = tm.filePath
		case effSource == "local":
			if !localAllowed {
				return nil, nil, http.StatusForbidden, errors.New("local Track media is disabled on non-loopback or --host 0.0.0.0 sessions")
			}
			if tm.source != "local" || tm.filePath == "" {
				return nil, nil, http.StatusBadRequest, fmt.Errorf("track %q has no local visual media", tr.DisplayTitle)
			}
			mediaKind = tm.mediaKind
			mediaPath = tm.filePath
		case effSource == "sidecar":
			if !localAllowed {
				return nil, nil, http.StatusForbidden, errors.New("local sidecar media is disabled on non-loopback or --host 0.0.0.0 sessions")
			}
			if tm.sidecarPath == "" {
				return nil, nil, http.StatusBadRequest, fmt.Errorf("track %q has no sidecar media", tr.DisplayTitle)
			}
			mediaKind = tm.sidecarKind
			mediaPath = tm.sidecarPath
		case effSource == "id3":
			if !tr.HasEmbeddedArt {
				return nil, nil, http.StatusBadRequest, fmt.Errorf("track %q has no embedded ID3 art", tr.DisplayTitle)
			}
			mediaKind = render.BackgroundKindImage
			isID3 = true
		default:
			if tm.defaultMediaSource == "sidecar" && tm.sidecarPath != "" && localAllowed {
				mediaKind = tm.sidecarKind
				mediaPath = tm.sidecarPath
			} else if tm.defaultMediaSource == "id3" && tr.HasEmbeddedArt {
				mediaKind = render.BackgroundKindImage
				isID3 = true
			} else {
				if effLayer == render.LayerFullscreen || effLayer == render.LayerArtCard {
					return nil, nil, http.StatusBadRequest, fmt.Errorf("track %q requires visual media for layer mode %q", tr.DisplayTitle, effLayer)
				}
				layerMode = render.LayerBackdrop
			}
		}

		if layerMode == "" {
			switch {
			case effLayer != "":
				layerMode = effLayer
			case layoutMode == mixer.TrackMediaLayoutFullscreen:
				layerMode = render.LayerFullscreen
			case layoutMode == mixer.TrackMediaLayoutCard:
				layerMode = render.LayerArtCard
			case isID3:
				layerMode = render.LayerArtCard
			case tm.defaultLayerMode != "" && tm.defaultLayerMode != render.LayerBackdrop:
				layerMode = tm.defaultLayerMode
			default:
				layerMode = render.LayerFullscreen
			}
		}

		segs[i] = render.TrackSegmentSpec{
			Index:         i,
			StartSec:      sched.Starts[i],
			SlotDuration:  slotDur,
			MediaPath:     mediaPath,
			MediaKind:     mediaKind,
			LayerMode:     layerMode,
			OverlayTitles: overlayTitles,
		}
		segInputs[i] = render.SegmentEstimateInput{
			SlotDurationSec: slotDur,
			LayerMode:       layerMode,
			MediaKind:       mediaKind,
			OverlayTitles:   overlayTitles,
		}
	}

	return segs, segInputs, http.StatusOK, nil
}

func (s *Server) resolveRenderTrackOverrides(
	ctx context.Context,
	sessionID string,
	trackIDs []string,
	tracks []mixer.Track,
	sessMedia []sessionTrackMedia,
	tempDir string,
	overrides []trackOverrideDTO,
	localAllowed bool,
) ([]mixer.TrackMediaOverride, int, error) {
	byID, err := indexTrackOverrides(trackIDs, overrides)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}

	out := make([]mixer.TrackMediaOverride, len(tracks))
	for i, tr := range tracks {
		tid := trackIDs[i]
		var tm sessionTrackMedia
		if i < len(sessMedia) {
			tm = sessMedia[i]
		}
		ov, hasOv := byID[tid]
		effSource, effLayer, err := resolveTrackSourceAndLayer(tm, ov, hasOv)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}

		isExplicitEmpty := effLayer == render.LayerBackdrop ||
			effSource == "backdrop" ||
			(tm.explicitEmpty && (!hasOv || (strings.TrimSpace(ov.Source) == "" && strings.TrimSpace(ov.LayerMode) == "")))

		switch {
		case isExplicitEmpty:
			out[i] = mixer.TrackMediaOverride{
				TrackIndex:    i,
				LayerMode:     render.LayerBackdrop,
				ExplicitEmpty: true,
			}
		case effSource == "upload":
			if tm.source != "upload" || tm.filePath == "" || tempDir == "" {
				return nil, http.StatusBadRequest, fmt.Errorf("track %q has no uploaded visual media", tr.DisplayTitle)
			}
			f, openErr := openFileInDir(tempDir, tm.filePath)
			if openErr != nil {
				return nil, http.StatusBadRequest, fmt.Errorf("track %q uploaded visual unavailable: %w", tr.DisplayTitle, openErr)
			}
			_ = f.Close()
			out[i] = mixer.TrackMediaOverride{
				TrackIndex: i,
				MediaPath:  tm.filePath,
				MediaKind:  tm.mediaKind,
				LayerMode:  effLayer,
			}
		case effSource == "local":
			if !localAllowed {
				return nil, http.StatusForbidden, errors.New("local Track media is disabled on non-loopback or --host 0.0.0.0 sessions")
			}
			if tm.source != "local" || tm.filePath == "" {
				return nil, http.StatusBadRequest, fmt.Errorf("track %q has no local visual media", tr.DisplayTitle)
			}
			out[i] = mixer.TrackMediaOverride{
				TrackIndex: i,
				MediaPath:  tm.filePath,
				MediaKind:  tm.mediaKind,
				LayerMode:  effLayer,
			}
		case effSource == "sidecar":
			if !localAllowed {
				return nil, http.StatusForbidden, errors.New("local sidecar media is disabled on non-loopback or --host 0.0.0.0 sessions")
			}
			if tm.sidecarPath == "" {
				return nil, http.StatusBadRequest, fmt.Errorf("track %q has no sidecar media", tr.DisplayTitle)
			}
			out[i] = mixer.TrackMediaOverride{
				TrackIndex: i,
				MediaPath:  tm.sidecarPath,
				MediaKind:  tm.sidecarKind,
				LayerMode:  effLayer,
			}
		case effSource == "id3":
			if !tr.HasEmbeddedArt {
				return nil, http.StatusBadRequest, fmt.Errorf("track %q has no embedded ID3 art", tr.DisplayTitle)
			}
			_, artPath, extractErr := s.ensureSessionTrackExtractedArt(ctx, sessionID, tid, tr, tm)
			if extractErr != nil {
				return nil, http.StatusInternalServerError, fmt.Errorf("extract embedded ID3 art for %q: %w", tr.DisplayTitle, extractErr)
			}
			out[i] = mixer.TrackMediaOverride{
				TrackIndex: i,
				MediaPath:  artPath,
				MediaKind:  render.BackgroundKindImage,
				LayerMode:  effLayer,
			}
		default:
			if tm.defaultMediaSource == "sidecar" && tm.sidecarPath != "" && localAllowed {
				out[i] = mixer.TrackMediaOverride{
					TrackIndex: i,
					MediaPath:  tm.sidecarPath,
					MediaKind:  tm.sidecarKind,
					LayerMode:  effLayer,
				}
			} else if tm.defaultMediaSource == "id3" && tr.HasEmbeddedArt {
				out[i] = mixer.TrackMediaOverride{
					TrackIndex: i,
					LayerMode:  effLayer,
				}
			} else {
				if effLayer == render.LayerFullscreen || effLayer == render.LayerArtCard {
					return nil, http.StatusBadRequest, fmt.Errorf("track %q requires visual media for layer mode %q", tr.DisplayTitle, effLayer)
				}
				out[i] = mixer.TrackMediaOverride{
					TrackIndex:    i,
					LayerMode:     render.LayerBackdrop,
					ExplicitEmpty: true,
				}
			}
		}
	}
	return out, http.StatusOK, nil
}

func (s *Server) parseRenderRequest(w http.ResponseWriter, r *http.Request, jobDir string) (renderConfigRequest, string, int, error) {
	var req renderConfigRequest
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "multipart/form-data") {
		if !strings.HasPrefix(ct, "application/json") {
			return req, "", http.StatusUnsupportedMediaType, errors.New("Content-Type must be multipart/form-data or application/json")
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			return req, "", http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err)
		}
		return req, "", http.StatusOK, nil
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return req, "", http.StatusBadRequest, fmt.Errorf("invalid multipart request: %w", err)
	}

	var bgPath string
	var hasConfig bool
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			status := http.StatusBadRequest
			if isPayloadTooLarge(err) {
				status = http.StatusRequestEntityTooLarge
			}
			return req, "", status, fmt.Errorf("read render multipart stream: %w", err)
		}

		switch part.FormName() {
		case "config":
			raw, err := io.ReadAll(io.LimitReader(part, 1<<20))
			_ = part.Close()
			if err != nil {
				return req, "", http.StatusBadRequest, fmt.Errorf("read config field: %w", err)
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return req, "", http.StatusBadRequest, fmt.Errorf("invalid config JSON: %w", err)
			}
			hasConfig = true
		case "bg":
			safeName := sanitizeBaseName(part.FileName(), "upload.bin")
			literalExt, ok := render.CanonicalBackgroundExt(filepath.Ext(safeName))
			if !ok {
				_ = part.Close()
				return req, "", http.StatusBadRequest, fmt.Errorf("unsupported Background Visual extension %q (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)", filepath.Ext(safeName))
			}
			dstName := "bg_upload" + literalExt
			dstPath := filepath.Join(jobDir, dstName)
			_, err := saveUploadedPartInDir(jobDir, dstName, part, s.cfg.MaxUploadBytes)
			_ = part.Close()
			if err != nil {
				status := http.StatusBadRequest
				if isPayloadTooLarge(err) {
					status = http.StatusRequestEntityTooLarge
				}
				return req, "", status, fmt.Errorf("save uploaded Background Visual: %w", err)
			}
			bgPath = dstPath
		default:
			_ = part.Close()
		}
	}

	if !hasConfig {
		return req, "", http.StatusBadRequest, errors.New("missing multipart config JSON field")
	}
	return req, bgPath, http.StatusOK, nil
}

func (s *Server) runRenderJob(ctx context.Context, job *renderJob, cfg mixer.Config, tracks []mixer.Track) {
	defer s.store.releaseActive(job.id)
	defer job.cancel()

	jobStart := job.startedAt
	if jobStart.IsZero() {
		jobStart = time.Now()
	}

	var mu sync.Mutex
	lastStep := 0
	lastPct := -1
	var lastProgressAt time.Time
	var estimatedSec float64
	sink := func(ev mixer.TelemetryEvent) {
		if ev.EstimatedTotalSec > 0 {
			mu.Lock()
			estimatedSec = ev.EstimatedTotalSec
			mu.Unlock()
		}
		switch ev.Kind {
		case mixer.EventStep:
			stepPayload := map[string]any{
				"step":     ev.Step,
				"total":    ev.TotalSteps,
				"message":  ev.Message,
				"detail":   ev.Detail,
				"progress": ev.Progress,
			}
			if ev.EstimatedTotalSec > 0 {
				stepPayload["estimatedTotalSec"] = ev.EstimatedTotalSec
				if ev.RemainingSec >= 0 {
					stepPayload["remainingSec"] = math.Round(ev.RemainingSec)
				}
			}
			job.emit("step", stepPayload)
		case mixer.EventProgress:
			mu.Lock()
			now := time.Now()
			shouldEmit := lastPct < 0 ||
				ev.Step != lastStep ||
				ev.Progress == 99 ||
				(ev.Progress != lastPct && (ev.Progress-lastPct >= 5 || now.Sub(lastProgressAt) >= 250*time.Millisecond)) ||
				now.Sub(lastProgressAt) >= time.Second
			if shouldEmit {
				lastStep = ev.Step
				lastPct = ev.Progress
				lastProgressAt = now
			}
			mu.Unlock()
			if !shouldEmit {
				return
			}
			remSec := -1.0
			if ev.EstimatedTotalSec > 0 && ev.RemainingSec >= 0 {
				remSec = math.Round(ev.RemainingSec)
			}
			job.emitProgress(map[string]any{
				"step":              ev.Step,
				"total":             ev.TotalSteps,
				"progress":          ev.Progress,
				"substage":          ev.Substage,
				"outTimeSec":        ev.OutTimeSec,
				"totalDurationSec":  ev.TotalDurationSec,
				"speed":             ev.Speed,
				"remainingSec":      remSec,
				"estimatedTotalSec": ev.EstimatedTotalSec,
			})
		case mixer.EventDone:
			job.emit("log", map[string]any{"message": ev.Message})
		}
	}

	err := mixer.RunWithTracks(ctx, cfg, tracks, sink, s.cfg.LookPath, s.cfg.Runner, s.cfg.StreamRunner)
	if err != nil {
		job.finish(jobFailed, err.Error(), "error", map[string]any{
			"jobId":   job.id,
			"message": err.Error(),
		})
		return
	}

	job.mu.Lock()
	tsPath := job.tsPath
	job.mu.Unlock()

	var tsText string
	if tsFile, openErr := s.openJobArtifact(job, tsPath); openErr == nil {
		if tsBytes, readErr := io.ReadAll(tsFile); readErr == nil {
			tsText = string(tsBytes)
		}
		_ = tsFile.Close()
	}

	mu.Lock()
	estSec := estimatedSec
	mu.Unlock()
	actualSec := time.Since(jobStart).Seconds()

	job.finish(jobComplete, "", "complete", map[string]any{
		"jobId":          job.id,
		"renderingPath":  job.renderingPath,
		"timestampsText": tsText,
		"outputUrl":      fmt.Sprintf("/api/render/%s/output", job.id),
		"timestampsUrl":  fmt.Sprintf("/api/render/%s/timestamps", job.id),
		"outputName":     job.outName,
		"timestampsName": job.tsName,
		"estimatedSec":   estSec,
		"actualSec":      actualSec,
	})
}

func (s *Server) handleRenderEvents(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	job, ok := s.store.getJob(jobID)
	if !ok {
		writeError(w, http.StatusNotFound, "render job not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	history, ch, done := job.subscribe()
	defer job.unsubscribe(ch)

	for _, ev := range history {
		if r.Context().Err() != nil {
			return
		}
		if err := writeSSEEvent(w, ev); err != nil {
			return
		}
	}
	if r.Context().Err() != nil {
		return
	}
	flusher.Flush()
	if done {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				return
			}
			flusher.Flush()
			if ev.name == "complete" || ev.name == "error" {
				return
			}
		}
	}
}

func (s *Server) handleRenderOutput(w http.ResponseWriter, r *http.Request) {
	s.serveJobArtifact(w, r, true)
}

func (s *Server) handleRenderTimestamps(w http.ResponseWriter, r *http.Request) {
	s.serveJobArtifact(w, r, false)
}

func (s *Server) serveJobArtifact(w http.ResponseWriter, r *http.Request, isOutput bool) {
	job, ok := s.store.getJob(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "render job not found")
		return
	}

	job.mu.Lock()
	status := job.status
	errMsg := job.errMsg
	filePath := job.tsPath
	fileName := job.tsName
	contentType := "text/plain; charset=utf-8"
	if isOutput {
		filePath = job.outPath
		fileName = job.outName
		contentType = "video/mp4"
	}
	job.mu.Unlock()

	if status == jobRunning {
		writeError(w, http.StatusConflict, "render job is still in progress")
		return
	}
	if status == jobFailed {
		writeError(w, http.StatusConflict, fmt.Sprintf("render job failed: %s", errMsg))
		return
	}

	f, err := s.openJobArtifact(job, filePath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("artifact not available: %v", err))
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("stat artifact: %v", err))
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeHeaderFilename(fileName)))
	http.ServeContent(w, r, fileName, info.ModTime(), f)
}

func (s *Server) openJobArtifact(job *renderJob, filePath string) (*os.File, error) {
	if job.dir != "" && isPathWithinRoot(job.dir, filePath) {
		return openFileInDir(job.dir, filePath)
	}
	f, _, _, err := s.openAllowedFile(filePath)
	return f, err
}

func writeSSEEvent(w io.Writer, ev sseEvent) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", ev.name); err != nil {
		return err
	}
	for _, line := range strings.Split(ev.data, "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
