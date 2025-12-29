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
	"sync"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
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
	var tracker track.Tracker
	if opts.SessionService != nil {
		var err error
		tracker, err = track.New(opts.SessionService,
			track.WithAggregatorFactory(opts.AggregatorFactory),
			track.WithAggregationOption(opts.AggregationOption...),
			track.WithFlushInterval(opts.FlushInterval),
		)
		if err != nil {
			log.Warnf("agui: tracker disabled: %v", err)
		}
	}
	run := &runner{
		runner:             r,
		appName:            opts.AppName,
		translatorFactory:  opts.TranslatorFactory,
		userIDResolver:     opts.UserIDResolver,
		translateCallbacks: opts.TranslateCallbacks,
		runAgentInputHook:  opts.RunAgentInputHook,
		runOptionResolver:  opts.RunOptionResolver,
		tracker:            tracker,
		runningSessions:    sync.Map{},
		startSpan:          opts.StartSpan,
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
	runOptionResolver  RunOptionResolver
	tracker            track.Tracker
	runningSessions    sync.Map
	startSpan          StartSpan
}

type runInput struct {
	key         session.Key
	threadID    string
	runID       string
	userID      string
	userMessage model.Message
	runOption   []agent.RunOption
	translator  translator.Translator
	enableTrack bool
	span        trace.Span
}

// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
func (r *runner) Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("agui: runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("agui: run input cannot be nil")
	}
	runAgentInput, err := r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("agui: run input hook: %w", err)
	}
	threadID := runAgentInput.ThreadID
	runID := runAgentInput.RunID
	if len(runAgentInput.Messages) == 0 {
		return nil, errors.New("no messages provided")
	}
	if runAgentInput.Messages[len(runAgentInput.Messages)-1].Role != model.RoleUser {
		return nil, errors.New("last message is not a user message")
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve user ID: %w", err)
	}
	runOption, err := r.runOptionResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve run option: %w", err)
	}
	ctx, span, err := r.startSpan(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("start span: %w", err)
	}
	input := &runInput{
		key: session.Key{
			AppName:   r.appName,
			UserID:    userID,
			SessionID: runAgentInput.ThreadID,
		},
		threadID:    threadID,
		runID:       runID,
		userID:      userID,
		userMessage: runAgentInput.Messages[len(runAgentInput.Messages)-1],
		runOption:   runOption,
		translator:  r.translatorFactory(ctx, runAgentInput),
		enableTrack: r.tracker != nil,
		span:        span,
	}
	if _, ok := r.runningSessions.LoadOrStore(input.key, struct{}{}); ok {
		return nil, fmt.Errorf("session is already running: %v", input.key)
	}
	events := make(chan aguievents.Event)
	runCtx := agent.CloneContext(ctx)
	go r.run(runCtx, input, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, input *runInput, events chan<- aguievents.Event) {
	defer r.runningSessions.Delete(input.key)
	defer input.span.End()
	defer close(events)
	threadID := input.threadID
	runID := input.runID
	if input.enableTrack {
		defer func() {
			if err := r.tracker.Flush(ctx, input.key); err != nil {
				log.WarnfContext(
					ctx,
					"agui run: threadID: %s, runID: %s, "+
						"flush track events: %v",
					threadID,
					runID,
					err,
				)
			}
		}()
		if err := r.recordUserMessage(ctx, input.key, &input.userMessage); err != nil {
			log.WarnfContext(
				ctx,
				"agui run: threadID: %s, runID: %s, record user "+
					"message failed, disable tracking: %v",
				threadID,
				runID,
				err,
			)
		}
	}
	if !r.emitEvent(ctx, events, aguievents.NewRunStartedEvent(threadID, runID), input) {
		return
	}
	ch, err := r.runner.Run(ctx, input.userID, threadID, input.userMessage, input.runOption...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui run: threadID: %s, runID: %s, run agent: %v",
			threadID,
			runID,
			err,
		)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("run agent: %v", err),
			aguievents.WithRunID(runID)), input)
		return
	}
	for event := range ch {
		customEvent, err := r.handleBeforeTranslate(ctx, event)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"agui run: threadID: %s, runID: %s, before "+
					"translate callback: %v",
				threadID,
				runID,
				err,
			)
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("before translate callback: %v", err),
				aguievents.WithRunID(runID)), input)
			return
		}
		aguiEvents, err := input.translator.Translate(ctx, customEvent)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"agui run: threadID: %s, runID: %s, translate "+
					"event: %v",
				threadID,
				runID,
				err,
			)
			r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("translate event: %v", err),
				aguievents.WithRunID(runID)), input)
			return
		}
		for _, aguiEvent := range aguiEvents {
			if !r.emitEvent(ctx, events, aguiEvent, input) {
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
	input *runInput) bool {
	event, err := r.handleAfterTranslate(ctx, event)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui emit event: original event: %v, threadID: %s, "+
				"runID: %s, after translate callback: %v",
			event,
			input.threadID,
			input.runID,
			err,
		)
		events <- aguievents.NewRunErrorEvent(fmt.Sprintf("after translate callback: %v", err),
			aguievents.WithRunID(input.runID))
		return false
	}
	log.DebugfContext(
		ctx,
		"agui emit event: emitted event: %v, threadID: %s, runID: %s",
		event,
		input.threadID,
		input.runID,
	)
	if input.enableTrack {
		if err := r.recordTrackEvent(ctx, input.key, event); err != nil {
			log.WarnfContext(
				ctx,
				"agui emit event: record track event failed: "+
					"threadID: %s, runID: %s, err: %v",
				input.threadID,
				input.runID,
				err,
			)
		}
	}
	events <- event
	return true
}

func (r *runner) recordUserMessage(ctx context.Context, key session.Key, message *model.Message) error {
	messageID := uuid.New().String()
	start := aguievents.NewTextMessageStartEvent(messageID, aguievents.WithRole(string(model.RoleUser)))
	events := []aguievents.Event{start}
	if message.Content != "" {
		events = append(events, aguievents.NewTextMessageContentEvent(messageID, message.Content))
	}
	events = append(events, aguievents.NewTextMessageEndEvent(messageID))
	for _, evt := range events {
		if err := r.recordTrackEvent(ctx, key, evt); err != nil {
			return fmt.Errorf("record track event: %w", err)
		}
	}
	return nil
}

func (r *runner) recordTrackEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	return r.tracker.AppendEvent(ctx, key, event)
}
