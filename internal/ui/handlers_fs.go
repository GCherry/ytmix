package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/GCherry/ytmix/internal/render"
)

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

type fsPickRequest struct {
	Kind        string `json:"kind"`
	InitialPath string `json:"initialPath"`
}

type fsPickResponse struct {
	Cancelled bool   `json:"cancelled"`
	Path      string `json:"path,omitempty"`
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
	dirPath, parent, entries, status, err := s.readAllowedDir(rawPath)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, fsListResponse{
		Current: dirPath,
		Parent:  parent,
		Roots:   s.snapshotQuickRoots(),
		Entries: entries,
	})
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
	rawParent := strings.TrimSpace(req.ParentDir)
	if rawParent == "" {
		rawParent = s.defaultBrowseDir()
	}

	targetDir, status, err := s.mkdirAllowedDir(rawParent, folderName)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, fsEntryDTO{
		Name: folderName,
		Path: targetDir,
	})
}

func (s *Server) defaultBrowseDir() string {
	if s.defaultOutDir != "" {
		if info, err := os.Stat(s.defaultOutDir); err == nil && info.IsDir() {
			return s.defaultOutDir
		}
	}
	return s.workspaceRoot
}
