//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import (
	"context"
	"fmt"
	"math/rand"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// PromptOptimizer simulates the PromptIter workflow in fake_deterministic mode
// and provides the integration contract for swapping in a real PromptIter engine.
type PromptOptimizer struct {
	mode    Mode
	rng     *rand.Rand
	targets []PromptTarget
}

// NewPromptOptimizer builds an optimizer for the given prompt targets.
func NewPromptOptimizer(mode Mode, seed int64, targets []PromptTarget) *PromptOptimizer {
	return &PromptOptimizer{
		mode:    mode,
		rng:     rand.New(rand.NewSource(seed)),
		targets: targets,
	}
}

// ProposeCandidate proposes a prompt candidate for one round using failure
// attributions as inputs. In fake_deterministic mode it cycles through
// known surfaces and produces predictable patches that align with the
// baseline evaluator's pre-programmed deltas.
func (o *PromptOptimizer) ProposeCandidate(
	_ context.Context,
	round int,
	train *EvalSummary,
	attributions []FailureAttribution,
	currentPrompts map[string]string,
) (*PromptCandidate, error) {
	// Strategy per round (deterministic order):
	//  round 1 -> try system_prompt
	//  round 2 -> try tool_desc_calc
	//  round 3 -> try router_prompt
	//  round 4 -> try agent_instruction
	roundStrategies := []struct {
		Surface   string
		Rationale string
		Patch     string
	}{
		{
			Surface:   "system_prompt",
			Rationale: "Top failed cases are FinalResponseMismatch and KnowledgeRecallInsufficient; tighten system-level answer policy.",
			Patch:     "# Optimized System Prompt\nYou are a precise and honest agent. For factual questions you MUST provide the direct answer and cite source when available; NEVER hedge ('I don't know') unless the knowledge is genuinely absent. Always end your response with a clear, concise final answer.",
		},
		{
			Surface:   "tool_desc_calc",
			Rationale: "Train attribution shows ToolArgumentError on calculator; patch tool description to stress numeric args only.",
			Patch:     "Tool: calculator. Computes arithmetic. REQUIRED numeric arguments (integers or floats). Use JSON numbers, NEVER string words like 'five'. Supported ops: +, -, *, /. Example payload: {\"a\": 5, \"b\": 3, \"op\": \"+\"}.",
		},
		{
			Surface:   "router_prompt",
			Rationale: "Round 2 did not move the needle. Try router-level guidance: always route email tasks to EmailAgent instead of MathAgent.",
			Patch:     "Router prompt (optimized). Rules: (1) If user intent is send_email, lookup_contact, or mentions 'email' -> route to EmailAgent. (2) If user asks calculation or math -> MathAgent. (3) Otherwise Agent-General. DO NOT route email intent to MathAgent.",
		},
		{
			Surface:   "agent_instruction",
			Rationale: "Fallback: instruct the general agent to double-check tool inputs before calling.",
			Patch:     "Agent Instruction v2. Before any tool call: (a) re-validate inputs against the tool schema, (b) cast numbers to floats, (c) if uncertain, call a clarifying internal reflection step, not the tool.",
		},
	}

	strategy := roundStrategies[(round-1)%len(roundStrategies)]

	// Make sure this surface is in targets.
	found := false
	for _, t := range o.targets {
		if t.SurfaceID == strategy.Surface {
			found = true
			break
		}
	}
	if !found {
		// Fallback to the first target surface.
		if len(o.targets) == 0 {
			return nil, fmt.Errorf("optimizer: no prompt targets configured")
		}
		strategy.Surface = o.targets[0].SurfaceID
		strategy.Patch = o.targets[0].BaselineText + "\n\n# (round " + fmt.Sprintf("%d", round) + " patch) " + strategy.Rationale
	}

	patches := map[string]string{strategy.Surface: strategy.Patch}

	// Augment rationale with attribution summary.
	attrSummary := summarizeAttributions(attributions)
	if attrSummary != "" {
		strategy.Rationale = strategy.Rationale + " Attribution signal: " + attrSummary
	}

	// Build the real promptiter.PatchSet so downstream consumers (promptiter
	// engine, evaluation service, audit trail) receive the native type.
	patchSet := &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{{
			SurfaceID: strategy.Surface,
			Value:     astructure.SurfaceValue{Text: &strategy.Patch},
			Reason:    strategy.Rationale,
		}},
	}

	// Build the real promptiter.Profile from the patch set.
	profile := &promptiter.Profile{
		StructureID: "optclosedloop-v1",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: strategy.Surface,
			Value:     astructure.SurfaceValue{Text: &strategy.Patch},
		}},
	}

	// Merge with current prompts to produce a complete profile.
	for surfaceID, text := range currentPrompts {
		if surfaceID == strategy.Surface {
			continue
		}
		t := text
		profile.Overrides = append(profile.Overrides, promptiter.SurfaceOverride{
			SurfaceID: surfaceID,
			Value:     astructure.SurfaceValue{Text: &t},
		})
	}

	return &PromptCandidate{
		CandidateID: fmt.Sprintf("cand_r%d_%06d", round, o.rng.Int63n(1_000_000)),
		Round:       round,
		GeneratedBy: "promptiter_" + string(o.mode),
		Patches:     patches,
		Rationale:   strategy.Rationale,
		PatchSet:    patchSet,
		Profile:     profile,
	}, nil
}

func summarizeAttributions(attrs []FailureAttribution) string {
	if len(attrs) == 0 {
		return ""
	}
	counts := map[FailureCategory]int{}
	for _, a := range attrs {
		counts[a.Category]++
	}
	summary := ""
	for cat, n := range counts {
		if summary != "" {
			summary += ", "
		}
		summary += fmt.Sprintf("%s=%d", cat, n)
	}
	return summary
}
