package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/probe"
	"github.com/GCherry/ytmix/internal/render"
	"github.com/GCherry/ytmix/internal/subtitle"
)

type trackDTO struct {
	ID           string  `json:"id"`
	FileName     string  `json:"fileName"`
	DisplayTitle string  `json:"displayTitle"`
	Duration     float64 `json:"duration"`
}

type probeResponse struct {
	SessionID string     `json:"sessionId"`
	Source    string     `json:"source"`
	Tracks    []trackDTO `json:"tracks"`
}

type localProbeRequest struct {
	AudioDir string `json:"audioDir"`
	Shuffle  bool   `json:"shuffle"`
}

type bgMetaDTO struct {
	DurationSec float64 `json:"durationSec"`
	FPS         float64 `json:"fps"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	CodecName   string  `json:"codecName"`
	PixFmt      string  `json:"pixFmt"`
}

type estimateRequest struct {
	SessionID            string     `json:"sessionId"`
	TrackIDs             []string   `json:"trackIds"`
	TrackDurations       []float64  `json:"trackDurations"`
	BGMode               string     `json:"bgMode"`
	BGPath               string     `json:"bgPath"`
	BGName               string     `json:"bgName"`
	BGMeta               *bgMetaDTO `json:"bgMeta"`
	Crossfade            float64    `json:"crossfade"`
	Normalize            bool       `json:"normalize"`
	OverlayTitles        bool       `json:"overlayTitles"`
	Visualizer           string     `json:"visualizer"`
	VisualizerTheme      string     `json:"visualizerTheme"`
	VisualizerResolution string     `json:"visualizerResolution"`
	VisualizerGlow       *bool      `json:"visualizerGlow"`
}

func (s *Server) handleSessionTrackAudio(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	tr, tempDir, err := s.store.resolveTrackWithDir(r.PathValue("sessionId"), r.PathValue("trackId"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !strings.EqualFold(filepath.Ext(tr.Path), ".mp3") {
		writeError(w, http.StatusBadRequest, "unsupported Track extension")
		return
	}

	var f *os.File
	if tempDir != "" {
		f, err = openFileInDir(tempDir, tr.Path)
	} else {
		f, _, _, err = s.openAllowedFile(tr.Path)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("cannot open Track audio: %v", err))
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "track audio unavailable")
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, sanitizeBaseName(tr.FileName, "track.mp3"), info.ModTime(), f)
}

func (s *Server) handlePreviewBG(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "local Background Visual preview is disabled on non-loopback or --host 0.0.0.0 sessions")
		return
	}
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	if _, ok := render.CanonicalBackgroundExt(filepath.Ext(rawPath)); !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Background Visual extension %q", filepath.Ext(rawPath)))
		return
	}
	f, bgPath, status, err := s.openAllowedFile(rawPath)
	if err != nil {
		if status == http.StatusNotFound {
			writeError(w, http.StatusNotFound, fmt.Sprintf("cannot open Background Visual: %v", err))
			return
		}
		writeError(w, status, err.Error())
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusBadRequest, "background visual path must reference a file")
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, filepath.Base(bgPath), info.ModTime(), f)
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "multipart/form-data") {
		s.handleProbeMultipart(w, r)
		return
	}
	if !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be multipart/form-data or application/json")
		return
	}
	s.handleProbeLocal(w, r)
}

func (s *Server) handleProbeMultipart(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart upload: %v", err))
		return
	}

	sessionDir, err := os.MkdirTemp("", "ytmix-ui-session-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create upload workspace: %v", err))
		return
	}
	keepDir := false
	defer func() {
		if !keepDir {
			os.RemoveAll(sessionDir)
		}
	}()

	var uploaded []mixer.NamedTrackFile
	var totalWritten int64
	idx := 0

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
			writeError(w, status, fmt.Sprintf("read upload stream: %v", err))
			return
		}

		formName := part.FormName()
		rawName := part.FileName()
		if rawName == "" || (formName != "tracks" && formName != "files") {
			_ = part.Close()
			continue
		}

		safeName := sanitizeBaseName(rawName, "upload.bin")
		if !strings.EqualFold(filepath.Ext(safeName), ".mp3") {
			_ = part.Close()
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Track extension %q for %q (expected .mp3)", filepath.Ext(safeName), safeName))
			return
		}

		idx++
		dstName := fmt.Sprintf("track_%04d.mp3", idx)
		dstPath := filepath.Join(sessionDir, dstName)
		written, err := saveUploadedPartInDir(sessionDir, dstName, part, s.cfg.MaxUploadBytes-totalWritten)
		_ = part.Close()
		if err != nil {
			status := http.StatusBadRequest
			if isPayloadTooLarge(err) {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, status, fmt.Sprintf("save uploaded Track %q: %v", safeName, err))
			return
		}
		totalWritten += written
		uploaded = append(uploaded, mixer.NamedTrackFile{
			Path:     dstPath,
			FileName: safeName,
		})
	}

	if len(uploaded) == 0 {
		writeError(w, http.StatusBadRequest, "no .mp3 Track files uploaded")
		return
	}

	tracks, err := mixer.ProbeNamedTrackFiles(r.Context(), uploaded, s.cfg.Runner)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sessID, err := s.saveSession("upload", sessionDir, tracks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	keepDir = true

	writeJSON(w, http.StatusOK, s.buildProbeResponse(sessID, "upload"))
}

func (s *Server) handleProbeLocal(w http.ResponseWriter, r *http.Request) {
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "local filesystem paths are disabled on non-loopback or --host 0.0.0.0 sessions; upload MP3 files instead")
		return
	}

	var req localProbeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	req.AudioDir = strings.TrimSpace(req.AudioDir)
	if req.AudioDir == "" {
		writeError(w, http.StatusBadRequest, "audioDir is required")
		return
	}
	safeDir, err := s.resolveAllowedPath(req.AudioDir, true)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	tracks, err := mixer.ProbeTracks(r.Context(), safeDir, req.Shuffle, nil, s.cfg.Runner)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sessID, err := s.saveSession("local", "", tracks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, s.buildProbeResponse(sessID, "local"))
}

func (s *Server) handleEstimate(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	var req estimateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	if math.IsNaN(req.Crossfade) || math.IsInf(req.Crossfade, 0) || req.Crossfade < 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("--crossfade must be >= 0 (got %v)", req.Crossfade))
		return
	}

	tracks, err := s.resolveEstimateTracks(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	bgMode := strings.ToLower(strings.TrimSpace(req.BGMode))
	switch bgMode {
	case "", "upload", "local", "visualizer":
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported bgMode %q (expected upload, local, or visualizer)", req.BGMode))
		return
	}

	rawBGPath := strings.TrimSpace(req.BGPath)
	rawBGName := strings.TrimSpace(req.BGName)
	rawViz := strings.TrimSpace(req.Visualizer)
	isVisualizerMode := bgMode == "visualizer" || (bgMode == "" && rawBGPath == "" && rawBGName == "" && rawViz != "")

	if isVisualizerMode {
		if rawBGPath != "" || rawBGName != "" {
			writeError(w, http.StatusBadRequest, "--bg and --visualizer cannot be used together")
			return
		}
		vizGlow := true
		if req.VisualizerGlow != nil {
			vizGlow = *req.VisualizerGlow
		}
		vizCfg, err := render.ValidateVisualizerConfig(render.VisualizerConfig{
			Preset:     rawViz,
			Theme:      req.VisualizerTheme,
			Resolution: req.VisualizerResolution,
			Glow:       vizGlow,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		encName := "libx264"
		if vizCfg.Preset != "black" && s.cfg.Runner != nil {
			encName = s.detectPathDEncoder(r.Context(), vizCfg.Resolution).Name
		}
		est := estimateMixForTracks(render.PathD, encName, req.Crossfade, req.Normalize, req.OverlayTitles, vizCfg, tracks, probe.BackgroundMeta{})
		writeJSON(w, http.StatusOK, est)
		return
	}

	if rawViz != "" {
		writeError(w, http.StatusBadRequest, "--bg and --visualizer cannot be used together")
		return
	}

	if bgMode == "upload" {
		if rawBGName == "" {
			writeError(w, http.StatusBadRequest, "bgName is required in upload mode")
			return
		}
		if _, ok := render.CanonicalBackgroundExt(filepath.Ext(rawBGName)); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Background Visual extension %q", filepath.Ext(rawBGName)))
			return
		}
		pathKind, err := render.SelectPath(rawBGName, req.OverlayTitles)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		bgMeta := sanitizeBGMeta(req.BGMeta, pathKind)
		encName := "libx264"
		if pathKind == render.PathB && s.cfg.Runner != nil {
			encName = s.detectEncoder(r.Context()).Name
		}
		est := estimateMixForTracks(pathKind, encName, req.Crossfade, req.Normalize, req.OverlayTitles, render.VisualizerConfig{}, tracks, bgMeta)
		writeJSON(w, http.StatusOK, est)
		return
	}

	// Local Background Visual path mode: strictly confined to loopback and allowedRoots (ADR-0002).
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "local Background Visual paths are disabled on non-loopback or --host 0.0.0.0 sessions")
		return
	}
	if rawBGPath == "" {
		writeError(w, http.StatusBadRequest, "bgPath is required in local mode")
		return
	}
	if _, ok := render.CanonicalBackgroundExt(filepath.Ext(rawBGPath)); !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Background Visual extension %q", filepath.Ext(rawBGPath)))
		return
	}
	safeBG, err := s.resolveAllowedPath(rawBGPath, true)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err := mixer.ValidateBackgroundFile(safeBG); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pathKind, err := render.SelectPath(safeBG, req.OverlayTitles)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var bgMeta probe.BackgroundMeta
	if (pathKind == render.PathA || pathKind == render.PathB) && s.cfg.Runner != nil {
		if probed, probeErr := probe.ProbeBackgroundVisual(r.Context(), safeBG, s.cfg.Runner); probeErr == nil {
			bgMeta = probed
		}
	}
	est, err := mixer.EstimateConfig(r.Context(), mixer.Config{
		BG:            safeBG,
		Crossfade:     req.Crossfade,
		Normalize:     req.Normalize,
		OverlayTitles: req.OverlayTitles,
	}, tracks, &bgMeta, s.cfg.LookPath, s.cfg.Runner)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, est)
}

func (s *Server) resolveEstimateTracks(req estimateRequest) ([]mixer.Track, error) {
	if strings.TrimSpace(req.SessionID) != "" {
		return s.store.resolveTracks(req.SessionID, req.TrackIDs)
	}
	if len(req.TrackDurations) == 0 {
		return nil, errors.New("sessionId and trackIds (or trackDurations) are required")
	}
	tracks := make([]mixer.Track, len(req.TrackDurations))
	for i, dur := range req.TrackDurations {
		if math.IsNaN(dur) || math.IsInf(dur, 0) || dur <= 0 {
			return nil, fmt.Errorf("trackDurations[%d] must be positive and finite", i)
		}
		tracks[i] = mixer.Track{
			Duration:     dur,
			DisplayTitle: fmt.Sprintf("Track %d", i+1),
		}
	}
	return tracks, nil
}

func sanitizeBGMeta(dto *bgMetaDTO, pathKind render.Path) probe.BackgroundMeta {
	if dto == nil {
		return probe.BackgroundMeta{}
	}
	var out probe.BackgroundMeta
	if dto.Width > 0 {
		out.Width = dto.Width
	}
	if dto.Height > 0 {
		out.Height = dto.Height
	}
	out.CodecName = strings.TrimSpace(dto.CodecName)
	out.PixFmt = strings.TrimSpace(dto.PixFmt)
	if pathKind == render.PathA || pathKind == render.PathB {
		if !math.IsNaN(dto.DurationSec) && !math.IsInf(dto.DurationSec, 0) && dto.DurationSec > 0 {
			out.DurationSec = dto.DurationSec
		}
		if !math.IsNaN(dto.FPS) && !math.IsInf(dto.FPS, 0) && dto.FPS > 0 && dto.FPS <= 240 {
			out.FPS = dto.FPS
		}
	}
	return out
}

func estimateMixForTracks(
	pathKind render.Path,
	encName string,
	crossfade float64,
	normalize bool,
	overlayTitles bool,
	vizCfg render.VisualizerConfig,
	tracks []mixer.Track,
	bgMeta probe.BackgroundMeta,
) render.MixEstimate {
	var totalAudioSec float64
	chapters := make([]subtitle.ChapterTrack, len(tracks))
	for i, tr := range tracks {
		totalAudioSec += tr.Duration
		chapters[i] = subtitle.ChapterTrack{
			Title:    tr.DisplayTitle,
			Duration: tr.Duration,
		}
	}
	mixDurSec := totalAudioSec
	if len(tracks) > 0 {
		sched := subtitle.BuildChapterSchedule(chapters, crossfade)
		mixDurSec = sched.TotalDuration
	}
	calPath, _ := mixer.DefaultCalibrationPath()
	cal := mixer.LoadCalibration(calPath)
	return render.EstimateMix(render.EstimateInput{
		Path:           pathKind,
		EncoderName:    encName,
		MixDurationSec: mixDurSec,
		TotalAudioSec:  totalAudioSec,
		TrackCount:     len(tracks),
		Normalize:      normalize,
		Crossfade:      crossfade,
		OverlayTitles:  overlayTitles,
		Visualizer:     vizCfg,
		CPUCores:       runtime.NumCPU(),
		BG: render.BackgroundEstimateInput{
			DurationSec: bgMeta.DurationSec,
			FPS:         bgMeta.FPS,
			Width:       bgMeta.Width,
			Height:      bgMeta.Height,
			CodecName:   bgMeta.CodecName,
			PixFmt:      bgMeta.PixFmt,
		},
	}, cal)
}

func (s *Server) saveSession(source, tempDir string, tracks []mixer.Track) (string, error) {
	sessID, err := newOpaqueID("sess")
	if err != nil {
		return "", err
	}
	trackMap := make(map[string]mixer.Track, len(tracks))
	order := make([]string, 0, len(tracks))
	for i, tr := range tracks {
		tid := fmt.Sprintf("trk_%d", i+1)
		trackMap[tid] = tr
		order = append(order, tid)
	}
	s.store.addSession(&sessionState{
		id:      sessID,
		source:  source,
		tempDir: tempDir,
		tracks:  trackMap,
		order:   order,
	})
	return sessID, nil
}

func (s *Server) buildProbeResponse(sessID, source string) probeResponse {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	sess, ok := s.store.sessions[sessID]
	if !ok {
		return probeResponse{SessionID: sessID, Source: source}
	}
	dtos := make([]trackDTO, 0, len(sess.order))
	for _, tid := range sess.order {
		tr := sess.tracks[tid]
		dtos = append(dtos, trackDTO{
			ID:           tid,
			FileName:     tr.FileName,
			DisplayTitle: tr.DisplayTitle,
			Duration:     tr.Duration,
		})
	}
	return probeResponse{
		SessionID: sessID,
		Source:    source,
		Tracks:    dtos,
	}
}

func isPayloadTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr) || strings.Contains(err.Error(), "exceeds maximum allowed size")
}
