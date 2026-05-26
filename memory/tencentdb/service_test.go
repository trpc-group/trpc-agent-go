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
	"sync/atomic"
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

func TestIngestSessionMarksInFlightBeforeAsyncCaptureCompletes(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		requests.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: 2})
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
	defer func() {
		close(release)
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	sess := captureReadySession()
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("capture request did not start")
	}
	if got := readBestEffortLastCaptureAt(sess); !got.IsZero() {
		t.Fatalf("persistent cursor advanced before capture success: %v", got)
	}
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("second IngestSession: %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("capture requests = %d, want 1", got)
	}
}

func TestIngestSessionRetriesAfterAsyncCaptureFailure(t *testing.T) {
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		switch requests.Add(1) {
		case 1:
			http.Error(w, "gateway unavailable", http.StatusBadGateway)
			close(firstDone)
		case 2:
			_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: 2})
			close(secondDone)
		default:
			t.Fatalf("unexpected extra capture request")
		}
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
	defer func() {
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	sess := captureReadySession()
	want := sess.Events[len(sess.Events)-1].Timestamp
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("IngestSession: %v", err)
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatalf("first capture request did not complete")
	}
	waitForCondition(t, time.Second, func() bool {
		svc.cursorMu.Lock()
		defer svc.cursorMu.Unlock()
		_, ok := svc.inFlight[svc.sessionKey(sess)]
		return !ok
	})
	if got := readBestEffortLastCaptureAt(sess); !got.IsZero() {
		t.Fatalf("persistent cursor advanced after failed capture: %v", got)
	}
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("retry IngestSession: %v", err)
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatalf("retry capture request did not complete")
	}
	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := readBestEffortLastCaptureAt(sess); !got.Equal(want) {
		t.Fatalf("cursor = %v, want %v", got, want)
	}
}

func TestIngestSessionSerializesSameSessionCapturesAndTimestamps(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstReqC := make(chan captureRequest, 1)
	secondReqC := make(chan captureRequest, 1)
	var released atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pathCapture {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req captureRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch requests.Add(1) {
		case 1:
			firstReqC <- req
			<-releaseFirst
		case 2:
			secondReqC <- req
		default:
			t.Fatalf("unexpected extra capture request")
		}
		_ = json.NewEncoder(w).Encode(captureResponse{L0Recorded: len(req.Messages)})
	}))
	defer server.Close()
	defer func() {
		if released.CompareAndSwap(false, true) {
			close(releaseFirst)
		}
	}()

	svc, err := NewService(
		WithGatewayURL(server.URL),
		WithIngestWorkers(2),
		WithIngestQueueSize(2),
		WithIngestJobTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer func() {
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	sess := captureReadySession()
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("first IngestSession: %v", err)
	}
	firstReq := waitCaptureRequest(t, firstReqC, "first capture")

	events := sess.GetEvents()
	nextAt := events[len(events)-1].Timestamp.Add(time.Second)
	appendSessionPair(sess, nextAt, "u2", "second fact", "a2", "stored second")
	if err := svc.IngestSession(context.Background(), sess); err != nil {
		t.Fatalf("second IngestSession: %v", err)
	}
	select {
	case req := <-secondReqC:
		t.Fatalf("second capture started before first completed: %#v", req)
	case <-time.After(50 * time.Millisecond):
	}
	if released.CompareAndSwap(false, true) {
		close(releaseFirst)
	}
	secondReq := waitCaptureRequest(t, secondReqC, "second capture")
	if len(firstReq.Messages) == 0 || len(secondReq.Messages) == 0 {
		t.Fatalf("empty capture messages: first=%d second=%d", len(firstReq.Messages), len(secondReq.Messages))
	}
	firstLast := firstReq.Messages[len(firstReq.Messages)-1].Timestamp
	if secondReq.Messages[0].Timestamp <= firstLast {
		t.Fatalf("second timestamp = %d, want > %d", secondReq.Messages[0].Timestamp, firstLast)
	}
	if secondReq.UserContent != "second fact" || secondReq.AssistantContent != "stored second" {
		t.Fatalf("second captured pair = %q / %q", secondReq.UserContent, secondReq.AssistantContent)
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
	if _, err := newGatewayClient(Options{GatewayURL: "/path-only"}); err == nil {
		t.Fatalf("expected path-only gateway url error")
	}
	if _, err := newGatewayClient(Options{GatewayURL: "ftp://example.com"}); err == nil {
		t.Fatalf("expected unsupported scheme gateway url error")
	}
	if _, err := newGatewayClient(Options{}); err == nil {
		t.Fatalf("expected empty gateway url error")
	}
	nullable, err := newGatewayClient(Options{GatewayURL: server.URL})
	if err != nil {
		t.Fatalf("new nullable client: %v", err)
	}
	if err := nullable.doJSON(context.Background(), httpMethodGet, pathHealth, nil, nil); err != nil {
		t.Fatalf("nil output should be accepted: %v", err)
	}
}

func TestGatewayClientDecodeAndRequestEdges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/bad-json":
			_, _ = w.Write([]byte("{"))
		default:
			_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
		}
	}))
	defer server.Close()

	client, err := newGatewayClient(Options{GatewayURL: server.URL})
	if err != nil {
		t.Fatalf("newGatewayClient: %v", err)
	}
	var out HealthResponse
	if err := client.doJSON(nil, httpMethodGet, "/empty", nil, &out); err != nil {
		t.Fatalf("empty response should be accepted: %v", err)
	}
	if err := client.doJSON(context.Background(), httpMethodGet, "/bad-json", nil, &out); err == nil {
		t.Fatalf("expected unmarshal error")
	}
	if err := client.doJSONOnce(context.Background(), httpMethodGet, "://bad", nil, nil); err == nil {
		t.Fatalf("expected request build error")
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
	deduped, err := NewService(
		WithGatewayURL(server.URL),
		WithStandardAliases(true),
		WithConversationSearchTool(false),
		func(o *Options) { o.ToolPrefix = "" },
	)
	if err != nil {
		t.Fatalf("NewService deduped: %v", err)
	}
	defer deduped.Close()
	if got := len(deduped.Tools()); got != 1 {
		t.Fatalf("deduped tool count = %d, want 1", got)
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
	if nilSvc.Tools() != nil {
		t.Fatalf("nil service should not expose tools")
	}
	if (&Service{}).Tools() != nil {
		t.Fatalf("empty service should not expose tools")
	}
	if err := nilSvc.Close(); err != nil {
		t.Fatalf("nil service close should be nil: %v", err)
	}
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

func TestSessionScanTimestampAndStateEdges(t *testing.T) {
	if got := scanTranscript(nil, time.Time{}); len(got.Messages) != 0 {
		t.Fatalf("nil session scan = %#v", got)
	}
	if got := scanTranscript(&session.Session{}, time.Time{}); len(got.Messages) != 0 {
		t.Fatalf("empty session scan = %#v", got)
	}

	base := time.Date(2026, 5, 22, 8, 0, 0, 0, time.UTC)
	sess := &session.Session{
		ID:      "s1",
		AppName: "app",
		UserID:  "user",
		Events: []event.Event{
			{ID: "old", Timestamp: base, Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("old"),
			}}}},
			{ID: "nil-response", Timestamp: base.Add(time.Second)},
			{ID: "system", Timestamp: base.Add(2 * time.Second), Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewSystemMessage("system"),
			}}}},
			{ID: "empty", Timestamp: base.Add(3 * time.Second), Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage("   "),
			}}}},
			{ID: "user", Timestamp: base.Add(4 * time.Second), Response: &model.Response{Choices: []model.Choice{{
				Index:   2,
				Message: model.NewUserMessage("new"),
			}}}},
		},
	}
	scan := scanTranscript(sess, base)
	if len(scan.Messages) != 1 || scan.Messages[0].ID != "user:2" || scan.Latest != base.Add(4*time.Second) {
		t.Fatalf("scan = %#v", scan)
	}

	if got := normalizeGatewayMessageTimestamps(nil, time.Now()); got != nil {
		t.Fatalf("nil normalized messages = %#v", got)
	}
	normalized := normalizeGatewayMessageTimestamps([]tdaiMessage{{Content: "x"}}, time.Time{})
	if len(normalized) != 1 || normalized[0].Timestamp == 0 {
		t.Fatalf("normalized messages = %#v", normalized)
	}
	empty, latest := normalizeGatewayMessageTimestampsAfter(nil, time.Now(), 123)
	if empty != nil || latest != 123 {
		t.Fatalf("empty normalize result = %#v latest=%d", empty, latest)
	}
	bumped, latest := normalizeGatewayMessageTimestampsAfter(
		[]tdaiMessage{{Content: "a"}, {Content: "b"}},
		time.UnixMilli(1000),
		5000,
	)
	if bumped[0].Timestamp != 5001 || bumped[1].Timestamp != 5002 || latest != 5002 {
		t.Fatalf("bumped timestamps = %#v latest=%d", bumped, latest)
	}

	stateSess := &session.Session{}
	stateSess.SetState(lastCaptureAtStateKey, []byte("not-a-time"))
	if got := readBestEffortLastCaptureAt(stateSess); !got.IsZero() {
		t.Fatalf("malformed last capture should be zero: %v", got)
	}
	writeBestEffortSyntheticTimestamp(nil, 10)
	writeBestEffortSyntheticTimestamp(stateSess, 0)
	if got := readBestEffortSyntheticTimestamp(stateSess); got != 0 {
		t.Fatalf("non-positive synthetic timestamp should be ignored: %d", got)
	}
	stateSess.SetState(syntheticTimestampStateKey, []byte("bad"))
	if got := readBestEffortSyntheticTimestamp(stateSess); got != 0 {
		t.Fatalf("malformed synthetic timestamp should be zero: %d", got)
	}
	writeBestEffortSyntheticTimestamp(stateSess, 77)
	if got := readBestEffortSyntheticTimestamp(stateSess); got != 77 {
		t.Fatalf("synthetic timestamp = %d", got)
	}
	clearBestEffortSyntheticTimestamp(nil)
	clearBestEffortSyntheticTimestamp(stateSess)
	if got := readBestEffortSyntheticTimestamp(stateSess); got != 0 {
		t.Fatalf("cleared synthetic timestamp = %d", got)
	}

	if messageText(model.Message{Role: model.RoleUser}) != "" {
		t.Fatalf("empty message should have no text")
	}
	if messageText(model.Message{
		Role: model.RoleUser,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
		}},
	}) != "" {
		t.Fatalf("nil text part should have no text")
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
	writeBestEffortSyntheticTimestamp(sess, 123)
	if err := svc.EndSession(context.Background(), sess); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if ended.SessionKey != "app:user:s1" || ended.UserID != "user" {
		t.Fatalf("end session request = %#v", ended)
	}
	if got := readBestEffortSyntheticTimestamp(sess); got != 0 {
		t.Fatalf("synthetic timestamp was not cleared: %d", got)
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

	previousFailed := &captureSerialState{done: make(chan struct{}), err: errors.New("previous failed")}
	close(previousFailed.done)
	if err := svc.capture(context.Background(), ingestJob{
		req:      captureRequest{SessionKey: "s"},
		previous: previousFailed,
		serial:   &captureSerialState{done: make(chan struct{})},
	}); err == nil {
		t.Fatalf("expected previous capture failure")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.capture(ctx, ingestJob{
		req:      captureRequest{SessionKey: "s"},
		previous: &captureSerialState{done: make(chan struct{})},
		serial:   &captureSerialState{done: make(chan struct{})},
	}); err == nil {
		t.Fatalf("expected context cancellation while waiting for previous capture")
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

func appendSessionPair(
	sess *session.Session,
	at time.Time,
	userID string,
	userContent string,
	assistantID string,
	assistantContent string,
) {
	sess.EventMu.Lock()
	defer sess.EventMu.Unlock()
	sess.Events = append(sess.Events,
		event.Event{
			ID:        userID,
			Timestamp: at,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewUserMessage(userContent),
			}}},
		},
		event.Event{
			ID:        assistantID,
			Timestamp: at.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewAssistantMessage(assistantContent),
			}}},
		},
	)
}

func waitCaptureRequest(t *testing.T, ch <-chan captureRequest, name string) captureRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(time.Second):
		t.Fatalf("%s did not start", name)
		return captureRequest{}
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition was not met within %s", timeout)
	}
}
