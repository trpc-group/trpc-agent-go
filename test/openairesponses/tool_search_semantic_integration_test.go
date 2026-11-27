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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

type invoiceSearchEmbedder struct{}

func (invoiceSearchEmbedder) GetEmbedding(_ context.Context, text string) ([]float64, error) {
	if text == "invoice status" || strings.Contains(text, "\nNamespace: billing ") {
		return []float64{1, 0}, nil
	}
	return []float64{0, 1}, nil
}

func (e invoiceSearchEmbedder) GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error) {
	v, err := e.GetEmbedding(ctx, text)
	return v, nil, err
}

func (invoiceSearchEmbedder) GetDimensions() int { return 2 }

func TestRunnerSemanticToolSearchLoadsAndExecutesNamespacedTool(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			server, srv := newSearchServer(t, clientSearchRound, loadedBillingCall, messageOutput("msg_1", "invoice paid"))
			handler, err := toolsearch.NewSemantic(invoiceSearchEmbedder{})
			require.NoError(t, err)
			calls := &searchExecutions{}
			m := newSearchModel(srv, responses.WithClientToolSearch(handler))
			agt := llmagent.New("search", llmagent.WithModel(m),
				llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}),
				llmagent.WithTools([]tool.Tool{calls.tool("weather", "Current weather.", "weather")}),
				llmagent.WithToolSets([]tool.ToolSet{
					deferred.ToolSet(searchToolSet{name: "crm", description: "Customer records.", tools: []tool.Tool{
						calls.tool("lookup", "Look up a record.", "crm.lookup"),
					}}),
					deferred.ToolSet(searchToolSet{name: "billing", description: "Invoices.", tools: []tool.Tool{
						calls.tool("lookup", "Look up a record.", "billing.lookup"),
					}}),
				}),
			)
			r := runner.NewRunner("search", agt)
			t.Cleanup(func() { _ = r.Close() })

			events, failures := runSearch(t, r, model.NewUserMessage("is invoice inv-7 paid?"))
			require.Empty(t, failures)
			require.Equal(t, "invoice paid", finalText(events))
			require.Equal(t, []string{"billing.lookup"}, calls.executed())

			requests := server.requests()
			require.Len(t, requests, 3)
			require.Equal(t, []string{"function:weather", "tool_search:client"}, toolSummary(t, requests[0]["tools"]))
			requireAppendOnlySearchRequests(t, requests)
			input := conversationItems(t, requests[1]["input"])
			output := input[len(input)-1]
			require.Equal(t, "tool_search_output", output.Type)
			require.Equal(t, []string{"namespace:billing[lookup+defer]"}, toolSummary(t, output.Tools))
			require.Contains(t, string(requests[2]["input"]), "billing.lookup:inv-7")
		})
	}
}
