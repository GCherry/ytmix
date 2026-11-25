package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/render"
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
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "-filters") {
				return []byte(`Filters:
 .. color |->V Color
 .. showwaves A->V Wave
 TS gblur V->V Blur
 .. scale V->V Scale
 .. pad V->V Pad
 .. showfreqs A->V Freqs
 .S avectorscope A->V Scope
 .. showcqt A->V CQT
 .S showspectrum A->V Spectrum
 .. a3dscope A->V 3D
 .. ahistogram A->V Histogram
 .. aphasemeter A->N Phase
 .. anullsink A->| Sink
 .. showvolume A->V Volume
 TS blend VV->V Blend
 TS colorchannelmixer V->V Mixer
 TS fade V->V Fade
`), nil
			}
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
		wantSub  []string
	}{
		{"/", "text/html", []string{"Editorial Mix Studio", "out-dir-input", "browse-out-dir-btn", "browse-audio-dir-btn", "browse-bg-path-btn", "fs-modal", "save-lan-folder-btn", "fs-modal-out-name", "fs-modal-ts-name", "bg-mode-visualizer", "bg-mode-black", "per-track-toggle", "track-layout-select", "track-transition-select", "stage-l1-fullscreen", "stage-l1-card-wrap", "download-manifest-btn", "viz-pager", "audition-btn", "darkness-box", "stage-canvas", "left-tab-tracks", "left-tab-studio", "left-pane-studio", "stage-audition-btn", "stage-track-select", "right-tab-chapters", "right-tab-output", "right-tab-telemetry", "render-dock", "estimate-strip", "estimate-pill", "cost-seg-audio", "cost-seg-video", "telemetry-accuracy"}},
		{"/styles.css", "text/css", []string{"[hidden]", ".modal-backdrop", ".fs-root-chip", ".preset-grid", ".preset-pager", ".darkness-box", ".audition-bar", ".composer-bar", ".mode-pills", ".stage-l1-card", ".studio-tabs-bar", ".dual-studio-grid", ".stage-track-bar", ".render-dock", ".estimate-strip", ".cost-bar", ".cost-seg-audio", ".cost-seg-video", ".telemetry-accuracy", "#render-btn-target"}},
		{"/app.js", "javascript", []string{"/api/render", "/api/estimate", "/api/fs/list", "/api/fs/mkdir", "/api/fs/pick", "pickNativePath", "showDirectoryPicker", "/api/sessions/", "/tracks/", "downloadManifestJSON", "reorderTrackById", "VIZ_PAGE_SIZE", "visualizerBrightness", "fadeToBlackMinutes", "setLeftTab", "setRightTab", "renderStageTrackSelect", "bgMeta", "remainingSec", "estimatedSec", "actualSec"}},
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
		body := rec.Body.String()
		for _, want := range tc.wantSub {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s missing expected content %q", tc.path, want)
			}
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
	wantExts := render.SupportedBackgroundExts()
	if !reflect.DeepEqual(health.SupportedBackgroundExts, wantExts) {
		t.Fatalf("SupportedBackgroundExts = %+v, want %+v", health.SupportedBackgroundExts, wantExts)
	}
	if len(health.Visualizers) != 14 {
		t.Fatalf("expected 14 visualizers in /api/health, got %d", len(health.Visualizers))
	}
	if len(health.VisualizerThemes) != 7 {
		t.Fatalf("expected 7 visualizerThemes in /api/health, got %d", len(health.VisualizerThemes))
	}
	for _, v := range health.Visualizers {
		if !v.Available {
			t.Fatalf("expected visualizer %q available under full mock filters", v.ID)
		}
	}
}

func TestHealthCachesEncoderProbe(t *testing.T) {
	var encoderProbes atomic.Int32
	var filterProbes atomic.Int32
	srv, err := NewServer(Config{
		Host:     "127.0.0.1",
		Port:     8080,
		LookPath: okLookPath,
		GOOS:     "linux",
		Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" {
				if strings.Contains(strings.Join(args, " "), "-filters") {
					filterProbes.Add(1)
					return []byte(" .. color |->V c\n .. showfreqs A->V f\n"), nil
				}
				encoderProbes.Add(1)
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("NewServer error: %v", err)
	}
	defer srv.Close()

	const requests = 5
	var wg sync.WaitGroup
	encoders := make([]string, requests)
	codes := make([]int, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.RemoteAddr = "127.0.0.1:41234"
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			codes[idx] = rec.Code
			var h healthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &h); err == nil {
				encoders[idx] = h.Encoder
			}
		}(i)
	}
	wg.Wait()

	if got := encoderProbes.Load(); got != 1 {
		t.Fatalf("expected ffmpeg encoder probe once across %d health checks, got %d", requests, got)
	}
	if got := filterProbes.Load(); got != 1 {
		t.Fatalf("expected ffmpeg filter probe once across %d health checks, got %d", requests, got)
	}
	for i := 0; i < requests; i++ {
		if codes[i] != http.StatusOK || encoders[i] != "h264_nvenc" {
			t.Fatalf("request %d: status=%d encoder=%q", i, codes[i], encoders[i])
		}
	}
}

func TestHealthDegradedAndLANRestriction(t *testing.T) {
	var probeCalls atomic.Int32
	srv, err := NewServer(Config{
		Host: "0.0.0.0",
		Port: 8080,
		LookPath: func(name string) (string, error) {
			if name == "ffmpeg" {
				return "", errors.New("missing")
			}
			return "/usr/bin/" + name, nil
		},
		Runner: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffmpeg" {
				probeCalls.Add(1)
			}
			return nil, nil
		},
		GOOS: "linux",
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
	if health.Status != "degraded" || health.FFmpeg || !health.FFprobe || health.LocalPathsAllowed || health.Encoder != "unavailable" {
		t.Fatalf("unexpected LAN/degraded health: %+v", health)
	}
	if got := probeCalls.Load(); got != 0 {
		t.Fatalf("expected 0 ffmpeg encoder probe calls when ffmpeg missing, got %d", got)
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
	outDir := t.TempDir()
	baseRunner := stubPipelineRunner(t, map[string]string{"track_0002": "10.0", "track_0001": "6.0"})
	sseReady := make(chan struct{})
	streamRunner := func(ctx context.Context, onLine func(string), name string, args ...string) error {
		if _, err := baseRunner(ctx, name, args...); err != nil {
			return err
		}
		select {
		case <-sseReady:
		case <-ctx.Done():
			return ctx.Err()
		}
		for _, line := range []string{
			"out_time_us=3500000",
			"speed=2.10x",
			"progress=continue",
			"out_time_us=14000000",
			"speed=2.40x",
			"progress=end",
		} {
			if onLine != nil {
				onLine(line)
			}
		}
		return nil
	}
	srv, err := NewServer(Config{
		Host:           "127.0.0.1",
		Port:           8080,
		WorkspaceDir:   outDir,
		LookPath:       okLookPath,
		Runner:         baseRunner,
		StreamRunner:   streamRunner,
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

	renderJob, ok := srv.store.getJob(started.JobID)
	if !ok {
		t.Fatalf("expected job %q in store", started.JobID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		renderJob.mu.Lock()
		subCount := len(renderJob.subs)
		renderJob.mu.Unlock()
		if subCount > 0 {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}
	close(sseReady)

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
	if !strings.Contains(rawEvents, `"detail":"Rendering Mix video to `) || !strings.Contains(rawEvents, `"step":5,"total":5`) || strings.Contains(rawEvents, `"progress":100,"step":5`) {
		t.Errorf("expected Step 5 step event to report <100 progress:\n%s", rawEvents)
	}
	if !strings.Contains(rawEvents, "event: progress") || !strings.Contains(rawEvents, `"speed":"2.10x"`) {
		t.Errorf("expected live SSE progress events with speed:\n%s", rawEvents)
	}
	if !strings.Contains(rawEvents, `"remainingSec":`) || !strings.Contains(rawEvents, `"estimatedTotalSec":`) {
		t.Errorf("expected live SSE progress events with remainingSec and estimatedTotalSec:\n%s", rawEvents)
	}
	if !strings.Contains(rawEvents, `"estimatedSec":`) || !strings.Contains(rawEvents, `"actualSec":`) {
		t.Errorf("expected SSE complete event with estimatedSec and actualSec:\n%s", rawEvents)
	}

	// Verify latestProgress reconnect snapshot does not bloat job.events history.
	job := newRenderJob("job_snap", outDir, "out.mp4", "out.mp4", "ts.txt", "ts.txt", "Path C", func() {})
	job.emit("step", map[string]any{"step": 5, "progress": 20})
	job.emitProgress(map[string]any{"step": 5, "progress": 42, "speed": "2.0x"})
	job.emitProgress(map[string]any{"step": 5, "progress": 77, "speed": "2.5x"})
	hist, sub, done := job.subscribe()
	job.unsubscribe(sub)
	if done {
		t.Fatal("expected running job not to be done")
	}
	if len(hist) != 2 || hist[0].name != "step" || hist[1].name != "progress" || !strings.Contains(hist[1].data, `"progress":77`) {
		t.Fatalf("expected 1 step + 1 latest progress snapshot on reconnect, got: %+v", hist)
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

	srv, err := NewServer(Config{
		Host:         "127.0.0.1",
		Port:         8080,
		WorkspaceDir: workDir,
		LookPath:     okLookPath,
		Runner:       blockingRunner,
		GOOS:         "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		once.Do(func() { close(gate) })
		srv.Close()
	}()

	// Probe local dir.
	probeBody, _ := json.Marshal(map[string]any{"audioDir": audioDir})
	probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", bytes.NewReader(probeBody))
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.RemoteAddr = "127.0.0.1:50000"
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	if probeRec.Code != http.StatusOK {
		t.Fatalf("probe failed: %d %s", probeRec.Code, probeRec.Body.String())
	}
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

	srv, err := NewServer(Config{Host: "127.0.0.1", Port: 8080, WorkspaceDir: dir, LookPath: okLookPath, Runner: stubPipelineRunner(t, nil)})
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
		for _, path := range []string{"/api/probe", "/api/render", "/api/fs/pick"} {
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
		localSrv, err := NewServer(Config{Host: "127.0.0.1", Port: 8080, WorkspaceDir: dir, LookPath: okLookPath, Runner: stubPipelineRunner(t, nil)})
		if err != nil {
			t.Fatal(err)
		}
		defer localSrv.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(dir)+`}`))
		req.Host = "127.0.0.1:8080"
		req.RemoteAddr = "127.0.0.1:50000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		rec := httptest.NewRecorder()
		localSrv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for same-origin request, got %d: %s", rec.Code, rec.Body.String())
		}

		// Path traversal outside WorkspaceDir must be rejected with 403 Forbidden.
		escReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":"../outside"}`))
		escReq.Host = "127.0.0.1:8080"
		escReq.RemoteAddr = "127.0.0.1:50000"
		escReq.Header.Set("Content-Type", "application/json")
		escRec := httptest.NewRecorder()
		localSrv.Handler().ServeHTTP(escRec, escReq)
		if escRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for workspace escape, got %d: %s", escRec.Code, escRec.Body.String())
		}

		bgEscReq := httptest.NewRequest(http.MethodGet, "/api/preview-bg?path=../secret.png", nil)
		bgEscReq.RemoteAddr = "127.0.0.1:50000"
		bgEscRec := httptest.NewRecorder()
		localSrv.Handler().ServeHTTP(bgEscRec, bgEscReq)
		if bgEscRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for preview-bg workspace escape, got %d: %s", bgEscRec.Code, bgEscRec.Body.String())
		}
	})
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestAllowedRootsAndOutputLocationAPIs(t *testing.T) {
	t.Run("health exposes outDir/out/timestamps defaults and redacts roots on LAN", func(t *testing.T) {
		ws := t.TempDir()
		extraDir := t.TempDir()
		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			OutDir:       extraDir,
			Out:          "my_mix.mp4",
			Timestamps:   "my_chapters.txt",
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
			GOOS:         "linux",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/health status = %d: %s", rec.Code, rec.Body.String())
		}
		var payload struct {
			Defaults struct {
				OutDir     string `json:"outDir"`
				Out        string `json:"out"`
				Timestamps string `json:"timestamps"`
			} `json:"defaults"`
			Roots []struct {
				Label string `json:"label"`
				Path  string `json:"path"`
			} `json:"roots"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Defaults.Out != "my_mix.mp4" || payload.Defaults.Timestamps != "my_chapters.txt" || payload.Defaults.OutDir == "" {
			t.Fatalf("unexpected health defaults: %+v", payload.Defaults)
		}
		if len(payload.Roots) < 2 {
			t.Fatalf("expected at least 2 quick roots (Workspace + Output Dir), got %+v", payload.Roots)
		}

		// Non-loopback client must not receive server filesystem roots or local paths.
		lanReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		lanReq.RemoteAddr = "192.168.1.50:50000"
		lanRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(lanRec, lanReq)
		var lanPayload struct {
			Defaults healthDefaults `json:"defaults"`
			Roots    []any          `json:"roots"`
		}
		if err := json.Unmarshal(lanRec.Body.Bytes(), &lanPayload); err != nil {
			t.Fatal(err)
		}
		if len(lanPayload.Roots) != 0 || lanPayload.Defaults.OutDir != "" || lanPayload.Defaults.AudioDir != "" || lanPayload.Defaults.BG != "" {
			t.Fatalf("expected server paths and roots redacted for LAN client, got %+v", lanPayload)
		}
	})

	t.Run("allowedRoots governs probe, preview-bg, render outDir, and blocks escapes", func(t *testing.T) {
		ws := t.TempDir()
		mediaDir := t.TempDir()
		exportDir := t.TempDir()
		outsideDir := t.TempDir()

		if err := os.WriteFile(filepath.Join(mediaDir, "01 - Track.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		bgFile := filepath.Join(mediaDir, "bg.png")
		if err := os.WriteFile(bgFile, []byte("PNG"), 0644); err != nil {
			t.Fatal(err)
		}

		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			OutDir:       exportDir,
			AllowDirs:    []string{mediaDir},
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, map[string]string{"Track": "6.0"}),
			GOOS:         "linux",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		// Probe inside AllowDirs succeeds.
		probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(mediaDir)+`}`))
		probeReq.Header.Set("Content-Type", "application/json")
		probeReq.RemoteAddr = "127.0.0.1:50000"
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusOK {
			t.Fatalf("probe in AllowDirs failed: %d: %s", probeRec.Code, probeRec.Body.String())
		}
		var probed probeResponse
		if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
			t.Fatal(err)
		}

		// Preview BG inside AllowDirs succeeds.
		prevReq := httptest.NewRequest(http.MethodGet, "/api/preview-bg?path="+bgFile, nil)
		prevReq.RemoteAddr = "127.0.0.1:50000"
		prevRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(prevRec, prevReq)
		if prevRec.Code != http.StatusOK {
			t.Fatalf("preview-bg in AllowDirs failed: %d: %s", prevRec.Code, prevRec.Body.String())
		}

		// Render with nested non-existent subfolder under OutDir creates folders & writes files.
		nestedOutDir := filepath.Join(exportDir, "2026", "chill-session")
		renderBody, _ := json.Marshal(renderConfigRequest{
			SessionID:  probed.SessionID,
			TrackIDs:   []string{probed.Tracks[0].ID},
			BGPath:     bgFile,
			OutDir:     nestedOutDir,
			Out:        "chill_mix.mp4",
			Timestamps: "chill_chapters.txt",
		})
		renderReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(renderBody))
		renderReq.Header.Set("Content-Type", "application/json")
		renderReq.RemoteAddr = "127.0.0.1:50000"
		renderRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(renderRec, renderReq)
		if renderRec.Code != http.StatusAccepted {
			t.Fatalf("render with OutDir failed: %d: %s", renderRec.Code, renderRec.Body.String())
		}
		var started renderStartResponse
		if err := json.Unmarshal(renderRec.Body.Bytes(), &started); err != nil {
			t.Fatal(err)
		}
		// Subscribe SSE to wait for job completion.
		sseReq := httptest.NewRequest(http.MethodGet, started.EventsURL, nil)
		sseRec := newSSERecorder()
		srv.Handler().ServeHTTP(sseRec, sseReq)
		if !strings.Contains(sseRec.String(), "event: complete") {
			t.Fatalf("expected complete SSE event, got:\n%s", sseRec.String())
		}
		if _, err := os.Stat(filepath.Join(nestedOutDir, "chill_mix.mp4")); err != nil {
			t.Fatalf("expected output video inside nested OutDir: %v", err)
		}
		if _, err := os.Stat(filepath.Join(nestedOutDir, "chill_chapters.txt")); err != nil {
			t.Fatalf("expected chapters inside nested OutDir: %v", err)
		}

		// Render outside allowedRoots must be rejected with 403 Forbidden.
		escBody, _ := json.Marshal(renderConfigRequest{
			SessionID:  probed.SessionID,
			TrackIDs:   []string{probed.Tracks[0].ID},
			BGPath:     bgFile,
			OutDir:     outsideDir,
			Out:        "pwn.mp4",
			Timestamps: "pwn.txt",
		})
		escReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(escBody))
		escReq.Header.Set("Content-Type", "application/json")
		escReq.RemoteAddr = "127.0.0.1:50000"
		escRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(escRec, escReq)
		if escRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for outside OutDir, got %d: %s", escRec.Code, escRec.Body.String())
		}

		// Render targeting an existing directory or trailing slash as --out must fail upfront with 400 Bad Request.
		for _, badOut := range []string{nestedOutDir, "trailing/"} {
			dirOutBody, _ := json.Marshal(renderConfigRequest{
				SessionID:  probed.SessionID,
				TrackIDs:   []string{probed.Tracks[0].ID},
				BGPath:     bgFile,
				OutDir:     exportDir,
				Out:        badOut,
				Timestamps: "ok.txt",
			})
			dirOutReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(dirOutBody))
			dirOutReq.Header.Set("Content-Type", "application/json")
			dirOutReq.RemoteAddr = "127.0.0.1:50000"
			dirOutRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(dirOutRec, dirOutReq)
			if dirOutRec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for directory/trailing-slash --out %q, got %d: %s", badOut, dirOutRec.Code, dirOutRec.Body.String())
			}
		}
	})

	t.Run("fs list and mkdir enforce loopback, allowedRoots, and single-segment names", func(t *testing.T) {
		ws := t.TempDir()
		outside := t.TempDir()
		if err := os.Mkdir(filepath.Join(ws, "alpha"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "ignore.mp3"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}

		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		// List workspace directories only (files excluded).
		listReq := httptest.NewRequest(http.MethodGet, "/api/fs/list", nil)
		listReq.RemoteAddr = "127.0.0.1:50000"
		listRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(listRec, listReq)
		if listRec.Code != http.StatusOK {
			t.Fatalf("GET /api/fs/list status = %d: %s", listRec.Code, listRec.Body.String())
		}
		var listResp struct {
			Current string `json:"current"`
			Parent  string `json:"parent"`
			Entries []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
			t.Fatal(err)
		}
		if listResp.Parent != "" {
			t.Fatalf("expected empty parent at allowedRoot boundary, got %q", listResp.Parent)
		}
		if len(listResp.Entries) != 1 || listResp.Entries[0].Name != "alpha" {
			t.Fatalf("expected only [alpha] directory entry, got %+v", listResp.Entries)
		}

		// Outside path rejected with 403.
		outListReq := httptest.NewRequest(http.MethodGet, "/api/fs/list?path="+outside, nil)
		outListReq.RemoteAddr = "127.0.0.1:50000"
		outListRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(outListRec, outListReq)
		if outListRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for outside fs/list, got %d", outListRec.Code)
		}

		// Non-loopback client rejected with 403.
		lanListReq := httptest.NewRequest(http.MethodGet, "/api/fs/list", nil)
		lanListReq.RemoteAddr = "192.168.1.99:50000"
		lanListRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(lanListRec, lanListReq)
		if lanListRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for LAN fs/list, got %d", lanListRec.Code)
		}

		// Create new subfolder via POST /api/fs/mkdir.
		mkdirReq := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", strings.NewReader(`{"parentDir":`+strconvQuote(ws)+`,"name":"exports"}`))
		mkdirReq.Header.Set("Content-Type", "application/json")
		mkdirReq.RemoteAddr = "127.0.0.1:50000"
		mkdirRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(mkdirRec, mkdirReq)
		if mkdirRec.Code != http.StatusCreated {
			t.Fatalf("POST /api/fs/mkdir status = %d: %s", mkdirRec.Code, mkdirRec.Body.String())
		}
		if info, err := os.Stat(filepath.Join(ws, "exports")); err != nil || !info.IsDir() {
			t.Fatalf("expected created directory exports, err=%v", err)
		}

		// Invalid multi-segment or traversal folder name rejected with 400.
		for _, badName := range []string{"../escape", "a/b", ".", "..", ""} {
			badReq := httptest.NewRequest(http.MethodPost, "/api/fs/mkdir", strings.NewReader(`{"parentDir":`+strconvQuote(ws)+`,"name":`+strconvQuote(badName)+`}`))
			badReq.Header.Set("Content-Type", "application/json")
			badReq.RemoteAddr = "127.0.0.1:50000"
			badRec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(badRec, badReq)
			if badRec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for mkdir name %q, got %d: %s", badName, badRec.Code, badRec.Body.String())
			}
		}
	})

	t.Run("rejects symlink escapes for existing and nested non-existent targets", func(t *testing.T) {
		ws := t.TempDir()
		outside := t.TempDir()
		linkPath := filepath.Join(ws, "escape-link")
		if err := os.Symlink(outside, linkPath); err != nil {
			t.Skipf("symlinks unavailable in this environment: %v", err)
		}

		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		// Listing via symlink pointing outside allowedRoots must be rejected.
		listReq := httptest.NewRequest(http.MethodGet, "/api/fs/list?path="+linkPath, nil)
		listReq.RemoteAddr = "127.0.0.1:50000"
		listRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(listRec, listReq)
		if listRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for symlink fs/list escape, got %d: %s", listRec.Code, listRec.Body.String())
		}

		// Non-existent nested path whose existing ancestor is a symlink escaping workspace must be rejected.
		if _, err := srv.resolveAllowedPath(filepath.Join(linkPath, "nested", "out.mp4"), false); err == nil {
			t.Fatalf("expected resolveAllowedPath to reject non-existent target through escaping symlink")
		}

		// Dangling symlink pointing outside allowedRoots must also be rejected.
		danglingPath := filepath.Join(ws, "dangling-link")
		if err := os.Symlink(filepath.Join(outside, "missing-dir"), danglingPath); err == nil {
			if _, err := srv.resolveAllowedPath(danglingPath, false); err == nil {
				t.Fatalf("expected resolveAllowedPath to reject direct dangling symlink")
			}
			if _, err := srv.resolveAllowedPath(filepath.Join(danglingPath, "out.mp4"), false); err == nil {
				t.Fatalf("expected resolveAllowedPath to reject dangling symlink escape")
			}
		}
	})

	t.Run("IncludeHomeRoot enables ~ expansion and Home root chip", func(t *testing.T) {
		ws := t.TempDir()
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)
		t.Setenv("USERPROFILE", fakeHome)

		homeTracks := filepath.Join(fakeHome, "music")
		if err := os.Mkdir(homeTracks, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(homeTracks, "01 - Home.mp3"), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}

		srv, err := NewServer(Config{
			Host:            "127.0.0.1",
			Port:            8080,
			WorkspaceDir:    ws,
			IncludeHomeRoot: true,
			LookPath:        okLookPath,
			Runner:          stubPipelineRunner(t, nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":"~/music"}`))
		probeReq.Header.Set("Content-Type", "application/json")
		probeReq.RemoteAddr = "127.0.0.1:50000"
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusOK {
			t.Fatalf("expected 200 for ~/music probe when IncludeHomeRoot=true, got %d: %s", probeRec.Code, probeRec.Body.String())
		}

		listReq := httptest.NewRequest(http.MethodGet, "/api/fs/list?path=~", nil)
		listReq.RemoteAddr = "127.0.0.1:50000"
		listRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(listRec, listReq)
		if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"music"`) {
			t.Fatalf("expected 200 and music directory for fs/list?path=~, got %d: %s", listRec.Code, listRec.Body.String())
		}
	})
}

func TestPathDServerHealthRenderLANAndTrackAudio(t *testing.T) {
	t.Run("health reports unavailable preset when required filter missing and rejects it before claiming job", func(t *testing.T) {
		baseRunner := stubPipelineRunner(t, nil)
		runnerNo3D := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffmpeg" && strings.Contains(strings.Join(args, " "), "-filters") {
				return []byte(" .. color |->V Color\n .. showfreqs A->V Freqs\n"), nil
			}
			return baseRunner(ctx, name, args...)
		}
		ws := t.TempDir()
		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       runnerNo3D,
			GOOS:         "linux",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		hReq := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		hReq.RemoteAddr = "127.0.0.1:50000"
		hRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(hRec, hReq)
		var health healthResponse
		if err := json.Unmarshal(hRec.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		var barsOK, scopeOK *bool
		for i := range health.Visualizers {
			v := &health.Visualizers[i]
			switch v.ID {
			case "bars":
				val := v.Available
				barsOK = &val
			case "3dscope":
				val := v.Available
				scopeOK = &val
			}
		}
		if barsOK == nil || !*barsOK || scopeOK == nil || *scopeOK {
			t.Fatalf("expected bars available=true and 3dscope available=false, got bars=%v 3dscope=%v", barsOK, scopeOK)
		}

		// Probe one uploaded MP3 so we can test /api/render + /audio.
		probeReq, _ := newMultipartProbeRequest(t, map[string][]byte{"01_Sleep.mp3": []byte("ID3MP3AUDIO")}, []string{"01_Sleep.mp3"})
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusOK {
			t.Fatalf("probe failed: %d %s", probeRec.Code, probeRec.Body.String())
		}
		var probed probeResponse
		if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
			t.Fatal(err)
		}

		// Unavailable 3dscope fails synchronously with 400 before creating OutDir on disk.
		uncreatedOutDir := filepath.Join(ws, "should-not-exist")
		badBody, _ := json.Marshal(map[string]any{
			"sessionId":  probed.SessionID,
			"trackIds":   []string{probed.Tracks[0].ID},
			"bgMode":     "visualizer",
			"visualizer": "3dscope",
			"outDir":     uncreatedOutDir,
		})
		badReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(badBody))
		badReq.Header.Set("Content-Type", "application/json")
		badReq.RemoteAddr = "127.0.0.1:50000"
		badRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(badRec, badReq)
		if badRec.Code != http.StatusBadRequest || !strings.Contains(badRec.Body.String(), "a3dscope") {
			t.Fatalf("expected 400 mentioning a3dscope, got %d: %s", badRec.Code, badRec.Body.String())
		}
		if _, statErr := os.Stat(uncreatedOutDir); !os.IsNotExist(statErr) {
			t.Fatalf("expected rejected visualizer filter not to create %q, statErr=%v", uncreatedOutDir, statErr)
		}

		// Invalid brightness (explicit 0 and 9 and 101), negative fade, mutual exclusion all fail with 400.
		for _, badPayload := range []map[string]any{
			{"sessionId": probed.SessionID, "trackIds": []string{probed.Tracks[0].ID}, "bgMode": "visualizer", "visualizer": "bars", "visualizerResolution": "720p"},
			{"sessionId": probed.SessionID, "trackIds": []string{probed.Tracks[0].ID}, "bgMode": "visualizer", "visualizer": "bars", "visualizerBrightness": 0},
			{"sessionId": probed.SessionID, "trackIds": []string{probed.Tracks[0].ID}, "bgMode": "visualizer", "visualizer": "bars", "visualizerBrightness": 9},
			{"sessionId": probed.SessionID, "trackIds": []string{probed.Tracks[0].ID}, "bgMode": "visualizer", "visualizer": "bars", "visualizerBrightness": 101},
			{"sessionId": probed.SessionID, "trackIds": []string{probed.Tracks[0].ID}, "bgMode": "visualizer", "visualizer": "bars", "fadeToBlackMinutes": -1},
			{"sessionId": probed.SessionID, "trackIds": []string{probed.Tracks[0].ID}, "bgMode": "visualizer", "bgPath": "cover.png", "visualizer": "bars"},
		} {
			b, _ := json.Marshal(badPayload)
			r := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(b))
			r.Header.Set("Content-Type", "application/json")
			r.RemoteAddr = "127.0.0.1:50000"
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, r)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for payload %+v, got %d: %s", badPayload, rec.Code, rec.Body.String())
			}
		}

		// Session track audio streaming enforces same-origin, 404 on unknown tokens, and audio headers on valid tokens.
		audURL := "/api/sessions/" + probed.SessionID + "/tracks/" + probed.Tracks[0].ID + "/audio"
		audReq := httptest.NewRequest(http.MethodGet, audURL, nil)
		audReq.Host = "127.0.0.1:8080"
		audReq.RemoteAddr = "127.0.0.1:50000"
		audRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(audRec, audReq)
		if audRec.Code != http.StatusOK ||
			audRec.Header().Get("Content-Type") != "audio/mpeg" ||
			audRec.Header().Get("X-Content-Type-Options") != "nosniff" ||
			audRec.Header().Get("Cache-Control") != "no-store" ||
			audRec.Body.String() != "ID3MP3AUDIO" {
			t.Fatalf("unexpected audio stream response: code=%d headers=%v body=%q", audRec.Code, audRec.Header(), audRec.Body.String())
		}

		crossReq := httptest.NewRequest(http.MethodGet, audURL, nil)
		crossReq.Host = "127.0.0.1:8080"
		crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
		crossRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(crossRec, crossReq)
		if crossRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for cross-site audio request, got %d", crossRec.Code)
		}

		missReq := httptest.NewRequest(http.MethodGet, "/api/sessions/sess_missing/tracks/trk_1/audio", nil)
		missRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(missRec, missReq)
		if missRec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for missing session audio, got %d", missRec.Code)
		}
	})

	t.Run("LAN session allows Path D Auto Visualizer render while blocking local bgPath", func(t *testing.T) {
		srv, err := NewServer(Config{
			Host:         "0.0.0.0",
			Port:         8080,
			WorkspaceDir: t.TempDir(),
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
			GOOS:         "linux",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		probeReq, _ := newMultipartProbeRequest(t, map[string][]byte{"01_Night.mp3": []byte("MP3")}, []string{"01_Night.mp3"})
		probeReq.RemoteAddr = "192.168.1.50:54321"
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusOK {
			t.Fatalf("LAN upload probe failed: %d %s", probeRec.Code, probeRec.Body.String())
		}
		var probed probeResponse
		if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
			t.Fatal(err)
		}

		// Local bgPath must still be blocked on LAN.
		localBG, _ := json.Marshal(map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  []string{probed.Tracks[0].ID},
			"bgMode":    "local",
			"bgPath":    "/etc/cover.png",
		})
		localReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(localBG))
		localReq.Header.Set("Content-Type", "application/json")
		localReq.RemoteAddr = "192.168.1.50:54321"
		localRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(localRec, localReq)
		if localRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for LAN local bgPath, got %d: %s", localRec.Code, localRec.Body.String())
		}

		// Path D Auto Visualizer must succeed on LAN without uploading any Background Visual.
		glowFalse := false
		brightness40 := 40
		vizBody, _ := json.Marshal(map[string]any{
			"sessionId":            probed.SessionID,
			"trackIds":             []string{probed.Tracks[0].ID},
			"bgMode":               "visualizer",
			"visualizer":           "black",
			"visualizerTheme":      "oled-ember",
			"visualizerResolution": "1440p",
			"visualizerGlow":       glowFalse,
			"visualizerBrightness": brightness40,
			"fadeToBlackMinutes":   15,
			"overlayTitles":        true,
			"dimTitles":            true,
		})
		vizReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(vizBody))
		vizReq.Header.Set("Content-Type", "application/json")
		vizReq.RemoteAddr = "192.168.1.50:54321"
		vizRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(vizRec, vizReq)
		if vizRec.Code != http.StatusAccepted {
			t.Fatalf("expected 202 for LAN Path D render, got %d: %s", vizRec.Code, vizRec.Body.String())
		}
		var started renderStartResponse
		if err := json.Unmarshal(vizRec.Body.Bytes(), &started); err != nil {
			t.Fatal(err)
		}
		if started.RenderingPath != "Path D" {
			t.Fatalf("RenderingPath = %q, want Path D", started.RenderingPath)
		}

		sseReq := httptest.NewRequest(http.MethodGet, started.EventsURL, nil)
		sseRec := newSSERecorder()
		srv.Handler().ServeHTTP(sseRec, sseReq)
		if !strings.Contains(sseRec.String(), "event: complete") {
			t.Fatalf("expected LAN Path D render complete event, got:\n%s", sseRec.String())
		}
	})
}

func TestNativePickerEndpointAndSessionRoots(t *testing.T) {
	t.Run("POST /api/fs/pick authorizes picked folder and bgFile parent for probe, preview-bg, list, and render", func(t *testing.T) {
		ws := t.TempDir()
		externalAudio := t.TempDir()
		externalArt := t.TempDir()
		externalOut := t.TempDir()

		if err := os.WriteFile(filepath.Join(externalAudio, "01 - Outside.mp3"), []byte("MP3"), 0644); err != nil {
			t.Fatal(err)
		}
		bgFile := filepath.Join(externalArt, "cover.png")
		if err := os.WriteFile(bgFile, []byte("PNG"), 0644); err != nil {
			t.Fatal(err)
		}

		nextPick := ""
		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, map[string]string{"Outside": "5.0"}),
			PickPath: func(_ context.Context, kind, _ string) (string, error) {
				if nextPick == "" {
					return "", ErrPickerCancelled
				}
				return nextPick, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		// Before picking, externalAudio is rejected.
		preProbe := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(externalAudio)+`}`))
		preProbe.Header.Set("Content-Type", "application/json")
		preProbe.RemoteAddr = "127.0.0.1:50000"
		preProbeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(preProbeRec, preProbe)
		if preProbeRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 before picking externalAudio, got %d", preProbeRec.Code)
		}

		// Pick externalAudio folder.
		nextPick = externalAudio
		pickAudioReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder","initialPath":"./tracks"}`))
		pickAudioReq.Header.Set("Content-Type", "application/json")
		pickAudioReq.RemoteAddr = "127.0.0.1:50000"
		pickAudioRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(pickAudioRec, pickAudioReq)
		if pickAudioRec.Code != http.StatusOK {
			t.Fatalf("pick folder failed: %d: %s", pickAudioRec.Code, pickAudioRec.Body.String())
		}
		var pickResp struct {
			Cancelled bool   `json:"cancelled"`
			Path      string `json:"path"`
		}
		if err := json.Unmarshal(pickAudioRec.Body.Bytes(), &pickResp); err != nil {
			t.Fatal(err)
		}
		if pickResp.Cancelled || pickResp.Path == "" {
			t.Fatalf("unexpected pick response: %+v", pickResp)
		}

		// Listing the newly picked externalAudio folder via /api/fs/list now succeeds and includes it in roots.
		listPickedReq := httptest.NewRequest(http.MethodGet, "/api/fs/list?path="+url.QueryEscape(pickResp.Path), nil)
		listPickedReq.RemoteAddr = "127.0.0.1:50000"
		listPickedRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(listPickedRec, listPickedReq)
		if listPickedRec.Code != http.StatusOK || !strings.Contains(listPickedRec.Body.String(), filepath.Base(pickResp.Path)) {
			t.Fatalf("fs/list after pick failed: %d: %s", listPickedRec.Code, listPickedRec.Body.String())
		}

		// Now probing externalAudio succeeds.
		probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(pickResp.Path)+`}`))
		probeReq.Header.Set("Content-Type", "application/json")
		probeReq.RemoteAddr = "127.0.0.1:50000"
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusOK {
			t.Fatalf("probe after pick failed: %d: %s", probeRec.Code, probeRec.Body.String())
		}
		var probed probeResponse
		if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
			t.Fatal(err)
		}

		// Pick bgFile and verify /api/preview-bg succeeds.
		nextPick = bgFile
		pickBGReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"bgFile"}`))
		pickBGReq.Header.Set("Content-Type", "application/json")
		pickBGReq.RemoteAddr = "127.0.0.1:50000"
		pickBGRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(pickBGRec, pickBGReq)
		if pickBGRec.Code != http.StatusOK {
			t.Fatalf("pick bgFile failed: %d: %s", pickBGRec.Code, pickBGRec.Body.String())
		}
		if err := json.Unmarshal(pickBGRec.Body.Bytes(), &pickResp); err != nil {
			t.Fatal(err)
		}
		prevReq := httptest.NewRequest(http.MethodGet, "/api/preview-bg?path="+pickResp.Path, nil)
		prevReq.RemoteAddr = "127.0.0.1:50000"
		prevRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(prevRec, prevReq)
		if prevRec.Code != http.StatusOK {
			t.Fatalf("preview-bg after pick failed: %d: %s", prevRec.Code, prevRec.Body.String())
		}

		// Pick externalOut and verify render writes into it.
		nextPick = externalOut
		pickOutReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		pickOutReq.Header.Set("Content-Type", "application/json")
		pickOutReq.RemoteAddr = "127.0.0.1:50000"
		pickOutRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(pickOutRec, pickOutReq)
		if pickOutRec.Code != http.StatusOK {
			t.Fatalf("pick outDir failed: %d: %s", pickOutRec.Code, pickOutRec.Body.String())
		}
		renderBody, _ := json.Marshal(renderConfigRequest{
			SessionID:  probed.SessionID,
			TrackIDs:   []string{probed.Tracks[0].ID},
			BGMode:     "local",
			BGPath:     bgFile,
			OutDir:     externalOut,
			Out:        "picked.mp4",
			Timestamps: "picked.txt",
		})
		renderReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(renderBody))
		renderReq.Header.Set("Content-Type", "application/json")
		renderReq.RemoteAddr = "127.0.0.1:50000"
		renderRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(renderRec, renderReq)
		if renderRec.Code != http.StatusAccepted {
			t.Fatalf("render to picked outDir failed: %d: %s", renderRec.Code, renderRec.Body.String())
		}
		var started renderStartResponse
		_ = json.Unmarshal(renderRec.Body.Bytes(), &started)
		sseReq := httptest.NewRequest(http.MethodGet, started.EventsURL, nil)
		sseRec := newSSERecorder()
		srv.Handler().ServeHTTP(sseRec, sseReq)
		if !strings.Contains(sseRec.String(), "event: complete") {
			t.Fatalf("expected complete SSE event, got:\n%s", sseRec.String())
		}

		// Cancel returns 200 { cancelled: true }.
		nextPick = ""
		cancelReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		cancelReq.Header.Set("Content-Type", "application/json")
		cancelReq.RemoteAddr = "127.0.0.1:50000"
		cancelRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(cancelRec, cancelReq)
		if cancelRec.Code != http.StatusOK || !strings.Contains(cancelRec.Body.String(), `"cancelled":true`) {
			t.Fatalf("expected cancelled:true, got %d: %s", cancelRec.Code, cancelRec.Body.String())
		}
	})

	t.Run("POST /api/fs/pick enforces guards, fallback 503, unsupported bg ext, and 409 busy", func(t *testing.T) {
		ws := t.TempDir()
		badFile := filepath.Join(ws, "notes.txt")
		if err := os.WriteFile(badFile, []byte("txt"), 0644); err != nil {
			t.Fatal(err)
		}

		startedCh := make(chan struct{})
		releaseCh := make(chan struct{})
		var mode string

		srv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       stubPipelineRunner(t, nil),
			PickPath: func(_ context.Context, _ string, _ string) (string, error) {
				switch mode {
				case "unavailable":
					return "", ErrPickerUnavailable
				case "badfile":
					return badFile, nil
				case "block":
					close(startedCh)
					<-releaseCh
					return ws, nil
				default:
					return ws, nil
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()

		// Non-loopback client gets 403.
		lanReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		lanReq.Header.Set("Content-Type", "application/json")
		lanReq.RemoteAddr = "192.168.1.55:50000"
		lanRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(lanRec, lanReq)
		if lanRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for LAN pick, got %d", lanRec.Code)
		}

		// Cross-origin Origin and Sec-Fetch-Site headers get 403.
		csrfOriginReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		csrfOriginReq.Host = "127.0.0.1:8080"
		csrfOriginReq.RemoteAddr = "127.0.0.1:50000"
		csrfOriginReq.Header.Set("Content-Type", "application/json")
		csrfOriginReq.Header.Set("Origin", "https://evil.example")
		csrfOriginRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(csrfOriginRec, csrfOriginReq)
		if csrfOriginRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for cross-origin Origin on pick, got %d", csrfOriginRec.Code)
		}

		csrfSiteReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		csrfSiteReq.Host = "127.0.0.1:8080"
		csrfSiteReq.RemoteAddr = "127.0.0.1:50000"
		csrfSiteReq.Header.Set("Content-Type", "application/json")
		csrfSiteReq.Header.Set("Sec-Fetch-Site", "cross-site")
		csrfSiteRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(csrfSiteRec, csrfSiteReq)
		if csrfSiteRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for Sec-Fetch-Site cross-site on pick, got %d", csrfSiteRec.Code)
		}

		// Invalid kind gets 400.
		badKindReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"exe"}`))
		badKindReq.Header.Set("Content-Type", "application/json")
		badKindReq.RemoteAddr = "127.0.0.1:50000"
		badKindRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(badKindRec, badKindReq)
		if badKindRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid kind, got %d", badKindRec.Code)
		}

		// Unavailable picker returns 503 with fallback:true.
		mode = "unavailable"
		unavailReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		unavailReq.Header.Set("Content-Type", "application/json")
		unavailReq.RemoteAddr = "127.0.0.1:50000"
		unavailRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(unavailRec, unavailReq)
		if unavailRec.Code != http.StatusServiceUnavailable || !strings.Contains(unavailRec.Body.String(), `"fallback":true`) {
			t.Fatalf("expected 503 fallback:true, got %d: %s", unavailRec.Code, unavailRec.Body.String())
		}

		// Unsupported bgFile extension returns 400.
		mode = "badfile"
		badBGReq := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"bgFile"}`))
		badBGReq.Header.Set("Content-Type", "application/json")
		badBGReq.RemoteAddr = "127.0.0.1:50000"
		badBGRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(badBGRec, badBGReq)
		if badBGRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for unsupported bgFile, got %d: %s", badBGRec.Code, badBGRec.Body.String())
		}

		// Concurrent picker returns 409 Conflict.
		mode = "block"
		done1 := make(chan int, 1)
		go func() {
			r1 := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
			r1.Header.Set("Content-Type", "application/json")
			r1.RemoteAddr = "127.0.0.1:50000"
			w1 := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w1, r1)
			done1 <- w1.Code
		}()
		<-startedCh
		r2 := httptest.NewRequest(http.MethodPost, "/api/fs/pick", strings.NewReader(`{"kind":"folder"}`))
		r2.Header.Set("Content-Type", "application/json")
		r2.RemoteAddr = "127.0.0.1:50000"
		w2 := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w2, r2)
		close(releaseCh)
		if code1 := <-done1; code1 != http.StatusOK {
			t.Fatalf("first concurrent pick = %d, want 200", code1)
		}
		if w2.Code != http.StatusConflict {
			t.Fatalf("second concurrent pick = %d, want 409", w2.Code)
		}
	})
}

func TestEstimateEndpointAndSecurityGuards(t *testing.T) {
	ws := t.TempDir()
	outside := t.TempDir()
	audioDir := filepath.Join(ws, "tracks")
	if err := os.Mkdir(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "01_Track.mp3"), []byte("mp3"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "02_Track.mp3"), []byte("mp3"), 0644); err != nil {
		t.Fatal(err)
	}
	loopBG := filepath.Join(ws, "loop.mp4")
	if err := os.WriteFile(loopBG, []byte("MP4"), 0644); err != nil {
		t.Fatal(err)
	}
	outsideBG := filepath.Join(outside, "escape.mp4")
	if err := os.WriteFile(outsideBG, []byte("MP4"), 0644); err != nil {
		t.Fatal(err)
	}

	var bgProbes atomic.Int32
	baseRunner := stubPipelineRunner(t, map[string]string{"01_Track": "120.0", "02_Track": "180.0"})
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" && len(args) > 0 {
			target := args[len(args)-1]
			if strings.Contains(target, "loop.mp4") {
				bgProbes.Add(1)
				return []byte(`{"format":{"duration":"12.000000"},"streams":[{"codec_name":"h264","width":1920,"height":1080,"r_frame_rate":"30/1","avg_frame_rate":"30/1","nb_frames":"360","pix_fmt":"yuv420p"}]}`), nil
			}
		}
		return baseRunner(ctx, name, args...)
	}

	srv, err := NewServer(Config{
		Host:         "127.0.0.1",
		Port:         8080,
		WorkspaceDir: ws,
		LookPath:     okLookPath,
		Runner:       runner,
		GOOS:         "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Probe tracks into a session.
	probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(audioDir)+`}`))
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.RemoteAddr = "127.0.0.1:50000"
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	if probeRec.Code != http.StatusOK {
		t.Fatalf("probe failed: %d %s", probeRec.Code, probeRec.Body.String())
	}
	var probed probeResponse
	if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}
	trackIDs := []string{probed.Tracks[0].ID, probed.Tracks[1].ID}

	postEstimate := func(remoteAddr, contentType string, headers map[string]string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/estimate", bytes.NewReader(raw))
		req.Host = "127.0.0.1:8080"
		req.RemoteAddr = remoteAddr
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	t.Run("local bgMode probes server-side BG and computes Path A and Path B loop cycles", func(t *testing.T) {
		rec := postEstimate("127.0.0.1:50000", "application/json", nil, map[string]any{
			"sessionId":     probed.SessionID,
			"trackIds":      trackIDs,
			"bgMode":        "local",
			"bgPath":        loopBG,
			"overlayTitles": true,
			"normalize":     true,
			"crossfade":     3.0,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for local estimate, got %d: %s", rec.Code, rec.Body.String())
		}
		var est render.MixEstimate
		if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
			t.Fatal(err)
		}
		if est.Path != render.PathB {
			t.Fatalf("renderingPath = %q, want Path B", est.Path)
		}
		if est.LoopDurationSec != 12.0 || est.LoopFPS != 30.0 || est.LoopCycles <= 0 {
			t.Fatalf("unexpected loop metadata: dur=%v fps=%v cycles=%d", est.LoopDurationSec, est.LoopFPS, est.LoopCycles)
		}
		if est.PointSec <= 0 || est.LowSec <= 0 || est.HighSec <= est.LowSec {
			t.Fatalf("unexpected estimate bounds: %+v", est)
		}
		if est.Weights.Audio <= 0 || est.Weights.Video <= 0 {
			t.Fatalf("expected positive stage weights, got %+v", est.Weights)
		}
		if bgProbes.Load() < 1 {
			t.Fatalf("expected server-side ProbeBackgroundVisual call for local bgPath")
		}
	})

	t.Run("upload bgMode uses client bgMeta without server filesystem access or re-upload", func(t *testing.T) {
		probesBefore := bgProbes.Load()
		rec := postEstimate("127.0.0.1:50000", "application/json", nil, map[string]any{
			"sessionId":     probed.SessionID,
			"trackIds":      trackIDs,
			"bgMode":        "upload",
			"bgName":        "client_loop.mp4",
			"overlayTitles": false,
			"bgMeta": map[string]any{
				"durationSec": 10.0,
				"fps":         60.0,
				"width":       1920,
				"height":      1080,
			},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for upload estimate, got %d: %s", rec.Code, rec.Body.String())
		}
		var est render.MixEstimate
		if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
			t.Fatal(err)
		}
		if est.Path != render.PathA || est.LoopDurationSec != 10.0 || est.LoopFPS != 60.0 || est.LoopCycles != 30 {
			t.Fatalf("unexpected upload Path A estimate: %+v", est)
		}
		if bgProbes.Load() != probesBefore {
			t.Fatalf("upload estimate must not probe server filesystem")
		}
	})

	t.Run("visualizer bgMode (Path D) and trackDurations fallback succeed", func(t *testing.T) {
		glow := false
		rec := postEstimate("127.0.0.1:50000", "application/json", nil, map[string]any{
			"trackDurations":       []float64{60, 120},
			"bgMode":               "visualizer",
			"visualizer":           "bars",
			"visualizerResolution": "1440p",
			"visualizerGlow":       glow,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for visualizer estimate, got %d: %s", rec.Code, rec.Body.String())
		}
		var est render.MixEstimate
		if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
			t.Fatal(err)
		}
		if est.Path != render.PathD || est.PointSec <= 0 {
			t.Fatalf("unexpected Path D estimate: %+v", est)
		}
	})

	t.Run("security guards: same-origin, Content-Type, LAN 403 on local bgPath, path traversal 403, invalid ext/tracks 400", func(t *testing.T) {
		// Cross-origin Origin and Sec-Fetch-Site headers blocked.
		csrfRec := postEstimate("127.0.0.1:50000", "application/json", map[string]string{"Origin": "https://evil.example"}, map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  trackIDs,
			"bgMode":    "visualizer",
		})
		if csrfRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for cross-origin Origin, got %d", csrfRec.Code)
		}

		sfsRec := postEstimate("127.0.0.1:50000", "application/json", map[string]string{"Sec-Fetch-Site": "cross-site"}, map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  trackIDs,
			"bgMode":    "visualizer",
		})
		if sfsRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for Sec-Fetch-Site cross-site, got %d", sfsRec.Code)
		}

		// Non-JSON Content-Type rejected with 415.
		ctRec := postEstimate("127.0.0.1:50000", "text/plain", nil, map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  trackIDs,
		})
		if ctRec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected 415 for text/plain, got %d", ctRec.Code)
		}

		// Path traversal blocked with 403.
		travRec := postEstimate("127.0.0.1:50000", "application/json", nil, map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  trackIDs,
			"bgMode":    "local",
			"bgPath":    outsideBG,
		})
		if travRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for path traversal bgPath, got %d: %s", travRec.Code, travRec.Body.String())
		}

		// Invalid extension rejected with 400.
		extRec := postEstimate("127.0.0.1:50000", "application/json", nil, map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  trackIDs,
			"bgMode":    "local",
			"bgPath":    filepath.Join(ws, "bad.mkv"),
		})
		if extRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for invalid bg ext, got %d: %s", extRec.Code, extRec.Body.String())
		}

		// Empty tracks rejected with 400.
		emptyRec := postEstimate("127.0.0.1:50000", "application/json", nil, map[string]any{
			"sessionId": probed.SessionID,
			"trackIds":  []string{},
			"bgMode":    "visualizer",
		})
		if emptyRec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty tracks, got %d: %s", emptyRec.Code, emptyRec.Body.String())
		}

		// LAN server blocks bgMode: local with 403 while allowing upload & visualizer.
		lanSrv, err := NewServer(Config{
			Host:         "0.0.0.0",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       runner,
			GOOS:         "linux",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer lanSrv.Close()

		lanRaw, _ := json.Marshal(map[string]any{
			"trackDurations": []float64{60},
			"bgMode":         "local",
			"bgPath":         loopBG,
		})
		lanReq := httptest.NewRequest(http.MethodPost, "/api/estimate", bytes.NewReader(lanRaw))
		lanReq.Header.Set("Content-Type", "application/json")
		lanReq.RemoteAddr = "192.168.1.50:50000"
		lanRec := httptest.NewRecorder()
		lanSrv.Handler().ServeHTTP(lanRec, lanReq)
		if lanRec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for LAN local bgPath on /api/estimate, got %d: %s", lanRec.Code, lanRec.Body.String())
		}

		lanVizRaw, _ := json.Marshal(map[string]any{
			"trackDurations": []float64{60},
			"bgMode":         "visualizer",
			"visualizer":     "bars",
		})
		lanVizReq := httptest.NewRequest(http.MethodPost, "/api/estimate", bytes.NewReader(lanVizRaw))
		lanVizReq.Header.Set("Content-Type", "application/json")
		lanVizReq.RemoteAddr = "192.168.1.50:50000"
		lanVizRec := httptest.NewRecorder()
		lanSrv.Handler().ServeHTTP(lanVizRec, lanVizReq)
		if lanVizRec.Code != http.StatusOK {
			t.Fatalf("expected 200 for LAN visualizer /api/estimate, got %d: %s", lanVizRec.Code, lanVizRec.Body.String())
		}
	})

	t.Run("local bgMode returns 200 with fallback default loop assumptions when ffprobe BG probe fails", func(t *testing.T) {
		failingBGProbeRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "ffprobe" && len(args) > 0 && strings.Contains(args[len(args)-1], "loop.mp4") {
				return nil, errors.New("ffprobe failed on corrupt loop header")
			}
			return baseRunner(ctx, name, args...)
		}
		fallbackSrv, err := NewServer(Config{
			Host:         "127.0.0.1",
			Port:         8080,
			WorkspaceDir: ws,
			LookPath:     okLookPath,
			Runner:       failingBGProbeRunner,
			GOOS:         "linux",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer fallbackSrv.Close()

		raw, _ := json.Marshal(map[string]any{
			"trackDurations": []float64{60, 60},
			"bgMode":         "local",
			"bgPath":         loopBG,
			"overlayTitles":  false,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/estimate", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		fallbackSrv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 fallback estimate when ffprobe BG fails, got %d: %s", rec.Code, rec.Body.String())
		}
		var est render.MixEstimate
		if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
			t.Fatal(err)
		}
		if est.Path != render.PathA || est.LoopDurationSec != 0 || est.LoopFPS != 30.0 || est.PointSec <= 0 {
			t.Fatalf("unexpected fallback Path A estimate: %+v", est)
		}
	})
}

func TestRenderSSENormalizeProgressAndCompletionAccuracy(t *testing.T) {
	ws := t.TempDir()
	audioDir := filepath.Join(ws, "tracks")
	if err := os.Mkdir(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "01_A.mp3"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "02_B.mp3"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	bgPath := filepath.Join(ws, "cover.png")
	if err := os.WriteFile(bgPath, []byte("PNG"), 0644); err != nil {
		t.Fatal(err)
	}

	baseRunner := stubPipelineRunner(t, map[string]string{"01_A": "30.0", "02_B": "40.0"})
	streamRunner := func(ctx context.Context, onLine func(string), name string, args ...string) error {
		if _, err := baseRunner(ctx, name, args...); err != nil {
			return err
		}
		if onLine != nil {
			onLine("out_time_us=10000000")
			onLine("speed=10.0x")
			onLine("progress=continue")
			onLine("out_time_us=70000000")
			onLine("speed=10.0x")
			onLine("progress=end")
		}
		return nil
	}

	srv, err := NewServer(Config{
		Host:         "127.0.0.1",
		Port:         8080,
		WorkspaceDir: ws,
		LookPath:     okLookPath,
		Runner:       baseRunner,
		StreamRunner: streamRunner,
		GOOS:         "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", strings.NewReader(`{"audioDir":`+strconvQuote(audioDir)+`}`))
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.RemoteAddr = "127.0.0.1:50000"
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	if probeRec.Code != http.StatusOK {
		t.Fatalf("probe failed: %d %s", probeRec.Code, probeRec.Body.String())
	}
	var probed probeResponse
	if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}

	renderBody, _ := json.Marshal(map[string]any{
		"sessionId":  probed.SessionID,
		"trackIds":   []string{probed.Tracks[0].ID, probed.Tracks[1].ID},
		"bgMode":     "local",
		"bgPath":     bgPath,
		"normalize":  true,
		"out":        filepath.Join(ws, "out.mp4"),
		"timestamps": filepath.Join(ws, "ts.txt"),
	})
	renderReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(renderBody))
	renderReq.Header.Set("Content-Type", "application/json")
	renderReq.RemoteAddr = "127.0.0.1:50000"
	renderRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(renderRec, renderReq)
	if renderRec.Code != http.StatusAccepted {
		t.Fatalf("render failed: %d %s", renderRec.Code, renderRec.Body.String())
	}
	var started renderStartResponse
	_ = json.Unmarshal(renderRec.Body.Bytes(), &started)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	evReq := httptest.NewRequest(http.MethodGet, started.EventsURL, nil).WithContext(ctx)
	evRec := newSSERecorder()
	srv.Handler().ServeHTTP(evRec, evReq)

	raw := evRec.String()
	if !strings.Contains(raw, `"substage":"normalize"`) {
		t.Errorf("expected Step 2 normalize SSE progress event, got:\n%s", raw)
	}
	if !strings.Contains(raw, `"estimatedSec":`) || !strings.Contains(raw, `"actualSec":`) {
		t.Errorf("expected estimatedSec and actualSec in complete event, got:\n%s", raw)
	}
}

func makeTestPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 62, G: 230, B: 181, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSessionTrackMediaEndpointsAndSecurity(t *testing.T) {
	ws := t.TempDir()
	audioDir := filepath.Join(ws, "tracks")
	if err := os.Mkdir(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "01_Sidecar.mp3"), []byte("mp3-1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "02_Embedded.mp3"), []byte("mp3-2"), 0644); err != nil {
		t.Fatal(err)
	}
	squarePNG := makeTestPNGBytes(t, 40, 40)
	widePNG := makeTestPNGBytes(t, 160, 90)
	if err := os.WriteFile(filepath.Join(audioDir, "01_Sidecar.png"), squarePNG, 0644); err != nil {
		t.Fatal(err)
	}
	localExtra := filepath.Join(ws, "local_wide.png")
	if err := os.WriteFile(localExtra, widePNG, 0644); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outsideFile, squarePNG, 0644); err != nil {
		t.Fatal(err)
	}

	var id3Extractions atomic.Int32
	baseRunner := stubPipelineRunner(t, nil)
	mediaRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffprobe" {
			target := args[len(args)-1]
			if strings.Contains(target, "02_Embedded") || strings.Contains(target, "track_0001") {
				return []byte(`{"format":{"duration":"12.0","tags":{"title":"Embedded Track"}},"streams":[{"index":1,"codec_type":"video","codec_name":"png","width":500,"height":500,"disposition":{"attached_pic":1}}]}`), nil
			}
			return []byte(`{"format":{"duration":"10.0","tags":{"title":"Sidecar Track"}},"streams":[]}`), nil
		}
		if name == "ffmpeg" && len(args) > 0 && strings.Contains(strings.Join(args, " "), "-frames:v 1") {
			id3Extractions.Add(1)
			outPath := args[len(args)-1]
			if err := os.WriteFile(outPath, squarePNG, 0600); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return baseRunner(ctx, name, args...)
	}

	srv, err := NewServer(Config{
		Host:           "127.0.0.1",
		Port:           8080,
		WorkspaceDir:   ws,
		LookPath:       okLookPath,
		Runner:         mediaRunner,
		MaxUploadBytes: 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// 1. Local probe detects sidecar + ID3 art without leaking filesystem paths.
	probeBody, _ := json.Marshal(map[string]any{"audioDir": audioDir})
	probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", bytes.NewReader(probeBody))
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.RemoteAddr = "127.0.0.1:50000"
	probeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(probeRec, probeReq)
	if probeRec.Code != http.StatusOK {
		t.Fatalf("probe failed: %d %s", probeRec.Code, probeRec.Body.String())
	}
	if strings.Contains(probeRec.Body.String(), ws) {
		t.Fatalf("probe response leaked workspace path: %s", probeRec.Body.String())
	}
	var probed probeResponse
	if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
		t.Fatal(err)
	}
	if len(probed.Tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(probed.Tracks))
	}
	t1, t2 := probed.Tracks[0], probed.Tracks[1]
	if t1.DefaultMediaSource != "sidecar" || t1.SidecarName != "01_Sidecar.png" || t1.SidecarKind != "image" || t1.DefaultLayerMode != "card" || t1.PreviewURL == "" {
		t.Fatalf("unexpected sidecar track DTO: %+v", t1)
	}
	if !t2.HasEmbeddedArt || t2.EmbeddedArtWidth != 500 || t2.EmbeddedArtHeight != 500 || t2.DefaultMediaSource != "id3" || t2.DefaultLayerMode != "card" || t2.PreviewURL == "" {
		t.Fatalf("unexpected ID3 track DTO: %+v", t2)
	}

	// 2. GET sidecar preview + GET ID3 preview (cached after first extraction) with nosniff + no-store.
	for _, previewURL := range []string{t1.PreviewURL, t2.PreviewURL, t2.PreviewURL} {
		getReq := httptest.NewRequest(http.MethodGet, previewURL, nil)
		getReq.RemoteAddr = "127.0.0.1:50000"
		getRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(getRec, getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", previewURL, getRec.Code, getRec.Body.String())
		}
		if got := getRec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("GET %s X-Content-Type-Options = %q, want nosniff", previewURL, got)
		}
		if got := getRec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q, want no-store", previewURL, got)
		}
	}
	if got := id3Extractions.Load(); got != 1 {
		t.Fatalf("expected ID3 cover extracted once and cached, got %d extractions", got)
	}

	// 3. Cross-origin request blocked with 403.
	crossReq := httptest.NewRequest(http.MethodGet, t1.PreviewURL, nil)
	crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossReq.RemoteAddr = "127.0.0.1:50000"
	crossRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on cross-site GET media, got %d", crossRec.Code)
	}

	// 4. POST multipart upload validation (rejects .exe -> 400, accepts .png -> 200).
	postMediaMultipart := func(fileName string, content []byte, layerMode string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if layerMode != "" {
			_ = mw.WriteField("layerMode", layerMode)
		}
		part, _ := mw.CreateFormFile("file", fileName)
		_, _ = part.Write(content)
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, t1.PreviewURL, &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	if badRec := postMediaMultipart("evil.exe", []byte("MZ"), ""); badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for .exe upload, got %d: %s", badRec.Code, badRec.Body.String())
	}

	upRec := postMediaMultipart("custom_wide.png", widePNG, "")
	if upRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for PNG upload, got %d: %s", upRec.Code, upRec.Body.String())
	}
	var upResp trackMediaResponse
	if err := json.Unmarshal(upRec.Body.Bytes(), &upResp); err != nil {
		t.Fatal(err)
	}
	if upResp.Source != "upload" || upResp.MediaName != "custom_wide.png" || upResp.MediaKind != "image" || upResp.LayerMode != "fullscreen" {
		t.Fatalf("unexpected upload response: %+v", upResp)
	}

	// Serve uploaded PNG bytes.
	getUpReq := httptest.NewRequest(http.MethodGet, upResp.PreviewURL, nil)
	getUpReq.RemoteAddr = "127.0.0.1:50000"
	getUpRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getUpRec, getUpReq)
	if getUpRec.Code != http.StatusOK || !bytes.Equal(getUpRec.Body.Bytes(), widePNG) {
		t.Fatalf("GET uploaded media status=%d, len=%d (want %d)", getUpRec.Code, getUpRec.Body.Len(), len(widePNG))
	}

	// 5. Local path binding: allowed inside workspace -> 200; outside -> 403.
	outsideReq := httptest.NewRequest(http.MethodPost, t1.PreviewURL, strings.NewReader(`{"localPath":`+strconvQuote(outsideFile)+`}`))
	outsideReq.Header.Set("Content-Type", "application/json")
	outsideReq.RemoteAddr = "127.0.0.1:50000"
	outsideRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(outsideRec, outsideReq)
	if outsideRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for outside localPath, got %d", outsideRec.Code)
	}

	localReq := httptest.NewRequest(http.MethodPost, t1.PreviewURL, strings.NewReader(`{"localPath":`+strconvQuote(localExtra)+`,"layerMode":"card"}`))
	localReq.Header.Set("Content-Type", "application/json")
	localReq.RemoteAddr = "127.0.0.1:50000"
	localRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(localRec, localReq)
	if localRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowed localPath, got %d: %s", localRec.Code, localRec.Body.String())
	}

	// 6. DELETE ?mode=backdrop sets explicitEmpty, then DELETE reverts to sidecar default.
	delBackdropReq := httptest.NewRequest(http.MethodDelete, t1.PreviewURL+"?mode=backdrop", nil)
	delBackdropReq.RemoteAddr = "127.0.0.1:50000"
	delBackdropRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(delBackdropRec, delBackdropReq)
	if delBackdropRec.Code != http.StatusOK {
		t.Fatalf("DELETE ?mode=backdrop status = %d", delBackdropRec.Code)
	}
	var delBackdropResp trackMediaResponse
	_ = json.Unmarshal(delBackdropRec.Body.Bytes(), &delBackdropResp)
	if !delBackdropResp.ExplicitEmpty || delBackdropResp.LayerMode != "backdrop" || delBackdropResp.PreviewURL != "" {
		t.Fatalf("unexpected DELETE ?mode=backdrop response: %+v", delBackdropResp)
	}

	// GET after backdrop delete returns 404.
	emptyGetReq := httptest.NewRequest(http.MethodGet, t1.PreviewURL, nil)
	emptyGetReq.RemoteAddr = "127.0.0.1:50000"
	emptyGetRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(emptyGetRec, emptyGetReq)
	if emptyGetRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when track is explicitEmpty, got %d", emptyGetRec.Code)
	}

	// DELETE (revert to default sidecar).
	revertReq := httptest.NewRequest(http.MethodDelete, t1.PreviewURL, nil)
	revertReq.RemoteAddr = "127.0.0.1:50000"
	revertRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(revertRec, revertReq)
	if revertRec.Code != http.StatusOK {
		t.Fatalf("DELETE revert status = %d", revertRec.Code)
	}
	var revertResp trackMediaResponse
	_ = json.Unmarshal(revertRec.Body.Bytes(), &revertResp)
	if revertResp.Source != "sidecar" || revertResp.LayerMode != "card" || revertResp.PreviewURL == "" {
		t.Fatalf("unexpected revert response: %+v", revertResp)
	}

	// 7. Non-loopback 0.0.0.0 server: upload + ID3 preview work, localPath binding returns 403.
	lanSrv, err := NewServer(Config{
		Host:           "0.0.0.0",
		Port:           8080,
		WorkspaceDir:   ws,
		LookPath:       okLookPath,
		Runner:         mediaRunner,
		MaxUploadBytes: 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lanSrv.Close()

	lanProbeReq, _ := newMultipartProbeRequest(t, map[string][]byte{"01_Lan.mp3": []byte("mp3")}, []string{"01_Lan.mp3"})
	lanProbeReq.RemoteAddr = "192.168.1.42:51000"
	lanProbeRec := httptest.NewRecorder()
	lanSrv.Handler().ServeHTTP(lanProbeRec, lanProbeReq)
	if lanProbeRec.Code != http.StatusOK {
		t.Fatalf("LAN upload probe failed: %d %s", lanProbeRec.Code, lanProbeRec.Body.String())
	}
	var lanProbed probeResponse
	_ = json.Unmarshal(lanProbeRec.Body.Bytes(), &lanProbed)
	if len(lanProbed.Tracks) != 1 || !lanProbed.Tracks[0].HasEmbeddedArt || lanProbed.Tracks[0].PreviewURL == "" {
		t.Fatalf("unexpected LAN probe response: %+v", lanProbed)
	}

	lanLocalReq := httptest.NewRequest(http.MethodPost, lanProbed.Tracks[0].PreviewURL, strings.NewReader(`{"localPath":`+strconvQuote(localExtra)+`}`))
	lanLocalReq.Header.Set("Content-Type", "application/json")
	lanLocalReq.RemoteAddr = "192.168.1.42:51000"
	lanLocalRec := httptest.NewRecorder()
	lanSrv.Handler().ServeHTTP(lanLocalRec, lanLocalReq)
	if lanLocalRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for LAN localPath track media bind, got %d: %s", lanLocalRec.Code, lanLocalRec.Body.String())
	}

	// LAN ID3 preview extraction works without leaking local paths.
	lanID3Req := httptest.NewRequest(http.MethodGet, lanProbed.Tracks[0].PreviewURL, nil)
	lanID3Req.RemoteAddr = "192.168.1.42:51000"
	lanID3Rec := httptest.NewRecorder()
	lanSrv.Handler().ServeHTTP(lanID3Rec, lanID3Req)
	if lanID3Rec.Code != http.StatusOK || !bytes.Equal(lanID3Rec.Body.Bytes(), squarePNG) {
		t.Fatalf("LAN GET ID3 preview status=%d, bodyLen=%d", lanID3Rec.Code, lanID3Rec.Body.Len())
	}
}
