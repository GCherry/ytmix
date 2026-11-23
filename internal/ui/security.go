package ui

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
	target, _, _, err := s.resolveAllowedRootAndRel(raw, mustExist)
	return target, err
}

func (s *Server) resolveAllowedRootAndRel(raw string, mustExist bool) (string, string, string, error) {
	cleanedInput := strings.TrimSpace(raw)
	if cleanedInput == "" || strings.ContainsRune(cleanedInput, '\x00') {
		return "", "", "", errors.New("path must not be empty")
	}
	expanded, err := s.expandHomePrefix(cleanedInput)
	if err != nil {
		return "", "", "", err
	}

	var candidate string
	if filepath.IsAbs(expanded) {
		candidate = filepath.Clean(expanded)
	} else {
		candidate = filepath.Join(s.workspaceRoot, expanded)
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid path: %w", err)
	}

	var target string
	if mustExist {
		realTarget, err := filepath.EvalSymlinks(absCandidate)
		if err != nil {
			return "", "", "", fmt.Errorf("cannot resolve path %q: %w", raw, err)
		}
		target = realTarget
	} else {
		resolved, err := resolveWithExistingAncestor(absCandidate)
		if err != nil {
			return "", "", "", fmt.Errorf("cannot resolve path %q: %w", raw, err)
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
			return root, root, ".", nil
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
					return "", "", "", fmt.Errorf("path %q escapes allowed roots", raw)
				}
				cur = filepath.Join(cur, seg)
				if !strings.HasPrefix(cur, basePrefix+sep) {
					return "", "", "", fmt.Errorf("path %q escapes allowed roots", raw)
				}
				// #nosec G703 -- cur is confined to allowedRoot via strings.HasPrefix and rel traversal checks above.
				if info, err := os.Lstat(cur); err == nil && info.Mode()&os.ModeSymlink != 0 {
					if _, evalErr := filepath.EvalSymlinks(cur); evalErr != nil {
						return "", "", "", fmt.Errorf("dangling symlink %q is not permitted", raw)
					}
				}
			}
		}
		return target, root, rel, nil
	}
	return "", "", "", fmt.Errorf("path %q escapes allowed roots", raw)
}

func (s *Server) openAllowedFile(raw string) (*os.File, string, int, error) {
	target, rootDir, rel, err := s.resolveAllowedRootAndRel(raw, false)
	if err != nil {
		return nil, "", http.StatusForbidden, err
	}
	// #nosec G304 G703 -- rootDir is a canonicalized member of s.allowedRoots and rel is confined via os.OpenRoot.
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, "", http.StatusNotFound, fmt.Errorf("cannot open root for %q: %w", raw, err)
	}
	defer func() { _ = root.Close() }()

	f, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", http.StatusNotFound, err
		}
		return nil, "", http.StatusForbidden, fmt.Errorf("cannot open %q within allowed root: %w", raw, err)
	}
	return f, target, http.StatusOK, nil
}

func openFileInDir(dir, filePath string) (*os.File, error) {
	absDir, err := canonicalizeCandidatePath(dir)
	if err != nil {
		return nil, err
	}
	absFile, err := canonicalizeCandidatePath(filePath)
	if err != nil {
		return nil, err
	}
	if !isPathWithinRoot(absDir, absFile) {
		return nil, fmt.Errorf("path %q escapes directory %q", filePath, dir)
	}
	rel, err := filepath.Rel(absDir, absFile)
	if err != nil {
		return nil, err
	}
	// #nosec G304 G703 -- absDir is canonicalized and rel is confined inside absDir via os.OpenRoot.
	root, err := os.OpenRoot(absDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Open(rel)
}

func (s *Server) readAllowedDir(raw string) (string, string, []fsEntryDTO, int, error) {
	dirPath, rootDir, rel, err := s.resolveAllowedRootAndRel(raw, false)
	if err != nil {
		return "", "", nil, http.StatusForbidden, err
	}
	// #nosec G304 G703 -- rootDir is a canonicalized member of s.allowedRoots and rel is confined via os.OpenRoot.
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return "", "", nil, http.StatusNotFound, fmt.Errorf("cannot access directory %q: %w", raw, err)
	}
	defer func() { _ = root.Close() }()

	info, err := root.Stat(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil, http.StatusNotFound, fmt.Errorf("cannot access directory %q: %w", raw, err)
		}
		return "", "", nil, http.StatusForbidden, fmt.Errorf("cannot access directory %q: %w", raw, err)
	}
	if !info.IsDir() {
		return "", "", nil, http.StatusBadRequest, fmt.Errorf("path %q is not a directory", raw)
	}

	fsRel := filepath.ToSlash(rel)
	if fsRel == "" {
		fsRel = "."
	}
	dirEntries, err := fs.ReadDir(root.FS(), fsRel)
	if err != nil {
		return "", "", nil, http.StatusInternalServerError, fmt.Errorf("read directory %q: %w", raw, err)
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
	return dirPath, parent, entries, http.StatusOK, nil
}

func (s *Server) mkdirAllowedDir(rawParent, folderName string) (string, int, error) {
	if !isValidSingleFolderName(folderName) {
		return "", http.StatusBadRequest, errors.New("folder name must be a single non-empty directory name without path separators")
	}
	parentDir, rootDir, relParent, err := s.resolveAllowedRootAndRel(rawParent, false)
	if err != nil {
		return "", http.StatusForbidden, err
	}
	// #nosec G304 G703 -- rootDir is a canonicalized member of s.allowedRoots and relParent is confined via os.OpenRoot.
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return "", http.StatusBadRequest, errors.New("parentDir must be an existing directory")
	}
	defer func() { _ = root.Close() }()

	parentInfo, err := root.Stat(relParent)
	if err != nil || !parentInfo.IsDir() {
		return "", http.StatusBadRequest, errors.New("parentDir must be an existing directory")
	}

	subRoot, err := root.OpenRoot(relParent)
	if err != nil {
		return "", http.StatusBadRequest, errors.New("parentDir must be an existing directory")
	}
	defer func() { _ = subRoot.Close() }()

	if _, err := subRoot.Stat(folderName); err == nil {
		return "", http.StatusConflict, fmt.Errorf("folder %q already exists", folderName)
	}
	// #nosec G301 -- user-facing output directory uses standard 0755 permissions inside a confined allowedRoot via os.OpenRoot.
	if err := subRoot.Mkdir(folderName, 0755); err != nil {
		return "", http.StatusInternalServerError, fmt.Errorf("create folder %q: %v", folderName, err)
	}
	return filepath.Join(parentDir, folderName), http.StatusCreated, nil
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

func saveUploadedPartInDir(dir, fileName string, part *multipart.Part, remainingLimit int64) (int64, error) {
	if remainingLimit <= 0 {
		return 0, errors.New("upload exceeds maximum allowed size")
	}
	if !isValidSingleFolderName(fileName) {
		return 0, fmt.Errorf("invalid upload filename %q", fileName)
	}
	// #nosec G304 G703 -- dir is a server-owned temp workspace and fileName is a validated single literal segment via os.OpenRoot.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()

	f, err := root.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
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
