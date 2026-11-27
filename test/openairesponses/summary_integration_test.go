//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const multiCallOutput = `[
	{"type":"reasoning","id":"rs_multi","summary":[],"encrypted_content":"opaque-multi-state"},
	{"type":"function_call","id":"fc_a","status":"completed","call_id":"call_a","name":"calculator","arguments":"{\"a\":1}"},
	{"type":"function_call","id":"fc_b","status":"completed","call_id":"call_b","name":"calculator","arguments":"{\"a\":2}"}
]`

func messageOutput(id, text string) string {
	return `[{"type":"message","id":"` + id + `","status":"completed","role":"assistant",` +
		`"content":[{"type":"output_text","text":"` + text + `"}]}]`
}

func TestSummaryCutoffInsideNativeMultiCallRoundRestoresTheRound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	outputs := []string{
		messageOutput("msg_first", "first answer"),
		multiCallOutput,
		messageOutput("msg_done", "both done"),
		messageOutput("msg_next", "next answer"),
	}
	var mu sync.Mutex
	var inputs []json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		inputs = append(inputs, body["input"])
		call := len(inputs)
		mu.Unlock()
		if call > len(outputs) {
			http.Error(w, "unexpected extra model call", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"resp_%d","status":"completed","output":%s}`, call, outputs[call-1])
	}))
	defer srv.Close()
	recorded := func() []json.RawMessage {
		mu.Lock()
		defer mu.Unlock()
		return append([]json.RawMessage(nil), inputs...)
	}

	var toolCalls atomic.Int32
	m := responses.New("gpt-5", responses.WithAPIKey("test"), responses.WithBaseURL(srv.URL))
	a := llmagent.New("native", llmagent.WithModel(m),
		llmagent.WithTools([]tool.Tool{integrationCalculator(&toolCalls)}))
	path := filepath.Join(t.TempDir(), "summary.db")
	openService := func() *sqlite.Service {
		db, err := sql.Open("sqlite3", path)
		require.NoError(t, err)
		svc, err := sqlite.NewService(db)
		require.NoError(t, err)
		return svc
	}
	svc := openService()
	r := runner.NewRunner("native", a, runner.WithSessionService(svc))
	for _, text := range []string{"first question", "compute both"} {
		for _, evt := range runIntegration(t, r, ctx, text) {
			require.Nil(t, evt.Error)
		}
	}
	require.EqualValues(t, 2, toolCalls.Load())
	require.Len(t, recorded(), 3)
	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())

	svc = openService()
	t.Cleanup(func() { _ = svc.Close() })
	sess, err := svc.GetSession(ctx, session.Key{AppName: "native", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	var round *event.Event
	var results []*event.Event
	for i := range sess.Events {
		evt := &sess.Events[i]
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		msg := evt.Response.Choices[0].Message
		if len(msg.ToolCalls) == 2 && msg.ProviderData != nil {
			round = evt
		}
		if msg.Role == model.RoleTool {
			results = append(results, evt)
		}
	}
	require.NotNil(t, round, "the reloaded session keeps the native round")
	require.NotEmpty(t, results)
	for _, result := range results {
		require.True(t, result.Timestamp.After(round.Timestamp),
			"the summary cutoff must fall inside the native round")
	}
	// The summary covers the native call event, but not its tool results.
	const summary = "Earlier turns were summarized."
	sess.Summaries = map[string]*session.Summary{round.FilterKey: {
		Summary:   summary,
		UpdatedAt: round.Timestamp,
		Boundary:  session.NewSummaryBoundaryWithEventID(round.FilterKey, round.Timestamp, round.ID),
	}}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationModel(m),
		agent.WithInvocationMessage(model.NewUserMessage("third question")),
		agent.WithInvocationEventFilterKey(round.FilterKey),
	)
	publicAgent := llmagent.New("native",
		llmagent.WithModel(m),
		llmagent.WithAddSessionSummary(true),
	)
	ch, err := publicAgent.Run(ctx, inv)
	require.NoError(t, err)
	for evt := range ch {
		require.Nil(t, evt.Error)
	}
	sent := recorded()
	require.Len(t, sent, 4)
	next := sent[3]
	for _, summarized := range []string{"first question", "first answer"} {
		require.NotContains(t, string(next), summarized, "the summarized prefix must be omitted")
	}

	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(next, &items))
	var native []map[string]json.RawMessage
	results2 := map[string]string{}
	summaryIndex, anchorIndex, nativeIndex, doneIndex, questionIndex := -1, -1, -1, -1, -1
	var outputIndexes []int
	for i, item := range items {
		id := string(item["id"])
		switch {
		case strings.Contains(string(item["content"]), summary):
			summaryIndex = i
		case string(item["content"]) == `"compute both"`:
			anchorIndex = i
		case id == `"rs_multi"` || id == `"fc_a"` || id == `"fc_b"`:
			if nativeIndex < 0 {
				nativeIndex = i
			}
			native = append(native, item)
		case string(item["type"]) == `"function_call_output"`:
			var callID, output string
			require.NoError(t, json.Unmarshal(item["call_id"], &callID))
			require.NoError(t, json.Unmarshal(item["output"], &output))
			results2[callID] = output
			outputIndexes = append(outputIndexes, i)
		case id == `"msg_done"`:
			doneIndex = i
		case strings.Contains(string(item["content"]), "third question"):
			questionIndex = i
		}
	}
	encoded, err := json.Marshal(native)
	require.NoError(t, err)
	require.JSONEq(t, multiCallOutput, string(encoded), "the round is restored with its complete native items")
	require.Equal(t, map[string]string{"call_a": "2", "call_b": "3"}, results2)
	require.Equal(t, []int{nativeIndex + 3, nativeIndex + 4}, outputIndexes,
		"both call outputs directly follow the restored native items")
	require.GreaterOrEqual(t, summaryIndex, 0)
	// The summarized user request of the cut turn stays as its anchor.
	require.Equal(t, []int{summaryIndex + 1, summaryIndex + 2}, []int{anchorIndex, nativeIndex})
	require.Greater(t, doneIndex, outputIndexes[1])
	require.Greater(t, questionIndex, doneIndex)
}

func TestSummaryCutoffInsideMixedNativeRoundRestoresCompleteResults(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, cutoffAt := range []string{"call", "search", "weather"} {
			t.Run(fmt.Sprintf("stream=%v/cutoff=%s", stream, cutoffAt), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				output := "[" + weatherCallItem + "," + clientSearchItem + "]"
				server, srv := newSearchServer(t, messageOutput("msg_first", "old answer"), output,
					messageOutput("msg_done", "done"), messageOutput("msg_next", "next"))
				executions := &searchExecutions{}
				var searches int
				handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					searches++
					return selectSearchCandidates(req, "billing_lookup"), nil
				}
				m := newSearchModel(srv, responses.WithClientToolSearch(handler))
				path := filepath.Join(t.TempDir(), "mixed-summary.db")
				svc := openNativeRoutingSQLite(t, path)
				r := runner.NewRunner("search", newSearchAgent(m, executions, stream), runner.WithSessionService(svc))
				for _, text := range []string{"old question", "weather and invoice"} {
					_, failures := runSearch(t, r, model.NewUserMessage(text))
					require.Empty(t, failures)
				}
				require.Equal(t, 1, searches)
				require.Equal(t, []string{"weather"}, executions.executed())
				require.Len(t, server.requests(), 3)
				require.NoError(t, r.Close())
				require.NoError(t, svc.Close())

				svc = openNativeRoutingSQLite(t, path)
				t.Cleanup(func() { _ = svc.Close() })
				sess, err := svc.GetSession(ctx, session.Key{AppName: "search", UserID: "user", SessionID: "session"})
				require.NoError(t, err)
				var call, searchResult, weatherResult *event.Event
				for i := range sess.Events {
					evt := &sess.Events[i]
					if evt.Response == nil || len(evt.Choices) == 0 || evt.IsPartial {
						continue
					}
					msg := evt.Choices[0].Message
					switch {
					case len(msg.ToolCalls) > 0 && msg.ProviderData != nil:
						call = evt
					case msg.ToolID == "search_1":
						searchResult = evt
					case msg.ToolID == "weather_1":
						weatherResult = evt
					}
				}
				require.NotNil(t, call)
				require.NotNil(t, searchResult)
				require.NotNil(t, weatherResult)
				cutoffs := map[string]*event.Event{"call": call, "search": searchResult, "weather": weatherResult}
				boundary := cutoffs[cutoffAt]
				const summary = "Earlier turns were summarized."
				sess.Summaries = map[string]*session.Summary{boundary.FilterKey: {
					Summary: summary, UpdatedAt: boundary.Timestamp,
					Boundary: session.NewSummaryBoundaryWithEventID(boundary.FilterKey, boundary.Timestamp, boundary.ID),
				}}
				inv := agent.NewInvocation(
					agent.WithInvocationSession(sess), agent.WithInvocationModel(m),
					agent.WithInvocationMessage(model.NewUserMessage("next question")),
					agent.WithInvocationEventFilterKey(boundary.FilterKey),
				)
				publicAgent := newSearchAgent(m, executions, stream, llmagent.WithAddSessionSummary(true))
				ch, err := publicAgent.Run(ctx, inv)
				require.NoError(t, err)
				for evt := range ch {
					if evt.RequiresCompletion {
						require.NoError(t, inv.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(evt.ID)))
					}
					require.Nil(t, evt.Error)
				}
				require.Equal(t, 1, searches, "history replay must not rerun the search")
				require.Equal(t, []string{"weather"}, executions.executed(), "history replay must not rerun the function")
				requests := server.requests()
				require.Len(t, requests, 4)
				require.NotContains(t, string(requests[3]["input"]), "old question")
				require.NotContains(t, string(requests[3]["input"]), "old answer")
				items := conversationItems(t, requests[3]["input"])
				if cutoffAt == "weather" {
					require.Equal(t, []string{"message:user", "message:assistant", "message:user"}, inputTypes(items),
						"a completely summarized historical round must stay omitted")
					return
				}
				require.Equal(t, []string{"message:user", "function_call", "tool_search_call", "tool_search_output", "function_call_output", "message:assistant", "message:user"}, inputTypes(items))
				require.Equal(t, "search_1", items[3].CallID)
				require.Contains(t, string(items[3].Tools), "lookup", "the complete native search result is restored")
				require.Equal(t, "weather_1", items[4].CallID)
				findRaw := func(raw json.RawMessage, kind string) string {
					t.Helper()
					var all []json.RawMessage
					require.NoError(t, json.Unmarshal(raw, &all))
					for _, item := range all {
						var header struct {
							Type string `json:"type"`
						}
						require.NoError(t, json.Unmarshal(item, &header))
						if header.Type == kind {
							return string(item)
						}
					}
					t.Fatalf("missing %s in %s", kind, raw)
					return ""
				}
				for _, kind := range []string{"function_call", "tool_search_call", "tool_search_output", "function_call_output"} {
					require.JSONEq(t, findRaw(requests[2]["input"], kind), findRaw(requests[3]["input"], kind),
						"the restored %s retains its complete payload", kind)
				}
			})
		}
	}
}

func TestSummaryCutoffBeforeNativeSearchResultRestoresActionOnlyRecord(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			server, srv := newSearchServer(t, messageOutput("msg_first", "old answer"),
				"["+clientSearchItem+"]", messageOutput("msg_done", "done"), messageOutput("msg_next", "next"))
			executions := &searchExecutions{}
			var searches int
			handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				searches++
				return selectSearchCandidates(req, "billing_lookup"), nil
			}
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			path := filepath.Join(t.TempDir(), "action-only-summary.db")
			svc := openNativeRoutingSQLite(t, path)
			r := runner.NewRunner("search", newSearchAgent(m, executions, stream), runner.WithSessionService(svc))
			for _, text := range []string{"old question", "find invoice"} {
				_, failures := runSearch(t, r, model.NewUserMessage(text))
				require.Empty(t, failures)
			}
			require.Equal(t, 1, searches)
			require.Empty(t, executions.executed())
			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())

			svc = openNativeRoutingSQLite(t, path)
			t.Cleanup(func() { _ = svc.Close() })
			sess, err := svc.GetSession(ctx, session.Key{AppName: "search", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			var source *event.Event
			for i := range sess.Events {
				evt := &sess.Events[i]
				if evt.Response == nil || evt.IsPartial || evt.Done || len(evt.Choices) == 0 {
					continue
				}
				msg := evt.Choices[0].Message
				if msg.Role == model.RoleAssistant && msg.ProviderData != nil && len(msg.ToolCalls) == 0 {
					source = evt
				}
			}
			require.NotNil(t, source)
			const summary = "Earlier turns were summarized."
			sess.Summaries = map[string]*session.Summary{source.FilterKey: {
				Summary: summary, UpdatedAt: source.Timestamp,
				Boundary: session.NewSummaryBoundaryWithEventID(source.FilterKey, source.Timestamp, source.ID),
			}}
			inv := agent.NewInvocation(agent.WithInvocationSession(sess), agent.WithInvocationModel(m),
				agent.WithInvocationMessage(model.NewUserMessage("next question")),
				agent.WithInvocationEventFilterKey(source.FilterKey))
			ch, err := newSearchAgent(m, executions, stream, llmagent.WithAddSessionSummary(true)).Run(ctx, inv)
			require.NoError(t, err)
			for evt := range ch {
				if evt.RequiresCompletion {
					require.NoError(t, inv.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(evt.ID)))
				}
				require.Nil(t, evt.Error)
			}
			require.Equal(t, 1, searches, "history replay must not rerun the search")
			require.Empty(t, executions.executed())
			requests := server.requests()
			require.Len(t, requests, 4)
			require.NotContains(t, string(requests[3]["input"]), "old question")
			require.NotContains(t, string(requests[3]["input"]), "old answer")
			items := conversationItems(t, requests[3]["input"])
			require.Equal(t, []string{"message:user", "tool_search_call", "tool_search_output", "message:assistant", "message:user"}, inputTypes(items))
			require.Equal(t, "search_1", items[1].CallID)
			require.Equal(t, "search_1", items[2].CallID)
			require.Contains(t, string(items[2].Tools), "lookup")
		})
	}
}
