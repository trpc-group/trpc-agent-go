//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type rawTerminalRecoveryAgent struct{ name string }

func (a *rawTerminalRecoveryAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		ch <- event.NewErrorEvent("", a.name, model.ErrorTypeFlowError, "child boom")
		ch <- event.NewResponseEvent(
			"",
			a.name,
			&model.Response{
				Object: model.ObjectTypeChatCompletion,
				Done:   true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("recovered"),
				}},
			},
			event.WithStateDelta(map[string][]byte{
				graph.MetadataKeyCompletion: []byte("{}"),
				graph.StateKeyLastResponse:  []byte(`"recovered"`),
				"child_value":               []byte(`"recovered"`),
			}),
		)
	}()
	return ch, nil
}

func (a *rawTerminalRecoveryAgent) Tools() []tool.Tool { return nil }

func (a *rawTerminalRecoveryAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *rawTerminalRecoveryAgent) SubAgents() []agent.Agent { return nil }

func (a *rawTerminalRecoveryAgent) FindSubAgent(name string) agent.Agent {
	if name == a.name {
		return a
	}
	return nil
}

func TestSubgraph_DisableGraphCompletionEvent_PreservesWrappedGraphAgentOutput(t *testing.T) {
	const (
		childNodeName     = "child_handoff"
		afterNodeName     = "after"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
		userInput         = "hello"
		valuePrefix       = "computed: "
	)

	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		input, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
		childValue := valuePrefix + input
		return graph.State{
			childValueKey:              childValue,
			graph.StateKeyLastResponse: childValue,
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	graphChild, err := graphagent.New("graph-child", childCompiled)
	require.NoError(t, err)
	wrappedChild := chainagent.New(
		childNodeName,
		chainagent.WithSubAgents([]agent.Agent{graphChild}),
	)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentGraph.AddNode(afterNodeName, func(ctx context.Context, state graph.State) (any, error) {
		value, ok := graph.GetStateValue[string](state, valueFromChildKey)
		if !ok {
			return nil, nil
		}
		return graph.State{graph.StateKeyLastResponse: value}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{wrappedChild}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var lastAssistant string
	for evt := range eventCh {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 {
			lastAssistant = evt.Response.Choices[0].Message.Content
		}
	}
	require.Equal(t, valuePrefix+userInput, lastAssistant)
}

func TestSubgraph_DisableGraphCompletionEvent_PreservesNestedCycleEscalation(t *testing.T) {
	const (
		childNodeName     = "cycle_child"
		afterNodeName     = "after"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
		userInput         = "hello"
		valuePrefix       = "computed: "
	)

	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		input, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
		childValue := valuePrefix + input
		return graph.State{
			childValueKey:              childValue,
			graph.StateKeyLastResponse: childValue,
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	graphChild, err := graphagent.New("graph-child", childCompiled)
	require.NoError(t, err)
	cycleChild := cycleagent.New(
		childNodeName,
		cycleagent.WithSubAgents([]agent.Agent{graphChild}),
		cycleagent.WithMaxIterations(2),
		cycleagent.WithEscalationFunc(func(evt *event.Event) bool {
			return evt != nil &&
				evt.Object == model.ObjectTypeChatCompletion &&
				len(evt.StateDelta) > 0
		}),
	)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentGraph.AddNode(afterNodeName, func(ctx context.Context, state graph.State) (any, error) {
		value, ok := graph.GetStateValue[string](state, valueFromChildKey)
		if !ok {
			return nil, nil
		}
		return graph.State{graph.StateKeyLastResponse: value}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{cycleChild}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var childVisibleCompletionCount int
	var lastAssistant string
	for evt := range eventCh {
		require.False(t, evt.Done && evt.Object == graph.ObjectTypeGraphExecution)
		if evt != nil &&
			evt.Object == model.ObjectTypeChatCompletion &&
			len(evt.StateDelta) > 0 &&
			evt.StateDelta[childValueKey] != nil {
			childVisibleCompletionCount++
		}
		if evt != nil && evt.Response != nil && len(evt.Response.Choices) > 0 {
			lastAssistant = evt.Response.Choices[0].Message.Content
		}
	}

	require.Equal(t, 1, childVisibleCompletionCount)
	require.Equal(t, valuePrefix+userInput, lastAssistant)
}

func TestSubgraph_RawTerminalSuccessClearsPriorTerminalError(t *testing.T) {
	const (
		childNodeName = "raw-child"
		afterNodeName = "after"
		childValueKey = "child_value"
	)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{childValueKey: value}
		}),
	)
	parentGraph.AddNode(afterNodeName, func(ctx context.Context, state graph.State) (any, error) {
		value, _ := graph.GetStateValue[string](state, childValueKey)
		return graph.State{graph.StateKeyLastResponse: "after:" + value}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{&rawTerminalRecoveryAgent{name: childNodeName}}),
	)
	require.NoError(t, err)

	eventCh, err := parentAgent.Run(
		context.Background(),
		agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("hello"))),
	)
	require.NoError(t, err)

	var lastAssistant string
	for evt := range eventCh {
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			lastAssistant = evt.Response.Choices[0].Message.Content
		}
	}

	require.Equal(t, "after:recovered", lastAssistant)
}

func TestSubgraph_DisableGraphCompletionEvent_DropsStaleOutputAfterChildAfterCallbackError(t *testing.T) {
	const (
		childNodeName     = "child_handoff"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			childValueKey:              "child-state",
			graph.StateKeyLastResponse: "child-state",
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		args *agent.AfterAgentArgs,
	) (*agent.AfterAgentResult, error) {
		return nil, errors.New("after callback failed")
	})
	childAgent, err := graphagent.New(
		childNodeName,
		childCompiled,
		graphagent.WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(childNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var sawCallbackError bool
	var sawStaleChildValue bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			evt.Error.Type == agent.ErrorTypeAgentCallbackError &&
			evt.Error.Message == "after callback failed" {
			sawCallbackError = true
		}
		if evt != nil &&
			evt.StateDelta != nil &&
			string(evt.StateDelta[valueFromChildKey]) == `"child-state"` {
			sawStaleChildValue = true
		}
	}

	require.True(t, sawCallbackError)
	require.False(t, sawStaleChildValue)
}

func TestSubgraph_DefaultCompatibility_PreservesOutputAfterChildAfterCallbackError(
	t *testing.T,
) {
	const (
		childNodeName     = "child_handoff"
		childValueKey     = "child_value"
		valueFromChildKey = "value_from_child"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(childValueKey, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("done", func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			childValueKey:              "child-state",
			graph.StateKeyLastResponse: "child-state",
		}, nil
	})
	childCompiled := childGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		args *agent.AfterAgentArgs,
	) (*agent.AfterAgentResult, error) {
		return nil, errors.New("after callback failed")
	})
	childAgent, err := graphagent.New(
		childNodeName,
		childCompiled,
		graphagent.WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{Type: reflect.TypeOf("")}).
		AddField(valueFromChildKey, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(
		childNodeName,
		graph.WithSubgraphOutputMapper(func(_ graph.State, result graph.SubgraphResult) graph.State {
			value, ok := graph.GetStateValue[string](result.FinalState, childValueKey)
			if !ok {
				return nil
			}
			return graph.State{valueFromChildKey: value}
		}),
	)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(childNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)

	eventCh, err := parentAgent.Run(
		context.Background(),
		agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("hello"))),
	)
	require.NoError(t, err)

	var sawCallbackError bool
	var sawMappedChildValue bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			evt.Error.Type == agent.ErrorTypeAgentCallbackError &&
			evt.Error.Message == "after callback failed" {
			sawCallbackError = true
		}
		if evt != nil &&
			evt.StateDelta != nil &&
			string(evt.StateDelta[valueFromChildKey]) == `"child-state"` {
			sawMappedChildValue = true
		}
	}

	require.True(t, sawCallbackError)
	require.True(t, sawMappedChildValue)
}

func TestSubgraph_DisableGraphExecutorEvents_ChildFailureStopsParentGraphWhenEnabled(t *testing.T) {
	const (
		childNodeName = "child"
		afterNodeName = "after"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	childAgent, err := graphagent.New(childNodeName, childCompiled)
	require.NoError(t, err)

	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentGraph.AddNode(afterNodeName, func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "after-ran"}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)

	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithDisableGraphExecutorEvents(true),
			agent.WithPropagateChildAgentErrors(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)

	var sawChildError bool
	var sawAfterResponse bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			strings.Contains(evt.Error.Message, "child boom") {
			sawChildError = true
		}
		if evt != nil &&
			evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == "after-ran" {
			sawAfterResponse = true
		}
	}

	require.True(t, sawChildError)
	require.False(t, sawAfterResponse)
}

func TestSubgraph_DisableGraphCompletionEvent_ChildFailureKeepsLegacyCompatibilityByDefault(
	t *testing.T,
) {
	const (
		childNodeName = "child"
		afterNodeName = "after"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	childAgent, err := graphagent.New(childNodeName, childCompiled)
	require.NoError(t, err)
	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentGraph.AddNode(afterNodeName, func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "after-ran"}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var sawChildError bool
	var sawAfterResponse bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			strings.Contains(evt.Error.Message, "child boom") {
			sawChildError = true
		}
		if evt != nil &&
			evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == "after-ran" {
			sawAfterResponse = true
		}
	}
	require.True(t, sawChildError)
	require.True(t, sawAfterResponse)
}

func TestSubgraph_DisableGraphCompletionEvent_ChildFailureStopsParentGraphWhenEnabled(
	t *testing.T,
) {
	const (
		childNodeName = "child"
		afterNodeName = "after"
	)
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	childAgent, err := graphagent.New(childNodeName, childCompiled)
	require.NoError(t, err)
	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentGraph.AddNode(afterNodeName, func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "after-ran"}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithPropagateChildAgentErrors(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var sawChildError bool
	var sawAfterResponse bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			strings.Contains(evt.Error.Message, "child boom") {
			sawChildError = true
		}
		if evt != nil &&
			evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == "after-ran" {
			sawAfterResponse = true
		}
	}
	require.True(t, sawChildError)
	require.False(t, sawAfterResponse)
}

func TestSubgraph_DisableGraphCompletionEvent_ParallelChildFailureStopsParentGraphWhenEnabled(
	t *testing.T,
) {
	const (
		childNodeName = "child"
		afterNodeName = "after"
	)
	failingSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	failingGraph := graph.NewStateGraph(failingSchema)
	failingGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	failingCompiled := failingGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	failingChild, err := graphagent.New("failing-child", failingCompiled)
	require.NoError(t, err)
	successSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	successGraph := graph.NewStateGraph(successSchema)
	successGraph.AddNode("done", func(context.Context, graph.State) (any, error) {
		time.Sleep(50 * time.Millisecond)
		return graph.State{graph.StateKeyLastResponse: "ok"}, nil
	})
	successCompiled := successGraph.SetEntryPoint("done").SetFinishPoint("done").MustCompile()
	successChild, err := graphagent.New("success-child", successCompiled)
	require.NoError(t, err)
	parallelChild := parallelagent.New(
		childNodeName,
		parallelagent.WithSubAgents([]agent.Agent{failingChild, successChild}),
	)
	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentGraph.AddNode(afterNodeName, func(context.Context, graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "after-ran"}, nil
	})
	parentGraph.AddEdge(childNodeName, afterNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(afterNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{parallelChild}),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphCompletionEvent(true),
			agent.WithPropagateChildAgentErrors(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var sawChildError bool
	var sawAfterResponse bool
	for evt := range eventCh {
		if evt != nil &&
			evt.Error != nil &&
			strings.Contains(evt.Error.Message, "child boom") {
			sawChildError = true
		}
		if evt != nil &&
			evt.Response != nil &&
			len(evt.Response.Choices) > 0 &&
			evt.Response.Choices[0].Message.Content == "after-ran" {
			sawAfterResponse = true
		}
	}
	require.True(t, sawChildError)
	require.False(t, sawAfterResponse)
}

func TestSubgraph_DisableGraphExecutorEvents_PreservesChildAfterCallbackCustomResponse(
	t *testing.T,
) {
	const childNodeName = "child"
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("child boom")
	})
	childCompiled := childGraph.SetEntryPoint("boom").SetFinishPoint("boom").MustCompile()
	callbacks := agent.NewCallbacks()
	callbacks.RegisterAfterAgent(func(
		ctx context.Context,
		args *agent.AfterAgentArgs,
	) (*agent.AfterAgentResult, error) {
		if args.Error == nil {
			return nil, nil
		}
		return &agent.AfterAgentResult{
			CustomResponse: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Done:   true,
				Choices: []model.Choice{{
					Message: model.NewAssistantMessage("recovered"),
				}},
			},
		}, nil
	})
	childAgent, err := graphagent.New(
		childNodeName,
		childCompiled,
		graphagent.WithAgentCallbacks(callbacks),
	)
	require.NoError(t, err)
	parentSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddAgentNode(childNodeName)
	parentCompiled := parentGraph.SetEntryPoint(childNodeName).SetFinishPoint(childNodeName).MustCompile()
	parentAgent, err := graphagent.New(
		"parent",
		parentCompiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)
	invocation := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithDisableGraphExecutorEvents(true),
		)),
	)
	eventCh, err := parentAgent.Run(context.Background(), invocation)
	require.NoError(t, err)
	var lastEvent *event.Event
	for evt := range eventCh {
		lastEvent = evt
	}
	require.NotNil(t, lastEvent)
	require.NotNil(t, lastEvent.Response)
	require.Nil(t, lastEvent.Error)
	require.Len(t, lastEvent.Response.Choices, 1)
	require.Equal(t, "recovered", lastEvent.Response.Choices[0].Message.Content)
}
