//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"fmt"
	"time"
)

// GraphInterrupt represents an interrupt in graph execution that can be resumed.
type GraphInterrupt struct {
	// Value is the value that was passed to interrupt().
	Value any
	// NodeID is the ID of the node where the interrupt occurred.
	NodeID string
	// TaskID is the ID of the task that was interrupted.
	TaskID string
	// Step is the step number when the interrupt occurred.
	Step int
	// Timestamp is when the interrupt occurred.
	Timestamp time.Time
	// Path is the execution path to the interrupted node.
	Path []string
}

// Error returns the error message for the interrupt.
func (g *GraphInterrupt) Error() string {
	return fmt.Sprintf("graph interrupted at node %s (step %d): %v", g.NodeID, g.Step, g.Value)
}

// ResumeCommand represents a command to resume graph execution.
type ResumeCommand struct {
	// Resume contains values to resume execution with.
	Resume any
	// ResumeMap maps task namespaces to resume values.
	ResumeMap map[string]any
}

// NewResumeCommand creates a new resume command.
func NewResumeCommand() *ResumeCommand {
	return &ResumeCommand{
		ResumeMap: make(map[string]any),
	}
}

// WithResume sets the resume value.
func (c *ResumeCommand) WithResume(value any) *ResumeCommand {
	c.Resume = value
	return c
}

// WithResumeMap sets the resume map.
func (c *ResumeCommand) WithResumeMap(resumeMap map[string]any) *ResumeCommand {
	c.ResumeMap = resumeMap
	return c
}

// AddResumeValue adds a resume value for a specific task.
func (c *ResumeCommand) AddResumeValue(taskID string, value any) *ResumeCommand {
	if c.ResumeMap == nil {
		c.ResumeMap = make(map[string]any)
	}
	c.ResumeMap[taskID] = value
	return c
}

// Interrupt creates a new GraphInterrupt with the given value.
func Interrupt(value any) *GraphInterrupt {
	return &GraphInterrupt{
		Value:     value,
		Timestamp: time.Now().UTC(),
	}
}

// IsInterrupt checks if an error is a GraphInterrupt.
func IsInterrupt(err error) bool {
	_, ok := err.(*GraphInterrupt)
	return ok
}

// GetInterrupt extracts GraphInterrupt from an error.
func GetInterrupt(err error) (*GraphInterrupt, bool) {
	if interrupt, ok := err.(*GraphInterrupt); ok {
		return interrupt, true
	}
	return nil, false
}
