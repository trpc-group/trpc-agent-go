//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openairesponses_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	openaiopt "github.com/openai/openai-go/option"
	openaioptv3 "github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	chat "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	switchSearchReasoning = `{"type":"reasoning","id":"rs_search","status":"completed","summary":[{"type":"summary_text","text":"search reasoning"}],"encrypted_content":"opaque-search-reasoning"}`
	switchSearchCall      = `{"type":"tool_search_call","id":"ts_search","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"invoice"}}`
	switchInvoiceCall     = `{"type":"function_call","id":"fc_invoice","status":"completed","call_id":"invoice_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-1\"}"}`
	switchInvoiceAnswer   = `{"type":"message","id":"msg_invoice","status":"completed","role":"assistant","content":[{"type":"output_text","text":"120","annotations":[]}]}`
)

type searchSwitchRequest struct {
	path string
	body map[string]json.RawMessage
}

// searchSwitchFixture uses one scripted endpoint for both protocols, so the
// test can inspect each actual wire request and its ordering.
type searchSwitchFixture struct {
	mu       sync.Mutex
	requests []searchSwitchRequest
	replies  []switchReply
	sessions *inmemory.SessionService
	runner   runner.Runner
}

func newSearchSwitchFixture(t *testing.T, handler responses.ToolSearchHandler, targetSearch responses.Option, tools []tool.Tool, sets []tool.ToolSet, replies ...switchReply) *searchSwitchFixture {
	t.Helper()
	f := &searchSwitchFixture{replies: replies, sessions: inmemory.NewSessionService()}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := mustReadJSONBody(t, r)
		f.mu.Lock()
		i := len(f.requests)
		f.requests = append(f.requests, searchSwitchRequest{path: r.URL.Path, body: body})
		var reply switchReply
		if i < len(f.replies) {
			reply = f.replies[i]
		}
		f.mu.Unlock()
		if reply.status == 0 {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		_, _ = fmt.Fprint(w, reply.body)
	}))
	t.Cleanup(server.Close)
	models := map[string]model.Model{
		switchSource: responses.New(switchSource, responses.WithBaseURL(server.URL), responses.WithAPIKey("test"), responses.WithClientToolSearch(handler), responses.WithOpenAIOptions(openaioptv3.WithMaxRetries(0))),
		switchTarget: responses.New(switchTarget, responses.WithBaseURL(server.URL), responses.WithAPIKey("test"), targetSearch, responses.WithOpenAIOptions(openaioptv3.WithMaxRetries(0))),
		switchChat: chat.New(switchChat, chat.WithBaseURL(server.URL), chat.WithAPIKey("test"),
			chat.WithOpenAIOptions(openaiopt.WithMaxRetries(0))),
	}
	a := llmagent.New("switch", llmagent.WithModel(models[switchSource]), llmagent.WithModels(models),
		llmagent.WithInstruction("Use the conversation."), llmagent.WithTools(tools), llmagent.WithToolSets(sets),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: false}))
	f.runner = runner.NewRunner("switch", a, runner.WithSessionService(f.sessions))
	t.Cleanup(func() {
		require.NoError(t, f.runner.Close())
		require.NoError(t, f.sessions.Close())
	})
	return f
}

func (f *searchSwitchFixture) snapshot() []searchSwitchRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]searchSwitchRequest(nil), f.requests...)
}

func (f *searchSwitchFixture) run(t *testing.T, name, text string, opts ...agent.RunOption) string {
	t.Helper()
	opts = append(opts, agent.WithModelName(name))
	events, failures := runSearchSession(t, f.runner, "search-switch", model.NewUserMessage(text), opts...)
	require.Empty(t, failures)
	return finalText(events)
}

func (f *searchSwitchFixture) savedEvents(t *testing.T) []event.Event {
	t.Helper()
	s, err := f.sessions.GetSession(context.Background(), session.Key{AppName: "switch", UserID: "user", SessionID: "search-switch"})
	require.NoError(t, err)
	require.NotNil(t, s)
	return s.Events
}

func snapshotSearchEvents(t *testing.T, events []event.Event) []byte {
	t.Helper()
	raw, err := json.Marshal(events)
	require.NoError(t, err)
	return raw
}

func requireSearchEventsUnchanged(t *testing.T, f *searchSwitchFixture, old []byte, count int) {
	t.Helper()
	current := f.savedEvents(t)
	require.GreaterOrEqual(t, len(current), count)
	require.JSONEq(t, string(old), string(snapshotSearchEvents(t, current[:count])), "earlier events and their extensions must not be mutated")
}

func requireSearchChatRequest(t *testing.T, req searchSwitchRequest, expectedTools ...string) {
	t.Helper()
	require.Equal(t, "/chat/completions", req.path)
	var name string
	require.NoError(t, json.Unmarshal(req.body["model"], &name))
	require.Equal(t, switchChat, name)
	var messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Calls      json.RawMessage `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
	}
	require.NoError(t, json.Unmarshal(req.body["messages"], &messages))
	var callIndex, resultIndex = -1, -1
	for i, msg := range messages {
		if msg.Role == "assistant" && len(msg.Calls) > 0 && string(msg.Calls) != "null" {
			callIndex = i
			require.Contains(t, string(msg.Calls), `"invoice_1"`)
			require.Contains(t, string(msg.Calls), `"billing_lookup"`)
		}
		if msg.ToolCallID == "invoice_1" {
			resultIndex = i
			require.Contains(t, string(msg.Content), "120")
		}
	}
	require.GreaterOrEqual(t, callIndex, 0)
	require.Equal(t, callIndex+1, resultIndex, "the ordinary call and result must remain paired")
	for _, hidden := range []string{"tool_search_call", "tool_search_output", "search_1", "opaque-search-reasoning", "search reasoning"} {
		require.NotContains(t, string(req.body["messages"]), hidden)
	}
	var tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	require.NoError(t, json.Unmarshal(req.body["tools"], &tools))
	var names []string
	for _, entry := range tools {
		names = append(names, entry.Function.Name)
	}
	require.ElementsMatch(t, expectedTools, names)
	require.NotContains(t, names, "external_lookup", "a run-only external tool is not a Chat tool")
}

func requireReplayedSearchItems(t *testing.T, source, req searchSwitchRequest, expectedCall string) {
	t.Helper()
	require.Equal(t, "/responses", req.path)
	var name string
	require.NoError(t, json.Unmarshal(req.body["model"], &name))
	require.Equal(t, switchTarget, name)
	var input []json.RawMessage
	require.NoError(t, json.Unmarshal(req.body["input"], &input))
	want := []string{switchSearchReasoning, switchSearchCall, expectedCall, switchInvoiceAnswer}
	ids := []string{"rs_search", "ts_search", "fc_invoice", "msg_invoice"}
	var seen []string
	for _, raw := range input {
		var id struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(raw, &id))
		for i := range ids {
			if id.ID == ids[i] {
				require.JSONEq(t, want[i], string(raw), "native item must survive exactly")
				seen = append(seen, id.ID)
			}
		}
	}
	require.Equal(t, ids, seen, "native items must retain IDs and order")
	require.Contains(t, string(req.body["input"]), "tool_search_output")
	require.Contains(t, string(req.body["input"]), "invoice_1")
	require.Contains(t, string(req.body["input"]), "240")

	// The last source request includes the generated search output and the
	// ordinary function result as well as the provider-native response items.
	// All of that history must be the unchanged prefix of the target request.
	conversation := func(raw json.RawMessage) []json.RawMessage {
		var items []json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &items))
		var out []json.RawMessage
		for _, item := range items {
			var meta struct {
				Role string `json:"role"`
			}
			require.NoError(t, json.Unmarshal(item, &meta))
			if meta.Role != "system" && meta.Role != "developer" {
				out = append(out, item)
			}
		}
		return out
	}
	from := conversation(source.body["input"])
	to := conversation(req.body["input"])
	require.GreaterOrEqual(t, len(to), len(from))
	for i := range from {
		require.JSONEq(t, string(from[i]), string(to[i]), "replayed source history item %d", i)
	}
}

func TestRunnerClientToolSearchResponsesChatResponses(t *testing.T) {
	var searches, invoiceRuns atomic.Int32
	invoice := function.NewFunctionTool(func(context.Context, struct {
		ID string `json:"id"`
	}) (string, error) {
		invoiceRuns.Add(1)
		return "120", nil
	}, function.WithName("lookup"), function.WithDescription("Invoice amount."))
	eta := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "soon", nil },
		function.WithName("eta"), function.WithDescription("Shipping ETA."))
	hidden := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "hidden", nil },
		function.WithName("hidden"), function.WithDescription("A filtered tool."))
	external := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "external", nil },
		function.WithName("external_lookup"), function.WithDescription("External search result."))
	handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		searches.Add(1)
		return append(selectSearchCandidates(req, "billing_lookup"), responses.ToolSearchEntry{Tool: external}), nil
	}
	f := newSearchSwitchFixture(t, handler, responses.WithClientToolSearch(handler), []tool.Tool{deferred.Tool(eta), deferred.Tool(hidden)},
		[]tool.ToolSet{deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{invoice}})},
		switchResponsesReply(switchSearchReasoning+","+switchSearchCall), switchResponsesReply(switchInvoiceCall),
		switchResponsesReply(switchInvoiceAnswer), switchChatReply(`{"role":"assistant","content":"240"}`, "stop"),
		switchTextReply("msg_back", "240"))
	require.Equal(t, "120", f.run(t, switchSource, "Find invoice amount."))
	require.EqualValues(t, 1, searches.Load())
	require.EqualValues(t, 1, invoiceRuns.Load())
	events := f.savedEvents(t)
	old, count := snapshotSearchEvents(t, events), len(events)
	var hasBindings bool
	for _, evt := range events {
		if len(evt.Extensions["trpc.provider_action.tool_bindings.v1"]) > 0 {
			hasBindings = true
		}
	}
	require.True(t, hasBindings, "the selection's persisted extension is included in the snapshot")
	require.Equal(t, "240", f.run(t, switchChat, "Double the amount.", agent.WithToolFilter(tool.NewExcludeToolNamesFilter("hidden"))))
	requireSearchEventsUnchanged(t, f, old, count)
	reqs := f.snapshot()
	require.Len(t, reqs, 4)
	requireSearchChatRequest(t, reqs[3], "billing_lookup", "eta")
	require.Equal(t, "240", f.run(t, switchTarget, "What was the doubled amount?"))
	reqs = f.snapshot()
	require.Len(t, reqs, 5)
	requireReplayedSearchItems(t, reqs[2], reqs[4], switchInvoiceCall)
	requireSearchEventsUnchanged(t, f, old, count)
	require.EqualValues(t, 1, searches.Load())
	require.EqualValues(t, 1, invoiceRuns.Load())
}

func TestRunnerClientToolSearchReplacementAcrossModelSwitch(t *testing.T) {
	const searchAgain = `{"type":"tool_search_call","id":"ts_again","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"invoice again"}}`
	const staleCall = `{"type":"function_call","id":"fc_stale","status":"completed","call_id":"stale_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-2\"}"}`
	const freshCall = `{"type":"function_call","id":"fc_fresh","status":"completed","call_id":"fresh_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-3\"}"}`
	var productionRuns, firstSandboxRuns, secondSandboxRuns, searches atomic.Int32
	production := function.NewFunctionTool(func(context.Context, struct {
		ID string `json:"id"`
	}) (string, error) {
		productionRuns.Add(1)
		return "production", nil
	}, function.WithName("lookup"), function.WithDescription("Production invoice."))
	var freshCandidates []string
	var runsAtSearch int32
	handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		which := searches.Add(1)
		if req.CallID == "search_2" {
			freshCandidates = candidateNames(req)
			runsAtSearch = firstSandboxRuns.Load() + secondSandboxRuns.Load()
		}
		selected := selectSearchCandidates(req, "billing_lookup")
		if len(selected) != 1 {
			return nil, fmt.Errorf("billing candidate missing for %s", req.CallID)
		}
		selected[0].Tool = function.NewFunctionTool(func(context.Context, struct {
			ID string `json:"id"`
		}) (string, error) {
			if which == 1 {
				firstSandboxRuns.Add(1)
				return "120", nil
			}
			secondSandboxRuns.Add(1)
			return "fresh sandbox 120", nil
		}, function.WithName("sandbox_lookup"), function.WithDescription("Sandbox invoice."))
		return selected, nil
	}
	f := newSearchSwitchFixture(t, handler, responses.WithClientToolSearch(handler), nil, []tool.ToolSet{deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{production}})},
		switchResponsesReply(switchSearchReasoning+","+switchSearchCall), switchResponsesReply(switchInvoiceCall),
		switchResponsesReply(switchInvoiceAnswer), switchChatReply(`{"role":"assistant","content":"240"}`, "stop"),
		switchTextReply("msg_read", "240"), switchResponsesReply(staleCall), switchResponsesReply(searchAgain),
		switchResponsesReply(freshCall), switchTextReply("msg_done", "done"))
	require.Equal(t, "120", f.run(t, switchSource, "Find the sandbox invoice."))
	require.EqualValues(t, 1, searches.Load())
	require.EqualValues(t, 1, firstSandboxRuns.Load())
	require.Zero(t, secondSandboxRuns.Load())
	require.Zero(t, productionRuns.Load())
	events := f.savedEvents(t)
	old, count := snapshotSearchEvents(t, events), len(events)
	require.Equal(t, "240", f.run(t, switchChat, "Double the amount."))
	reqs := f.snapshot()
	require.Len(t, reqs, 4)
	requireSearchChatRequest(t, reqs[3], "billing_lookup")
	require.Equal(t, "240", f.run(t, switchTarget, "What was the doubled amount?"))
	reqs = f.snapshot()
	require.Len(t, reqs, 5)
	requireReplayedSearchItems(t, reqs[2], reqs[4], switchInvoiceCall)
	require.EqualValues(t, 1, searches.Load(), "reading completed history does not search again")
	require.EqualValues(t, 1, firstSandboxRuns.Load())
	require.Zero(t, secondSandboxRuns.Load())
	requireSearchEventsUnchanged(t, f, old, count)
	require.Equal(t, "done", f.run(t, switchTarget, "Look up a new invoice."))
	reqs = f.snapshot()
	require.Len(t, reqs, 9)
	require.Contains(t, string(reqs[6].body["input"]), "search for it again")
	require.Contains(t, freshCandidates, "billing_lookup", "the completed old search must not block a fresh selection")
	require.EqualValues(t, 1, runsAtSearch, "the expired call executed neither old sandbox nor production")
	require.EqualValues(t, 2, searches.Load())
	require.EqualValues(t, 1, firstSandboxRuns.Load(), "the old sandbox must not execute again")
	require.EqualValues(t, 1, secondSandboxRuns.Load(), "the freshly selected sandbox executes once")
	require.Contains(t, string(reqs[8].body["input"]), "fresh sandbox 120")
	require.Zero(t, productionRuns.Load(), "same-name production must never replace the handler's selected sandbox")
	requireSearchEventsUnchanged(t, f, old, count)
}

func TestRunnerHostedSearchAfterClientReplacement(t *testing.T) {
	const call = `{"type":"function_call","call_id":"target-call","namespace":"billing","name":"lookup","arguments":"{}"}`
	const search = `{"type":"tool_search_call","execution":"server","status":"completed","arguments":{}},`
	const matched = `[{"type":"namespace","name":"billing","tools":[{"type":"function","name":"lookup"}]}]`
	for _, tc := range []struct {
		name, definitions string
		nextTurn          bool
		wantRegistered    int32
	}{
		{"same response", matched, false, 1},
		{"next turn", matched, true, 1},
		{"search miss", "[]", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var searches, sandbox, registered atomic.Int32
			makeLookup := func(counter *atomic.Int32, value string) tool.Tool {
				return function.NewFunctionTool(func(context.Context, struct{}) (string, error) {
					counter.Add(1)
					return value, nil
				}, function.WithName("lookup"), function.WithDescription("Look up an invoice."))
			}
			handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				searches.Add(1)
				selected := selectSearchCandidates(req, "billing_lookup")
				if len(selected) != 1 {
					return nil, fmt.Errorf("missing billing lookup")
				}
				selected[0].Tool = makeLookup(&sandbox, "sandbox")
				return selected, nil
			}
			hosted := search + `{"type":"tool_search_output","execution":"server","status":"completed","tools":` + tc.definitions + `}`
			replies := []switchReply{switchResponsesReply(switchSearchCall), switchResponsesReply(switchInvoiceCall), switchTextReply("msg_a", "done")}
			if tc.nextTurn {
				replies = append(replies, switchResponsesReply(hosted+","+switchInvoiceAnswer), switchResponsesReply(call))
			} else {
				replies = append(replies, switchResponsesReply(hosted+","+call))
			}
			replies = append(replies, switchTextReply("msg_b", "done"))
			f := newSearchSwitchFixture(t, handler, responses.WithHostedToolSearch(), nil, []tool.ToolSet{deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{makeLookup(&registered, "registered")}})}, replies...)
			f.run(t, switchSource, "Look up the sandbox invoice.")
			f.run(t, switchTarget, "Search the hosted catalog.")
			if tc.nextTurn {
				f.run(t, switchTarget, "Call the tool you found.")
			}
			require.EqualValues(t, 1, searches.Load())
			require.EqualValues(t, 1, sandbox.Load())
			require.Equal(t, tc.wantRegistered, registered.Load())
			requests := f.snapshot()
			last := string(requests[len(requests)-1].body["input"])
			if tc.wantRegistered == 0 {
				require.Contains(t, last, "search for it again")
			} else {
				require.Contains(t, last, "registered")
			}
		})
	}
}
