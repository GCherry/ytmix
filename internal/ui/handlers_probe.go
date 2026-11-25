package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
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
	ID                 string  `json:"id"`
	FileName           string  `json:"fileName"`
	DisplayTitle       string  `json:"displayTitle"`
	Duration           float64 `json:"duration"`
	HasEmbeddedArt     bool    `json:"hasEmbeddedArt"`
	EmbeddedArtWidth   int     `json:"embeddedArtWidth"`
	EmbeddedArtHeight  int     `json:"embeddedArtHeight"`
	SidecarName        string  `json:"sidecarName"`
	SidecarKind        string  `json:"sidecarKind"`
	DefaultMediaSource string  `json:"defaultMediaSource"`
	DefaultLayerMode   string  `json:"defaultLayerMode"`
	PreviewURL         string  `json:"previewUrl"`
}

type trackOverrideDTO struct {
	TrackID   string `json:"trackId"`
	LayerMode string `json:"layerMode"`
	Source    string `json:"source"`
}

type trackMediaUpdateRequest struct {
	LocalPath       string `json:"localPath"`
	LayerMode       string `json:"layerMode"`
	RevertToDefault bool   `json:"revertToDefault"`
}

type trackMediaResponse struct {
	TrackID       string `json:"trackId"`
	Source        string `json:"source"`
	MediaName     string `json:"mediaName"`
	MediaKind     string `json:"mediaKind"`
	LayerMode     string `json:"layerMode"`
	ExplicitEmpty bool   `json:"explicitEmpty"`
	PreviewURL    string `json:"previewUrl"`
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
	SessionID            string             `json:"sessionId"`
	TrackIDs             []string           `json:"trackIds"`
	TrackDurations       []float64          `json:"trackDurations"`
	BGMode               string             `json:"bgMode"`
	Layer0Mode           string             `json:"layer0Mode"`
	BGPath               string             `json:"bgPath"`
	BGName               string             `json:"bgName"`
	BGMeta               *bgMetaDTO         `json:"bgMeta"`
	Crossfade            float64            `json:"crossfade"`
	Normalize            bool               `json:"normalize"`
	OverlayTitles        bool               `json:"overlayTitles"`
	Visualizer           string             `json:"visualizer"`
	VisualizerTheme      string             `json:"visualizerTheme"`
	VisualizerResolution string             `json:"visualizerResolution"`
	VisualizerGlow       *bool              `json:"visualizerGlow"`
	Segmented            *bool              `json:"segmented"`
	TrackTransition      string             `json:"trackTransition"`
	TrackMediaLayout     string             `json:"trackMediaLayout"`
	TrackOverrides       []trackOverrideDTO `json:"trackOverrides"`
}

func (s *Server) handleSessionTrackAudio(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	tr, sessSource, tempDir, err := s.store.resolveTrackWithDir(r.PathValue("sessionId"), r.PathValue("trackId"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !strings.EqualFold(filepath.Ext(tr.Path), ".mp3") {
		writeError(w, http.StatusBadRequest, "unsupported Track extension")
		return
	}

	var f *os.File
	if sessSource == "upload" && tempDir != "" {
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

	tracks, err := mixer.ProbeNamedTrackFilesWithArt(r.Context(), uploaded, s.cfg.Runner)
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

	tracks, err := mixer.ProbeTracksWithArt(r.Context(), safeDir, req.Shuffle, nil, s.cfg.Runner)
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

func (s *Server) handleSessionTrackMediaUpload(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	sessionID := r.PathValue("sessionId")
	trackID := r.PathValue("trackId")

	tr, tm, _, _, err := s.store.getSessionTrackMedia(sessionID, trackID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(ct, "multipart/form-data") {
		s.handleSessionTrackMediaMultipart(w, r, sessionID, trackID, tm)
		return
	}
	if !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be multipart/form-data or application/json")
		return
	}

	var req trackMediaUpdateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}

	var parsedMode render.TrackLayerMode
	if strings.TrimSpace(req.LayerMode) != "" {
		m, err := render.ValidateTrackLayerMode(render.TrackLayerMode(req.LayerMode))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		parsedMode = m
	}

	if req.RevertToDefault {
		next := resetTrackMediaToDefault(tm, parsedMode)
		stale, err := s.store.setSessionTrackMedia(sessionID, trackID, next)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if stale != "" {
			_ = os.Remove(stale)
		}
		writeJSON(w, http.StatusOK, buildTrackMediaResponse(sessionID, trackID, next))
		return
	}

	localPath := strings.TrimSpace(req.LocalPath)
	if localPath != "" {
		if !s.localPathsAllowed(r) {
			writeError(w, http.StatusForbidden, "local media paths are disabled on non-loopback or --host 0.0.0.0 sessions")
			return
		}
		if _, ok := render.CanonicalBackgroundExt(filepath.Ext(localPath)); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Track media extension %q", filepath.Ext(localPath)))
			return
		}
		safePath, err := s.resolveAllowedPath(localPath, true)
		if err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		if err := mixer.ValidateBackgroundFile(safePath); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		kind, _, err := render.ClassifyBackground(safePath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		layerMode := parsedMode
		if layerMode == "" {
			layerMode = s.resolveAutoLayerModeAllowed(safePath, kind)
		}
		next := tm
		next.source = "local"
		next.filePath = safePath
		next.mediaKind = kind
		next.mediaName = filepath.Base(safePath)
		next.layerMode = layerMode
		next.layerPinned = parsedMode != ""
		next.explicitEmpty = layerMode == render.LayerBackdrop
		stale, err := s.store.setSessionTrackMedia(sessionID, trackID, next)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if stale != "" {
			_ = os.Remove(stale)
		}
		writeJSON(w, http.StatusOK, buildTrackMediaResponse(sessionID, trackID, next))
		return
	}

	if parsedMode == "" {
		writeError(w, http.StatusBadRequest, "file, localPath, layerMode, or revertToDefault is required")
		return
	}

	next := tm
	if parsedMode == render.LayerBackdrop {
		next.layerMode = render.LayerBackdrop
		next.layerPinned = true
		next.explicitEmpty = true
	} else {
		hasActiveFile := (tm.source == "upload" || tm.source == "local") && tm.filePath != ""
		hasDefault := tm.defaultMediaSource == "sidecar" || (tm.defaultMediaSource == "id3" && tr.HasEmbeddedArt)
		if !hasActiveFile && !hasDefault {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("track %q has no visual media for %q mode", tr.DisplayTitle, parsedMode))
			return
		}
		next.explicitEmpty = false
		next.layerMode = parsedMode
		next.layerPinned = true
		if !hasActiveFile {
			next = resetTrackMediaToDefault(next, parsedMode)
		}
	}
	stale, err := s.store.setSessionTrackMedia(sessionID, trackID, next)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if stale != "" {
		_ = os.Remove(stale)
	}
	writeJSON(w, http.StatusOK, buildTrackMediaResponse(sessionID, trackID, next))
}

func (s *Server) handleSessionTrackMediaMultipart(w http.ResponseWriter, r *http.Request, sessionID, trackID string, prev sessionTrackMedia) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart upload: %v", err))
		return
	}

	tempDir, err := s.store.ensureSessionTempDir(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var (
		uploadedPath string
		uploadedFile string
		uploadedName string
		uploadedKind render.BackgroundKind
		rawLayerMode string
	)
	removeInTemp := func(relName string) {
		if relName == "" {
			return
		}
		if root, rootErr := os.OpenRoot(tempDir); rootErr == nil {
			_ = root.Remove(relName)
			_ = root.Close()
		}
	}
	cleanupUpload := func() {
		removeInTemp(uploadedFile)
	}

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanupUpload()
			status := http.StatusBadRequest
			if isPayloadTooLarge(err) {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, status, fmt.Sprintf("read upload stream: %v", err))
			return
		}

		switch part.FormName() {
		case "layerMode":
			raw, readErr := io.ReadAll(io.LimitReader(part, 1024))
			_ = part.Close()
			if readErr != nil {
				cleanupUpload()
				writeError(w, http.StatusBadRequest, fmt.Sprintf("read layerMode: %v", readErr))
				return
			}
			rawLayerMode = strings.TrimSpace(string(raw))
		case "file":
			safeName := sanitizeBaseName(part.FileName(), "upload.bin")
			literalExt, ok := render.CanonicalBackgroundExt(filepath.Ext(safeName))
			if !ok {
				_ = part.Close()
				cleanupUpload()
				writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Track media extension %q (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)", filepath.Ext(safeName)))
				return
			}
			kind, _, err := render.ClassifyBackground("media" + literalExt)
			if err != nil {
				_ = part.Close()
				cleanupUpload()
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			seq := s.store.mediaSeq.Add(1)
			dstName := fmt.Sprintf("media_%d%s", seq, literalExt)
			dstPath := filepath.Join(tempDir, dstName)
			if _, err := saveUploadedPartInDir(tempDir, dstName, part, s.cfg.MaxUploadBytes); err != nil {
				_ = part.Close()
				removeInTemp(dstName)
				cleanupUpload()
				status := http.StatusBadRequest
				if isPayloadTooLarge(err) {
					status = http.StatusRequestEntityTooLarge
				}
				writeError(w, status, fmt.Sprintf("save uploaded Track media %q: %v", safeName, err))
				return
			}
			_ = part.Close()
			cleanupUpload()
			uploadedPath = dstPath
			uploadedFile = dstName
			uploadedName = safeName
			uploadedKind = kind
		default:
			_ = part.Close()
		}
	}

	if uploadedPath == "" {
		writeError(w, http.StatusBadRequest, "file field is required in multipart upload")
		return
	}

	var layerMode render.TrackLayerMode
	if rawLayerMode != "" {
		m, err := render.ValidateTrackLayerMode(render.TrackLayerMode(rawLayerMode))
		if err != nil {
			cleanupUpload()
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		layerMode = m
	} else {
		layerMode = resolveAutoLayerModeInDir(tempDir, uploadedPath, uploadedKind)
	}

	next := prev
	next.source = "upload"
	next.filePath = uploadedPath
	next.mediaKind = uploadedKind
	next.mediaName = uploadedName
	next.layerMode = layerMode
	next.layerPinned = rawLayerMode != ""
	next.explicitEmpty = layerMode == render.LayerBackdrop

	stale, err := s.store.setSessionTrackMedia(sessionID, trackID, next)
	if err != nil {
		cleanupUpload()
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if stale != "" {
		_ = os.Remove(stale)
	}

	writeJSON(w, http.StatusOK, buildTrackMediaResponse(sessionID, trackID, next))
}

func (s *Server) handleSessionTrackMediaGet(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	sessionID := r.PathValue("sessionId")
	trackID := r.PathValue("trackId")

	tr, tm, _, tempDir, err := s.store.getSessionTrackMedia(sessionID, trackID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	wantDefault := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("source")), "default")
	if !wantDefault && tm.explicitEmpty {
		writeError(w, http.StatusNotFound, "track visual media is set to L0 Only")
		return
	}

	effectiveSource := tm.source
	if wantDefault {
		effectiveSource = tm.defaultMediaSource
	} else if effectiveSource == "" || effectiveSource == "inherit" {
		effectiveSource = tm.defaultMediaSource
	}

	var (
		f         *os.File
		serveName string
	)
	switch effectiveSource {
	case "upload":
		if tm.filePath == "" || tempDir == "" {
			writeError(w, http.StatusNotFound, "uploaded Track media unavailable")
			return
		}
		f, err = openFileInDir(tempDir, tm.filePath)
		if err != nil {
			writeError(w, http.StatusNotFound, "uploaded Track media unavailable")
			return
		}
		serveName = sanitizeBaseName(tm.mediaName, filepath.Base(tm.filePath))
	case "local":
		if !s.localPathsAllowed(r) {
			writeError(w, http.StatusForbidden, "local Track media is disabled on non-loopback or --host 0.0.0.0 sessions")
			return
		}
		if tm.filePath == "" {
			writeError(w, http.StatusNotFound, "local Track media unavailable")
			return
		}
		var status int
		f, _, status, err = s.openAllowedFile(tm.filePath)
		if err != nil {
			writeError(w, status, "local Track media unavailable")
			return
		}
		serveName = sanitizeBaseName(tm.mediaName, filepath.Base(tm.filePath))
	case "sidecar":
		if !s.localPathsAllowed(r) {
			writeError(w, http.StatusForbidden, "local sidecar media is disabled on non-loopback or --host 0.0.0.0 sessions")
			return
		}
		if tm.sidecarPath == "" {
			writeError(w, http.StatusNotFound, "sidecar media unavailable")
			return
		}
		var status int
		f, _, status, err = s.openAllowedFile(tm.sidecarPath)
		if err != nil {
			writeError(w, status, "sidecar media unavailable")
			return
		}
		serveName = sanitizeBaseName(tm.sidecarName, filepath.Base(tm.sidecarPath))
	case "id3":
		if !tr.HasEmbeddedArt {
			writeError(w, http.StatusNotFound, "track has no embedded ID3 art")
			return
		}
		artDir, artPath, extractErr := s.ensureSessionTrackExtractedArt(r.Context(), sessionID, trackID, tr, tm)
		if extractErr != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("extract embedded ID3 art: %v", extractErr))
			return
		}
		f, err = openFileInDir(artDir, artPath)
		if err != nil {
			writeError(w, http.StatusNotFound, "extracted ID3 art unavailable")
			return
		}
		serveName = filepath.Base(artPath)
	default:
		writeError(w, http.StatusNotFound, "track has no visual media")
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "track media unavailable")
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, serveName, info.ModTime(), f)
}

func (s *Server) handleSessionTrackMediaDelete(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	sessionID := r.PathValue("sessionId")
	trackID := r.PathValue("trackId")

	_, tm, _, _, err := s.store.getSessionTrackMedia(sessionID, trackID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var next sessionTrackMedia
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("mode")), "backdrop") {
		next = tm
		next.source = "inherit"
		next.filePath = ""
		next.mediaKind = ""
		next.mediaName = ""
		next.layerMode = render.LayerBackdrop
		next.layerPinned = true
		next.explicitEmpty = true
	} else {
		next = resetTrackMediaToDefault(tm, "")
	}

	stale, err := s.store.setSessionTrackMedia(sessionID, trackID, next)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if stale != "" {
		_ = os.Remove(stale)
	}

	writeJSON(w, http.StatusOK, buildTrackMediaResponse(sessionID, trackID, next))
}

func resetTrackMediaToDefault(tm sessionTrackMedia, overrideLayerMode render.TrackLayerMode) sessionTrackMedia {
	next := tm
	next.filePath = ""
	next.explicitEmpty = false
	next.source = tm.defaultMediaSource
	if next.source == "" {
		next.source = "inherit"
	}
	switch next.source {
	case "sidecar":
		next.mediaName = tm.sidecarName
		next.mediaKind = tm.sidecarKind
	case "id3":
		next.mediaName = "Embedded Cover (ID3)"
		next.mediaKind = render.BackgroundKindImage
	default:
		next.mediaName = ""
		next.mediaKind = ""
	}
	if overrideLayerMode != "" {
		next.layerMode = overrideLayerMode
		next.layerPinned = true
	} else {
		next.layerMode = tm.defaultLayerMode
		next.layerPinned = false
	}
	return next
}

func buildTrackMediaResponse(sessionID, trackID string, tm sessionTrackMedia) trackMediaResponse {
	previewURL := ""
	if !tm.explicitEmpty && (tm.source == "upload" || tm.source == "local" || tm.source == "sidecar" || tm.source == "id3") {
		previewURL = fmt.Sprintf("/api/sessions/%s/tracks/%s/media", sessionID, trackID)
	}
	return trackMediaResponse{
		TrackID:       trackID,
		Source:        tm.source,
		MediaName:     tm.mediaName,
		MediaKind:     string(tm.mediaKind),
		LayerMode:     string(tm.layerMode),
		ExplicitEmpty: tm.explicitEmpty,
		PreviewURL:    previewURL,
	}
}

func (s *Server) ensureSessionTrackExtractedArt(ctx context.Context, sessionID, trackID string, tr mixer.Track, tm sessionTrackMedia) (string, string, error) {
	tempDir, err := s.store.ensureSessionTempDir(sessionID)
	if err != nil {
		return "", "", err
	}
	if tm.extractedArtPath != "" {
		if f, openErr := openFileInDir(tempDir, tm.extractedArtPath); openErr == nil {
			_ = f.Close()
			return tempDir, tm.extractedArtPath, nil
		}
	}

	ext := ".jpg"
	if strings.EqualFold(tr.EmbeddedArtCodec, "png") {
		ext = ".png"
	}
	seq := s.store.mediaSeq.Add(1)
	artPath := filepath.Join(tempDir, fmt.Sprintf("id3_art_%d%s", seq, ext))
	tmpFile, err := os.CreateTemp(tempDir, fmt.Sprintf("id3_tmp_%d_*%s", seq, ext))
	if err != nil {
		return "", "", fmt.Errorf("create temp file for ID3 artwork: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := probe.ExtractEmbeddedArt(ctx, tr.Path, tmpPath, s.cfg.Runner); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmpPath, artPath); err != nil {
		return "", "", fmt.Errorf("finalize ID3 artwork %q: %w", artPath, err)
	}
	keepTmp = true
	s.store.setSessionExtractedArtPath(sessionID, trackID, artPath)
	return tempDir, artPath, nil
}

func decodeImageDimensionsFromFile(f *os.File) (int, int) {
	if f == nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

func resolveAutoLayerModeInDir(dir, path string, kind render.BackgroundKind) render.TrackLayerMode {
	if kind != render.BackgroundKindImage || dir == "" || path == "" {
		return render.LayerFullscreen
	}
	f, err := openFileInDir(dir, path)
	if err != nil {
		return render.LayerFullscreen
	}
	defer func() { _ = f.Close() }()
	w, h := decodeImageDimensionsFromFile(f)
	return render.ResolveAutoTrackLayerMode(false, w, h)
}

func (s *Server) resolveAutoLayerModeAllowed(path string, kind render.BackgroundKind) render.TrackLayerMode {
	if kind != render.BackgroundKindImage || path == "" {
		return render.LayerFullscreen
	}
	f, _, _, err := s.openAllowedFile(path)
	if err != nil {
		return render.LayerFullscreen
	}
	defer func() { _ = f.Close() }()
	w, h := decodeImageDimensionsFromFile(f)
	return render.ResolveAutoTrackLayerMode(false, w, h)
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

	tracks, sessMedia, err := s.resolveEstimateTracks(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	bgMode, err := resolveEffectiveBGMode(req.BGMode, req.Layer0Mode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if strings.TrimSpace(req.TrackTransition) != "" {
		if _, err := render.ValidateTrackTransition(render.TrackTransition(req.TrackTransition)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	layoutMode, err := mixer.ValidateTrackMediaLayout(req.TrackMediaLayout)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	isSegmented := resolveIsSegmented(req.Segmented, req.TrackOverrides, sessMedia)

	rawBGPath := strings.TrimSpace(req.BGPath)
	rawBGName := strings.TrimSpace(req.BGName)
	rawViz := strings.TrimSpace(req.Visualizer)
	isBlackMode := bgMode == "black"
	isVisualizerMode := bgMode == "visualizer" || (bgMode == "" && rawBGPath == "" && rawBGName == "" && rawViz != "")

	var (
		pathKind render.Path
		vizCfg   render.VisualizerConfig
		bgMeta   probe.BackgroundMeta
		layer0   render.Layer0Spec
		encName  = "libx264"
	)

	switch {
	case isBlackMode:
		if rawBGPath != "" || rawBGName != "" {
			writeError(w, http.StatusBadRequest, "--bg cannot be used with black backdrop mode")
			return
		}
		if rawViz != "" && !strings.EqualFold(rawViz, "black") {
			writeError(w, http.StatusBadRequest, "black backdrop mode conflicts with non-black visualizer preset")
			return
		}
		vizCfg, err = render.ValidateVisualizerConfig(render.VisualizerConfig{
			Preset:     "black",
			Resolution: req.VisualizerResolution,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		pathKind = render.PathD
		layer0 = render.Layer0Spec{Mode: render.Layer0Black, Visualizer: vizCfg}
	case isVisualizerMode:
		if rawBGPath != "" || rawBGName != "" {
			writeError(w, http.StatusBadRequest, "--bg and --visualizer cannot be used together")
			return
		}
		vizGlow := true
		if req.VisualizerGlow != nil {
			vizGlow = *req.VisualizerGlow
		}
		vizCfg, err = render.ValidateVisualizerConfig(render.VisualizerConfig{
			Preset:     rawViz,
			Theme:      req.VisualizerTheme,
			Resolution: req.VisualizerResolution,
			Glow:       vizGlow,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		pathKind = render.PathD
		if vizCfg.Preset != "black" && s.cfg.Runner != nil {
			encName = s.detectPathDEncoder(r.Context(), vizCfg.Resolution).Name
		}
		l0Mode := render.Layer0Visualizer
		if vizCfg.Preset == "black" {
			l0Mode = render.Layer0Black
		}
		layer0 = render.Layer0Spec{Mode: l0Mode, Visualizer: vizCfg}
	default:
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
			pathKind, err = render.SelectPath(rawBGName, req.OverlayTitles)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			bgMeta = sanitizeBGMeta(req.BGMeta, pathKind)
			if pathKind == render.PathB && s.cfg.Runner != nil {
				encName = s.detectEncoder(r.Context()).Name
			}
			bgKind, _, _ := render.ClassifyBackground(rawBGName)
			layer0 = render.Layer0Spec{Mode: render.Layer0Media, BGPath: rawBGName, BGKind: bgKind}
		} else {
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
			pathKind, err = render.SelectPath(safeBG, req.OverlayTitles)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if (pathKind == render.PathA || pathKind == render.PathB) && s.cfg.Runner != nil {
				if probed, probeErr := probe.ProbeBackgroundVisual(r.Context(), safeBG, s.cfg.Runner); probeErr == nil {
					bgMeta = probed
				}
			}
			if !isSegmented {
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
				return
			}
			bgKind, _, _ := render.ClassifyBackground(safeBG)
			layer0 = render.Layer0Spec{Mode: render.Layer0Media, BGPath: safeBG, BGKind: bgKind}
		}
	}

	if isSegmented {
		segs, segInputs, statusCode, segErr := buildEstimateSegments(
			tracks,
			req.TrackIDs,
			sessMedia,
			req.TrackOverrides,
			layoutMode,
			req.Crossfade,
			req.OverlayTitles,
			s.localPathsAllowed(r),
		)
		if segErr != nil {
			writeError(w, statusCode, segErr.Error())
			return
		}
		uniformFPS := render.ResolveSegmentedFPS(render.SegmentedRenderOptions{
			Layer0:   layer0,
			Segments: segs,
		})
		if uniformFPS > 10 && s.cfg.Runner != nil {
			if layer0.Mode == render.Layer0Visualizer && layer0.Visualizer.Preset != "black" {
				encName = s.detectPathDEncoder(r.Context(), layer0.Visualizer.Resolution).Name
			} else {
				encName = s.detectEncoder(r.Context()).Name
			}
		} else if uniformFPS <= 10 {
			encName = "libx264"
		}
		est := estimateSegmentedMixForTracks(pathKind, encName, req.Crossfade, req.Normalize, req.OverlayTitles, layer0, tracks, bgMeta, segInputs)
		writeJSON(w, http.StatusOK, est)
		return
	}

	est := estimateMixForTracks(pathKind, encName, req.Crossfade, req.Normalize, req.OverlayTitles, vizCfg, tracks, bgMeta)
	writeJSON(w, http.StatusOK, est)
}

func (s *Server) resolveEstimateTracks(req estimateRequest) ([]mixer.Track, []sessionTrackMedia, error) {
	if strings.TrimSpace(req.SessionID) != "" {
		tracks, media, _, _, err := s.store.resolveTracksWithMedia(req.SessionID, req.TrackIDs)
		return tracks, media, err
	}
	if len(req.TrackDurations) == 0 {
		return nil, nil, errors.New("sessionId and trackIds (or trackDurations) are required")
	}
	tracks := make([]mixer.Track, len(req.TrackDurations))
	for i, dur := range req.TrackDurations {
		if math.IsNaN(dur) || math.IsInf(dur, 0) || dur <= 0 {
			return nil, nil, fmt.Errorf("trackDurations[%d] must be positive and finite", i)
		}
		tracks[i] = mixer.Track{
			Duration:     dur,
			DisplayTitle: fmt.Sprintf("Track %d", i+1),
		}
	}
	return tracks, nil, nil
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

func estimateMixWithTracks(
	pathKind render.Path,
	encName string,
	crossfade float64,
	normalize bool,
	overlayTitles bool,
	vizCfg render.VisualizerConfig,
	tracks []mixer.Track,
	bgMeta probe.BackgroundMeta,
	segmented bool,
	layer0 render.Layer0Spec,
	segInputs []render.SegmentEstimateInput,
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
		Segmented: segmented,
		Layer0:    layer0,
		Segments:  segInputs,
	}, cal)
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
	return estimateMixWithTracks(pathKind, encName, crossfade, normalize, overlayTitles, vizCfg, tracks, bgMeta, false, render.Layer0Spec{}, nil)
}

func estimateSegmentedMixForTracks(
	pathKind render.Path,
	encName string,
	crossfade float64,
	normalize bool,
	overlayTitles bool,
	layer0 render.Layer0Spec,
	tracks []mixer.Track,
	bgMeta probe.BackgroundMeta,
	segInputs []render.SegmentEstimateInput,
) render.MixEstimate {
	return estimateMixWithTracks(pathKind, encName, crossfade, normalize, overlayTitles, layer0.Visualizer, tracks, bgMeta, true, layer0, segInputs)
}

func (s *Server) saveSession(source, tempDir string, tracks []mixer.Track) (string, error) {
	sessID, err := newOpaqueID("sess")
	if err != nil {
		return "", err
	}
	trackMap := make(map[string]mixer.Track, len(tracks))
	mediaMap := make(map[string]sessionTrackMedia, len(tracks))
	order := make([]string, 0, len(tracks))
	for i, tr := range tracks {
		tid := fmt.Sprintf("trk_%d", i+1)
		trackMap[tid] = tr
		order = append(order, tid)

		tm := sessionTrackMedia{
			source:             "inherit",
			defaultMediaSource: "inherit",
			defaultLayerMode:   render.LayerBackdrop,
		}
		if source == "local" {
			if sidecarPath, sidecarKind, ok := probe.DiscoverSidecarMedia(tr.Path); ok {
				if safeSidecar, err := s.resolveAllowedPath(sidecarPath, true); err == nil {
					kind := render.BackgroundKind(sidecarKind)
					tm.sidecarPath = safeSidecar
					tm.sidecarName = filepath.Base(safeSidecar)
					tm.sidecarKind = kind
					tm.defaultMediaSource = "sidecar"
					tm.defaultLayerMode = s.resolveAutoLayerModeAllowed(safeSidecar, kind)
				}
			}
		}
		if tm.sidecarPath == "" && tr.HasEmbeddedArt {
			tm.defaultMediaSource = "id3"
			tm.defaultLayerMode = render.LayerArtCard
		}
		mediaMap[tid] = tm
	}
	s.store.addSession(&sessionState{
		id:         sessID,
		source:     source,
		tempDir:    tempDir,
		tracks:     trackMap,
		trackMedia: mediaMap,
		order:      order,
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
		var tm sessionTrackMedia
		if sess.trackMedia != nil {
			tm = sess.trackMedia[tid]
		}
		previewURL := ""
		if tm.defaultMediaSource == "sidecar" || tm.defaultMediaSource == "id3" {
			previewURL = fmt.Sprintf("/api/sessions/%s/tracks/%s/media", sessID, tid)
		}
		dtos = append(dtos, trackDTO{
			ID:                 tid,
			FileName:           tr.FileName,
			DisplayTitle:       tr.DisplayTitle,
			Duration:           tr.Duration,
			HasEmbeddedArt:     tr.HasEmbeddedArt,
			EmbeddedArtWidth:   tr.EmbeddedArtWidth,
			EmbeddedArtHeight:  tr.EmbeddedArtHeight,
			SidecarName:        tm.sidecarName,
			SidecarKind:        string(tm.sidecarKind),
			DefaultMediaSource: tm.defaultMediaSource,
			DefaultLayerMode:   string(tm.defaultLayerMode),
			PreviewURL:         previewURL,
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
