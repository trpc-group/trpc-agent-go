//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	callOptsTestKeyOther   = "other"
	callOptsTestValOther   = "v"
	callOptsTestNodeChild  = "child"
	callOptsTestNodeLLM    = "llm"
	callOptsTestInvID      = "inv-call-opts"
	callOptsTestUserInput  = "hi"
	callOptsTestTempGlobal = 0.1
	callOptsTestTempNode   = 0.9
	callOptsTestMaxTokens  = 42
)

func TestWithCallOptions_MergesCustomAgentConfigs(t *testing.T) {
	runOpts := agent.RunOptions{
		CustomAgentConfigs: map[string]any{
			callOptsTestKeyOther: callOptsTestValOther,
		},
	}
	WithCallOptions(
		WithCallGenerationConfigPatch(model.GenerationConfigPatch{
			Temperature: model.Float64Ptr(callOptsTestTempGlobal),
		}),
	)(&runOpts)

	require.Equal(
		t,
		callOptsTestValOther,
		runOpts.CustomAgentConfigs[callOptsTestKeyOther],
	)
	opts := graphCallOptionsFromConfigs(runOpts.CustomAgentConfigs)
	require.NotNil(t, opts)
	patch := generationPatchForNode(opts, "")
	require.NotNil(t, patch.Temperature)
	require.Equal(t, callOptsTestTempGlobal, *patch.Temperature)

	WithCallOptions(
		DesignateNode(
			callOptsTestNodeLLM,
			WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callOptsTestMaxTokens),
			}),
		),
	)(&runOpts)

	opts = graphCallOptionsFromConfigs(runOpts.CustomAgentConfigs)
	require.NotNil(t, opts)
	patch = generationPatchForNode(opts, callOptsTestNodeLLM)
	require.NotNil(t, patch.Temperature)
	require.Equal(t, callOptsTestTempGlobal, *patch.Temperature)
	require.NotNil(t, patch.MaxTokens)
	require.Equal(t, callOptsTestMaxTokens, *patch.MaxTokens)
}

func TestScopeCallOptionsForSubgraph(t *testing.T) {
	runOpts := agent.RunOptions{}
	WithCallOptions(
		WithCallGenerationConfigPatch(model.GenerationConfigPatch{
			Temperature: model.Float64Ptr(callOptsTestTempGlobal),
		}),
		DesignateNode(
			callOptsTestNodeChild,
			WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callOptsTestMaxTokens),
			}),
		),
		DesignateNodeWithPath(
			NodePath{callOptsTestNodeChild, callOptsTestNodeLLM},
			WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				Temperature: model.Float64Ptr(callOptsTestTempNode),
			}),
		),
	)(&runOpts)

	parent := graphCallOptionsFromConfigs(runOpts.CustomAgentConfigs)
	require.NotNil(t, parent)

	child := scopeCallOptionsForSubgraph(parent, callOptsTestNodeChild)
	require.NotNil(t, child)

	patch := generationPatchForNode(child, callOptsTestNodeLLM)
	require.NotNil(t, patch.MaxTokens)
	require.Equal(t, callOptsTestMaxTokens, *patch.MaxTokens)
	require.NotNil(t, patch.Temperature)
	require.Equal(t, callOptsTestTempNode, *patch.Temperature)
}

func TestCallOptions_AppliedToNestedSubgraph(t *testing.T) {
	childModel := &captureModel{}
	childGraph, err := NewStateGraph(MessagesStateSchema()).
		AddLLMNode(callOptsTestNodeLLM, childModel, "inst", nil).
		SetEntryPoint(callOptsTestNodeLLM).
		SetFinishPoint(callOptsTestNodeLLM).
		Compile()
	require.NoError(t, err)
	childExec, err := NewExecutor(childGraph)
	require.NoError(t, err)

	childAgent := &checkpointGraphAgent{
		name: callOptsTestNodeChild,
		exec: childExec,
	}
	parent := &parentWithSubAgent{a: childAgent}
	parentGraph, err := NewStateGraph(MessagesStateSchema()).
		AddAgentNode(callOptsTestNodeChild).
		SetEntryPoint(callOptsTestNodeChild).
		SetFinishPoint(callOptsTestNodeChild).
		Compile()
	require.NoError(t, err)
	parentExec, err := NewExecutor(parentGraph)
	require.NoError(t, err)

	runOpts := agent.RunOptions{}
	WithCallOptions(
		WithCallGenerationConfigPatch(model.GenerationConfigPatch{
			Temperature: model.Float64Ptr(callOptsTestTempGlobal),
		}),
		DesignateNode(
			callOptsTestNodeChild,
			WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callOptsTestMaxTokens),
			}),
		),
		DesignateNodeWithPath(
			NodePath{callOptsTestNodeChild, callOptsTestNodeLLM},
			WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				Temperature: model.Float64Ptr(callOptsTestTempNode),
			}),
		),
	)(&runOpts)

	initial := State{
		StateKeyParentAgent: parent,
		StateKeyUserInput:   callOptsTestUserInput,
	}
	inv := agent.NewInvocation(
		agent.WithInvocationID(callOptsTestInvID),
		agent.WithInvocationRunOptions(runOpts),
		agent.WithInvocationMessage(model.NewUserMessage(callOptsTestUserInput)),
	)

	ch, err := parentExec.Execute(context.Background(), initial, inv)
	require.NoError(t, err)
	for range ch {
	}

	require.NotNil(t, childModel.lastReq)
	gen := childModel.lastReq.GenerationConfig
	require.NotNil(t, gen.MaxTokens)
	require.Equal(t, callOptsTestMaxTokens, *gen.MaxTokens)
	require.NotNil(t, gen.Temperature)
	require.Equal(t, callOptsTestTempNode, *gen.Temperature)
}
