//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tracecapture implements internal execution trace recording.
package tracecapture

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
)

// StartStepInput contains the metadata needed to start a new step.
type StartStepInput struct {
	InvocationID       string
	ParentInvocationID string
	AgentName          string
	Branch             string
	NodeID             string
	StartedAt          time.Time
	PredecessorStepIDs []string
	Input              *trace.Snapshot
}

// Capture records all steps for a single root runner.Run execution.
type Capture struct {
	mu                       sync.Mutex
	rootAgentName            string
	rootInvocationID         string
	sessionID                string
	startedAt                time.Time
	nextStepSeq              int
	steps                    []trace.Step
	stepIndexByID            map[string]int
	terminalByInvocation     map[string]map[string]struct{}
	childInvocationsByParent map[string]map[string]struct{}
}

// New creates a new capture for a root invocation.
func New(rootAgentName, rootInvocationID, sessionID string, startedAt time.Time) *Capture {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return &Capture{
		rootAgentName:            rootAgentName,
		rootInvocationID:         rootInvocationID,
		sessionID:                sessionID,
		startedAt:                startedAt,
		stepIndexByID:            make(map[string]int),
		terminalByInvocation:     make(map[string]map[string]struct{}),
		childInvocationsByParent: make(map[string]map[string]struct{}),
	}
}

// SetRootAgentName updates the root agent name when it becomes available later.
func (c *Capture) SetRootAgentName(name string) {
	if c == nil || name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rootAgentName == "" {
		c.rootAgentName = name
	}
}

// SetSessionID updates the session id when it becomes available later.
func (c *Capture) SetSessionID(sessionID string) {
	if c == nil || sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID == "" {
		c.sessionID = sessionID
	}
}

// RegisterInvocation records a parent-child invocation relationship.
func (c *Capture) RegisterInvocation(parentInvocationID string, invocationID string) {
	if c == nil || parentInvocationID == "" || invocationID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	children := c.childInvocationsByParent[parentInvocationID]
	if children == nil {
		children = make(map[string]struct{})
		c.childInvocationsByParent[parentInvocationID] = children
	}
	children[invocationID] = struct{}{}
}

// StartStep records a new started step and returns its allocated step id.
func (c *Capture) StartStep(in StartStepInput) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextStepSeq++
	stepID := fmt.Sprintf("s%d", c.nextStepSeq)
	if in.StartedAt.IsZero() {
		in.StartedAt = time.Now()
	}
	step := trace.Step{
		StepID:             stepID,
		InvocationID:       in.InvocationID,
		ParentInvocationID: in.ParentInvocationID,
		AgentName:          in.AgentName,
		Branch:             in.Branch,
		NodeID:             in.NodeID,
		StartedAt:          in.StartedAt,
		PredecessorStepIDs: slices.Clone(in.PredecessorStepIDs),
		Input:              cloneSnapshot(in.Input),
	}
	c.steps = append(c.steps, step)
	c.stepIndexByID[stepID] = len(c.steps) - 1
	if in.ParentInvocationID != "" {
		children := c.childInvocationsByParent[in.ParentInvocationID]
		if children == nil {
			children = make(map[string]struct{})
			c.childInvocationsByParent[in.ParentInvocationID] = children
		}
		children[in.InvocationID] = struct{}{}
	}
	c.removeTerminalStepIDsLocked(in.PredecessorStepIDs)
	terminals := c.terminalByInvocation[in.InvocationID]
	if terminals == nil {
		terminals = make(map[string]struct{})
		c.terminalByInvocation[in.InvocationID] = terminals
	}
	terminals[stepID] = struct{}{}
	return stepID
}

// FinishStep updates a previously started step.
func (c *Capture) FinishStep(
	stepID string,
	output *trace.Snapshot,
	errText string,
	endedAt time.Time,
) {
	if c == nil || stepID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	idx, ok := c.stepIndexByID[stepID]
	if !ok {
		return
	}
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	c.steps[idx].EndedAt = endedAt
	c.steps[idx].Output = cloneSnapshot(output)
	c.steps[idx].Error = errText
}

// PredecessorsForInvocation returns the current invocation predecessors for the next real step.
func (c *Capture) PredecessorsForInvocation(invocationID string, entryPredecessors []string) []string {
	if c == nil {
		return slices.Clone(entryPredecessors)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	effectiveTerminalSet := c.effectiveTerminalStepIDsLocked(invocationID, make(map[string]struct{}))
	if len(effectiveTerminalSet) == 0 {
		return slices.Clone(entryPredecessors)
	}
	return effectiveTerminalSet
}

// TerminalStepIDs returns the current terminal steps for an invocation.
func (c *Capture) TerminalStepIDs(invocationID string) []string {
	if c == nil || invocationID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	terminalSet := c.terminalByInvocation[invocationID]
	if len(terminalSet) == 0 {
		return nil
	}
	return c.sortedStepIDsLocked(terminalSet)
}

// Build materializes the final public trace.
func (c *Capture) Build(status trace.TraceStatus, endedAt time.Time) *trace.Trace {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	out := &trace.Trace{
		RootAgentName:    c.rootAgentName,
		RootInvocationID: c.rootInvocationID,
		SessionID:        c.sessionID,
		StartedAt:        c.startedAt,
		EndedAt:          endedAt,
		Status:           status,
		Steps:            make([]trace.Step, 0, len(c.steps)),
	}
	for _, step := range c.steps {
		out.Steps = append(out.Steps, cloneStep(step))
	}
	return out
}

func (c *Capture) removeTerminalStepIDsLocked(stepIDs []string) {
	if len(stepIDs) == 0 {
		return
	}
	for invocationID, terminalSet := range c.terminalByInvocation {
		for _, stepID := range stepIDs {
			delete(terminalSet, stepID)
		}
		if len(terminalSet) == 0 {
			delete(c.terminalByInvocation, invocationID)
		}
	}
}

func (c *Capture) sortedStepIDsLocked(stepSet map[string]struct{}) []string {
	ids := make([]string, 0, len(stepSet))
	for stepID := range stepSet {
		ids = append(ids, stepID)
	}
	slices.SortFunc(ids, func(a, b string) int {
		return c.stepIndexByID[a] - c.stepIndexByID[b]
	})
	return ids
}

func (c *Capture) effectiveTerminalStepIDsLocked(
	invocationID string,
	visited map[string]struct{},
) []string {
	if invocationID == "" {
		return nil
	}
	if _, seen := visited[invocationID]; seen {
		return nil
	}
	visited[invocationID] = struct{}{}
	aggregated := make(map[string]struct{})
	for stepID := range c.terminalByInvocation[invocationID] {
		aggregated[stepID] = struct{}{}
	}
	for childInvocationID := range c.childInvocationsByParent[invocationID] {
		for _, stepID := range c.effectiveTerminalStepIDsLocked(childInvocationID, visited) {
			aggregated[stepID] = struct{}{}
		}
	}
	if len(aggregated) == 0 {
		return nil
	}
	return c.sortedStepIDsLocked(aggregated)
}

func cloneStep(step trace.Step) trace.Step {
	return trace.Step{
		StepID:             step.StepID,
		InvocationID:       step.InvocationID,
		ParentInvocationID: step.ParentInvocationID,
		AgentName:          step.AgentName,
		Branch:             step.Branch,
		NodeID:             step.NodeID,
		StartedAt:          step.StartedAt,
		EndedAt:            step.EndedAt,
		PredecessorStepIDs: slices.Clone(step.PredecessorStepIDs),
		Input:              cloneSnapshot(step.Input),
		Output:             cloneSnapshot(step.Output),
		Error:              step.Error,
	}
}

func cloneSnapshot(snapshot *trace.Snapshot) *trace.Snapshot {
	if snapshot == nil {
		return nil
	}
	return &trace.Snapshot{Text: snapshot.Text}
}
