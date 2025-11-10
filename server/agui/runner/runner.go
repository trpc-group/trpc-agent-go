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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
		runner:             r,
		appName:            opts.AppName,
		translatorFactory:  opts.TranslatorFactory,
		userIDResolver:     opts.UserIDResolver,
		translateCallbacks: opts.TranslateCallbacks,
		runAgentInputHook:  opts.RunAgentInputHook,
		sessionService:     opts.SessionService,
		runOptionResolver:  opts.RunOptionResolver,
	}
	return run
}

// runner is the default implementation of the Runner.
type runner struct {
	appName            string
	runner             trunner.Runner
	translatorFactory  TranslatorFactory
	userIDResolver     UserIDResolver
	translateCallbacks *translator.Callbacks
	runAgentInputHook  RunAgentInputHook
	sessionService     session.Service
	runOptionResolver  RunOptionResolver
}

// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
func (r *runner) Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	modifiedInput, err := r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("agui: run input hook: %w", err)
	}
	events := make(chan aguievents.Event)
	go r.run(ctx, modifiedInput, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, runAgentInput *adapter.RunAgentInput, events chan<- aguievents.Event) {
	defer close(events)
	threadID := runAgentInput.ThreadID
	runID := runAgentInput.RunID
	translator := r.translatorFactory(runAgentInput)
	if !r.emitEvent(ctx, events, aguievents.NewRunStartedEvent(threadID, runID), threadID, runID) {
		return
	}
	if len(runAgentInput.Messages) == 0 {
		log.Warnf("agui run: no messages provided, threadID: %s, runID: %s", threadID, runID)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("no messages provided", aguievents.WithRunID(runID)),
			threadID, runID)
		return
	}
	userMessage := runAgentInput.Messages[len(runAgentInput.Messages)-1]
	if userMessage.Role != model.RoleUser {
		log.Warnf("agui run: last message is not a user message, thread ID: %s, run ID: %s", threadID, runID)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("last message is not a user message",
			aguievents.WithRunID(runID)), threadID, runID)
		return
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		log.Errorf("agui run: threadID: %s, runID: %s, resolve user ID: %v", threadID, runID, err)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("resolve user ID: %v", err),
			aguievents.WithRunID(runID)), threadID, runID)
		return
	}
	runOption, err := r.runOptionResolver(ctx, runAgentInput)
	if err != nil {
		log.Errorf("agui run: threadID: %s, runID: %s, resolve run options: %v", threadID, runID, err)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("resolve run options: %v", err),
			aguievents.WithRunID(runID)), threadID, runID)
		return
	}
	ch, err := r.runner.Run(ctx, userID, threadID, userMessage, runOption...)
	if err != nil {
		log.Errorf("agui run: threadID: %s, runID: %s, run agent: %v", threadID, runID, err)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("run agent: %v", err),
			aguievents.WithRunID(runID)), threadID, runID)
		return
	}
	for event := range ch {
		customEvent, err := r.handleBeforeTranslate(ctx, event)
		if err != nil {
			log.Errorf("agui run: threadID: %s, runID: %s, before translate callback: %v", threadID, runID, err)
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("before translate callback: %v", err),
				aguievents.WithRunID(runID)), threadID, runID)
			return
		}
		aguiEvents, err := translator.Translate(customEvent)
		if err != nil {
			log.Errorf("agui run: threadID: %s, runID: %s, translate event: %v", threadID, runID, err)
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("translate event: %v", err),
				aguievents.WithRunID(runID)), threadID, runID)
			return
		}
		for _, aguiEvent := range aguiEvents {
			if !r.emitEvent(ctx, events, aguiEvent, threadID, runID) {
				return
			}
		}
	}
}

func (r *runner) applyRunAgentInputHook(ctx context.Context,
	input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
	if r.runAgentInputHook == nil {
		return input, nil
	}
	newInput, err := r.runAgentInputHook(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("run agent input hook: %w", err)
	}
	if newInput == nil {
		return input, nil
	}
	return newInput, nil
}

func (r *runner) handleBeforeTranslate(ctx context.Context, event *event.Event) (*event.Event, error) {
	if r.translateCallbacks == nil {
		return event, nil
	}
	customEvent, err := r.translateCallbacks.RunBeforeTranslate(ctx, event)
	if err != nil {
		return nil, err
	}
	if customEvent != nil {
		return customEvent, nil
	}
	return event, nil
}

func (r *runner) handleAfterTranslate(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
	if r.translateCallbacks == nil {
		return event, nil
	}
	customEvent, err := r.translateCallbacks.RunAfterTranslate(ctx, event)
	if err != nil {
		return nil, err
	}
	if customEvent != nil {
		return customEvent, nil
	}
	return event, nil
}

func (r *runner) emitEvent(ctx context.Context, events chan<- aguievents.Event, event aguievents.Event,
	threadID, runID string) bool {
	customEvent, err := r.handleAfterTranslate(ctx, event)
	if err != nil {
		log.Errorf("agui emit event: original event: %v, threadID: %s, runID: %s, after translate callback: %v",
			event, threadID, runID, err)
		events <- aguievents.NewRunErrorEvent(fmt.Sprintf("after translate callback: %v", err),
			aguievents.WithRunID(runID))
		return false
	}
	log.Debugf("agui emit event: emitted event: %v, threadID: %s, runID: %s", customEvent, threadID, runID)
	events <- customEvent
	return true
}
