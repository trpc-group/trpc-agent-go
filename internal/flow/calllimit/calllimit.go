//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package calllimit coordinates opt-in finalization at LLM and tool-call
// limits.
package calllimit

import "trpc.group/trpc-go/trpc-agent-go/agent"

const stateKey = "__trpc_agent_internal_call_limit_finalization__"

// DefaultInstruction is used when finalization is enabled without a custom
// instruction.
const DefaultInstruction = "The call limit has been reached. Do not call " +
	"tools. Use the available conversation and tool results to provide the " +
	"best possible final answer. Clearly state any unresolved limitations."

type phase uint8

const (
	phaseNone phase = iota
	phasePending
	phaseActive
)

type policy struct {
	enabled     bool
	instruction string
}

type state struct {
	llmPolicy      policy
	toolPolicy     policy
	llmCalls       int
	toolIterations int
	phase          phase
	instruction    string
}

// Configure resets finalization state for one invocation. A nil instruction
// disables finalization for that limit; a non-nil empty instruction enables
// the framework default.
func Configure(
	invocation *agent.Invocation,
	llmInstruction *string,
	toolInstruction *string,
) {
	if invocation == nil {
		return
	}
	if llmInstruction == nil && toolInstruction == nil {
		invocation.DeleteState(stateKey)
		return
	}
	invocation.SetState(stateKey, state{
		llmPolicy:  newPolicy(llmInstruction),
		toolPolicy: newPolicy(toolInstruction),
	})
}

// RecordLLMCall records one allowed LLM call and reports whether it is the
// final allowed call with LLM-limit finalization enabled.
func RecordLLMCall(invocation *agent.Invocation, limit int) bool {
	current, ok := load(invocation)
	if !ok || limit <= 0 {
		return false
	}
	current.llmCalls++
	save(invocation, current)
	return current.llmPolicy.enabled &&
		current.llmCalls == limit
}

// RecordToolIteration records one allowed tool-call iteration and reports
// whether it reached the configured tool limit with finalization enabled.
func RecordToolIteration(invocation *agent.Invocation, limit int) bool {
	current, ok := load(invocation)
	if !ok || limit <= 0 {
		return false
	}
	current.toolIterations++
	save(invocation, current)
	return current.toolPolicy.enabled &&
		current.toolIterations == limit
}

// ScheduleToolFinalization schedules a tool-limit finalization for the next
// allowed LLM call.
func ScheduleToolFinalization(invocation *agent.Invocation) {
	current, ok := load(invocation)
	if !ok || !current.toolPolicy.enabled || current.phase == phaseActive {
		return
	}
	current.phase = phasePending
	current.instruction = resolveInstruction(current.toolPolicy)
	save(invocation, current)
}

// ActivateForLLM activates finalization for the current LLM call. LLM-limit
// finalization takes precedence over a pending tool-limit finalization.
func ActivateForLLM(
	invocation *agent.Invocation,
	llmLimitReached bool,
) (string, bool) {
	current, ok := load(invocation)
	if !ok {
		return "", false
	}
	if current.phase == phaseActive {
		return current.instruction, true
	}
	if llmLimitReached && current.llmPolicy.enabled {
		current.phase = phaseActive
		current.instruction = resolveInstruction(current.llmPolicy)
		save(invocation, current)
		return current.instruction, true
	}
	if current.phase != phasePending {
		return "", false
	}
	current.phase = phaseActive
	save(invocation, current)
	return current.instruction, true
}

// Active reports whether the invocation is processing its bounded
// finalization call.
func Active(invocation *agent.Invocation) bool {
	current, ok := load(invocation)
	return ok && current.phase == phaseActive
}

// Finish clears call-limit finalization state after the bounded final call.
func Finish(invocation *agent.Invocation) {
	if invocation == nil {
		return
	}
	invocation.DeleteState(stateKey)
}

func newPolicy(instruction *string) policy {
	if instruction == nil {
		return policy{}
	}
	return policy{
		enabled:     true,
		instruction: *instruction,
	}
}

func resolveInstruction(config policy) string {
	if config.instruction == "" {
		return DefaultInstruction
	}
	return config.instruction
}

func load(invocation *agent.Invocation) (state, bool) {
	if invocation == nil {
		return state{}, false
	}
	value, ok := invocation.GetState(stateKey)
	if !ok {
		return state{}, false
	}
	current, ok := value.(state)
	return current, ok
}

func save(invocation *agent.Invocation, current state) {
	if invocation != nil {
		invocation.SetState(stateKey, current)
	}
}
