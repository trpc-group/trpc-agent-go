//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// AppendEventContext carries context for AppendEvent hooks.
type AppendEventContext struct {
	Context context.Context
	Session *Session
	Event   *event.Event
	Key     Key
}

// GetSessionContext carries context for GetSession hooks.
type GetSessionContext struct {
	Context context.Context
	Key     Key
	Options *Options
}

// AppendEventHook processes events with next() chain pattern.
// Call next() to continue processing, or return directly to abort.
type AppendEventHook func(ctx *AppendEventContext, next func() error) error

// GetSessionHook processes session retrieval with next() chain pattern.
// Call next() to get session from storage, then optionally modify and return.
type GetSessionHook func(ctx *GetSessionContext, next func() (*Session, error)) (*Session, error)
