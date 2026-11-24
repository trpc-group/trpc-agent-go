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
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestRunnerMixedSearchContinuationAfterFunctionInterrupted(t *testing.T) {
	for _, namespaced := range []bool{false, true} {
		t.Run(fmt.Sprintf("namespaced=%v", namespaced), func(t *testing.T) {
			call := weatherCallItem
			if namespaced {
				call = `{"type":"function_call","id":"fc_lookup","status":"completed","call_id":"lookup_1","namespace":"crm","name":"lookup","arguments":"{\"id\":\"customer\"}"}`
			}
			callID := "weather_1"
			if namespaced {
				callID = "lookup_1"
			}
			server, srv := newSearchServer(t, "["+call+","+clientSearchItem+"]", messageOutput("msg_done", "done"))
			var searches, functions atomic.Int32
			handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				searches.Add(1)
				return selectSearchCandidates(req, "billing_lookup"), nil
			}
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			started := make(chan struct{})
			var startOnce sync.Once
			block := func(name string) tool.Tool {
				return function.NewFunctionTool(func(ctx context.Context, _ struct {
					ID string `json:"id"`
				}) (string, error) {
					functions.Add(1)
					startOnce.Do(func() { close(started) })
					<-ctx.Done()
					return "", ctx.Err()
				}, function.WithName(name), function.WithDescription("Block until cancellation."))
			}
			executions := &searchExecutions{}
			newAgent := func() agent.Agent {
				return llmagent.New("search",
					llmagent.WithModel(m),
					llmagent.WithTools([]tool.Tool{block("weather")}),
					llmagent.WithToolSets([]tool.ToolSet{
						deferred.ToolSet(searchToolSet{name: "crm", tools: []tool.Tool{
							block("lookup"), executions.tool("orders", "List orders.", "orders"),
						}}, deferred.WithFilter(tool.NewIncludeToolNamesFilter("orders"))),
						deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{
							executions.tool("lookup", "Look up an invoice.", "billing.lookup"),
						}}),
					}),
				)
			}
			path := filepath.Join(t.TempDir(), "mixed.db")
			db, err := sql.Open("sqlite3", path)
			require.NoError(t, err)
			searchSaved := make(chan struct{})
			var saveOnce sync.Once
			svc, err := sqlite.NewService(db, sqlite.WithEnableAsyncPersist(false),
				sqlite.WithAppendEventHook(func(c *session.AppendEventContext, next func() error) error {
					err := next()
					if err == nil && c.Event.Response != nil && len(c.Event.Choices) > 0 {
						msg := c.Event.Choices[0].Message
						if msg.Role == model.RoleTool && msg.ToolID == "search_1" {
							saveOnce.Do(func() { close(searchSaved) })
						}
					}
					return err
				}))
			require.NoError(t, err)
			r := runner.NewRunner("search", newAgent(), runner.WithSessionService(svc))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ch, err := r.Run(ctx, "user", "session", model.NewUserMessage("search and run a function"))
			require.NoError(t, err)
			drained := make(chan struct{})
			go func() {
				for range ch {
				}
				close(drained)
			}()
			for _, ready := range []<-chan struct{}{searchSaved, started} {
				select {
				case <-ready:
				case <-ctx.Done():
					t.Fatal("search output and function start were not observed")
				}
			}
			cancel()
			select {
			case <-drained:
			case <-time.After(10 * time.Second):
				t.Fatal("interrupted run did not finish")
			}
			require.NoError(t, r.Close())
			require.NoError(t, svc.Close())

			svc = openNativeRoutingSQLite(t, path)
			defer svc.Close()
			key := session.Key{AppName: "search", UserID: "user", SessionID: "session"}
			stored, err := svc.GetSession(context.Background(), key)
			require.NoError(t, err)
			var searchPayload json.RawMessage
			for _, evt := range stored.Events {
				if evt.Response == nil || len(evt.Choices) == 0 {
					continue
				}
				msg := evt.Choices[0].Message
				if msg.Role == model.RoleTool && msg.ToolID == "search_1" {
					require.NotNil(t, msg.ProviderData)
					searchPayload = append(json.RawMessage(nil), msg.ProviderData.Payload...)
				}
				require.False(t, msg.Role == model.RoleTool && msg.ToolID == callID,
					"the interrupted function has no stored result")
			}
			require.NotEmpty(t, searchPayload)
			r = runner.NewRunner("search", newAgent(), runner.WithSessionService(svc))
			defer r.Close()
			events, failures := runSearch(t, r, model.NewUserMessage("continue without rerunning"))
			require.Empty(t, failures)
			require.Equal(t, "done", finalText(events))
			require.EqualValues(t, 1, searches.Load(), "the saved search must not run again")
			require.EqualValues(t, 1, functions.Load(), "the interrupted function must not run again")
			requests := server.requests()
			require.Len(t, requests, 2)
			var input []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(requests[1]["input"], &input))
			var savedOutput []json.RawMessage
			require.NoError(t, json.Unmarshal(searchPayload, &savedOutput))
			require.Len(t, savedOutput, 1)
			var searchOutputs, functionOutputs int
			for _, item := range input {
				var kind, id string
				_ = json.Unmarshal(item["type"], &kind)
				_ = json.Unmarshal(item["call_id"], &id)
				switch kind {
				case "tool_search_output":
					searchOutputs++
					require.Equal(t, "search_1", id)
					require.JSONEq(t, string(savedOutput[0]), stringMustJSON(t, item))
				case "function_call_output":
					functionOutputs++
					require.Equal(t, callID, id)
					var output string
					require.NoError(t, json.Unmarshal(item["output"], &output))
					require.Contains(t, output, "outcome is unknown")
				}
			}
			require.Equal(t, 1, searchOutputs)
			require.Equal(t, 1, functionOutputs)
			stored, err = svc.GetSession(context.Background(), key)
			require.NoError(t, err)
			for _, evt := range stored.Events {
				if evt.Response != nil && len(evt.Choices) > 0 {
					msg := evt.Choices[0].Message
					require.False(t, msg.Role == model.RoleTool && msg.ToolID == callID,
						"the placeholder must not be persisted")
				}
			}
		})
	}
}

func TestRunnerMixedSearchMissingResultBehavior(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%v", resume), func(t *testing.T) {
			server, srv := newSearchServer(t, "["+weatherCallItem+","+clientSearchItem+"]", messageOutput("msg_done", "done"))
			executions := &searchExecutions{}
			var searches int
			handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				searches++
				if searches == 1 {
					return nil, errors.New("search index unavailable")
				}
				return selectSearchCandidates(req, "billing_lookup"), nil
			}
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			path := filepath.Join(t.TempDir(), "missing-search.db")
			run := func(msg model.Message, opts ...agent.RunOption) ([]string, string) {
				svc := openNativeRoutingSQLite(t, path)
				defer svc.Close()
				r := runner.NewRunner("search", newSearchAgent(m, executions, false), runner.WithSessionService(svc))
				defer r.Close()
				events, failures := runSearch(t, r, msg, opts...)
				return failures, finalText(events)
			}
			failures, _ := run(model.NewUserMessage("weather and invoice status"))
			require.Len(t, failures, 1)
			require.Contains(t, failures[0], "search index unavailable")
			require.Empty(t, executions.executed())
			require.Len(t, server.requests(), 1)
			if resume {
				failures, answer := run(model.Message{}, agent.WithResume(true))
				require.Empty(t, failures)
				require.Equal(t, "done", answer)
				require.Equal(t, 2, searches)
				require.Equal(t, []string{"weather"}, executions.executed())
				require.Len(t, server.requests(), 2)
				require.Equal(t, []string{"message:user", "function_call", "tool_search_call", "tool_search_output", "function_call_output"},
					inputTypes(conversationItems(t, server.requests()[1]["input"])))
			} else {
				failures, _ = run(model.NewUserMessage("continue"))
				require.NotEmpty(t, failures)
				require.Contains(t, failures[0], `client tool_search_call "search_1" has no tool_search_output`)
				require.Equal(t, 1, searches, "a new user turn does not rerun a failed search")
				require.Empty(t, executions.executed())
				require.Len(t, server.requests(), 1, "the unanswered search must fail before dispatch")
			}
		})
	}
}
