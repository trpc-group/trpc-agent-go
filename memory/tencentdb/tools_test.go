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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestConversationSearchToolUsesCurrentSessionKey(t *testing.T) {
	var got searchConversationsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, pathSearchConversations, r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_ = json.NewEncoder(w).Encode(searchConversationsResponse{
			Results: "hit",
			Total:   1,
		})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	require.NoError(t, err, "NewService")
	defer svc.Close()

	var convTool tool.CallableTool
	for _, tl := range svc.Tools() {
		if tl.Declaration().Name == "tdai_conversation_search" {
			var ok bool
			convTool, ok = tl.(tool.CallableTool)
			require.True(t, ok, "conversation tool is not callable")
			break
		}
	}
	require.NotNil(t, convTool, "conversation tool not found")
	assert.Contains(t, convTool.Declaration().Description, "session_key")

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "u1"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{Session: sess}).Context
	raw, err := convTool.Call(ctx, []byte(`{"query":"previous topic","session_key":"app:other:secret"}`))
	require.NoError(t, err, "Call")
	rsp := raw.(*searchConversationsToolResponse)
	assert.Equal(t, "hit", rsp.Results)
	assert.Equal(t, 1, rsp.Total)
	responseJSON, err := json.Marshal(rsp)
	require.NoError(t, err, "marshal tool response")
	assert.NotContains(t, string(responseJSON), "session_key")
	assert.Equal(t, "YXBw:dTE:czE", got.SessionKey)
	assert.Equal(t, "u1", got.UserID)
	assert.Equal(t, defaultSearchLimit, got.Limit)
}

func TestMemorySearchToolAndHelpers(t *testing.T) {
	var got searchMemoriesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, pathSearchMemories, r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_ = json.NewEncoder(w).Encode(searchMemoriesResponse{
			Results:  "memory result",
			Total:    3,
			Strategy: "hybrid",
		})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL), WithConversationSearchTool(false), WithMemorySearchTool(true))
	require.NoError(t, err, "NewService")
	defer svc.Close()

	var memTool tool.CallableTool
	for _, tl := range svc.Tools() {
		if tl.Declaration().Name == "tdai_memory_search" {
			var ok bool
			memTool, ok = tl.(tool.CallableTool)
			require.True(t, ok, "memory tool is not callable")
			break
		}
	}
	require.NotNil(t, memTool, "memory tool not found")
	assert.Contains(t, memTool.Declaration().Description, "gateway sidecar")
	_, hasScene := memTool.Declaration().InputSchema.Properties["scene"]
	assert.True(t, hasScene, "legacy memory tool must keep the scene input")
	_, err = memTool.Call(context.Background(), []byte(`{"query":"hello"}`))
	require.Error(t, err, "expected missing invocation error")
	assert.ErrorContains(t, err, "invocation")

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "u1"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{Session: sess}).Context
	_, err = memTool.Call(ctx, []byte(`{"query":""}`))
	require.Error(t, err, "expected query validation error")
	assert.ErrorContains(t, err, "query")

	raw, err := memTool.Call(ctx, []byte(`{"query":"profile","limit":99,"type":"L1","scene":"work"}`))
	require.NoError(t, err, "Call")
	rsp := raw.(*searchMemoriesToolResponse)
	assert.Equal(t, "memory result", rsp.Results)
	assert.Equal(t, 3, rsp.Total)
	assert.Equal(t, "hybrid", rsp.Strategy)
	assert.Equal(t, searchMemoriesRequest{
		Query:  "profile",
		Limit:  maxSearchLimit,
		Type:   "L1",
		Scene:  "work",
		UserID: "u1",
	}, got)
	assert.Equal(t, defaultSearchLimit, normalizeLimit(-1))
	assert.Equal(t, 7, normalizeLimit(7))
	assert.Equal(t, maxSearchLimit, normalizeLimit(99))

	partText := " content part text "
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeFile},
			{Type: model.ContentTypeText, Text: &partText},
		},
	}
	assert.Equal(t, "content part text", messageText(msg))
	assert.Empty(t, messageID("", 0))
	assert.Equal(t, "evt:2", messageID("evt", 2))
	writeBestEffortLastCaptureAt(nil, time.Now())
	assert.True(t, readBestEffortLastCaptureAt(&session.Session{}).IsZero())
}

func TestV3MemorySearchAndScenarioReadTools(t *testing.T) {
	var atomicReq map[string]any
	var scenarioRequests []v3ScenarioReadRequest
	content := "Review transaction boundaries before merging."
	version := "v1"
	createdAt := "2026-08-01T00:00:00Z"
	updatedAt := "2026-08-13T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-key", r.Header.Get(httpHeaderAuthorization))
		assert.Equal(t, "service-1", r.Header.Get(httpHeaderServiceID))
		switch r.URL.Path {
		case pathV3AtomicSearch:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&atomicReq))
			writeV3TestEnvelope(w, v3AtomicSearchData{
				Items: []v3AtomicSearchHit{{
					ID:      "memory-1",
					Type:    "preference",
					Content: "Use concise reviews.",
					Score:   0.9,
				}},
			})
		case pathV3ScenarioRead:
			var req v3ScenarioReadRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			scenarioRequests = append(scenarioRequests, req)
			file := v3ScenarioFile{
				Path:      req.Path,
				Version:   v3Version(version),
				Content:   content,
				CreatedAt: createdAt,
				UpdatedAt: updatedAt,
			}
			writeV3TestEnvelope(w, file)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithAPIKey("test-key"),
		WithServiceIdentity("service-1", "team-1", "agent-1"),
		WithMemorySearchTool(true),
		WithStandardAliases(true),
	)
	require.NoError(t, err)
	defer svc.Close()

	var memoryTool, memoryAlias, conversationTool, scenarioTool tool.CallableTool
	var names []string
	for _, tl := range svc.Tools() {
		names = append(names, tl.Declaration().Name)
		callable, ok := tl.(tool.CallableTool)
		require.True(t, ok)
		switch tl.Declaration().Name {
		case "tdai_memory_search":
			memoryTool = callable
		case "memory_search":
			memoryAlias = callable
		case "tdai_conversation_search":
			conversationTool = callable
		case "tdai_read_scenario":
			scenarioTool = callable
		}
	}
	assert.Equal(t, []string{
		"tdai_memory_search",
		"memory_search",
		"tdai_conversation_search",
		"tdai_read_scenario",
	}, names)
	require.NotNil(t, memoryTool)
	require.NotNil(t, memoryAlias)
	require.NotNil(t, conversationTool)
	require.NotNil(t, scenarioTool)
	assert.Contains(t, memoryTool.Declaration().Description, "service, team, agent")
	assert.Contains(t, conversationTool.Declaration().Description, "session_id")
	for _, tl := range []tool.CallableTool{memoryTool, memoryAlias} {
		_, hasScene := tl.Declaration().InputSchema.Properties["scene"]
		assert.False(t, hasScene, "V3 memory tool must not expose scene")
	}

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "user-1"}
	ctx := agent.NewInvocationContext(
		context.Background(),
		&agent.Invocation{Session: sess},
	).Context
	raw, err := memoryTool.Call(ctx, []byte(`{"query":"review","limit":3,"type":"preference"}`))
	require.NoError(t, err)
	memoryRsp := raw.(*searchMemoriesToolResponse)
	assert.Contains(t, memoryRsp.Results, "concise reviews")
	assert.NotContains(t, atomicReq, "scene")
	assert.Equal(t, "team-1", atomicReq["team_id"])
	assert.Equal(t, "agent-1", atomicReq["agent_id"])
	assert.Equal(t, "user-1", atomicReq["user_id"])

	_, err = scenarioTool.Call(ctx, []byte(`{"path":""}`))
	require.ErrorContains(t, err, "path is required")
	raw, err = scenarioTool.Call(ctx, []byte(`{"path":" reviews.md "}`))
	require.NoError(t, err)
	scenarioRsp := raw.(*readScenarioToolResponse)
	assert.Equal(t, "reviews.md", scenarioRsp.Path)
	assert.Equal(t, version, scenarioRsp.Version)
	assert.Equal(t, content, scenarioRsp.Content)
	assert.Equal(t, createdAt, scenarioRsp.CreatedAt)
	assert.Equal(t, updatedAt, scenarioRsp.UpdatedAt)

	require.Len(t, scenarioRequests, 1)
	for _, req := range scenarioRequests {
		assert.Equal(t, "team-1", req.TeamID)
		assert.Equal(t, "agent-1", req.AgentID)
		assert.Equal(t, "user-1", req.UserID)
		assert.Empty(t, req.SessionID)
	}
}
