//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package event provides the event bridge from runner events to AG-UI events.
package event

import (
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Bridge is the event bridge from trpc-agent-go events to AG-UI events.
type Bridge interface {
	// NewRunStartedEvent creates a new run started event.
	NewRunStartedEvent() events.Event
	// NewRunErrorEvent creates a new run error event.
	NewRunErrorEvent(errorMessage string) events.Event
	// NewRunFinishedEvent creates a new run finished event.
	NewRunFinishedEvent() events.Event
	// Translate translates a trpc-agent-go event to AG-UI events.
	Translate(event *event.Event) ([]events.Event, error)
}

// bridge is the default implementation of the Bridge.
type bridge struct {
	threadID      string
	runID         string
	lastMessageID string
}

// NewBridge creates a new event bridge.
func NewBridge(threadID, runID string) Bridge {
	return &bridge{
		threadID: threadID,
		runID:    runID,
	}
}

// NewRunStartedEvent creates a new run started event.
func (m *bridge) NewRunStartedEvent() events.Event {
	return events.NewRunStartedEvent(m.threadID, m.runID)
}

// NewRunErrorEvent creates a new run error event.
func (m *bridge) NewRunErrorEvent(errorMessage string) events.Event {
	return events.NewRunErrorEvent(errorMessage, events.WithRunID(m.runID))
}

// NewRunFinishedEvent creates a new run finished event.
func (m *bridge) NewRunFinishedEvent() events.Event {
	return events.NewRunFinishedEvent(m.threadID, m.runID)
}
