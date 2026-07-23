//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite wires SQLite session and memory services into replaytest.
package sqlite

import (
	"database/sql"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

type factoryOpts struct {
	sessionOpts []sessionsqlite.ServiceOpt
	memoryOpts  []memorysqlite.ServiceOpt
}

// FactoryOpt configures the SQLite replay backend factory.
type FactoryOpt func(*factoryOpts)

// WithSessionOpts appends options for the SQLite session service.
func WithSessionOpts(opts ...sessionsqlite.ServiceOpt) FactoryOpt {
	return func(o *factoryOpts) {
		o.sessionOpts = append(o.sessionOpts, opts...)
	}
}

// WithMemoryOpts appends options for the SQLite memory service.
func WithMemoryOpts(opts ...memorysqlite.ServiceOpt) FactoryOpt {
	return func(o *factoryOpts) {
		o.memoryOpts = append(o.memoryOpts, opts...)
	}
}

// WithSkipDBInit controls table initialization for both SQLite services.
func WithSkipDBInit(skip bool) FactoryOpt {
	return func(o *factoryOpts) {
		o.sessionOpts = append(o.sessionOpts, sessionsqlite.WithSkipDBInit(skip))
		o.memoryOpts = append(o.memoryOpts, memorysqlite.WithSkipDBInit(skip))
	}
}

// NewFactory creates a replaytest backend factory backed by SQLite.
//
// The returned factory creates both the session service and memory service on
// the same *sql.DB. The caller owns opening the database handle; the created
// services follow their normal lifecycle and close that handle from Close().
//
// Example:
//
//	import (
//		"database/sql"
//
//		_ "github.com/mattn/go-sqlite3"
//		replaytestsqlite "trpc.group/trpc-go/trpc-agent-go/session/replaytest/sqlite"
//	)
//
//	db, err := sql.Open("sqlite3", ":memory:")
//	if err != nil {
//		return err
//	}
//	factory := replaytestsqlite.NewFactory(db)
//	sessionSvc, memorySvc, profile, err := factory()
//	if err != nil {
//		return err
//	}
//	defer sessionSvc.Close()
//	defer memorySvc.Close()
//	_ = profile
func NewFactory(db *sql.DB, opts ...FactoryOpt) replaytest.BackendFactory {
	cfg := factoryOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}
	sessionOpts := append([]sessionsqlite.ServiceOpt(nil), cfg.sessionOpts...)
	memoryOpts := append([]memorysqlite.ServiceOpt(nil), cfg.memoryOpts...)

	return func() (
		session.Service,
		memory.Service,
		replaytest.BackendProfile,
		error,
	) {
		sessionSvc, err := sessionsqlite.NewService(db, sessionOpts...)
		if err != nil {
			return nil, nil, replaytest.BackendProfile{}, fmt.Errorf(
				"create sqlite session service: %w", err,
			)
		}
		memorySvc, err := memorysqlite.NewService(db, memoryOpts...)
		if err != nil {
			_ = sessionSvc.Close()
			return nil, nil, replaytest.BackendProfile{}, fmt.Errorf(
				"create sqlite memory service: %w", err,
			)
		}
		return sessionSvc, memorySvc, replaytest.SQLiteProfile(), nil
	}
}
