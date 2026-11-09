//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mcp

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

const sessionWriteWeight = 1 << 30

// sessionLock protects the client for the duration of an SDK operation. Each
// reader takes one slot and writers take all slots. A queued writer stops new
// readers from bypassing it, and callers can cancel while waiting for either.
type sessionLock struct {
	once sync.Once
	gate *semaphore.Weighted
}

func (l *sessionLock) acquire(ctx context.Context, weight int64) error {
	l.once.Do(func() { l.gate = semaphore.NewWeighted(sessionWriteWeight) })
	return l.gate.Acquire(ctx, weight)
}

func (l *sessionLock) lock(ctx context.Context) error  { return l.acquire(ctx, sessionWriteWeight) }
func (l *sessionLock) rlock(ctx context.Context) error { return l.acquire(ctx, 1) }

func (l *sessionLock) Lock()    { _ = l.lock(context.Background()) }
func (l *sessionLock) Unlock()  { l.gate.Release(sessionWriteWeight) }
func (l *sessionLock) RLock()   { _ = l.rlock(context.Background()) }
func (l *sessionLock) RUnlock() { l.gate.Release(1) }
