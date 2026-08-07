//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package privatestate routes session-state updates that must not be exposed
// through public event deltas.
package privatestate

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// UpdateRequest describes one private session-state update.
type UpdateRequest struct {
	// Key identifies the session receiving the update.
	Key session.Key
	// State contains the private session-scoped values to update.
	State session.StateMap
}

// Writer receives private session-state updates without converting them into
// public event deltas.
type Writer interface {
	UpdatePrivateSessionState(context.Context, UpdateRequest) error
}

// Update applies state through a private writer when the service provides one.
// Otherwise, it falls back to the service's direct session-state update.
func Update(
	ctx context.Context,
	service session.Service,
	key session.Key,
	state session.StateMap,
) error {
	if len(state) == 0 {
		return nil
	}
	if service == nil {
		return errors.New("update private session state: service is nil")
	}
	copied := cloneStateMap(state)
	if writer, ok := service.(Writer); ok {
		return writer.UpdatePrivateSessionState(ctx, UpdateRequest{
			Key:   key,
			State: copied,
		})
	}
	return service.UpdateSessionState(ctx, key, copied)
}

func cloneStateMap(state session.StateMap) session.StateMap {
	if state == nil {
		return nil
	}
	cloned := make(session.StateMap, len(state))
	for key, value := range state {
		if value == nil {
			cloned[key] = nil
			continue
		}
		cloned[key] = make([]byte, len(value))
		copy(cloned[key], value)
	}
	return cloned
}
