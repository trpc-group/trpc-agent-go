//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func toolTestContext(req *model.Request) context.Context {
	var message model.Message
	if len(req.Messages) > 0 {
		message = req.Messages[len(req.Messages)-1]
	}
	return agent.NewInvocationContext(context.Background(), agent.NewInvocation(agent.WithInvocationMessage(message)))
}

func TestCallback_InvocationCachePreservesNewTools(t *testing.T) {
	t.Parallel()
	server, rec := decisionServer(t, binaryResponder(map[string]float64{"tool:keep": 0.9}, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0))
	require.NoError(t, err)
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("workflow helper")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tools := newToolMap(&stubTool{name: "keep", description: "workflow helper"}, &stubTool{name: "drop", description: "workflow helper"})
	req := newUserRequest("unrelated request content", tools)
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, []string{"keep"}, sortedToolNames(req.Tools))
	tools["added"] = &stubTool{name: "added", description: "workflow helper"}
	req = newUserRequest("unrelated request content", tools)
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, []string{"added", "keep"}, sortedToolNames(req.Tools))
	require.Equal(t, 1, rec.requests())
	other := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("workflow helper")))
	req = newUserRequest("unrelated request content", tools)
	_, err = cb.Callback()(agent.NewInvocationContext(context.Background(), other), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, []string{"keep"}, sortedToolNames(req.Tools))
	require.Equal(t, 2, rec.requests())
}

func TestCallback_ConcurrentDecisionAndCanceledWaiter(t *testing.T) {
	started, release := make(chan struct{}, 8), make(chan struct{}, 1)
	defer close(release)
	server, rec := decisionServer(t, func(q map[string]string) string {
		started <- struct{}{}
		<-release
		return binaryResponder(map[string]float64{"tool:keep": 0.9}, 0.05)(q)
	}, http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0))
	require.NoError(t, err)
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("workflow helper")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	newRequest := func() *model.Request {
		return newUserRequest("workflow helper", newToolMap(&stubTool{name: "keep"}, &stubTool{name: "drop"}))
	}
	first := newRequest()
	done := make(chan struct{})
	go func() { c := cb.Callback(); _, _ = c(ctx, &model.BeforeModelArgs{Request: first}); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("decision did not start")
	}
	otherReq := newRequest()
	otherReq.Tools["added"] = &stubTool{name: "added"}
	otherDone := make(chan struct{})
	go func() { c := cb.Callback(); _, _ = c(ctx, &model.BeforeModelArgs{Request: otherReq}); close(otherDone) }()
	waitCtx, cancel := context.WithCancel(ctx)
	waitReq := newRequest()
	waitDone := make(chan struct{})
	go func() {
		c := cb.Callback()
		_, _ = c(waitCtx, &model.BeforeModelArgs{Request: waitReq})
		close(waitDone)
	}()
	select {
	case <-started:
		t.Fatal("waiter started a duplicate decision")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter did not return")
	}
	require.Equal(t, []string{"drop", "keep"}, sortedToolNames(waitReq.Tools))
	release <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("decision did not finish")
	}
	select {
	case <-otherDone:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent waiter did not finish")
	}
	require.Equal(t, []string{"keep"}, sortedToolNames(first.Tools))
	require.Equal(t, []string{"added", "keep"}, sortedToolNames(otherReq.Tools))
	require.Equal(t, 1, rec.requests())
}

func TestCallback_MissingInvocationLeavesToolsUntouched(t *testing.T) {
	t.Parallel()
	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0))
	require.NoError(t, err)
	tools := newToolMap(&stubTool{name: "helper", description: "workflow helper"})
	req := newUserRequest("workflow helper", tools)
	_, err = cb.Callback()(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools)
	require.Equal(t, 0, rec.requests())
}

func TestNewToolCallback_ValidatesOptions(t *testing.T) {
	t.Parallel()

	_, err := NewToolCallback("")
	require.Error(t, err)

	_, err = NewToolCallback("https://decision.example", WithThreshold(1.5))
	require.ErrorContains(t, err, "threshold")

	_, err = NewToolCallback("https://decision.example", WithSlack(-0.1))
	require.ErrorContains(t, err, "slack")

	_, err = NewToolCallback("https://decision.example", WithThreshold(math.NaN()))
	require.ErrorContains(t, err, "threshold")

	_, err = NewToolCallback("https://decision.example", WithSlack(math.NaN()))
	require.ErrorContains(t, err, "slack")
}

// catalogTools is a catalog large enough to pass the default min tool count,
// with the framework keep set present.
func catalogTools() map[string]*stubTool {
	return map[string]*stubTool{
		"web_search":     {name: "web_search", description: "search the web for flights and hotels"},
		"hotel_booking":  {name: "hotel_booking", description: "book hotel rooms"},
		"calculator":     {name: "calculator", description: "perform arithmetic calculations"},
		"calendar":       {name: "calendar", description: "manage calendar events"},
		"email_sender":   {name: "email_sender", description: "send emails to recipients"},
		"file_reader":    {name: "file_reader", description: "read files from disk"},
		"database_query": {name: "database_query", description: "run SQL queries"},
		"image_render":   {name: "image_render", description: "render images from prompts"},
		"skill_load":     {name: "skill_load", description: "load a skill"},
		"tool_search":    {name: "tool_search", description: "search for tools"},
	}
}

func TestCallback_NarrowsAndKeepsDefaultKeepSet(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(map[string]float64{
		"tool:web_search":    0.9,
		"tool:hotel_booking": 0.55, // below threshold, within slack
	}, 0.05), http.StatusOK)

	var reports []*Report
	cb, err := NewToolCallback(server.URL,
		WithHTTPClient(server.Client()),
		WithObserver(func(r *Report) { reports = append(reports, r) }),
	)
	require.NoError(t, err)

	req := newUserRequest("find cheap flights and hotels online",
		newToolMap(catalogTools()["web_search"],
			catalogTools()["hotel_booking"],
			catalogTools()["calculator"],
			catalogTools()["calendar"],
			catalogTools()["email_sender"],
			catalogTools()["file_reader"],
			catalogTools()["database_query"],
			catalogTools()["image_render"],
			catalogTools()["skill_load"],
			catalogTools()["tool_search"],
		))
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	require.Equal(t, 1, rec.requests())
	// A candidate passed the threshold, so the near-threshold tool is dropped.
	require.Equal(t, []string{"skill_load", "web_search"},
		sortedToolNames(req.Tools))

	require.Len(t, reports, 1)
	report := reports[0]
	require.Equal(t, OutcomeNarrowed, report.Outcome)
	require.Equal(t, []string{
		"calculator", "calendar", "database_query",
		"email_sender", "file_reader", "hotel_booking", "image_render", "tool_search",
	}, report.Dropped)
	require.InDelta(t, 0.9, report.Scores["web_search"], 1e-9)
	require.NotContains(t, report.Scores, "skill_load") // never scored
	require.Greater(t, report.SavedTokens, 0)
}

func TestCallback_FrameworkWorkflowToolsKept(t *testing.T) {
	t.Parallel()
	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0))
	require.NoError(t, err)
	workflow := []string{
		"skill_load", "skill_list_docs", "skill_select_docs", "skill_run", "skill_exec",
		"skill_write_stdin", "skill_poll_session", "skill_kill_session",
		"workspace_exec", "workspace_write_stdin", "workspace_kill_session", "workspace_save_artifact",
		"session_search", "session_load", "transfer_to_agent", "await_user_reply", "environment_context_current_time",
	}
	tools := newToolMap()
	for _, name := range workflow {
		tools[name] = &stubTool{name: name, description: "framework workflow helper"}
	}
	for _, name := range []string{"tool_search", "call_tool", "memory_search", "file_read_file"} {
		tools[name] = &stubTool{name: name, description: "framework workflow helper"}
	}
	req := newUserRequest("framework workflow helper", tools)
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	for _, name := range workflow {
		require.Contains(t, req.Tools, name)
		require.NotContains(t, rec.lastQuestions(), "tool:"+name)
	}
	for _, name := range []string{"tool_search", "call_tool", "memory_search", "file_read_file"} {
		require.NotContains(t, req.Tools, name)
		require.Contains(t, rec.lastQuestions(), "tool:"+name)
	}
}

func TestCallback_SlackFallbackWithKeepSet(t *testing.T) {
	t.Parallel()
	server, _ := decisionServer(t, binaryResponder(map[string]float64{
		"tool:hotel_booking": 0.5,
	}, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0))
	require.NoError(t, err)
	req := newUserRequest("book hotel rooms", newToolMap(
		catalogTools()["hotel_booking"], catalogTools()["calculator"], catalogTools()["skill_load"],
	))
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, []string{"hotel_booking", "skill_load"}, sortedToolNames(req.Tools))
}

func TestCallback_TurnCacheReplaysWithinTurn(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()))
	require.NoError(t, err)

	tools := newToolMap(catalogTools()["web_search"],
		catalogTools()["hotel_booking"],
		catalogTools()["calculator"],
		catalogTools()["calendar"],
		catalogTools()["email_sender"],
		catalogTools()["file_reader"],
		catalogTools()["database_query"],
		catalogTools()["image_render"],
		catalogTools()["skill_load"],
	)

	ctx := toolTestContext(newUserRequest("find cheap flights and hotels online", tools))
	// Same user message: the second ReAct step replays the cached decision.
	for i := 0; i < 2; i++ {
		req := newUserRequest("find cheap flights and hotels online", tools)
		_, err := cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
		require.NoError(t, err)
		require.Equal(t, 1, rec.requests())
	}

	// A new user message is a new turn and triggers a new decision.
	req := newUserRequest("read a file from disk", tools)
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, 2, rec.requests())
}

func TestCallback_FailOpenOnServerError(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, nil, http.StatusInternalServerError)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()))
	require.NoError(t, err)

	tools := newToolMap(catalogTools()["web_search"],
		catalogTools()["hotel_booking"],
		catalogTools()["calculator"],
		catalogTools()["calendar"],
		catalogTools()["email_sender"],
		catalogTools()["file_reader"],
		catalogTools()["database_query"],
		catalogTools()["image_render"],
		catalogTools()["skill_load"],
	)
	req := newUserRequest("find cheap flights online", tools)

	ctx := toolTestContext(req)
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools) // full catalog kept
	require.Equal(t, 1, rec.requests())

	// The fail-open result is cached: no request on the next step.
	req2 := newUserRequest("find cheap flights online", tools)
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req2})
	require.NoError(t, err)
	require.Equal(t, tools, req2.Tools)
	require.Equal(t, 1, rec.requests())
}

func TestCallback_FailOpenOnInvalidResponse(t *testing.T) {
	t.Parallel()

	server, _ := decisionServer(t, func(map[string]string) string {
		return "not json"
	}, http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()))
	require.NoError(t, err)

	tools := newToolMap(catalogTools()["web_search"],
		catalogTools()["hotel_booking"],
		catalogTools()["calculator"],
		catalogTools()["calendar"],
		catalogTools()["email_sender"],
		catalogTools()["file_reader"],
		catalogTools()["database_query"],
		catalogTools()["image_render"],
		catalogTools()["skill_load"],
	)
	req := newUserRequest("find cheap flights online", tools)

	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools)
}

func TestCallback_MinToolCountSkipsFiltering(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()))
	require.NoError(t, err)

	tools := newToolMap(
		&stubTool{name: "web_search", description: "search the web"},
		&stubTool{name: "calculator", description: "arithmetic"},
		&stubTool{name: "calendar", description: "events"},
		&stubTool{name: "email_sender", description: "emails"},
		&stubTool{name: "file_reader", description: "files"},
	)
	req := newUserRequest("find flights", tools)

	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools)
	require.Equal(t, 0, rec.requests())
}

func TestCallback_MinToolCountBoundary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		want  int
	}{
		{name: "below", count: 1, want: 0},
		{name: "equal", count: 2, want: 1},
		{name: "above", count: 3, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, rec := decisionServer(t, binaryResponder(nil, 0.9), http.StatusOK)
			cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(2))
			require.NoError(t, err)
			tools := newToolMap()
			for i := 0; i < tc.count; i++ {
				name := fmt.Sprintf("tool_%d", i)
				tools[name] = &stubTool{name: name}
			}
			req := newUserRequest("find tools", tools)
			_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
			require.NoError(t, err)
			require.Equal(t, tc.want, rec.requests())
		})
	}
}

func TestCallback_UntouchedDecisionReplayed(t *testing.T) {
	tests := []struct {
		name    string
		message string
		tools   []*stubTool
		opts    []Option
		reason  string
	}{
		{"small catalog", "find flights", []*stubTool{{name: "search"}}, nil, "catalog below min tool count"},
		{"no scoreable candidates", "find flights", []*stubTool{{name: "skill_load"}}, []Option{WithMinToolCount(0)}, "no scoreable candidates"},
		{"no lexical overlap", "你好世界", []*stubTool{{name: "search"}, {name: "calendar"}}, []Option{WithMinToolCount(0), WithShortlistLimit(1)}, "no lexical overlap with the user message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
			var reports []*Report
			opts := append([]Option{WithHTTPClient(server.Client()), WithObserver(func(r *Report) { reports = append(reports, r) })}, tt.opts...)
			cb, err := NewToolCallback(server.URL, opts...)
			require.NoError(t, err)
			inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage(tt.message)))
			ctx := agent.NewInvocationContext(context.Background(), inv)
			initial := newToolMap(tt.tools...)
			first := newUserRequest(tt.message, initial)
			_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: first})
			require.NoError(t, err)
			require.Equal(t, initial, first.Tools)
			later := newToolMap(tt.tools...)
			for i := 0; i < 10; i++ {
				name := fmt.Sprintf("new_tool_%d", i)
				later[name] = &stubTool{name: name, description: "find flights 你好世界"}
			}
			second := newUserRequest(tt.message, later)
			_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: second})
			require.NoError(t, err)
			require.Equal(t, later, second.Tools)
			require.Len(t, reports, 1)
			require.Equal(t, CatalogTool, reports[0].Catalog)
			require.Equal(t, OutcomeUntouched, reports[0].Outcome)
			require.Equal(t, tt.reason, reports[0].Reason)
			require.Equal(t, 0, rec.requests())
		})
	}
}

func TestCallback_UntouchedReplayDoesNotRetryFailedEmbedding(t *testing.T) {
	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	emb := &stubEmbedder{err: errors.New("embedding failed")}
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0),
		WithShortlistLimit(1), WithShortlistEmbedder(emb))
	require.NoError(t, err)
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("你好世界")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tools := newToolMap(&stubTool{name: "search"}, &stubTool{name: "calendar"})
	for i := 0; i < 2; i++ {
		req := newUserRequest("你好世界", tools)
		_, err := cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
		require.NoError(t, err)
		require.Equal(t, tools, req.Tools)
	}
	require.Equal(t, 1, emb.calls())
	require.Equal(t, 0, rec.requests())
}

func TestCallback_UntouchedObserverCanReenter(t *testing.T) {
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("find flights")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	tools := newToolMap(&stubTool{name: "search"})
	var cb *ToolCallback
	reports := 0
	var err error
	cb, err = NewToolCallback("https://decision.example", WithObserver(func(*Report) {
		reports++
		req := newUserRequest("find flights", tools)
		_, _ = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	}))
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		req := newUserRequest("find flights", tools)
		_, _ = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("observer blocked when reentering the filter")
	}
	require.Equal(t, 1, reports)
}

func TestCallback_AllCandidatesKeptReportsUntouched(t *testing.T) {
	for _, tc := range []struct {
		name  string
		score float64
		opts  []Option
	}{
		{name: "threshold", score: 0.9},
		{name: "slack", score: 0.5},
		{name: "unasked and always kept", score: 0.9, opts: []Option{WithMaxQuestions(1), WithKeepAlways("fixed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, rec := decisionServer(t, binaryResponder(nil, tc.score), http.StatusOK)
			var reports []*Report
			opts := append([]Option{WithHTTPClient(server.Client()), WithMinToolCount(0), WithObserver(func(r *Report) { reports = append(reports, r) })}, tc.opts...)
			cb, err := NewToolCallback(server.URL, opts...)
			require.NoError(t, err)
			inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("find tools")))
			ctx := agent.NewInvocationContext(context.Background(), inv)
			tools := newToolMap(&stubTool{name: "alpha"}, &stubTool{name: "beta"}, &stubTool{name: "fixed"})
			for i := 0; i < 2; i++ {
				req := newUserRequest("find tools", tools)
				_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
				require.NoError(t, err)
				require.Equal(t, tools, req.Tools)
			}
			require.Equal(t, 1, rec.requests())
			require.Len(t, reports, 1)
			require.Equal(t, OutcomeUntouched, reports[0].Outcome)
			require.Empty(t, reports[0].Dropped)
			require.Zero(t, reports[0].SavedTokens)
			cached, ok := cb.cache.get(inv.InvocationID)
			require.True(t, ok)
			require.Equal(t, OutcomeUntouched, cached.outcome)
		})
	}
}

func TestCallback_RemovalWithZeroEstimatedSavingsIsNarrowed(t *testing.T) {
	server, _ := decisionServer(t, binaryResponder(map[string]float64{"tool:a": 0.9, "tool:b": 0.1}, 0.1), http.StatusOK)
	var report *Report
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0), WithObserver(func(r *Report) { report = r }))
	require.NoError(t, err)
	tools := newToolMap(&stubTool{name: "a"}, &stubTool{name: "b"})
	req := newUserRequest("find tools", tools)
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, sortedToolNames(req.Tools))
	require.Equal(t, OutcomeNarrowed, report.Outcome)
	require.Equal(t, []string{"b"}, report.Dropped)
	require.Zero(t, report.SavedTokens)
}

func TestCallback_NoUserMessageSkipsFiltering(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()))
	require.NoError(t, err)

	tools := newToolMap(catalogTools()["web_search"],
		catalogTools()["hotel_booking"],
		catalogTools()["calculator"],
		catalogTools()["calendar"],
		catalogTools()["email_sender"],
		catalogTools()["file_reader"],
		catalogTools()["database_query"],
		catalogTools()["image_render"],
	)
	req := &model.Request{Tools: tools}

	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools)
	require.Equal(t, 0, rec.requests())
}

func TestCallback_ZeroLexicalOverlapAborts(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0), WithShortlistLimit(1))
	require.NoError(t, err)

	tools := newToolMap(catalogTools()["web_search"],
		catalogTools()["hotel_booking"],
		catalogTools()["calculator"],
		catalogTools()["calendar"],
		catalogTools()["email_sender"],
		catalogTools()["file_reader"],
		catalogTools()["database_query"],
		catalogTools()["image_render"],
	)
	// No token overlap with any catalog entry.
	req := newUserRequest("你好世界", tools)

	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Equal(t, tools, req.Tools)
	require.Equal(t, 0, rec.requests())
}

func TestCallback_SmallCatalogAskedWithoutLexicalOverlap(t *testing.T) {
	t.Parallel()
	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0), WithShortlistLimit(2))
	require.NoError(t, err)
	req := newUserRequest("unrelated", newToolMap(&stubTool{name: "search", description: "find flights"}))
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Contains(t, rec.lastQuestions(), "tool:search")
}

func TestCallback_UnaskedBeyondQuestionBudgetKept(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL,
		WithHTTPClient(server.Client()),
		WithShortlistLimit(24),
		WithMaxQuestions(20),
	)
	require.NoError(t, err)

	// 25 scoreable candidates plus the framework keep set.
	tools := make(map[string]*stubTool, 30)
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("helper_%02d", i)
		tools[name] = &stubTool{name: name, description: "helper tool alpha beta"}
	}
	toolMap := newToolMap()
	for name, st := range tools {
		toolMap[name] = st
	}
	for _, name := range defaultKeepTools {
		toolMap[name] = &stubTool{name: name, description: "built-in"}
	}

	req := newUserRequest("use the helper tool alpha beta", toolMap)
	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	require.Equal(t, 1, rec.requests())
	require.Len(t, rec.lastQuestions(), 20) // asked within budget

	// Every candidate scores 0.05, so only the keep set and the four
	// shortlisted-but-unasked candidates survive.
	want := append([]string{}, defaultKeepTools...)
	want = append(want, "helper_20", "helper_21", "helper_22", "helper_23")
	got := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		got = append(got, name)
	}
	require.ElementsMatch(t, want, got)
}

func TestCallback_KeepAlwaysProtectsUserTools(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	cb, err := NewToolCallback(server.URL,
		WithHTTPClient(server.Client()),
		WithKeepAlways("crm_connector", "tool_search"),
	)
	require.NoError(t, err)

	tools := newToolMap(catalogTools()["web_search"],
		catalogTools()["hotel_booking"],
		catalogTools()["calculator"],
		catalogTools()["calendar"],
		catalogTools()["email_sender"],
		catalogTools()["file_reader"],
		catalogTools()["database_query"],
		catalogTools()["skill_load"],
		catalogTools()["tool_search"],
		&stubTool{name: "crm_connector", description: "company CRM access"},
	)
	req := newUserRequest("find cheap flights online", tools)

	_, err = cb.Callback()(toolTestContext(req), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Contains(t, req.Tools, "crm_connector")
	require.NotContains(t, rec.lastQuestions(), "tool:crm_connector")
	require.Contains(t, req.Tools, "tool_search")
	require.NotContains(t, rec.lastQuestions(), "tool:tool_search")
}
