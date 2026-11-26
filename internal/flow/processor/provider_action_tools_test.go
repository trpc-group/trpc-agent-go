//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type actionBindingModel struct{ plainNativeModel }

func (actionBindingModel) Info() model.Info {
	return model.Info{Name: "native-model", ProviderDataProtocol: "test.action-bindings"}
}

func (actionBindingModel) CompleteProviderActions(context.Context, *model.Request, *model.Response) ([]model.Message, error) {
	return nil, nil
}

func (actionBindingModel) ProjectProviderData(data *model.ProviderData) (model.Message, error) {
	return model.Message{Role: model.RoleAssistant, ProviderData: data}, nil
}

func (m actionBindingModel) ProjectProviderDataWithRequest(_ *model.Request, data *model.ProviderData) (model.Message, error) {
	return m.ProjectProviderData(data)
}

type forwardingActionBindingsModel struct{ model.Model }

func (m forwardingActionBindingsModel) CompleteProviderActions(ctx context.Context, request *model.Request, response *model.Response) ([]model.Message, error) {
	return m.Model.(model.ProviderActionCompleter).CompleteProviderActions(ctx, request, response)
}

func (m forwardingActionBindingsModel) ProjectProviderData(data *model.ProviderData) (model.Message, error) {
	return m.Model.(model.ProviderDataProjector).ProjectProviderData(data)
}

func (m forwardingActionBindingsModel) ProjectProviderDataWithRequest(request *model.Request, data *model.ProviderData) (model.Message, error) {
	return m.Model.(model.ProviderDataRequestProjector).ProjectProviderDataWithRequest(request, data)
}

func TestExecutionRequestUsesExactActionBindingAndPreservesCallbackNames(t *testing.T) {
	registered := unavailableActionTool("registered")
	literal := unavailableActionTool("literal")
	callback := unavailableActionTool("callback")
	m := forwardingActionBindingsModel{Model: actionBindingModel{}}
	providerdata.RegisterActionDecoder(m.Info().ProviderDataProtocol, func(*model.Request, model.Message) ([]providerdata.ActionBinding, error) {
		return []providerdata.ActionBinding{
			{CallID: "member", Name: "crm_orders", ActionID: "old", Identity: providerdata.ActionToolIdentity{Namespace: "crm", Name: "orders"}},
			{CallID: "literal", Name: "crm.orders", ActionID: "new", Identity: providerdata.ActionToolIdentity{Name: "crm.orders"}},
		}, nil
	}, nil, nil)
	inv := agent.NewInvocation(agent.WithInvocationModel(m))
	state := actionToolsForInvocation(inv)
	stored, err := json.Marshal(map[string]bool{actionIdentityKey(providerdata.ActionToolIdentity{Namespace: "crm", Name: "orders"}): true})
	require.NoError(t, err)
	state.saved = map[string]json.RawMessage{"old": stored}
	state.live["new"] = map[string]tool.Tool{actionIdentityKey(providerdata.ActionToolIdentity{Name: "crm.orders"}): literal}
	request := &model.Request{Tools: map[string]tool.Tool{
		"crm_orders": registered, "callback": callback,
	}}
	response := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant, ProviderData: providerActionData(`[]`), ToolCalls: []model.ToolCall{
			{ID: "member", Function: model.FunctionDefinitionParam{Name: "crm_orders"}},
			{ID: "literal", Function: model.FunctionDefinitionParam{Name: "crm.orders"}},
		},
	}}}}
	response.Choices[0].Message.ProviderData.Protocol = m.Info().ProviderDataProtocol
	ctx, err := withActionExecutionBindings(context.Background(), inv, request, response)
	require.NoError(t, err)
	memberTools := executionToolsForCall(ctx, response.Choices[0].Message.ToolCalls[0], request.Tools)
	literalTools := executionToolsForCall(ctx, response.Choices[0].Message.ToolCalls[1], request.Tools)
	_, err = memberTools["crm_orders"].(tool.CallableTool).Call(context.Background(), nil)
	require.ErrorContains(t, err, `tool "crm_orders" no longer has its selected execution binding`)
	require.Equal(t, literal, literalTools["crm.orders"], "the later literal binding remains separate")
	require.Equal(t, registered, request.Tools["crm_orders"], "execution does not mutate the model request")
	require.NotContains(t, request.Tools, "crm.orders")

	response.Choices[0].Message.ToolCalls[0].Function.Name = "callback"
	ctx, err = withActionExecutionBindings(ctx, inv, request, response)
	require.NoError(t, err)
	require.Equal(t, registered, request.Tools["crm_orders"], "an AfterModel name edit bypasses the original binding")
	require.Equal(t, callback, request.Tools["callback"])
	callbackTools := executionToolsForCall(ctx, response.Choices[0].Message.ToolCalls[0], request.Tools)
	require.Equal(t, callback, callbackTools["callback"])

	next := response.Clone()
	next.Choices[0].Message.ProviderData = nil
	ctx, err = withActionExecutionBindings(ctx, inv, request, next)
	require.NoError(t, err)
	memberTools = executionToolsForCall(ctx, response.Choices[0].Message.ToolCalls[1], request.Tools)
	require.NotContains(t, memberTools, "crm.orders", "bindings do not survive the next response")
}

func TestSearchBindingUsesProducerOfResultInSelectedHistory(t *testing.T) {
	identity := providerdata.ActionToolIdentity{Name: "lookup"}
	first := providerActionResult("search-1")
	first.ProviderData.Model = "model-a"
	second := providerActionResult("search-1")
	resultEvent := func(msg model.Message, selection tool.Tool) event.Event {
		e := event.NewResponseEvent("run", "agent", &model.Response{Done: true,
			Choices: []model.Choice{{Message: msg}}})
		recordActionToolKeys(e, map[string]tool.Tool{actionIdentityKey(identity): selection})
		return *e
	}
	// The session contains two producers' results. Each request selects one.
	sess := &session.Session{Events: []event.Event{
		resultEvent(first, unavailableActionTool("sandbox")),
		resultEvent(second, nil),
	}}
	inv := agent.NewInvocation(agent.WithInvocationModel(plainNativeModel{}), agent.WithInvocationSession(sess))
	inv.AgentName = "agent"
	for _, tc := range []struct {
		result  model.Message
		expired bool
	}{{first, true}, {second, false}} {
		req := &model.Request{Messages: []model.Message{tc.result}}
		state := actionToolsForInvocation(inv)
		ctx, _, err := state.collect(context.Background(), inv, req, []model.Message{})
		require.NoError(t, err)
		actions := providerdata.ActionToolsFromContext(ctx)
		require.Equal(t, tc.expired, actions.Expired("search-1", identity))
	}
}
