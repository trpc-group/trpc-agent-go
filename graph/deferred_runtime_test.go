//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/internal/agenttoolgraph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
)

func TestDeferredAgentToolGraphInterruptResume(t *testing.T) {
	const (
		lineage       = "deferred-agent-lineage"
		parentNS      = "deferred-agent-parent"
		childName     = "deferred-child"
		toolsNode     = "tools"
		childAskNode  = "ask"
		interruptKey  = "approval"
		approvalValue = "approved"
	)
	saver := checkpointinmemory.NewSaver()
	var prepareRuns, afterRuns int
	childSchema := graph.NewStateSchema().
		AddField(graph.StateKeyLastResponse, graph.StateField{Type: reflect.TypeOf("")})
	childGraph := graph.NewStateGraph(childSchema)
	childGraph.AddNode("prepare", func(context.Context, graph.State) (any, error) {
		prepareRuns++
		return graph.State{"prepared": true}, nil
	})
	childGraph.AddNode(childAskNode, func(ctx context.Context, state graph.State) (any, error) {
		value, err := graph.Interrupt(ctx, state, interruptKey, "approve child")
		if err != nil {
			return nil, err
		}
		text, _ := value.(string)
		return graph.State{graph.StateKeyLastResponse: "child:" + text}, nil
	})
	childCompiled := childGraph.AddEdge("prepare", childAskNode).
		SetEntryPoint("prepare").SetFinishPoint(childAskNode).MustCompile()
	child, err := graphagent.New(childName, childCompiled, graphagent.WithCheckpointSaver(saver))
	require.NoError(t, err)

	parentSchema := graph.MessagesStateSchema().
		AddField("result", graph.StateField{Type: reflect.TypeOf("")})
	parentGraph := graph.NewStateGraph(parentSchema)
	parentGraph.AddToolsNode(toolsNode, map[string]tool.Tool{
		childName: deferred.Tool(agenttool.NewTool(child), nil),
	})
	parentGraph.AddNode("after", func(_ context.Context, state graph.State) (any, error) {
		afterRuns++
		var result string
		response, _ := state[graph.StateKeyLastToolResponse].(string)
		if err := json.Unmarshal([]byte(response), &result); err != nil {
			return nil, err
		}
		return graph.State{"result": result}, nil
	})
	parentCompiled := parentGraph.AddEdge(toolsNode, "after").
		SetEntryPoint(toolsNode).SetFinishPoint("after").MustCompile()
	parent, err := graphagent.New("parent", parentCompiled, graphagent.WithCheckpointSaver(saver))
	require.NoError(t, err)

	initial := agent.NewInvocation(
		agent.WithInvocationID("initial"),
		agent.WithInvocationMessage(model.NewUserMessage("start")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithRuntimeState(map[string]any{
			graph.CfgKeyLineageID:    lineage,
			graph.CfgKeyCheckpointNS: parentNS,
			graph.StateKeyMessages: []model.Message{{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "call-child", Type: "function",
					Function: model.FunctionDefinitionParam{Name: childName, Arguments: []byte(`{"request":"start"}`)},
				}},
			}},
		}))),
	)
	events, err := parent.Run(agent.NewInvocationContext(context.Background(), initial), initial)
	require.NoError(t, err)
	for range events {
	}
	require.Equal(t, 1, prepareRuns)
	require.Equal(t, 0, afterRuns)

	manager := parent.Executor().CheckpointManager()
	require.NotNil(t, manager)
	parentTuples, err := manager.ListCheckpoints(context.Background(), graph.CreateCheckpointConfig(lineage, "", parentNS), nil)
	require.NoError(t, err)
	childTuples, err := manager.ListCheckpoints(context.Background(), graph.CreateCheckpointConfig(lineage, "", childName), nil)
	require.NoError(t, err)
	var parentInterrupt, childInterrupt *graph.CheckpointTuple
	for _, tuple := range parentTuples {
		if tuple != nil && tuple.Checkpoint != nil && tuple.Checkpoint.InterruptState != nil && tuple.Checkpoint.InterruptState.NodeID == toolsNode {
			parentInterrupt = tuple
		}
	}
	for _, tuple := range childTuples {
		if tuple != nil && tuple.Checkpoint != nil && tuple.Checkpoint.InterruptState != nil && tuple.Checkpoint.InterruptState.NodeID == childAskNode {
			childInterrupt = tuple
		}
	}
	require.NotNil(t, parentInterrupt, "the parent must checkpoint the interrupted tool call")
	require.NotNil(t, childInterrupt, "the child must checkpoint the interrupt")

	resume := agent.NewInvocation(
		agent.WithInvocationID("resume"),
		agent.WithInvocationMessage(model.NewUserMessage("resume")),
		agent.WithInvocationRunOptions(agent.NewRunOptions(agent.WithRuntimeState(map[string]any{
			graph.CfgKeyLineageID:    lineage,
			graph.CfgKeyCheckpointNS: parentNS,
			graph.CfgKeyCheckpointID: parentInterrupt.Checkpoint.ID,
			graph.StateKeyCommand: &graph.Command{
				ResumeMap: map[string]any{interruptKey: approvalValue},
			},
		}))),
	)
	resumeEvents, err := parent.Run(agent.NewInvocationContext(context.Background(), resume), resume)
	require.NoError(t, err)
	var done *event.Event
	for ev := range resumeEvents {
		if ev != nil && ev.Done && ev.Object == graph.ObjectTypeGraphExecution {
			done = ev
		}
	}
	require.NotNil(t, done)
	var result string
	require.NoError(t, json.Unmarshal(done.StateDelta["result"], &result))
	require.Equal(t, "child:"+approvalValue, result)
	require.Equal(t, 1, prepareRuns, "resuming must not replay the completed child node")
	require.Equal(t, 1, afterRuns)
}

type deferredRuntimeTarget struct{ runtimeCalled bool }

func (t *deferredRuntimeTarget) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "outer"}
}

func (t *deferredRuntimeTarget) Call(context.Context, []byte) (any, error) {
	return "inner", nil
}

func (t *deferredRuntimeTarget) CallWithAgentToolGraphRuntime(
	context.Context, []byte, agenttoolgraph.RuntimeContext,
) (any, error) {
	t.runtimeCalled = true
	return "inner runtime", nil
}

type deferredUserWrapper struct {
	original *deferredRuntimeTarget
	called   bool
}

func (t *deferredUserWrapper) Declaration() *tool.Declaration {
	return t.original.Declaration()
}

func (t *deferredUserWrapper) Original() tool.Tool { return t.original }

func (t *deferredUserWrapper) Call(context.Context, []byte) (any, error) {
	t.called = true
	return "outer", nil
}

func TestDeferredToolKeepsUserWrapperCall(t *testing.T) {
	inner := &deferredRuntimeTarget{}
	outer := &deferredUserWrapper{original: inner}
	node := graph.NewToolsNodeFunc(map[string]tool.Tool{"outer": deferred.Tool(outer, nil)})
	state := graph.State{
		graph.StateKeyCurrentNodeID: "tools",
		graph.StateKeyMessages: []model.Message{{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID: "call-outer", Type: "function",
				Function: model.FunctionDefinitionParam{Name: "outer", Arguments: []byte(`{}`)},
			}},
		}},
	}
	invocation := agent.NewInvocation(agent.WithInvocationID("parent"))
	result, err := node(agent.NewInvocationContext(context.Background(), invocation), state)
	require.NoError(t, err)
	require.Equal(t, `"outer"`, result.(graph.State)[graph.StateKeyLastToolResponse])
	require.True(t, outer.called)
	require.False(t, inner.runtimeCalled, "user-defined Original methods are not unwrapped")
}
