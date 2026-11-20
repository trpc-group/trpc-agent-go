//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	imodelrequest "trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

type searchTestTool struct {
	name        string
	description string
}

func (s searchTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: s.description,
		InputSchema: &tool.Schema{
			Type:       "object",
			Properties: map[string]*tool.Schema{"id": {Type: "string"}},
		},
	}
}

func (s searchTestTool) Call(context.Context, []byte) (any, error) { return s.name, nil }

type selfDeferredTool struct{ searchTestTool }

func (selfDeferredTool) ShouldDefer(context.Context) bool { return true }

type searchTestToolSet struct {
	name        string
	description string
	tools       []tool.Tool
}

func (s searchTestToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s searchTestToolSet) Close() error                      { return nil }
func (s searchTestToolSet) Name() string                      { return s.name }
func (s searchTestToolSet) Description() string               { return s.description }

// agentTools registers tools the way an LLM agent does: ToolSet members are
// exposed with qualified names and an optional deferral decision.
func agentTools(t *testing.T, standalone []tool.Tool, sets ...tool.ToolSet) map[string]tool.Tool {
	t.Helper()
	out := make(map[string]tool.Tool)
	for _, tl := range standalone {
		out[tl.Declaration().Name] = tl
	}
	for _, set := range sets {
		named := itool.NewNamedToolSet(set)
		for _, tl := range named.Tools(context.Background()) {
			out[tl.Declaration().Name] = tl
		}
	}
	return out
}

func crmToolSet() searchTestToolSet {
	return searchTestToolSet{
		name:        "crm",
		description: "CRM tools for customers and orders.",
		tools: []tool.Tool{
			searchTestTool{name: "lookup", description: "Look up a customer."},
			searchTestTool{name: "orders", description: "List open orders."},
		},
	}
}

func billingToolSet() searchTestToolSet {
	return searchTestToolSet{
		name: "billing",
		tools: []tool.Tool{
			searchTestTool{name: "lookup", description: "Look up an invoice."},
		},
	}
}

func TestToolSearchDisabledKeepsRequestsUnchanged(t *testing.T) {
	plain := agentTools(t,
		[]tool.Tool{searchTestTool{name: "weather"}},
		crmToolSet(),
	)
	marked := agentTools(t,
		[]tool.Tool{deferred.Tool(searchTestTool{name: "weather"}), selfDeferredTool{searchTestTool{name: "self"}}},
		deferred.ToolSet(crmToolSet()),
	)
	delete(marked, "self")
	user := []model.Message{model.NewUserMessage("hi")}
	want := captureRequest(t, &model.Request{Messages: user, Tools: plain})
	got := captureRequest(t, &model.Request{Messages: user, Tools: marked})
	require.Equal(t, want.body, got.body, "deferral has no wire effect without tool search")

	searchWithoutDeferral := captureRequest(t, &model.Request{Messages: user, Tools: plain}, WithHostedToolSearch())
	require.Equal(t, want.body, searchWithoutDeferral.body,
		"tool search adds nothing when no tool is deferred and history has no search")

	history := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
		{"type":"tool_search_call","execution":"server","call_id":null,"status":"completed","arguments":{"paths":[]}},
		{"type":"tool_search_output","execution":"server","call_id":null,"status":"completed","tools":[]}]`),
	}}
	searchWithHistory := captureRequest(t, &model.Request{Messages: append(user, history)}, WithHostedToolSearch())
	requireJSONField(t, `[{"type":"tool_search"}]`, searchWithHistory.body["tools"])

	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
		return nil, nil
	}))
	ctx := imodelrequest.WithToolsDisabled(context.Background())
	params, _, err := m.buildParams(ctx, &model.Request{Messages: user, Tools: marked})
	require.NoError(t, err)
	require.Empty(t, params.Tools, "a tool-free final response also disables client search")
}

func TestHostedToolSearchDeclaresDeferredToolsAndNamespaces(t *testing.T) {
	tools := agentTools(t,
		[]tool.Tool{
			searchTestTool{name: "weather", description: "Weather."},
			deferred.Tool(searchTestTool{name: "shipping_eta", description: "ETA."}),
		},
		deferred.ToolSet(crmToolSet(), deferred.WithFilter(tool.NewIncludeToolNamesFilter("orders"))),
		billingToolSet(),
	)
	got := captureRequest(t, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}, Tools: tools}, WithHostedToolSearch())
	requireJSONField(t, `[
	  {"type":"function","name":"billing_lookup","description":"Look up an invoice.","strict":false,
	   "parameters":{"type":"object","properties":{"id":{"type":"string"}}}},
	  {"type":"namespace","name":"crm","description":"CRM tools for customers and orders.","tools":[
	    {"type":"function","name":"lookup","description":"Look up a customer.","strict":false,
	     "parameters":{"type":"object","properties":{"id":{"type":"string"}}}},
	    {"type":"function","name":"orders","description":"List open orders.","strict":false,"defer_loading":true,
	     "parameters":{"type":"object","properties":{"id":{"type":"string"}}}}]},
	  {"type":"function","name":"shipping_eta","description":"ETA.","strict":false,"defer_loading":true,
	   "parameters":{"type":"object","properties":{"id":{"type":"string"}}}},
	  {"type":"function","name":"weather","description":"Weather.","strict":false,
	   "parameters":{"type":"object","properties":{"id":{"type":"string"}}}},
	  {"type":"tool_search"}
	]`, got.body["tools"])
}

func TestHostedToolSearchNamespaceDescriptionFallback(t *testing.T) {
	tools := agentTools(t, nil, deferred.ToolSet(billingToolSet()))
	got := captureRequest(t, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}, Tools: tools}, WithHostedToolSearch())
	encoded, err := json.Marshal(got.body["tools"])
	require.NoError(t, err)
	var params []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &params))
	require.Equal(t, "namespace", params[0]["type"])
	require.Equal(t, "Tools from the billing tool set.", params[0]["description"])
}

func TestToolSearchRejectsNamespaceFunctionNameConflicts(t *testing.T) {
	tools := agentTools(t,
		[]tool.Tool{searchTestTool{name: "crm"}},
		deferred.ToolSet(crmToolSet(), deferred.WithFilter(tool.NewIncludeToolNamesFilter("orders"))),
	)
	srv, calls := rejectingServer(t)
	for _, opt := range []Option{WithHostedToolSearch(), WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
		return nil, nil
	})} {
		m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), opt)
		_, err := m.GenerateContent(context.Background(), &model.Request{
			Messages: []model.Message{model.NewUserMessage("hi")},
			Tools:    tools,
		})
		require.ErrorContains(t, err, `"crm"`)
	}
	require.Zero(t, calls.Load(), "the conflict is rejected before the request is sent")
}

func TestToolSearchMapsNamespacedCallsToRequestTools(t *testing.T) {
	tools := agentTools(t,
		[]tool.Tool{deferred.Tool(searchTestTool{name: "shipping_eta"})},
		deferred.ToolSet(crmToolSet()),
		deferred.ToolSet(billingToolSet()),
	)
	m := New("gpt-5", WithHostedToolSearch())
	_, plan, err := m.buildParams(context.Background(), &model.Request{Tools: tools})
	require.NoError(t, err)
	output := `{"id":"r","status":"completed","output":[
	  {"type":"tool_search_call","id":"ts_1","execution":"server","call_id":null,"status":"completed","arguments":{"paths":["crm","billing"]}},
	  {"type":"tool_search_output","id":"tso_1","execution":"server","call_id":null,"status":"completed","tools":[]},
	  {"type":"function_call","id":"fc_1","status":"completed","call_id":"c1","namespace":"crm","name":"lookup","arguments":"{}"},
	  {"type":"function_call","id":"fc_2","status":"completed","call_id":"c2","namespace":"billing","name":"lookup","arguments":"{}"},
	  {"type":"function_call","id":"fc_3","status":"completed","call_id":"c3","namespace":"shipping_eta","name":"shipping_eta","arguments":"{}"},
	  {"type":"function_call","id":"fc_4","status":"completed","call_id":"c4","namespace":"crm","name":"missing","arguments":"{}"}]}`
	projected, err := projectRequestResponse(decodeResponse(t, output), "gpt-5", plan)
	require.NoError(t, err)
	require.True(t, projected.Done, "hosted search completes inside the response")
	calls := projected.Choices[0].Message.ToolCalls
	require.Len(t, calls, 4)
	require.Equal(t, "crm_lookup", calls[0].Function.Name)
	require.Equal(t, "billing_lookup", calls[1].Function.Name,
		"members with the same name in different namespaces map to different tools")
	require.Equal(t, "shipping_eta", calls[2].Function.Name)
	require.Equal(t, "crm.missing", calls[3].Function.Name,
		"an unknown namespaced call never falls back to a tool with the bare name")
	require.Equal(t, "billing_lookup", mustFunctionName(t, plan, "", "billing_lookup"),
		"an unqualified call can still address an agent-registered execution key")
	payload := string(projected.Choices[0].Message.ProviderData.Payload)
	require.Contains(t, payload, `"namespace":"crm"`, "the native record keeps the namespace")

	m2 := New("gpt-5")
	_, err = m2.ProjectProviderData(projected.Choices[0].Message.ProviderData)
	require.ErrorContains(t, err, "needs its request")
}

func TestClientToolSearchRequestOmitsDeferredDefinitions(t *testing.T) {
	tools := agentTools(t,
		[]tool.Tool{deferred.Tool(searchTestTool{name: "shipping_eta"})},
		deferred.ToolSet(crmToolSet(), deferred.WithFilter(tool.NewIncludeToolNamesFilter("orders"))),
	)
	handler := func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }
	got := captureRequest(t, &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}, Tools: tools}, WithClientToolSearch(handler))
	requireJSONField(t, `[
	  {"type":"namespace","name":"crm","description":"CRM tools for customers and orders.","tools":[
	    {"type":"function","name":"lookup","description":"Look up a customer.","strict":false,
	     "parameters":{"type":"object","properties":{"id":{"type":"string"}}}}]},
	  {"type":"tool_search","execution":"client",
	   "description":"`+clientToolSearchDescription+`",
	   "parameters":{"type":"object","properties":{"query":{"type":"string","description":"The capability or task that the needed tools must support."}},
	     "required":["query"],"additionalProperties":false}}
	]`, got.body["tools"])

	_, err := New("gpt-5", WithClientToolSearch(nil)).GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	})
	require.ErrorContains(t, err, "requires a handler")
}

func TestClientToolSearchWithNoCandidates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools map[string]tool.Tool
	}{
		{name: "no tools"},
		{name: "eager tools", tools: agentTools(t, []tool.Tool{searchTestTool{name: "weather"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var handled bool
			handler := func(_ context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
				handled = true
				require.Empty(t, req.Candidates)
				return nil, nil
			}
			request := &model.Request{Messages: []model.Message{model.NewUserMessage("orders")}, Tools: tc.tools}
			got := captureRequest(t, request, WithClientToolSearch(handler))
			encoded, err := json.Marshal(got.body["tools"])
			require.NoError(t, err)
			var tools []map[string]any
			require.NoError(t, json.Unmarshal(encoded, &tools))
			require.Len(t, tools, len(tc.tools)+1)
			require.Equal(t, "tool_search", tools[len(tools)-1]["type"])
			require.Equal(t, "client", tools[len(tools)-1]["execution"])

			m := New("gpt-5", WithClientToolSearch(handler))
			_, plan, err := m.buildParams(context.Background(), request)
			require.NoError(t, err)
			response, err := projectRequestResponse(decodeResponse(t, clientSearchOutput), "gpt-5", plan)
			require.NoError(t, err)
			results, err := m.CompleteProviderActions(context.Background(), request, response)
			require.NoError(t, err)
			require.True(t, handled)
			require.Len(t, results, 1)
			require.JSONEq(t, `[{"type":"tool_search_output","execution":"client","call_id":"search_1","status":"completed","tools":[]}]`,
				string(results[0].ProviderData.Payload))
		})
	}
}

func TestToolSearchPreservesDeferralWithDeclarationOverride(t *testing.T) {
	ctx := context.Background()
	updated := tool.Declaration{
		Name:        "find_order",
		Description: "Find an order by customer email.",
		InputSchema: &tool.Schema{
			Type:       "object",
			Properties: map[string]*tool.Schema{"email": {Type: "string"}},
			Required:   []string{"email"},
		},
	}
	base := deferred.Tool(searchTestTool{name: "find_order", description: "Original description."})
	overlaid := itool.ApplyDeclarations([]tool.Tool{base}, []tool.Declaration{updated})
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("find my order")},
		Tools:    map[string]tool.Tool{"find_order": overlaid[0]},
	}

	hosted := captureRequest(t, req, WithHostedToolSearch())
	requireJSONField(t, `[
	  {"type":"function","name":"find_order","description":"Find an order by customer email.","strict":false,"defer_loading":true,
	   "parameters":{"type":"object","properties":{"email":{"type":"string"}},"required":["email"]}},
	  {"type":"tool_search"}
	]`, hosted.body["tools"])

	var seen *ToolSearchRequest
	handler := func(_ context.Context, search *ToolSearchRequest) ([]ToolSearchEntry, error) {
		seen = search
		return search.Candidates, nil
	}
	client := captureRequest(t, req, WithClientToolSearch(handler))
	requireJSONField(t, `[
	  {"type":"tool_search","execution":"client",
	   "description":"`+clientToolSearchDescription+`",
	   "parameters":{"type":"object","properties":{"query":{"type":"string","description":"The capability or task that the needed tools must support."}},
	     "required":["query"],"additionalProperties":false}}
	]`, client.body["tools"])

	m := New("gpt-5", WithClientToolSearch(handler))
	_, plan, err := m.buildParams(ctx, req)
	require.NoError(t, err)
	response, err := projectRequestResponse(decodeResponse(t, clientSearchOutput), "gpt-5", plan)
	require.NoError(t, err)
	results, err := m.CompleteProviderActions(ctx, req, response)
	require.NoError(t, err)
	require.NotNil(t, seen)
	require.Len(t, seen.Candidates, 1)
	require.Equal(t, "find_order", seen.Candidates[0].Name)
	require.Equal(t, overlaid[0], seen.Candidates[0].Tool)
	require.Equal(t, updated, *seen.Candidates[0].Tool.Declaration())
	require.Len(t, results, 1)
	require.JSONEq(t, `[{"type":"tool_search_output","execution":"client","call_id":"search_1","status":"completed","tools":[
	  {"type":"function","name":"find_order","description":"Find an order by customer email.","defer_loading":true,"strict":false,
	   "parameters":{"type":"object","properties":{"email":{"type":"string"}},"required":["email"]}}]}]`,
		string(results[0].ProviderData.Payload))
}

const clientSearchOutput = `{"id":"r1","status":"completed","output":[
  {"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"opaque"},
  {"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"search_1","status":"completed","arguments":{"query":"orders"}}]}`

func TestClientToolSearchCompletesSearchCalls(t *testing.T) {
	tools := agentTools(t,
		[]tool.Tool{deferred.Tool(searchTestTool{name: "shipping_eta", description: "ETA."})},
		deferred.ToolSet(crmToolSet(), deferred.WithFilter(tool.NewIncludeToolNamesFilter("orders"))),
		deferred.ToolSet(billingToolSet()),
	)
	var seen *ToolSearchRequest
	m := New("gpt-5", WithClientToolSearch(func(_ context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
		seen = req
		return []ToolSearchEntry{req.Candidates[1], req.Candidates[2], req.Candidates[0]}, nil
	}))
	request := &model.Request{Messages: []model.Message{model.NewUserMessage("orders")}, Tools: tools}
	_, plan, err := m.buildParams(context.Background(), request)
	require.NoError(t, err)
	projected, err := projectRequestResponse(decodeResponse(t, clientSearchOutput), "gpt-5", plan)
	require.NoError(t, err)
	require.False(t, projected.Done, "a client search must be completed before the next model step")
	require.Empty(t, projected.Choices[0].Message.ToolCalls, "a search call is not a function call")

	messages, err := m.CompleteProviderActions(context.Background(), request, projected)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "search_1", seen.CallID)
	require.Equal(t, "orders", seen.Query)
	require.JSONEq(t, `{"query":"orders"}`, string(seen.Arguments))
	require.Len(t, seen.Candidates, 3)
	require.Equal(t, ToolSearchEntry{
		Namespace: "billing", NamespaceDescription: "Tools from the billing tool set.",
		Name: "lookup", Tool: tools["billing_lookup"],
	}, seen.Candidates[0])
	require.Equal(t, "Look up an invoice.", seen.Candidates[0].Tool.Declaration().Description)
	require.Equal(t, "orders", seen.Candidates[1].Name)
	require.Equal(t, tools["crm_orders"], seen.Candidates[1].Tool)
	require.Equal(t, "shipping_eta", seen.Candidates[2].Name)
	require.Equal(t, tools["shipping_eta"], seen.Candidates[2].Tool)
	require.Empty(t, seen.Candidates[2].Namespace)

	result := messages[0]
	require.Equal(t, model.RoleTool, result.Role)
	require.Equal(t, "search_1", result.ToolID)
	require.Equal(t, ToolSearchToolName, result.ToolName)
	require.JSONEq(t, `{"loaded_tools":["crm_orders","shipping_eta","billing_lookup"]}`, result.Content)
	require.Equal(t, responseProvider, result.ProviderData.Protocol)
	require.Equal(t, "gpt-5", result.ProviderData.Model)
	require.JSONEq(t, `[{"type":"tool_search_output","execution":"client","call_id":"search_1","status":"completed","tools":[
	  {"type":"namespace","name":"crm","description":"CRM tools for customers and orders.","tools":[
	    {"type":"function","name":"orders","description":"List open orders.","defer_loading":true,"strict":false,
	     "parameters":{"type":"object","properties":{"id":{"type":"string"}}}}]},
	  {"type":"function","name":"shipping_eta","description":"ETA.","defer_loading":true,"strict":false,
	   "parameters":{"type":"object","properties":{"id":{"type":"string"}}}},
	  {"type":"namespace","name":"billing","description":"Tools from the billing tool set.","tools":[
	    {"type":"function","name":"lookup","description":"Look up an invoice.","defer_loading":true,"strict":false,
	     "parameters":{"type":"object","properties":{"id":{"type":"string"}}}}]}]}]`,
		string(result.ProviderData.Payload))

	// The next request replays the call and output in order and offers only
	// tools that are not loaded yet.
	next := &model.Request{
		Messages: []model.Message{model.NewUserMessage("orders"), projected.Choices[0].Message, result},
		Tools:    tools,
	}
	items, err := convertMessages(next.Messages, "gpt-5")
	require.NoError(t, err)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	var input []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &input))
	require.Len(t, input, 4)
	require.Equal(t, "reasoning", input[1]["type"])
	require.Equal(t, "tool_search_call", input[2]["type"])
	require.Equal(t, "tool_search_output", input[3]["type"])
	require.Equal(t, "search_1", input[3]["call_id"])
	_, nextPlan, err := m.buildParams(context.Background(), next)
	require.NoError(t, err)
	require.Empty(t, nextPlan.deferred, "loaded tools are not offered again")
	require.Equal(t, "crm_orders", mustFunctionName(t, nextPlan, "crm", "orders"))
	require.Equal(t, "billing_lookup", mustFunctionName(t, nextPlan, "billing", "lookup"))
	require.Equal(t, "shipping_eta", mustFunctionName(t, nextPlan, "shipping_eta", "shipping_eta"))
	last := nextPlan.params[len(nextPlan.params)-1]
	require.NotNil(t, last.OfToolSearch, "tool search stays declared while history uses it")
}

func mustFunctionName(t *testing.T, plan *toolPlan, namespace, name string) string {
	t.Helper()
	got, err := plan.functionName(namespace, name)
	require.NoError(t, err)
	return got
}

func TestProjectionPrefersStandaloneSelfNamespaceOverUnloadedEagerMember(t *testing.T) {
	request := &model.Request{Tools: agentTools(t, []tool.Tool{searchTestTool{name: "crm"}},
		searchTestToolSet{name: "crm", tools: []tool.Tool{searchTestTool{name: "crm"}}})}
	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }))
	plan, err := m.planTools(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "crm", mustFunctionName(t, plan, "crm", "crm"))
	require.Equal(t, "crm", mustFunctionName(t, projectionToolPlan(request), "crm", "crm"))
}

func TestHostedSearchHistoryKeepsLoadedEagerMemberRouting(t *testing.T) {
	history := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"server","status":"completed","tools":[{"type":"namespace","name":"billing","tools":[{"type":"function","name":"lookup"}]}]}]`),
	}}
	for _, mode := range []tool.ToolSetToolNameMode{tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal} {
		members := itool.NewNamedToolSetWithMode(billingToolSet(), mode).Tools(context.Background())
		key := members[0].Declaration().Name
		request := &model.Request{Messages: []model.Message{history}, Tools: map[string]tool.Tool{key: members[0]}}
		plan, err := New("gpt-5", WithHostedToolSearch()).planTools(context.Background(), request)
		require.NoError(t, err)
		require.Equal(t, key, mustFunctionName(t, plan, "billing", "lookup"))
		require.Equal(t, key, mustFunctionName(t, projectionToolPlan(request), "billing", "lookup"))
	}
}

func TestHostedSearchHistoryDoesNotEraseClientBindingOrigin(t *testing.T) {
	definitions := `[{"type":"namespace","name":"billing","tools":[{"type":"function","name":"lookup"}]}]`
	client := model.Message{Role: model.RoleTool, ToolID: "search_1", ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"search_1","tools":` + definitions + `}]`),
	}}
	hosted := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"server","tools":` + definitions + `}]`),
	}}
	history := scanToolSearchHistory([]model.Message{client, hosted})
	require.Equal(t, "search_1", history.origins[wireFunction{namespace: "billing", name: "lookup"}])
}

func TestClientToolSearchUsesReturnedEntries(t *testing.T) {
	withSchema := func(name, description, property string) tool.Tool {
		return itool.ApplyDeclarations([]tool.Tool{searchTestTool{name: name}}, []tool.Declaration{{
			Name: name, Description: description,
			InputSchema: &tool.Schema{
				Type:       "object",
				Properties: map[string]*tool.Schema{property: {Type: "string"}},
				Required:   []string{property},
			},
		}})[0]
	}
	original := deferred.Tool(searchTestTool{name: "shipping_eta", description: "Original ETA."})
	replacement := withSchema("shipping_eta", "Estimate delivery by postal code.", "postal_code")
	external := withSchema("catalog_lookup", "Find a product.", "sku")
	stock := withSchema("inventory_stock", "Check product stock.", "sku")
	m := New("gpt-5", WithClientToolSearch(func(_ context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
		require.Len(t, req.Candidates, 1)
		return []ToolSearchEntry{
			{Namespace: "catalog", NamespaceDescription: "Product tools.", Name: "lookup", Tool: external},
			{Tool: replacement},
			{Namespace: "catalog", NamespaceDescription: "Product tools.", Name: "stock", Tool: stock},
		}, nil
	}))
	request := &model.Request{
		Messages: []model.Message{model.NewUserMessage("find a product and estimate delivery")},
		Tools:    agentTools(t, []tool.Tool{original}),
	}
	_, plan, err := m.buildParams(context.Background(), request)
	require.NoError(t, err)
	response, err := projectRequestResponse(decodeResponse(t, clientSearchOutput), "gpt-5", plan)
	require.NoError(t, err)
	results, err := m.CompleteProviderActions(context.Background(), request, response)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.JSONEq(t, `[{"type":"tool_search_output","execution":"client","call_id":"search_1","status":"completed","tools":[
	  {"type":"namespace","name":"catalog","description":"Product tools.","tools":[
	    {"type":"function","name":"lookup","description":"Find a product.","defer_loading":true,"strict":false,
	     "parameters":{"type":"object","properties":{"sku":{"type":"string"}},"required":["sku"]}},
	    {"type":"function","name":"stock","description":"Check product stock.","defer_loading":true,"strict":false,
	     "parameters":{"type":"object","properties":{"sku":{"type":"string"}},"required":["sku"]}}]},
	  {"type":"function","name":"shipping_eta","description":"Estimate delivery by postal code.","defer_loading":true,"strict":false,
	   "parameters":{"type":"object","properties":{"postal_code":{"type":"string"}},"required":["postal_code"]}}]}]`,
		string(results[0].ProviderData.Payload))
	require.Equal(t, map[string]tool.Tool{"shipping_eta": original}, request.Tools,
		"completing a search does not modify the input request's tools")
	request.Messages = append(request.Messages, response.Choices[0].Message, results[0])
	_, plan, err = m.buildParams(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "catalog.lookup", mustFunctionName(t, plan, "catalog", "lookup"))
	_, err = plan.functionName("", "catalog.lookup")
	require.ErrorContains(t, err, "does not name a top-level tool")
}

func TestClientToolSearchHandlerOutcomes(t *testing.T) {
	tools := agentTools(t, []tool.Tool{deferred.Tool(searchTestTool{name: "shipping_eta"})})
	pending := func(t *testing.T, m *Model) (*model.Request, *model.Response) {
		request := &model.Request{Messages: []model.Message{model.NewUserMessage("eta")}, Tools: tools}
		_, plan, err := m.buildParams(context.Background(), request)
		require.NoError(t, err)
		projected, err := projectRequestResponse(decodeResponse(t, clientSearchOutput), "gpt-5", plan)
		require.NoError(t, err)
		return request, projected
	}
	for _, tc := range []struct {
		name    string
		handler ToolSearchHandler
		ctx     func() context.Context
		want    string
		tools   string
	}{
		{name: "empty", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return []ToolSearchEntry{}, nil
		}, tools: `[]`},
		{name: "nil", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return nil, nil
		}, tools: `[]`},
		{name: "error", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return nil, errors.New("index unavailable")
		}, want: "index unavailable"},
		{name: "tools disabled", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return nil, errors.New("unexpected handler call")
		}, ctx: func() context.Context {
			return imodelrequest.WithToolsDisabled(context.Background())
		}, want: "tools are disabled"},
		{name: "nil tool", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return []ToolSearchEntry{{Name: "missing"}}, nil
		}, want: "nil tool"},
		{name: "typed nil tool", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			var missing *searchTestTool
			return []ToolSearchEntry{{Tool: missing}}, nil
		}, want: "nil tool"},
		{name: "duplicate", handler: func(_ context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return []ToolSearchEntry{req.Candidates[0], req.Candidates[0]}, nil
		}, want: "selected more than once"},
		{name: "duplicate wire identity", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return []ToolSearchEntry{
				{Namespace: "catalog", Name: "lookup", Tool: searchTestTool{name: "products_lookup"}},
				{Namespace: "catalog", Name: "lookup", Tool: searchTestTool{name: "inventory_lookup"}},
			}, nil
		}, want: "selected more than once"},
		{name: "colliding execution names", handler: func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
			return []ToolSearchEntry{
				{Namespace: "catalog", Name: "lookup", Tool: searchTestTool{name: "products"}},
				{Name: "catalog.lookup", Tool: searchTestTool{name: "inventory"}},
			}, nil
		}, want: "conflicts with execution key"},
		{name: "canceled", handler: func(ctx context.Context, _ *ToolSearchRequest) ([]ToolSearchEntry, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}, want: context.Canceled.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New("gpt-5", WithClientToolSearch(tc.handler))
			request, projected := pending(t, m)
			ctx := context.Background()
			if tc.ctx != nil {
				ctx = tc.ctx()
			}
			messages, err := m.CompleteProviderActions(ctx, request, projected)
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
				require.Nil(t, messages)
				return
			}
			require.NoError(t, err)
			require.Len(t, messages, 1)
			var output []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(messages[0].ProviderData.Payload, &output))
			require.JSONEq(t, tc.tools, string(output[0]["tools"]))
		})
	}

	hosted := New("gpt-5", WithHostedToolSearch())
	request, projected := pending(t, hosted)
	_, err := hosted.CompleteProviderActions(context.Background(), request, projected)
	require.ErrorContains(t, err, "client tool search is not configured")

	done := &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("hi")}}}
	messages, err := hosted.CompleteProviderActions(context.Background(), request, done)
	require.NoError(t, err)
	require.Nil(t, messages, "a response without client search calls requests no action")
}

func TestClientToolSearchResumeSkipsSavedResults(t *testing.T) {
	var handled []string
	m := New("gpt-5", WithClientToolSearch(func(_ context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
		handled = append(handled, req.CallID)
		require.Len(t, req.Candidates, 1, "the saved output already loaded the first tool")
		require.Equal(t, "second", req.Candidates[0].Name)
		return req.Candidates, nil
	}))
	response := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant, ProviderData: &model.ProviderData{
			Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
			{"type":"tool_search_call","id":"ts1","execution":"client","call_id":"s1","status":"completed","arguments":{"query":"first"}},
			{"type":"tool_search_call","id":"ts2","execution":"client","call_id":"s2","status":"completed","arguments":{"query":"second"}}]`),
		},
	}}}}
	saved := model.Message{Role: model.RoleTool, ToolID: "s1", ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
		{"type":"tool_search_output","execution":"client","call_id":"s1","status":"completed","tools":[
		{"type":"function","name":"first","defer_loading":true,"parameters":{"type":"object"}}]}]`),
	}}
	request := &model.Request{
		Messages: []model.Message{response.Choices[0].Message, saved},
		Tools: agentTools(t, []tool.Tool{
			deferred.Tool(searchTestTool{name: "first"}),
			deferred.Tool(searchTestTool{name: "second"}),
		}),
	}
	results, err := m.CompleteProviderActions(context.Background(), request, response)
	require.NoError(t, err)
	require.Equal(t, []string{"s2"}, handled)
	require.Len(t, results, 1)
	require.Equal(t, "s2", results[0].ToolID)
	request.Messages = append(request.Messages, results...)
	results, err = m.CompleteProviderActions(context.Background(), request, response)
	require.NoError(t, err)
	require.Empty(t, results)
	require.Equal(t, []string{"s2"}, handled, "saved results never rerun a handler")
	_, err = convertMessages(request.Messages, "gpt-5")
	require.NoError(t, err, "the recovered round can be sent to the model")
}

func TestClientToolSearchAcceptsStringArguments(t *testing.T) {
	var handled bool
	m := New("gpt-5", WithClientToolSearch(func(_ context.Context, req *ToolSearchRequest) ([]ToolSearchEntry, error) {
		handled = true
		require.Equal(t, "invoice", req.Query)
		require.JSONEq(t, `{"query":"invoice"}`, string(req.Arguments))
		return nil, nil
	}))
	response := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant, ProviderData: &model.ProviderData{
			Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
			{"type":"tool_search_call","execution":"client","call_id":"s1","status":"completed","arguments":"{\"query\":\"invoice\"}"}]`),
		},
	}}}}
	results, err := m.CompleteProviderActions(context.Background(), &model.Request{}, response)
	require.NoError(t, err)
	require.True(t, handled)
	require.Len(t, results, 1)
	require.Equal(t, "s1", results[0].ToolID)
	require.JSONEq(t, `[{"type":"tool_search_output","execution":"client","call_id":"s1","status":"completed","tools":[]}]`, string(results[0].ProviderData.Payload))
}

func TestClientToolSearchRecoveryRejectsInvalidSavedResults(t *testing.T) {
	var handled int
	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) {
		handled++
		return nil, nil
	}))
	pending := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant, ProviderData: &model.ProviderData{
			Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
			{"type":"tool_search_call","execution":"client","call_id":"s1","status":"completed","arguments":{"query":"invoice"}}]`),
		},
	}}}}
	for _, tc := range []struct {
		name, modelName, payload, want string
	}{
		{"different model", "other", `[]`, "invalid saved search result"},
		{"invalid item", "gpt-5", `[{"type":"function_call"}]`, "invalid saved search result"},
		{"different call", "gpt-5", `[{"type":"tool_search_output","execution":"client","call_id":"s2","status":"completed","tools":[]}]`, "does not answer call_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := model.Message{Role: model.RoleTool, ToolID: "s1", ProviderData: &model.ProviderData{
				Protocol: responseProvider, Model: tc.modelName, Payload: json.RawMessage(tc.payload),
			}}
			results, err := m.CompleteProviderActions(context.Background(), &model.Request{Messages: []model.Message{saved}}, pending)
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, results)
		})
	}
	_, err := m.CompleteProviderActions(context.Background(), nil, pending)
	require.ErrorContains(t, err, "requires the model request")
	partial := *pending
	partial.IsPartial = true
	results, err := m.CompleteProviderActions(context.Background(), &model.Request{}, &partial)
	require.NoError(t, err)
	require.Empty(t, results, "partial streaming output must not invoke a search handler")
	require.Zero(t, handled, "invalid saved state cannot silently cause a repeated search")
}

func TestToolSearchHistoryPairing(t *testing.T) {
	call := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5",
		Payload: json.RawMessage(`[{"type":"tool_search_call","id":"ts_1","execution":"client","call_id":"s1","status":"completed","arguments":{"query":"x"}}]`),
	}}
	output := func(callID string) model.Message {
		return model.Message{Role: model.RoleTool, ToolID: callID, ProviderData: &model.ProviderData{
			Protocol: responseProvider, Model: "gpt-5",
			Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"` + callID + `","status":"completed","tools":[]}]`),
		}}
	}
	hosted := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5",
		Payload: json.RawMessage(`[{"type":"tool_search_call","id":"ts_2","execution":"server","call_id":null,"status":"completed","arguments":{"paths":["crm"]}},` +
			`{"type":"tool_search_output","id":"tso_2","execution":"server","call_id":null,"status":"completed","tools":[]}]`),
	}}
	user := model.NewUserMessage("hi")
	_, err := convertMessages([]model.Message{user, call, output("s1"), hosted}, "gpt-5")
	require.NoError(t, err)
	_, err = convertMessages([]model.Message{user, call}, "gpt-5")
	require.ErrorContains(t, err, `client tool_search_call "s1" has no tool_search_output`)
	_, err = convertMessages([]model.Message{user, output("s1")}, "gpt-5")
	require.ErrorContains(t, err, "has no preceding tool_search_call")
	_, err = convertMessages([]model.Message{user, call, output("s1"), output("s1")}, "gpt-5")
	require.ErrorContains(t, err, "duplicate tool_search_output")
	mismatched := output("s1")
	mismatched.ToolID = "other"
	_, err = convertMessages([]model.Message{user, call, mismatched}, "gpt-5")
	require.ErrorContains(t, err, "may only record the client tool_search_output")
	onAssistant := output("s1")
	onAssistant.Role = model.RoleAssistant
	_, err = convertMessages([]model.Message{user, call, onAssistant}, "gpt-5")
	require.ErrorContains(t, err, "must be recorded on a tool message")
}

func TestStreamingToolSearchProjection(t *testing.T) {
	tools := agentTools(t, nil, deferred.ToolSet(crmToolSet()))
	stream := func(t *testing.T, m *Model, output string) *model.Response {
		var compact bytes.Buffer
		require.NoError(t, json.Compact(&compact, []byte(output)))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeSSE(w, `{"type":"response.created","response":{"id":"r","status":"in_progress","output":[]}}`,
				`{"type":"response.completed","response":{"id":"r","status":"completed","output":`+compact.String()+`}}`)
		}))
		defer srv.Close()
		m.client = New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithOpenAIOptions(option.WithMaxRetries(0))).client
		ch, err := m.GenerateContent(context.Background(), &model.Request{
			Messages:         []model.Message{model.NewUserMessage("hi")},
			Tools:            tools,
			GenerationConfig: model.GenerationConfig{Stream: true},
		})
		require.NoError(t, err)
		got := collect(t, ch)
		require.NotEmpty(t, got)
		final := got[len(got)-1]
		require.Nil(t, final.Error)
		return final
	}
	hosted := stream(t, New("gpt-5", WithHostedToolSearch()), `[
	  {"type":"tool_search_call","id":"ts","execution":"server","call_id":null,"status":"completed","arguments":{"paths":["crm"]}},
	  {"type":"tool_search_output","id":"tso","execution":"server","call_id":null,"status":"completed","tools":[]},
	  {"type":"function_call","id":"fc","status":"completed","call_id":"c1","namespace":"crm","name":"orders","arguments":"{}"}]`)
	require.True(t, hosted.Done)
	require.Equal(t, "crm_orders", hosted.Choices[0].Message.ToolCalls[0].Function.Name)

	handler := func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }
	client := stream(t, New("gpt-5", WithClientToolSearch(handler)), `[
	  {"type":"tool_search_call","id":"ts","execution":"client","call_id":"s1","status":"completed","arguments":{"query":"orders"}}]`)
	require.False(t, client.Done)
	require.False(t, client.IsPartial)
	require.NotNil(t, client.Choices[0].Message.ProviderData)
}

func TestNonStreamingClientToolSearchResponseIsNotDone(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, clientSearchOutput)
	}))
	defer srv.Close()
	handler := func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }
	m := New("gpt-5", WithAPIKey("test"), WithBaseURL(srv.URL), WithClientToolSearch(handler))
	tools := agentTools(t, []tool.Tool{deferred.Tool(searchTestTool{name: "shipping_eta"})})
	ch, err := m.GenerateContent(context.Background(), &model.Request{Messages: []model.Message{model.NewUserMessage("eta")}, Tools: tools})
	require.NoError(t, err)
	got := collect(t, ch)
	require.Len(t, got, 1)
	require.Nil(t, got[0].Error)
	require.False(t, got[0].Done)
	require.False(t, got[0].IsFinalResponse())
	requireJSONField(t, `[{"type":"tool_search","execution":"client",
	  "description":"`+clientToolSearchDescription+`",
	  "parameters":{"type":"object","properties":{"query":{"type":"string","description":"The capability or task that the needed tools must support."}},
	    "required":["query"],"additionalProperties":false}}]`, body["tools"])
}
