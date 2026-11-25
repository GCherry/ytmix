package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type errReader struct {
	err error
}

func (e errReader) Read(_ []byte) (int, error) {
	return 0, e.err
}

type errWriter struct {
	err error
}

func (e errWriter) Write(_ []byte) (int, error) {
	return 0, e.err
}

func TestServerServe(t *testing.T) {
	t.Run("processes newline-delimited requests, skips empty lines, suppresses notifications, and handles EOF without newline", func(t *testing.T) {
		input := strings.Join([]string{
			"",
			"   \t  ",
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}` + "\r",
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
			`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		}, "\n") // intentionally omit trailing newline

		var out, errOut bytes.Buffer
		srv := NewServer(ServerConfig{
			In:     strings.NewReader(input),
			Out:    &out,
			ErrOut: &errOut,
			Info:   ServerInfo{Name: "ytmix", Version: "1.2.3"},
		})

		if err := srv.Serve(context.Background()); err != nil {
			t.Fatalf("Serve error: %v", err)
		}

		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("expected 3 JSON response lines (notification & blank lines omitted), got %d:\n%s", len(lines), out.String())
		}
		for i, line := range lines {
			if !json.Valid([]byte(line)) {
				t.Fatalf("line %d is not valid JSON: %q", i, line)
			}
		}
		if errOut.Len() != 0 {
			t.Fatalf("expected zero stderr output on clean session, got %q", errOut.String())
		}
	})

	t.Run("notifications alone write zero stdout bytes", func(t *testing.T) {
		var out, errOut bytes.Buffer
		srv := NewServer(ServerConfig{
			In:     strings.NewReader("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n"),
			Out:    &out,
			ErrOut: &errOut,
		})
		if err := srv.Serve(context.Background()); err != nil {
			t.Fatalf("Serve error: %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("expected 0 bytes on stdout for notification, got %q", out.String())
		}
	})

	t.Run("handles messages larger than 64KB without token truncation", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "dev"})
		reg.RegisterTool(Tool{
			Name:        "echo_len",
			InputSchema: map[string]any{"type": "object"},
		}, func(_ context.Context, args json.RawMessage) (CallToolResult, error) {
			var in struct {
				Payload string `json:"payload"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return CallToolResult{}, err
			}
			return TextToolResult(in.Payload[:5]), nil
		})

		bigPayload := strings.Repeat("a", 96*1024)
		req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo_len","arguments":{"payload":"` + bigPayload + `"}}}` + "\n"

		var out, errOut bytes.Buffer
		srv := NewServer(ServerConfig{
			In:       strings.NewReader(req),
			Out:      &out,
			ErrOut:   &errOut,
			Registry: reg,
		})
		if err := srv.Serve(context.Background()); err != nil {
			t.Fatalf("Serve error on >64KB payload: %v", err)
		}
		if !strings.Contains(out.String(), `"text":"aaaaa"`) {
			t.Fatalf("unexpected response for >64KB payload: %s", out.String())
		}
	})

	t.Run("stops promptly on context cancellation even when stdin pipe is open", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() {
			_ = pw.Close()
			_ = pr.Close()
		}()

		var out syncBuffer
		var errOut bytes.Buffer
		srv := NewServer(ServerConfig{
			In:     pr,
			Out:    &out,
			ErrOut: &errOut,
		})

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- srv.Serve(ctx)
		}()

		if _, err := io.WriteString(pw, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"); err != nil {
			t.Fatal(err)
		}

		// Wait briefly for ping response before cancelling.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(out.String(), `"id":1`) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !strings.Contains(out.String(), `"id":1`) {
			t.Fatalf("did not observe ping response before cancel; out=%q", out.String())
		}

		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("expected nil or context.Canceled on cancel, got %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not exit promptly after context cancellation")
		}
	})

	t.Run("logs read and write errors to stderr only and never to stdout", func(t *testing.T) {
		var out, errOut bytes.Buffer
		srvReadErr := NewServer(ServerConfig{
			In:     errReader{err: errors.New("boom read")},
			Out:    &out,
			ErrOut: &errOut,
		})
		if err := srvReadErr.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "boom read") {
			t.Fatalf("expected boom read error, got %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("expected empty stdout on read error, got %q", out.String())
		}
		if !strings.Contains(errOut.String(), "boom read") {
			t.Fatalf("expected diagnostic in stderr, got %q", errOut.String())
		}

		errOut.Reset()
		srvWriteErr := NewServer(ServerConfig{
			In:     strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"),
			Out:    errWriter{err: errors.New("boom write")},
			ErrOut: &errOut,
		})
		if err := srvWriteErr.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "boom write") {
			t.Fatalf("expected boom write error, got %v", err)
		}
		if !strings.Contains(errOut.String(), "boom write") {
			t.Fatalf("expected write diagnostic in stderr, got %q", errOut.String())
		}
	})

	t.Run("rejects messages exceeding MaxMessageBytes cleanly to stderr", func(t *testing.T) {
		var out, errOut bytes.Buffer
		oversized := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", 128) + `"}}` + "\n"
		srv := NewServer(ServerConfig{
			In:              strings.NewReader(oversized),
			Out:             &out,
			ErrOut:          &errOut,
			MaxMessageBytes: 64,
		})
		err := srv.Serve(context.Background())
		if err == nil || !strings.Contains(err.Error(), "exceeds 64 bytes") {
			t.Fatalf("expected max message size error, got %v", err)
		}
		if out.Len() != 0 {
			t.Fatalf("expected zero stdout bytes on oversized message, got %q", out.String())
		}
		if !strings.Contains(errOut.String(), "exceeds 64 bytes") {
			t.Fatalf("expected diagnostic in stderr, got %q", errOut.String())
		}
	})
}
