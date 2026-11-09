//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type dynamicHeaderContextKey struct{}

func TestToolSet_HTTPTypeUnionSchema(t *testing.T) {
	inputSchema := json.RawMessage(`{"type":"object","properties":{"limit":{"type":["null","integer"],"default":50}}}`)
	outputSchema := json.RawMessage(`{"type":"object","properties":{"count":{"type":["integer","null"]}}}`)
	arguments := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "schema-test", "version": "1.0.0"},
				"capabilities":    map[string]any{},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name": "read_records", "inputSchema": inputSchema, "outputSchema": outputSchema,
			}}}
		case "tools/call":
			args, _ := request.Params["arguments"].(map[string]any)
			arguments <- args
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	toolSet := NewMCPToolSet(ConnectionConfig{Transport: "streamable", ServerURL: server.URL},
		WithMCPOptions(tmcp.WithClientGetSSEEnabled(false)))
	defer func() { require.NoError(t, toolSet.Close()) }()
	require.NoError(t, toolSet.Init(ctx))
	tools := toolSet.Tools(ctx)
	require.Len(t, tools, 1)
	declaration := tools[0].Declaration()
	for _, schema := range []*tool.Schema{
		declaration.InputSchema.Properties["limit"], declaration.OutputSchema.Properties["count"],
	} {
		require.Empty(t, schema.Type)
		require.Len(t, schema.AnyOf, 2)
	}
	require.Equal(t, "null", declaration.InputSchema.Properties["limit"].AnyOf[0].Type)
	require.Equal(t, "integer", declaration.InputSchema.Properties["limit"].AnyOf[1].Type)
	require.Equal(t, float64(50), declaration.InputSchema.Properties["limit"].Default)
	require.Equal(t, "integer", declaration.OutputSchema.Properties["count"].AnyOf[0].Type)
	require.Equal(t, "null", declaration.OutputSchema.Properties["count"].AnyOf[1].Type)
	require.Empty(t, declaration.InputSchema.Required, "optional and nullable are independent")
	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)
	for _, tc := range []struct {
		args string
		want any
	}{
		{`{"limit":100}`, float64(100)},
		{`{"limit":null}`, nil},
		{`{"limit":"100"}`, "100"},
	} {
		_, err := callable.Call(ctx, []byte(tc.args))
		require.NoError(t, err)
		select {
		case received := <-arguments:
			require.Contains(t, received, "limit")
			require.Equal(t, tc.want, received["limit"], "the client must not coerce argument types")
		case <-ctx.Done():
			t.Fatal("tools/call was not received")
		}
	}
}

// TestToolSet_PerRequestHeadersViaBeforeRequest verifies that a user-supplied
// mcp.WithHTTPBeforeRequest hook (passed through WithMCPOptions) observes the
// per-call context for every outgoing MCP HTTP request, so callers can read
// context values and inject request-scoped headers such as per-user auth
// tokens.
func TestToolSet_PerRequestHeadersViaBeforeRequest(t *testing.T) {
	handler := &recordingMCPHTTPHandler{}
	toolSet := NewMCPToolSet(
		ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://mcp.test",
			Headers: map[string]string{
				"X-Static": "static",
			},
		},
		WithMCPOptions(
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
			tmcp.WithHTTPBeforeRequest(
				func(ctx context.Context, req *http.Request) error {
					token, ok := ctx.Value(dynamicHeaderContextKey{}).(string)
					if !ok || token == "" {
						return nil
					}
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("X-Dynamic", token)
					return nil
				},
			),
		),
	)
	defer func() { _ = toolSet.Close() }()

	initCtx := context.WithValue(
		context.Background(),
		dynamicHeaderContextKey{},
		"init-token",
	)
	require.NoError(t, toolSet.sessionManager.connect(initCtx))

	callCtx := context.WithValue(
		context.Background(),
		dynamicHeaderContextKey{},
		"call-token",
	)
	_, err := toolSet.sessionManager.callTool(
		callCtx,
		"echo",
		map[string]any{"q": "hello"},
	)
	require.NoError(t, err)

	initHeaders := handler.headersForMethod(t, "initialize")
	require.Equal(t, "static", initHeaders.Get("X-Static"))
	require.Equal(t, "Bearer init-token", initHeaders.Get("Authorization"))
	require.Equal(t, "init-token", initHeaders.Get("X-Dynamic"))

	callHeaders := handler.headersForMethod(t, "tools/call")
	require.Equal(t, "static", callHeaders.Get("X-Static"))
	require.Equal(t, "Bearer call-token", callHeaders.Get("Authorization"))
	require.Equal(t, "call-token", callHeaders.Get("X-Dynamic"))
}

// TestToolSet_BeforeRequestHookReturnsError verifies that a non-nil error
// returned from WithHTTPBeforeRequest aborts the MCP request and is surfaced
// to the caller.
func TestToolSet_BeforeRequestHookReturnsError(t *testing.T) {
	handler := &recordingMCPHTTPHandler{}
	toolSet := NewMCPToolSet(
		ConnectionConfig{
			Transport: "streamable",
			ServerURL: "http://mcp.test",
		},
		WithMCPOptions(
			tmcp.WithClientGetSSEEnabled(false),
			tmcp.WithHTTPReqHandler(handler),
			tmcp.WithHTTPBeforeRequest(
				func(ctx context.Context, req *http.Request) error {
					return context.Canceled
				},
			),
		),
	)
	defer func() { _ = toolSet.Close() }()

	err := toolSet.sessionManager.connect(context.Background())
	require.Error(t, err)
}

type recordingMCPHTTPHandler struct {
	mu       sync.Mutex
	requests []recordedMCPRequest
}

type recordedMCPRequest struct {
	method  string
	headers http.Header
}

func (h *recordingMCPHTTPHandler) Handle(
	_ context.Context,
	_ *http.Client,
	req *http.Request,
) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &envelope)

	h.mu.Lock()
	h.requests = append(h.requests, recordedMCPRequest{
		method:  envelope.Method,
		headers: req.Header.Clone(),
	})
	h.mu.Unlock()

	if envelope.ID == nil {
		return mcpHTTPResponse(http.StatusAccepted, ""), nil
	}

	switch envelope.Method {
	case "initialize":
		return mcpJSONRPCResponse(http.StatusOK, envelope.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"serverInfo": map[string]any{
				"name":    "test",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{},
		}), nil
	case "tools/call":
		return mcpJSONRPCResponse(http.StatusOK, envelope.ID, map[string]any{
			"content": []map[string]any{{
				"type": "text",
				"text": "ok",
			}},
		}), nil
	default:
		return mcpJSONRPCResponse(http.StatusOK, envelope.ID, map[string]any{
			"tools": []any{},
		}), nil
	}
}

func (h *recordingMCPHTTPHandler) headersForMethod(
	t *testing.T,
	method string,
) http.Header {
	t.Helper()

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, req := range h.requests {
		if req.method == method {
			return req.headers
		}
	}
	require.Failf(t, "missing recorded MCP request", "method %s", method)
	return nil
}

func mcpHTTPResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func mcpJSONRPCResponse(status int, id any, result any) *http.Response {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
	return mcpHTTPResponse(status, string(body))
}
