//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	minmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// InMemoryTarget is the reference target backed by the in-memory session
// and memory services. It supports every capability.
type InMemoryTarget struct {
	name string
	sess *sinmemory.SessionService
	mem  *minmemory.MemoryService
}

// NewInMemoryTarget creates a ready-to-use in-memory target.
func NewInMemoryTarget(name string) *InMemoryTarget {
	t := &InMemoryTarget{name: name}
	_ = t.Reset(context.Background())
	return t
}

// Name returns the target name.
func (t *InMemoryTarget) Name() string { return t.name }

// Caps returns the full capability set.
func (t *InMemoryTarget) Caps() Capability { return CapAll }

// SessionService returns the in-memory session service.
func (t *InMemoryTarget) SessionService() session.Service { return t.sess }

// MemoryService returns the in-memory memory service.
func (t *InMemoryTarget) MemoryService() memory.Service { return t.mem }

// Reset replaces the session service with a fresh instance and clears the
// case user's memories. The memory service is reused across resets because
// its construction loads the tokenizer dictionary, which is slow.
func (t *InMemoryTarget) Reset(ctx context.Context) error {
	if t.sess != nil {
		_ = t.sess.Close()
	}
	t.sess = sinmemory.NewSessionService(sinmemory.WithSummarizer(NewFakeSummarizer()))
	if t.mem == nil {
		t.mem = minmemory.NewMemoryService()
		return nil
	}
	return t.mem.ClearMemories(ctx, memory.UserKey{AppName: CaseAppName, UserID: CaseUserID})
}

// Close releases both services.
func (t *InMemoryTarget) Close() error {
	if t.sess != nil {
		_ = t.sess.Close()
	}
	if t.mem != nil {
		_ = t.mem.Close()
	}
	return nil
}
