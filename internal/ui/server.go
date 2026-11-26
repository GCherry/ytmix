package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	cfg                Config
	workspaceRoot      string
	homeDir            string
	rootsMu            sync.RWMutex
	allowedRoots       []string
	quickRoots         []fsRootDTO
	defaultOutDir      string
	pickActive         atomic.Bool
	baseCtx            context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	store              *jobStore
	mux                *http.ServeMux
	assetsFS           fs.FS
	encoderOnce        sync.Once
	cachedEncoder      render.EncoderInfo
	pathDEncoderOnce   sync.Once
	cachedPathDEncoder render.EncoderInfo
	filtersOnce        sync.Once
	cachedFilters      map[string]bool
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
	s.mux.HandleFunc("POST /api/estimate", s.handleEstimate)
	s.mux.HandleFunc("GET /api/sessions/{sessionId}/tracks/{trackId}/audio", s.handleSessionTrackAudio)
	s.mux.HandleFunc("POST /api/sessions/{sessionId}/tracks/{trackId}/media", s.handleSessionTrackMediaUpload)
	s.mux.HandleFunc("GET /api/sessions/{sessionId}/tracks/{trackId}/media", s.handleSessionTrackMediaGet)
	s.mux.HandleFunc("DELETE /api/sessions/{sessionId}/tracks/{trackId}/media", s.handleSessionTrackMediaDelete)
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

func (s *Server) detectPathDEncoder(ctx context.Context, resolution string) render.EncoderInfo {
	s.pathDEncoderOnce.Do(func() {
		probeCtx := s.baseCtx
		if probeCtx == nil {
			probeCtx = ctx
		}
		s.cachedPathDEncoder = render.DetectPathDEncoder(probeCtx, s.cfg.GOOS, resolution, s.cfg.Runner)
	})
	return render.EncoderInfo{
		Name: s.cachedPathDEncoder.Name,
		Args: append([]string(nil), s.cachedPathDEncoder.Args...),
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
	maps.Copy(cloned, s.cachedFilters)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
