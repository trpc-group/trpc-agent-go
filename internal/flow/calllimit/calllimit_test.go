//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package calllimit

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestLLMFinalizationUsesCustomInstruction(t *testing.T) {
	invocation := agent.NewInvocation()
	instruction := "finish now"
	Configure(invocation, &instruction, nil)

	require.False(t, RecordLLMCall(invocation, 2))
	require.False(t, Active(invocation))
	require.True(t, RecordLLMCall(invocation, 2))

	got, ok := ActivateForLLM(invocation, true)
	require.True(t, ok)
	require.Equal(t, instruction, got)
	require.True(t, Active(invocation))

	Finish(invocation)
	require.False(t, Active(invocation))
}

func TestToolFinalizationUsesDefaultInstruction(t *testing.T) {
	invocation := agent.NewInvocation()
	instruction := ""
	Configure(invocation, nil, &instruction)

	require.False(t, RecordToolIteration(invocation, 2))
	require.True(t, RecordToolIteration(invocation, 2))
	ScheduleToolFinalization(invocation)

	got, ok := ActivateForLLM(invocation, false)
	require.True(t, ok)
	require.Equal(t, DefaultInstruction, got)
}

func TestLLMFinalizationTakesPriorityOverPendingToolFinalization(t *testing.T) {
	invocation := agent.NewInvocation()
	llmInstruction := "finish for LLM limit"
	toolInstruction := "finish for tool limit"
	Configure(invocation, &llmInstruction, &toolInstruction)

	require.True(t, RecordToolIteration(invocation, 1))
	ScheduleToolFinalization(invocation)
	require.True(t, RecordLLMCall(invocation, 1))

	got, ok := ActivateForLLM(invocation, true)
	require.True(t, ok)
	require.Equal(t, llmInstruction, got)
}

func TestFinalizationPoliciesAreIndependent(t *testing.T) {
	invocation := agent.NewInvocation()
	toolInstruction := "finish for tool limit"
	Configure(invocation, nil, &toolInstruction)

	require.False(t, RecordLLMCall(invocation, 1))
	_, ok := ActivateForLLM(invocation, false)
	require.False(t, ok)

	require.True(t, RecordToolIteration(invocation, 1))
	ScheduleToolFinalization(invocation)
	got, ok := ActivateForLLM(invocation, false)
	require.True(t, ok)
	require.Equal(t, toolInstruction, got)
}

func TestNonPositiveLimitsDoNotAdvanceFinalization(t *testing.T) {
	invocation := agent.NewInvocation()
	instruction := ""
	Configure(invocation, &instruction, &instruction)

	require.False(t, RecordLLMCall(invocation, 0))
	require.False(t, RecordToolIteration(invocation, -1))
	_, ok := ActivateForLLM(invocation, false)
	require.False(t, ok)
}

func TestFinalizationStateIsNotInheritedByClone(t *testing.T) {
	parent := agent.NewInvocation()
	instruction := ""
	Configure(parent, &instruction, &instruction)

	child := parent.Clone()
	require.False(t, RecordLLMCall(child, 1))
	require.False(t, RecordToolIteration(child, 1))
}

func TestConfigureWithoutPoliciesClearsExistingState(t *testing.T) {
	invocation := agent.NewInvocation()
	instruction := ""
	Configure(invocation, &instruction, nil)
	require.True(t, RecordLLMCall(invocation, 1))

	Configure(invocation, nil, nil)
	_, ok := ActivateForLLM(invocation, true)
	require.False(t, ok)
}
