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

// A registered top-level tool and a ToolSet member can exchange execution
// names across runs. Their persisted selection status must remain independent.
func TestRunnerClientToolSearchBindingsSurviveNameModeChange(t *testing.T) {
	const oldCall = `[{"type":"function_call","id":"fc_old","status":"completed","call_id":"old_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"before\"}"}]`
	const searchAgain = `[{"type":"tool_search_call","id":"ts_2","execution":"client","call_id":"search_2","status":"completed","arguments":{"query":"find invoice tools again"}}]`
	const newCall = `[{"type":"function_call","id":"fc_new","status":"completed","call_id":"new_1","namespace":"billing","name":"lookup","arguments":"{\"id\":\"after\"}"}]`
	for _, transition := range []struct {
		name  string
		modes [2]tool.ToolSetToolNameMode
	}{
		{"qualified_to_original", [2]tool.ToolSetToolNameMode{tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal}},
		{"original_to_qualified", [2]tool.ToolSetToolNameMode{tool.ToolSetToolNameModeOriginal, tool.ToolSetToolNameModeQualified}},
	} {
		for _, replacedMember := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/replaced_member=%v/stream=%v", transition.name, replacedMember, stream), func(t *testing.T) {
					server, srv := newSearchServer(t, clientSearchRound, messageOutput("first", "selected"),
						oldCall, searchAgain, newCall, messageOutput("second", "done"))
					executions := &searchExecutions{}
					var candidatesAtResearch []responses.ToolSearchEntry
					var executionsAtResearch []string
					handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
						switch req.CallID {
						case "search_1":
							selected := append([]responses.ToolSearchEntry(nil), req.Candidates...)
							if len(selected) != 2 {
								return nil, fmt.Errorf("want two candidates, got %d", len(selected))
							}
							for i := range selected {
								isMember := selected[i].Namespace == "billing" && selected[i].Name == "lookup"
								if isMember == replacedMember {
									selected[i].Tool = executions.tool("sandbox_lookup", "Sandbox lookup.", "sandbox")
								}
							}
							return selected, nil
						case "search_2":
							candidatesAtResearch = append([]responses.ToolSearchEntry(nil), req.Candidates...)
							executionsAtResearch = executions.executed()
							return req.Candidates, nil
						default:
							return nil, fmt.Errorf("unexpected search %q", req.CallID)
						}
					}
					m := newSearchModel(srv, responses.WithClientToolSearch(handler))
					path := filepath.Join(t.TempDir(), "binding-identity.db")
					for run, mode := range transition.modes {
						var top []tool.Tool
						if run == 0 {
							name := "lookup"
							if mode == tool.ToolSetToolNameModeOriginal {
								name = "billing_lookup"
							}
							top = []tool.Tool{deferred.Tool(executions.tool(name, "Top-level lookup.", "top"))}
						}
						svc := openNativeRoutingSQLite(t, path)
						r := runner.NewRunner("search", llmagent.New("search", llmagent.WithModel(m),
							llmagent.WithTools(top), llmagent.WithToolSetToolNameMode("billing", mode),
							llmagent.WithToolSets([]tool.ToolSet{deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{
								executions.tool("lookup", "Production lookup.", "production"),
							}})}), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream})), runner.WithSessionService(svc))
						events, failures := runSearch(t, r, model.NewUserMessage(fmt.Sprintf("run %d", run+1)))
						require.NoError(t, r.Close())
						require.NoError(t, svc.Close())
						require.Empty(t, failures)
						if run == 0 {
							require.Equal(t, "selected", finalText(events))
							require.Empty(t, executions.executed())
						} else {
							require.Equal(t, "done", finalText(events))
						}
					}
					requests := server.requests()
					require.Len(t, requests, 6)
					if replacedMember {
						require.Empty(t, executionsAtResearch, "the expired replacement cannot dispatch to the registered tool")
						require.Len(t, candidatesAtResearch, 1)
						require.Equal(t, "billing", candidatesAtResearch[0].Namespace)
						require.Equal(t, "lookup", candidatesAtResearch[0].Name)
						require.Contains(t, string(requests[3]["input"]), "search for it again")
						require.Equal(t, []string{"production"}, executions.executed())
					} else {
						require.Equal(t, []string{"production"}, executionsAtResearch, "a registered member remains selected")
						require.Empty(t, candidatesAtResearch)
						require.Contains(t, string(requests[3]["input"]), "production:before")
						require.Equal(t, []string{"production", "production"}, executions.executed())
					}
					require.Contains(t, string(requests[5]["input"]), "production:after")
				})
			}
		}
	}
}
