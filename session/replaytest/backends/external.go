//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package backends

import (
	"fmt"
	"os"

	mempg "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	sessionpg "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Environment variables that opt a run in to an external backend. When unset,
// the run stays on inmemory + sqlite so local development needs no services.
const (
	envRedisAddr   = "REPLAYTEST_REDIS_ADDR"
	envPostgresDSN = "REPLAYTEST_POSTGRES_DSN"
)

// externalBackends builds env-gated external backends. Each backend is added
// only when its env var is set; a backend that fails to construct is skipped
// with a warning rather than aborting the whole run, since external services
// are optional integration targets.
func externalBackends(summarizer summary.SessionSummarizer) []*Backend {
	var bs []*Backend
	if addr := os.Getenv(envRedisAddr); addr != "" {
		if b, err := newRedisBackend(addr, summarizer); err == nil {
			bs = append(bs, b)
		} else {
			fmt.Fprintf(os.Stderr, "replaytest: skip redis backend: %v\n", err)
		}
	}
	if dsn := os.Getenv(envPostgresDSN); dsn != "" {
		if b, err := newPostgresBackend(dsn, summarizer); err == nil {
			bs = append(bs, b)
		} else {
			fmt.Fprintf(os.Stderr, "replaytest: skip postgres backend: %v\n", err)
		}
	}
	return bs
}

func newRedisBackend(addr string, summarizer summary.SessionSummarizer) (*Backend, error) {
	sess, err := sessionredis.NewService(
		sessionredis.WithRedisClientURL(addr),
		sessionredis.WithSummarizer(summarizer),
	)
	if err != nil {
		return nil, fmt.Errorf("new redis session service: %w", err)
	}
	mem, err := memredis.NewService(memredis.WithRedisClientURL(addr))
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("new redis memory service: %w", err)
	}
	return &Backend{
		Name:              "redis",
		Session:           sess,
		Memory:            mem,
		SupportsEventPage: false,
		SupportsTTL:       true,
	}, nil
}

func newPostgresBackend(dsn string, summarizer summary.SessionSummarizer) (*Backend, error) {
	sess, err := sessionpg.NewService(
		sessionpg.WithPostgresClientDSN(dsn),
		sessionpg.WithSummarizer(summarizer),
	)
	if err != nil {
		return nil, fmt.Errorf("new postgres session service: %w", err)
	}
	mem, err := mempg.NewService(mempg.WithPostgresClientDSN(dsn))
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("new postgres memory service: %w", err)
	}
	return &Backend{
		Name:              "postgres",
		Session:           sess,
		Memory:            mem,
		SupportsEventPage: true,
		SupportsTTL:       false,
	}, nil
}
