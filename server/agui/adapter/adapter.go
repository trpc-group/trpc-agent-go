//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package adapter provides the adapter for the AG-UI SDK.
package adapter

import (
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RunAgentInput represents the parameters for an AG-UI run request.
type RunAgentInput struct {
	// ThreadID is the ID of the conversation thread, which is the session ID.
	ThreadID string `json:"threadId"`
	// RunID is the ID of the current run, which is the invocation ID.
	RunID string `json:"runId"`
	// Messages is the list of messages in the conversation.
	Messages []model.Message `json:"messages"`
	// State is the session state of the agent.
	State map[string]any `json:"state"`
	// ForwardedProps is the custom properties forwarded to the agent.
	ForwardedProps map[string]any `json:"forwardedProps"`
}

// NewRunStartedEvent creates a new run started event.
func NewRunStartedEvent(threadID, runID string) events.Event {
	return events.NewRunStartedEvent(threadID, runID)
}

// NewRunErrorEvent creates a new run error event.
func NewRunErrorEvent(errorMessage string, runID string) events.Event {
	return events.NewRunErrorEvent(errorMessage, events.WithRunID(runID))
}

// NewRunFinishedEvent creates a new run finished event.
func NewRunFinishedEvent(threadID, runID string) events.Event {
	return events.NewRunFinishedEvent(threadID, runID)
}
