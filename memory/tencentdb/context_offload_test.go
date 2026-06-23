//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tencentdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestContextOffloadPlugin_DelegatesHooksToGateway(t *testing.T) {
	var afterReq offloadAfterToolMessagesRequest
	var beforeReq offloadBeforeModelRequest
	var afterHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathOffloadAfterTool:
			afterHeaders = r.Header.Clone()
			require.NoError(t, json.NewDecoder(r.Body).Decode(&afterReq))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tool_result_messages": []model.Message{{
					Role:    model.RoleTool,
					ToolID:  "call-1",
					Content: "summary from gateway",
				}},
			})
		case pathOffloadBeforeModel:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&beforeReq))
			_ = json.NewEncoder(w).Encode(offloadBeforeModelResponse{
				Messages: []model.Message{
					model.NewSystemMessage("gateway mmd"),
					model.NewUserMessage("compressed"),
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithAPIKey("svc-key"),
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)

	sess := &session.Session{ID: "sess-1", AppName: "app", UserID: "user"}
	inv := &agent.Invocation{Session: sess, AgentName: "agent-name"}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	toolCalls := []model.ToolCall{{
		ID:   "call-1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "grep",
			Arguments: []byte(`{"pattern":"x"}`),
		},
	}}
	toolResults := []model.Message{{
		Role:     model.RoleTool,
		ToolID:   "call-1",
		ToolName: "grep",
		Content:  "large result",
	}}
	afterRsp, err := mgr.AfterToolMessages(ctx, &pluginpkg.AfterToolMessagesArgs{
		Invocation:         inv,
		Messages:           []model.Message{model.NewUserMessage("find x")},
		ToolCalls:          toolCalls,
		ToolResultMessages: toolResults,
	})
	require.NoError(t, err)
	require.NotNil(t, afterRsp)
	require.Len(t, afterRsp.ToolResultMessages, 1)
	assert.Equal(t, "summary from gateway", afterRsp.ToolResultMessages[0].Content)
	assert.Equal(t, "Bearer svc-key", afterHeaders.Get(httpHeaderAuthorization))
	assert.Equal(t, "app", afterHeaders.Get(httpHeaderAppName))
	assert.Equal(t, "user", afterHeaders.Get(httpHeaderUserID))
	assert.Equal(t, "sess-1", afterHeaders.Get(httpHeaderSessionID))
	assert.Equal(t, "agent-name", afterHeaders.Get(httpHeaderAgentName))
	assert.Equal(t, defaultSessionKey(sess), afterHeaders.Get(httpHeaderSessionKey))
	assert.Equal(t, "app", afterReq.Scope.AppName)
	assert.Equal(t, "user", afterReq.Scope.UserID)
	assert.Equal(t, "sess-1", afterReq.Scope.SessionID)
	assert.Equal(t, defaultSessionKey(sess), afterReq.Scope.SessionKey)
	require.Len(t, afterReq.ToolResultMessages, 1)
	assert.Equal(t, "large result", afterReq.ToolResultMessages[0].Content)
	require.Len(t, afterReq.ToolCalls, 1)
	assert.Equal(t, "call-1", afterReq.ToolCalls[0].ID)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("next")}}
	callbacks := mgr.ModelCallbacks()
	require.NotNil(t, callbacks)
	_, err = callbacks.RunBeforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "gateway mmd", req.Messages[0].Content)
	assert.Equal(t, "compressed", req.Messages[1].Content)
	require.NotNil(t, beforeReq.Request)
	require.Len(t, beforeReq.Request.Messages, 1)
	assert.Equal(t, "next", beforeReq.Request.Messages[0].Content)
	assert.Equal(t, "agent-name", beforeReq.Scope.AgentName)
}

func TestContextOffloadPlugin_DoesNotCreateLocalOffloadDirectory(t *testing.T) {
	var gotAfter bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pathOffloadAfterTool {
			gotAfter = true
			_ = json.NewEncoder(w).Encode(offloadAfterToolMessagesResponse{})
			return
		}
		t.Fatalf("unexpected path: %s", r.URL.Path)
	}))
	defer server.Close()

	workDir := t.TempDir()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(workDir))
	defer func() { require.NoError(t, os.Chdir(prev)) }()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)
	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	inv := &agent.Invocation{Session: sess, AgentName: "agent"}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context
	_, err = mgr.AfterToolMessages(ctx, &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		ToolResultMessages: []model.Message{{
			Role:    model.RoleTool,
			ToolID:  "call",
			Content: "payload",
		}},
	})
	require.NoError(t, err)
	assert.True(t, gotAfter)
	assert.NoFileExists(t, filepath.Join(workDir, ".tdai-offload"))
	assert.NoDirExists(t, filepath.Join(workDir, ".tdai-offload"))
}

func TestContextOffloadPlugin_UsesOffloadGatewayOverride(t *testing.T) {
	var gotAuth string
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("primary gateway should not receive offload request: %s", r.URL.Path)
	}))
	defer primary.Close()
	offload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, pathOffloadAfterTool, r.URL.Path)
		gotAuth = r.Header.Get(httpHeaderAuthorization)
		_ = json.NewEncoder(w).Encode(offloadAfterToolMessagesResponse{
			ToolResultMessages: []model.Message{model.NewToolMessage("call", "tool", "offloaded")},
		})
	}))
	defer offload.Close()

	svc, err := NewService(
		WithGatewayURL(primary.URL),
		WithAPIKey("primary-key"),
		WithContextOffload(ContextOffloadConfig{
			Enabled:    true,
			GatewayURL: offload.URL,
			APIKey:     "offload-key",
		}),
	)
	require.NoError(t, err)
	defer svc.Close()

	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)
	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	inv := &agent.Invocation{Session: sess}
	rsp, err := mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call", "tool", "payload"),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, rsp)
	require.Len(t, rsp.ToolResultMessages, 1)
	assert.Equal(t, "offloaded", rsp.ToolResultMessages[0].Content)
	assert.Equal(t, "Bearer offload-key", gotAuth)
}

func TestContextOffloadPlugin_GatewayFailuresLeaveContextUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	plugin := NewContextOffloadPlugin(
		WithGatewayURL(server.URL),
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	)
	assert.Equal(t, contextOffloadPluginName, plugin.Name())
	mgr, err := pluginpkg.NewManager(plugin)
	require.NoError(t, err)

	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	inv := &agent.Invocation{Session: sess}
	ctx := agent.NewInvocationContext(context.Background(), inv).Context

	afterRsp, err := mgr.AfterToolMessages(ctx, &pluginpkg.AfterToolMessagesArgs{
		Invocation: inv,
		ToolResultMessages: []model.Message{
			model.NewToolMessage("call", "tool", "payload"),
		},
	})
	require.NoError(t, err)
	assert.Nil(t, afterRsp)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("original")}}
	_, err = mgr.ModelCallbacks().RunBeforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, "original", req.Messages[0].Content)
}

func TestContextOffloadPlugin_BeforeModelResponseShapes(t *testing.T) {
	tests := []struct {
		name        string
		response    offloadBeforeModelResponse
		wantContent string
	}{
		{
			name: "messages",
			response: offloadBeforeModelResponse{
				Messages: []model.Message{model.NewSystemMessage("from messages")},
			},
			wantContent: "from messages",
		},
		{
			name: "request messages override",
			response: offloadBeforeModelResponse{
				Messages: []model.Message{model.NewSystemMessage("ignored")},
				Request: &model.Request{
					Messages: []model.Message{model.NewSystemMessage("from request")},
				},
			},
			wantContent: "from request",
		},
		{
			name: "orphan tool result ignored",
			response: offloadBeforeModelResponse{
				Messages: []model.Message{
					model.NewToolMessage("missing-call", "tool", "orphan"),
				},
			},
			wantContent: "original",
		},
		{
			name:        "empty response ignored",
			response:    offloadBeforeModelResponse{},
			wantContent: "original",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, pathOffloadBeforeModel, r.URL.Path)
				_ = json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			svc, err := NewService(
				WithGatewayURL(server.URL),
				WithContextOffload(ContextOffloadConfig{Enabled: true}),
			)
			require.NoError(t, err)
			defer svc.Close()

			mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
			require.NoError(t, err)
			ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
				Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
			}).Context
			req := &model.Request{Messages: []model.Message{model.NewUserMessage("original")}}

			_, err = mgr.ModelCallbacks().RunBeforeModel(ctx, &model.BeforeModelArgs{Request: req})
			require.NoError(t, err)
			require.Len(t, req.Messages, 1)
			assert.Equal(t, tt.wantContent, req.Messages[0].Content)
		})
	}
}

func TestContextOffloadPlugin_SkipsInvalidInputs(t *testing.T) {
	mgr, err := pluginpkg.NewManager(NewContextOffloadPlugin(
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	))
	require.NoError(t, err)

	afterRsp, err := mgr.AfterToolMessages(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, afterRsp)

	afterRsp, err = mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{
		Invocation: &agent.Invocation{
			Session: &session.Session{ID: "sess", AppName: "app"},
		},
	})
	require.NoError(t, err)
	assert.Nil(t, afterRsp)

	callbacks := mgr.ModelCallbacks()
	_, err = callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{Request: nil})
	require.NoError(t, err)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("original")}}
	_, err = callbacks.RunBeforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Messages, 1)
	assert.Equal(t, "original", req.Messages[0].Content)
}

func TestContextOffloadTools_DelegateToGateway(t *testing.T) {
	var gotRef offloadReadRefRequest
	var gotNode offloadReadNodeRequest
	var gotSearch offloadSearchIndexRequest
	nodeID := "001-N1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathOffloadReadRef:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotRef))
			_ = json.NewEncoder(w).Encode(offloadReadRefResponse{
				ResultRef: "refs/a.md",
				Content:   "raw evidence",
				Truncated: true,
			})
		case pathOffloadReadNode:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotNode))
			_ = json.NewEncoder(w).Encode(offloadReadNodeResponse{
				NodeID: nodeID,
				Entries: []offloadIndexEntry{{
					NodeID:     &nodeID,
					Summary:    "node summary",
					ResultRef:  "refs/a.md",
					ToolCallID: "call-1",
				}},
			})
		case pathOffloadSearchIndex:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotSearch))
			_ = json.NewEncoder(w).Encode(offloadSearchIndexResponse{
				Query: "needle",
				Entries: []offloadIndexEntry{{
					Summary:   "matched",
					ResultRef: "refs/b.md",
				}},
				Total: 1,
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	)
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "sess", AppName: "app", UserID: "user"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session:   sess,
		AgentName: "agent",
	}).Context

	readRef := findCallableTool(t, svc.Tools(), "tdai_read_offload_ref")
	refRsp, err := callToolJSON(t, readRef, ctx, &readOffloadRefToolRequest{ResultRef: "refs/a.md"})
	require.NoError(t, err)
	require.IsType(t, &readOffloadRefToolResponse{}, refRsp)
	assert.Equal(t, "raw evidence", refRsp.(*readOffloadRefToolResponse).Content)
	assert.True(t, refRsp.(*readOffloadRefToolResponse).Truncated)
	assert.Equal(t, "refs/a.md", gotRef.ResultRef)
	assert.Equal(t, "app", gotRef.Scope.AppName)
	assert.Equal(t, "agent", gotRef.Scope.AgentName)

	readNode := findCallableTool(t, svc.Tools(), "tdai_read_offload_node")
	nodeRsp, err := callToolJSON(t, readNode, ctx, &readOffloadNodeToolRequest{NodeID: nodeID})
	require.NoError(t, err)
	require.IsType(t, &readOffloadNodeToolResponse{}, nodeRsp)
	assert.Equal(t, "node summary", nodeRsp.(*readOffloadNodeToolResponse).Entries[0].Summary)
	assert.Equal(t, nodeID, gotNode.NodeID)

	search := findCallableTool(t, svc.Tools(), "tdai_search_offload_index")
	searchRsp, err := callToolJSON(t, search, ctx, &searchOffloadIndexToolRequest{Query: "needle", Limit: 100})
	require.NoError(t, err)
	require.IsType(t, &searchOffloadIndexToolResponse{}, searchRsp)
	assert.Equal(t, 1, searchRsp.(*searchOffloadIndexToolResponse).Total)
	assert.Equal(t, "needle", gotSearch.Query)
	assert.Equal(t, maxSearchLimit, gotSearch.Limit)
}

func TestContextOffloadTools_ErrorPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithContextOffload(ContextOffloadConfig{Enabled: true}),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{ID: "sess", AppName: "app", UserID: "user"},
	}).Context

	readRef := findCallableTool(t, svc.Tools(), "tdai_read_offload_ref")
	_, err = callToolJSON(t, readRef, ctx, &readOffloadRefToolRequest{})
	require.ErrorContains(t, err, "result_ref is required")
	_, err = callToolJSON(t, readRef, context.Background(), &readOffloadRefToolRequest{ResultRef: "refs/a.md"})
	require.ErrorContains(t, err, "invocation session is required")
	_, err = callToolJSON(t, readRef, ctx, &readOffloadRefToolRequest{ResultRef: "refs/a.md"})
	require.Error(t, err)

	readNode := findCallableTool(t, svc.Tools(), "tdai_read_offload_node")
	_, err = callToolJSON(t, readNode, ctx, &readOffloadNodeToolRequest{})
	require.ErrorContains(t, err, "node_id is required")
	_, err = callToolJSON(t, readNode, ctx, &readOffloadNodeToolRequest{NodeID: "node"})
	require.Error(t, err)

	search := findCallableTool(t, svc.Tools(), "tdai_search_offload_index")
	_, err = callToolJSON(t, search, ctx, &searchOffloadIndexToolRequest{})
	require.ErrorContains(t, err, "query is required")
	_, err = callToolJSON(t, search, ctx, &searchOffloadIndexToolRequest{Query: "needle"})
	require.Error(t, err)
}

func TestContextOffloadPlugin_DisabledByDefault(t *testing.T) {
	svc, err := NewService()
	require.NoError(t, err)
	defer svc.Close()

	mgr, err := pluginpkg.NewManager(svc.ContextOffloadPlugin())
	require.NoError(t, err)
	after, err := mgr.AfterToolMessages(context.Background(), &pluginpkg.AfterToolMessagesArgs{})
	require.NoError(t, err)
	assert.Nil(t, after)
	assert.Nil(t, findTool(svc.Tools(), "tdai_read_offload_ref"))
}

func TestContextOffloadHelpers(t *testing.T) {
	assert.NotNil(t, (*Service)(nil).ContextOffloadPlugin())

	standalone := &contextOffloadPlugin{opts: Options{}}
	client, err := standalone.contextOffloadClient()
	require.Error(t, err)
	assert.Nil(t, client)

	client, err = (*contextOffloadPlugin)(nil).contextOffloadClient()
	require.NoError(t, err)
	assert.Nil(t, client)

	assert.Nil(t, cloneModelRequest(nil))
	assistant := model.NewAssistantMessage("")
	assistant.ToolCalls = []model.ToolCall{{ID: "call-1"}}
	assert.False(t, hasOrphanToolResults([]model.Message{
		assistant,
		{Role: model.RoleTool, ToolID: "call-1"},
	}))
	assert.False(t, hasOrphanToolResults([]model.Message{
		{Role: model.RoleTool},
	}))

	var rsp offloadAfterToolMessagesResponse
	require.NoError(t, json.Unmarshal([]byte(`{"toolResultMessages":[{"role":"tool","tool_id":"call","content":"camel"}]}`), &rsp))
	require.Len(t, rsp.ToolResultMessages, 1)
	assert.Equal(t, "camel", rsp.ToolResultMessages[0].Content)
	require.Error(t, json.Unmarshal([]byte(`{`), &rsp))
}

func findTool(tools []tool.Tool, name string) tool.Tool {
	for _, t := range tools {
		if t != nil && t.Declaration() != nil && t.Declaration().Name == name {
			return t
		}
	}
	return nil
}

func findCallableTool(t *testing.T, tools []tool.Tool, name string) tool.CallableTool {
	t.Helper()
	found := findTool(tools, name)
	require.NotNil(t, found, "tool %s", name)
	callable, ok := found.(tool.CallableTool)
	require.True(t, ok, "tool %s should be callable", name)
	return callable
}

func callToolJSON(t *testing.T, callable tool.CallableTool, ctx context.Context, req any) (any, error) {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	return callable.Call(ctx, b)
}
