package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type sseStreamRecorder struct {
	mu      sync.Mutex
	header  http.Header
	code    int
	buf     bytes.Buffer
	flushed chan struct{}
}

func newSSERecorder() *sseStreamRecorder {
	return &sseStreamRecorder{
		header:  make(http.Header),
		flushed: make(chan struct{}, 32),
	}
}

func (r *sseStreamRecorder) Header() http.Header { return r.header }

func (r *sseStreamRecorder) WriteHeader(code int) {
	r.mu.Lock()
	r.code = code
	r.mu.Unlock()
}

func (r *sseStreamRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	n, err := r.buf.Write(p)
	r.mu.Unlock()
	return n, err
}

func (r *sseStreamRecorder) Flush() {
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func (r *sseStreamRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func okLookPath(name string) (string, error) {
	return "/usr/bin/" + name, nil
}

func stubPipelineRunner(t *testing.T, probeDurations map[string]string) func(context.Context, string, ...string) ([]byte, error) {
	t.Helper()
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			target := args[len(args)-1]
			dur := "5.000000"
			for substr, val := range probeDurations {
				if strings.Contains(target, substr) {
					dur = val
					break
				}
			}
			return []byte(`{"format":{"duration":"` + dur + `","tags":{}}}`), nil
		}
		if name == "ffmpeg" {
			// Write dummy output file for final render commands so /output download succeeds.
			if len(args) > 0 {
				last := args[len(args)-1]
				if strings.HasSuffix(strings.ToLower(last), ".mp4") {
					if err := os.WriteFile(last, []byte("FAKE_MP4_DATA"), 0600); err != nil {
						return nil, err
					}
				}
			}
			return nil, nil
		}
		return nil, errors.New("unexpected binary: " + name)
	}
}

func newMultipartProbeRequest(t *testing.T, files map[string][]byte, order []string) (*http.Request, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, name := range order {
		part, err := mw.CreateFormFile("tracks", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/probe", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "127.0.0.1:50000"
	return req, mw.FormDataContentType()
}

func TestStaticAssetsAndHealth(t *testing.T) {
	srv, err := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     8080,
		AudioDir: "./tracks",
		LookPath: okLookPath,
		Runner:   stubPipelineRunner(t, nil),
		GOOS:     "linux",
	})
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}
	defer srv.Close()

	for _, tc := range []struct {
		path     string
		wantType string
		wantBody string
	}{
		{"/", "text/html", "Editorial Mix Studio"},
		{"/styles.css", "text/css", "#060707"},
		{"/app.js", "javascript", "/api/render"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", tc.path, rec.Code)
		}
		if !strings.Contains(strings.ToLower(rec.Header().Get("Content-Type")), tc.wantType) {
			t.Fatalf("GET %s Content-Type = %q, want %q", tc.path, rec.Header().Get("Content-Type"), tc.wantType)
		}
		if !strings.Contains(rec.Body.String(), tc.wantBody) {
			t.Fatalf("GET %s missing expected content %q", tc.path, tc.wantBody)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "127.0.0.1:41234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health status = %d, want 200", rec.Code)
	}
	var health healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health.Status != "ok" || !health.FFmpeg || !health.FFprobe || !health.LocalPathsAllowed {
		t.Fatalf("unexpected health response: %+v", health)
	}
	if health.Encoder == "" {
		t.Fatalf("expected non-empty encoder in health response: %+v", health)
	}
}

func TestHealthDegradedAndLANRestriction(t *testing.T) {
	srv, err := NewServer(Config{
		Host: "0.0.0.0",
		Port: 8080,
		LookPath: func(name string) (string, error) {
			if name == "ffmpeg" {
				return "", errors.New("missing")
			}
			return "/usr/bin/" + name, nil
		},
		Runner: stubPipelineRunner(t, nil),
		GOOS:   "linux",
	})
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "192.168.1.42:51000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var health healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "degraded" || health.FFmpeg || !health.FFprobe || health.LocalPathsAllowed {
		t.Fatalf("unexpected LAN/degraded health: %+v", health)
	}

	// Local path probe must be rejected when bound to 0.0.0.0 / non-loopback.
	probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":"/etc"}`))
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.RemoteAddr = "192.168.1.42:51000"
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	if probeRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for LAN local-path probe, got %d: %s", probeRec.Code, probeRec.Body.String())
	}
}

func TestProbeUploadValidationAndOpaqueTokens(t *testing.T) {
	srv, err := NewServer(Config{
		Host:           "127.0.0.1",
		Port:           8080,
		LookPath:       okLookPath,
		Runner:         stubPipelineRunner(t, map[string]string{"Beta": "12.0", "Alpha": "8.0"}),
		MaxUploadBytes: 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	t.Run("rejects non-mp3 upload extension", func(t *testing.T) {
		req, _ := newMultipartProbeRequest(t, map[string][]byte{"evil.exe": []byte("nope")}, []string{"evil.exe"})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for non-mp3 upload, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("probes uploaded mp3s in upload order with opaque IDs", func(t *testing.T) {
		req, _ := newMultipartProbeRequest(
			t,
			map[string][]byte{"02 - Beta.mp3": []byte("b"), "01 - Alpha.mp3": []byte("a")},
			[]string{"02 - Beta.mp3", "01 - Alpha.mp3"},
		)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/probe status = %d: %s", rec.Code, rec.Body.String())
		}
		var resp probeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.SessionID == "" || len(resp.Tracks) != 2 {
			t.Fatalf("unexpected probe response: %+v", resp)
		}
		if resp.Tracks[0].DisplayTitle != "Beta" || resp.Tracks[1].DisplayTitle != "Alpha" {
			t.Fatalf("expected upload order preserved, got: %+v", resp.Tracks)
		}
		if strings.Contains(rec.Body.String(), os.TempDir()) {
			t.Fatalf("probe response must not leak server temp paths: %s", rec.Body.String())
		}
	})
}

func TestRenderUploadFlowSSEAndDownloads(t *testing.T) {
	srv, err := NewServer(Config{
		Host:           "127.0.0.1",
		Port:           8080,
		LookPath:       okLookPath,
		Runner:         stubPipelineRunner(t, map[string]string{"Second": "10.0", "First": "6.0"}),
		GOOS:           "linux",
		MaxUploadBytes: 10 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// 1. Probe two uploaded MP3s.
	probeReq, _ := newMultipartProbeRequest(
		t,
		map[string][]byte{"01_First.mp3": []byte("a"), "02_Second.mp3": []byte("b")},
		[]string{"01_First.mp3", "02_Second.mp3"},
	)
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	if probeRec.Code != http.StatusOK {
		t.Fatalf("probe failed: %d %s", probeRec.Code, probeRec.Body.String())
	}
	var probed probeResponse
	if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}

	// 2. Render with REORDERED trackIds ([Second, First]) and uploaded PNG Background Visual.
	outDir := t.TempDir()
	var renderBuf bytes.Buffer
	mw := multipart.NewWriter(&renderBuf)
	cfgJSON, err := json.Marshal(map[string]any{
		"sessionId":     probed.SessionID,
		"trackIds":      []string{probed.Tracks[1].ID, probed.Tracks[0].ID},
		"out":           filepath.Join(outDir, "custom-mix.mp4"),
		"timestamps":    filepath.Join(outDir, "chapters.txt"),
		"crossfade":     2.0,
		"overlayTitles": true,
		"titleDuration": 4,
		"fontName":      "Arial",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("config", string(cfgJSON)); err != nil {
		t.Fatal(err)
	}
	bgPart, err := mw.CreateFormFile("bg", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bgPart.Write([]byte("PNGDATA")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	renderReq := httptest.NewRequest(http.MethodPost, "/api/render", &renderBuf)
	renderReq.Header.Set("Content-Type", mw.FormDataContentType())
	renderReq.RemoteAddr = "127.0.0.1:50000"
	renderRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(renderRec, renderReq)
	if renderRec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/render status = %d, want 202: %s", renderRec.Code, renderRec.Body.String())
	}
	var started renderStartResponse
	if err := json.NewDecoder(renderRec.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started.JobID == "" || started.RenderingPath != "Path C" {
		t.Fatalf("unexpected render start response: %+v", started)
	}

	// 3. Stream SSE events and wait for completion.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	evReq := httptest.NewRequest(http.MethodGet, "/api/render/"+started.JobID+"/events", nil).WithContext(ctx)
	evRec := newSSERecorder()
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		srv.Handler().ServeHTTP(evRec, evReq)
	}()

	select {
	case <-doneCh:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for SSE completion; got:\n%s", evRec.String())
	}

	if !strings.Contains(evRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("unexpected SSE Content-Type: %q", evRec.Header().Get("Content-Type"))
	}
	rawEvents := evRec.String()
	if !strings.Contains(rawEvents, "event: complete") {
		t.Fatalf("did not receive SSE complete event; got:\n%s", rawEvents)
	}
	for _, step := range []string{"[1/5]", "[2/5]", "[3/5]", "[4/5]", "[5/5]"} {
		if !strings.Contains(rawEvents, step) {
			t.Errorf("missing SSE step %s in:\n%s", step, rawEvents)
		}
	}

	// 4. Verify downloadable output.mp4 and reordered timestamps.txt.
	tsReq := httptest.NewRequest(http.MethodGet, "/api/render/"+started.JobID+"/timestamps", nil)
	tsRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tsRec, tsReq)
	if tsRec.Code != http.StatusOK || tsRec.Body.String() != "00:00 Second\n00:08 First\n" {
		t.Fatalf("timestamps download status=%d body=%q, want 00:00 Second\\n00:08 First\\n", tsRec.Code, tsRec.Body.String())
	}

	outReq := httptest.NewRequest(http.MethodGet, "/api/render/"+started.JobID+"/output", nil)
	outRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(outRec, outReq)
	if outRec.Code != http.StatusOK || outRec.Body.String() != "FAKE_MP4_DATA" {
		t.Fatalf("output download status=%d body=%q", outRec.Code, outRec.Body.String())
	}
}

func TestRenderRejectsConcurrentAndInvalidInputs(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	blockingRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			return []byte(`{"format":{"duration":"6.000000","tags":{}}}`), nil
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if len(args) > 0 && strings.HasSuffix(args[len(args)-1], ".mp4") {
			_ = os.WriteFile(args[len(args)-1], []byte("MP4"), 0600)
		}
		return nil, nil
	}

	srv, err := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     8080,
		LookPath: okLookPath,
		Runner:   blockingRunner,
		GOOS:     "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		once.Do(func() { close(gate) })
		srv.Close()
	}()

	workDir := t.TempDir()
	audioDir := filepath.Join(workDir, "tracks")
	if err := os.Mkdir(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "01_A.mp3"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	bgPath := filepath.Join(workDir, "bg.png")
	if err := os.WriteFile(bgPath, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	// Probe local dir.
	probeBody, _ := json.Marshal(map[string]any{"audioDir": audioDir})
	probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", bytes.NewReader(probeBody))
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.RemoteAddr = "127.0.0.1:50000"
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	var probed probeResponse
	if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}

	// Invalid bg extension should fail synchronously with 400.
	badReqBody, _ := json.Marshal(map[string]any{
		"sessionId": probed.SessionID,
		"trackIds":  []string{probed.Tracks[0].ID},
		"bgPath":    filepath.Join(workDir, "bad.mkv"),
	})
	badReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(badReqBody))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.RemoteAddr = "127.0.0.1:50000"
	badRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid bg extension, got %d", badRec.Code)
	}

	// Start first render job (blocked on gate).
	goodBody, _ := json.Marshal(map[string]any{
		"sessionId":  probed.SessionID,
		"trackIds":   []string{probed.Tracks[0].ID},
		"bgPath":     bgPath,
		"out":        filepath.Join(workDir, "out.mp4"),
		"timestamps": filepath.Join(workDir, "ts.txt"),
	})
	req1 := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(goodBody))
	req1.Header.Set("Content-Type", "application/json")
	req1.RemoteAddr = "127.0.0.1:50000"
	rec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for first render, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Second concurrent render must return 409 Conflict.
	req2 := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(goodBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.RemoteAddr = "127.0.0.1:50000"
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for concurrent render, got %d: %s", rec2.Code, rec2.Body.String())
	}
	once.Do(func() { close(gate) })
}

func TestUploadLimitAndCleanup(t *testing.T) {
	srv, err := NewServer(Config{
		Host:           "127.0.0.1",
		Port:           8080,
		LookPath:       okLookPath,
		Runner:         stubPipelineRunner(t, nil),
		MaxUploadBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}

	req, _ := newMultipartProbeRequest(t, map[string][]byte{"big.mp3": bytes.Repeat([]byte("A"), 256)}, []string{"big.mp3"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Request Entity Too Large, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify Close removes any allocated session/job temp directories.
	srv2, err := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     8080,
		LookPath: okLookPath,
		Runner:   stubPipelineRunner(t, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	okReq, _ := newMultipartProbeRequest(t, map[string][]byte{"01_Track.mp3": []byte("mp3")}, []string{"01_Track.mp3"})
	okRec := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("probe failed: %d", okRec.Code)
	}
	var probed probeResponse
	if err := json.Unmarshal(okRec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}
	srv2.store.mu.Lock()
	sessDir := srv2.store.sessions[probed.SessionID].tempDir
	srv2.store.mu.Unlock()
	if _, err := os.Stat(sessDir); err != nil {
		t.Fatalf("expected session tempDir %q to exist before Close: %v", sessDir, err)
	}
	srv2.Close()
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Fatalf("expected session tempDir %q to be deleted after Close, got err=%v", sessDir, err)
	}
}

func TestLocalBackgroundPreviewGuard(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(imgPath, []byte("PNG"), 0644); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(Config{Host: "127.0.0.1", Port: 8080, LookPath: okLookPath, Runner: stubPipelineRunner(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Valid loopback Background Visual serves 200.
	req := httptest.NewRequest(http.MethodGet, "/api/preview-bg?path="+imgPath, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "PNG" {
		t.Fatalf("expected 200 PNG preview, got %d: %s", rec.Code, rec.Body.String())
	}

	// Non-background file extension must be rejected.
	badReq := httptest.NewRequest(http.MethodGet, "/api/preview-bg?path="+secretPath, nil)
	badReq.RemoteAddr = "127.0.0.1:50000"
	badRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-background extension, got %d", badRec.Code)
	}

	// Non-loopback client must be rejected.
	lanReq := httptest.NewRequest(http.MethodGet, "/api/preview-bg?path="+imgPath, nil)
	lanReq.RemoteAddr = "192.168.1.50:50000"
	lanRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(lanRec, lanReq)
	if lanRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for LAN client, got %d", lanRec.Code)
	}
}

func TestCSRFSameOriginAndContentTypeGuards(t *testing.T) {
	srv, err := NewServer(Config{Host: "127.0.0.1", Port: 8080, LookPath: okLookPath, Runner: stubPipelineRunner(t, nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	t.Run("rejects cross-origin Origin and Sec-Fetch-Site headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/render", strings.NewReader(`{}`))
		req.Host = "127.0.0.1:8080"
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for cross-origin Origin, got %d: %s", rec.Code, rec.Body.String())
		}

		req2 := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":"."}`))
		req2.Host = "127.0.0.1:8080"
		req2.RemoteAddr = "127.0.0.1:50000"
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Sec-Fetch-Site", "cross-site")
		rec2 := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for Sec-Fetch-Site cross-site, got %d: %s", rec2.Code, rec2.Body.String())
		}
	})

	t.Run("rejects non-JSON Content-Type such as text/plain on JSON routes", func(t *testing.T) {
		for _, path := range []string{"/api/probe", "/api/render"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"audioDir":"."}`))
			req.Host = "127.0.0.1:8080"
			req.RemoteAddr = "127.0.0.1:50000"
			req.Header.Set("Content-Type", "text/plain")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("POST %s with text/plain got %d, want 415", path, rec.Code)
			}
		}
	})

	t.Run("allows same-origin Origin and Sec-Fetch-Site", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "01_A.mp3"), []byte("a"), 0644); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(dir)+`}`))
		req.Host = "127.0.0.1:8080"
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for same-origin request, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
