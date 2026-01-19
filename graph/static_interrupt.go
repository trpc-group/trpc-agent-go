//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import "sort"

// StaticInterruptKeyPrefixBefore/After are interrupt key prefixes used by
// static interrupts (debug breakpoints).
const (
	StaticInterruptKeyPrefixBefore = "static_interrupt_before:"
	StaticInterruptKeyPrefixAfter  = "static_interrupt_after:"
)

// StaticInterruptPhase indicates when a static interrupt is triggered.
type StaticInterruptPhase string

// Supported static interrupt phases.
const (
	StaticInterruptPhaseBefore StaticInterruptPhase = "before"
	StaticInterruptPhaseAfter  StaticInterruptPhase = "after"
)

// StaticInterruptPayload is stored in InterruptError.Value for static
// interrupts, providing structured information for debugging UIs.
type StaticInterruptPayload struct {
	Phase       StaticInterruptPhase `json:"phase"`
	Nodes       []string             `json:"nodes"`
	ActiveNodes []string             `json:"activeNodes,omitempty"`
}

func (e *Executor) maybeStaticInterruptBefore(
	execCtx *ExecutionContext,
	tasks []*Task,
	step int,
) *InterruptError {
	hits := e.staticInterruptHits(tasks, true)
	if len(hits) == 0 {
		return nil
	}

	if execCtx == nil || execCtx.State == nil {
		return e.newStaticInterruptError(
			StaticInterruptPhaseBefore,
			hits,
			uniqueSortedTaskNodes(tasks),
			step,
		)
	}

	skips := getStaticInterruptSkips(execCtx.State)
	if hasSkipsForAll(skips, hits) {
		clearSkips(execCtx.State, skips, hits)
		return nil
	}

	activeNodes := uniqueSortedTaskNodes(tasks)
	setSkips(skips, hits)

	intr := e.newStaticInterruptError(
		StaticInterruptPhaseBefore,
		hits,
		activeNodes,
		step,
	)
	intr.NextNodes = append([]string(nil), activeNodes...)
	return intr
}

func (e *Executor) maybeStaticInterruptAfter(
	tasks []*Task,
	step int,
) *InterruptError {
	hits := e.staticInterruptHits(tasks, false)
	if len(hits) == 0 {
		return nil
	}

	return e.newStaticInterruptError(
		StaticInterruptPhaseAfter,
		hits,
		uniqueSortedTaskNodes(tasks),
		step,
	)
}

func (e *Executor) staticInterruptHits(
	tasks []*Task,
	before bool,
) []string {
	if e == nil || e.graph == nil || len(tasks) == 0 {
		return nil
	}

	hitSet := make(map[string]struct{})
	for _, t := range tasks {
		if t == nil || t.NodeID == "" {
			continue
		}
		node, ok := e.graph.Node(t.NodeID)
		if !ok || node == nil {
			continue
		}
		if before {
			if node.interruptBefore {
				hitSet[t.NodeID] = struct{}{}
			}
			continue
		}
		if node.interruptAfter {
			hitSet[t.NodeID] = struct{}{}
		}
	}

	return keysOfSet(hitSet)
}

func (e *Executor) newStaticInterruptError(
	phase StaticInterruptPhase,
	nodes []string,
	activeNodes []string,
	step int,
) *InterruptError {
	payload := StaticInterruptPayload{
		Phase:       phase,
		Nodes:       append([]string(nil), nodes...),
		ActiveNodes: append([]string(nil), activeNodes...),
	}

	intr := NewInterruptError(payload)
	if len(nodes) > 0 {
		intr.NodeID = nodes[0]
		intr.TaskID = nodes[0]
	}
	if phase == StaticInterruptPhaseAfter {
		intr.Key = StaticInterruptKeyPrefixAfter + intr.NodeID
		intr.SkipRerun = true
	} else {
		intr.Key = StaticInterruptKeyPrefixBefore + intr.NodeID
	}
	intr.Step = step
	return intr
}

func uniqueSortedTaskNodes(tasks []*Task) []string {
	if len(tasks) == 0 {
		return nil
	}
	set := make(map[string]struct{})
	for _, t := range tasks {
		if t == nil || t.NodeID == "" {
			continue
		}
		set[t.NodeID] = struct{}{}
	}
	return keysOfSet(set)
}

func keysOfSet(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func getStaticInterruptSkips(state State) map[string]any {
	if state == nil {
		return nil
	}
	if existing, ok := state[StateKeyStaticInterruptSkips]; ok {
		if m, ok := existing.(map[string]any); ok {
			return m
		}
	}
	m := make(map[string]any)
	state[StateKeyStaticInterruptSkips] = m
	return m
}

func hasSkipsForAll(skips map[string]any, nodeIDs []string) bool {
	if len(nodeIDs) == 0 {
		return false
	}
	for _, nodeID := range nodeIDs {
		if _, ok := skips[nodeID]; !ok {
			return false
		}
	}
	return true
}

func setSkips(skips map[string]any, nodeIDs []string) {
	for _, nodeID := range nodeIDs {
		skips[nodeID] = true
	}
}

func clearSkips(state State, skips map[string]any, nodeIDs []string) {
	if state == nil || skips == nil {
		return
	}
	for _, nodeID := range nodeIDs {
		delete(skips, nodeID)
	}
	if len(skips) == 0 {
		delete(state, StateKeyStaticInterruptSkips)
	}
}
