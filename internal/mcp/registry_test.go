package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRegistryLifecycleAndErrors(t *testing.T) {
	reg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})

	t.Run("initialize negotiates supported protocol version and returns capabilities", func(t *testing.T) {
		tests := []struct {
			name        string
			req         string
			wantVersion string
		}{
			{
				name:        "accepts 2025-06-18",
				req:         `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"antigravity","version":"1.0"}}}`,
				wantVersion: "2025-06-18",
			},
			{
				name:        "accepts 2025-03-26",
				req:         `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"claude","version":"1.0"}}}`,
				wantVersion: "2025-03-26",
			},
			{
				name:        "accepts 2024-11-05",
				req:         `{"jsonrpc":"2.0","id":"init-2","method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
				wantVersion: "2024-11-05",
			},
			{
				name:        "falls back to default supported version for unknown client version",
				req:         `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
				wantVersion: DefaultProtocolVersion,
			},
			{
				name:        "handles initialize with omitted params",
				req:         `{"jsonrpc":"2.0","id":4,"method":"initialize"}`,
				wantVersion: DefaultProtocolVersion,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				respBytes := reg.HandleMessage(context.Background(), []byte(tc.req))
				if len(respBytes) == 0 {
					t.Fatal("expected non-empty response for initialize")
				}
				var env struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      json.RawMessage `json:"id"`
					Result  struct {
						ProtocolVersion string         `json:"protocolVersion"`
						Capabilities    map[string]any `json:"capabilities"`
						ServerInfo      ServerInfo     `json:"serverInfo"`
					} `json:"result"`
					Error *RPCError `json:"error"`
				}
				if err := json.Unmarshal(respBytes, &env); err != nil {
					t.Fatalf("unmarshal response: %v (%s)", err, respBytes)
				}
				if env.Error != nil {
					t.Fatalf("unexpected RPC error: %+v", env.Error)
				}
				if env.JSONRPC != "2.0" {
					t.Fatalf("jsonrpc = %q, want 2.0", env.JSONRPC)
				}
				if env.Result.ProtocolVersion != tc.wantVersion {
					t.Fatalf("protocolVersion = %q, want %q", env.Result.ProtocolVersion, tc.wantVersion)
				}
				if env.Result.ServerInfo.Name != "ytmix" || env.Result.ServerInfo.Version != "0.1.0" {
					t.Fatalf("unexpected serverInfo: %+v", env.Result.ServerInfo)
				}
				for _, capKey := range []string{"tools", "resources", "prompts"} {
					if _, ok := env.Result.Capabilities[capKey]; !ok {
						t.Fatalf("capabilities missing %q: %+v", capKey, env.Result.Capabilities)
					}
				}
			})
		}
	})

	t.Run("notifications produce nil response and track initialized state", func(t *testing.T) {
		if reg.Initialized() {
			t.Fatal("expected reg.Initialized() to be false initially")
		}
		for _, msg := range []string{
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`,
			`{"jsonrpc":"2.0","method":"unknown/notification"}`,
		} {
			resp := reg.HandleMessage(context.Background(), []byte(msg))
			if resp != nil {
				t.Fatalf("expected nil response for notification %s, got %s", msg, resp)
			}
		}
		if !reg.Initialized() {
			t.Fatal("expected reg.Initialized() to be true after notifications/initialized")
		}
	})

	t.Run("ping returns empty object result", func(t *testing.T) {
		resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":"p-1","method":"ping"}`))
		var env struct {
			ID     string         `json:"id"`
			Result map[string]any `json:"result"`
			Error  *RPCError      `json:"error"`
		}
		if err := json.Unmarshal(resp, &env); err != nil {
			t.Fatalf("unmarshal ping response: %v", err)
		}
		if env.Error != nil || env.ID != "p-1" || env.Result == nil || len(env.Result) != 0 {
			t.Fatalf("unexpected ping response: %s", resp)
		}
	})

	t.Run("JSON-RPC error codes", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			wantCode int
			wantID   string
		}{
			{
				name:     "parse error invalid JSON",
				input:    `{not valid json`,
				wantCode: ErrCodeParse,
				wantID:   "null",
			},
			{
				name:     "invalid request non-object",
				input:    `123`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "null",
			},
			{
				name:     "invalid request empty object without id",
				input:    `{}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "null",
			},
			{
				name:     "invalid request wrong jsonrpc version without id",
				input:    `{"jsonrpc":"1.0","method":"ping"}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "null",
			},
			{
				name:     "invalid request empty method without id",
				input:    `{"jsonrpc":"2.0","method":""}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "null",
			},
			{
				name:     "invalid request wrong jsonrpc version",
				input:    `{"jsonrpc":"1.0","id":7,"method":"ping"}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "7",
			},
			{
				name:     "invalid request empty method",
				input:    `{"jsonrpc":"2.0","id":"x","method":""}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   `"x"`,
			},
			{
				name:     "invalid request null id",
				input:    `{"jsonrpc":"2.0","id":null,"method":"ping"}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "null",
			},
			{
				name:     "invalid request boolean id",
				input:    `{"jsonrpc":"2.0","id":true,"method":"ping"}`,
				wantCode: ErrCodeInvalidRequest,
				wantID:   "null",
			},
			{
				name:     "unknown method returns -32601",
				input:    `{"jsonrpc":"2.0","id":9,"method":"does/not/exist"}`,
				wantCode: ErrCodeMethodNotFound,
				wantID:   "9",
			},
			{
				name:     "notification method sent as request with id returns -32601",
				input:    `{"jsonrpc":"2.0","id":90,"method":"notifications/initialized"}`,
				wantCode: ErrCodeMethodNotFound,
				wantID:   "90",
			},
			{
				name:     "initialize with invalid params type returns -32602",
				input:    `{"jsonrpc":"2.0","id":10,"method":"initialize","params":123}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "10",
			},
			{
				name:     "tools/call missing name returns -32602",
				input:    `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{}}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "11",
			},
			{
				name:     "tools/call unknown tool returns -32602",
				input:    `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"missing_tool"}}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "12",
			},
			{
				name:     "tools/call non-object arguments returns -32602",
				input:    `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"missing_tool","arguments":"bad"}}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "13",
			},
			{
				name:     "resources/read missing uri returns -32602",
				input:    `{"jsonrpc":"2.0","id":14,"method":"resources/read","params":{}}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "14",
			},
			{
				name:     "resources/read unknown uri returns -32002",
				input:    `{"jsonrpc":"2.0","id":15,"method":"resources/read","params":{"uri":"ytmix://missing"}}`,
				wantCode: ErrCodeResourceNotFound,
				wantID:   "15",
			},
			{
				name:     "prompts/get missing name returns -32602",
				input:    `{"jsonrpc":"2.0","id":16,"method":"prompts/get","params":{}}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "16",
			},
			{
				name:     "prompts/get unknown prompt returns -32602",
				input:    `{"jsonrpc":"2.0","id":17,"method":"prompts/get","params":{"name":"missing"}}`,
				wantCode: ErrCodeInvalidParams,
				wantID:   "17",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				resp := reg.HandleMessage(context.Background(), []byte(tc.input))
				var env struct {
					JSONRPC string          `json:"jsonrpc"`
					ID      json.RawMessage `json:"id"`
					Error   *RPCError       `json:"error"`
				}
				if err := json.Unmarshal(resp, &env); err != nil {
					t.Fatalf("unmarshal error response: %v (%s)", err, resp)
				}
				if env.Error == nil {
					t.Fatalf("expected error response, got: %s", resp)
				}
				if env.Error.Code != tc.wantCode {
					t.Fatalf("error.code = %d, want %d (msg=%q)", env.Error.Code, tc.wantCode, env.Error.Message)
				}
				if string(env.ID) != tc.wantID {
					t.Fatalf("id = %s, want %s", env.ID, tc.wantID)
				}
			})
		}
	})
}

func TestRegistryToolsResourcesPrompts(t *testing.T) {
	reg := NewRegistry(ServerInfo{})

	t.Run("empty lists marshal as non-null JSON arrays", func(t *testing.T) {
		for _, tc := range []struct {
			method string
			field  string
		}{
			{"tools/list", `"tools":[]`},
			{"resources/list", `"resources":[]`},
			{"prompts/list", `"prompts":[]`},
		} {
			resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"`+tc.method+`"}`))
			if !strings.Contains(string(resp), tc.field) {
				t.Fatalf("%s expected %s in %s", tc.method, tc.field, resp)
			}
		}
	})

	reg.RegisterTool(Tool{
		Name:        "zeta_tool",
		Description: "Zeta",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, _ json.RawMessage) (CallToolResult, error) {
		return TextToolResult("zeta-ok"), nil
	})
	reg.RegisterTool(Tool{
		Name:        "alpha_tool",
		Description: "Alpha",
		InputSchema: map[string]any{"type": "object"},
	}, func(_ context.Context, args json.RawMessage) (CallToolResult, error) {
		var in struct {
			Fail    bool `json:"fail"`
			BadArgs bool `json:"badArgs"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return CallToolResult{}, InvalidParamsError("invalid args")
			}
		}
		if in.BadArgs {
			return CallToolResult{}, InvalidParamsError("badArgs requested")
		}
		if in.Fail {
			return CallToolResult{}, errors.New("tool execution exploded")
		}
		return TextToolResult("alpha-ok"), nil
	})

	reg.RegisterResource(Resource{
		URI:      "ytmix://b",
		Name:     "B",
		MimeType: "application/json",
	}, func(_ context.Context, uri string) ([]ResourceContent, error) {
		return []ResourceContent{{URI: uri, MimeType: "application/json", Text: `{"b":2}`}}, nil
	})
	reg.RegisterResource(Resource{
		URI:      "ytmix://a",
		Name:     "A",
		MimeType: "application/json",
	}, func(_ context.Context, uri string) ([]ResourceContent, error) {
		return []ResourceContent{{URI: uri, MimeType: "application/json", Text: `{"a":1}`}}, nil
	})

	reg.RegisterPrompt(Prompt{
		Name:        "z-prompt",
		Description: "Z",
	}, func(_ context.Context, _ map[string]string) (PromptResult, error) {
		return PromptResult{Messages: []PromptMessage{{Role: "user", Content: PromptContent{Type: "text", Text: "z"}}}}, nil
	})
	reg.RegisterPrompt(Prompt{
		Name:        "a-prompt",
		Description: "A",
		Arguments:   []PromptArgument{{Name: "audioDir", Required: false}},
	}, func(_ context.Context, args map[string]string) (PromptResult, error) {
		dir := args["audioDir"]
		return PromptResult{
			Description: "A prompt",
			Messages:    []PromptMessage{{Role: "user", Content: PromptContent{Type: "text", Text: "dir=" + dir}}},
		}, nil
	})

	t.Run("tools/list is deterministically sorted", func(t *testing.T) {
		resp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		var env struct {
			Result struct {
				Tools []Tool `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(resp, &env); err != nil {
			t.Fatal(err)
		}
		if len(env.Result.Tools) != 2 || env.Result.Tools[0].Name != "alpha_tool" || env.Result.Tools[1].Name != "zeta_tool" {
			t.Fatalf("unexpected tools order: %+v", env.Result.Tools)
		}
	})

	t.Run("tools/call handles success, execution error as isError, and RPCError as JSON-RPC error", func(t *testing.T) {
		okResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"alpha_tool","arguments":{}}}`))
		var okEnv struct {
			Result CallToolResult `json:"result"`
			Error  *RPCError      `json:"error"`
		}
		if err := json.Unmarshal(okResp, &okEnv); err != nil {
			t.Fatal(err)
		}
		if okEnv.Error != nil || okEnv.Result.IsError || len(okEnv.Result.Content) != 1 || okEnv.Result.Content[0].Text != "alpha-ok" {
			t.Fatalf("unexpected ok tools/call: %s", okResp)
		}

		execErrResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"alpha_tool","arguments":{"fail":true}}}`))
		var errEnv struct {
			Result CallToolResult `json:"result"`
			Error  *RPCError      `json:"error"`
		}
		if err := json.Unmarshal(execErrResp, &errEnv); err != nil {
			t.Fatal(err)
		}
		if errEnv.Error != nil || !errEnv.Result.IsError || len(errEnv.Result.Content) != 1 || !strings.Contains(errEnv.Result.Content[0].Text, "exploded") {
			t.Fatalf("expected isError=true CallToolResult, got: %s", execErrResp)
		}

		rpcErrResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"alpha_tool","arguments":{"badArgs":true}}}`))
		var rpcEnv struct {
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(rpcErrResp, &rpcEnv); err != nil {
			t.Fatal(err)
		}
		if rpcEnv.Error == nil || rpcEnv.Error.Code != ErrCodeInvalidParams {
			t.Fatalf("expected -32602 RPCError, got: %s", rpcErrResp)
		}
	})

	t.Run("resources/list and resources/read work in sorted order", func(t *testing.T) {
		listResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
		var listEnv struct {
			Result struct {
				Resources []Resource `json:"resources"`
			} `json:"result"`
		}
		if err := json.Unmarshal(listResp, &listEnv); err != nil {
			t.Fatal(err)
		}
		if len(listEnv.Result.Resources) != 2 || listEnv.Result.Resources[0].URI != "ytmix://a" || listEnv.Result.Resources[1].URI != "ytmix://b" {
			t.Fatalf("unexpected resources order: %+v", listEnv.Result.Resources)
		}

		readResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ytmix://a"}}`))
		var readEnv struct {
			Result struct {
				Contents []ResourceContent `json:"contents"`
			} `json:"result"`
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(readResp, &readEnv); err != nil {
			t.Fatal(err)
		}
		if readEnv.Error != nil || len(readEnv.Result.Contents) != 1 || readEnv.Result.Contents[0].Text != `{"a":1}` {
			t.Fatalf("unexpected resources/read response: %s", readResp)
		}
	})

	t.Run("prompts/list and prompts/get work in sorted order", func(t *testing.T) {
		listResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`))
		var listEnv struct {
			Result struct {
				Prompts []Prompt `json:"prompts"`
			} `json:"result"`
		}
		if err := json.Unmarshal(listResp, &listEnv); err != nil {
			t.Fatal(err)
		}
		if len(listEnv.Result.Prompts) != 2 || listEnv.Result.Prompts[0].Name != "a-prompt" || listEnv.Result.Prompts[1].Name != "z-prompt" {
			t.Fatalf("unexpected prompts order: %+v", listEnv.Result.Prompts)
		}

		getResp := reg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"a-prompt","arguments":{"audioDir":"/music"}}}`))
		var getEnv struct {
			Result PromptResult `json:"result"`
			Error  *RPCError    `json:"error"`
		}
		if err := json.Unmarshal(getResp, &getEnv); err != nil {
			t.Fatal(err)
		}
		if getEnv.Error != nil || len(getEnv.Result.Messages) != 1 || getEnv.Result.Messages[0].Content.Text != "dir=/music" {
			t.Fatalf("unexpected prompts/get response: %s", getResp)
		}
	})

	t.Run("JSON-RPC 2.0 batch requests handle mixed requests and notifications", func(t *testing.T) {
		batchResp := reg.HandleMessage(context.Background(), []byte(`[
			{"jsonrpc":"2.0","id":1,"method":"ping"},
			{"jsonrpc":"2.0","method":"notifications/initialized"},
			{"jsonrpc":"2.0","id":2,"method":"tools/list"}
		]`))
		var items []json.RawMessage
		if err := json.Unmarshal(batchResp, &items); err != nil {
			t.Fatalf("expected JSON array batch response, got %s (%v)", batchResp, err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 responses in batch (notification omitted), got %d: %s", len(items), batchResp)
		}

		notifOnly := reg.HandleMessage(context.Background(), []byte(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`))
		if notifOnly != nil {
			t.Fatalf("expected nil for notification-only batch, got %s", notifOnly)
		}

		emptyBatch := reg.HandleMessage(context.Background(), []byte(`[]`))
		var env struct {
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(emptyBatch, &env); err != nil || env.Error == nil || env.Error.Code != ErrCodeInvalidRequest {
			t.Fatalf("expected -32600 for empty batch, got %s", emptyBatch)
		}
	})

	t.Run("DecodeToolArgs validates structs and rejects unknown fields or trailing tokens", func(t *testing.T) {
		type sampleArgs struct {
			AudioDir string `json:"audioDir"`
		}

		got, err := DecodeToolArgs[sampleArgs](json.RawMessage(`{"audioDir":"/tmp/tracks"}`))
		if err != nil || got.AudioDir != "/tmp/tracks" {
			t.Fatalf("expected valid decode, got=%+v err=%v", got, err)
		}

		if emptyGot, err := DecodeToolArgs[sampleArgs](nil); err != nil || emptyGot.AudioDir != "" {
			t.Fatalf("expected empty nil args to decode cleanly, got=%+v err=%v", emptyGot, err)
		}

		for _, raw := range []string{
			`{"audioDir":"/tmp","extra":true}`,
			`{"audioDir":123}`,
			`{"audioDir":"/tmp"}{"trailing":1}`,
			`[]`,
		} {
			if _, err := DecodeToolArgs[sampleArgs](json.RawMessage(raw)); err == nil {
				t.Fatalf("expected InvalidParamsError for %s", raw)
			}
		}
	})

	t.Run("recovers panics in tool, resource, and prompt handlers without crashing", func(t *testing.T) {
		panicReg := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		panicReg.RegisterTool(Tool{
			Name:        "boom_tool",
			InputSchema: map[string]any{"type": "object"},
		}, func(_ context.Context, _ json.RawMessage) (CallToolResult, error) {
			panic("tool exploded")
		})
		panicReg.RegisterResource(Resource{
			URI:  "ytmix://boom",
			Name: "Boom",
		}, func(_ context.Context, _ string) ([]ResourceContent, error) {
			panic("resource exploded")
		})
		panicReg.RegisterPrompt(Prompt{
			Name: "boom_prompt",
		}, func(_ context.Context, _ map[string]string) (PromptResult, error) {
			panic("prompt exploded")
		})

		for _, req := range []string{
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom_tool"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ytmix://boom"}}`,
			`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"boom_prompt"}}`,
		} {
			resp := panicReg.HandleMessage(context.Background(), []byte(req))
			var env struct {
				Error *RPCError `json:"error"`
			}
			if err := json.Unmarshal(resp, &env); err != nil || env.Error == nil || env.Error.Code != ErrCodeInternal {
				t.Fatalf("expected -32603 internal error for panicking request %s, got %s", req, resp)
			}
		}

		// Verify registry still responds normally after panic recovery.
		pingResp := panicReg.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":4,"method":"ping"}`))
		if !strings.Contains(string(pingResp), `"result":{}`) {
			t.Fatalf("expected server to remain responsive after panic, got %s", pingResp)
		}
	})

	t.Run("UnregisterResource removes registered URI and ignores nil or empty", func(t *testing.T) {
		var nilReg *Registry
		nilReg.UnregisterResource("ytmix://ignored")

		r := NewRegistry(ServerInfo{Name: "ytmix", Version: "0.1.0"})
		r.RegisterResource(Resource{URI: "ytmix://jobs/job_1", Name: "Job 1"}, func(_ context.Context, uri string) ([]ResourceContent, error) {
			return []ResourceContent{{URI: uri, Text: "{}"}}, nil
		})
		r.UnregisterResource("")
		r.UnregisterResource("   ")

		readBefore := r.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ytmix://jobs/job_1"}}`))
		if strings.Contains(string(readBefore), `"error"`) {
			t.Fatalf("expected resource to be readable before unregister, got %s", readBefore)
		}

		r.UnregisterResource("ytmix://jobs/job_1")
		readAfter := r.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ytmix://jobs/job_1"}}`))
		var env struct {
			Error *RPCError `json:"error"`
		}
		if err := json.Unmarshal(readAfter, &env); err != nil || env.Error == nil || env.Error.Code != ErrCodeResourceNotFound {
			t.Fatalf("expected -32002 after unregister, got %s", readAfter)
		}

		listAfter := r.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`))
		if strings.Contains(string(listAfter), "ytmix://jobs/job_1") {
			t.Fatalf("expected unregistered resource omitted from resources/list, got %s", listAfter)
		}
	})
}
