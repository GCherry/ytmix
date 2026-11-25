package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/mixer"
)

func readResourceRPC(t *testing.T, reg *Registry, uri string) ([]ResourceContent, *RPCError, string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/read",
		"params":  map[string]any{"uri": uri},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := reg.HandleMessage(context.Background(), payload)
	var env struct {
		Result struct {
			Contents []ResourceContent `json:"contents"`
		} `json:"result"`
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal resources/read: %v (%s)", err, raw)
	}
	return env.Result.Contents, env.Error, string(raw)
}

func listResourcesRPC(t *testing.T, reg *Registry) []Resource {
	t.Helper()
	raw := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
	var env struct {
		Result struct {
			Resources []Resource `json:"resources"`
		} `json:"result"`
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal resources/list: %v (%s)", err, raw)
	}
	if env.Error != nil {
		t.Fatalf("unexpected resources/list error: %+v", env.Error)
	}
	return env.Result.Resources
}

func waitForJobStatus(t *testing.T, reg *Registry, jobID string, want RenderJobStatus) RenderJobSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, rpcErr, raw := callToolRPC(t, reg, "get_render_status", map[string]any{"jobId": jobID})
		if rpcErr != nil || res.IsError {
			t.Fatalf("get_render_status failed: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
		}
		var snap RenderJobSnapshot
		if err := json.Unmarshal([]byte(res.Content[0].Text), &snap); err != nil {
			t.Fatal(err)
		}
		if snap.Status == want {
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for job %s to reach status %q", jobID, want)
	return RenderJobSnapshot{}
}

func TestRenderToolsRegistrationAndSafeClose(t *testing.T) {
	var nilMgr *RenderJobManager
	nilMgr.Close()

	nilRegMgr := RegisterRenderTools(nil, RenderDeps{})
	nilRegMgr.Close()

	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	mgr := RegisterRenderTools(reg, RenderDeps{})
	defer mgr.Close()

	resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	var env struct {
		Result struct {
			Tools []Tool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}
	if len(env.Result.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d (%s)", len(env.Result.Tools), resp)
	}

	gotByName := make(map[string]Tool, len(env.Result.Tools))
	for _, tool := range env.Result.Tools {
		gotByName[tool.Name] = tool
	}

	expectations := map[string]struct {
		readOnly    bool
		destructive bool
		idempotent  bool
	}{
		"start_render":      {readOnly: false, destructive: false, idempotent: false},
		"get_render_status": {readOnly: true, destructive: false, idempotent: true},
		"cancel_render":     {readOnly: false, destructive: false, idempotent: true},
		"verify_mix":        {readOnly: true, destructive: false, idempotent: true},
	}
	for name, exp := range expectations {
		tool, ok := gotByName[name]
		if !ok {
			t.Fatalf("missing tool %q", name)
		}
		ann := tool.Annotations
		if ann == nil ||
			ann.ReadOnlyHint == nil || *ann.ReadOnlyHint != exp.readOnly ||
			ann.DestructiveHint == nil || *ann.DestructiveHint != exp.destructive ||
			ann.IdempotentHint == nil || *ann.IdempotentHint != exp.idempotent ||
			ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Fatalf("unexpected annotations on %s: %+v", name, ann)
		}
	}
}

func TestRenderLifecycleProgressConflictAndCompletion(t *testing.T) {
	dir := t.TempDir()
	audioDir := filepath.Join(dir, "tracks")
	if err := os.Mkdir(audioDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01 - Alpha.mp3", "02 - Beta.mp3"} {
		if err := os.WriteFile(filepath.Join(audioDir, name), []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bgPath := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(bgPath, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var closeOnce sync.Once

	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	mgr := RegisterRenderTools(reg, RenderDeps{
		LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
		Runner: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == "ffprobe" {
				return []byte(`{"format":{"duration":"90.0"}}`), nil
			}
			return nil, nil
		},
		RunFn: func(ctx context.Context, cfg mixer.Config, sink mixer.TelemetrySink, _ func(string) (string, error), _ mixer.CommandRunner, _ mixer.StreamRunner) error {
			sink(mixer.TelemetryEvent{
				Kind:              mixer.EventStep,
				Step:              2,
				TotalSteps:        5,
				Progress:          20,
				Message:           "[2/5] Processing & merging 2 Track(s)...",
				Detail:            "Processing & merging 2 Track(s)...",
				RemainingSec:      12,
				EstimatedTotalSec: 15,
			})
			sink(mixer.TelemetryEvent{
				Kind:              mixer.EventProgress,
				Step:              5,
				TotalSteps:        5,
				Progress:          64,
				Substage:          "render",
				OutTimeSec:        115.2,
				TotalDurationSec:  180,
				Speed:             "3.5x",
				RemainingSec:      6.5,
				EstimatedTotalSec: 15,
			})
			closeOnce.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			if err := os.WriteFile(cfg.Timestamps, []byte("00:00 Alpha\n01:30 Beta\n"), 0644); err != nil {
				return err
			}
			if err := os.WriteFile(cfg.Out, []byte("mp4"), 0644); err != nil {
				return err
			}
			sink(mixer.TelemetryEvent{
				Kind:              mixer.EventDone,
				Step:              5,
				TotalSteps:        5,
				Progress:          100,
				Message:           "Done! Rendered output.mp4 via Path C.",
				TotalDurationSec:  180,
				EstimatedTotalSec: 15,
			})
			return nil
		},
	})
	defer mgr.Close()

	// Cancel the per-RPC context immediately after start_render returns to confirm background job survives.
	rpcCtx, rpcCancel := context.WithCancel(context.Background())
	startPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "start_render",
			"arguments": map[string]any{
				"audioDir": audioDir,
				"bg":       bgPath,
				"outDir":   filepath.Join(dir, "out"),
				"out":      "mix.mp4",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	startRespRaw := reg.HandleMessage(rpcCtx, startPayload)
	rpcCancel()

	var startEnv struct {
		Result CallToolResult `json:"result"`
		Error  *RPCError      `json:"error"`
	}
	if err := json.Unmarshal(startRespRaw, &startEnv); err != nil || startEnv.Error != nil || startEnv.Result.IsError {
		t.Fatalf("start_render failed: err=%v rpcErr=%v raw=%s", err, startEnv.Error, startRespRaw)
	}

	var startRes StartRenderResult
	if err := json.Unmarshal([]byte(startEnv.Result.Content[0].Text), &startRes); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(startRes.JobID, "job_") || startRes.Status != RenderJobRunning ||
		startRes.RenderingPath != "Path C" || startRes.TrackCount != 2 ||
		startRes.ResourceURI != "ytmix://jobs/"+startRes.JobID ||
		startRes.TimestampsResourceURI != "ytmix://jobs/"+startRes.JobID+"/timestamps" {
		t.Fatalf("unexpected start_render response: %+v", startRes)
	}

	<-started

	// Concurrent start_render must be rejected cleanly.
	conflictRes, rpcErr, _ := callToolRPC(t, reg, "start_render", map[string]any{
		"audioDir": audioDir,
		"bg":       bgPath,
		"outDir":   filepath.Join(dir, "conflict_out"),
	})
	if rpcErr != nil || !conflictRes.IsError || !strings.Contains(conflictRes.Content[0].Text, "already in progress") {
		t.Fatalf("expected active job conflict ErrorToolResult, got rpcErr=%v res=%+v", rpcErr, conflictRes)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "conflict_out")); !os.IsNotExist(statErr) {
		t.Fatalf("expected rejected start_render not to create outDir, stat err=%v", statErr)
	}

	// Poll live telemetry via get_render_status and ytmix://jobs/{id}.
	statusRes, rpcErr, _ := callToolRPC(t, reg, "get_render_status", map[string]any{"jobId": startRes.JobID})
	if rpcErr != nil || statusRes.IsError {
		t.Fatalf("get_render_status failed: rpcErr=%v res=%+v", rpcErr, statusRes)
	}
	var runningSnap RenderJobSnapshot
	if err := json.Unmarshal([]byte(statusRes.Content[0].Text), &runningSnap); err != nil {
		t.Fatal(err)
	}
	if runningSnap.Status != RenderJobRunning || runningSnap.Step != 5 || runningSnap.Progress != 64 ||
		runningSnap.Substage != "render" || runningSnap.Speed != "3.5x" || runningSnap.OutTimeSec != 115.2 {
		t.Fatalf("unexpected running telemetry snapshot: %+v", runningSnap)
	}

	// ytmix://jobs/{id} returns live JSON, while ytmix://jobs/{id}/timestamps fails cleanly until complete.
	jobResContents, rpcErr, _ := readResourceRPC(t, reg, startRes.ResourceURI)
	if rpcErr != nil || len(jobResContents) != 1 || jobResContents[0].MimeType != "application/json" {
		t.Fatalf("unexpected ytmix://jobs/{id} read: rpcErr=%v contents=%+v", rpcErr, jobResContents)
	}
	_, tsErr, _ := readResourceRPC(t, reg, startRes.TimestampsResourceURI)
	if tsErr == nil || tsErr.Code != ErrCodeResourceNotFound {
		t.Fatalf("expected -32002 for incomplete timestamps resource, got %+v", tsErr)
	}

	// Finish job and verify final snapshot + timestamps resource.
	close(release)
	completeSnap := waitForJobStatus(t, reg, startRes.JobID, RenderJobComplete)
	if completeSnap.Progress != 100 || completeSnap.RemainingSec != 0 || completeSnap.FinishedAt == nil {
		t.Fatalf("unexpected completed snapshot: %+v", completeSnap)
	}

	tsContents, tsErr, _ := readResourceRPC(t, reg, startRes.TimestampsResourceURI)
	if tsErr != nil || len(tsContents) != 1 || tsContents[0].MimeType != "text/plain" ||
		tsContents[0].Text != "00:00 Alpha\n01:30 Beta\n" {
		t.Fatalf("unexpected completed timestamps resource: err=%v contents=%+v", tsErr, tsContents)
	}
}

func TestStartRenderExplicitTracksDisplayTitleOverridesAndOrder(t *testing.T) {
	dir := t.TempDir()
	trackB := filepath.Join(dir, "02_raw_draft_v2.mp3")
	trackA := filepath.Join(dir, "01_messy_demo_final.mp3")
	for _, p := range []string{trackA, trackB} {
		if err := os.WriteFile(p, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bgPath := filepath.Join(dir, "cover.jpg")
	if err := os.WriteFile(bgPath, []byte("jpg"), 0644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "exports")

	var capturedASS string
	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	mgr := RegisterRenderTools(reg, RenderDeps{
		LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
		Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
			switch name {
			case "ffprobe":
				target := args[len(args)-1]
				if target == trackB {
					return []byte(`{"format":{"duration":"45.0","tags":{"title":"ID3 Should Be Overridden"}}}`), nil
				}
				return []byte(`{"format":{"duration":"60.0"}}`), nil
			case "ffmpeg":
				outArg := args[len(args)-1]
				if strings.HasSuffix(outArg, ".m4a") {
					return nil, os.WriteFile(outArg, []byte("m4a"), 0644)
				}
				return nil, nil
			default:
				return nil, errors.New("unexpected binary " + name)
			}
		},
		StreamRunner: func(_ context.Context, onLine func(string), _ string, args ...string) error {
			for i, a := range args {
				if a == "-vf" && i+1 < len(args) {
					// Read the generated titles.ass before mixer cleans up tempDir.
					const prefix = "subtitles='"
					if idx := strings.Index(args[i+1], prefix); idx >= 0 {
						rest := args[i+1][idx+len(prefix):]
						if end := strings.Index(rest, "'"); end >= 0 {
							assPath := strings.ReplaceAll(rest[:end], `\:`, `:`)
							if b, err := os.ReadFile(assPath); err == nil {
								capturedASS = string(b)
							}
						}
					}
				}
			}
			outArg := args[len(args)-1]
			if err := os.WriteFile(outArg, []byte("mp4"), 0644); err != nil {
				return err
			}
			if onLine != nil {
				onLine("out_time_us=100000000")
				onLine("speed=2.0x")
				onLine("progress=end")
			}
			return nil
		},
	})
	defer mgr.Close()

	// Deliberately pass trackB first (omitting durationSec so ffprobe runs) and trackA second (explicit durationSec).
	res, rpcErr, raw := callToolRPC(t, reg, "start_render", map[string]any{
		"tracks": []map[string]any{
			{
				"path":         trackB,
				"displayTitle": "Clean Nightfall",
			},
			{
				"path":         trackA,
				"displayTitle": "Morning Horizon",
				"durationSec":  60.0,
			},
		},
		"bg":            bgPath,
		"outDir":        outDir,
		"overlayTitles": true,
		"crossfade":     5.0,
	})
	if rpcErr != nil || res.IsError {
		t.Fatalf("start_render failed: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
	}

	var startRes StartRenderResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &startRes); err != nil {
		t.Fatal(err)
	}

	snap := waitForJobStatus(t, reg, startRes.JobID, RenderJobComplete)
	if snap.TrackCount != 2 {
		t.Fatalf("expected 2 tracks, got %d", snap.TrackCount)
	}

	tsBytes, err := os.ReadFile(startRes.TimestampsPath)
	if err != nil {
		t.Fatal(err)
	}
	wantTS := "00:00 Clean Nightfall\n00:40 Morning Horizon\n"
	if string(tsBytes) != wantTS {
		t.Fatalf("timestamps.txt = %q, want %q", string(tsBytes), wantTS)
	}
	if !strings.Contains(capturedASS, "Clean Nightfall") || !strings.Contains(capturedASS, "Morning Horizon") {
		t.Fatalf("expected custom Display Titles in titles.ass:\n%s", capturedASS)
	}
	if strings.Contains(capturedASS, "ID3 Should Be Overridden") || strings.Contains(capturedASS, "raw_draft") {
		t.Fatalf("unexpected un-overridden title in titles.ass:\n%s", capturedASS)
	}

	// Verify original .mp3 filenames on disk were untouched.
	for _, p := range []string{trackA, trackB} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected original .mp3 %q to remain untouched: %v", p, err)
		}
	}
}

func TestRenderCancellationFailureAndGracefulShutdown(t *testing.T) {
	t.Run("cancel_render stops running job, cleans tempDir, and handles already-finished jobs", func(t *testing.T) {
		dir := t.TempDir()
		trackPath := filepath.Join(dir, "01 - Track.mp3")
		if err := os.WriteFile(trackPath, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		bgPath := filepath.Join(dir, "cover.png")
		if err := os.WriteFile(bgPath, []byte("png"), 0644); err != nil {
			t.Fatal(err)
		}

		renderStarted := make(chan string, 1)
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		mgr := RegisterRenderTools(reg, RenderDeps{
			LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name == "ffmpeg" {
					outArg := args[len(args)-1]
					if strings.HasSuffix(outArg, ".m4a") {
						return nil, os.WriteFile(outArg, []byte("m4a"), 0644)
					}
				}
				return nil, nil
			},
			StreamRunner: func(ctx context.Context, _ func(string), _ string, args ...string) error {
				for _, a := range args {
					if strings.Contains(a, "merged_audio.m4a") {
						select {
						case renderStarted <- filepath.Dir(a):
						default:
						}
						break
					}
				}
				<-ctx.Done()
				return ctx.Err()
			},
		})
		defer mgr.Close()

		res, rpcErr, _ := callToolRPC(t, reg, "start_render", map[string]any{
			"tracks": []map[string]any{{"path": trackPath, "durationSec": 30}},
			"bg":     bgPath,
			"outDir": filepath.Join(dir, "out"),
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("start_render failed: rpcErr=%v res=%+v", rpcErr, res)
		}
		var startRes StartRenderResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &startRes); err != nil {
			t.Fatal(err)
		}

		tempDir := <-renderStarted
		if _, err := os.Stat(tempDir); err != nil {
			t.Fatalf("expected tempDir %q to exist while running: %v", tempDir, err)
		}

		cancelRes, rpcErr, raw := callToolRPC(t, reg, "cancel_render", map[string]any{"jobId": startRes.JobID})
		if rpcErr != nil || cancelRes.IsError {
			t.Fatalf("cancel_render failed: rpcErr=%v res=%+v raw=%s", rpcErr, cancelRes, raw)
		}
		var cancelled CancelRenderResult
		if err := json.Unmarshal([]byte(cancelRes.Content[0].Text), &cancelled); err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != RenderJobCancelled || cancelled.AlreadyFinished || !cancelled.CancelRequested {
			t.Fatalf("unexpected cancel_render result: %+v", cancelled)
		}

		// Wait briefly for mixer's defer os.RemoveAll(tempDir) after ctx.Err() unwinds.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(tempDir); os.IsNotExist(err) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
			t.Fatalf("expected tempDir %q to be cleaned up on cancel, stat err=%v", tempDir, err)
		}

		// Calling cancel_render again on an already-finished job should succeed cleanly.
		againRes, rpcErr, _ := callToolRPC(t, reg, "cancel_render", map[string]any{"jobId": startRes.JobID})
		if rpcErr != nil || againRes.IsError {
			t.Fatalf("second cancel_render failed: rpcErr=%v res=%+v", rpcErr, againRes)
		}
		var again CancelRenderResult
		if err := json.Unmarshal([]byte(againRes.Content[0].Text), &again); err != nil {
			t.Fatal(err)
		}
		if !again.AlreadyFinished || again.Status != RenderJobCancelled {
			t.Fatalf("expected alreadyFinished=true on finished job, got %+v", again)
		}
	})

	t.Run("pipeline error sets failed status and Close cancels active goroutines", func(t *testing.T) {
		dir := t.TempDir()
		trackPath := filepath.Join(dir, "01 - Track.mp3")
		if err := os.WriteFile(trackPath, []byte("mp3"), 0644); err != nil {
			t.Fatal(err)
		}
		bgPath := filepath.Join(dir, "cover.png")
		if err := os.WriteFile(bgPath, []byte("png"), 0644); err != nil {
			t.Fatal(err)
		}

		failOnce := true
		blocked := make(chan struct{})
		exited := make(chan struct{})

		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		mgr := RegisterRenderTools(reg, RenderDeps{
			LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			RunWithTracksFn: func(ctx context.Context, _ mixer.Config, _ []mixer.Track, _ mixer.TelemetrySink, _ func(string) (string, error), _ mixer.CommandRunner, _ mixer.StreamRunner) error {
				if failOnce {
					failOnce = false
					return errors.New("ffmpeg exploded")
				}
				close(blocked)
				<-ctx.Done()
				close(exited)
				return ctx.Err()
			},
		})

		res1, _, _ := callToolRPC(t, reg, "start_render", map[string]any{
			"tracks": []map[string]any{{"path": trackPath, "durationSec": 20}},
			"bg":     bgPath,
			"outDir": filepath.Join(dir, "out1"),
		})
		var start1 StartRenderResult
		_ = json.Unmarshal([]byte(res1.Content[0].Text), &start1)

		failedSnap := waitForJobStatus(t, reg, start1.JobID, RenderJobFailed)
		if !strings.Contains(failedSnap.Error, "ffmpeg exploded") || failedSnap.FinishedAt == nil {
			t.Fatalf("unexpected failed snapshot: %+v", failedSnap)
		}

		// Start a second blocking job, then Close() mgr to confirm graceful cancellation & wait.
		res2, rpcErr, _ := callToolRPC(t, reg, "start_render", map[string]any{
			"tracks": []map[string]any{{"path": trackPath, "durationSec": 20}},
			"bg":     bgPath,
			"outDir": filepath.Join(dir, "out2"),
		})
		if rpcErr != nil || res2.IsError {
			t.Fatalf("second start_render failed: rpcErr=%v res=%+v", rpcErr, res2)
		}
		<-blocked
		mgr.Close()

		select {
		case <-exited:
		default:
			t.Fatal("expected mgr.Close() to wait until active render goroutine exited")
		}
	})
}

func TestRenderJobRingEvictionUnregistersResources(t *testing.T) {
	dir := t.TempDir()
	trackPath := filepath.Join(dir, "01 - Track.mp3")
	if err := os.WriteFile(trackPath, []byte("mp3"), 0644); err != nil {
		t.Fatal(err)
	}
	bgPath := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(bgPath, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	mgr := RegisterRenderTools(reg, RenderDeps{
		MaxJobs:  2,
		LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
		RunWithTracksFn: func(_ context.Context, cfg mixer.Config, _ []mixer.Track, _ mixer.TelemetrySink, _ func(string) (string, error), _ mixer.CommandRunner, _ mixer.StreamRunner) error {
			return os.WriteFile(cfg.Timestamps, []byte("00:00 Track\n"), 0644)
		},
	})
	defer mgr.Close()

	var jobIDs []string
	for i := 0; i < 3; i++ {
		res, rpcErr, raw := callToolRPC(t, reg, "start_render", map[string]any{
			"tracks": []map[string]any{{"path": trackPath, "durationSec": 10}},
			"bg":     bgPath,
			"outDir": filepath.Join(dir, "out"),
		})
		if rpcErr != nil || res.IsError {
			t.Fatalf("start_render %d failed: rpcErr=%v res=%+v raw=%s", i, rpcErr, res, raw)
		}
		var startRes StartRenderResult
		if err := json.Unmarshal([]byte(res.Content[0].Text), &startRes); err != nil {
			t.Fatal(err)
		}
		jobIDs = append(jobIDs, startRes.JobID)
		waitForJobStatus(t, reg, startRes.JobID, RenderJobComplete)
	}

	// Job 0 should have been evicted from ring of size 2 and its URIs unregistered.
	resources := listResourcesRPC(t, reg)
	if len(resources) != 4 {
		t.Fatalf("expected 4 remaining resources for 2 retained jobs, got %d: %+v", len(resources), resources)
	}
	for _, uri := range []string{jobResourceURI(jobIDs[0]), jobTimestampsResourceURI(jobIDs[0])} {
		_, rpcErr, _ := readResourceRPC(t, reg, uri)
		if rpcErr == nil || rpcErr.Code != ErrCodeResourceNotFound {
			t.Fatalf("expected -32002 for evicted URI %s, got %+v", uri, rpcErr)
		}
	}
	for _, id := range jobIDs[1:] {
		if _, rpcErr, _ := readResourceRPC(t, reg, jobResourceURI(id)); rpcErr != nil {
			t.Fatalf("expected retained job %s to be readable, got %+v", id, rpcErr)
		}
	}
}

func TestStartRenderValidationErrors(t *testing.T) {
	dir := t.TempDir()
	trackPath := filepath.Join(dir, "01 - Track.mp3")
	if err := os.WriteFile(trackPath, []byte("mp3"), 0644); err != nil {
		t.Fatal(err)
	}
	notMP3 := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(notMP3, []byte("wav"), 0644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
	mgr := RegisterRenderTools(reg, RenderDeps{
		LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
		Runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if strings.Contains(strings.Join(args, " "), "-filters") {
				return []byte(" .. showfreqs A->V Freqs\n"), nil // lacks a3dscope
			}
			return nil, nil
		},
	})
	defer mgr.Close()

	for _, badParams := range []map[string]any{
		{},
		{"audioDir": dir, "tracks": []map[string]any{{"path": trackPath}}},
		{"tracks": []map[string]any{}},
		{"tracks": []map[string]any{{"path": ""}}},
		{"tracks": []map[string]any{{"path": trackPath, "durationSec": -1}}},
		{"audioDir": dir, "crossfade": -1},
		{"audioDir": dir, "titleDuration": -2},
		{"audioDir": dir, "bg": "cover.png", "visualizer": "bars"},
		{"audioDir": dir, "visualizer": "nope"},
		{"audioDir": dir, "visualizerBrightness": 5},
		{"audioDir": dir, "fadeToBlack": "not-a-duration"},
		{"audioDir": dir, "unknownField": true},
	} {
		_, rpcErr, raw := callToolRPC(t, reg, "start_render", badParams)
		if rpcErr == nil || rpcErr.Code != ErrCodeInvalidParams {
			t.Fatalf("expected -32602 for %v, got %s", badParams, raw)
		}
	}

	for _, toolErrParams := range []map[string]any{
		{"tracks": []map[string]any{{"path": notMP3, "durationSec": 10}}},
		{"tracks": []map[string]any{{"path": filepath.Join(dir, "missing.mp3"), "durationSec": 10}}},
		{"audioDir": filepath.Join(dir, "missing_dir")},
		{"audioDir": dir, "visualizer": "3dscope"},
	} {
		res, rpcErr, raw := callToolRPC(t, reg, "start_render", toolErrParams)
		if rpcErr != nil || !res.IsError {
			t.Fatalf("expected ErrorToolResult for %v, got rpcErr=%v raw=%s", toolErrParams, rpcErr, raw)
		}
	}

	for _, toolName := range []string{"get_render_status", "cancel_render"} {
		_, rpcErr, _ := callToolRPC(t, reg, toolName, map[string]any{"jobId": "   "})
		if rpcErr == nil || rpcErr.Code != ErrCodeInvalidParams {
			t.Fatalf("expected -32602 for blank jobId on %s", toolName)
		}
		res, rpcErr, _ := callToolRPC(t, reg, toolName, map[string]any{"jobId": "job_unknown"})
		if rpcErr != nil || !res.IsError {
			t.Fatalf("expected ErrorToolResult for unknown jobId on %s, got rpcErr=%v res=%+v", toolName, rpcErr, res)
		}
	}
}

func TestVerifyMix(t *testing.T) {
	dir := t.TempDir()
	mp4Path := filepath.Join(dir, "mix.mp4")
	if err := os.WriteFile(mp4Path, []byte("mp4"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		ffprobeJSON    string
		timestampsBody string
		omitTimestamps bool
		wantValid      bool
		wantTSValid    *bool
		wantChapters   int
		wantAudioCount int
		wantStrayAudio int
		wantWarnSubstr string
		wantErrSubstr  string
	}{
		{
			name:           "valid 1080p h264 + aac with aligned MM:SS timestamps",
			ffprobeJSON:    `{"format":{"duration":"180.0"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"pix_fmt":"yuv420p","r_frame_rate":"30/1","avg_frame_rate":"30/1"},{"index":1,"codec_type":"audio","codec_name":"aac"}]}`,
			timestampsBody: "00:00 First Track\n01:30 Second Track\n",
			wantValid:      true,
			wantTSValid:    BoolPtr(true),
			wantChapters:   2,
			wantAudioCount: 1,
		},
		{
			name:           "valid 4K HH:MM:SS timestamps",
			ffprobeJSON:    `{"format":{"duration":"3700.0"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":3840,"height":2160,"pix_fmt":"yuv420p","avg_frame_rate":"30/1"},{"index":1,"codec_type":"audio","codec_name":"aac"}]}`,
			timestampsBody: "00:00:00 Opening\n00:45:00 Finale\n",
			wantValid:      true,
			wantTSValid:    BoolPtr(true),
			wantChapters:   2,
			wantAudioCount: 1,
		},
		{
			name:           "flags stray extra audio stream as invalid",
			ffprobeJSON:    `{"format":{"duration":"120.0"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"30/1"},{"index":1,"codec_type":"audio","codec_name":"aac"},{"index":2,"codec_type":"audio","codec_name":"aac"}]}`,
			omitTimestamps: true,
			wantValid:      false,
			wantAudioCount: 2,
			wantStrayAudio: 1,
			wantErrSubstr:  "stray audio stream",
		},
		{
			name:           "flags missing video stream and wrong audio codec",
			ffprobeJSON:    `{"format":{"duration":"60.0"},"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3"}]}`,
			omitTimestamps: true,
			wantValid:      false,
			wantAudioCount: 1,
			wantErrSubstr:  "missing video stream",
		},
		{
			name:           "flags non-h264 video codec and zero duration",
			ffprobeJSON:    `{"format":{"duration":"0"},"streams":[{"index":0,"codec_type":"video","codec_name":"vp9","width":1920,"height":1080},{"index":1,"codec_type":"audio","codec_name":"aac"}]}`,
			omitTimestamps: true,
			wantValid:      false,
			wantAudioCount: 1,
			wantErrSubstr:  "expected h264",
		},
		{
			name:           "flags sub-1080p non-16:9 as warnings while still valid streams",
			ffprobeJSON:    `{"format":{"duration":"60.0"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":800,"height":600,"avg_frame_rate":"24/1"},{"index":1,"codec_type":"audio","codec_name":"aac"}]}`,
			omitTimestamps: true,
			wantValid:      true,
			wantAudioCount: 1,
			wantWarnSubstr: "not 16:9",
		},
		{
			name:           "flags timestamps not starting at 00:00",
			ffprobeJSON:    `{"format":{"duration":"120.0"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080},{"index":1,"codec_type":"audio","codec_name":"aac"}]}`,
			timestampsBody: "00:10 Late Start\n",
			wantValid:      false,
			wantTSValid:    BoolPtr(false),
			wantChapters:   1,
			wantAudioCount: 1,
			wantErrSubstr:  "00:00",
		},
		{
			name:           "flags non-monotonic and out-of-range timestamps",
			ffprobeJSON:    `{"format":{"duration":"100.0"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080},{"index":1,"codec_type":"audio","codec_name":"aac"}]}`,
			timestampsBody: "00:00 First\n02:00 Second\n01:30 Third\n",
			wantValid:      false,
			wantTSValid:    BoolPtr(false),
			wantChapters:   3,
			wantAudioCount: 1,
			wantErrSubstr:  "earlier than previous chapter",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotArgs []string
			reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
			mgr := RegisterRenderTools(reg, RenderDeps{
				LookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
				Runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
					if name != "ffprobe" {
						return nil, errors.New("unexpected command " + name)
					}
					gotArgs = append([]string(nil), args...)
					return []byte(tc.ffprobeJSON), nil
				},
			})
			defer mgr.Close()

			params := map[string]any{"outPath": mp4Path}
			if !tc.omitTimestamps {
				tsPath := filepath.Join(dir, "timestamps.txt")
				if err := os.WriteFile(tsPath, []byte(tc.timestampsBody), 0644); err != nil {
					t.Fatal(err)
				}
				params["timestampsPath"] = tsPath
			}

			res, rpcErr, raw := callToolRPC(t, reg, "verify_mix", params)
			if rpcErr != nil || res.IsError {
				t.Fatalf("verify_mix unexpected failure: rpcErr=%v res=%+v raw=%s", rpcErr, res, raw)
			}

			wantArgs := []string{
				"-v", "error",
				"-show_entries", "format=duration",
				"-show_entries", "stream=index,codec_type,codec_name,width,height,pix_fmt,r_frame_rate,avg_frame_rate",
				"-of", "json",
				mp4Path,
			}
			if !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("ffprobe args = %v, want %v", gotArgs, wantArgs)
			}

			var got VerifyMixResult
			if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
				t.Fatal(err)
			}
			if got.Valid != tc.wantValid || got.AudioStreamCount != tc.wantAudioCount ||
				got.StrayAudioStreams != tc.wantStrayAudio || got.ChapterCount != tc.wantChapters {
				t.Fatalf("unexpected VerifyMixResult: %+v", got)
			}
			if tc.wantTSValid == nil && got.TimestampsValid != nil {
				t.Fatalf("expected nil TimestampsValid, got %v", *got.TimestampsValid)
			}
			if tc.wantTSValid != nil && (got.TimestampsValid == nil || *got.TimestampsValid != *tc.wantTSValid) {
				t.Fatalf("TimestampsValid = %v, want %v", got.TimestampsValid, *tc.wantTSValid)
			}
			if tc.wantWarnSubstr != "" && !strings.Contains(strings.Join(got.Warnings, "\n"), tc.wantWarnSubstr) {
				t.Fatalf("expected warning containing %q, got %v", tc.wantWarnSubstr, got.Warnings)
			}
			if tc.wantErrSubstr != "" && !strings.Contains(strings.Join(got.Errors, "\n"), tc.wantErrSubstr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErrSubstr, got.Errors)
			}
		})
	}

	t.Run("handles missing/dir outPath and missing ffprobe cleanly", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		mgr := RegisterRenderTools(reg, RenderDeps{
			LookPath: func(string) (string, error) { return "", errors.New("not found") },
		})
		defer mgr.Close()

		for _, badOut := range []string{filepath.Join(dir, "missing.mp4"), dir} {
			res, rpcErr, _ := callToolRPC(t, reg, "verify_mix", map[string]any{"outPath": badOut})
			if rpcErr != nil || !res.IsError {
				t.Fatalf("expected ErrorToolResult for outPath %q, got rpcErr=%v res=%+v", badOut, rpcErr, res)
			}
		}

		res, rpcErr, _ := callToolRPC(t, reg, "verify_mix", map[string]any{"outPath": mp4Path})
		if rpcErr != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "ffprobe") {
			t.Fatalf("expected missing ffprobe ErrorToolResult, got rpcErr=%v res=%+v", rpcErr, res)
		}
	})
}
