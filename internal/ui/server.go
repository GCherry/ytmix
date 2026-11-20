package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/render"
)

//go:embed assets/*
var embeddedAssets embed.FS

const defaultMaxUploadBytes int64 = 512 << 20 // 512 MiB

// Config configures the Editorial Mix Studio HTTP server.
type Config struct {
	Host            string
	Port            int
	WorkspaceDir    string
	AudioDir        string
	BG              string
	OutDir          string
	Out             string
	Timestamps      string
	AllowDirs       []string
	IncludeHomeRoot bool
	LookPath        func(string) (string, error)
	Runner          mixer.CommandRunner
	StreamRunner    mixer.StreamRunner
	PickPath        PickPathFunc
	GOOS            string
	MaxUploadBytes  int64
	MaxJobs         int
}

// Server serves the embedded Editorial Mix Studio web UI and REST/SSE endpoints.
type Server struct {
	cfg           Config
	workspaceRoot string
	homeDir       string
	rootsMu       sync.RWMutex
	allowedRoots  []string
	quickRoots    []fsRootDTO
	defaultOutDir string
	pickActive    atomic.Bool
	baseCtx       context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	store         *jobStore
	mux           *http.ServeMux
	assetsFS      fs.FS
	encoderOnce   sync.Once
	cachedEncoder render.EncoderInfo
	filtersOnce   sync.Once
	cachedFilters map[string]bool
}

type fsRootDTO struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type fsEntryDTO struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type fsListResponse struct {
	Current string       `json:"current"`
	Parent  string       `json:"parent"`
	Roots   []fsRootDTO  `json:"roots"`
	Entries []fsEntryDTO `json:"entries"`
}

type fsMkdirRequest struct {
	ParentDir string `json:"parentDir"`
	Name      string `json:"name"`
}

type visualizerDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Filter      string `json:"filter"`
	Category    string `json:"category"`
	Description string `json:"description"`
	IsDark      bool   `json:"isDark"`
	Available   bool   `json:"available"`
}

type healthResponse struct {
	Status                  string                  `json:"status"`
	FFmpeg                  bool                    `json:"ffmpeg"`
	FFprobe                 bool                    `json:"ffprobe"`
	Encoder                 string                  `json:"encoder"`
	GOOS                    string                  `json:"goos"`
	BindHost                string                  `json:"bindHost"`
	LocalPathsAllowed       bool                    `json:"localPathsAllowed"`
	SupportedBackgroundExts render.BackgroundExts   `json:"supportedBackgroundExts"`
	Visualizers             []visualizerDTO         `json:"visualizers"`
	VisualizerThemes        []render.ThemeSpec      `json:"visualizerThemes"`
	VisualizerResolutions   []render.ResolutionSpec `json:"visualizerResolutions"`
	Defaults                healthDefaults          `json:"defaults"`
	Roots                   []fsRootDTO             `json:"roots,omitempty"`
}

type healthDefaults struct {
	AudioDir   string `json:"audioDir"`
	BG         string `json:"bg"`
	OutDir     string `json:"outDir"`
	Out        string `json:"out"`
	Timestamps string `json:"timestamps"`
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
	SessionID            string   `json:"sessionId"`
	TrackIDs             []string `json:"trackIds"`
	AudioDir             string   `json:"audioDir"`
	BGMode               string   `json:"bgMode"`
	BGPath               string   `json:"bgPath"`
	OutDir               string   `json:"outDir"`
	Out                  string   `json:"out"`
	Timestamps           string   `json:"timestamps"`
	Crossfade            float64  `json:"crossfade"`
	Normalize            bool     `json:"normalize"`
	OverlayTitles        bool     `json:"overlayTitles"`
	TitleDuration        int      `json:"titleDuration"`
	FontName             string   `json:"fontName"`
	Shuffle              bool     `json:"shuffle"`
	Visualizer           string   `json:"visualizer"`
	VisualizerTheme      string   `json:"visualizerTheme"`
	VisualizerResolution string   `json:"visualizerResolution"`
	VisualizerGlow       *bool    `json:"visualizerGlow"`
	VisualizerBrightness *int     `json:"visualizerBrightness"`
	FadeToBlackMinutes   float64  `json:"fadeToBlackMinutes"`
	DimTitles            bool     `json:"dimTitles"`
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
	if strings.TrimSpace(cfg.Out) == "" {
		cfg.Out = "output.mp4"
	}
	if strings.TrimSpace(cfg.Timestamps) == "" {
		cfg.Timestamps = "timestamps.txt"
	}
	if cfg.LookPath == nil {
		return nil, errors.New("lookPath function is required")
	}
	if cfg.Runner == nil {
		cfg.Runner = mixer.ExecCommandRunner
		if cfg.StreamRunner == nil {
			cfg.StreamRunner = mixer.ExecStreamRunner
		}
	}
	if cfg.GOOS == "" {
		cfg.GOOS = runtime.GOOS
	}
	if cfg.PickPath == nil {
		cfg.PickPath = newDefaultPickPath(cfg.GOOS, cfg.LookPath)
	}
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = defaultMaxUploadBytes
	}
	wsRoot := strings.TrimSpace(cfg.WorkspaceDir)
	if wsRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		wsRoot = wd
	}
	absRoot, err := canonicalizeCandidatePath(wsRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}

	var homeDir string
	if cfg.IncludeHomeRoot {
		if rawHome, err := os.UserHomeDir(); err == nil && strings.TrimSpace(rawHome) != "" {
			if canonicalHome, err := canonicalizeCandidatePath(rawHome); err == nil {
				homeDir = canonicalHome
			}
		}
	}

	sub, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return nil, fmt.Errorf("load embedded assets: %w", err)
	}

	baseCtx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:           cfg,
		workspaceRoot: absRoot,
		homeDir:       homeDir,
		baseCtx:       baseCtx,
		cancel:        cancel,
		store:         newJobStore(cfg.MaxJobs),
		mux:           http.NewServeMux(),
		assetsFS:      sub,
	}

	s.addAllowedRoot("Workspace", absRoot)
	if homeDir != "" {
		s.addAllowedRoot("Home", homeDir)
	}
	if trimmedOutDir := strings.TrimSpace(cfg.OutDir); trimmedOutDir != "" {
		resolvedOutDir, err := s.canonicalizeFromConfig(trimmedOutDir)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("resolve --out-dir: %w", err)
		}
		s.defaultOutDir = resolvedOutDir
		s.addAllowedRoot("Output Dir", resolvedOutDir)
	}
	for _, extra := range cfg.AllowDirs {
		if strings.TrimSpace(extra) == "" {
			continue
		}
		resolvedExtra, err := s.canonicalizeFromConfig(extra)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("resolve --allow-dir %q: %w", extra, err)
		}
		s.addAllowedRoot(filepath.Base(resolvedExtra), resolvedExtra)
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
	s.wg.Wait()
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
	s.mux.HandleFunc("GET /api/fs/list", s.handleFSList)
	s.mux.HandleFunc("POST /api/fs/mkdir", s.handleFSMkdir)
	s.mux.HandleFunc("POST /api/fs/pick", s.handleFSPick)
	s.mux.HandleFunc("POST /api/probe", s.handleProbe)
	s.mux.HandleFunc("GET /api/sessions/{sessionId}/tracks/{trackId}/audio", s.handleSessionTrackAudio)
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

func (s *Server) detectEncoder(ctx context.Context) render.EncoderInfo {
	s.encoderOnce.Do(func() {
		probeCtx := s.baseCtx
		if probeCtx == nil {
			probeCtx = ctx
		}
		s.cachedEncoder = render.DetectEncoder(probeCtx, s.cfg.GOOS, s.cfg.Runner)
	})
	return render.EncoderInfo{
		Name: s.cachedEncoder.Name,
		Args: append([]string(nil), s.cachedEncoder.Args...),
	}
}

func (s *Server) detectFilters(ctx context.Context) map[string]bool {
	s.filtersOnce.Do(func() {
		probeCtx := s.baseCtx
		if probeCtx == nil {
			probeCtx = ctx
		}
		filters, err := render.DetectFilters(probeCtx, s.cfg.Runner)
		if err != nil {
			return
		}
		s.cachedFilters = filters
	})
	cloned := make(map[string]bool, len(s.cachedFilters))
	for k, v := range s.cachedFilters {
		cloned[k] = v
	}
	return cloned
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, errFFmpeg := s.cfg.LookPath("ffmpeg")
	_, errFFprobe := s.cfg.LookPath("ffprobe")
	hasFFmpeg := errFFmpeg == nil
	hasFFprobe := errFFprobe == nil

	status := "ok"
	encoder := "unavailable"
	var availFilters map[string]bool
	if hasFFmpeg {
		encoder = s.detectEncoder(r.Context()).Name
		availFilters = s.detectFilters(r.Context())
	} else {
		status = "degraded"
	}
	if !hasFFprobe {
		status = "degraded"
	}

	specs := render.ListVisualizers()
	vizDTOs := make([]visualizerDTO, len(specs))
	for i, spec := range specs {
		ok := false
		if hasFFmpeg {
			ok, _ = render.PresetAvailable(spec.ID, availFilters)
		}
		vizDTOs[i] = visualizerDTO{
			ID:          spec.ID,
			Name:        spec.Name,
			Filter:      spec.Filter,
			Category:    spec.Category,
			Description: spec.Description,
			IsDark:      spec.IsDark,
			Available:   ok,
		}
	}

	localAllowed := s.localPathsAllowed(r)
	var roots []fsRootDTO
	var audioDirDefault, bgDefault, outDirDefault string
	if localAllowed {
		roots = s.snapshotQuickRoots()
		audioDirDefault = s.cfg.AudioDir
		bgDefault = s.cfg.BG
		outDirDefault = s.defaultOutDir
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:                  status,
		FFmpeg:                  hasFFmpeg,
		FFprobe:                 hasFFprobe,
		Encoder:                 encoder,
		GOOS:                    s.cfg.GOOS,
		BindHost:                s.cfg.Host,
		LocalPathsAllowed:       localAllowed,
		SupportedBackgroundExts: render.SupportedBackgroundExts(),
		Visualizers:             vizDTOs,
		VisualizerThemes:        render.ListThemes(),
		VisualizerResolutions:   render.ListResolutions(),
		Defaults: healthDefaults{
			AudioDir:   audioDirDefault,
			BG:         bgDefault,
			OutDir:     outDirDefault,
			Out:        s.cfg.Out,
			Timestamps: s.cfg.Timestamps,
		},
		Roots: roots,
	})
}

func (s *Server) handleSessionTrackAudio(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	tr, err := s.store.resolveTrack(r.PathValue("sessionId"), r.PathValue("trackId"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !strings.EqualFold(filepath.Ext(tr.Path), ".mp3") {
		writeError(w, http.StatusBadRequest, "unsupported Track extension")
		return
	}
	// #nosec G304 G703 -- tr.Path is resolved strictly from server-owned session state via opaque sessionId/trackId tokens.
	f, err := os.Open(tr.Path)
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
	bgPath, err := s.resolveAllowedPath(rawPath, true)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// #nosec G304 G703 -- bgPath is confined to s.allowedRoots via resolveAllowedPath and restricted to supported Background Visual extensions.
	f, err := os.Open(bgPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("cannot open Background Visual: %v", err))
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
		dstPath := filepath.Join(sessionDir, fmt.Sprintf("track_%04d.mp3", idx))
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
	bgMode := strings.ToLower(strings.TrimSpace(req.BGMode))
	switch bgMode {
	case "", "upload", "local", "visualizer":
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported bgMode %q (expected upload, local, or visualizer)", req.BGMode))
		return
	}

	isVisualizerMode := bgMode == "visualizer" || (bgMode == "" && uploadedBGPath == "" && strings.TrimSpace(req.BGPath) == "" && strings.TrimSpace(req.Visualizer) != "")

	bgPath := uploadedBGPath
	if isVisualizerMode {
		if bgPath != "" || strings.TrimSpace(req.BGPath) != "" {
			writeError(w, http.StatusBadRequest, "--bg and --visualizer cannot be used together")
			return
		}
		bgPath = ""
	} else {
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

	var tracks []mixer.Track
	if strings.TrimSpace(req.SessionID) != "" {
		tracks, err = s.store.resolveTracks(req.SessionID, req.TrackIDs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if localAllowed && safeAudioDir != "" {
		tracks, err = mixer.ProbeTracks(r.Context(), safeAudioDir, req.Shuffle, nil, s.cfg.Runner)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "sessionId and trackIds are required (probe Tracks first)")
		return
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
		Visualizer:           req.Visualizer,
		VisualizerTheme:      req.VisualizerTheme,
		VisualizerResolution: req.VisualizerResolution,
		VisualizerGlow:       vizGlow,
		VisualizerBrightness: vizBrightness,
		FadeToBlack:          fadeToBlack,
		DimTitles:            req.DimTitles,
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
	})
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
			dstPath := filepath.Join(jobDir, "bg_upload"+literalExt)
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

	var mu sync.Mutex
	lastPct := -1
	var lastProgressAt time.Time
	sink := func(ev mixer.TelemetryEvent) {
		switch ev.Kind {
		case mixer.EventStep:
			job.emit("step", map[string]any{
				"step":     ev.Step,
				"total":    ev.TotalSteps,
				"message":  ev.Message,
				"detail":   ev.Detail,
				"progress": ev.Progress,
			})
		case mixer.EventProgress:
			mu.Lock()
			now := time.Now()
			shouldEmit := ev.Progress != lastPct && (lastPct < 0 || ev.Progress == 99 || ev.Progress-lastPct >= 5 || now.Sub(lastProgressAt) >= 250*time.Millisecond)
			if shouldEmit {
				lastPct = ev.Progress
				lastProgressAt = now
			}
			mu.Unlock()
			if !shouldEmit {
				return
			}
			job.emitProgress(map[string]any{
				"step":             ev.Step,
				"total":            ev.TotalSteps,
				"progress":         ev.Progress,
				"substage":         ev.Substage,
				"outTimeSec":       ev.OutTimeSec,
				"totalDurationSec": ev.TotalDurationSec,
				"speed":            ev.Speed,
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

	// #nosec G304 G703 -- reads the Chapter Timestamps file produced by this job's validated mixer run.
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

	// #nosec G304 G703 -- serves artifact produced by a completed server-side renderJob.
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

func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "local filesystem browsing is disabled on non-loopback or --host 0.0.0.0 sessions")
		return
	}

	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		rawPath = s.defaultBrowseDir()
	}
	dirPath, err := s.resolveAllowedPath(rawPath, true)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// #nosec G304 G703 -- dirPath is confined to s.allowedRoots via resolveAllowedPath.
	info, err := os.Stat(dirPath)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("cannot access directory %q: %v", rawPath, err))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("path %q is not a directory", rawPath))
		return
	}

	// #nosec G304 G703 -- dirPath is confined to s.allowedRoots via resolveAllowedPath.
	dirEntries, err := os.ReadDir(dirPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read directory %q: %v", rawPath, err))
		return
	}

	entries := make([]fsEntryDTO, 0, len(dirEntries))
	for _, entry := range dirEntries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		entries = append(entries, fsEntryDTO{
			Name: name,
			Path: filepath.Join(dirPath, name),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	parent := filepath.Dir(dirPath)
	if parent == dirPath || !s.isWithinAnyAllowedRoot(parent) {
		parent = ""
	}

	writeJSON(w, http.StatusOK, fsListResponse{
		Current: dirPath,
		Parent:  parent,
		Roots:   s.snapshotQuickRoots(),
		Entries: entries,
	})
}

type fsPickRequest struct {
	Kind        string `json:"kind"`
	InitialPath string `json:"initialPath"`
}

type fsPickResponse struct {
	Cancelled bool   `json:"cancelled"`
	Path      string `json:"path,omitempty"`
}

func (s *Server) handleFSPick(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "native OS folder picking is disabled on non-loopback or --host 0.0.0.0 sessions")
		return
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	var req fsPickRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	kind := strings.TrimSpace(req.Kind)
	if kind != pickKindFolder && kind != pickKindBGFile {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid picker kind %q", req.Kind))
		return
	}

	if !s.pickActive.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a native folder or file picker dialog is already open")
		return
	}
	defer s.pickActive.Store(false)

	pickCtx, cancel := context.WithCancel(s.baseCtx)
	defer cancel()
	stopCancel := context.AfterFunc(r.Context(), cancel)
	defer stopCancel()

	initDir := s.resolvePickerInitialDir(req.InitialPath)
	picked, err := s.cfg.PickPath(pickCtx, kind, initDir)
	if err != nil {
		if errors.Is(err, ErrPickerCancelled) {
			writeJSON(w, http.StatusOK, fsPickResponse{Cancelled: true})
			return
		}
		if errors.Is(err, ErrPickerUnavailable) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":    err.Error(),
				"fallback": true,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("native picker failed: %v", err))
		return
	}
	if strings.TrimSpace(picked) == "" {
		writeJSON(w, http.StatusOK, fsPickResponse{Cancelled: true})
		return
	}

	canonicalPath, err := canonicalizeCandidatePath(picked)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid picked path %q: %v", picked, err))
		return
	}
	// #nosec G304 G703 -- canonicalPath originates from the loopback native OS picker dialog and is canonicalized via canonicalizeCandidatePath.
	info, err := os.Stat(canonicalPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("cannot access picked path %q: %v", canonicalPath, err))
		return
	}

	var rootDir string
	if kind == pickKindFolder {
		if !info.IsDir() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("picked path %q is not a directory", canonicalPath))
			return
		}
		rootDir = canonicalPath
	} else {
		if info.IsDir() {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("picked path %q is a directory, not a Background Visual file", canonicalPath))
			return
		}
		if _, ok := render.CanonicalBackgroundExt(filepath.Ext(canonicalPath)); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported Background Visual extension %q", filepath.Ext(canonicalPath)))
			return
		}
		rootDir = filepath.Dir(canonicalPath)
	}

	s.addAllowedRoot(friendlyRootLabel(rootDir), rootDir)
	writeJSON(w, http.StatusOK, fsPickResponse{
		Cancelled: false,
		Path:      canonicalPath,
	})
}

func (s *Server) resolvePickerInitialDir(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return s.defaultBrowseDir()
	}
	if resolved, err := s.resolveAllowedPath(trimmed, true); err == nil {
		// #nosec G304 G703 -- resolved is confined to s.allowedRoots via resolveAllowedPath.
		if info, statErr := os.Stat(resolved); statErr == nil {
			if info.IsDir() {
				return resolved
			}
			return filepath.Dir(resolved)
		}
	}
	if resolvedParent, err := s.resolveAllowedPath(filepath.Dir(trimmed), true); err == nil {
		// #nosec G304 G703 -- resolvedParent is confined to s.allowedRoots via resolveAllowedPath.
		if info, statErr := os.Stat(resolvedParent); statErr == nil && info.IsDir() {
			return resolvedParent
		}
	}
	return s.defaultBrowseDir()
}

func friendlyRootLabel(dir string) string {
	base := filepath.Base(dir)
	if base == "" || base == "." || base == string(filepath.Separator) {
		if vol := filepath.VolumeName(dir); vol != "" {
			return vol
		}
		return dir
	}
	return base
}

func (s *Server) handleFSMkdir(w http.ResponseWriter, r *http.Request) {
	if err := requireSameOrigin(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if !s.localPathsAllowed(r) {
		writeError(w, http.StatusForbidden, "local filesystem creation is disabled on non-loopback or --host 0.0.0.0 sessions")
		return
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	var req fsMkdirRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %v", err))
		return
	}
	folderName := strings.TrimSpace(req.Name)
	if !isValidSingleFolderName(folderName) {
		writeError(w, http.StatusBadRequest, "folder name must be a single non-empty directory name without path separators")
		return
	}
	rawParent := strings.TrimSpace(req.ParentDir)
	if rawParent == "" {
		rawParent = s.defaultBrowseDir()
	}
	parentDir, err := s.resolveAllowedPath(rawParent, true)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// #nosec G304 G703 -- parentDir is confined to s.allowedRoots via resolveAllowedPath.
	parentInfo, err := os.Stat(parentDir)
	if err != nil || !parentInfo.IsDir() {
		writeError(w, http.StatusBadRequest, "parentDir must be an existing directory")
		return
	}

	targetDir, err := s.resolveAllowedPath(filepath.Join(parentDir, folderName), false)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// #nosec G703 -- targetDir is a validated single segment inside a confined allowedRoot.
	if _, err := os.Stat(targetDir); err == nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("folder %q already exists", folderName))
		return
	}
	// #nosec G301 G703 -- user-facing output directory uses standard 0755 permissions inside a confined allowedRoot.
	if err := os.Mkdir(targetDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create folder %q: %v", folderName, err))
		return
	}

	writeJSON(w, http.StatusCreated, fsEntryDTO{
		Name: folderName,
		Path: targetDir,
	})
}

func isValidSingleFolderName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\:`) || strings.ContainsRune(name, '\x00') {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	return true
}

func (s *Server) defaultBrowseDir() string {
	if s.defaultOutDir != "" {
		if info, err := os.Stat(s.defaultOutDir); err == nil && info.IsDir() {
			return s.defaultOutDir
		}
	}
	return s.workspaceRoot
}

func (s *Server) snapshotQuickRoots() []fsRootDTO {
	s.rootsMu.RLock()
	defer s.rootsMu.RUnlock()
	return append([]fsRootDTO(nil), s.quickRoots...)
}

func (s *Server) addAllowedRoot(label, canonicalPath string) {
	if canonicalPath == "" {
		return
	}
	s.rootsMu.Lock()
	defer s.rootsMu.Unlock()
	for _, existing := range s.allowedRoots {
		if existing == canonicalPath {
			return
		}
	}
	if strings.TrimSpace(label) == "" {
		label = canonicalPath
	}
	s.allowedRoots = append(s.allowedRoots, canonicalPath)
	s.quickRoots = append(s.quickRoots, fsRootDTO{
		Label: label,
		Path:  canonicalPath,
	})
}

func (s *Server) canonicalizeFromConfig(raw string) (string, error) {
	expanded, err := s.expandHomePrefix(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(s.workspaceRoot, expanded)
	}
	return canonicalizeCandidatePath(expanded)
}

func canonicalizeCandidatePath(raw string) (string, error) {
	absPath, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", err
	}
	return resolveWithExistingAncestor(absPath)
}

func resolveWithExistingAncestor(absPath string) (string, error) {
	if realPath, err := filepath.EvalSymlinks(absPath); err == nil {
		return realPath, nil
	}
	var tail []string
	cur := absPath
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return absPath, nil
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{realParent}, tail...)
			return filepath.Join(parts...), nil
		}
		cur = parent
	}
}

func (s *Server) resolveRenderOutputTarget(safeOutDir, raw, fallback string) (string, int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = fallback
	}
	if strings.HasSuffix(trimmed, "/") || strings.HasSuffix(trimmed, `\`) {
		return "", http.StatusBadRequest, fmt.Errorf("output path %q must be a file path, not a directory", trimmed)
	}
	joined, err := s.joinWithOutDir(safeOutDir, trimmed)
	if err != nil {
		return "", http.StatusForbidden, err
	}
	resolved, err := s.resolveAllowedPath(joined, false)
	if err != nil {
		return "", http.StatusForbidden, err
	}
	return resolved, http.StatusOK, nil
}

func (s *Server) expandHomePrefix(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "~" {
		if s.homeDir == "" {
			return "", errors.New("home directory access is not enabled")
		}
		return s.homeDir, nil
	}
	if strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `~\`) {
		if s.homeDir == "" {
			return "", errors.New("home directory access is not enabled")
		}
		return filepath.Join(s.homeDir, trimmed[2:]), nil
	}
	return trimmed, nil
}

func (s *Server) joinWithOutDir(safeOutDir, rawTarget string) (string, error) {
	expanded, err := s.expandHomePrefix(rawTarget)
	if err != nil {
		return "", err
	}
	if safeOutDir != "" && !filepath.IsAbs(expanded) {
		return filepath.Join(safeOutDir, expanded), nil
	}
	return expanded, nil
}

func (s *Server) isWithinAnyAllowedRoot(target string) bool {
	s.rootsMu.RLock()
	roots := append([]string(nil), s.allowedRoots...)
	s.rootsMu.RUnlock()
	for _, root := range roots {
		if isPathWithinRoot(root, target) {
			return true
		}
	}
	return false
}

func isPathWithinRoot(root, target string) bool {
	sep := string(filepath.Separator)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+sep) || filepath.IsAbs(rel) {
		return false
	}
	if target == root {
		return true
	}
	if strings.HasSuffix(root, sep) {
		return strings.HasPrefix(target, root)
	}
	return strings.HasPrefix(target, root+sep)
}

func (s *Server) resolveAllowedPath(raw string, mustExist bool) (string, error) {
	cleanedInput := strings.TrimSpace(raw)
	if cleanedInput == "" || strings.ContainsRune(cleanedInput, '\x00') {
		return "", errors.New("path must not be empty")
	}
	expanded, err := s.expandHomePrefix(cleanedInput)
	if err != nil {
		return "", err
	}

	var candidate string
	if filepath.IsAbs(expanded) {
		candidate = filepath.Clean(expanded)
	} else {
		candidate = filepath.Join(s.workspaceRoot, expanded)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	var target string
	if mustExist {
		realTarget, err := filepath.EvalSymlinks(absCandidate)
		if err != nil {
			return "", fmt.Errorf("cannot resolve path %q: %w", raw, err)
		}
		target = realTarget
	} else {
		resolved, err := resolveWithExistingAncestor(absCandidate)
		if err != nil {
			return "", fmt.Errorf("cannot resolve path %q: %w", raw, err)
		}
		target = resolved
	}

	// Keep containment barriers inline here so static analysis (CodeQL/gosec) can verify
	// prefix and relative-path confinement directly before any post-resolution syscalls.
	s.rootsMu.RLock()
	roots := append([]string(nil), s.allowedRoots...)
	s.rootsMu.RUnlock()
	sep := string(filepath.Separator)
	for _, root := range roots {
		if target == root {
			return root, nil
		}
		basePrefix := strings.TrimSuffix(root, sep)
		if !strings.HasPrefix(target, basePrefix+sep) {
			continue
		}
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+sep) || filepath.IsAbs(rel) {
			continue
		}
		if !mustExist {
			cur := root
			for _, seg := range strings.Split(rel, sep) {
				if seg == "" || seg == "." {
					continue
				}
				if seg == ".." {
					return "", fmt.Errorf("path %q escapes allowed roots", raw)
				}
				cur = filepath.Join(cur, seg)
				if !strings.HasPrefix(cur, basePrefix+sep) {
					return "", fmt.Errorf("path %q escapes allowed roots", raw)
				}
				// #nosec G703 -- cur is confined to allowedRoot via strings.HasPrefix and rel traversal checks above.
				if info, err := os.Lstat(cur); err == nil && info.Mode()&os.ModeSymlink != 0 {
					if _, evalErr := filepath.EvalSymlinks(cur); evalErr != nil {
						return "", fmt.Errorf("dangling symlink %q is not permitted", raw)
					}
				}
			}
		}
		return target, nil
	}
	return "", fmt.Errorf("path %q escapes allowed roots", raw)
}

func requireSameOrigin(r *http.Request) error {
	if sfs := strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")); sfs != "" {
		if !strings.EqualFold(sfs, "same-origin") && !strings.EqualFold(sfs, "none") {
			return fmt.Errorf("cross-origin request blocked (Sec-Fetch-Site=%q)", sfs)
		}
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, r.Host) {
			return fmt.Errorf("cross-origin request blocked (Origin=%q)", origin)
		}
	}
	return nil
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

func sanitizeBaseName(raw, fallback string) string {
	cleaned := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	base := filepath.Base(cleaned)
	base = strings.TrimSpace(sanitizeHeaderFilename(strings.ReplaceAll(base, ":", "_")))
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
	// #nosec G304 G703 -- dstPath is constructed strictly inside server-owned tempDir using fixed literal filenames.
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
