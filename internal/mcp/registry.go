package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// DefaultProtocolVersion is the preferred MCP protocol version negotiated during initialize.
	DefaultProtocolVersion = "2025-03-26"

	ErrCodeParse            = -32700
	ErrCodeInvalidRequest   = -32600
	ErrCodeMethodNotFound   = -32601
	ErrCodeInvalidParams    = -32602
	ErrCodeInternal         = -32603
	ErrCodeResourceNotFound = -32002
)

var supportedProtocolVersions = map[string]bool{
	"2025-03-26": true,
	"2024-11-05": true,
}

// CommandRunner executes an external binary and returns stdout or an error containing stderr.
type CommandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error)

// ServerInfo identifies this MCP server in the initialize response.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// RPCError represents a JSON-RPC 2.0 error payload.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface.
func (e *RPCError) Error() string {
	return e.Message
}

// InvalidParamsError returns a JSON-RPC -32602 Invalid params error.
func InvalidParamsError(msg string) *RPCError {
	return &RPCError{Code: ErrCodeInvalidParams, Message: msg}
}

// ResourceNotFoundError returns an MCP -32002 Resource not found error.
func ResourceNotFoundError(uri string) *RPCError {
	return &RPCError{Code: ErrCodeResourceNotFound, Message: fmt.Sprintf("resource not found: %s", uri)}
}

// Tool describes an MCP tool exposed via tools/list and invoked via tools/call.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema"`
}

// ContentBlock represents a single MCP content item in a tool result.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// CallToolResult is the MCP response returned by tools/call.
type CallToolResult struct {
	Content           []ContentBlock `json:"content"`
	StructuredContent any            `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

// TextToolResult builds a successful text-content CallToolResult.
func TextToolResult(text string) CallToolResult {
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}

// JSONToolResult serializes payload as indented JSON text alongside structuredContent.
func JSONToolResult(payload any) (CallToolResult, error) {
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return CallToolResult{}, fmt.Errorf("marshal tool result: %w", err)
	}
	return CallToolResult{
		Content:           []ContentBlock{{Type: "text", Text: string(raw)}},
		StructuredContent: payload,
	}, nil
}

// ErrorToolResult builds a CallToolResult flagged with IsError=true.
func ErrorToolResult(msg string) CallToolResult {
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// ToolHandler executes a registered MCP tool with raw JSON arguments.
type ToolHandler func(ctx context.Context, args json.RawMessage) (CallToolResult, error)

// Resource describes a static MCP resource exposed via resources/list and resources/read.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// ResourceContent represents a single content payload returned by resources/read.
type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ResourceReader reads the contents of a registered MCP resource URI.
type ResourceReader func(ctx context.Context, uri string) ([]ResourceContent, error)

// PromptArgument describes an optional or required parameter for an MCP prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Prompt describes an MCP prompt template exposed via prompts/list and prompts/get.
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptContent represents text content inside an MCP prompt message.
type PromptContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// PromptMessage represents one role/content turn returned by prompts/get.
type PromptMessage struct {
	Role    string        `json:"role"`
	Content PromptContent `json:"content"`
}

// PromptResult is the MCP response returned by prompts/get.
type PromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptGetter materializes a registered MCP prompt with caller-supplied arguments.
type PromptGetter func(ctx context.Context, args map[string]string) (PromptResult, error)

type registeredTool struct {
	def     Tool
	handler ToolHandler
}

type registeredResource struct {
	def    Resource
	reader ResourceReader
}

type registeredPrompt struct {
	def    Prompt
	getter PromptGetter
}

// Registry stores registered MCP tools, resources, and prompts and dispatches JSON-RPC 2.0 messages.
type Registry struct {
	info        ServerInfo
	initialized atomic.Bool

	mu        sync.RWMutex
	tools     map[string]registeredTool
	resources map[string]registeredResource
	prompts   map[string]registeredPrompt
}

// Initialized reports whether notifications/initialized has been received.
func (r *Registry) Initialized() bool {
	if r == nil {
		return false
	}
	return r.initialized.Load()
}

// NewRegistry constructs an empty MCP Registry with normalized server metadata.
func NewRegistry(info ServerInfo) *Registry {
	if strings.TrimSpace(info.Name) == "" {
		info.Name = "ytmix"
	}
	if strings.TrimSpace(info.Version) == "" {
		info.Version = "dev"
	}
	return &Registry{
		info:      info,
		tools:     make(map[string]registeredTool),
		resources: make(map[string]registeredResource),
		prompts:   make(map[string]registeredPrompt),
	}
}

// RegisterTool registers (or replaces) an MCP tool by name.
func (r *Registry) RegisterTool(def Tool, handler ToolHandler) {
	if r == nil || strings.TrimSpace(def.Name) == "" || handler == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[def.Name] = registeredTool{def: def, handler: handler}
}

// RegisterResource registers (or replaces) a static MCP resource by URI.
func (r *Registry) RegisterResource(def Resource, reader ResourceReader) {
	if r == nil || strings.TrimSpace(def.URI) == "" || reader == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resources[def.URI] = registeredResource{def: def, reader: reader}
}

// RegisterPrompt registers (or replaces) an MCP prompt by name.
func (r *Registry) RegisterPrompt(def Prompt, getter PromptGetter) {
	if r == nil || strings.TrimSpace(def.Name) == "" || getter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts[def.Name] = registeredPrompt{def: def, getter: getter}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

var nullID = json.RawMessage("null")

// HandleMessage processes a single JSON-RPC 2.0 message (object or batch array) and returns
// the serialized JSON response, or nil when only notifications were processed.
func (r *Registry) HandleMessage(ctx context.Context, raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return marshalResponse(rpcResponse{
			JSONRPC: "2.0",
			ID:      nullID,
			Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: "empty JSON-RPC message"},
		})
	}

	if trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return marshalResponse(rpcResponse{
				JSONRPC: "2.0",
				ID:      nullID,
				Error:   &RPCError{Code: ErrCodeParse, Message: "parse error: invalid JSON batch"},
			})
		}
		if len(batch) == 0 {
			return marshalResponse(rpcResponse{
				JSONRPC: "2.0",
				ID:      nullID,
				Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: "invalid request: empty batch"},
			})
		}
		responses := make([]rpcResponse, 0, len(batch))
		for _, item := range batch {
			if resp, ok := r.handleSingleObject(ctx, item); ok {
				responses = append(responses, resp)
			}
		}
		if len(responses) == 0 {
			return nil
		}
		out, err := json.Marshal(responses)
		if err != nil {
			return marshalResponse(rpcResponse{
				JSONRPC: "2.0",
				ID:      nullID,
				Error:   &RPCError{Code: ErrCodeInternal, Message: fmt.Sprintf("marshal batch response: %v", err)},
			})
		}
		return out
	}

	resp, ok := r.handleSingleObject(ctx, trimmed)
	if !ok {
		return nil
	}
	return marshalResponse(resp)
}

func marshalResponse(resp rpcResponse) []byte {
	out, err := json.Marshal(resp)
	if err != nil {
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"internal error: marshal response"}}`)
	}
	return out
}

func hasJSONObjectPrefix(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func parseValidRequestID(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}
	if bytes.Equal(trimmed, nullID) {
		return nullID, false
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nullID, false
		}
		return trimmed, true
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var num json.Number
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&num); err != nil {
			return nullID, false
		}
		return trimmed, true
	default:
		return nullID, false
	}
}

func (r *Registry) handleSingleObject(ctx context.Context, raw []byte) (rpcResponse, bool) {
	trimmed := bytes.TrimSpace(raw)
	var probeVal any
	if err := json.Unmarshal(trimmed, &probeVal); err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      nullID,
			Error:   &RPCError{Code: ErrCodeParse, Message: "parse error: invalid JSON"},
		}, true
	}
	if !hasJSONObjectPrefix(trimmed) {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      nullID,
			Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: "invalid request: expected JSON object"},
		}, true
	}

	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      nullID,
			Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: "invalid request: malformed fields"},
		}, true
	}

	hasID := len(bytes.TrimSpace(req.ID)) > 0
	respID := nullID
	if hasID {
		parsedID, valid := parseValidRequestID(req.ID)
		if !valid {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      nullID,
				Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: "invalid request: id must be a string or number"},
			}, true
		}
		respID = parsedID
	}

	if req.JSONRPC != "2.0" {
		if !hasID {
			return rpcResponse{}, false
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      respID,
			Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: `invalid request: jsonrpc must be "2.0"`},
		}, true
	}
	if strings.TrimSpace(req.Method) == "" {
		if !hasID {
			return rpcResponse{}, false
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      respID,
			Error:   &RPCError{Code: ErrCodeInvalidRequest, Message: "invalid request: method must be a non-empty string"},
		}, true
	}

	if !hasID {
		r.handleNotification(req.Method)
		return rpcResponse{}, false
	}

	result, rpcErr := r.dispatchRequest(ctx, req.Method, req.Params)
	if rpcErr != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      respID,
			Error:   rpcErr,
		}, true
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      respID,
		Result:  result,
	}, true
}

func (r *Registry) handleNotification(method string) {
	if method == "notifications/initialized" {
		r.initialized.Store(true)
	}
}

func validateOptionalObjectParams(params json.RawMessage) *RPCError {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, nullID) {
		return nil
	}
	if !hasJSONObjectPrefix(trimmed) {
		return InvalidParamsError("invalid params: expected JSON object")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return InvalidParamsError("invalid params: malformed JSON object")
	}
	return nil
}

func decodeObjectParams(params json.RawMessage, method string, dst any) *RPCError {
	if !hasJSONObjectPrefix(params) {
		return InvalidParamsError("invalid params: expected JSON object")
	}
	if err := json.Unmarshal(params, dst); err != nil {
		return InvalidParamsError(fmt.Sprintf("invalid %s params", method))
	}
	return nil
}

func mapHandlerRPCError(err error) *RPCError {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &RPCError{Code: ErrCodeInternal, Message: err.Error()}
}

func (r *Registry) dispatchRequest(ctx context.Context, method string, params json.RawMessage) (any, *RPCError) {
	if ctx == nil {
		ctx = context.Background()
	}

	switch method {
	case "initialize":
		return r.handleInitialize(params)
	case "ping":
		if rpcErr := validateOptionalObjectParams(params); rpcErr != nil {
			return nil, rpcErr
		}
		return map[string]any{}, nil
	case "tools/list":
		if rpcErr := validateOptionalObjectParams(params); rpcErr != nil {
			return nil, rpcErr
		}
		return r.handleToolsList(), nil
	case "tools/call":
		return r.handleToolsCall(ctx, params)
	case "resources/list":
		if rpcErr := validateOptionalObjectParams(params); rpcErr != nil {
			return nil, rpcErr
		}
		return r.handleResourcesList(), nil
	case "resources/read":
		return r.handleResourcesRead(ctx, params)
	case "prompts/list":
		if rpcErr := validateOptionalObjectParams(params); rpcErr != nil {
			return nil, rpcErr
		}
		return r.handlePromptsList(), nil
	case "prompts/get":
		return r.handlePromptsGet(ctx, params)
	default:
		return nil, &RPCError{Code: ErrCodeMethodNotFound, Message: fmt.Sprintf("method not found: %s", method)}
	}
}

func (r *Registry) handleInitialize(params json.RawMessage) (any, *RPCError) {
	if rpcErr := validateOptionalObjectParams(params); rpcErr != nil {
		return nil, rpcErr
	}
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, nullID) {
		if err := json.Unmarshal(trimmed, &in); err != nil {
			return nil, InvalidParamsError("invalid initialize params")
		}
	}

	version := DefaultProtocolVersion
	if supportedProtocolVersions[in.ProtocolVersion] {
		version = in.ProtocolVersion
	}

	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
		},
		"serverInfo": r.info,
	}, nil
}

func (r *Registry) handleToolsList() any {
	r.mu.RLock()
	tools := make([]Tool, 0, len(r.tools))
	for _, entry := range r.tools {
		tools = append(tools, entry.def)
	}
	r.mu.RUnlock()

	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return map[string]any{
		"tools": tools,
	}
}

func (r *Registry) handleToolsCall(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var in struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if rpcErr := decodeObjectParams(params, "tools/call", &in); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, InvalidParamsError("invalid tools/call params: name is required")
	}

	args := bytes.TrimSpace(in.Arguments)
	if len(args) == 0 || bytes.Equal(args, nullID) {
		args = json.RawMessage("{}")
	} else if !hasJSONObjectPrefix(args) {
		return nil, InvalidParamsError("invalid tools/call params: arguments must be a JSON object")
	}

	r.mu.RLock()
	entry, ok := r.tools[in.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, InvalidParamsError(fmt.Sprintf("unknown tool %q", in.Name))
	}

	res, err := entry.handler(ctx, args)
	if err != nil {
		var rpcErr *RPCError
		if errors.As(err, &rpcErr) {
			return nil, rpcErr
		}
		return ErrorToolResult(err.Error()), nil
	}
	if res.Content == nil {
		res.Content = []ContentBlock{}
	}
	return res, nil
}

func (r *Registry) handleResourcesList() any {
	r.mu.RLock()
	resources := make([]Resource, 0, len(r.resources))
	for _, entry := range r.resources {
		resources = append(resources, entry.def)
	}
	r.mu.RUnlock()

	sort.Slice(resources, func(i, j int) bool {
		return resources[i].URI < resources[j].URI
	})
	return map[string]any{
		"resources": resources,
	}
}

func (r *Registry) handleResourcesRead(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var in struct {
		URI string `json:"uri"`
	}
	if rpcErr := decodeObjectParams(params, "resources/read", &in); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(in.URI) == "" {
		return nil, InvalidParamsError("invalid resources/read params: uri is required")
	}

	r.mu.RLock()
	entry, ok := r.resources[in.URI]
	r.mu.RUnlock()
	if !ok {
		return nil, ResourceNotFoundError(in.URI)
	}

	contents, err := entry.reader(ctx, in.URI)
	if err != nil {
		return nil, mapHandlerRPCError(err)
	}
	if contents == nil {
		contents = []ResourceContent{}
	}
	return map[string]any{
		"contents": contents,
	}, nil
}

func (r *Registry) handlePromptsList() any {
	r.mu.RLock()
	prompts := make([]Prompt, 0, len(r.prompts))
	for _, entry := range r.prompts {
		prompts = append(prompts, entry.def)
	}
	r.mu.RUnlock()

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})
	return map[string]any{
		"prompts": prompts,
	}
}

func (r *Registry) handlePromptsGet(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var in struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if rpcErr := decodeObjectParams(params, "prompts/get", &in); rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, InvalidParamsError("invalid prompts/get params: name is required")
	}

	r.mu.RLock()
	entry, ok := r.prompts[in.Name]
	r.mu.RUnlock()
	if !ok {
		return nil, InvalidParamsError(fmt.Sprintf("unknown prompt %q", in.Name))
	}

	res, err := entry.getter(ctx, in.Arguments)
	if err != nil {
		return nil, mapHandlerRPCError(err)
	}
	if res.Messages == nil {
		res.Messages = []PromptMessage{}
	}
	return res, nil
}
