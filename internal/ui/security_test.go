package ui

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityOSRootConfinement(t *testing.T) {
	t.Run("openAllowedFile reads inside allowedRoots and blocks outside or symlink escapes", func(t *testing.T) {
		ws := t.TempDir()
		outside := t.TempDir()

		imgPath := filepath.Join(ws, "sub", "bg.png")
		if err := os.MkdirAll(filepath.Dir(imgPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(imgPath, []byte("PNGDATA"), 0644); err != nil {
			t.Fatal(err)
		}
		secretPath := filepath.Join(outside, "secret.png")
		if err := os.WriteFile(secretPath, []byte("SECRET"), 0644); err != nil {
			t.Fatal(err)
		}

		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		f, resolved, status, err := srv.openAllowedFile(filepath.Join("sub", "bg.png"))
		if err != nil || status != http.StatusOK {
			t.Fatalf("openAllowedFile valid failed: status=%d err=%v", status, err)
		}
		_ = f.Close()
		if !strings.HasSuffix(resolved, filepath.Join("sub", "bg.png")) {
			t.Fatalf("unexpected resolved path: %q", resolved)
		}

		// Non-existent file inside allowedRoot returns 404.
		if fMissing, _, status, err := srv.openAllowedFile(filepath.Join(ws, "sub", "missing.png")); err == nil || status != http.StatusNotFound {
			if fMissing != nil {
				_ = fMissing.Close()
			}
			t.Fatalf("expected 404 for missing file inside root, got status=%d err=%v", status, err)
		}

		// File outside allowedRoots returns 403.
		if fOut, _, status, err := srv.openAllowedFile(secretPath); err == nil || status != http.StatusForbidden {
			if fOut != nil {
				_ = fOut.Close()
			}
			t.Fatalf("expected 403 for outside file, got status=%d err=%v", status, err)
		}

		// Symlink inside workspace pointing outside allowedRoots returns 403.
		linkPath := filepath.Join(ws, "escape.png")
		if err := os.Symlink(secretPath, linkPath); err == nil {
			if fLink, _, status, err := srv.openAllowedFile(linkPath); err == nil || status != http.StatusForbidden {
				if fLink != nil {
					_ = fLink.Close()
				}
				t.Fatalf("expected 403 for symlink escape, got status=%d err=%v", status, err)
			}
		}
	})

	t.Run("readAllowedDir and mkdirAllowedDir confine operations with os.Root", func(t *testing.T) {
		ws := t.TempDir()
		outside := t.TempDir()
		if err := os.Mkdir(filepath.Join(ws, "zeta"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(ws, "alpha"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "file.txt"), []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}

		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		cur, parent, entries, status, err := srv.readAllowedDir(ws)
		if err != nil || status != http.StatusOK {
			t.Fatalf("readAllowedDir failed: status=%d err=%v", status, err)
		}
		if cur == "" || parent != "" {
			t.Fatalf("unexpected cur=%q parent=%q", cur, parent)
		}
		if len(entries) != 2 || entries[0].Name != "alpha" || entries[1].Name != "zeta" {
			t.Fatalf("expected sorted [alpha zeta], got %+v", entries)
		}

		// Reading a regular file as directory returns 400.
		if _, _, _, status, err := srv.readAllowedDir(filepath.Join(ws, "file.txt")); err == nil || status != http.StatusBadRequest {
			t.Fatalf("expected 400 for non-directory path, got status=%d err=%v", status, err)
		}

		// Reading outside allowedRoots returns 403.
		if _, _, _, status, err := srv.readAllowedDir(outside); err == nil || status != http.StatusForbidden {
			t.Fatalf("expected 403 for outside dir, got status=%d err=%v", status, err)
		}

		// mkdirAllowedDir creates single segment and rejects duplicates / traversal.
		created, status, err := srv.mkdirAllowedDir(ws, "exports")
		if err != nil || status != http.StatusCreated {
			t.Fatalf("mkdirAllowedDir failed: status=%d err=%v", status, err)
		}
		if info, statErr := os.Stat(created); statErr != nil || !info.IsDir() {
			t.Fatalf("expected created directory %q, err=%v", created, statErr)
		}
		if _, status, err := srv.mkdirAllowedDir(ws, "exports"); err == nil || status != http.StatusConflict {
			t.Fatalf("expected 409 for duplicate dir, got status=%d err=%v", status, err)
		}
		if _, status, err := srv.mkdirAllowedDir(ws, "../escape"); err == nil || status != http.StatusBadRequest {
			t.Fatalf("expected 400 for traversal segment, got status=%d err=%v", status, err)
		}
	})

	t.Run("openFileInDir and saveUploadedPartInDir confine within dir via os.Root", func(t *testing.T) {
		rootDir := t.TempDir()
		outside := t.TempDir()
		outsideFile := filepath.Join(outside, "out.mp3")
		if err := os.WriteFile(outsideFile, []byte("OUT"), 0644); err != nil {
			t.Fatal(err)
		}

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("tracks", "01.mp3")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("MP3BYTES")); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}

		mr := multipart.NewReader(&body, mw.Boundary())
		mp, err := mr.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = mp.Close() }()

		n, err := saveUploadedPartInDir(rootDir, "track_0001.mp3", mp, 1024)
		if err != nil || n != 8 {
			t.Fatalf("saveUploadedPartInDir failed: n=%d err=%v", n, err)
		}

		f, err := openFileInDir(rootDir, filepath.Join(rootDir, "track_0001.mp3"))
		if err != nil {
			t.Fatalf("openFileInDir valid failed: %v", err)
		}
		_ = f.Close()

		if fBad, err := openFileInDir(rootDir, outsideFile); err == nil {
			_ = fBad.Close()
			t.Fatalf("expected openFileInDir to reject outside file")
		}
	})
}
