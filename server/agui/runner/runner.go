//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package runner wraps a trpc-agent-go runner and translates it to AG-UI events.
package runner

import (
	"context"
	"errors"
	"fmt"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

// Runner executes AG-UI runs and emits AG-UI events.
type Runner interface {
	// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
	Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// New wraps a trpc-agent-go runner with AG-UI specific translation logic.
func New(r trunner.Runner, opt ...Option) Runner {
	opts := NewOptions(opt...)
	run := &runner{
		runner:            r,
		translatorFactory: opts.TranslatorFactory,
		userIDResolver:    opts.UserIDResolver,
	}
	return run
}

// runner is the default implementation of the Runner.
type runner struct {
	runner            trunner.Runner
	translatorFactory TranslatorFactory
	userIDResolver    UserIDResolver
}

// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
func (r *runner) Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	events := make(chan aguievents.Event)
	go r.run(ctx, runAgentInput, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, runAgentInput *adapter.RunAgentInput, events chan<- aguievents.Event) {
	defer close(events)
	translator := r.translatorFactory(runAgentInput)
	events <- aguievents.NewRunStartedEvent(runAgentInput.ThreadID, runAgentInput.RunID)
	if len(runAgentInput.Messages) == 0 {
		events <- aguievents.NewRunErrorEvent("no messages provided", aguievents.WithRunID(runAgentInput.RunID))
		return
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		events <- aguievents.NewRunErrorEvent(fmt.Sprintf("resolve user ID: %v", err),
			aguievents.WithRunID(runAgentInput.RunID))
		return
	}
	userMessage := runAgentInput.Messages[len(runAgentInput.Messages)-1]
	if userMessage.Role != model.RoleUser {
		events <- aguievents.NewRunErrorEvent("last message is not a user message",
			aguievents.WithRunID(runAgentInput.RunID))
		return
	}
	ch, err := r.runner.Run(ctx, userID, runAgentInput.ThreadID, userMessage)
	if err != nil {
		events <- aguievents.NewRunErrorEvent(fmt.Sprintf("run agent: %v", err),
			aguievents.WithRunID(runAgentInput.RunID))
		return
	}
	for event := range ch {
		aguiEvents, err := translator.Translate(event)
		if err != nil {
			events <- aguievents.NewRunErrorEvent(fmt.Sprintf("translate event: %v", err),
				aguievents.WithRunID(runAgentInput.RunID))
			return
		}
		for _, aguiEvent := range aguiEvents {
			events <- aguiEvent
		}
	}
}
