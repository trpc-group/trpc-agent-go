//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
)

type traceTaskRegistryEntry struct {
	task *traceTaskMetadata
}

type traceTaskMetadata struct {
	mu                   sync.Mutex
	owner                *ExecutionContext
	taskID               string
	nodeID               string
	predecessorStepIDs   []string
	preRunInputSnapshot  *atrace.Snapshot
	childTerminalStepIDs []string
	wrapperStepID        string
	postChildStepID      string
	claimed              bool
	fallbackToWrapper    bool
}

func newTraceTaskMetadata(
	owner *ExecutionContext,
	taskID string,
	nodeID string,
	predecessorStepIDs []string,
	inputSnapshot *atrace.Snapshot,
) *traceTaskMetadata {
	return &traceTaskMetadata{
		owner:               owner,
		taskID:              taskID,
		nodeID:              nodeID,
		predecessorStepIDs:  normalizeTraceStepIDs(predecessorStepIDs),
		preRunInputSnapshot: inputSnapshot,
	}
}

func (e *Executor) registerAgentNodeTraceTask(execCtx *ExecutionContext, task *traceTaskMetadata) bool {
	if execCtx == nil || task == nil || task.nodeID == "" {
		return false
	}
	execCtx.traceMu.Lock()
	if execCtx.traceAgentNodeTasksByNodeID == nil {
		execCtx.traceAgentNodeTasksByNodeID = make(map[string]*traceTaskRegistryEntry)
	}
	if existing := execCtx.traceAgentNodeTasksByNodeID[task.nodeID]; existing != nil {
		existingTask := existing.task
		execCtx.traceMu.Unlock()
		task.markFallbackToWrapper()
		if existingTask != nil {
			existingTask.mu.Lock()
			if !existingTask.claimed {
				existingTask.fallbackToWrapper = true
			}
			existingTask.mu.Unlock()
		}
		return false
	}
	execCtx.traceAgentNodeTasksByNodeID[task.nodeID] = &traceTaskRegistryEntry{task: task}
	execCtx.traceMu.Unlock()
	return true
}

func (e *Executor) unregisterAgentNodeTraceTask(execCtx *ExecutionContext, nodeID string, task *traceTaskMetadata) {
	if execCtx == nil || nodeID == "" || task == nil {
		return
	}
	execCtx.traceMu.Lock()
	defer execCtx.traceMu.Unlock()
	entry := execCtx.traceAgentNodeTasksByNodeID[nodeID]
	if entry == nil || entry.task != task {
		return
	}
	delete(execCtx.traceAgentNodeTasksByNodeID, nodeID)
}

func claimAgentNodeTraceTask(state State) *traceTaskMetadata {
	execCtx := executionContextFromState(state)
	nodeID, _ := GetStateValue[string](state, StateKeyCurrentNodeID)
	if execCtx == nil || nodeID == "" {
		return nil
	}
	if stepID, _ := GetStateValue[string](state, currentTraceStepIDStateKey); stepID != "" {
		return nil
	}
	return execCtx.claimAgentNodeTraceTask(nodeID)
}

func (e *ExecutionContext) claimAgentNodeTraceTask(nodeID string) *traceTaskMetadata {
	if e == nil || nodeID == "" {
		return nil
	}
	e.traceMu.Lock()
	entry := e.traceAgentNodeTasksByNodeID[nodeID]
	if entry == nil || entry.task == nil {
		e.traceMu.Unlock()
		return nil
	}
	task := entry.task
	e.traceMu.Unlock()
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.owner != e || task.nodeID != nodeID || task.claimed {
		return nil
	}
	task.claimed = true
	return task
}

func (m *traceTaskMetadata) childEntryPredecessorStepIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.wrapperStepID != "" {
		return []string{m.wrapperStepID}
	}
	return append([]string(nil), m.predecessorStepIDs...)
}

func (m *traceTaskMetadata) setChildTerminalStepIDs(stepIDs []string) {
	if m == nil {
		return
	}
	normalized := normalizeTraceStepIDs(stepIDs)
	if len(normalized) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.childTerminalStepIDs = normalized
}

func (m *traceTaskMetadata) markFallbackToWrapper() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallbackToWrapper = true
}

func (m *traceTaskMetadata) shouldFallbackToWrapper() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fallbackToWrapper
}

func (m *traceTaskMetadata) materializeWrapper(invocation *agent.Invocation) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fallbackToWrapper = true
	if m.wrapperStepID != "" {
		return m.wrapperStepID
	}
	stepID := agent.StartExecutionTraceStep(
		invocation,
		traceNodeIDForAgentNode(invocation, m.nodeID),
		m.preRunInputSnapshot,
		m.predecessorStepIDs,
	)
	m.wrapperStepID = stepID
	return stepID
}

func (m *traceTaskMetadata) materializePostChildStep(
	invocation *agent.Invocation,
	predecessorStepIDs []string,
) string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.postChildStepID != "" {
		return m.postChildStepID
	}
	stepID := agent.StartExecutionTraceStep(
		invocation,
		traceNodeIDForAgentNode(invocation, m.nodeID),
		m.preRunInputSnapshot,
		normalizeTraceStepIDs(predecessorStepIDs),
	)
	m.postChildStepID = stepID
	return stepID
}

func (m *traceTaskMetadata) snapshot() traceTaskMetadataSnapshot {
	if m == nil {
		return traceTaskMetadataSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return traceTaskMetadataSnapshot{
		childTerminalStepIDs: append([]string(nil), m.childTerminalStepIDs...),
		claimed:              m.claimed,
		fallbackToWrapper:    m.fallbackToWrapper,
	}
}

type traceTaskMetadataSnapshot struct {
	childTerminalStepIDs []string
	claimed              bool
	fallbackToWrapper    bool
}

func traceNodeIDForAgentNode(invocation *agent.Invocation, nodeID string) string {
	if invocation == nil || nodeID == "" {
		return ""
	}
	rootNodeID := agent.InvocationTraceNodeID(invocation)
	if rootNodeID == "" {
		return ""
	}
	return istructure.JoinNodeID(rootNodeID, nodeID)
}
