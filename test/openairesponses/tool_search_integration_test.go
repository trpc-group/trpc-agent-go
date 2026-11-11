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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/option"
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
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// searchServer is a scripted Responses endpoint. Each request receives the
// next output array, as JSON or as an SSE stream when the request streams.
type searchServer struct {
	t       *testing.T
	mu      sync.Mutex
	outputs []string
	bodies  []map[string]json.RawMessage
}

func newSearchServer(t *testing.T, outputs ...string) (*searchServer, *httptest.Server) {
	s := &searchServer{t: t, outputs: outputs}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *searchServer) script(outputs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outputs = append(s.outputs, outputs...)
}

func (s *searchServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body := mustReadJSONBody(s.t, r)
	s.mu.Lock()
	index := len(s.bodies)
	s.bodies = append(s.bodies, body)
	var output string
	if index < len(s.outputs) {
		output = s.outputs[index]
	}
	s.mu.Unlock()
	if output == "" {
		http.Error(w, "unexpected model request", http.StatusBadRequest)
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(output)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp := fmt.Sprintf(`{"id":"resp_%d","object":"response","status":"completed","model":"gpt-5","output":%s}`, index, compact.String())
	if string(body["stream"]) == "true" {
		writeSSE(w,
			fmt.Sprintf(`{"type":"response.created","sequence_number":0,"response":{"id":"resp_%d","status":"in_progress","output":[]}}`, index),
			`{"type":"response.completed","sequence_number":1,"response":`+resp+`}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, resp)
}

func (s *searchServer) requests() []map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]json.RawMessage(nil), s.bodies...)
}

type searchToolSet struct {
	name        string
	description string
	tools       []tool.Tool
}

func (s searchToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s searchToolSet) Close() error                      { return nil }
func (s searchToolSet) Name() string                      { return s.name }
func (s searchToolSet) Description() string               { return s.description }

// searchExecutions records which registered executor ran.
type searchExecutions struct {
	mu    sync.Mutex
	calls []string
}

func (e *searchExecutions) tool(name, description, executor string) tool.Tool {
	return function.NewFunctionTool(func(_ context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		e.mu.Lock()
		e.calls = append(e.calls, executor)
		e.mu.Unlock()
		return executor + ":" + in.ID, nil
	}, function.WithName(name), function.WithDescription(description))
}

func (e *searchExecutions) executed() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

// newSearchAgent registers an eager tool, a deferred standalone tool, a
// partly deferred ToolSet, and a fully deferred ToolSet whose member has the
// same name as a member of the first ToolSet.
func newSearchAgent(m model.Model, executions *searchExecutions, stream bool, opts ...llmagent.Option) agent.Agent {
	base := []llmagent.Option{
		llmagent.WithModel(m),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}),
		llmagent.WithTools([]tool.Tool{
			executions.tool("weather", "Current weather.", "weather"),
			executions.tool("eta", "Shipping ETA for an order.", "eta"),
		}),
		llmagent.WithToolSets([]tool.ToolSet{
			searchToolSet{name: "crm", description: "CRM tools for customers and orders.", tools: []tool.Tool{
				executions.tool("lookup", "Look up a customer.", "crm.lookup"),
				executions.tool("orders", "List open orders.", "crm.orders"),
			}},
			searchToolSet{name: "billing", tools: []tool.Tool{
				executions.tool("lookup", "Look up an invoice.", "billing.lookup"),
			}},
		}),
		llmagent.WithDeferredTools(tool.NewIncludeToolNamesFilter("eta")),
		llmagent.WithDeferredToolSet("crm", tool.NewIncludeToolNamesFilter("orders")),
		llmagent.WithDeferredToolSet("billing", nil),
	}
	return llmagent.New("search", append(base, opts...)...)
}

func newSearchModel(srv *httptest.Server, opts ...responses.Option) *responses.Model {
	return responses.New("gpt-5", append([]responses.Option{
		responses.WithAPIKey("test"),
		responses.WithBaseURL(srv.URL),
		responses.WithOpenAIOptions(option.WithMaxRetries(0)),
	}, opts...)...)
}

func runSearch(t *testing.T, r runner.Runner, message model.Message, opts ...agent.RunOption) ([]*event.Event, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := r.Run(ctx, "user", "session", message, opts...)
	require.NoError(t, err)
	var events []*event.Event
	var failures []string
	for evt := range ch {
		events = append(events, evt)
		if evt.Error != nil {
			failures = append(failures, evt.Error.Message)
		}
	}
	require.NoError(t, ctx.Err(), "the runner must finish before the test deadline")
	return events, failures
}

func finalText(events []*event.Event) string {
	var text string
	for _, evt := range events {
		if evt.Response == nil || evt.IsPartial || len(evt.Choices) == 0 {
			continue
		}
		if evt.IsFinalResponse() && evt.Choices[0].Message.Content != "" {
			text = evt.Choices[0].Message.Content
		}
	}
	return text
}

// toolSummary renders request tools as type:name[+defer] entries, with
// namespace members in brackets.
func toolSummary(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var params []struct {
		Type         string `json:"type"`
		Name         string `json:"name"`
		Execution    string `json:"execution"`
		DeferLoading bool   `json:"defer_loading"`
		Tools        []struct {
			Name         string `json:"name"`
			DeferLoading bool   `json:"defer_loading"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(raw, &params))
	out := make([]string, 0, len(params))
	for _, p := range params {
		entry := p.Type
		if p.Name != "" {
			entry += ":" + p.Name
		}
		if p.Execution != "" {
			entry += ":" + p.Execution
		}
		if p.DeferLoading {
			entry += "+defer"
		}
		if p.Type == "namespace" {
			members := make([]string, 0, len(p.Tools))
			for _, member := range p.Tools {
				name := member.Name
				if member.DeferLoading {
					name += "+defer"
				}
				members = append(members, name)
			}
			entry += "[" + strings.Join(members, ",") + "]"
		}
		out = append(out, entry)
	}
	return out
}

type inputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	CallID    string          `json:"call_id"`
	Namespace string          `json:"namespace"`
	Name      string          `json:"name"`
	Execution string          `json:"execution"`
	Tools     json.RawMessage `json:"tools"`
}

func inputItems(t *testing.T, raw json.RawMessage) []inputItem {
	t.Helper()
	var items []inputItem
	require.NoError(t, json.Unmarshal(raw, &items))
	return items
}

// inputTypes renders conversation input items as type[:role] entries,
// skipping the agent instruction.
func inputTypes(items []inputItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Role == "system" || item.Role == "developer" {
			continue
		}
		entry := item.Type
		if entry == "" {
			entry = "message"
		}
		if item.Role != "" {
			entry += ":" + item.Role
		}
		out = append(out, entry)
	}
	return out
}

// conversationItems drops the agent instruction so indexes match inputTypes.
func conversationItems(t *testing.T, raw json.RawMessage) []inputItem {
	t.Helper()
	var out []inputItem
	for _, item := range inputItems(t, raw) {
		if item.Role != "system" && item.Role != "developer" {
			out = append(out, item)
		}
	}
	return out
}

const hostedSearchRound = `[
  {"type":"tool_search_call","id":"ts_1","execution":"server","call_id":null,"status":"completed","arguments":{"paths":["billing"]}},
  {"type":"tool_search_output","id":"tso_1","execution":"server","call_id":null,"status":"completed","tools":[
    {"type":"namespace","name":"billing","description":"Tools from the billing tool set.","tools":[
      {"type":"function","name":"lookup","description":"Look up an invoice.","defer_loading":true,"parameters":{"type":"object"}}]}]},
  {"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-7\"}"}]`

const clientSearchRound = `[
  {"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque"},
  {"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"invoice status"}}]`

const loadedBillingCall = `[
  {"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-7\"}"}]`

func TestRunnerToolSearchAfterModelEditsNamespacedCall(t *testing.T) {
	for _, nameMode := range []tool.ToolSetToolNameMode{tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%v/stream=%v", nameMode, stream), func(t *testing.T) {
				server, srv := newSearchServer(t, hostedSearchRound, messageOutput("msg_1", "updated invoice"))
				callbacks := model.NewCallbacks().RegisterAfterModel(func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					if args.Response != nil && !args.Response.IsPartial && len(args.Response.Choices) > 0 {
						data := args.Response.Choices[0].Message.ProviderData
						if data != nil {
							data.Payload = bytes.ReplaceAll(data.Payload, []byte("inv-7"), []byte("inv-9"))
						}
					}
					return nil, nil
				})
				executions := &searchExecutions{}
				m := newSearchModel(srv, responses.WithHostedToolSearch())
				agt := newSearchAgent(m, executions, stream,
					llmagent.WithToolSetToolNameMode("billing", nameMode), llmagent.WithModelCallbacks(callbacks))
				r := runner.NewRunner("search", agt)
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("look up invoice"))
				require.Empty(t, failures)
				require.Equal(t, "updated invoice", finalText(events))
				require.Equal(t, []string{"billing.lookup"}, executions.executed())
				requests := server.requests()
				require.Len(t, requests, 2)
				require.Contains(t, string(requests[1]["input"]), "billing.lookup:inv-9",
					"the edited arguments reach the correct executor and its result reaches the model")
				require.NotContains(t, string(requests[1]["input"]), "inv-7")
			})
		}
	}
}

func TestRunnerHostedToolSearch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			server, srv := newSearchServer(t, hostedSearchRound, messageOutput("msg_1", "invoice paid"))
			executions := &searchExecutions{}
			var toolNames []string
			callbacks := tool.NewCallbacks().RegisterBeforeTool(func(_ context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
				toolNames = append(toolNames, args.ToolName)
				return nil, nil
			})
			path := filepath.Join(t.TempDir(), "hosted.db")
			svc := openNativeRoutingSQLite(t, path)
			m := newSearchModel(srv, responses.WithHostedToolSearch())
			r := runner.NewRunner("search", newSearchAgent(m, executions, stream, llmagent.WithToolCallbacks(callbacks)),
				runner.WithSessionService(svc))
			events, failures := runSearch(t, r, model.NewUserMessage("is invoice inv-7 paid?"))
			require.Empty(t, failures)
			require.Equal(t, "invoice paid", finalText(events))
			require.Equal(t, []string{"billing.lookup"}, executions.executed(),
				"a namespaced call runs the member of that namespace only")
			require.Equal(t, []string{"billing_lookup"}, toolNames, "tool callbacks see the registered tool name")

			requests := server.requests()
			require.Len(t, requests, 2)
			wantTools := []string{
				"namespace:billing[lookup+defer]",
				"namespace:crm[lookup,orders+defer]",
				"function:eta+defer",
				"function:weather",
				"tool_search",
			}
			require.Equal(t, wantTools, toolSummary(t, requests[0]["tools"]))
			require.Equal(t, wantTools, toolSummary(t, requests[1]["tools"]))
			second := conversationItems(t, requests[1]["input"])
			require.Equal(t, []string{"message:user", "tool_search_call", "tool_search_output", "function_call", "function_call_output"},
				inputTypes(second), "native items are replayed in their original order")
			require.Equal(t, "billing", second[3].Namespace, "the namespace of the call is preserved")
			require.Equal(t, "call_1", second[4].CallID)
			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())

			// A reopened session continues with the same native history.
			server.script(messageOutput("msg_2", "still paid"))
			svc = openNativeRoutingSQLite(t, path)
			defer svc.Close()
			r = runner.NewRunner("search", newSearchAgent(m, executions, stream), runner.WithSessionService(svc))
			defer r.Close()
			events, failures = runSearch(t, r, model.NewUserMessage("and now?"))
			require.Empty(t, failures)
			require.Equal(t, "still paid", finalText(events))
			third := conversationItems(t, server.requests()[2]["input"])
			require.Equal(t, []string{
				"message:user", "tool_search_call", "tool_search_output", "function_call", "function_call_output",
				"message:assistant", "message:user",
			}, inputTypes(third))
		})
	}
}

func TestRunnerClientToolSearch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			server, srv := newSearchServer(t, clientSearchRound, loadedBillingCall, messageOutput("msg_1", "invoice paid"))
			executions := &searchExecutions{}
			var searches []*responses.ToolSearchRequest
			handler := func(_ context.Context, req *responses.ToolSearchRequest) (*responses.ToolSearchResult, error) {
				searches = append(searches, req)
				if len(searches) == 1 {
					return &responses.ToolSearchResult{ToolIDs: []string{"billing_lookup"}}, nil
				}
				return &responses.ToolSearchResult{}, nil
			}
			var pendingSearches int
			modelCallbacks := model.NewCallbacks().RegisterAfterModel(
				func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
					if args.Response != nil && !args.Response.Done && !args.Response.IsPartial {
						pendingSearches++
					}
					return nil, nil
				})
			path := filepath.Join(t.TempDir(), "client.db")
			svc := openNativeRoutingSQLite(t, path)
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			r := runner.NewRunner("search", newSearchAgent(m, executions, stream, llmagent.WithModelCallbacks(modelCallbacks)),
				runner.WithSessionService(svc))
			events, failures := runSearch(t, r, model.NewUserMessage("is invoice inv-7 paid?"))
			require.Empty(t, failures)
			require.Equal(t, "invoice paid", finalText(events))
			require.Equal(t, []string{"billing.lookup"}, executions.executed())
			require.Equal(t, 1, pendingSearches, "model callbacks observe the search response")

			require.Len(t, searches, 1)
			require.Equal(t, "search_1", searches[0].CallID)
			require.Equal(t, "invoice status", searches[0].Query)
			require.Equal(t, []string{"billing_lookup", "crm_orders", "eta"}, candidateIDs(searches[0]))
			require.Equal(t, "billing", searches[0].Candidates[0].Namespace)
			require.Equal(t, "lookup", searches[0].Candidates[0].Name)
			require.Equal(t, "Look up an invoice.", searches[0].Candidates[0].Declaration.Description)

			var searchResults int
			for _, evt := range events {
				if evt.Response != nil && evt.IsToolResultResponse() &&
					evt.Choices[0].Message.ToolName == responses.ToolSearchToolName {
					searchResults++
					require.Equal(t, "search_1", evt.Choices[0].Message.ToolID)
				}
			}
			require.Equal(t, 1, searchResults, "the search result is published as a tool result event")

			requests := server.requests()
			require.Len(t, requests, 3)
			wantTools := []string{"namespace:crm[lookup]", "function:weather", "tool_search:client"}
			for i, request := range requests {
				require.Equal(t, wantTools, toolSummary(t, request["tools"]),
					"request %d keeps the same top-level tools; loaded definitions stay in history", i)
			}
			second := conversationItems(t, requests[1]["input"])
			require.Equal(t, []string{"message:user", "reasoning", "tool_search_call", "tool_search_output"}, inputTypes(second))
			require.Equal(t, "search_1", second[3].CallID)
			require.Equal(t, "client", second[3].Execution)
			require.Equal(t, []string{"namespace:billing[lookup+defer]"}, toolSummary(t, second[3].Tools))
			third := conversationItems(t, requests[2]["input"])
			require.Equal(t, []string{
				"message:user", "reasoning", "tool_search_call", "tool_search_output", "function_call", "function_call_output",
			}, inputTypes(third))
			require.Equal(t, "billing", third[4].Namespace)
			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())

			// After a reload, loaded tools stay loaded and are not offered again.
			server.script(`[{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"eta"}}]`,
				messageOutput("msg_2", "nothing else"))
			svc = openNativeRoutingSQLite(t, path)
			defer svc.Close()
			r = runner.NewRunner("search", newSearchAgent(m, executions, stream), runner.WithSessionService(svc))
			defer r.Close()
			events, failures = runSearch(t, r, model.NewUserMessage("anything else?"))
			require.Empty(t, failures)
			require.Equal(t, "nothing else", finalText(events))
			require.Len(t, searches, 2)
			require.Equal(t, []string{"crm_orders", "eta"}, candidateIDs(searches[1]))
			fifth := conversationItems(t, server.requests()[4]["input"])
			require.Equal(t, []string{
				"message:user", "reasoning", "tool_search_call", "tool_search_output", "function_call", "function_call_output",
				"message:assistant", "message:user", "tool_search_call", "tool_search_output",
			}, inputTypes(fifth))
			require.JSONEq(t, `[]`, string(fifth[9].Tools), "an empty search result loads nothing")
		})
	}
}

func candidateIDs(req *responses.ToolSearchRequest) []string {
	out := make([]string, len(req.Candidates))
	for i, candidate := range req.Candidates {
		out[i] = candidate.ID
	}
	return out
}

func TestRunnerToolSearchHonorsToolFilters(t *testing.T) {
	t.Run("client candidates", func(t *testing.T) {
		_, srv := newSearchServer(t, clientSearchRound, messageOutput("msg_1", "done"))
		var candidates []string
		handler := func(_ context.Context, req *responses.ToolSearchRequest) (*responses.ToolSearchResult, error) {
			candidates = candidateIDs(req)
			return &responses.ToolSearchResult{}, nil
		}
		r := runner.NewRunner("search", newSearchAgent(newSearchModel(srv, responses.WithClientToolSearch(handler)), &searchExecutions{}, false,
			llmagent.WithToolFilter(tool.NewExcludeToolNamesFilter("eta"))))
		defer r.Close()
		_, failures := runSearch(t, r, model.NewUserMessage("hi"),
			agent.WithToolFilter(tool.NewExcludeToolNamesFilter("crm_orders")))
		require.Empty(t, failures)
		require.Equal(t, []string{"billing_lookup"}, candidates,
			"agent and run filters remove tools from the search candidates")
	})

	t.Run("hosted exposure and execution", func(t *testing.T) {
		server, srv := newSearchServer(t,
			`[{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","namespace":"billing","name":"lookup","arguments":"{}"}]`,
			messageOutput("msg_1", "done"))
		executions := &searchExecutions{}
		r := runner.NewRunner("search", newSearchAgent(newSearchModel(srv, responses.WithHostedToolSearch()), executions, false))
		defer r.Close()
		_, failures := runSearch(t, r, model.NewUserMessage("hi"),
			agent.WithToolFilter(tool.NewExcludeToolNamesFilter("crm_orders", "billing_lookup")))
		require.Empty(t, failures)
		requests := server.requests()
		require.Equal(t, []string{"function:crm_lookup", "function:eta+defer", "function:weather", "tool_search"},
			toolSummary(t, requests[0]["tools"]), "a ToolSet without visible deferred members is not grouped")
		require.Empty(t, executions.executed(), "a filtered tool is not executed")
		second := conversationItems(t, requests[1]["input"])
		require.Equal(t, "function_call_output", second[len(second)-1].Type)
	})
}

func TestRunnerToolSearchIsScopedToTheAgent(t *testing.T) {
	server, srv := newSearchServer(t,
		`[{"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"refund"}}]`,
		messageOutput("msg_1", "refund agent done"),
		messageOutput("msg_2", "plain agent done"))
	var candidates []string
	handler := func(_ context.Context, req *responses.ToolSearchRequest) (*responses.ToolSearchResult, error) {
		candidates = candidateIDs(req)
		return &responses.ToolSearchResult{}, nil
	}
	shared := newSearchModel(srv, responses.WithClientToolSearch(handler))
	executions := &searchExecutions{}
	refunds := llmagent.New("refunds",
		llmagent.WithModel(shared),
		llmagent.WithTools([]tool.Tool{executions.tool("refund", "Issue a refund.", "refund")}),
		llmagent.WithDeferredTools(nil),
	)
	plain := llmagent.New("plain",
		llmagent.WithModel(shared),
		llmagent.WithTools([]tool.Tool{executions.tool("weather", "Current weather.", "weather")}),
	)
	first := runner.NewRunner("refunds", refunds)
	defer first.Close()
	_, failures := runSearch(t, first, model.NewUserMessage("refund"))
	require.Empty(t, failures)
	require.Equal(t, []string{"refund"}, candidates, "candidates come only from the calling agent")
	second := runner.NewRunner("plain", plain)
	defer second.Close()
	events, failures := runSearch(t, second, model.NewUserMessage("weather"))
	require.Empty(t, failures)
	require.Equal(t, "plain agent done", finalText(events))
	requests := server.requests()
	require.Len(t, requests, 3)
	require.Equal(t, []string{"function:weather"}, toolSummary(t, requests[2]["tools"]),
		"an agent without deferred tools sends an unchanged request")
}

func TestRunnerClientToolSearchResumeAfterFirstResultPersisted(t *testing.T) {
	batch := `[
	{"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"invoice"}},
	{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"orders"}}]`
	server, srv := newSearchServer(t, batch, loadedBillingCall, messageOutput("msg_1", "invoice paid"))
	var mu sync.Mutex
	handled := map[string]int{}
	handler := func(_ context.Context, req *responses.ToolSearchRequest) (*responses.ToolSearchResult, error) {
		mu.Lock()
		handled[req.CallID]++
		mu.Unlock()
		id := "billing_lookup"
		if req.CallID == "search_2" {
			id = "crm_orders"
		}
		return &responses.ToolSearchResult{ToolIDs: []string{id}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "partial-search.db")
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
	m := newSearchModel(srv, responses.WithClientToolSearch(handler))
	executions := &searchExecutions{}
	r := runner.NewRunner("search", newSearchAgent(m, executions, false), runner.WithSessionService(svc))
	ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("invoice status"))
	require.NoError(t, err)
	for range ch {
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NoError(t, r.Close())
	require.NoError(t, svc.Close())
	require.Len(t, server.requests(), 1)

	svc = openNativeRoutingSQLite(t, path)
	defer svc.Close()
	sess, err := svc.GetSession(context.Background(), session.Key{AppName: "search", UserID: "user", SessionID: "session"})
	require.NoError(t, err)
	var saved []string
	for _, evt := range sess.Events {
		if evt.Response != nil && len(evt.Choices) > 0 && evt.Choices[0].Message.Role == model.RoleTool {
			saved = append(saved, evt.Choices[0].Message.ToolID)
		}
	}
	require.Equal(t, []string{"search_1"}, saved, "only a prefix survived cancellation")
	r = runner.NewRunner("search", newSearchAgent(m, executions, false), runner.WithSessionService(svc))
	defer r.Close()
	events, failures := runSearch(t, r, model.Message{}, agent.WithResume(true))
	require.Empty(t, failures)
	require.Equal(t, "invoice paid", finalText(events))
	mu.Lock()
	require.Equal(t, map[string]int{"search_1": 1, "search_2": 2}, handled,
		"only the handler whose output was not stored runs again")
	mu.Unlock()
	require.Equal(t, []string{"billing.lookup"}, executions.executed())
	require.Len(t, server.requests(), 3)
}

func TestRunnerClientToolSearchFailureAndResume(t *testing.T) {
	for _, failure := range []string{"error", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			server, srv := newSearchServer(t, clientSearchRound, loadedBillingCall, messageOutput("msg_1", "invoice paid"))
			executions := &searchExecutions{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			recorded := make(chan struct{})
			var recordOnce sync.Once
			var handlerCalls int
			failuresBeforeSuccess := 1
			if failure == "error" {
				failuresBeforeSuccess = 2
			}
			handler := func(handlerCtx context.Context, req *responses.ToolSearchRequest) (*responses.ToolSearchResult, error) {
				handlerCalls++
				if handlerCalls > failuresBeforeSuccess {
					return &responses.ToolSearchResult{ToolIDs: []string{"billing_lookup"}}, nil
				}
				if failure == "cancel" {
					// Cancel only after the search call is persisted, so the
					// cancellation lands inside the search.
					<-recorded
					cancel()
					<-handlerCtx.Done()
					return nil, handlerCtx.Err()
				}
				return nil, errors.New("search index unavailable")
			}
			path := filepath.Join(t.TempDir(), "resume.db")
			db, err := sql.Open("sqlite3", path)
			require.NoError(t, err)
			svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false),
				sqlite.WithAppendEventHook(func(c *session.AppendEventContext, next func() error) error {
					err := next()
					if err == nil && c.Event.Response != nil && !c.Event.Done && len(c.Event.Choices) > 0 &&
						c.Event.Choices[0].Message.ProviderData != nil {
						recordOnce.Do(func() { close(recorded) })
					}
					return err
				}))
			require.NoError(t, err)
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			r := runner.NewRunner("search", newSearchAgent(m, executions, false), runner.WithSessionService(svc))
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("is invoice inv-7 paid?"))
			require.NoError(t, err)
			var failures []string
			for evt := range ch {
				if evt.Error != nil {
					failures = append(failures, evt.Error.Message)
				}
			}
			if failure == "error" {
				require.Len(t, failures, 1)
				require.Contains(t, failures[0], "search index unavailable")
			} else {
				require.ErrorIs(t, ctx.Err(), context.Canceled)
				for _, message := range failures {
					require.NotContains(t, message, "complete provider actions",
						"cancellation is not reported as a search failure")
				}
			}
			require.Len(t, server.requests(), 1, "a failed search does not reach the model again")
			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())

			for attempt := 1; attempt < failuresBeforeSuccess; attempt++ {
				svc = openNativeRoutingSQLite(t, path)
				r = runner.NewRunner("search", newSearchAgent(m, executions, false), runner.WithSessionService(svc))
				_, failures = runSearch(t, r, model.Message{}, agent.WithResume(true))
				require.Len(t, failures, 1)
				require.Contains(t, failures[0], "search index unavailable")
				require.Equal(t, attempt+1, handlerCalls)
				require.Len(t, server.requests(), 1, "failed recovery does not repeat the model request")
				require.NoError(t, r.Close())
				require.NoError(t, svc.Close())
			}

			svc = openNativeRoutingSQLite(t, path)
			defer svc.Close()
			sess, err := svc.GetSession(context.Background(), session.Key{AppName: "search", UserID: "user", SessionID: "session"})
			require.NoError(t, err)
			var unanswered int
			for _, evt := range sess.Events {
				if evt.Response == nil || len(evt.Choices) == 0 {
					continue
				}
				require.NotEqual(t, model.RoleTool, evt.Choices[0].Message.Role, "a failed search records no output")
				if evt.Choices[0].Message.ProviderData != nil && !evt.Done {
					unanswered++
				}
			}
			require.Equal(t, 1, unanswered, "the search call is persisted unanswered")

			r = runner.NewRunner("search", newSearchAgent(m, executions, false), runner.WithSessionService(svc))
			defer r.Close()
			events, failures := runSearch(t, r, model.Message{}, agent.WithResume(true))
			require.Empty(t, failures)
			require.Equal(t, "invoice paid", finalText(events))
			require.Equal(t, failuresBeforeSuccess+1, handlerCalls, "each resume retries the unanswered search")
			require.Equal(t, []string{"billing.lookup"}, executions.executed())
			second := conversationItems(t, server.requests()[1]["input"])
			require.Equal(t, []string{"message:user", "reasoning", "tool_search_call", "tool_search_output"}, inputTypes(second),
				"the failure is not replayed between the call and its output")
		})
	}
}

func TestRunnerMixedClientSearchAndFunctionCall(t *testing.T) {
	search := `{"type":"tool_search_call","id":"ts_mixed","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"invoice status"}}`
	function := `{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","name":"weather","arguments":"{\"id\":\"city\"}"}`
	for _, searchFirst := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("searchFirst=%v/stream=%v", searchFirst, stream), func(t *testing.T) {
				output := "[" + function + "," + search + "]"
				callTypes := []string{"function_call", "tool_search_call"}
				if searchFirst {
					output = "[" + search + "," + function + "]"
					callTypes = []string{"tool_search_call", "function_call"}
				}
				server, srv := newSearchServer(t, output, loadedBillingCall, messageOutput("msg_done", "done"))
				executions := &searchExecutions{}
				var searches int
				handler := func(context.Context, *responses.ToolSearchRequest) (*responses.ToolSearchResult, error) {
					searches++
					return &responses.ToolSearchResult{ToolIDs: []string{"billing_lookup"}}, nil
				}
				r := runner.NewRunner("search", newSearchAgent(
					newSearchModel(srv, responses.WithClientToolSearch(handler)), executions, stream))
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("weather and invoice status"))
				require.Empty(t, failures)
				require.Equal(t, "done", finalText(events))
				require.Equal(t, 1, searches)
				require.Equal(t, []string{"weather", "billing.lookup"}, executions.executed())
				requests := server.requests()
				require.Len(t, requests, 3)
				second := conversationItems(t, requests[1]["input"])
				expected := append([]string{"message:user"}, callTypes...)
				expected = append(expected, "tool_search_output", "function_call_output")
				require.Equal(t, expected, inputTypes(second))
				require.Equal(t, "search_1", second[3].CallID)
				require.Contains(t, string(second[3].Tools), "lookup", "the loaded schema reaches the model")
				require.Equal(t, "weather_1", second[4].CallID)
				third := conversationItems(t, requests[2]["input"])
				require.Equal(t, second, third[:len(second)], "the complete mixed round survives later history preparation")
			})
		}
	}
}
