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
