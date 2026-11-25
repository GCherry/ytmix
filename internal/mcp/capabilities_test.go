package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestGetCapabilities(t *testing.T) {
	allFiltersOutput := []byte(`Filters:
 .. color |->V Color
 .. showwaves A->V Wave
 TS gblur V->V Blur
 .. scale V->V Scale
 .. pad V->V Pad
 .. showfreqs A->V Freqs
 .S avectorscope A->V Scope
 .. showcqt A->V CQT
 .S showspectrum A->V Spectrum
 .. ahistogram A->V Histogram
 .. aphasemeter A->N Phase
 .. anullsink A->| Sink
 .. showvolume A->V Volume
`)

	tests := []struct {
		name            string
		goos            string
		lookPath        func(string) (string, error)
		runner          CommandRunner
		wantStatus      string
		wantFFmpeg      bool
		wantFFprobe     bool
		wantEncoder     string
		wantPathDEnc    string
		want3DScope     bool
		want3DScopeMiss string
		wantBars        bool
	}{
		{
			name:     "linux with ffmpeg nvenc, ffprobe, and missing a3dscope filter",
			goos:     "linux",
			lookPath: func(bin string) (string, error) { return "/usr/bin/" + bin, nil },
			runner: func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "ffmpeg" {
					return nil, errors.New("unexpected binary")
				}
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "-filters") {
					return allFiltersOutput, nil
				}
				if strings.Contains(joined, "h264_nvenc") {
					return nil, nil
				}
				return nil, errors.New("unsupported")
			},
			wantStatus:      "ok",
			wantFFmpeg:      true,
			wantFFprobe:     true,
			wantEncoder:     "h264_nvenc",
			wantPathDEnc:    "h264_nvenc",
			want3DScope:     false,
			want3DScopeMiss: "a3dscope",
			wantBars:        true,
		},
		{
			name:     "windows falls back to libx264 when nvenc fails",
			goos:     "windows",
			lookPath: func(bin string) (string, error) { return "C:\\ffmpeg\\" + bin + ".exe", nil },
			runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "-filters") {
					return append(append([]byte(nil), allFiltersOutput...), []byte(" .. a3dscope A->V 3D\n")...), nil
				}
				return nil, errors.New("nvenc unavailable")
			},
			wantStatus:   "ok",
			wantFFmpeg:   true,
			wantFFprobe:  true,
			wantEncoder:  "libx264",
			wantPathDEnc: "libx264",
			want3DScope:  true,
			wantBars:     true,
		},
		{
			name:     "darwin selects h264_videotoolbox",
			goos:     "darwin",
			lookPath: func(bin string) (string, error) { return "/opt/homebrew/bin/" + bin, nil },
			runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "-filters") {
					return allFiltersOutput, nil
				}
				if strings.Contains(joined, "h264_videotoolbox") {
					return nil, nil
				}
				return nil, errors.New("unsupported")
			},
			wantStatus:      "ok",
			wantFFmpeg:      true,
			wantFFprobe:     true,
			wantEncoder:     "h264_videotoolbox",
			wantPathDEnc:    "h264_videotoolbox",
			want3DScope:     false,
			want3DScopeMiss: "a3dscope",
			wantBars:        true,
		},
		{
			name: "missing ffmpeg marks status degraded and encoders unavailable",
			goos: "linux",
			lookPath: func(bin string) (string, error) {
				if bin == "ffmpeg" {
					return "", errors.New("not found")
				}
				return "/usr/bin/" + bin, nil
			},
			runner: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
				t.Fatal("runner must not be called when ffmpeg is missing")
				return nil, nil
			},
			wantStatus:   "degraded",
			wantFFmpeg:   false,
			wantFFprobe:  true,
			wantEncoder:  "unavailable",
			wantPathDEnc: "unavailable",
			want3DScope:  false,
			wantBars:     false,
		},
		{
			name: "missing ffprobe marks status degraded while still probing ffmpeg",
			goos: "linux",
			lookPath: func(bin string) (string, error) {
				if bin == "ffprobe" {
					return "", errors.New("not found")
				}
				return "/usr/bin/" + bin, nil
			},
			runner: func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "-filters") {
					return allFiltersOutput, nil
				}
				return nil, errors.New("no nvenc")
			},
			wantStatus:      "degraded",
			wantFFmpeg:      true,
			wantFFprobe:     false,
			wantEncoder:     "libx264",
			wantPathDEnc:    "libx264",
			want3DScope:     false,
			want3DScopeMiss: "a3dscope",
			wantBars:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
			RegisterCapabilitiesTool(reg, CapabilitiesDeps{
				LookPath: tc.lookPath,
				Runner:   tc.runner,
				GOOS:     tc.goos,
			})

			listResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			if !strings.Contains(string(listResp), `"name":"get_capabilities"`) {
				t.Fatalf("tools/list missing get_capabilities: %s", listResp)
			}

			callResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_capabilities","arguments":{}}}`))
			var env struct {
				Result struct {
					Content           []ContentBlock     `json:"content"`
					StructuredContent CapabilitiesResult `json:"structuredContent"`
					IsError           bool               `json:"isError"`
				} `json:"result"`
				Error *RPCError `json:"error"`
			}
			if err := json.Unmarshal(callResp, &env); err != nil {
				t.Fatalf("unmarshal tools/call response: %v (%s)", err, callResp)
			}
			if env.Error != nil || env.Result.IsError {
				t.Fatalf("unexpected error in get_capabilities response: %s", callResp)
			}
			if len(env.Result.Content) != 1 || env.Result.Content[0].Type != "text" {
				t.Fatalf("expected 1 text content block, got %+v", env.Result.Content)
			}

			var fromText CapabilitiesResult
			if err := json.Unmarshal([]byte(env.Result.Content[0].Text), &fromText); err != nil {
				t.Fatalf("text content is not valid JSON: %v", err)
			}

			got := env.Result.StructuredContent
			if got.Status != tc.wantStatus || got.FFmpeg != tc.wantFFmpeg || got.FFprobe != tc.wantFFprobe || got.GOOS != tc.goos {
				t.Fatalf("unexpected status/binaries/goos: %+v", got)
			}
			if got.Encoder.Name != tc.wantEncoder || got.PathDEncoder.Name != tc.wantPathDEnc {
				t.Fatalf("unexpected encoders: encoder=%+v pathDEncoder=%+v", got.Encoder, got.PathDEncoder)
			}
			if len(got.Visualizers) != 14 {
				t.Fatalf("expected 14 visualizers, got %d", len(got.Visualizers))
			}
			if len(got.VisualizerThemes) != 7 {
				t.Fatalf("expected 7 themes, got %d", len(got.VisualizerThemes))
			}
			if len(got.VisualizerResolutions) != 3 {
				t.Fatalf("expected 3 resolutions, got %d", len(got.VisualizerResolutions))
			}
			if len(got.SupportedBackgroundExts.Image) != 4 || len(got.SupportedBackgroundExts.Video) != 3 {
				t.Fatalf("unexpected supportedBackgroundExts: %+v", got.SupportedBackgroundExts)
			}

			byID := make(map[string]VisualizerCapability, len(got.Visualizers))
			for _, v := range got.Visualizers {
				byID[v.ID] = v
			}
			if byID["3dscope"].Available != tc.want3DScope || byID["3dscope"].MissingFilter != tc.want3DScopeMiss {
				t.Fatalf("3dscope = %+v, want avail=%v miss=%q", byID["3dscope"], tc.want3DScope, tc.want3DScopeMiss)
			}
			if byID["bars"].Available != tc.wantBars {
				t.Fatalf("bars = %+v, want avail=%v", byID["bars"], tc.wantBars)
			}
			if fromText.Encoder.Name != got.Encoder.Name || len(fromText.Visualizers) != len(got.Visualizers) {
				t.Fatalf("text JSON and structuredContent diverged")
			}
		})
	}

	t.Run("rejects unexpected arguments and accepts omitted or null arguments", func(t *testing.T) {
		reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		RegisterCapabilitiesTool(reg, CapabilitiesDeps{GOOS: "linux"})

		badResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_capabilities","arguments":{"unexpected":true}}}`))
		var errEnv struct {
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(badResp, &errEnv); err != nil || errEnv.Error == nil || errEnv.Error.Code != ErrCodeInvalidParams {
			t.Fatalf("expected -32602 for unexpected arguments, got: %s", badResp)
		}

		for _, req := range []string{
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_capabilities"}}`,
			`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_capabilities","arguments":null}}`,
		} {
			okResp := reg.HandleMessage(context.Background(), []byte(req))
			var okEnv struct {
				Error *RPCError `json:"error"`
			}
			if err := json.Unmarshal(okResp, &okEnv); err != nil || okEnv.Error != nil {
				t.Fatalf("expected success for %s, got: %s", req, okResp)
			}
		}
	})
}
