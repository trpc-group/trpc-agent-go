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
	callOptsTestNodeDeep   = "deep"
	callOptsTestInvID      = "inv-call-opts"
	callOptsTestUserInput  = "hi"
	callOptsTestTempGlobal = 0.1
	callOptsTestTempNode   = 0.9
	callOptsTestTopP       = 0.8
	callOptsTestPresence   = 1.1
	callOptsTestFrequency  = 1.2
	callOptsTestMaxTokens  = 42
	callOptsTestThinkTok   = 7
	callOptsTestStopA      = "A"
	callOptsTestStopB      = "B"
	callOptsTestEffort     = "high"
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

func TestMergeGenPatch_AllFields(t *testing.T) {
	base := model.GenerationConfigPatch{
		Stop: []string{callOptsTestStopA},
	}
	override := model.GenerationConfigPatch{
		MaxTokens:        model.IntPtr(callOptsTestMaxTokens),
		Temperature:      model.Float64Ptr(callOptsTestTempNode),
		TopP:             model.Float64Ptr(callOptsTestTopP),
		Stream:           model.BoolPtr(true),
		Stop:             []string{callOptsTestStopB},
		PresencePenalty:  model.Float64Ptr(callOptsTestPresence),
		FrequencyPenalty: model.Float64Ptr(callOptsTestFrequency),
		ReasoningEffort:  model.StringPtr(callOptsTestEffort),
		ThinkingEnabled:  model.BoolPtr(true),
		ThinkingTokens:   model.IntPtr(callOptsTestThinkTok),
	}
	got := mergeGenPatch(base, override)
	require.NotNil(t, got.MaxTokens)
	require.Equal(t, callOptsTestMaxTokens, *got.MaxTokens)
	require.NotNil(t, got.Temperature)
	require.Equal(t, callOptsTestTempNode, *got.Temperature)
	require.NotNil(t, got.TopP)
	require.Equal(t, callOptsTestTopP, *got.TopP)
	require.NotNil(t, got.Stream)
	require.True(t, *got.Stream)
	require.Equal(t, []string{callOptsTestStopB}, got.Stop)
	require.NotNil(t, got.PresencePenalty)
	require.Equal(t, callOptsTestPresence, *got.PresencePenalty)
	require.NotNil(t, got.FrequencyPenalty)
	require.Equal(t, callOptsTestFrequency, *got.FrequencyPenalty)
	require.NotNil(t, got.ReasoningEffort)
	require.Equal(t, callOptsTestEffort, *got.ReasoningEffort)
	require.NotNil(t, got.ThinkingEnabled)
	require.True(t, *got.ThinkingEnabled)
	require.NotNil(t, got.ThinkingTokens)
	require.Equal(t, callOptsTestThinkTok, *got.ThinkingTokens)

	override.Stop[0] = callOptsTestStopA
	require.Equal(t, []string{callOptsTestStopB}, got.Stop)
}

func TestGraphCallOptionsFromConfigs_ClonesValueType(t *testing.T) {
	original := callOptions{
		generation: model.GenerationConfigPatch{
			Stop: []string{callOptsTestStopA},
		},
		nodes: map[string]*callNodeOptions{
			callOptsTestNodeLLM: &callNodeOptions{
				generation: model.GenerationConfigPatch{
					Stop: []string{callOptsTestStopB},
				},
				child: &callOptions{
					nodes: map[string]*callNodeOptions{
						callOptsTestNodeDeep: &callNodeOptions{
							generation: model.GenerationConfigPatch{
								Temperature: model.Float64Ptr(
									callOptsTestTempGlobal,
								),
							},
						},
					},
				},
			},
		},
	}
	cfgs := map[string]any{
		graphCallOptionsKey: original,
	}
	cloned := graphCallOptionsFromConfigs(cfgs)
	require.NotNil(t, cloned)
	require.Equal(t, []string{callOptsTestStopA}, cloned.generation.Stop)
	require.NotNil(t, cloned.nodes[callOptsTestNodeLLM])
	require.Equal(
		t,
		[]string{callOptsTestStopB},
		cloned.nodes[callOptsTestNodeLLM].generation.Stop,
	)
	require.NotNil(t, cloned.nodes[callOptsTestNodeLLM].child)
	require.NotNil(
		t,
		cloned.nodes[callOptsTestNodeLLM].child.nodes[callOptsTestNodeDeep],
	)

	original.generation.Stop[0] = callOptsTestStopB
	original.nodes[callOptsTestNodeLLM].generation.Stop[0] =
		callOptsTestStopA
	require.Equal(t, []string{callOptsTestStopA}, cloned.generation.Stop)
	require.Equal(
		t,
		[]string{callOptsTestStopB},
		cloned.nodes[callOptsTestNodeLLM].generation.Stop,
	)
}

func TestWithScopedGraphCallOptions_RemovesKeyWhenEmpty(t *testing.T) {
	cfgs := map[string]any{
		graphCallOptionsKey: &callOptions{
			nodes: map[string]*callNodeOptions{
				callOptsTestNodeChild: &callNodeOptions{},
			},
		},
	}
	require.Nil(
		t,
		withScopedGraphCallOptions(cfgs, callOptsTestNodeChild),
	)
}

func TestNewCallOptions_EmptyAndNilOpt(t *testing.T) {
	require.Nil(t, newCallOptions())
	require.Nil(t, newCallOptions(nil))
}

func TestDesignateNode_SetsChildNodes(t *testing.T) {
	opts := newCallOptions(
		DesignateNode(
			callOptsTestNodeChild,
			WithCallGenerationConfigPatch(model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callOptsTestMaxTokens),
			}),
			DesignateNode(
				callOptsTestNodeLLM,
				WithCallGenerationConfigPatch(model.GenerationConfigPatch{
					Temperature: model.Float64Ptr(callOptsTestTempNode),
				}),
			),
		),
	)
	require.NotNil(t, opts)
	child := opts.nodes[callOptsTestNodeChild]
	require.NotNil(t, child)
	require.NotNil(t, child.generation.MaxTokens)
	require.Equal(t, callOptsTestMaxTokens, *child.generation.MaxTokens)
	require.NotNil(t, child.child)
	require.NotNil(t, child.child.nodes[callOptsTestNodeLLM])
	nested := child.child.nodes[callOptsTestNodeLLM]
	require.NotNil(t, nested.generation.Temperature)
	require.Equal(t, callOptsTestTempNode, *nested.generation.Temperature)
}

func TestMergeCallNodes_MergesExistingKey(t *testing.T) {
	a := map[string]*callNodeOptions{
		callOptsTestNodeLLM: &callNodeOptions{
			generation: model.GenerationConfigPatch{
				MaxTokens: model.IntPtr(callOptsTestMaxTokens),
				Stop:      []string{callOptsTestStopA},
			},
			child: &callOptions{
				generation: model.GenerationConfigPatch{
					Temperature: model.Float64Ptr(callOptsTestTempGlobal),
				},
			},
		},
		"skip-nil": nil,
		"skip-empty": &callNodeOptions{
			generation: model.GenerationConfigPatch{},
		},
	}
	b := map[string]*callNodeOptions{
		callOptsTestNodeLLM: &callNodeOptions{
			generation: model.GenerationConfigPatch{
				Temperature: model.Float64Ptr(callOptsTestTempNode),
				Stop:        []string{callOptsTestStopB},
			},
			child: &callOptions{
				generation: model.GenerationConfigPatch{
					ThinkingEnabled: model.BoolPtr(true),
				},
			},
		},
	}
	out := mergeCallNodes(a, b)
	require.NotNil(t, out)
	n := out[callOptsTestNodeLLM]
	require.NotNil(t, n)
	require.NotNil(t, n.generation.MaxTokens)
	require.Equal(t, callOptsTestMaxTokens, *n.generation.MaxTokens)
	require.NotNil(t, n.generation.Temperature)
	require.Equal(t, callOptsTestTempNode, *n.generation.Temperature)
	require.Equal(t, []string{callOptsTestStopB}, n.generation.Stop)
	require.NotNil(t, n.child)
	require.NotNil(t, n.child.generation.Temperature)
	require.Equal(t, callOptsTestTempGlobal, *n.child.generation.Temperature)
	require.NotNil(t, n.child.generation.ThinkingEnabled)
	require.True(t, *n.child.generation.ThinkingEnabled)

	b[callOptsTestNodeLLM].generation.Stop[0] = callOptsTestStopA
	require.Equal(t, []string{callOptsTestStopB}, n.generation.Stop)
}

func TestMergeCallOptions_NilSidesAndEmpty(t *testing.T) {
	require.Nil(t, mergeCallOptions(nil, nil))
	require.Nil(t, mergeCallOptions(&callOptions{}, &callOptions{}))

	a := &callOptions{
		generation: model.GenerationConfigPatch{
			Stop: []string{callOptsTestStopA},
		},
	}
	b := &callOptions{
		generation: model.GenerationConfigPatch{
			Stop: []string{callOptsTestStopB},
		},
	}
	require.NotNil(t, mergeCallOptions(nil, a))
	require.NotNil(t, mergeCallOptions(b, nil))
	require.NotNil(t, mergeCallOptions(a, b))
}

func TestWithCallGenerationConfigPatch_NilReceiver(t *testing.T) {
	WithCallGenerationConfigPatch(model.GenerationConfigPatch{
		Temperature: model.Float64Ptr(callOptsTestTempGlobal),
	})(nil)
}

func TestCallOptions_EarlyReturnPaths(t *testing.T) {
	var nilCallOpts *callOptions
	require.True(t, nilCallOpts.isEmpty())

	require.Equal(
		t,
		model.GenerationConfigPatch{},
		generationPatchForNode(nil, callOptsTestNodeLLM),
	)

	empty := &callOptions{}
	require.Equal(
		t,
		model.GenerationConfigPatch{},
		generationPatchForNode(empty, callOptsTestNodeLLM),
	)

	noMatch := &callOptions{
		nodes: map[string]*callNodeOptions{
			"other": &callNodeOptions{},
		},
	}
	require.Equal(
		t,
		model.GenerationConfigPatch{},
		generationPatchForNode(noMatch, callOptsTestNodeLLM),
	)

	DesignateNode("")(empty)
	DesignateNode(callOptsTestNodeLLM)(nil)
	DesignateNodeWithPath(nil)(empty)
	DesignateNodeWithPath(NodePath{""})(empty)

	WithCallOptions()(&agent.RunOptions{})
	WithCallOptions(
		WithCallGenerationConfigPatch(model.GenerationConfigPatch{
			Temperature: model.Float64Ptr(callOptsTestTempGlobal),
		}),
	)(nil)
}

func TestCallOptions_CloneHelpers_EdgeCases(t *testing.T) {
	require.Nil(t, cloneCallOptions(nil))
	require.Nil(t, cloneCallOptions(&callOptions{}))
	require.Nil(t, cloneCallNodeOptions(nil))
	require.Nil(t, cloneCallNodeOptions(&callNodeOptions{}))
	require.Nil(t, cloneCallNodeMap(nil))
	require.Nil(t, cloneCallNodeMap(map[string]*callNodeOptions{}))
	require.Nil(t, cloneCallNodeMap(map[string]*callNodeOptions{
		"x": nil,
		"y": &callNodeOptions{},
	}))
	require.Nil(t, graphCallOptionsFromConfigs(nil))
	require.Nil(t, graphCallOptionsFromConfigs(map[string]any{
		graphCallOptionsKey: 123,
	}))

	parent := &callOptions{
		generation: model.GenerationConfigPatch{
			Stop: []string{callOptsTestStopA},
		},
	}
	scoped := scopeCallOptionsForSubgraph(parent, "")
	require.NotNil(t, scoped)
	parent.generation.Stop[0] = callOptsTestStopB
	require.Equal(t, []string{callOptsTestStopA}, scoped.generation.Stop)
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
