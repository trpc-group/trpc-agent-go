//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	distributedCancelRunMarkerKey    = session.StateTempPrefix + "agui:distributed_cancel:run"
	distributedCancelCancelMarkerKey = session.StateTempPrefix + "agui:distributed_cancel:cancel"
)

type distributedCancelMarker struct {
	UpdatedAt string `json:"updatedAt"`
}

// Canceler cancels a running run identified by the request payload.
type Canceler interface {
	// Cancel cancels a running run and returns an error when the run key cannot be found.
	Cancel(ctx context.Context, input *adapter.RunAgentInput) error
}

// Cancel cancels a running run identified by appName, userID, and sessionID.
func (r *runner) Cancel(ctx context.Context, runAgentInput *adapter.RunAgentInput) error {
	if r.runner == nil {
		return errors.New("runner is nil")
	}
	if runAgentInput == nil {
		return errors.New("run input cannot be nil")
	}
	runAgentInput, err := r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return fmt.Errorf("run input hook: %w", err)
	}
	appName, err := r.resolveAppName(ctx, runAgentInput)
	if err != nil {
		return fmt.Errorf("resolve app name: %w", err)
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		return fmt.Errorf("resolve user ID: %w", err)
	}
	key := session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: runAgentInput.ThreadID,
	}
	r.runningMu.Lock()
	entry, ok := r.running[key]
	if !ok {
		r.runningMu.Unlock()
		if !r.distributedCancelEnabled {
			return fmt.Errorf("%w: session: %v", ErrRunNotFound, key)
		}
		return r.cancelDistributed(ctx, key)
	}
	entry.cancel(errExplicitCancel)
	r.runningMu.Unlock()
	return nil
}

func (r *runner) startDistributedCancel(
	ctx context.Context,
	key session.Key,
	cancel context.CancelCauseFunc,
) (context.CancelFunc, error) {
	sessionService := r.sessionService
	if sessionService == nil {
		return nil, errors.New("agui: session service is required when distributed cancel is enabled")
	}
	if err := writeDistributedRunMarker(ctx, sessionService, key); err != nil {
		return nil, err
	}
	watchCtx, stop := context.WithCancel(context.Background())
	go r.watchDistributedCancel(watchCtx, sessionService, key, cancel)
	return stop, nil
}

func (r *runner) finishDistributedCancel(ctx context.Context, key session.Key) {
	started, stop := r.distributedCancelSnapshot(key)
	if stop != nil {
		stop()
	}
	if !started {
		return
	}
	sessionService := r.sessionService
	if sessionService == nil {
		return
	}
	cleanupCtx := context.WithoutCancel(ctx)
	if r.postRunFinalizationTimeout > 0 {
		var cancel context.CancelFunc
		cleanupCtx, cancel = context.WithTimeout(cleanupCtx, r.postRunFinalizationTimeout)
		defer cancel()
	}
	if err := clearDistributedCancelMarkers(cleanupCtx, sessionService, key); err != nil {
		log.WarnfContext(cleanupCtx, "agui distributed cancel cleanup: session: %v, err: %v", key, err)
	}
}

func (r *runner) watchDistributedCancel(
	ctx context.Context,
	sessionService session.Service,
	key session.Key,
	cancel context.CancelCauseFunc,
) {
	interval := r.distributedCancelPollInterval
	if interval <= 0 {
		interval = defaultDistributedCancelPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			requested, err := readDistributedCancelMarker(ctx, sessionService, key)
			if err != nil {
				log.WarnfContext(ctx, "agui distributed cancel poll: session: %v, err: %v", key, err)
				continue
			}
			if requested {
				cancel(errExplicitCancel)
				return
			}
		}
	}
}

func writeDistributedRunMarker(
	ctx context.Context,
	service session.Service,
	key session.Key,
) error {
	raw, err := marshalDistributedCancelMarker()
	if err != nil {
		return fmt.Errorf("marshal run marker: %w", err)
	}
	state := session.StateMap{
		distributedCancelRunMarkerKey:    raw,
		distributedCancelCancelMarkerKey: nil,
	}
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		if _, err = service.CreateSession(ctx, key, state); err == nil {
			return nil
		}
		sess, getErr := service.GetSession(ctx, key)
		if getErr != nil {
			return fmt.Errorf("create session: %v; get session: %w", err, getErr)
		}
		if sess == nil {
			return fmt.Errorf("create session: %w", err)
		}
	}
	return service.UpdateSessionState(ctx, key, state)
}

func writeDistributedCancelMarker(
	ctx context.Context,
	service session.Service,
	key session.Key,
) error {
	raw, err := marshalDistributedCancelMarker()
	if err != nil {
		return fmt.Errorf("marshal cancel marker: %w", err)
	}
	return service.UpdateSessionState(ctx, key, session.StateMap{
		distributedCancelCancelMarkerKey: raw,
	})
}

func marshalDistributedCancelMarker() ([]byte, error) {
	return json.Marshal(distributedCancelMarker{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func clearDistributedCancelMarkers(
	ctx context.Context,
	service session.Service,
	key session.Key,
) error {
	return service.UpdateSessionState(ctx, key, session.StateMap{
		distributedCancelRunMarkerKey:    nil,
		distributedCancelCancelMarkerKey: nil,
	})
}

func readDistributedCancelMarker(
	ctx context.Context,
	service session.Service,
	key session.Key,
) (bool, error) {
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return false, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return false, nil
	}
	raw, ok := sess.GetState(distributedCancelCancelMarkerKey)
	return ok && len(raw) > 0, nil
}

func (r *runner) cancelDistributed(ctx context.Context, key session.Key) error {
	sessionService := r.sessionService
	if sessionService == nil {
		return errors.New("agui: session service is required when distributed cancel is enabled")
	}
	active, err := activeDistributedRun(ctx, sessionService, key)
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("%w: session: %v", ErrRunNotFound, key)
	}
	if err := writeDistributedCancelMarker(ctx, sessionService, key); err != nil {
		return fmt.Errorf("write cancel marker: %w", err)
	}
	return nil
}

func activeDistributedRun(
	ctx context.Context,
	service session.Service,
	key session.Key,
) (bool, error) {
	sess, err := service.GetSession(ctx, key)
	if err != nil {
		return false, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return false, nil
	}
	raw, ok := sess.GetState(distributedCancelRunMarkerKey)
	if !ok || len(raw) == 0 {
		return false, nil
	}
	return true, nil
}
