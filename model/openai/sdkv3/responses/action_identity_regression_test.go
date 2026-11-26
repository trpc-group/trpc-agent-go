//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package responses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestActionBindingDoesNotTreatNamespaceMemberAsStandaloneSearch(t *testing.T) {
	const definitions = `[{"type":"function","name":"weather"}]`
	message := model.Message{Role: model.RoleAssistant, ProviderData: &model.ProviderData{
		Protocol: responseProvider, Model: "gpt-5", Payload: json.RawMessage(`[
		{"type":"function_call","call_id":"call_1","namespace":"weather","name":"weather","arguments":"{}"}]`),
	}}

	t.Run("namespace member", func(t *testing.T) {
		request := &model.Request{
			Messages: []model.Message{bindingSearchResult("search_1", definitions)},
			Tools: agentTools(t, nil, searchTestToolSet{name: "weather", tools: []tool.Tool{
				searchTestTool{name: "weather"},
			}}),
		}
		message.ToolCalls = []model.ToolCall{{ID: "call_1", Function: model.FunctionDefinitionParam{Name: "weather_weather"}}}
		bindings, err := resolveActionBindings(request, message)
		require.NoError(t, err)
		require.Empty(t, bindings, "the unrelated standalone search cannot bind the namespace member")
	})

	t.Run("standalone search", func(t *testing.T) {
		request := &model.Request{Messages: []model.Message{bindingSearchResult("search_1", definitions)}}
		message.ToolCalls = []model.ToolCall{{ID: "call_1", Function: model.FunctionDefinitionParam{Name: "weather"}}}
		bindings, err := resolveActionBindings(request, message)
		require.NoError(t, err)
		require.Equal(t, []providerdata.ActionBinding{{
			CallID: "call_1", Name: "weather", ActionID: "search_1",
			Identity: providerdata.ActionToolIdentity{Name: "weather"},
		}}, bindings)
	})
}
