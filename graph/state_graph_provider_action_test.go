//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type providerActionGraphModel struct{ done bool }

func (m *providerActionGraphModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	message := model.NewAssistantMessage("")
	message.ProviderData = &model.ProviderData{
		Protocol: "test.native",
		Model:    "native-model",
		Payload:  json.RawMessage(`[{"type":"action","id":"a1"}]`),
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{ID: "r1", Done: m.done, Choices: []model.Choice{{Message: message}}}
	close(ch)
	return ch, nil
}

func (m *providerActionGraphModel) Info() model.Info {
	return model.Info{Name: "native-model", ProviderDataProtocol: "test.native"}
}

func TestLLMNodeReportsUncompletedProviderActions(t *testing.T) {
	run := func(t *testing.T, llm model.Model) string {
		sg := NewStateGraph(MessagesStateSchema())
		sg.AddLLMNode("llm", llm, "", nil).SetEntryPoint("llm").SetFinishPoint("llm")
		compiled, err := sg.Compile()
		require.NoError(t, err)
		executor, err := NewExecutor(compiled)
		require.NoError(t, err)
		ch, err := executor.Execute(context.Background(), State{}, agent.NewInvocation())
		require.NoError(t, err)
		var failure string
		for ev := range ch {
			if ev.Error != nil {
				failure = ev.Error.Message
			}
		}
		return failure
	}
	require.Contains(t, run(t, &providerActionGraphModel{}), errMsgProviderActions,
		"a pending provider action must not become the node result")
	require.Empty(t, run(t, &providerActionGraphModel{done: true}),
		"a completed native response is still a valid node result")
}
