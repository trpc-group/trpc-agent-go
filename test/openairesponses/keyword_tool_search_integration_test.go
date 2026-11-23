//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package openairesponses_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	responses "trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

func TestRunnerKeywordToolSearchNamespaces(t *testing.T) {
	for _, tc := range []struct {
		namespace   string
		argument    string
		description string
		hidden      string
	}{
		{"billing", "sandbox_id", "Look up an invoice in the sandbox.", "Look up a customer."},
		{"crm", "id", "Look up a customer.", "Look up an invoice in the sandbox."},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.namespace, stream), func(t *testing.T) {
				search := fmt.Sprintf(`[{"type":"tool_search_call","id":"ts_keyword","execution":"client","call_id":"search_keyword","status":"completed","arguments":{"query":%q}}]`, tc.namespace)
				arguments, err := json.Marshal(map[string]string{tc.argument: "record-7"})
				require.NoError(t, err)
				call := fmt.Sprintf(`[{"type":"function_call","id":"fc_keyword","status":"completed","call_id":"call_keyword","namespace":%q,"name":"lookup","arguments":%q}]`, tc.namespace, string(arguments))
				answer := tc.namespace + " lookup complete"
				server, srv := newSearchServer(t, search, call, messageOutput("msg_keyword", answer))
				executions := &searchExecutions{}
				agt := llmagent.New("keyword",
					llmagent.WithModel(newSearchModel(srv, responses.WithClientToolSearch(responses.NewKeywordToolSearch()))),
					llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}),
					llmagent.WithToolSets([]tool.ToolSet{
						deferred.ToolSet(searchToolSet{name: "billing", tools: []tool.Tool{
							executions.sandboxTool("lookup", "billing.lookup"),
						}}),
						deferred.ToolSet(searchToolSet{name: "crm", tools: []tool.Tool{
							executions.tool("lookup", "Look up a customer.", "crm.lookup"),
						}}),
					}))
				r := runner.NewRunner("keyword", agt)
				t.Cleanup(func() { _ = r.Close() })
				events, failures := runSearch(t, r, model.NewUserMessage("find and use the "+tc.namespace+" lookup"))
				require.Empty(t, failures)
				require.Equal(t, answer, finalText(events))
				require.Equal(t, []string{tc.namespace + ".lookup"}, executions.executed(),
					"the selected namespace must reach its own Go tool exactly once")

				requests := server.requests()
				require.Len(t, requests, 3, "search, function call, and final answer are separate model rounds")
				requireAppendOnlySearchRequests(t, requests)
				for _, request := range requests {
					require.Equal(t, stream, string(request["stream"]) == "true")
					require.Equal(t, []string{"tool_search:client"}, toolSummary(t, request["tools"]),
						"deferred schemas are loaded only through the native search result")
					require.NotContains(t, string(request["input"]), tc.hidden)
				}
				second := conversationItems(t, requests[1]["input"])
				require.Equal(t, []string{"message:user", "tool_search_call", "tool_search_output"}, inputTypes(second))
				result := second[len(second)-1]
				require.Equal(t, "search_keyword", result.CallID)
				require.Equal(t, "client", result.Execution)
				require.Equal(t, []string{"namespace:" + tc.namespace + "[lookup+defer]"}, toolSummary(t, result.Tools))
				var namespaces []struct {
					Name  string `json:"name"`
					Tools []struct {
						Name        string `json:"name"`
						Description string `json:"description"`
						Parameters  struct {
							Properties map[string]json.RawMessage `json:"properties"`
						} `json:"parameters"`
					} `json:"tools"`
				}
				require.NoError(t, json.Unmarshal(result.Tools, &namespaces))
				require.Len(t, namespaces, 1)
				require.Equal(t, tc.namespace, namespaces[0].Name)
				require.Len(t, namespaces[0].Tools, 1)
				member := namespaces[0].Tools[0]
				require.Equal(t, "lookup", member.Name)
				require.Equal(t, tc.description, member.Description)
				require.Len(t, member.Parameters.Properties, 1)
				require.Contains(t, member.Parameters.Properties, tc.argument,
					"same-name members retain the selected tool's input schema")
				third := conversationItems(t, requests[2]["input"])
				require.Equal(t, []string{
					"message:user", "tool_search_call", "tool_search_output", "function_call", "function_call_output",
				}, inputTypes(third))
				require.Equal(t, tc.namespace, third[len(third)-2].Namespace)
				require.Equal(t, "call_keyword", third[len(third)-1].CallID)
				require.Contains(t, string(requests[2]["input"]), tc.namespace+".lookup:record-7")
			})
		}
	}
}
