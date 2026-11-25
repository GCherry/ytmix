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

	"github.com/GCherry/ytmix/internal/render"
)

type failingSSEWriter struct {
	mu       sync.Mutex
	header   http.Header
	writes   int
	failAt   int
	flushes  int
	buf      bytes.Buffer
	writeErr error
}

func (f *failingSSEWriter) Header() http.Header {
	if f.header == nil {
		f.header = make(http.Header)
	}
	return f.header
}

func (f *failingSSEWriter) WriteHeader(int) {}

func (f *failingSSEWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.failAt > 0 && f.writes >= f.failAt {
		return 0, f.writeErr
	}
	return f.buf.Write(p)
}

func (f *failingSSEWriter) Flush() {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
}

func TestConsecutiveLocalRendersPreservePriorJobArtifacts(t *testing.T) {
	ws := t.TempDir()
	audioDir1 := filepath.Join(ws, "set1")
	audioDir2 := filepath.Join(ws, "set2")
	for _, dir := range []string{audioDir1, audioDir2} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(audioDir1, "01_Alpha.mp3"), []byte("a1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir2, "01_Beta.mp3"), []byte("b1"), 0644); err != nil {
		t.Fatal(err)
	}
	bgPath := filepath.Join(ws, "cover.png")
	if err := os.WriteFile(bgPath, []byte("PNG"), 0644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	payloadLabel := "MP4_JOB_1"
	baseRunner := stubPipelineRunner(t, map[string]string{"01_Alpha": "10.0", "01_Beta": "12.0"})
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffmpeg" && len(args) > 0 && strings.HasSuffix(strings.ToLower(args[len(args)-1]), ".mp4") {
			mu.Lock()
			data := payloadLabel
			mu.Unlock()
			if err := os.WriteFile(args[len(args)-1], []byte(data), 0600); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return baseRunner(ctx, name, args...)
	}

	srv, err := NewServer(Config{
		Host:         "127.0.0.1",
		WorkspaceDir: ws,
		LookPath:     okLookPath,
		Runner:       runner,
		GOOS:         "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	runLocalRender := func(dir string) renderStartResponse {
		t.Helper()
		probeBody, _ := json.Marshal(map[string]any{"audioDir": dir})
		probeReq := httptest.NewRequest(http.MethodPost, "/api/probe", bytes.NewReader(probeBody))
		probeReq.Header.Set("Content-Type", "application/json")
		probeReq.RemoteAddr = "127.0.0.1:50000"
		probeRec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(probeRec, probeReq)
		if probeRec.Code != http.StatusOK {
			t.Fatalf("probe %s failed: %d %s", dir, probeRec.Code, probeRec.Body.String())
		}
		var probed probeResponse
		if err := json.Unmarshal(probeRec.Body.Bytes(), &probed); err != nil {
			t.Fatal(err)
		}

		renderBody, _ := json.Marshal(renderConfigRequest{
			SessionID:  probed.SessionID,
			TrackIDs:   []string{probed.Tracks[0].ID},
			BGPath:     bgPath,
			Out:        "output.mp4",
			Timestamps: "timestamps.txt",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(renderBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("render %s failed: %d %s", dir, rec.Code, rec.Body.String())
		}
		var started renderStartResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
			t.Fatal(err)
		}

		sseReq := httptest.NewRequest(http.MethodGet, started.EventsURL, nil)
		sseRec := newSSERecorder()
		srv.Handler().ServeHTTP(sseRec, sseReq)
		if !strings.Contains(sseRec.String(), "event: complete") {
			t.Fatalf("expected complete SSE for %s, got:\n%s", dir, sseRec.String())
		}
		return started
	}

	job1 := runLocalRender(audioDir1)

	mu.Lock()
	payloadLabel = "MP4_JOB_2"
	mu.Unlock()

	job2 := runLocalRender(audioDir2)

	// Disk target holds the latest render (Job 2).
	diskMP4, err := os.ReadFile(filepath.Join(ws, "output.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(diskMP4) != "MP4_JOB_2" {
		t.Fatalf("disk output.mp4 = %q, want MP4_JOB_2", string(diskMP4))
	}

	// Job 1 download endpoints must still serve Job 1's exact artifacts!
	out1Req := httptest.NewRequest(http.MethodGet, "/api/render/"+job1.JobID+"/output", nil)
	out1Rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out1Rec, out1Req)
	if out1Rec.Code != http.StatusOK || out1Rec.Body.String() != "MP4_JOB_1" {
		t.Fatalf("job1 output = (%d, %q), want (200, MP4_JOB_1)", out1Rec.Code, out1Rec.Body.String())
	}

	ts1Req := httptest.NewRequest(http.MethodGet, "/api/render/"+job1.JobID+"/timestamps", nil)
	ts1Rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(ts1Rec, ts1Req)
	if ts1Rec.Code != http.StatusOK || ts1Rec.Body.String() != "00:00 Alpha\n" {
		t.Fatalf("job1 timestamps = (%d, %q), want (200, \"00:00 Alpha\\n\")", ts1Rec.Code, ts1Rec.Body.String())
	}

	// Job 2 download endpoints must serve Job 2's exact artifacts.
	out2Req := httptest.NewRequest(http.MethodGet, "/api/render/"+job2.JobID+"/output", nil)
	out2Rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out2Rec, out2Req)
	if out2Rec.Code != http.StatusOK || out2Rec.Body.String() != "MP4_JOB_2" {
		t.Fatalf("job2 output = (%d, %q), want (200, MP4_JOB_2)", out2Rec.Code, out2Rec.Body.String())
	}
}

func TestHandleRenderEventsStopsOnDisconnectAndWriteErrors(t *testing.T) {
	srv, err := NewServer(Config{
		Host:         "127.0.0.1",
		WorkspaceDir: t.TempDir(),
		LookPath:     okLookPath,
		Runner:       stubPipelineRunner(t, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	t.Run("stops replay immediately on write error", func(t *testing.T) {
		job := newRenderJob("job_err", t.TempDir(), "out.mp4", "out.mp4", "ts.txt", "ts.txt", "Path C", func() {})
		for i := 0; i < 10; i++ {
			job.emit("log", map[string]any{"index": i})
		}
		job.finish(jobComplete, "", "complete", map[string]any{"jobId": job.id})
		if err := srv.store.claimRenderSlot(job, ""); err != nil {
			t.Fatal(err)
		}

		fw := &failingSSEWriter{failAt: 2, writeErr: errors.New("broken pipe")}
		req := httptest.NewRequest(http.MethodGet, "/api/render/"+job.id+"/events", nil)
		srv.Handler().ServeHTTP(fw, req)

		if fw.writes > 2 {
			t.Fatalf("expected SSE replay to stop immediately on write error, got %d writes", fw.writes)
		}
		if fw.flushes != 0 {
			t.Fatalf("expected no flush after failed replay write, got %d flushes", fw.flushes)
		}
	})

	t.Run("stops history replay immediately when request context is already canceled", func(t *testing.T) {
		job := newRenderJob("job_ctx", t.TempDir(), "out.mp4", "out.mp4", "ts.txt", "ts.txt", "Path C", func() {})
		for i := 0; i < 5; i++ {
			job.emit("log", map[string]any{"index": i})
		}
		if err := srv.store.claimRenderSlot(job, ""); err != nil {
			t.Fatal(err)
		}
		defer srv.store.releaseActive(job.id)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		rec := newSSERecorder()
		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			req := httptest.NewRequest(http.MethodGet, "/api/render/"+job.id+"/events", nil).WithContext(ctx)
			srv.Handler().ServeHTTP(rec, req)
		}()

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("handleRenderEvents did not exit promptly on canceled context")
		}

		if rec.String() != "" {
			t.Fatalf("expected 0 replay writes for pre-canceled context, got:\n%s", rec.String())
		}
	})

	t.Run("stops live stream immediately on write error", func(t *testing.T) {
		job := newRenderJob("job_live_err", t.TempDir(), "out.mp4", "out.mp4", "ts.txt", "ts.txt", "Path C", func() {})
		if err := srv.store.claimRenderSlot(job, ""); err != nil {
			t.Fatal(err)
		}
		defer srv.store.releaseActive(job.id)

		fw := &failingSSEWriter{failAt: 1, writeErr: errors.New("client disconnected")}
		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			req := httptest.NewRequest(http.MethodGet, "/api/render/"+job.id+"/events", nil)
			srv.Handler().ServeHTTP(fw, req)
		}()

		// Give handler time to subscribe before emitting live event.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			job.mu.Lock()
			subs := len(job.subs)
			job.mu.Unlock()
			if subs > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		job.emit("step", map[string]any{"step": 1, "total": 5})

		select {
		case <-doneCh:
		case <-time.After(2 * time.Second):
			t.Fatal("handleRenderEvents did not exit on live stream write error")
		}
		if fw.flushes != 1 {
			t.Fatalf("expected only initial replay flush before live write error, got %d flushes", fw.flushes)
		}
	})
}

func TestCopyFileToDirViaOSRoot(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "output.mp4")
	if err := os.WriteFile(srcPath, []byte("COPY_FALLBACK_BYTES"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyFileToDir(srcPath, dstDir, "mix.mp4"); err != nil {
		t.Fatalf("copyFileToDir failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dstDir, "mix.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "COPY_FALLBACK_BYTES" {
		t.Fatalf("copied content = %q, want COPY_FALLBACK_BYTES", string(got))
	}
}

func TestAppJSIncludesHoursInTrackTableWhenMixAtLeastOneHour(t *testing.T) {
	srv, err := NewServer(Config{
		Host:         "127.0.0.1",
		WorkspaceDir: t.TempDir(),
		LookPath:     okLookPath,
		Runner:       stubPipelineRunner(t, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	req := httptest.NewRequest(http.MethodGet, "/app.js", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app.js status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "const includeHours = sched.totalDuration >= 3600;") ||
		!strings.Contains(body, "formatTime(sched.starts[idx] || 0, includeHours)") {
		t.Fatalf("expected renderTracksTable in app.js to format Track Start times with includeHours")
	}
}

func TestEstimateAndRenderSegmentedAndBlackModes(t *testing.T) {
	ws := t.TempDir()
	squarePNG := makeTestPNGBytes(t, 32, 32)

	var (
		mu         sync.Mutex
		ffmpegArgs [][]string
	)
	baseRunner := stubPipelineRunner(t, map[string]string{"track_0001": "10.0", "track_0002": "12.0"})
	segRunner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ffmpeg" {
			mu.Lock()
			ffmpegArgs = append(ffmpegArgs, append([]string(nil), args...))
			mu.Unlock()
		}
		return baseRunner(ctx, name, args...)
	}
	segStreamRunner := func(ctx context.Context, onLine func(string), name string, args ...string) error {
		if _, err := segRunner(ctx, name, args...); err != nil {
			return err
		}
		if onLine != nil {
			onLine("out_time_us=10000000")
			onLine("speed=10.0x")
			onLine("progress=end")
		}
		return nil
	}

	srv, err := NewServer(Config{
		Host:           "127.0.0.1",
		WorkspaceDir:   ws,
		LookPath:       okLookPath,
		Runner:         segRunner,
		StreamRunner:   segStreamRunner,
		GOOS:           "linux",
		MaxUploadBytes: 5 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// 1. Probe two uploaded tracks.
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
	t1ID, t2ID := probed.Tracks[0].ID, probed.Tracks[1].ID

	// 2. Upload PNG for Track 1.
	var upBuf bytes.Buffer
	upMW := multipart.NewWriter(&upBuf)
	part, _ := upMW.CreateFormFile("file", "art1.png")
	_, _ = part.Write(squarePNG)
	_ = upMW.Close()
	upReq := httptest.NewRequest(http.MethodPost, "/api/sessions/"+probed.SessionID+"/tracks/"+t1ID+"/media", &upBuf)
	upReq.Header.Set("Content-Type", upMW.FormDataContentType())
	upReq.RemoteAddr = "127.0.0.1:50000"
	upRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusOK {
		t.Fatalf("track media upload failed: %d %s", upRec.Code, upRec.Body.String())
	}

	// 3. Table-driven /api/estimate tests across black, visualizer, upload, and invalid inputs.
	for _, tc := range []struct {
		name     string
		payload  map[string]any
		wantCode int
		wantPath render.Path
	}{
		{
			name: "non-segmented bgMode black",
			payload: map[string]any{
				"sessionId":     probed.SessionID,
				"trackIds":      []string{t1ID, t2ID},
				"bgMode":        "black",
				"overlayTitles": true,
			},
			wantCode: http.StatusOK,
			wantPath: render.PathD,
		},
		{
			name: "segmented layer0Mode black with trackOverrides and dip",
			payload: map[string]any{
				"sessionId":       probed.SessionID,
				"trackIds":        []string{t1ID, t2ID},
				"layer0Mode":      "black",
				"segmented":       true,
				"trackTransition": "dip",
				"trackOverrides": []map[string]any{
					{"trackId": t1ID, "layerMode": "card", "source": "upload"},
					{"trackId": t2ID, "layerMode": "backdrop", "source": "backdrop"},
				},
			},
			wantCode: http.StatusOK,
			wantPath: render.PathD,
		},
		{
			name: "segmented layer0Mode visualizer with trackOverrides",
			payload: map[string]any{
				"sessionId":  probed.SessionID,
				"trackIds":   []string{t1ID, t2ID},
				"layer0Mode": "visualizer",
				"visualizer": "bars",
				"segmented":  true,
				"trackOverrides": []map[string]any{
					{"trackId": t1ID, "layerMode": "card", "source": "upload"},
					{"trackId": t2ID, "layerMode": "backdrop", "source": "backdrop"},
				},
			},
			wantCode: http.StatusOK,
			wantPath: render.PathD,
		},
		{
			name: "segmented layer0Mode upload with loop.mp4",
			payload: map[string]any{
				"sessionId":  probed.SessionID,
				"trackIds":   []string{t1ID, t2ID},
				"layer0Mode": "upload",
				"bgName":     "loop.mp4",
				"segmented":  true,
				"trackOverrides": []map[string]any{
					{"trackId": t1ID, "layerMode": "fullscreen", "source": "upload"},
					{"trackId": t2ID, "layerMode": "backdrop", "source": "backdrop"},
				},
			},
			wantCode: http.StatusOK,
			wantPath: render.PathA,
		},
		{
			name: "invalid layer0Mode returns 400",
			payload: map[string]any{
				"sessionId":  probed.SessionID,
				"trackIds":   []string{t1ID, t2ID},
				"layer0Mode": "neon",
			},
			wantCode: http.StatusBadRequest,
		},
		{
			name: "unknown trackId in trackOverrides returns 400",
			payload: map[string]any{
				"sessionId":  probed.SessionID,
				"trackIds":   []string{t1ID, t2ID},
				"layer0Mode": "black",
				"segmented":  true,
				"trackOverrides": []map[string]any{
					{"trackId": "trk_999", "layerMode": "backdrop", "source": "backdrop"},
				},
			},
			wantCode: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.payload)
			req := httptest.NewRequest(http.MethodPost, "/api/estimate", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "127.0.0.1:50000"
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				var est render.MixEstimate
				if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
					t.Fatal(err)
				}
				if est.Path != tc.wantPath || est.PointSec <= 0 {
					t.Fatalf("unexpected estimate: %+v", est)
				}
			}
		})
	}

	// 4. Segmented POST /api/render with layer0Mode=black + trackOverrides + dip transition.
	renderBody, _ := json.Marshal(map[string]any{
		"sessionId":        probed.SessionID,
		"trackIds":         []string{t1ID, t2ID},
		"layer0Mode":       "black",
		"segmented":        true,
		"trackTransition":  "dip",
		"trackMediaLayout": "auto",
		"overlayTitles":    true,
		"out":              "seg_black.mp4",
		"timestamps":       "seg_black.txt",
		"trackOverrides": []map[string]any{
			{"trackId": t1ID, "layerMode": "card", "source": "upload"},
			{"trackId": t2ID, "layerMode": "backdrop", "source": "backdrop"},
		},
	})
	renderReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(renderBody))
	renderReq.Header.Set("Content-Type", "application/json")
	renderReq.RemoteAddr = "127.0.0.1:50000"
	renderRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(renderRec, renderReq)
	if renderRec.Code != http.StatusAccepted {
		t.Fatalf("segmented render failed: %d %s", renderRec.Code, renderRec.Body.String())
	}
	var started renderStartResponse
	if err := json.Unmarshal(renderRec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if !started.Segmented || started.RenderingPath != "Path D" {
		t.Fatalf("unexpected render start response: %+v", started)
	}

	sseReq := httptest.NewRequest(http.MethodGet, started.EventsURL, nil)
	sseRec := newSSERecorder()
	srv.Handler().ServeHTTP(sseRec, sseReq)
	if !strings.Contains(sseRec.String(), "event: complete") {
		t.Fatalf("expected complete SSE event for segmented render, got:\n%s", sseRec.String())
	}

	mu.Lock()
	var sawConcat bool
	for _, args := range ffmpegArgs {
		if strings.Contains(strings.Join(args, " "), "-f concat") {
			sawConcat = true
			break
		}
	}
	mu.Unlock()
	if !sawConcat {
		t.Fatalf("expected Option A segment concat ffmpeg command, got %v", ffmpegArgs)
	}

	// 5. Segmented multipart upload BG + trackOverrides.
	var mpBuf bytes.Buffer
	mw := multipart.NewWriter(&mpBuf)
	mpCfg, _ := json.Marshal(map[string]any{
		"sessionId":  probed.SessionID,
		"trackIds":   []string{t1ID, t2ID},
		"layer0Mode": "upload",
		"segmented":  true,
		"out":        "seg_upload.mp4",
		"timestamps": "seg_upload.txt",
		"trackOverrides": []map[string]any{
			{"trackId": t1ID, "layerMode": "card", "source": "upload"},
			{"trackId": t2ID, "layerMode": "backdrop", "source": "backdrop"},
		},
	})
	_ = mw.WriteField("config", string(mpCfg))
	bgPart, _ := mw.CreateFormFile("bg", "l0.png")
	_, _ = bgPart.Write(squarePNG)
	_ = mw.Close()

	mpReq := httptest.NewRequest(http.MethodPost, "/api/render", &mpBuf)
	mpReq.Header.Set("Content-Type", mw.FormDataContentType())
	mpReq.RemoteAddr = "127.0.0.1:50000"
	mpRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(mpRec, mpReq)
	if mpRec.Code != http.StatusAccepted {
		t.Fatalf("multipart segmented render failed: %d %s", mpRec.Code, mpRec.Body.String())
	}
	var mpStarted renderStartResponse
	_ = json.Unmarshal(mpRec.Body.Bytes(), &mpStarted)
	mpSSE := newSSERecorder()
	srv.Handler().ServeHTTP(mpSSE, httptest.NewRequest(http.MethodGet, mpStarted.EventsURL, nil))
	if !strings.Contains(mpSSE.String(), "event: complete") {
		t.Fatalf("expected complete SSE for multipart segmented render, got:\n%s", mpSSE.String())
	}

	// 6. Non-loopback 0.0.0.0 rejects trackOverrides with source=local -> 403 Forbidden.
	lanSrv, err := NewServer(Config{
		Host:         "0.0.0.0",
		WorkspaceDir: ws,
		LookPath:     okLookPath,
		Runner:       segRunner,
		StreamRunner: segStreamRunner,
		GOOS:         "linux",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lanSrv.Close()

	lanProbeReq, _ := newMultipartProbeRequest(t, map[string][]byte{"01.mp3": []byte("a")}, []string{"01.mp3"})
	lanProbeReq.RemoteAddr = "192.168.1.50:51000"
	lanProbeRec := httptest.NewRecorder()
	lanSrv.Handler().ServeHTTP(lanProbeRec, lanProbeReq)
	var lanProbed probeResponse
	_ = json.Unmarshal(lanProbeRec.Body.Bytes(), &lanProbed)

	lanRenderBody, _ := json.Marshal(map[string]any{
		"sessionId":  lanProbed.SessionID,
		"trackIds":   []string{lanProbed.Tracks[0].ID},
		"layer0Mode": "black",
		"segmented":  true,
		"trackOverrides": []map[string]any{
			{"trackId": lanProbed.Tracks[0].ID, "layerMode": "card", "source": "local"},
		},
	})
	lanRenderReq := httptest.NewRequest(http.MethodPost, "/api/render", bytes.NewReader(lanRenderBody))
	lanRenderReq.Header.Set("Content-Type", "application/json")
	lanRenderReq.RemoteAddr = "192.168.1.50:51000"
	lanRenderRec := httptest.NewRecorder()
	lanSrv.Handler().ServeHTTP(lanRenderRec, lanRenderReq)
	if lanRenderRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for LAN render with source=local override, got %d: %s", lanRenderRec.Code, lanRenderRec.Body.String())
	}
}
