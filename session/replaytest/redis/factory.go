//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis wires Redis session and memory services into replaytest.
package redis

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

type factoryOpts struct {
	sessionOpts []sessionredis.ServiceOpt
	memoryOpts  []memoryredis.ServiceOpt
}

// FactoryOpt configures the Redis replay backend factory.
type FactoryOpt func(*factoryOpts)

// WithSessionOpts appends options for the Redis session service.
func WithSessionOpts(opts ...sessionredis.ServiceOpt) FactoryOpt {
	return func(o *factoryOpts) {
		o.sessionOpts = append(o.sessionOpts, opts...)
	}
}

// WithMemoryOpts appends options for the Redis memory service.
func WithMemoryOpts(opts ...memoryredis.ServiceOpt) FactoryOpt {
	return func(o *factoryOpts) {
		o.memoryOpts = append(o.memoryOpts, opts...)
	}
}

// NewFactory creates a replaytest backend factory backed by Redis.
//
// The url argument is typically read from REDIS_ADDR by the caller. It accepts
// either a Redis URL such as redis://localhost:6379 or a bare host:port value
// such as localhost:6379.
func NewFactory(url string, opts ...FactoryOpt) replaytest.BackendFactory {
	cfg := factoryOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}
	redisURL := normalizeURL(url)
	sessionOpts := append([]sessionredis.ServiceOpt{
		sessionredis.WithRedisClientURL(redisURL),
	}, cfg.sessionOpts...)
	memoryOpts := append([]memoryredis.ServiceOpt{
		memoryredis.WithRedisClientURL(redisURL),
	}, cfg.memoryOpts...)

	return func() (
		session.Service,
		memory.Service,
		replaytest.BackendProfile,
		error,
	) {
		sessionSvc, err := sessionredis.NewService(sessionOpts...)
		if err != nil {
			return nil, nil, replaytest.BackendProfile{}, fmt.Errorf(
				"create redis session service: %w", err,
			)
		}
		memorySvc, err := memoryredis.NewService(memoryOpts...)
		if err != nil {
			_ = sessionSvc.Close()
			return nil, nil, replaytest.BackendProfile{}, fmt.Errorf(
				"create redis memory service: %w", err,
			)
		}
		return sessionSvc, memorySvc, replaytest.RedisProfile(), nil
	}
}

func normalizeURL(url string) string {
	if strings.Contains(url, "://") {
		return url
	}
	return "redis://" + url
}
