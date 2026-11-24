//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunnerClientToolSearchResumeKeepsTrimmedHistoryCandidates(t *testing.T) {
	for _, policy := range []string{"history limit", "summary cutoff"} {
		t.Run(policy, func(t *testing.T) {
			testClientSearchResumeAfterHistoryTrim(t, policy)
		})
	}
}

type resumeHistorySummarizer struct{}

func (resumeHistorySummarizer) ShouldSummarize(*session.Session) bool { return false }
func (resumeHistorySummarizer) Summarize(context.Context, *session.Session) (string, error) {
	return "The old invoice request is complete.", nil
}
func (resumeHistorySummarizer) SetPrompt(string)         {}
func (resumeHistorySummarizer) SetModel(model.Model)     {}
func (resumeHistorySummarizer) Metadata() map[string]any { return nil }

func testClientSearchResumeAfterHistoryTrim(t *testing.T, policy string) {
	t.Helper()
	const oldSearch = `[{"type":"tool_search_call","id":"ts_old","execution":"client","call_id":"old_search","status":"completed","arguments":{"query":"invoice"}}]`
	const freshSearch = `[{"type":"tool_search_call","id":"ts_fresh","execution":"client","call_id":"fresh_search","status":"completed","arguments":{"query":"invoice again"}}]`
	server, srv := newSearchServer(t, oldSearch, messageOutput("msg_old", "old done"),
		freshSearch, messageOutput("msg_fresh", "fresh done"))
	var freshCandidates [][]string
	handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		if req.CallID == "fresh_search" {
			freshCandidates = append(freshCandidates, candidateNames(req))
			if len(freshCandidates) == 1 {
				return nil, errors.New("temporary search failure")
			}
		}
		selected := selectSearchCandidates(req, "billing_lookup")
		if len(selected) != 1 {
			return nil, fmt.Errorf("billing candidate missing for %s", req.CallID)
		}
		return selected, nil
	}
	m := newSearchModel(srv, responses.WithClientToolSearch(handler))
	executions := &searchExecutions{}
	path := filepath.Join(t.TempDir(), "trimmed-search.db")
	openService := func() *sqlite.Service {
		db, err := sql.Open("sqlite3", path)
		require.NoError(t, err)
		svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false),
			sqlite.WithSummarizer(resumeHistorySummarizer{}))
		require.NoError(t, err)
		return svc
	}
	options := []llmagent.Option{llmagent.WithMaxHistoryRuns(1)}
	if policy == "summary cutoff" {
		options = []llmagent.Option{llmagent.WithAddSessionSummary(true)}
	}
	run := func(message model.Message, opts ...agent.RunOption) (string, []string) {
		svc := openService()
		defer svc.Close()
		r := runner.NewRunner("search", newSearchAgent(m, executions, false, options...), runner.WithSessionService(svc))
		defer r.Close()
		events, failures := runSearch(t, r, message, opts...)
		return finalText(events), failures
	}

	text, failures := run(model.NewUserMessage("old invoice request"))
	require.Empty(t, failures)
	require.Equal(t, "old done", text)
	if policy == "summary cutoff" {
		svc := openService()
		ctx := context.Background()
		key := session.Key{AppName: "search", UserID: "user", SessionID: "session"}
		sess, err := svc.GetSession(ctx, key)
		require.NoError(t, err)
		var filterKey string
		for _, evt := range sess.Events {
			if evt.Response != nil && len(evt.Choices) > 0 && evt.Choices[0].Message.Content == "old done" {
				filterKey = evt.FilterKey
			}
		}
		require.NoError(t, svc.CreateSessionSummary(ctx, sess, filterKey, true))
		require.NoError(t, svc.Close())
		svc = openService()
		sess, err = svc.GetSession(ctx, key)
		require.NoError(t, err)
		require.NotEmpty(t, sess.Summaries)
		require.NotEmpty(t, sess.Summaries[filterKey].CutoffBoundary().LastEventID,
			"the persisted summary has a real event cutoff")
		require.NoError(t, svc.Close())
	}
	_, failures = run(model.NewUserMessage("new invoice request"))
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "temporary search failure")
	require.Len(t, server.requests(), 3)
	beforeFailure := conversationItems(t, server.requests()[2]["input"])
	require.Equal(t, []string{"message:user"}, inputTypes(beforeFailure),
		"the new search did not see the old search result after history trimming")
	require.Contains(t, freshCandidates[0], "billing_lookup")

	text, failures = run(model.Message{}, agent.WithResume(true))
	require.Empty(t, failures)
	require.Equal(t, "fresh done", text)
	require.Len(t, freshCandidates, 2)
	require.Equal(t, freshCandidates[0], freshCandidates[1],
		"resume must not revive an old loaded schema that the failed request had already trimmed")
	require.Len(t, server.requests(), 4)
	require.Empty(t, executions.executed())
}

func TestRunnerClientToolSearchResumeKeepsRequestWindow(t *testing.T) {
	const oldSearch = `[{"type":"tool_search_call","id":"ts_old","execution":"client","call_id":"old_search","status":"completed","arguments":{"query":"invoice"}}]`
	const freshSearch = `[{"type":"tool_search_call","id":"ts_fresh","execution":"client","call_id":"fresh_search","status":"completed","arguments":{"query":"invoice again"}}]`
	server, srv := newSearchServer(t, oldSearch, messageOutput("msg_old", "old done"), freshSearch, messageOutput("msg_fresh", "fresh done"))
	var seen [][]string
	handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		if req.CallID == "old_search" {
			return selectSearchCandidates(req, "billing_lookup"), nil
		}
		seen = append(seen, candidateNames(req))
		if len(seen) == 1 {
			return nil, errors.New("temporary search failure")
		}
		return nil, nil
	}
	m := newSearchModel(srv, responses.WithClientToolSearch(handler))
	execs := &searchExecutions{}
	path := filepath.Join(t.TempDir(), "window.db")
	run := func(msg model.Message, opts ...agent.RunOption) (string, []string) {
		svc := openNativeRoutingSQLite(t, path)
		defer svc.Close()
		r := runner.NewRunner("search", newSearchAgent(m, execs, false, llmagent.WithMaxHistoryRuns(4)), runner.WithSessionService(svc))
		defer r.Close()
		events, failures := runSearch(t, r, msg, opts...)
		return finalText(events), failures
	}
	text, failures := run(model.NewUserMessage("old request"))
	require.Empty(t, failures)
	require.Equal(t, "old done", text)
	_, failures = run(model.NewUserMessage("new request"))
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "temporary search failure")
	inputs := conversationItems(t, server.requests()[2]["input"])
	require.Equal(t, []string{"tool_search_call", "tool_search_output", "message:assistant", "message:user"}, inputTypes(inputs))
	text, failures = run(model.Message{}, agent.WithResume(true))
	require.Empty(t, failures)
	require.Equal(t, "fresh done", text)
	require.Len(t, seen, 2)
	require.NotContains(t, seen[0], "billing_lookup", "the failed request still saw the old search output")
	require.Equal(t, seen[0], seen[1], "the pending search must not push the old output outside its own request window")
}

func TestRunnerFunctionResumeRespectsTrimmedSearchBindings(t *testing.T) {
	const oldSearch = `[{"type":"tool_search_call","id":"ts_old","execution":"client","call_id":"old_search","status":"completed","arguments":{"query":"weather"}}]`
	const callWeather = `[{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","name":"weather","arguments":"{\"id\":\"x\"}"}]`
	for _, tc := range []struct {
		name, policy, loadedName string
		pause                    bool
	}{
		{name: "history limit and resume", policy: "history", loadedName: "weather", pause: true},
		{name: "summary cutoff and resume", policy: "summary", loadedName: "weather", pause: true},
		{name: "history limit without resume", policy: "history", loadedName: "weather"},
		{name: "resume without matching old binding", policy: "history", loadedName: "other", pause: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, srv := newSearchServer(t, oldSearch, messageOutput("msg_old", "old done"), callWeather, messageOutput("msg_new", "new done"))
			execs := &searchExecutions{}
			handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				return []responses.ToolSearchEntry{{Name: tc.loadedName, Tool: execs.sandboxTool("sandbox_weather", "sandbox")}}, nil
			}
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			path := filepath.Join(t.TempDir(), "ordinary.db")
			openService := func() *sqlite.Service {
				db, err := sql.Open("sqlite3", path)
				require.NoError(t, err)
				svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false), sqlite.WithSummarizer(resumeHistorySummarizer{}))
				require.NoError(t, err)
				return svc
			}
			options := []llmagent.Option{llmagent.WithMaxHistoryRuns(1)}
			if tc.policy == "summary" {
				options = []llmagent.Option{llmagent.WithAddSessionSummary(true)}
			}
			run := func(msg model.Message, opts ...agent.RunOption) (string, []string) {
				svc := openService()
				defer svc.Close()
				r := runner.NewRunner("search", newSearchAgent(m, execs, false, options...), runner.WithSessionService(svc))
				defer r.Close()
				events, failures := runSearch(t, r, msg, opts...)
				return finalText(events), failures
			}
			text, failures := run(model.NewUserMessage("old request"))
			require.Empty(t, failures)
			require.Equal(t, "old done", text)
			if tc.policy == "summary" {
				svc := openService()
				ctx := context.Background()
				sess, err := svc.GetSession(ctx, session.Key{AppName: "search", UserID: "user", SessionID: "session"})
				require.NoError(t, err)
				var filterKey string
				for _, evt := range sess.Events {
					if evt.Response != nil && len(evt.Choices) > 0 && evt.Choices[0].Message.Content == "old done" {
						filterKey = evt.FilterKey
					}
				}
				require.NoError(t, svc.CreateSessionSummary(ctx, sess, filterKey, true))
				require.NoError(t, svc.Close())
			}
			if tc.pause {
				_, failures = run(model.NewUserMessage("new request"), agent.WithToolExecutionFilter(func(_ context.Context, _ tool.Tool) bool { return false }))
				require.Empty(t, failures)
				require.Empty(t, execs.executed())
				require.Len(t, server.requests(), 3)
				require.NotContains(t, string(server.requests()[2]["input"]), "old_search", "the original request excluded the old binding")
				text, failures = run(model.Message{}, agent.WithResume(true))
			} else {
				text, failures = run(model.NewUserMessage("new request"))
			}
			require.Empty(t, failures)
			require.Equal(t, "new done", text)
			require.Equal(t, []string{"weather"}, execs.executed())
			require.Len(t, server.requests(), 4)
			require.Contains(t, string(server.requests()[3]["input"]), "weather:x")
		})
	}
}

func TestRunnerClientToolSearchResumeAfterSummaryKeepsSameInvocationHistory(t *testing.T) {
	const oldSearch = `[{"type":"tool_search_call","id":"ts_old","execution":"client","call_id":"old_search","status":"completed","arguments":{"query":"orders"}}]`
	const batch = `[
		{"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"invoice"}},
		{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"other"}}]`
	server, srv := newSearchServer(t, oldSearch, batch, messageOutput("msg_done", "done"))
	var mu sync.Mutex
	var search2Candidates [][]string
	handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		if req.CallID == "search_2" {
			mu.Lock()
			search2Candidates = append(search2Candidates, candidateNames(req))
			mu.Unlock()
		}
		if req.CallID == "old_search" {
			return selectSearchCandidates(req, "crm_orders"), nil
		}
		return selectSearchCandidates(req, "billing_lookup"), nil
	}
	m := newSearchModel(srv, responses.WithClientToolSearch(handler))
	execs := &searchExecutions{}
	path := filepath.Join(t.TempDir(), "partial-summary.db")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false),
		sqlite.WithAppendEventHook(func(c *session.AppendEventContext, next func() error) error {
			if c.Event.Response != nil && len(c.Event.Choices) > 0 {
				msg := c.Event.Choices[0].Message
				if msg.Role == model.RoleTool && msg.ToolID == "search_2" {
					return context.Canceled
				}
				if msg.Role == model.RoleTool && msg.ToolID == "search_1" {
					err := next()
					if err == nil {
						cancel()
					}
					return err
				}
			}
			return next()
		}))
	require.NoError(t, err)
	r := runner.NewRunner("search", newSearchAgent(m, execs, false, llmagent.WithAddSessionSummary(true)), runner.WithSessionService(svc))
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("invoice status"))
	require.NoError(t, err)
	for range ch {
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())
	require.Len(t, server.requests(), 2)

	db, err = sql.Open("sqlite3", path)
	require.NoError(t, err)
	svc, err = sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false), sqlite.WithSummarizer(resumeHistorySummarizer{}))
	require.NoError(t, err)
	defer svc.Close()
	key := session.Key{AppName: "search", UserID: "user", SessionID: "session"}
	sess, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	var saved []string
	var filterKey string
	for _, evt := range sess.Events {
		if evt.Response == nil || len(evt.Choices) == 0 {
			continue
		}
		msg := evt.Choices[0].Message
		if msg.Role == model.RoleTool {
			saved = append(saved, msg.ToolID)
			if msg.ToolID == "search_1" {
				filterKey = evt.FilterKey
			}
		}
	}
	require.Equal(t, []string{"old_search", "search_1"}, saved)
	require.NoError(t, svc.CreateSessionSummary(context.Background(), sess, filterKey, true))
	sess, err = svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotEmpty(t, sess.Summaries[filterKey].CutoffBoundary().LastEventID)

	r = runner.NewRunner("search", newSearchAgent(m, execs, false, llmagent.WithAddSessionSummary(true)), runner.WithSessionService(svc))
	defer r.Close()
	events, failures := runSearch(t, r, model.Message{}, agent.WithResume(true))
	require.Empty(t, failures)
	require.Equal(t, "done", finalText(events))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, [][]string{{"eta"}, {"eta"}}, search2Candidates,
		"a new resume invocation must retain the preceding complete search from the pending response's invocation")
}
