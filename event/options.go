//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package event provides the event system for agent communication.
package event

import (
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Option is a function that can be used to configure the Event.
type Option func(*Event)

// WithBranch sets the branch for the event.
func WithBranch(branch string) Option {
	return func(e *Event) {
		e.Branch = branch
	}
}

// WithResponse sets the response for the event.
func WithResponse(response *model.Response) Option {
	return func(e *Event) {
		e.Response = response
	}
}

// WithObject sets the object for the event.
func WithObject(o string) Option {
	return func(e *Event) {
		e.Object = o
	}
}

// WithStateDelta sets state delta for the event.
func WithStateDelta(stateDelta map[string][]byte) Option {
	return func(e *Event) {
		e.StateDelta = stateDelta
	}
}

// WithStructuredOutputPayload sets a typed structured output payload on the event.
// This data is not serialized and is intended for immediate consumption.
func WithStructuredOutputPayload(payload any) Option {
	return func(e *Event) {
		e.StructuredOutput = payload
	}
}

// WithSkipSummarization sets the SkipSummarization action on the event.
func WithSkipSummarization() Option {
	return func(e *Event) {
		if e.Actions == nil {
			e.Actions = &EventActions{}
		}
		e.Actions.SkipSummarization = true
	}
}

// WithTag sets the tag for the event.
func WithTag(tag string) Option {
	return func(e *Event) {
		if e.Tag == "" {
			e.Tag = tag
			return
		}
		e.Tag += TagDelimiter + tag
	}
}
