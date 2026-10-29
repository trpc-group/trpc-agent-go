//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messageorigin

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const sourceKey = "trpc_agent.message_origin.v1"

// Source describes the producer of a persisted message independently of role.
type Source string

const (
	// Unknown is used for imported history and events without provenance.
	Unknown Source = ""
	// UserInput identifies input submitted by the host for a user turn.
	UserInput Source = "user_input"
	// Agent identifies generated or delegated agent input.
	Agent Source = "agent"
)

// MarkInput records the provenance of newly created input events. Imported
// history must not be marked as UserInput merely because it has a user role.
func MarkInput(evt *event.Event, source Source) {
	if evt == nil {
		return
	}
	extensions := make(map[string]json.RawMessage, len(evt.Extensions)+1)
	for key, value := range evt.Extensions {
		extensions[key] = value
	}
	data, _ := json.Marshal(source)
	extensions[sourceKey] = data
	evt.Extensions = extensions
}

// InputSource reads versioned input provenance. Missing and unrecognized
// values remain unknown, including events persisted by older framework versions.
func InputSource(evt *event.Event) Source {
	if evt == nil {
		return Unknown
	}
	var source Source
	if err := json.Unmarshal(evt.Extensions[sourceKey], &source); err != nil {
		return Unknown
	}
	switch source {
	case UserInput, Agent:
		return source
	default:
		return Unknown
	}
}

type agentInputKey struct{}

// WithAgentInput marks input to an independent child runner as agent-authored.
func WithAgentInput(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentInputKey{}, true)
}

// MarkRunnerInput distinguishes current host input from seeded history and
// child-invocation input. Authentication of host input remains host-owned.
func MarkRunnerInput(ctx context.Context, evt *event.Event, msg model.Message, currentTurn bool) {
	if evt == nil {
		return
	}
	source := Unknown
	if msg.Role == model.RoleUser && currentTurn {
		source = UserInput
		if delegated, _ := ctx.Value(agentInputKey{}).(bool); delegated || evt.ParentInvocationID != "" || evt.ParentMetadata != nil {
			source = Agent
		}
	}
	MarkInput(evt, source)
}
