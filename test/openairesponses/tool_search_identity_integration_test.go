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
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

// searchPublicModelWrapper exposes only documented model interfaces, so no
// concrete adapter methods are promoted onto the wrapper's method set.
type searchPublicModelWrapper struct {
	model.Model
	model.ProviderActionCompleter
	model.ProviderDataRequestProjector
}

func TestRunnerClientToolSearchKeepsDottedAndNamespacedIdentitiesDistinct(t *testing.T) {
	const calls = `[
		{"type":"function_call","id":"fc_namespace","status":"completed","call_id":"namespace_1","namespace":"crm","name":"orders","arguments":"{\"id\":\"registered\"}"},
		{"type":"function_call","id":"fc_literal","status":"completed","call_id":"literal_1","name":"crm.orders","arguments":"{\"id\":\"external\"}"}
	]`
	const searchAgain = `[{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"another order tool"}}]`
	for _, flow := range []struct {
		name    string
		batches [][]string
	}{
		{name: "same_search", batches: [][]string{{"namespace", "literal"}}},
		{name: "namespace_then_literal", batches: [][]string{{"namespace"}, {"literal"}}},
		{name: "literal_then_namespace", batches: [][]string{{"literal"}, {"namespace"}}},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", flow.name, stream), func(t *testing.T) {
				outputs := []string{clientSearchRound}
				if len(flow.batches) > 1 {
					outputs = append(outputs, searchAgain)
				}
				outputs = append(outputs, calls, messageOutput("msg_done", "done"))
				server, srv := newSearchServer(t, outputs...)
				executions := &searchExecutions{}
				var candidates [][]string
				handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					candidates = append(candidates, candidateNames(req))
					if len(candidates) > len(flow.batches) {
						return nil, fmt.Errorf("unexpected search call %q", req.CallID)
					}
					var selected []responses.ToolSearchEntry
					for _, identity := range flow.batches[len(candidates)-1] {
						if identity == "namespace" {
							registered := selectSearchCandidates(req, "crm_orders")
							if len(registered) != 1 {
								return nil, fmt.Errorf("expected one crm orders candidate, got %d", len(registered))
							}
							selected = append(selected, registered...)
						} else {
							selected = append(selected, responses.ToolSearchEntry{
								Name: "crm.orders", Tool: executions.tool("literal_orders", "External order lookup.", "literal"),
							})
						}
					}
					return selected, nil
				}
				r := runner.NewRunner("search", newSearchAgent(newSearchModel(srv, responses.WithClientToolSearch(handler)), executions, stream))
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("use the registered and external order tools"))
				require.Empty(t, failures)
				require.Equal(t, "done", finalText(events))
				require.ElementsMatch(t, []string{"crm.orders", "literal"}, executions.executed(),
					"each protocol identity reaches its own executor regardless of which search loaded it")
				requests := server.requests()
				require.Len(t, requests, len(outputs))
				requireAppendOnlySearchRequests(t, requests)
				finalInput := string(requests[len(requests)-1]["input"])
				require.Contains(t, finalInput, "crm.orders:registered")
				require.Contains(t, finalInput, "literal:external")
				if flow.name == "same_search" {
					require.Len(t, candidates, 1)
					second := conversationItems(t, requests[1]["input"])
					require.ElementsMatch(t, []string{"namespace:crm[orders+defer]", "function:crm.orders+defer"},
						toolSummary(t, second[len(second)-1].Tools), "both schemas retain their distinct protocol identities")
				} else {
					require.Len(t, candidates, 2)
					if flow.name == "namespace_then_literal" {
						require.NotContains(t, candidates[1], "crm_orders")
					} else {
						require.Contains(t, candidates[1], "crm_orders", "the dotted external name does not load the namespace member")
					}
				}
			})
		}
	}
}

func TestRunnerClientToolSearchRediscoversNewDeferredRegistrationAfterExternalRun(t *testing.T) {
	const externalCall = `[{"type":"function_call","id":"fc_external","status":"completed","call_id":"external_1","namespace":"billing","name":"lookup","arguments":"{\"sandbox_id\":\"inv-7\"}"}]`
	const obsoleteCall = `[{"type":"function_call","id":"fc_obsolete","status":"completed","call_id":"obsolete_1","namespace":"billing","name":"lookup","arguments":"{\"sandbox_id\":\"inv-8\"}"}]`
	const rediscover = `[{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"find the invoice tool again"}}]`
	const registeredCall = `[{"type":"function_call","id":"fc_registered","status":"completed","call_id":"registered_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-9\"}"}]`
	const searchLoaded = `[{"type":"tool_search_call","id":"ts_3","execution":"client","call_id":"search_3","status":"completed","arguments":{"query":"find another invoice tool"}}]`
	for _, wrapped := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("wrapper=%v/stream=%v", wrapped, stream), func(t *testing.T) {
				server, srv := newSearchServer(t, clientSearchRound, externalCall, messageOutput("msg_external", "external done"),
					obsoleteCall, rediscover, registeredCall, searchLoaded, messageOutput("msg_registered", "registered done"))
				executions := &searchExecutions{}
				var candidates [][]string
				var executionsAtRediscovery []string
				handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					candidates = append(candidates, candidateNames(req))
					switch req.CallID {
					case "search_1":
						return []responses.ToolSearchEntry{{Namespace: "billing", Name: "lookup",
							Tool: executions.sandboxTool("external_lookup", "external")}}, nil
					case "search_2":
						executionsAtRediscovery = executions.executed()
						selected := selectSearchCandidates(req, "billing_lookup")
						if len(selected) != 1 {
							return nil, fmt.Errorf("expected one newly registered billing candidate, got %d", len(selected))
						}
						return selected, nil
					case "search_3":
						return req.Candidates, nil
					default:
						return nil, fmt.Errorf("unexpected search call %q", req.CallID)
					}
				}
				base := newSearchModel(srv, responses.WithClientToolSearch(handler))
				var m model.Model = base
				if wrapped {
					m = searchPublicModelWrapper{Model: base, ProviderActionCompleter: base, ProviderDataRequestProjector: base}
				}
				// Keep eager exposure present from the first request so the agent's
				// ordinary tool instruction is stable before and after discovery.
				eager := executions.tool("weather", "Current weather.", "weather")
				path := filepath.Join(t.TempDir(), "external-then-registered.db")
				svc := openNativeRoutingSQLite(t, path)
				r := runner.NewRunner("search", llmagent.New("search", llmagent.WithModel(m),
					llmagent.WithTools([]tool.Tool{eager}),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream})), runner.WithSessionService(svc))
				events, failures := runSearch(t, r, model.NewUserMessage("find an external invoice tool"))
				require.Empty(t, failures)
				require.Equal(t, "external done", finalText(events))
				require.Equal(t, []string{"external"}, executions.executed())
				require.NoError(t, r.Close())
				require.NoError(t, svc.Close())

				// Only the schema survives Run 1. Run 2 adds a deferred local object
				// at that identity, which must be explicitly selected by a new search.
				svc = openNativeRoutingSQLite(t, path)
				defer svc.Close()
				set := deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{
					executions.tool("lookup", "Newly registered invoice lookup.", "registered"),
				}})
				r = runner.NewRunner("search", llmagent.New("search", llmagent.WithModel(m),
					llmagent.WithTools([]tool.Tool{eager}),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}),
					llmagent.WithToolSets([]tool.ToolSet{set})), runner.WithSessionService(svc))
				defer r.Close()
				events, failures = runSearch(t, r, model.NewUserMessage("use the newly registered invoice tool"))
				require.Empty(t, failures)
				require.Equal(t, "registered done", finalText(events))
				require.Equal(t, []string{"external"}, executionsAtRediscovery,
					"the obsolete call cannot execute the prior object or the new registration")
				require.Equal(t, []string{"external", "registered"}, executions.executed())
				require.Equal(t, [][]string{{}, {"billing_lookup"}, {}}, candidates,
					"a candidates-only handler can select the new registration once")
				requests := server.requests()
				require.Len(t, requests, 8)
				requireAppendOnlySearchRequests(t, requests[:3])
				requireAppendOnlySearchRequests(t, requests[3:])
				for _, request := range requests {
					require.Equal(t, []string{"function:weather", "tool_search:client"}, toolSummary(t, request["tools"]))
				}
				obsolete := conversationItems(t, requests[4]["input"])
				require.Equal(t, "function_call_output", obsolete[len(obsolete)-1].Type)
				require.Equal(t, "obsolete_1", obsolete[len(obsolete)-1].CallID)
				require.Contains(t, string(requests[4]["input"]), "search for it again")
				loaded := conversationItems(t, requests[5]["input"])
				result := loaded[len(loaded)-1]
				require.Equal(t, "tool_search_output", result.Type)
				require.Equal(t, "search_2", result.CallID)
				require.Equal(t, []string{"namespace:billing[lookup+defer]"}, toolSummary(t, result.Tools))
				require.Contains(t, string(result.Tools), "Newly registered invoice lookup.")
				require.Contains(t, string(requests[6]["input"]), "registered:inv-9")
				last := conversationItems(t, requests[7]["input"])
				require.Equal(t, "search_3", last[len(last)-1].CallID)
				require.JSONEq(t, `[]`, string(last[len(last)-1].Tools), "the already loaded candidate is not repeated")
			})
		}
	}
}

func TestRunnerClientToolSearchWithoutRegisteredToolsPreservesRequestPrefix(t *testing.T) {
	const externalCall = `[{"type":"function_call","id":"fc_external","status":"completed","call_id":"external_1","namespace":"billing","name":"lookup","arguments":"{\"sandbox_id\":\"inv-7\"}"}]`
	for _, wrapped := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("wrapper=%v/stream=%v", wrapped, stream), func(t *testing.T) {
				server, srv := newSearchServer(t, clientSearchRound, externalCall, messageOutput("msg_external", "external done"))
				executions := &searchExecutions{}
				handler := func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					return []responses.ToolSearchEntry{{Namespace: "billing", Name: "lookup",
						Tool: executions.sandboxTool("external_lookup", "external")}}, nil
				}
				base := newSearchModel(srv, responses.WithClientToolSearch(handler))
				var m model.Model = base
				if wrapped {
					m = searchPublicModelWrapper{Model: base, ProviderActionCompleter: base, ProviderDataRequestProjector: base}
				}
				r := runner.NewRunner("search", llmagent.New("search",
					llmagent.WithModel(m),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream})))
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("find an external invoice tool"))
				require.Empty(t, failures)
				require.Equal(t, "external done", finalText(events))
				require.Equal(t, []string{"external"}, executions.executed())
				requests := server.requests()
				require.Len(t, requests, 3)
				for _, request := range requests {
					require.Equal(t, []string{"tool_search:client"}, toolSummary(t, request["tools"]))
				}
				requireAppendOnlySearchRequests(t, requests)
			})
		}
	}
}

func TestRunnerClientToolSearchResolvesExternalToolBeforeEagerNamespaceMember(t *testing.T) {
	const call = `[{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","namespace":"weather","name":"weather","arguments":"{\"id\":\"city\"}"}]`
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			server, srv := newSearchServer(t, clientSearchRound, call, messageOutput("msg_weather", "done"))
			executions := &searchExecutions{}
			handler := func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				return []responses.ToolSearchEntry{{Name: "weather",
					Tool: executions.tool("external_weather", "External weather lookup.", "external")}}, nil
			}
			// The eager ToolSet is exposed as weather_weather. The search
			// independently exposes a standalone tool named weather.
			r := runner.NewRunner("search", llmagent.New("search",
				llmagent.WithModel(newSearchModel(srv, responses.WithClientToolSearch(handler))),
				llmagent.WithToolSets([]tool.ToolSet{searchToolSet{name: "weather", tools: []tool.Tool{
					executions.tool("weather", "Local weather lookup.", "local"),
				}}}), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream})))
			t.Cleanup(func() { _ = r.Close() })
			events, failures := runSearch(t, r, model.NewUserMessage("find the external weather tool"))
			require.Empty(t, failures)
			require.Equal(t, "done", finalText(events))
			require.Equal(t, []string{"external"}, executions.executed(),
				"the searched executor takes precedence over the eager namespace/member fallback")
			requests := server.requests()
			require.Len(t, requests, 3)
			require.Equal(t, []string{"function:weather_weather", "tool_search:client"}, toolSummary(t, requests[0]["tools"]))
			second := conversationItems(t, requests[1]["input"])
			require.Equal(t, []string{"function:weather+defer"}, toolSummary(t, second[len(second)-1].Tools))
			require.Contains(t, string(requests[2]["input"]), "external:city")
			requireAppendOnlySearchRequests(t, requests)
		})
	}
}

func TestRunnerOriginalToolSetNamePreservesStandaloneSearchBinding(t *testing.T) {
	const call = `[{"type":"function_call","id":"fc_weather","status":"completed","call_id":"weather_1","namespace":"weather","name":"weather","arguments":"{\"id\":\"city\"}"}]`
	for _, reopen := range []bool{false, true} {
		t.Run(fmt.Sprintf("reopen=%v", reopen), func(t *testing.T) {
			outputs := []string{clientSearchRound}
			if reopen {
				outputs = append(outputs, messageOutput("msg_selected", "selected"))
			}
			outputs = append(outputs, call, messageOutput("msg_done", "done"))
			server, srv := newSearchServer(t, outputs...)
			executions := &searchExecutions{}
			handler := func(context.Context, *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
				return []responses.ToolSearchEntry{{Name: "weather", Tool: executions.tool("external_weather", "External weather.", "external")}}, nil
			}
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			path := filepath.Join(t.TempDir(), "weather.db")
			run := func(prompt string, options ...llmagent.Option) {
				svc := openNativeRoutingSQLite(t, path)
				defer svc.Close()
				options = append(options, llmagent.WithModel(m))
				r := runner.NewRunner("search", llmagent.New("search", options...), runner.WithSessionService(svc))
				defer r.Close()
				_, failures := runSearch(t, r, model.NewUserMessage(prompt))
				require.Empty(t, failures)
			}
			if reopen {
				run("Select the external weather tool.")
			}
			local := searchToolSet{name: "weather", tools: []tool.Tool{executions.tool("weather", "Local weather.", "local")}}
			run("Use the external weather tool.", llmagent.WithToolSets([]tool.ToolSet{local}),
				llmagent.WithToolSetToolNameMode("weather", tool.ToolSetToolNameModeOriginal))
			requests := server.requests()
			require.Len(t, requests, len(outputs))
			lastInput := string(requests[len(requests)-1]["input"])
			if reopen {
				require.Empty(t, executions.executed())
				require.Contains(t, lastInput, "search for it again")
			} else {
				require.Equal(t, []string{"external"}, executions.executed())
				require.Contains(t, lastInput, "external:city")
			}
		})
	}
}

func TestRunnerClientToolSearchExpiresBindingAfterToolSetNameModeChange(t *testing.T) {
	const obsoleteCall = `[{"type":"function_call","id":"fc_obsolete","status":"completed","call_id":"obsolete_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-7\"}"}]`
	const searchAgain = `[{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"find the invoice tool again"}}]`
	const selectedCall = `[{"type":"function_call","id":"fc_selected","status":"completed","call_id":"selected_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"inv-8\"}"}]`
	for _, transition := range []struct {
		name  string
		modes [2]tool.ToolSetToolNameMode
	}{
		{name: "qualified_to_original", modes: [2]tool.ToolSetToolNameMode{tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal}},
		{name: "original_to_qualified", modes: [2]tool.ToolSetToolNameMode{tool.ToolSetToolNameModeOriginal, tool.ToolSetToolNameModeQualified}},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", transition.name, stream), func(t *testing.T) {
				server, srv := newSearchServer(t, clientSearchRound, messageOutput("msg_first", "tool selected"),
					obsoleteCall, searchAgain, selectedCall, messageOutput("msg_second", "invoice found"))
				executions := &searchExecutions{}
				var candidates [][]string
				var executionsAtRediscovery []string
				handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
					candidates = append(candidates, candidateNames(req))
					selected := selectSearchCandidates(req, "billing_lookup")
					if len(selected) != 1 {
						return nil, fmt.Errorf("expected one billing candidate, got %d", len(selected))
					}
					switch req.CallID {
					case "search_1":
						selected[0].Tool = executions.tool("sandbox_lookup", "Sandbox invoice lookup.", "sandbox")
					case "search_2":
						executionsAtRediscovery = executions.executed()
					default:
						return nil, fmt.Errorf("unexpected search call %q", req.CallID)
					}
					return selected, nil
				}
				m := newSearchModel(srv, responses.WithClientToolSearch(handler))
				path := filepath.Join(t.TempDir(), "name-mode-change.db")
				for run, mode := range transition.modes {
					svc := openNativeRoutingSQLite(t, path)
					r := runner.NewRunner("search", llmagent.New("search", llmagent.WithModel(m),
						llmagent.WithToolSetToolNameMode("billing", mode),
						llmagent.WithToolSets([]tool.ToolSet{deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{
							executions.tool("lookup", "Production invoice lookup.", "production"),
						}})}), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream})), runner.WithSessionService(svc))
					events, failures := runSearch(t, r, model.NewUserMessage(fmt.Sprintf("look up the invoice in run %d", run+1)))
					require.NoError(t, r.Close())
					require.NoError(t, svc.Close())
					require.Empty(t, failures)
					if run == 0 {
						require.Equal(t, "tool selected", finalText(events))
						require.Empty(t, executions.executed(), "Run 1 only selects a sandbox object")
					} else {
						require.Equal(t, "invoice found", finalText(events))
					}
				}
				require.Empty(t, executionsAtRediscovery, "the obsolete call cannot execute the sandbox or the new registry key")
				require.Equal(t, []string{"production"}, executions.executed(), "only the explicitly selected Run 2 call executes")
				require.Equal(t, [][]string{{"billing_lookup"}, {"billing_lookup"}}, candidates)
				requests := server.requests()
				require.Len(t, requests, 6)
				obsolete := conversationItems(t, requests[3]["input"])
				require.Equal(t, "obsolete_1", obsolete[len(obsolete)-1].CallID)
				require.Contains(t, string(requests[3]["input"]), "search for it again")
				require.Contains(t, string(requests[5]["input"]), "production:inv-8")
				requireAppendOnlySearchRequests(t, requests[:2])
				requireAppendOnlySearchRequests(t, requests[2:])
			})
		}
	}
}

// requireAppendOnlySearchRequests checks the complete JSON items, including
// tool_search_output schemas, rather than only their types or call IDs.
func requireAppendOnlySearchRequests(t *testing.T, requests []map[string]json.RawMessage) {
	t.Helper()
	require.NotEmpty(t, requests)
	var previous []json.RawMessage
	for i, request := range requests {
		require.Equal(t, string(requests[0]["tools"]), string(request["tools"]),
			"request %d keeps the same top-level tool definitions", i)
		var current []json.RawMessage
		require.NoError(t, json.Unmarshal(request["input"], &current))
		require.GreaterOrEqual(t, len(current), len(previous), "request %d preserves the prior input", i)
		for j := range previous {
			require.Equal(t, string(previous[j]), string(current[j]),
				"request %d preserves input item %d as an unchanged prefix", i, j)
		}
		previous = current
	}
}
