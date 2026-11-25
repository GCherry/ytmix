package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ServerConfig configures an MCP stdio Server.
type ServerConfig struct {
	In       io.Reader
	Out      io.Writer
	ErrOut   io.Writer
	Info     ServerInfo
	Registry *Registry
}

// Server serves MCP JSON-RPC 2.0 requests over newline-delimited stdio streams.
type Server struct {
	*Registry
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
	writeMu sync.Mutex
}

// NewServer constructs an MCP stdio Server around cfg.Registry (or a fresh Registry if nil).
func NewServer(cfg ServerConfig) *Server {
	reg := cfg.Registry
	if reg == nil {
		reg = NewRegistry(cfg.Info)
	}
	in := cfg.In
	if in == nil {
		in = strings.NewReader("")
	}
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}
	errOut := cfg.ErrOut
	if errOut == nil {
		errOut = io.Discard
	}
	return &Server{
		Registry: reg,
		in:       in,
		out:      out,
		errOut:   errOut,
	}
}

type readLineResult struct {
	line []byte
	err  error
}

// Serve runs the newline-delimited JSON-RPC 2.0 loop until EOF, stream error, or ctx cancellation.
// Nothing other than serialized JSON-RPC responses is ever written to Out; diagnostics go to ErrOut.
func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	lines := make(chan readLineResult)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(lines)
		reader := bufio.NewReaderSize(s.in, 64*1024)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				select {
				case lines <- readLineResult{line: line}:
				case <-done:
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				select {
				case lines <- readLineResult{err: err}:
				case <-done:
				}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case item, ok := <-lines:
			if !ok {
				return nil
			}
			if item.err != nil {
				s.logErrf("ytmix mcp: read stdin: %v", item.err)
				return item.err
			}
			if len(bytes.TrimSpace(item.line)) == 0 {
				continue
			}
			resp := s.HandleMessage(ctx, item.line)
			if len(resp) == 0 {
				continue
			}
			if err := s.writeJSONLine(resp); err != nil {
				s.logErrf("ytmix mcp: write stdout: %v", err)
				return err
			}
		}
	}
}

func (s *Server) writeJSONLine(payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if _, err := s.out.Write(payload); err != nil {
		return err
	}
	_, err := s.out.Write([]byte{'\n'})
	return err
}

func (s *Server) logErrf(format string, args ...any) {
	if s.errOut == nil {
		return
	}
	fmt.Fprintf(s.errOut, format, args...)
	fmt.Fprintln(s.errOut)
}
