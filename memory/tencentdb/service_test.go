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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestServiceOptionsAndLifecycleEdges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(captureResponse{})
	}))
	defer server.Close()

	customClient := server.Client()
	svc, err := NewService(
		nil,
		WithGatewayURL(server.URL),
		WithTimeout(time.Second),
		WithHTTPClient(customClient),
		WithMaxBodyBytes(1024),
		WithIngestWorkers(2),
		WithIngestQueueSize(2),
		WithIngestJobTimeout(time.Second),
		WithSessionKeyFunc(func(sess *session.Session) string {
			return "custom:" + sess.ID
		}),
		WithRecallEnabled(false),
		WithMemorySearchTool(true),
		WithConversationSearchTool(false),
		WithStandardAliases(true),
		WithToolPrefix("_custom_"),
	)
	require.NoError(t, err, "NewService")
	defer svc.Close()

	assert.Same(t, customClient, svc.client.hc)
	assert.Equal(t, time.Second, svc.client.timeout)
	assert.Equal(t, int64(1024), svc.client.maxBodyBytes)
	assert.Equal(t, "custom:s1", svc.sessionKey(&session.Session{ID: "s1"}))
	names := map[string]bool{}
	for _, tl := range svc.Tools() {
		names[tl.Declaration().Name] = true
	}
	assert.True(t, names["custom_memory_search"], "tool names = %#v", names)
	assert.True(t, names["memory_search"], "tool names = %#v", names)
	assert.False(t, names["custom_conversation_search"], "conversation search should be disabled")
	deduped, err := NewService(
		WithGatewayURL(server.URL),
		WithMemorySearchTool(true),
		WithStandardAliases(true),
		WithConversationSearchTool(false),
		func(o *Options) { o.ToolPrefix = "" },
	)
	require.NoError(t, err, "NewService deduped")
	defer deduped.Close()
	assert.Len(t, deduped.Tools(), 1)
	plugin, ok := svc.Plugin().(*recallPlugin)
	require.True(t, ok, "unexpected plugin = %#v", plugin)
	assert.Same(t, svc, plugin.service)
	mgr, err := pluginpkg.NewManager(plugin)
	require.NoError(t, err)
	assert.Nil(t, mgr.ModelCallbacks(), "recall disabled should not register callbacks")

	var nilSvc *Service
	assert.Nil(t, nilSvc.Tools())
	assert.Nil(t, (&Service{}).Tools())
	require.NoError(t, nilSvc.Close(), "nil service close should be nil")
	require.Error(t, nilSvc.IngestSession(context.Background(), &session.Session{}), "expected nil service error")
	require.Error(t, svc.IngestSession(context.Background(), nil), "expected nil session error")
	require.Error(t, svc.IngestSession(context.Background(), &session.Session{ID: "s", AppName: "app"}), "expected missing user error")
	require.NoError(t, svc.IngestSession(context.Background(), &session.Session{ID: "s", AppName: "app", UserID: "u"}), "empty transcript should be ignored")
	assert.Empty(t, defaultSessionKey(nil))

	closed, err := NewService(WithGatewayURL(server.URL), WithIngestQueueSize(1))
	require.NoError(t, err, "NewService closed")
	require.NoError(t, closed.Close(), "Close")
	require.Error(t, closed.IngestSession(context.Background(), captureReadySession()), "expected closed service error")
}

func TestDefaultSessionKeyAvoidsDelimiterCollisions(t *testing.T) {
	left := &session.Session{AppName: "app", UserID: "user:session", ID: "id"}
	right := &session.Session{AppName: "app:user", UserID: "session", ID: "id"}

	assert.NotEqual(t, defaultSessionKey(left), defaultSessionKey(right))
	assert.Equal(t, "YXBw:dXNlcjpzZXNzaW9u:aWQ", defaultSessionKey(left))
	assert.Equal(t, "YXBwOnVzZXI:c2Vzc2lvbg:aWQ", defaultSessionKey(right))
}

func TestSafeDefaultsDisableCrossTenantReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(captureResponse{})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	require.NoError(t, err, "NewService")
	defer svc.Close()

	// Recall is opt-in: the plugin must not register a BeforeModel callback by
	// default, since the shared gateway does not enforce user/session scoping.
	mgr, err := pluginpkg.NewManager(svc.Plugin())
	require.NoError(t, err, "NewManager")
	assert.Nil(t, mgr.ModelCallbacks(), "recall should be disabled by default")

	// memory_search is opt-in: only the session-scoped conversation search is
	// exposed by default.
	names := map[string]bool{}
	for _, tl := range svc.Tools() {
		names[tl.Declaration().Name] = true
	}
	assert.False(t, names["tdai_memory_search"], "memory search should be opt-in; tools=%#v", names)
	assert.True(t, names["tdai_conversation_search"], "conversation search should be on by default; tools=%#v", names)

	aliasOnly, err := NewService(
		WithGatewayURL(server.URL),
		WithStandardAliases(true),
	)
	require.NoError(t, err, "NewService alias only")
	defer aliasOnly.Close()

	aliasOnlyMgr, err := pluginpkg.NewManager(aliasOnly.Plugin())
	require.NoError(t, err, "NewManager alias only")
	assert.Nil(t, aliasOnlyMgr.ModelCallbacks(), "recall should remain disabled when only aliases are enabled")

	aliasOnlyNames := map[string]bool{}
	for _, tl := range aliasOnly.Tools() {
		aliasOnlyNames[tl.Declaration().Name] = true
	}
	assert.False(t, aliasOnlyNames["tdai_memory_search"], "native memory search should stay opt-in; tools=%#v", aliasOnlyNames)
	assert.False(t, aliasOnlyNames["memory_search"], "standard alias should require memory search to be enabled; tools=%#v", aliasOnlyNames)

	enabled, err := NewService(
		WithGatewayURL(server.URL),
		WithRecallEnabled(true),
		WithMemorySearchTool(true),
	)
	require.NoError(t, err, "NewService enabled")
	defer enabled.Close()

	enabledMgr, err := pluginpkg.NewManager(enabled.Plugin())
	require.NoError(t, err, "NewManager enabled")
	require.NotNil(t, enabledMgr.ModelCallbacks(), "recall should register when enabled")
	assert.Len(t, enabledMgr.ModelCallbacks().BeforeModel, 1)

	enabledNames := map[string]bool{}
	for _, tl := range enabled.Tools() {
		enabledNames[tl.Declaration().Name] = true
	}
	assert.True(t, enabledNames["tdai_memory_search"], "memory search should be exposed when enabled; tools=%#v", enabledNames)
}

func TestEndSessionAndHealth(t *testing.T) {
	var ended endSessionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathEndSession:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&ended))
			_ = json.NewEncoder(w).Encode(endSessionResponse{Flushed: true})
		case pathHealth:
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
		default:
			require.Failf(t, "unexpected path", "path=%s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	require.NoError(t, err, "NewService")
	defer svc.Close()

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "user"}
	writeBestEffortSyntheticTimestamp(sess, 123)
	require.NoError(t, svc.EndSession(context.Background(), sess), "EndSession")
	assert.Equal(t, endSessionRequest{SessionKey: defaultSessionKey(sess), UserID: "user"}, ended)
	assert.Zero(t, readBestEffortSyntheticTimestamp(sess))
	health, err := svc.Health(context.Background())
	require.NoError(t, err, "Health")
	assert.Equal(t, "ok", health.Status)

	var nilSvc *Service
	require.Error(t, nilSvc.EndSession(context.Background(), sess), "expected nil service EndSession error")
	require.Error(t, svc.EndSession(context.Background(), nil), "expected nil session EndSession error")
	_, err = nilSvc.Health(context.Background())
	require.Error(t, err, "expected nil service Health error")
}

func TestCaptureAndClientFailurePaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	require.NoError(t, err, "NewService")
	defer svc.Close()

	err = svc.capture(context.Background(), ingestJob{
		req: captureRequest{SessionKey: "s"},
	})
	require.Error(t, err, "expected capture failure")
	_, err = svc.client.capture(context.Background(), captureRequest{SessionKey: "s"})
	require.Error(t, err, "expected client capture failure")
	_, err = svc.client.recall(context.Background(), recallRequest{Query: "q"})
	require.Error(t, err, "expected client recall failure")
	_, err = svc.client.searchMemories(context.Background(), searchMemoriesRequest{Query: "q"})
	require.Error(t, err, "expected client memory search failure")
	_, err = svc.client.searchConversations(context.Background(), searchConversationsRequest{Query: "q"})
	require.Error(t, err, "expected client conversation search failure")
	_, err = svc.client.endSession(context.Background(), endSessionRequest{SessionKey: "s"})
	require.Error(t, err, "expected client end session failure")

	previousErr := errors.New("previous failed")
	previousFailed := &captureSerialState{done: make(chan struct{}), err: previousErr}
	close(previousFailed.done)
	err = svc.capture(context.Background(), ingestJob{
		req: captureRequest{SessionKey: "s"},
		serial: &captureSerialState{
			sessionKey: "s",
			previous:   previousFailed,
			done:       make(chan struct{}),
		},
	})
	require.ErrorIs(t, err, previousErr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = svc.capture(ctx, ingestJob{
		req: captureRequest{SessionKey: "s"},
		serial: &captureSerialState{
			sessionKey: "s",
			previous:   &captureSerialState{done: make(chan struct{})},
			done:       make(chan struct{}),
		},
	})
	require.ErrorIs(t, err, context.Canceled)
}
