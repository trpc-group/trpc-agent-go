//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const defaultCoreRecordingTrackPersistenceTimeout = 5 * time.Second

// WrapCoreRunner wraps a trpc-agent-go core runner with AG-UI track recording.
// Unlike Runner, the returned runner keeps the core runner Run signature and
// forwards core events unchanged. AG-UI translation and persistence are best
// effort, so recording failures do not change the core run events or errors.
// The returned event channel closes after the final track batch has been
// flushed or its bounded persistence timeout has elapsed.
//
// appName identifies the session scope used for AG-UI tracks and must match the
// effective application name of the wrapped runner. sessionService must be the
// same service used by the wrapped runner and must implement session.TrackService.
// Close delegates to the wrapped runner; callers should close the returned
// runner instead of closing both. Optional capabilities implemented by the
// wrapped runner beyond trpc-agent-go/runner.Runner are not exposed by the
// returned adapter.
func WrapCoreRunner(
	base trunner.Runner,
	appName string,
	sessionService session.Service,
) (trunner.Runner, error) {
	if base == nil {
		return nil, errors.New("agui: core runner is nil")
	}
	if strings.TrimSpace(appName) == "" {
		return nil, errors.New("agui: core runner app name is empty")
	}
	if sessionService == nil {
		return nil, errors.New("agui: core runner session service is nil")
	}
	if _, ok := sessionService.(session.TrackService); !ok {
		return nil, errors.New("agui: core runner session service does not implement track service")
	}
	return &coreRecordingRunner{
		base:           base,
		appName:        appName,
		sessionService: sessionService,
	}, nil
}

// TODO: Share the track recording lifecycle below with the default AG-UI
// Runner after both paths can preserve their current final-event ordering and
// filtering behavior.
type coreRecordingRunner struct {
	base           trunner.Runner
	appName        string
	sessionService session.Service
}

func (r *coreRecordingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	events, err := r.base.Run(ctx, userID, sessionID, message, runOpts...)
	if err != nil || events == nil {
		return events, err
	}
	key := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: sessionID,
	}
	out := make(chan *event.Event)
	go r.forward(ctx, key, message, events, out)
	return out, nil
}

func (r *coreRecordingRunner) Close() error {
	return r.base.Close()
}

func (r *coreRecordingRunner) forward(
	ctx context.Context,
	key session.Key,
	message model.Message,
	source <-chan *event.Event,
	out chan<- *event.Event,
) {
	defer close(out)
	var state *coreRecordingState
	recordingDisabled := false
	for evt := range source {
		if state == nil && !recordingDisabled && evt != nil {
			var err error
			state, err = r.newRecordingState(ctx, key, message, evt.RequestID)
			if err != nil {
				recordingDisabled = true
				log.WarnfContext(ctx,
					"agui recording: initialize track failed: app=%s, user=%s, session=%s, err=%v",
					key.AppName, key.UserID, key.SessionID, err,
				)
			}
		}
		if state != nil {
			if err := state.record(ctx, evt); err != nil {
				log.WarnfContext(ctx,
					"agui recording: record event failed: app=%s, user=%s, session=%s, err=%v",
					key.AppName, key.UserID, key.SessionID, err,
				)
			}
		}
		out <- evt
	}
	if state == nil && !recordingDisabled {
		var err error
		state, err = r.newRecordingState(ctx, key, message, "")
		if err != nil {
			log.WarnfContext(ctx,
				"agui recording: initialize track for empty core stream failed: app=%s, user=%s, session=%s, err=%v",
				key.AppName, key.UserID, key.SessionID, err,
			)
			return
		}
	}
	if state == nil {
		return
	}
	if err := state.finish(ctx); err != nil {
		log.WarnfContext(ctx,
			"agui recording: finish track failed: app=%s, user=%s, session=%s, err=%v",
			key.AppName, key.UserID, key.SessionID, err,
		)
	}
}

func (r *coreRecordingRunner) newRecordingState(
	ctx context.Context,
	key session.Key,
	message model.Message,
	runID string,
) (*coreRecordingState, error) {
	if err := key.CheckSessionKey(); err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	if message.Role == "" && model.HasPayload(message) {
		message.Role = model.RoleUser
	}
	if message.Role != model.RoleUser {
		return nil, fmt.Errorf("input message role must be user: %s", message.Role)
	}
	if runID == "" {
		runID = uuid.NewString()
	}
	tracker, err := track.New(r.sessionService)
	if err != nil {
		return nil, fmt.Errorf("create tracker: %w", err)
	}
	trans, err := translator.New(ctx, key.SessionID, runID)
	if err != nil {
		return nil, fmt.Errorf("create translator: %w", err)
	}
	userMessage, err := multimodal.UserMessageFromModel(uuid.NewString(), message)
	if err != nil {
		return nil, fmt.Errorf("convert user message: %w", err)
	}
	userMessage.Name = key.UserID
	state := &coreRecordingState{
		key:        key,
		runID:      runID,
		tracker:    tracker,
		translator: trans,
	}
	if err := state.append(ctx,
		aguievents.NewCustomEvent(
			multimodal.CustomEventNameUserMessage,
			aguievents.WithValue(userMessage),
		),
		aguievents.NewRunStartedEvent(key.SessionID, runID),
	); err != nil {
		return nil, fmt.Errorf("record startup events: %w", err)
	}
	return state, nil
}

type coreRecordingState struct {
	key        session.Key
	runID      string
	tracker    track.Tracker
	translator translator.Translator
	terminal   bool
}

func (s *coreRecordingState) record(ctx context.Context, evt *event.Event) error {
	if s == nil || s.terminal || evt == nil {
		return nil
	}
	aguiEvents, err := s.translator.Translate(ctx, evt)
	if err != nil {
		return fmt.Errorf("translate event: %w", err)
	}
	if err := s.append(ctx, aguiEvents...); err != nil {
		return fmt.Errorf("append translated events: %w", err)
	}
	return nil
}

func (s *coreRecordingState) append(ctx context.Context, events ...aguievents.Event) error {
	for _, evt := range events {
		if evt == nil {
			continue
		}
		if err := s.tracker.AppendEvent(ctx, s.key, evt); err != nil {
			return err
		}
		if coreRecordingTerminalRunSignal(evt) {
			s.terminal = true
			break
		}
	}
	return nil
}

func (s *coreRecordingState) finish(ctx context.Context) error {
	var finishErr error
	if !s.terminal {
		if finalizer, ok := s.translator.(translator.PostRunFinalizingTranslator); ok {
			events, err := finalizer.PostRunFinalizationEvents(ctx)
			if err != nil {
				finishErr = errors.Join(
					finishErr,
					fmt.Errorf("finalize translator: %w", err),
				)
			} else if err := s.append(ctx, events...); err != nil {
				finishErr = errors.Join(
					finishErr,
					fmt.Errorf("append finalization events: %w", err),
				)
			}
		}
	}
	if !s.terminal {
		var terminal aguievents.Event
		if finishErr != nil {
			terminal = aguievents.NewRunErrorEvent(
				finishErr.Error(),
				aguievents.WithRunID(s.runID),
			)
		} else if cause := context.Cause(ctx); cause != nil {
			terminal = aguievents.NewRunErrorEvent(
				cause.Error(),
				aguievents.WithRunID(s.runID),
			)
		} else {
			terminal = aguievents.NewRunFinishedEvent(s.key.SessionID, s.runID)
		}
		if err := s.append(ctx, terminal); err != nil {
			finishErr = errors.Join(
				finishErr,
				fmt.Errorf("append terminal event: %w", err),
			)
		}
	}
	closeCtx, cancel := newCoreRecordingPersistenceContext(ctx)
	defer cancel()
	if err := s.tracker.Close(closeCtx, s.key); err != nil {
		finishErr = errors.Join(
			finishErr,
			fmt.Errorf("close tracker: %w", err),
		)
	}
	return finishErr
}

func coreRecordingTerminalRunSignal(evt aguievents.Event) bool {
	switch evt.(type) {
	case *aguievents.RunFinishedEvent, *aguievents.RunErrorEvent:
		return true
	default:
		return false
	}
}

func newCoreRecordingPersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = context.WithoutCancel(agent.CloneContext(ctx))
	return context.WithTimeout(ctx, defaultCoreRecordingTrackPersistenceTimeout)
}
