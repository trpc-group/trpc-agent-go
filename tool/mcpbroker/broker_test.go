//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool"
	legacymcp "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type brokerHTTPServer struct {
	URL string

	server       *httptest.Server
	mu           sync.Mutex
	methodCount  map[string]int
	lastXTest    string
	lastAuth     string
	requiredAuth string
}

func startBrokerHTTPServer(t *testing.T) *brokerHTTPServer {
	t.Helper()
	return startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{})
}

type brokerHTTPServerOptions struct {
	RequiredAuth       string
	RegisterDottedTool bool
	RegisterStructured bool
	ServerPath         string
}

func startBrokerHTTPServerWithOptions(t *testing.T, opts brokerHTTPServerOptions) *brokerHTTPServer {
	t.Helper()

	mcpServer := tmcp.NewServer(
		"broker-test-server",
		"1.0.0",
		tmcp.WithServerPath(func() string {
			if strings.TrimSpace(opts.ServerPath) != "" {
				return opts.ServerPath
			}
			return "/mcp"
		}()),
	)

	type echoOutput struct {
		Message string `json:"message"`
	}

	echoTool := tmcp.NewTool(
		"echo",
		tmcp.WithDescription("Echo text."),
		tmcp.WithString("text", tmcp.Required(), tmcp.Description("Text to echo.")),
		tmcp.WithOutputStruct[echoOutput](),
	)
	mcpServer.RegisterTool(echoTool, func(ctx context.Context, req *tmcp.CallToolRequest) (*tmcp.CallToolResult, error) {
		text, _ := req.Params.Arguments["text"].(string)
		return tmcp.NewTextResult("Echo: " + text), nil
	})

	addTool := tmcp.NewTool(
		"add",
		tmcp.WithDescription("Add two numbers."),
		tmcp.WithNumber("a", tmcp.Required(), tmcp.Description("First number.")),
		tmcp.WithNumber("b", tmcp.Required(), tmcp.Description("Second number.")),
	)
	mcpServer.RegisterTool(addTool, func(ctx context.Context, req *tmcp.CallToolRequest) (*tmcp.CallToolResult, error) {
		a, _ := req.Params.Arguments["a"].(float64)
		b, _ := req.Params.Arguments["b"].(float64)
		return tmcp.NewTextResult(fmt.Sprintf("%g", a+b)), nil
	})

	if opts.RegisterStructured {
		type structuredOutput struct {
			Message string `json:"message"`
		}

		structuredTool := tmcp.NewTool(
			"structured_echo",
			tmcp.WithDescription("Echo text as structured output."),
			tmcp.WithString("text", tmcp.Required(), tmcp.Description("Text to echo.")),
			tmcp.WithOutputStruct[structuredOutput](),
		)
		mcpServer.RegisterTool(structuredTool, func(ctx context.Context, req *tmcp.CallToolRequest) (*tmcp.CallToolResult, error) {
			text, _ := req.Params.Arguments["text"].(string)
			message := "Echo: " + text
			return &tmcp.CallToolResult{
				Content:           []tmcp.Content{tmcp.NewTextContent("duplicate text: " + message)},
				StructuredContent: map[string]any{"message": message},
			}, nil
		})
	}

	if opts.RegisterDottedTool {
		dottedTool := tmcp.NewTool(
			"smartsheet.list_tables",
			tmcp.WithDescription("List tables for a smart sheet."),
			tmcp.WithString("file_id", tmcp.Required(), tmcp.Description("Sheet file id.")),
		)
		mcpServer.RegisterTool(dottedTool, func(ctx context.Context, req *tmcp.CallToolRequest) (*tmcp.CallToolResult, error) {
			fileID, _ := req.Params.Arguments["file_id"].(string)
			return tmcp.NewTextResult("tables for " + fileID), nil
		})
	}

	result := &brokerHTTPServer{
		methodCount:  map[string]int{},
		requiredAuth: opts.RequiredAuth,
	}

	baseHandler := mcpServer.HTTPHandler()
	result.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if result.requiredAuth != "" && authHeader != result.requiredAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			var request struct {
				Method string `json:"method"`
			}
			if err := json.Unmarshal(body, &request); err == nil && request.Method != "" {
				result.mu.Lock()
				result.methodCount[request.Method]++
				result.mu.Unlock()
			}
		}

		result.mu.Lock()
		result.lastXTest = r.Header.Get("X-Test")
		result.lastAuth = authHeader
		result.mu.Unlock()

		baseHandler.ServeHTTP(w, r)
	}))
	serverPath := opts.ServerPath
	if strings.TrimSpace(serverPath) == "" {
		serverPath = "/mcp"
	}
	result.URL = result.server.URL + serverPath
	return result
}

func (s *brokerHTTPServer) Close() {
	s.server.Close()
}

func (s *brokerHTTPServer) MethodCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.methodCount[method]
}

func (s *brokerHTTPServer) LastXTest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastXTest
}

func (s *brokerHTTPServer) LastAuthorization() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuth
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(currentFile)
}

func stdioServerDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(packageDir(t), "testdata", "stdio_server")
}

func TestListServers_ReturnsConfiguredServers(t *testing.T) {
	broker := New(
		WithServers(map[string]legacymcp.ConnectionConfig{
			"from_code": {
				Command: "go",
				Args:    []string{"run", "./server"},
			},
		}),
	)

	output, err := broker.listServers(context.Background(), listServersInput{})
	require.NoError(t, err)
	require.Len(t, output.Servers, 1)

	require.Equal(t, "from_code", output.Servers[0].Name)
	require.Equal(t, "stdio", output.Servers[0].Transport)
}

func TestResolveNamedServers_TrimsNamesAndRejectsCollisions(t *testing.T) {
	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		" trimmed ": {
			Command: "go",
		},
	}))

	servers, merged, err := broker.resolveNamedServers()
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.Equal(t, "trimmed", servers[0].Name)
	require.Contains(t, merged, "trimmed")
	require.NotContains(t, merged, " trimmed ")

	target, err := broker.resolveTarget(targetInput{ServerName: " trimmed "})
	require.NoError(t, err)
	require.Equal(t, "trimmed", target.Name)

	broker = New(WithServers(map[string]legacymcp.ConnectionConfig{
		"dup": {
			Command: "go",
		},
		" dup ": {
			Command: "go",
		},
	}))
	_, _, err = broker.resolveNamedServers()
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate MCP server name")
}

func TestListTools_TargetValidation(t *testing.T) {
	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"named": {
			Command: "go",
			Args:    []string{"run", stdioServerDir(t)},
			Timeout: 10 * time.Second,
		},
	}))

	_, err := broker.listTools(context.Background(), listToolsInput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "selector is required")

	output, err := broker.listTools(context.Background(), listToolsInput{
		Selector:  "named",
		Transport: "streamable",
		Headers:   map[string]string{"X-Test": "ignored"},
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 2)
}

func TestListTools_ReturnsBriefSummariesOnly(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	output, err := broker.listTools(context.Background(), listToolsInput{Selector: server.URL})
	require.NoError(t, err)
	require.Len(t, output.Tools, 2)
	require.NotEmpty(t, output.Tools[0].Signature)
}

func TestInspectTools_ReturnsInputSchemaOnlyByDefault(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	output, err := broker.inspectTools(context.Background(), inspectToolsInput{
		Selector: server.URL,
		Tools:    []string{"echo"},
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 1)
	require.NotNil(t, output.Tools[0].InputSchema)
	require.Contains(t, output.Tools[0].InputSchema, "properties")
	require.Nil(t, output.Tools[0].OutputSchema)
	require.True(t, output.Tools[0].HasOutputSchema)
}

func TestInspectTools_IncludeOutputSchema(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	output, err := broker.inspectTools(context.Background(), inspectToolsInput{
		Selector:            server.URL,
		Tools:               []string{"echo"},
		IncludeOutputSchema: true,
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 1)
	require.NotNil(t, output.Tools[0].OutputSchema)
	require.Contains(t, output.Tools[0].OutputSchema, "properties")
}

func TestInspectTools_Validation(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	_, err := broker.inspectTools(context.Background(), inspectToolsInput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "tools is required")

	_, err = broker.inspectTools(context.Background(), inspectToolsInput{
		Selector: server.URL,
		Tools:    []string{"missing"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requested MCP tools not found")
}

func TestAdHocHeaderDenylist_IsCaseInsensitive(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	_, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
		Headers:  map[string]string{"AUTHORIZATION": "Bearer token"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not allowed")
}

func TestNamedStdioServer_ListTools(t *testing.T) {
	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"local_stdio": {
			Command: "go",
			Args:    []string{"run", stdioServerDir(t)},
			Timeout: 10 * time.Second,
		},
	}))

	output, err := broker.listTools(context.Background(), listToolsInput{
		Selector: "local_stdio",
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 2)
	require.Equal(t, "add", output.Tools[0].Name)
	require.Equal(t, "echo", output.Tools[1].Name)
	require.Equal(t, "local_stdio.add", output.Tools[0].Selector)
}

func TestNamedHTTPServer_ListToolsAndCallTool(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"http_named": {
			ServerURL: server.URL,
			Transport: "streamable_http",
			Timeout:   5 * time.Second,
		},
	}))

	toolsOutput, err := broker.listTools(context.Background(), listToolsInput{
		Selector: "http_named",
	})
	require.NoError(t, err)
	require.Len(t, toolsOutput.Tools, 2)

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  "http_named.echo",
		Arguments: map[string]any{"text": "hello"},
	})
	require.NoError(t, err)
	require.Len(t, callOutput.Content, 1)

	textContent, ok := callOutput.Content[0].(tmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "Echo: hello", textContent.Text)
}

func TestAdHocHTTPServer_ListToolsAndCallTool(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	toolsOutput, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
		Headers:  map[string]string{"X-Test": "from-list"},
	})
	require.NoError(t, err)
	require.Len(t, toolsOutput.Tools, 2)
	require.Equal(t, "from-list", server.LastXTest())

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector: server.URL + ".echo",
		Headers:  map[string]string{"X-Test": "from-call"},
		Arguments: map[string]any{
			"text": "ad-hoc",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "from-call", server.LastXTest())
	require.Len(t, callOutput.Content, 1)
}

func TestCallTool_PrefersStructuredContent(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RegisterStructured: true,
	})
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  server.URL + ".structured_echo",
		Arguments: map[string]any{"text": "structured"},
	})
	require.NoError(t, err)
	require.Empty(t, callOutput.Content)

	structured, ok := callOutput.StructuredContent.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Echo: structured", structured["message"])
}

func TestAdHocHTTPServer_DottedToolSelectorRoundTrip(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RegisterDottedTool: true,
	})
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	toolsOutput, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
	})
	require.NoError(t, err)
	dottedSelector := ""
	for _, listed := range toolsOutput.Tools {
		if listed.Name == "smartsheet.list_tables" {
			dottedSelector = listed.Selector
			break
		}
	}
	require.Equal(t, server.URL+".smartsheet.list_tables", dottedSelector)

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  dottedSelector,
		Arguments: map[string]any{"file_id": "sheet_123"},
	})
	require.NoError(t, err)
	require.Len(t, callOutput.Content, 1)

	textContent, ok := callOutput.Content[0].(tmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "tables for sheet_123", textContent.Text)
}

func TestAdHocHTTPServer_FragmentToolSelectorStillSupported(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RegisterDottedTool: true,
	})
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  server.URL + "#tool=smartsheet.list_tables",
		Arguments: map[string]any{"file_id": "sheet_456"},
	})
	require.NoError(t, err)
	require.Len(t, callOutput.Content, 1)

	textContent, ok := callOutput.Content[0].(tmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "tables for sheet_456", textContent.Text)
}

func TestAdHocHTTPServer_QuerySelectorUsesFragmentForm(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	selector := server.URL + "?tenant=alpha"
	broker := New(WithAllowAdHocHTTP(true))

	toolsOutput, err := broker.listTools(context.Background(), listToolsInput{
		Selector: selector,
	})
	require.NoError(t, err)
	echoSelector := ""
	for _, listed := range toolsOutput.Tools {
		if listed.Name == "echo" {
			echoSelector = listed.Selector
			break
		}
	}
	require.Equal(t, selector+"#tool=echo", echoSelector)

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  echoSelector,
		Arguments: map[string]any{"text": "query"},
	})
	require.NoError(t, err)
	require.Len(t, callOutput.Content, 1)

	textContent, ok := callOutput.Content[0].(tmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "Echo: query", textContent.Text)
}

func TestAdHocHTTPServer_DottedEndpointUsesFragmentForm(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		ServerPath: "/v1/mcp.v2",
	})
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))

	toolsOutput, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
	})
	require.NoError(t, err)
	echoSelector := ""
	for _, listed := range toolsOutput.Tools {
		if listed.Name == "echo" {
			echoSelector = listed.Selector
			break
		}
	}
	require.Equal(t, server.URL+"#tool=echo", echoSelector)

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  echoSelector,
		Arguments: map[string]any{"text": "path-dot"},
	})
	require.NoError(t, err)
	require.Len(t, callOutput.Content, 1)

	textContent, ok := callOutput.Content[0].(tmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "Echo: path-dot", textContent.Text)
}

func TestNamedServerWithDots_RoundTrip(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RegisterDottedTool: true,
	})
	defer server.Close()

	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"docs.prod": {
			ServerURL: server.URL,
			Transport: "streamable_http",
			Timeout:   5 * time.Second,
		},
	}))

	toolsOutput, err := broker.listTools(context.Background(), listToolsInput{
		Selector: "docs.prod",
	})
	require.NoError(t, err)
	dottedSelector := ""
	for _, listed := range toolsOutput.Tools {
		if listed.Name == "smartsheet.list_tables" {
			dottedSelector = listed.Selector
			break
		}
	}
	require.Equal(t, "docs.prod.smartsheet.list_tables", dottedSelector)

	callOutput, err := broker.callTool(context.Background(), callToolInput{
		Selector:  dottedSelector,
		Arguments: map[string]any{"file_id": "sheet_named"},
	})
	require.NoError(t, err)
	require.Len(t, callOutput.Content, 1)

	textContent, ok := callOutput.Content[0].(tmcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "tables for sheet_named", textContent.Text)
}

func TestNamedHTTPServer_HeaderInjectorAddsAuthorization(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RequiredAuth: "Bearer named-token",
	})
	defer server.Close()

	type ctxKey string
	const tokenKey ctxKey = "token"

	broker := New(
		WithServers(map[string]legacymcp.ConnectionConfig{
			"http_named": {
				ServerURL: server.URL,
				Transport: "streamable_http",
				Timeout:   5 * time.Second,
			},
		}),
		WithHTTPHeaderInjector(func(ctx context.Context, req *HeaderInjectRequest) (map[string]string, error) {
			if req.BaseURL != server.URL || req.Phase != phaseListTools {
				return nil, nil
			}
			token, _ := ctx.Value(tokenKey).(string)
			if token == "" {
				return nil, nil
			}
			return map[string]string{
				"Authorization": "Bearer " + token,
			}, nil
		}),
	)

	output, err := broker.listTools(context.WithValue(context.Background(), tokenKey, "named-token"), listToolsInput{
		Selector: "http_named",
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 2)
	require.Equal(t, "Bearer named-token", server.LastAuthorization())
}

func TestAdHocHTTPServer_HeaderInjectorCanInjectAuthorization(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RequiredAuth: "Bearer adhoc-token",
	})
	defer server.Close()

	broker := New(
		WithAllowAdHocHTTP(true),
		WithHTTPHeaderInjector(func(ctx context.Context, req *HeaderInjectRequest) (map[string]string, error) {
			require.True(t, req.IsAdHoc)
			require.Equal(t, server.URL, req.BaseURL)
			return map[string]string{
				"Authorization": "Bearer adhoc-token",
			}, nil
		}),
	)

	output, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 2)
	require.Equal(t, "Bearer adhoc-token", server.LastAuthorization())
}

func TestNamedHTTPServer_HeaderInjectorCanonicalizesOverrides(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RequiredAuth: "Bearer injected-token",
	})
	defer server.Close()

	broker := New(
		WithServers(map[string]legacymcp.ConnectionConfig{
			"http_named": {
				ServerURL: server.URL,
				Headers:   map[string]string{"authorization": "Bearer configured-token"},
			},
		}),
		WithHTTPHeaderInjector(func(ctx context.Context, req *HeaderInjectRequest) (map[string]string, error) {
			return map[string]string{"Authorization": "Bearer injected-token"}, nil
		}),
	)

	output, err := broker.listTools(context.Background(), listToolsInput{
		Selector: "http_named",
	})
	require.NoError(t, err)
	require.Len(t, output.Tools, 2)
	require.Equal(t, "Bearer injected-token", server.LastAuthorization())
}

func TestErrorInterceptor_CanWrapListToolsError(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RequiredAuth: "Bearer missing-token",
	})
	defer server.Close()

	broker := New(
		WithAllowAdHocHTTP(true),
		WithErrorInterceptor(func(ctx context.Context, req *BrokerErrorRequest) (*BrokerErrorDecision, error) {
			require.Equal(t, phaseListTools, req.Phase)
			require.Equal(t, server.URL, req.BaseURL)
			return &BrokerErrorDecision{
				Handled:   true,
				WrapError: fmt.Errorf("connect this provider in the host application before retrying"),
			}, nil
		}),
	)

	_, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "connect this provider in the host application before retrying")
}

func TestErrorInterceptor_CanWrapCallError(t *testing.T) {
	server := startBrokerHTTPServerWithOptions(t, brokerHTTPServerOptions{
		RequiredAuth: "Bearer missing-token",
	})
	defer server.Close()

	broker := New(
		WithAllowAdHocHTTP(true),
		WithErrorInterceptor(func(ctx context.Context, req *BrokerErrorRequest) (*BrokerErrorDecision, error) {
			require.Equal(t, phaseCallTool, req.Phase)
			require.Equal(t, "echo", req.ToolName)
			return &BrokerErrorDecision{
				Handled:   true,
				WrapError: fmt.Errorf("authorization required for %s", req.BaseURL),
			}, nil
		}),
	)

	_, err := broker.callTool(context.Background(), callToolInput{
		Selector:  server.URL + ".echo",
		Arguments: map[string]any{"text": "hello"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "authorization required")
}

func TestOneShot_ReinitializesPerCall(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"http_named": {
			ServerURL: server.URL,
		},
	}))

	_, err := broker.listTools(context.Background(), listToolsInput{Selector: "http_named"})
	require.NoError(t, err)
	_, err = broker.listTools(context.Background(), listToolsInput{Selector: "http_named"})
	require.NoError(t, err)

	require.Equal(t, 2, server.MethodCount("initialize"))
	require.Equal(t, 2, server.MethodCount("tools/list"))
}

func TestBrokerTools_CoexistWithLegacyMCPToolSet(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	legacyToolSet := legacymcp.NewMCPToolSet(legacymcp.ConnectionConfig{
		Transport: "streamable_http",
		ServerURL: server.URL,
		Timeout:   5 * time.Second,
	})
	require.NoError(t, legacyToolSet.Init(context.Background()))
	defer legacyToolSet.Close()

	broker := New()
	agent := llmagent.New(
		"broker-agent",
		llmagent.WithTools(broker.Tools()),
		llmagent.WithToolSets([]agenttool.ToolSet{legacyToolSet}),
	)

	names := make([]string, 0, len(agent.Tools()))
	for _, tl := range agent.Tools() {
		names = append(names, tl.Declaration().Name)
	}

	require.Contains(t, names, listServersToolName)
	require.Contains(t, names, listToolsToolName)
	require.Contains(t, names, inspectToolsToolName)
	require.Contains(t, names, callToolToolName)
	require.Contains(t, names, "mcp_echo")
}

func TestBrokerTools_PublicSurface(t *testing.T) {
	broker := New()

	tools := broker.Tools()
	require.Len(t, tools, 4)

	names := []string{
		tools[0].Declaration().Name,
		tools[1].Declaration().Name,
		tools[2].Declaration().Name,
		tools[3].Declaration().Name,
	}
	require.ElementsMatch(t, []string{
		listServersToolName,
		listToolsToolName,
		inspectToolsToolName,
		callToolToolName,
	}, names)
}

func TestBrokerTools_HaveExplicitOutputSchemas(t *testing.T) {
	broker := New()

	for _, tl := range broker.Tools() {
		decl := tl.Declaration()
		require.NotNil(t, decl.OutputSchema, decl.Name)
		require.Equal(t, "object", decl.OutputSchema.Type, decl.Name)
	}
}

func TestCallTool_MissingSelector(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	_, err := broker.callTool(context.Background(), callToolInput{
		Arguments: map[string]any{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "selector is required")
}

func TestCallTool_MissingArgumentsObject(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	_, err := broker.callTool(context.Background(), callToolInput{
		Selector: server.URL + ".echo",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "arguments is required")
}

func TestCallTool_MissingRequiredArguments(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	_, err := broker.callTool(context.Background(), callToolInput{
		Selector:  server.URL + ".add",
		Arguments: map[string]any{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `missing required arguments for MCP tool "add": a, b`)
}

func TestCallTool_ExplicitNullRequiredArgumentIsNotMissing(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithAllowAdHocHTTP(true))
	output, err := broker.callTool(context.Background(), callToolInput{
		Selector:  server.URL + ".echo",
		Arguments: map[string]any{"text": nil},
	})
	require.NoError(t, err)
	require.Len(t, output.Content, 1)
}

func TestCallTool_NamedServerIgnoresAdHocNoiseAndValidatesArguments(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"http_named": {
			ServerURL: server.URL,
			Transport: "streamable_http",
			Timeout:   5 * time.Second,
		},
	}))

	_, err := broker.callTool(context.Background(), callToolInput{
		Selector:  "http_named.add",
		Transport: "streamable",
		Headers:   map[string]string{"X-Test": "ignored"},
		Arguments: map[string]any{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `missing required arguments for MCP tool "add": a, b`)
}

func TestAllowCustomDenylistHeaders(t *testing.T) {
	server := startBrokerHTTPServer(t)
	defer server.Close()

	broker := New(
		WithAllowAdHocHTTP(true),
		WithAdHocSensitiveHeaderDenylist([]string{"X-Internal-Token"}),
	)

	_, err := broker.listTools(context.Background(), listToolsInput{
		Selector: server.URL,
		Headers:  map[string]string{"x-internal-token": "secret"},
	})
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "x-internal-token")
}

func TestSelectToolsForInspection_MatchesExactCase(t *testing.T) {
	selected, err := selectToolsForInspection([]tmcp.Tool{
		{Name: "Foo"},
		{Name: "foo"},
	}, []string{"foo"})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	require.Equal(t, "foo", selected[0].Name)

	_, err = selectToolsForInspection([]tmcp.Tool{{Name: "Foo"}}, []string{"foo"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "foo")
}

func TestSelectToolsForInspection_RejectsEmptyAndDeduplicates(t *testing.T) {
	tools := []tmcp.Tool{{Name: "alpha"}, {Name: "beta"}}

	selected, err := selectToolsForInspection(tools, []string{"alpha", " alpha ", "beta"})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, []string{selected[0].Name, selected[1].Name})

	_, err = selectToolsForInspection(tools, []string{" "})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty tool names")
}

func TestResolveTargetValidation(t *testing.T) {
	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"named": {Command: "go"},
	}))

	_, err := broker.resolveTarget(targetInput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one")

	_, err = broker.resolveTarget(targetInput{ServerName: "missing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown MCP server")

	_, err = broker.resolveTarget(targetInput{URL: "https://example.com/mcp"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ad-hoc HTTP MCP is disabled")

	target, err := New(WithAllowAdHocHTTP(true)).resolveTarget(targetInput{URL: "https://example.com/mcp"})
	require.NoError(t, err)
	require.Equal(t, "adhoc", target.Origin)
	require.Equal(t, targetTypeHTTP, target.TargetType)
}

func TestSplitNamedToolSelectorBranches(t *testing.T) {
	broker := New(WithServers(map[string]legacymcp.ConnectionConfig{
		"docs":      {Command: "go"},
		"docs.prod": {Command: "go"},
	}))

	server, toolName, err := broker.splitNamedToolSelector("docs.prod.smartsheet.list_tables")
	require.NoError(t, err)
	require.Equal(t, "docs.prod", server)
	require.Equal(t, "smartsheet.list_tables", toolName)

	server, toolName, err = broker.splitNamedToolSelector("docs.echo")
	require.NoError(t, err)
	require.Equal(t, "docs", server)
	require.Equal(t, "echo", toolName)

	_, _, err = broker.splitNamedToolSelector("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "selector is required")

	_, _, err = broker.splitNamedToolSelector("docs.")
	require.Error(t, err)
	require.Contains(t, err.Error(), "call selector")

	_, _, err = broker.splitNamedToolSelector("missing.echo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown MCP server")
}

func TestSplitHTTPToolSelectorBranches(t *testing.T) {
	baseURL, toolName, err := splitHTTPToolSelector("https://example.com/mcp#tool=smartsheet.list_tables")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/mcp", baseURL)
	require.Equal(t, "smartsheet.list_tables", toolName)

	baseURL, toolName, err = splitHTTPToolSelector("https://example.com/mcp.echo")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/mcp", baseURL)
	require.Equal(t, "echo", toolName)

	for _, selector := range []string{
		"https://example.com/mcp#tool=",
		"#tool=echo",
		"https://example.com/mcp",
		"https://example.com/.echo",
		"://bad",
	} {
		_, _, err = splitHTTPToolSelector(selector)
		require.Error(t, err, selector)
	}
}

func TestSelectorFormattingHelpers(t *testing.T) {
	require.Equal(t, "", joinToolSelector("", "echo"))
	require.Equal(t, "", joinToolSelector("server", ""))
	require.Equal(t, "https://example.com/mcp?tenant=alpha#tool=echo", joinToolSelector("https://example.com/mcp?tenant=alpha", "echo"))
	require.Equal(t, "https://example.com/v1/mcp.v2#tool=echo", joinToolSelector("https://example.com/v1/mcp.v2", "echo"))
	require.Equal(t, "server.echo", joinToolSelector("server", "echo"))

	require.True(t, shouldUseFragmentHTTPToolSelector("%"))
	require.False(t, shouldUseFragmentHTTPToolSelector("https://example.com"))
}

func TestHeaderHelpers(t *testing.T) {
	require.Nil(t, mergeHeaders(nil, nil))
	require.Equal(t, map[string]string{"Authorization": "Bearer token"}, mergeHeaders(nil, map[string]string{"authorization": "Bearer token"}))
	require.Equal(t, map[string]string{"X-Test": "base"}, mergeHeaders(map[string]string{"x-test": "base"}, nil))
	require.Equal(t, map[string]string{"Authorization": "Bearer extra"}, mergeHeaders(
		map[string]string{"authorization": "Bearer base"},
		map[string]string{"Authorization": "Bearer extra", " ": "ignored"},
	))
	require.Equal(t, map[string]string{"X-Test": "ok"}, canonicalizeHeaders(map[string]string{" x-test ": "ok", " ": "ignored"}))
	require.Nil(t, httpHeaderOptions(nil))
	require.NotNil(t, httpHeaderOptions(map[string]string{" x-test ": "ok", " ": "ignored"}))
}

func TestErrorInterceptorBranches(t *testing.T) {
	ctx := context.Background()
	broker := New()
	target := resolvedTarget{TargetType: targetTypeHTTP, Config: legacymcp.ConnectionConfig{Transport: "streamable"}}
	meta := operationMetadata{Selector: "https://example.com/mcp", BaseURL: "https://example.com/mcp", Phase: phaseListTools}

	handled, err := interceptHTTPOperationError(ctx, broker, target, meta, nil)
	require.False(t, handled)
	require.NoError(t, err)

	handled, err = interceptHTTPOperationError(ctx, broker, resolvedTarget{TargetType: targetTypeStdio}, meta, fmt.Errorf("boom"))
	require.False(t, handled)
	require.Error(t, err)

	broker = New(WithErrorInterceptor(func(context.Context, *BrokerErrorRequest) (*BrokerErrorDecision, error) {
		return nil, nil
	}))
	handled, err = interceptHTTPOperationError(ctx, broker, target, meta, fmt.Errorf("boom"))
	require.False(t, handled)
	require.Error(t, err)

	broker = New(WithErrorInterceptor(func(context.Context, *BrokerErrorRequest) (*BrokerErrorDecision, error) {
		return nil, fmt.Errorf("interceptor failed")
	}))
	handled, err = interceptHTTPOperationError(ctx, broker, target, meta, fmt.Errorf("boom"))
	require.True(t, handled)
	require.Contains(t, err.Error(), "interceptor failed")

	broker = New(WithErrorInterceptor(func(context.Context, *BrokerErrorRequest) (*BrokerErrorDecision, error) {
		return &BrokerErrorDecision{Handled: true}, nil
	}))
	handled, err = interceptHTTPOperationError(ctx, broker, target, meta, fmt.Errorf("boom"))
	require.True(t, handled)
	require.Contains(t, err.Error(), "returned no wrapped error")
}

func TestSchemaHelpers(t *testing.T) {
	require.Equal(t, "unknown", schemaTypeName(nil))
	require.Equal(t, "unknown", schemaTypeName(map[string]any{}))
	require.Equal(t, "string", schemaTypeName(map[string]any{"type": "string"}))
	require.Equal(t, "number", schemaTypeName(map[string]any{"type": []any{"number"}}))
	require.Equal(t, "array", schemaTypeName(map[string]any{"type": "array"}))
	require.Equal(t, "array<string>", schemaTypeName(map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}))
	require.Equal(t, "object", schemaTypeName(map[string]any{"type": "object"}))

	_, ok := firstSchemaType([]any{""})
	require.False(t, ok)
	value, ok := firstSchemaType([]any{"boolean"})
	require.True(t, ok)
	require.Equal(t, "boolean", value)

	require.Nil(t, schemaToMap(nil))
	require.Nil(t, schemaToMap(make(chan int)))
	require.Nil(t, schemaToMap([]string{"not", "an", "object"}))
	require.Equal(t, "string", schemaToMap(map[string]any{"type": "string"})["type"])
}

func TestRenderToolSignature(t *testing.T) {
	toolWithSchema := tmcp.NewTool(
		"search",
		tmcp.WithString("query", tmcp.Required()),
		tmcp.WithNumber("limit"),
	)
	require.Equal(t, "search(query: string, limit?: number)", renderToolSignature(*toolWithSchema))

	require.Equal(t, "empty()", renderToolSignature(tmcp.Tool{Name: "empty"}))
}

func TestWithTimeoutContext_UsesShorterDeadline(t *testing.T) {
	parentLong, parentCancel := context.WithTimeout(context.Background(), time.Hour)
	defer parentCancel()

	child, cancel := withTimeoutContext(parentLong, 50*time.Millisecond)
	defer cancel()

	deadline, ok := child.Deadline()
	require.True(t, ok)
	require.Less(t, time.Until(deadline), time.Second)

	parentShort, parentShortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer parentShortCancel()

	child, cancel = withTimeoutContext(parentShort, time.Hour)
	defer cancel()

	childDeadline, ok := child.Deadline()
	require.True(t, ok)
	parentDeadline, ok := parentShort.Deadline()
	require.True(t, ok)
	require.Equal(t, parentDeadline, childDeadline)
}

func TestNormalizeConnectionConfigValidation(t *testing.T) {
	_, _, err := normalizeConnectionConfig(legacymcp.ConnectionConfig{}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transport is required")

	_, _, err = normalizeConnectionConfig(legacymcp.ConnectionConfig{
		Command: "go",
		Headers: map[string]string{
			"X-Test": "bad",
		},
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stdio MCP cannot specify headers")

	_, _, err = normalizeConnectionConfig(legacymcp.ConnectionConfig{
		ServerURL: "ftp://example.com/mcp",
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires http or https")

	_, _, err = normalizeConnectionConfig(legacymcp.ConnectionConfig{
		ServerURL: "https://example.com/mcp",
		Timeout:   -time.Second,
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-negative")

	cfg, kind, err := normalizeConnectionConfig(legacymcp.ConnectionConfig{
		ServerURL: "https://example.com/mcp",
		Transport: "http",
		Headers:   map[string]string{"X-Test": "ok"},
	}, false)
	require.NoError(t, err)
	require.Equal(t, transportStreamable, kind)
	require.Equal(t, "streamable", cfg.Transport)
	require.Equal(t, "ok", cfg.Headers["X-Test"])

	_, _, err = normalizeConnectionConfig(legacymcp.ConnectionConfig{
		Command:   "go",
		Transport: "stdio",
	}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ad-hoc MCP only supports HTTP")
}
