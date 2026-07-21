//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis binds the Redis session and memory backends to the
// session/replaytest harness. It is an integration-mode binding: the test
// is skipped unless TRPC_REPLAYTEST_REDIS_URL is set.
package redis

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	mredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// Target pairs the Redis session service with the Redis memory service.
// Reset rotates the key prefix so cases are isolated without flushing the
// server (safe against shared instances).
type Target struct {
	name string
	url  string
	seq  int

	sessSvc *redis.Service
	memSvc  *mredis.Service
}

// NewTarget creates a Redis target against the given URL,
// e.g. "redis://localhost:6379".
func NewTarget(name, url string) (*Target, error) {
	t := &Target{name: name, url: url}
	if err := t.Reset(context.Background()); err != nil {
		return nil, err
	}
	return t, nil
}

// Name returns the target name.
func (t *Target) Name() string { return t.name }

// Caps returns the full capability set.
func (t *Target) Caps() replaytest.Capability { return replaytest.CapAll }

// SessionService returns the Redis session service.
func (t *Target) SessionService() session.Service { return t.sessSvc }

// MemoryService returns the Redis memory service.
func (t *Target) MemoryService() memory.Service { return t.memSvc }

// Reset recreates both services under a fresh key prefix. Session-side keys
// carry a one-hour TTL so replaytest residue on shared instances expires on
// its own; memory/redis has no TTL option, so its keys rely on the rotating
// prefix alone (see the README's integration-mode note).
func (t *Target) Reset(ctx context.Context) error {
	t.closeServices()
	t.seq++
	prefix := fmt.Sprintf("replaytest:%s:%d", t.name, t.seq)
	sessSvc, err := redis.NewService(
		redis.WithRedisClientURL(t.url),
		redis.WithKeyPrefix(prefix+":sess"),
		redis.WithSummarizer(replaytest.NewFakeSummarizer()),
		redis.WithSessionTTL(time.Hour),
		redis.WithAppStateTTL(time.Hour),
		redis.WithUserStateTTL(time.Hour),
	)
	if err != nil {
		return fmt.Errorf("create redis session service: %w", err)
	}
	memSvc, err := mredis.NewService(
		mredis.WithRedisClientURL(t.url),
		mredis.WithKeyPrefix(prefix+":mem"),
	)
	if err != nil {
		sessSvc.Close()
		return fmt.Errorf("create redis memory service: %w", err)
	}
	t.sessSvc, t.memSvc = sessSvc, memSvc
	return nil
}

// Close releases both services.
func (t *Target) Close() error {
	t.closeServices()
	return nil
}

// closeServices closes both services.
func (t *Target) closeServices() {
	if t.sessSvc != nil {
		t.sessSvc.Close()
	}
	if t.memSvc != nil {
		t.memSvc.Close()
	}
	t.sessSvc, t.memSvc = nil, nil
}
