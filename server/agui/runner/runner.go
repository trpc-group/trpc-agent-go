//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package runner provides the AG-UI runner implementation.
package runner

import (
	"context"
	"errors"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	aguievent "trpc.group/trpc-go/trpc-agent-go/server/agui/event"
)

// Runner is the interface for running agents.
type Runner interface {
	Run(ctx context.Context, input *RunAgentInput) (<-chan events.Event, error)
}

// New wraps a runner.Runner with AG-UI specific translation logic.
func New(r trunner.Runner) Runner {
	return &runner{
		runner:            r,
		translatorFactory: aguievent.NewTranslator,
	}
}

// runner is the AG-UI runner implementation.
type runner struct {
	runner            trunner.Runner
	translatorFactory func(threadID, runID string) aguievent.Translator
}

// Run executes one run and streams translated events back.
func (r *runner) Run(ctx context.Context, input *RunAgentInput) (<-chan events.Event, error) {
	if input == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	events := make(chan events.Event)
	go r.run(ctx, input, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, input *RunAgentInput, out chan<- events.Event) {
	defer close(out)
	threadID := input.ThreadID
	runID := input.RunID
	out <- events.NewRunStartedEvent(threadID, runID)
	msgs := input.Messages
	if len(msgs) == 0 {
		out <- events.NewRunErrorEvent("no messages provided", events.WithRunID(runID))
		return
	}
	if _, ok := input.LatestUserMessage(); !ok {
		out <- events.NewRunErrorEvent("no user message found", events.WithRunID(runID))
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := r.runner.Run(ctx, threadID, runID, input.Messages[0])
	if err != nil {
		out <- events.NewRunErrorEvent(err.Error(), events.WithRunID(runID))
		return
	}
	translator := r.translatorFactory(threadID, runID)
	for {
		select {
		case <-ctx.Done():
			out <- events.NewRunErrorEvent(ctx.Err().Error(), events.WithRunID(runID))
			return
		case evt, ok := <-ch:
			if !ok {
				for _, fin := range translator.Finalize() {
					out <- fin
				}
				out <- events.NewRunFinishedEvent(threadID, runID)
				return
			}
			for _, translated := range translator.FromRunnerEvent(evt) {
				out <- translated
			}
		}
	}
}
