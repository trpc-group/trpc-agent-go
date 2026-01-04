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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		return fmt.Errorf("resolve user ID: %w", err)
	}
	key := session.Key{
		AppName:   r.appName,
		UserID:    userID,
		SessionID: runAgentInput.ThreadID,
	}
	r.runningMu.Lock()
	entry, ok := r.running[key]
	if !ok {
		r.runningMu.Unlock()
		return fmt.Errorf("%w: session: %v", ErrRunNotFound, key)
	}
	entry.cancel()
	r.runningMu.Unlock()
	return nil
}
