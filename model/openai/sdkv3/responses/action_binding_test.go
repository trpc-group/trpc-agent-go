//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

func bindingSearchResult(id, definitions string) model.Message {
	return model.Message{Role: model.RoleTool, ToolID: id, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5",
		Payload: json.RawMessage(`[{"type":"tool_search_output","execution":"client","call_id":"` + id + `","tools":` + definitions + `}]`),
	}}
}

func TestToolSearchPlanningAndProjectionKeepEagerMemberIdentity(t *testing.T) {
	request := &model.Request{Tools: agentTools(t, nil, billingToolSet())}
	plan, err := New("gpt-5", WithHostedToolSearch()).planTools(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, "billing_lookup", mustFunctionName(t, plan, "billing", "lookup"))
	require.Equal(t, "billing_lookup", mustFunctionName(t, projectionToolPlan(request), "billing", "lookup"))
}

func TestClientToolSearchReoffersOnlyLatestExpiredBinding(t *testing.T) {
	definitions := `[{"type":"namespace","name":"billing","tools":[{"type":"function","name":"lookup"}]}]`
	request := &model.Request{
		Messages: []model.Message{bindingSearchResult("old", definitions)},
		Tools:    agentTools(t, nil, deferred.ToolSet(billingToolSet())),
	}
	ctx := providerdata.WithActionTools(context.Background(), &providerdata.ActionTools{
		Expired: func(id string, identity providerdata.ActionToolIdentity) bool {
			return id == "old" && identity == (providerdata.ActionToolIdentity{Namespace: "billing", Name: "lookup"})
		},
	})
	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }))
	plan, err := m.planTools(ctx, request)
	require.NoError(t, err)
	require.Len(t, plan.deferred, 1, "the old external key expired even though the registered key changed")
	require.Equal(t, "lookup", plan.deferred[0].Name)
	request.Messages = append(request.Messages, bindingSearchResult("new", definitions))
	plan, err = m.planTools(ctx, request)
	require.NoError(t, err)
	require.Empty(t, plan.deferred, "reselection supersedes an older expired binding")
}

func TestActionBindingResolverSeparatesLiteralAndNamespacedIdentities(t *testing.T) {
	qualified := `{"type":"namespace","name":"crm","tools":[{"type":"function","name":"orders"}]}`
	literal := `{"type":"function","name":"crm.orders"}`
	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }))
	for _, tc := range []struct {
		name          string
		messages      []model.Message
		literalOrigin string
	}{
		{"namespace first in batch", []model.Message{bindingSearchResult("one", `[`+qualified+`,`+literal+`]`)}, "one"},
		{"literal first in batch", []model.Message{bindingSearchResult("one", `[`+literal+`,`+qualified+`]`)}, "one"},
		{"later literal", []model.Message{bindingSearchResult("one", `[`+qualified+`]`), bindingSearchResult("two", `[`+literal+`]`)}, "two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &model.Request{Messages: tc.messages, Tools: agentTools(t, nil, crmToolSet())}
			message := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
				Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
				{"type":"function_call","call_id":"member","namespace":"crm","name":"orders","arguments":"{}"},
				{"type":"function_call","call_id":"literal","name":"crm.orders","arguments":"{}"}]`),
			}}
			bindings, err := providerdata.ResolveActionBindings(request, message)
			require.NoError(t, err)
			require.Equal(t, []providerdata.ActionBinding{
				{CallID: "member", Name: "crm_orders", ActionID: "one", Identity: providerdata.ActionToolIdentity{Namespace: "crm", Name: "orders"}},
				{CallID: "literal", Name: "crm.orders", ActionID: tc.literalOrigin, Identity: providerdata.ActionToolIdentity{Name: "crm.orders"}},
			}, bindings)
			projected, err := m.ProjectProviderDataWithRequest(request, message.ProviderData)
			require.NoError(t, err)
			for _, call := range projected.ToolCalls {
				require.Empty(t, call.ExtraFields)
			}
		})
	}
}

func TestClientSearchRejectsConflictingRegisteredAliases(t *testing.T) {
	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }))
	request := &model.Request{Tools: agentTools(t, nil, crmToolSet())}
	plan, err := m.planTools(context.Background(), request)
	require.NoError(t, err)
	message, err := m.toolSearchOutputMessage(context.Background(), "one", []ToolSearchEntry{
		{Namespace: "crm", Name: "orders", Tool: searchTestTool{name: "sandbox"}},
		{Name: "crm_orders", Tool: searchTestTool{name: "other"}},
	}, plan, map[wireFunction]bool{})
	require.ErrorContains(t, err, `conflicts with execution key "crm_orders"`)
	require.Nil(t, message.ProviderData, "a conflicting result cannot be committed")
}

func TestToolSearchHistoryPreservesLiteralAfterToolSetRemoval(t *testing.T) {
	const tools = `[{"type":"namespace","name":"crm","tools":[{"type":"function","name":"orders"}]},{"type":"function","name":"crm.orders"}]`
	req := &model.Request{Messages: []model.Message{bindingSearchResult("search_1", tools)}}
	m := New("gpt-5", WithClientToolSearch(func(context.Context, *ToolSearchRequest) ([]ToolSearchEntry, error) { return nil, nil }))
	plan, err := m.planTools(context.Background(), req)
	require.NoError(t, err)
	for _, p := range []*toolPlan{plan, projectionToolPlan(req)} {
		require.Equal(t, "crm.orders", mustFunctionName(t, p, "", "crm.orders"))
		_, err := p.functionName("crm", "orders")
		require.ErrorContains(t, err, "ambiguous")
	}
}

func TestHostedSearchSupersedesOnlyEarlierBindingsForReportedIdentities(t *testing.T) {
	const flat = `{"type":"function","name":"lookup"}`
	const member = `{"type":"namespace","name":"billing","tools":[{"type":"function","name":"lookup"}]}`
	const hosted = `{"type":"tool_search_output","execution":"server","tools":[` + flat + `]}`
	const before = `{"type":"function_call","call_id":"before","name":"lookup","arguments":"{}"}`
	const after = `{"type":"function_call","call_id":"after","name":"lookup","arguments":"{}"}`
	const other = `{"type":"function_call","call_id":"other","namespace":"billing","name":"lookup","arguments":"{}"}`
	req := &model.Request{Messages: []model.Message{bindingSearchResult("old", "["+flat+","+member+"]")}, Tools: agentTools(t, nil, billingToolSet())}
	msg := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{Protocol: responseProvider, Model: "target",
		Payload: json.RawMessage("[" + before + "," + hosted + "," + after + "," + other + "]")}}
	bindings, err := providerdata.ResolveActionBindings(req, msg)
	require.NoError(t, err)
	require.Len(t, bindings, 2)
	require.Equal(t, "before", bindings[0].CallID)
	require.Equal(t, "other", bindings[1].CallID)
	require.Equal(t, "old", bindings[1].ActionID)
}
