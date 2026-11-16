package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/render"
)

//go:embed assets/*
var embeddedAssets embed.FS

const defaultMaxUploadBytes int64 = 512 << 20 // 512 MiB

// Config configures the Editorial Mix Studio HTTP server.
type Config struct {
	Host           string
	Port           int
	AudioDir       string
	BG             string
	LookPath       func(string) (string, error)
	Runner         mixer.CommandRunner
	GOOS           string
	MaxUploadBytes int64
	MaxJobs        int
}

// Server serves the embedded Editorial Mix Studio web UI and REST/SSE endpoints.
type Server struct {
	cfg      Config
	baseCtx  context.Context
	cancel   context.CancelFunc
	store    *jobStore
	mux      *http.ServeMux
	assetsFS fs.FS
}

type healthResponse struct {
	Status            string         `json:"status"`
	FFmpeg            bool           `json:"ffmpeg"`
	FFprobe           bool           `json:"ffprobe"`
	Encoder           string         `json:"encoder"`
	GOOS              string         `json:"goos"`
	BindHost          string         `json:"bindHost"`
	LocalPathsAllowed bool           `json:"localPathsAllowed"`
	Defaults          healthDefaults `json:"defaults"`
}

type healthDefaults struct {
	AudioDir string `json:"audioDir"`
	BG       string `json:"bg"`
}

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

type renderConfigRequest struct {
	SessionID     string   `json:"sessionId"`
	TrackIDs      []string `json:"trackIds"`
	AudioDir      string   `json:"audioDir"`
	BGPath        string   `json:"bgPath"`
	Out           string   `json:"out"`
	Timestamps    string   `json:"timestamps"`
	Crossfade     float64  `json:"crossfade"`
	Normalize     bool     `json:"normalize"`
	OverlayTitles bool     `json:"overlayTitles"`
	TitleDuration int      `json:"titleDuration"`
	FontName      string   `json:"fontName"`
	Shuffle       bool     `json:"shuffle"`
}

type renderStartResponse struct {
	JobID         string `json:"jobId"`
	RenderingPath string `json:"renderingPath"`
	EventsURL     string `json:"eventsUrl"`
}

// NewServer constructs an Editorial Mix Studio Server with validated defaults.
func NewServer(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.LookPath == nil {
		return nil, errors.New("lookPath function is required")
	}
	if cfg.Runner == nil {
		cfg.Runner = mixer.ExecCommandRunner
	}
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = defaultMaxUploadBytes
	}
	sub, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return nil, fmt.Errorf("load embedded assets: %w", err)
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:      cfg,
		baseCtx:  baseCtx,
		cancel:   cancel,
		store:    newJobStore(cfg.MaxJobs),
		mux:      http.NewServeMux(),
		assetsFS: sub,
	}
	s.registerRoutes()
	return s, nil
}

// Handler returns the root http.Handler for the server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// HTTPServer constructs a hardened stdlib http.Server bound to addr.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

// Close cancels active render jobs and deletes all temporary session and job workspaces.
func (s *Server) Close() {
	s.cancel()
	s.store.closeAll()
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /styles.css", func(w http.ResponseWriter, r *http.Request) {
		s.serveEmbeddedAsset(w, "styles.css", "text/css; charset=utf-8")
	})
	s.mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		s.serveEmbeddedAsset(w, "app.js", "application/javascript; charset=utf-8")
	})
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/preview-bg", s.handlePreviewBG)
	s.mux.HandleFunc("POST /api/probe", s.handleProbe)
	s.mux.HandleFunc("POST /api/render", s.handleRender)
	s.mux.HandleFunc("GET /api/render/{id}/events", s.handleRenderEvents)
	s.mux.HandleFunc("GET /api/render/{id}/output", s.handleRenderOutput)
	s.mux.HandleFunc("GET /api/render/{id}/timestamps", s.handleRenderTimestamps)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	s.serveEmbeddedAsset(w, "index.html", "text/html; charset=utf-8")
}

func (s *Server) serveEmbeddedAsset(w http.ResponseWriter, name, contentType string) {
	data, err := fs.ReadFile(s.assetsFS, name)
	if err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, errFFmpeg := s.cfg.LookPath("ffmpeg")
	_, errFFprobe := s.cfg.LookPath("ffprobe")
	hasFFmpeg := errFFmpeg == nil
	hasFFprobe := errFFprobe == nil

	status := "ok"
	encoder := "unavailable"
	if hasFFmpeg {
		args := render.DetectEncoderArgs(s.cfg.GOOS, func(name string, a ...string) ([]byte, error) {
			return s.cfg.Runner(r.Context(), name, a...)
		})
		encoder = extractEncoderName(args)
	} else {
		status = "degraded"
	}
	if !hasFFprobe {
		status = "degraded"
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:            status,
		FFmpeg:            hasFFmpeg,
		FFprobe:           hasFFprobe,
		Encoder:           encoder,
		GOOS:              s.cfg.GOOS,
		BindHost:          s.cfg.Host,
		LocalPathsAllowed: s.localPathsAllowed(r),
		Defaults: healthDefaults{
			AudioDir: s.cfg.AudioDir,
			BG:       s.cfg.BG,
		},
	})
}

func (s *Server) handlePreviewBG(w http.ResponseWriter, r *http.Request) {
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "local Background Visual preview is disabled on non-loopback or --host 0.0.0.0 sessions")
		return
	}
	bgPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if bgPath == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	if !mixer.IsSupportedBackgroundExt(filepath.Ext(bgPath)) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Background Visual extension %q", filepath.Ext(bgPath)))
		return
	}
	// #nosec G304 -- strictly gated to loopback sessions and supported Background Visual media extensions.
	f, err := os.Open(bgPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("cannot open Background Visual %q: %v", bgPath, err))
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
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "multipart/form-data") {
		s.handleProbeMultipart(w, r)
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

	var uploadedPaths []string
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

		safeName := sanitizeUploadFilename(rawName)
		if !strings.EqualFold(filepath.Ext(safeName), ".mp3") {
			_ = part.Close()
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Track extension %q for %q (expected .mp3)", filepath.Ext(safeName), safeName))
			return
		}

		idx++
		trackSubdir := filepath.Join(sessionDir, fmt.Sprintf("%04d", idx))
		if err := os.MkdirAll(trackSubdir, 0700); err != nil {
			_ = part.Close()
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("create track dir: %v", err))
			return
		}
		dstPath := filepath.Join(trackSubdir, safeName)
		written, err := saveUploadedPart(part, dstPath, s.cfg.MaxUploadBytes-totalWritten)
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
		uploadedPaths = append(uploadedPaths, dstPath)
	}

	if len(uploadedPaths) == 0 {
		writeError(w, http.StatusBadRequest, "no .mp3 Track files uploaded")
		return
	}

	tracks, err := mixer.ProbeTrackFiles(r.Context(), uploadedPaths, s.cfg.Runner)
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

	tracks, err := mixer.ProbeTracks(r.Context(), req.AudioDir, req.Shuffle, nil, s.cfg.Runner)
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
	sess := s.store.sessions[sessID]
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
	s.store.mu.Unlock()
	return probeResponse{
		SessionID: sessID,
		Source:    source,
		Tracks:    dtos,
	}
}

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
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
	bgPath := uploadedBGPath
	if bgPath == "" {
		if !localAllowed {
			writeError(w, http.StatusForbidden, "local Background Visual paths are disabled on non-loopback or --host 0.0.0.0 sessions; upload a Background Visual file instead")
			return
		}
		bgPath = strings.TrimSpace(req.BGPath)
	}

	var tracks []mixer.Track
	if strings.TrimSpace(req.SessionID) != "" {
		tracks, err = s.store.resolveTracks(req.SessionID, req.TrackIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if localAllowed && strings.TrimSpace(req.AudioDir) != "" {
		tracks, err = mixer.ProbeTracks(r.Context(), req.AudioDir, req.Shuffle, nil, s.cfg.Runner)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "sessionId and trackIds are required (probe Tracks first)")
		return
	}

	outName := sanitizeArtifactName(req.Out, "output.mp4")
	tsName := sanitizeArtifactName(req.Timestamps, "timestamps.txt")
	outPath := filepath.Join(jobDir, outName)
	tsPath := filepath.Join(jobDir, tsName)
	if localAllowed {
		if trimmedOut := strings.TrimSpace(req.Out); trimmedOut != "" {
			outPath = trimmedOut
		}
		if trimmedTS := strings.TrimSpace(req.Timestamps); trimmedTS != "" {
			tsPath = trimmedTS
		}
	}

	fontName := strings.TrimSpace(req.FontName)
	if fontName == "" {
		fontName = "Arial"
	}

	mixCfg := mixer.Config{
		AudioDir:      req.AudioDir,
		BG:            bgPath,
		Out:           outPath,
		Timestamps:    tsPath,
		Crossfade:     req.Crossfade,
		Normalize:     req.Normalize,
		OverlayTitles: req.OverlayTitles,
		TitleDuration: req.TitleDuration,
		FontName:      fontName,
		Shuffle:       req.Shuffle,
	}

	if err := mixer.ValidateConfig(mixCfg, s.cfg.LookPath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	bgInfo, err := os.Stat(mixCfg.BG)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot access Background Visual %q: %v", mixCfg.BG, err))
		return
	}
	if bgInfo.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("background visual path %q is a directory, expected a file", mixCfg.BG))
		return
	}

	pathKind, err := render.SelectPath(mixCfg.BG, mixCfg.OverlayTitles)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	jobID, err := newOpaqueID("job")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jobCtx, jobCancel := context.WithCancel(s.baseCtx)
	job := newRenderJob(jobID, jobDir, outPath, filepath.Base(outPath), tsPath, filepath.Base(tsPath), string(pathKind), jobCancel)
	if err := s.store.claimRenderSlot(job); err != nil {
		jobCancel()
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	keepJobDir = true

	go s.runRenderJob(jobCtx, job, mixCfg, tracks)

	writeJSON(w, http.StatusAccepted, renderStartResponse{
		JobID:         jobID,
		RenderingPath: string(pathKind),
		EventsURL:     fmt.Sprintf("/api/render/%s/events", jobID),
	})
}

func (s *Server) parseRenderRequest(w http.ResponseWriter, r *http.Request, jobDir string) (renderConfigRequest, string, int, error) {
	var req renderConfigRequest
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(ct, "multipart/form-data") {
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
			safeName := sanitizeUploadFilename(part.FileName())
			if !mixer.IsSupportedBackgroundExt(filepath.Ext(safeName)) {
				_ = part.Close()
				return req, "", http.StatusBadRequest, fmt.Errorf("unsupported Background Visual extension %q (supported: .jpg, .jpeg, .png, .webp, .mp4, .mov, .gif)", filepath.Ext(safeName))
			}
			dstPath := filepath.Join(jobDir, "bg_"+safeName)
			_, err := saveUploadedPart(part, dstPath, s.cfg.MaxUploadBytes)
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

	logWriter := &sseLogWriter{job: job}
	err := mixer.RunWithTracks(ctx, cfg, tracks, logWriter, s.cfg.LookPath, s.cfg.Runner)
	logWriter.flushRemaining()
	if err != nil {
		job.finish(jobFailed, err.Error(), "error", map[string]any{
			"jobId":   job.id,
			"message": err.Error(),
		})
		return
	}

	// #nosec G304 -- reads the Chapter Timestamps file produced by this job's validated mixer run.
	tsBytes, readErr := os.ReadFile(job.tsPath)
	tsText := ""
	if readErr == nil {
		tsText = string(tsBytes)
	}

	job.finish(jobComplete, "", "complete", map[string]any{
		"jobId":          job.id,
		"renderingPath":  job.renderingPath,
		"timestampsText": tsText,
		"outputUrl":      fmt.Sprintf("/api/render/%s/output", job.id),
		"timestampsUrl":  fmt.Sprintf("/api/render/%s/timestamps", job.id),
		"outputName":     job.outName,
		"timestampsName": job.tsName,
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
		writeSSEEvent(w, ev)
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
			writeSSEEvent(w, ev)
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
	job.mu.Unlock()

	if status == jobRunning {
		writeError(w, http.StatusConflict, "render job is still in progress")
		return
	}
	if status == jobFailed {
		writeError(w, http.StatusConflict, fmt.Sprintf("render job failed: %s", errMsg))
		return
	}

	filePath := job.tsPath
	fileName := job.tsName
	contentType := "text/plain; charset=utf-8"
	if isOutput {
		filePath = job.outPath
		fileName = job.outName
		contentType = "video/mp4"
	}

	// #nosec G304 -- serves artifact produced by a completed server-side renderJob.
	f, err := os.Open(filePath)
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

func (s *Server) localPathsAllowed(r *http.Request) bool {
	return isLoopbackHost(s.cfg.Host) && isLoopbackRemoteAddr(r.RemoteAddr)
}

func isLoopbackHost(host string) bool {
	h := strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host := strings.TrimSpace(remoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return isLoopbackHost(host)
}

func extractEncoderName(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c:v" {
			return args[i+1]
		}
	}
	return "libx264"
}

func sanitizeUploadFilename(raw string) string {
	cleaned := strings.ReplaceAll(raw, `\`, "/")
	base := filepath.Base(cleaned)
	base = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' || r == ':' || r == '"' {
			return '_'
		}
		return r
	}, base)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." {
		return "upload.bin"
	}
	return base
}

func sanitizeArtifactName(raw, fallback string) string {
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	base := filepath.Base(cleaned)
	base = sanitizeHeaderFilename(base)
	if base == "" || base == "." || base == ".." {
		return fallback
	}
	return base
}

func sanitizeHeaderFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, name)
}

func saveUploadedPart(part *multipart.Part, dstPath string, remainingLimit int64) (int64, error) {
	if remainingLimit <= 0 {
		return 0, errors.New("upload exceeds maximum allowed size")
	}
	// #nosec G304 -- dstPath is constructed strictly inside server-owned tempDir with sanitized base filename.
	f, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	limited := io.LimitReader(part, remainingLimit+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		return n, err
	}
	if n > remainingLimit {
		return n, errors.New("upload exceeds maximum allowed size")
	}
	return n, nil
}

func isPayloadTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr) || strings.Contains(err.Error(), "exceeds maximum allowed size")
}

func writeSSEEvent(w io.Writer, ev sseEvent) {
	fmt.Fprintf(w, "event: %s\n", ev.name)
	for _, line := range strings.Split(ev.data, "\n") {
		fmt.Fprintf(w, "data: %s\n", line)
	}
	fmt.Fprintln(w)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
