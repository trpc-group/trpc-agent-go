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
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	pluginpkg "trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestIngestSessionCapturesTimestampedMessagesAndCursor(t *testing.T) {
	var got captureRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: len(got.Messages)})
	}))
	defer server.Close()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestQueueSize(1),
		WithIngestJobTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	ts1 := time.Date(2026, 5, 22, 8, 0, 0, 123*1e6, time.UTC)
	ts2 := ts1.Add(time.Second)
	sess := &session.Session{
		ID:      "sess-1",
		AppName: "app",
		UserID:  "user",
		State:   session.StateMap{},
		Events: []event.Event{
			{
				ID:        "u1",
				Timestamp: ts1,
				Response: &model.Response{Choices: []model.Choice{{
					Index:   0,
					Message: model.NewUserMessage("remember this"),
				}}},
			},
			{
				ID:        "a1",
				Timestamp: ts2,
				Response: &model.Response{Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("stored"),
				}}},
			},
		},
	}
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got.SessionKey != "app:user:sess-1" {
		t.Fatalf("session_key = %q", got.SessionKey)
	}
	if got.UserContent != "remember this" || got.AssistantContent != "stored" {
		t.Fatalf("captured pair = %q / %q", got.UserContent, got.AssistantContent)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages length = %d", len(got.Messages))
	}
	if got.Messages[0].Timestamp <= ts2.UnixMilli() || got.Messages[1].Timestamp != got.Messages[0].Timestamp+1 {
		t.Fatalf("timestamps = %d, %d", got.Messages[0].Timestamp, got.Messages[1].Timestamp)
	}
	if readBestEffortLastCaptureAt(sess) != ts2 {
		t.Fatalf("cursor was not advanced to latest event")
	}
}

func TestInjectRecallContext(t *testing.T) {
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base"),
		model.NewUserMessage("hello"),
	}}
	injectRecallContext(req, &recallResponse{
		AppendSystemContext: "system recall",
		PrependContext:      "user recall",
	})

	if len(req.Messages) != 3 {
		t.Fatalf("message length = %d", len(req.Messages))
	}
	if req.Messages[0].Role != model.RoleSystem || req.Messages[0].Content != "base\n\nsystem recall" {
		t.Fatalf("system message = %#v", req.Messages[0])
	}
	if req.Messages[1].Role != model.RoleUser || req.Messages[1].Content != "user recall" {
		t.Fatalf("prepended user message = %#v", req.Messages[1])
	}
	if req.Messages[2].Content != "hello" {
		t.Fatalf("latest user was not preserved: %#v", req.Messages[2])
	}
}

func TestConversationSearchToolUsesCurrentSessionKey(t *testing.T) {
	var got searchConversationsRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathSearchConversations {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(searchConversationsResponse{
			Results: "hit",
			Total:   1,
		})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	var convTool tool.CallableTool
	for _, tl := range svc.Tools() {
		if tl.Declaration().Name == "tdai_conversation_search" {
			var ok bool
			convTool, ok = tl.(tool.CallableTool)
			if !ok {
				t.Fatalf("conversation tool is not callable")
			}
			break
		}
	}
	if convTool == nil {
		t.Fatalf("conversation tool not found")
	}

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "u1"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{Session: sess}).Context
	raw, err := convTool.Call(ctx, []byte(`{"query":"previous topic"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	rsp := raw.(*searchConversationsToolResponse)
	if rsp.Results != "hit" || rsp.Total != 1 {
		t.Fatalf("response = %#v", rsp)
	}
	if got.SessionKey != "app:u1:s1" {
		t.Fatalf("session_key = %q", got.SessionKey)
	}
	if got.Limit != defaultSearchLimit {
		t.Fatalf("limit = %d", got.Limit)
	}
}

func TestGatewayClientEndpointsAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathRecall:
			_ = json.NewEncoder(w).Encode(recallResponse{
				Context:             "legacy",
				PrependContext:      "prepend",
				AppendSystemContext: "append",
				Strategy:            "hybrid",
				MemoryCount:         2,
			})
		case pathSearchMemories:
			_ = json.NewEncoder(w).Encode(searchMemoriesResponse{
				Results:  "memory hit",
				Total:    1,
				Strategy: "semantic",
			})
		case pathEndSession:
			_ = json.NewEncoder(w).Encode(endSessionResponse{Flushed: true})
		case pathHealth:
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok", Version: "test"})
		default:
			http.Error(w, strings.Repeat("x", 700), http.StatusBadGateway)
		}
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{
		GatewayURL:   server.URL,
		Timeout:      time.Second,
		MaxBodyBytes: defaultMaxBodyBytes,
	})
	if err != nil {
		t.Fatalf("newGatewayClient: %v", err)
	}
	recall, err := client.recall(context.Background(), recallRequest{Query: "q", SessionKey: "s"})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if recall.AppendSystemContext != "append" || recall.MemoryCount != 2 {
		t.Fatalf("recall response = %#v", recall)
	}
	memories, err := client.searchMemories(context.Background(), searchMemoriesRequest{Query: "q"})
	if err != nil {
		t.Fatalf("searchMemories: %v", err)
	}
	if memories.Results != "memory hit" || memories.Strategy != "semantic" {
		t.Fatalf("search memories response = %#v", memories)
	}
	ended, err := client.endSession(context.Background(), endSessionRequest{SessionKey: "s"})
	if err != nil {
		t.Fatalf("endSession: %v", err)
	}
	if !ended.Flushed {
		t.Fatalf("end session response = %#v", ended)
	}
	health, err := client.health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != "ok" || health.Version != "test" {
		t.Fatalf("health response = %#v", health)
	}

	if err := client.doJSON(context.Background(), httpMethodGet, "/missing", nil, nil); err == nil {
		t.Fatalf("expected API error")
	} else {
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected APIError, got %T: %v", err, err)
		}
		if !strings.Contains(apiErr.Error(), "status=502") {
			t.Fatalf("unexpected APIError string: %s", apiErr.Error())
		}
		if len(apiErr.Body) > maxErrorBodyPreview+len("...(truncated)") {
			t.Fatalf("error preview was not truncated")
		}
	}
	if err := client.doJSON(context.Background(), httpMethodPost, pathCapture, map[string]any{
		"bad": func() {},
	}, nil); err == nil {
		t.Fatalf("expected marshal error")
	}

	tiny, err := newGatewayClient(Options{GatewayURL: server.URL, MaxBodyBytes: 4})
	if err != nil {
		t.Fatalf("new tiny client: %v", err)
	}
	if _, err := tiny.health(context.Background()); err == nil {
		t.Fatalf("expected response body too large")
	}
	if _, err := newGatewayClient(Options{GatewayURL: "://bad"}); err == nil {
		t.Fatalf("expected invalid gateway url error")
	}
}

func TestRecallPluginInjectsContext(t *testing.T) {
	var got recallRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathRecall {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(recallResponse{
			AppendSystemContext: "remembered system",
			PrependContext:      "remembered user context",
		})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	p := svc.Plugin()
	if p.Name() != "tencentdb_agent_memory" {
		t.Fatalf("plugin name = %q", p.Name())
	}
	mgr, err := pluginpkg.NewManager(p)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	callbacks := mgr.ModelCallbacks()
	if callbacks == nil || len(callbacks.BeforeModel) != 1 {
		t.Fatalf("expected one before model callback")
	}

	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base"),
		model.NewUserMessage("what did I say?"),
	}}
	sess := &session.Session{ID: "s1", AppName: "app", UserID: "user"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{Session: sess}).Context
	if _, err := callbacks.BeforeModel[0](ctx, &model.BeforeModelArgs{Request: req}); err != nil {
		t.Fatalf("before model: %v", err)
	}

	if got.Query != "what did I say?" || got.SessionKey != "app:user:s1" || got.UserID != "user" {
		t.Fatalf("recall request = %#v", got)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("message length = %d", len(req.Messages))
	}
	if req.Messages[0].Content != "base\n\nremembered system" {
		t.Fatalf("system message = %#v", req.Messages[0])
	}
	if req.Messages[1].Content != "remembered user context" {
		t.Fatalf("inserted context = %#v", req.Messages[1])
	}
}

func TestServiceOptionsAndLifecycleEdges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(captureResponse{})
	}))
	defer server.Close()

	customClient := server.Client()
	svc, err := NewService(
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
		WithConversationSearchTool(false),
		WithStandardAliases(true),
		WithToolPrefix("_custom_"),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	if svc.client.hc != customClient {
		t.Fatalf("custom HTTP client was not used")
	}
	if svc.client.timeout != time.Second || svc.client.maxBodyBytes != 1024 {
		t.Fatalf("client options = timeout %v max %d", svc.client.timeout, svc.client.maxBodyBytes)
	}
	if svc.sessionKey(&session.Session{ID: "s1"}) != "custom:s1" {
		t.Fatalf("custom session key was not used")
	}
	names := map[string]bool{}
	for _, tl := range svc.Tools() {
		names[tl.Declaration().Name] = true
	}
	if !names["custom_memory_search"] || !names["memory_search"] {
		t.Fatalf("tool names = %#v", names)
	}
	if names["custom_conversation_search"] {
		t.Fatalf("conversation search should be disabled")
	}
	if plugin, ok := svc.Plugin().(*recallPlugin); !ok || plugin.service != svc {
		t.Fatalf("unexpected plugin = %#v", plugin)
	} else {
		mgr, err := pluginpkg.NewManager(plugin)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		if mgr.ModelCallbacks() != nil {
			t.Fatalf("recall disabled should not register callbacks")
		}
	}

	var nilSvc *Service
	if err := nilSvc.IngestSession(context.Background(), &session.Session{}); err == nil {
		t.Fatalf("expected nil service error")
	}
	if err := svc.IngestSession(context.Background(), nil); err == nil {
		t.Fatalf("expected nil session error")
	}
	if err := svc.IngestSession(context.Background(), &session.Session{ID: "s", AppName: "app"}); err == nil {
		t.Fatalf("expected missing user error")
	}
	if err := svc.IngestSession(context.Background(), &session.Session{ID: "s", AppName: "app", UserID: "u"}); err != nil {
		t.Fatalf("empty transcript should be ignored: %v", err)
	}
	if defaultSessionKey(nil) != "" {
		t.Fatalf("nil default session key should be empty")
	}

	closed, err := NewService(WithGatewayURL(server.URL), WithIngestQueueSize(1))
	if err != nil {
		t.Fatalf("NewService closed: %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := closed.IngestSession(context.Background(), captureReadySession()); err == nil {
		t.Fatalf("expected closed service error")
	}
}

func TestMemorySearchToolAndHelpers(t *testing.T) {
	var got searchMemoriesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathSearchMemories {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(searchMemoriesResponse{
			Results:  "memory result",
			Total:    3,
			Strategy: "hybrid",
		})
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL), WithConversationSearchTool(false))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	memTool, ok := svc.Tools()[0].(tool.CallableTool)
	if !ok {
		t.Fatalf("memory tool is not callable")
	}
	if _, err := memTool.Call(context.Background(), []byte(`{"query":"hello"}`)); err == nil {
		t.Fatalf("expected missing invocation error")
	}
	if _, err := memTool.Call(context.Background(), []byte(`{"query":""}`)); err == nil {
		t.Fatalf("expected query validation error")
	}

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "u1"}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{Session: sess}).Context
	raw, err := memTool.Call(ctx, []byte(`{"query":"profile","limit":99,"type":"L1","scene":"work"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	rsp := raw.(*searchMemoriesToolResponse)
	if rsp.Results != "memory result" || rsp.Total != 3 || rsp.Strategy != "hybrid" {
		t.Fatalf("response = %#v", rsp)
	}
	if got.Query != "profile" || got.Limit != maxSearchLimit || got.Type != "L1" || got.Scene != "work" {
		t.Fatalf("search request = %#v", got)
	}
	if normalizeLimit(-1) != defaultSearchLimit || normalizeLimit(7) != 7 || normalizeLimit(99) != maxSearchLimit {
		t.Fatalf("normalizeLimit returned unexpected values")
	}

	partText := " content part text "
	msg := model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeFile},
			{Type: model.ContentTypeText, Text: &partText},
		},
	}
	if messageText(msg) != "content part text" {
		t.Fatalf("messageText did not read text parts")
	}
	if messageID("", 0) != "" || messageID("evt", 2) != "evt:2" {
		t.Fatalf("unexpected messageID behavior")
	}
	writeBestEffortLastCaptureAt(nil, time.Now())
	if !readBestEffortLastCaptureAt(&session.Session{}).IsZero() {
		t.Fatalf("empty cursor should be zero")
	}
}

func TestEndSessionAndHealth(t *testing.T) {
	var ended endSessionRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathEndSession:
			if err := json.NewDecoder(r.Body).Decode(&ended); err != nil {
				t.Fatalf("decode end session: %v", err)
			}
			_ = json.NewEncoder(w).Encode(endSessionResponse{Flushed: true})
		case pathHealth:
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	sess := &session.Session{ID: "s1", AppName: "app", UserID: "user"}
	if err := svc.EndSession(context.Background(), sess); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if ended.SessionKey != "app:user:s1" || ended.UserID != "user" {
		t.Fatalf("end session request = %#v", ended)
	}
	health, err := svc.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("health = %#v", health)
	}

	var nilSvc *Service
	if err := nilSvc.EndSession(context.Background(), sess); err == nil {
		t.Fatalf("expected nil service EndSession error")
	}
	if err := svc.EndSession(context.Background(), nil); err == nil {
		t.Fatalf("expected nil session EndSession error")
	}
	if _, err := nilSvc.Health(context.Background()); err == nil {
		t.Fatalf("expected nil service Health error")
	}
}

func TestRecallAndPluginEdges(t *testing.T) {
	if latestUserText(nil) != "" {
		t.Fatalf("nil request should not have latest user text")
	}
	if latestUserText(&model.Request{Messages: []model.Message{model.NewSystemMessage("sys")}}) != "" {
		t.Fatalf("request without user should not have latest user text")
	}

	empty := &model.Request{}
	injectRecallContext(empty, &recallResponse{Context: "legacy context"})
	if len(empty.Messages) != 1 || empty.Messages[0].Role != model.RoleSystem {
		t.Fatalf("legacy context should create system message: %#v", empty.Messages)
	}

	noSystem := &model.Request{Messages: []model.Message{model.NewAssistantMessage("hi")}}
	injectRecallContext(noSystem, &recallResponse{AppendSystemContext: "sys", PrependContext: "ctx"})
	if len(noSystem.Messages) != 3 {
		t.Fatalf("expected system, assistant, context messages: %#v", noSystem.Messages)
	}
	if noSystem.Messages[0].Role != model.RoleSystem || noSystem.Messages[0].Content != "sys" {
		t.Fatalf("system was not prepended: %#v", noSystem.Messages)
	}
	if noSystem.Messages[2].Content != "ctx" {
		t.Fatalf("context should append when no user exists: %#v", noSystem.Messages)
	}
	insertBeforeLatestUser(nil, model.NewUserMessage("ignored"))

	svc := &Service{opts: defaultOptions(), client: &gatewayClient{}}
	p := &recallPlugin{service: svc}
	if _, err := p.beforeModel(context.Background(), nil); err != nil {
		t.Fatalf("nil args should be ignored: %v", err)
	}
	if _, err := p.beforeModel(context.Background(), &model.BeforeModelArgs{}); err != nil {
		t.Fatalf("nil request should be ignored: %v", err)
	}
	if _, err := p.beforeModel(context.Background(), &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{model.NewUserMessage("q")}},
	}); err != nil {
		t.Fatalf("missing invocation should be ignored: %v", err)
	}
	ctx := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{ID: "s", AppName: "app", UserID: "user"},
	}).Context
	if _, err := p.beforeModel(ctx, &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{model.NewSystemMessage("sys")}},
	}); err != nil {
		t.Fatalf("missing query should be ignored: %v", err)
	}
	badScope := agent.NewInvocationContext(context.Background(), &agent.Invocation{
		Session: &session.Session{ID: "s", AppName: "app"},
	}).Context
	if _, err := p.beforeModel(badScope, &model.BeforeModelArgs{
		Request: &model.Request{Messages: []model.Message{model.NewUserMessage("q")}},
	}); err != nil {
		t.Fatalf("invalid session scope should be ignored: %v", err)
	}
}

func TestCaptureAndClientFailurePaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc, err := NewService(WithGatewayURL(server.URL))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	if err := svc.capture(context.Background(), ingestJob{
		req: captureRequest{SessionKey: "s"},
	}); err == nil {
		t.Fatalf("expected capture failure")
	}
	if _, err := svc.client.capture(context.Background(), captureRequest{SessionKey: "s"}); err == nil {
		t.Fatalf("expected client capture failure")
	}
	if _, err := svc.client.recall(context.Background(), recallRequest{Query: "q"}); err == nil {
		t.Fatalf("expected client recall failure")
	}
	if _, err := svc.client.searchMemories(context.Background(), searchMemoriesRequest{Query: "q"}); err == nil {
		t.Fatalf("expected client memory search failure")
	}
	if _, err := svc.client.searchConversations(context.Background(), searchConversationsRequest{Query: "q"}); err == nil {
		t.Fatalf("expected client conversation search failure")
	}
	if _, err := svc.client.endSession(context.Background(), endSessionRequest{SessionKey: "s"}); err == nil {
		t.Fatalf("expected client end session failure")
	}
}

func captureReadySession() *session.Session {
	now := time.Now()
	return &session.Session{
		ID:      "s1",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{
				ID:        "u",
				Timestamp: now,
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewUserMessage("remember"),
				}}},
			},
			{
				ID:        "a",
				Timestamp: now.Add(time.Second),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewAssistantMessage("ok"),
				}}},
			},
		},
	}
}
