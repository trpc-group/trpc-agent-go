//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package persistence owns and composes the example's durable resources.
package persistence

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	artifactsqlite "trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/artifact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"

	frameworkartifact "trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"

	_ "modernc.org/sqlite"
)

//go:embed DDL.sql
var sqliteDDL string

// Resources contains the persistence capabilities consumed by the example.
// Close releases all resources created by Open.
type Resources struct {
	ReviewStore     *store.SQLite
	SessionService  session.Service
	ArtifactService frameworkartifact.Service

	applicationDB *sql.DB
}

// Open initializes all persistence capabilities backed by the SQLite file at
// path. The review store and artifact service share a caller-owned connection
// pool. The session service receives its own pool because it owns and closes
// the database passed to sessionsqlite.NewService. appendEventHook runs before
// Session events update in-memory state or reach SQLite.
func Open(ctx context.Context, path string, appendEventHook session.AppendEventHook) (resources *Resources, err error) {
	if path == "" {
		return nil, errors.New("sqlite database path is required")
	}
	if appendEventHook == nil {
		return nil, errors.New("session append event hook is required")
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	applicationDB, err := openDB(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open application database: %w", err)
	}
	if err := initializeSchema(ctx, applicationDB); err != nil {
		_ = applicationDB.Close()
		return nil, err
	}

	reviewStore, err := store.NewSQLite(applicationDB)
	if err != nil {
		_ = applicationDB.Close()
		return nil, fmt.Errorf("create review store: %w", err)
	}
	artifactService, err := artifactsqlite.New(applicationDB)
	if err != nil {
		_ = applicationDB.Close()
		return nil, fmt.Errorf("create artifact service: %w", err)
	}

	sessionDB, err := openDB(ctx, path)
	if err != nil {
		_ = applicationDB.Close()
		return nil, fmt.Errorf("open session database: %w", err)
	}
	sessionService, err := sessionsqlite.NewService(
		sessionDB,
		sessionsqlite.WithAppendEventHook(appendEventHook),
	)
	if err != nil {
		_ = sessionDB.Close()
		_ = applicationDB.Close()
		return nil, fmt.Errorf("create session service: %w", err)
	}

	return &Resources{
		ReviewStore:     reviewStore,
		SessionService:  sessionService,
		ArtifactService: artifactService,
		applicationDB:   applicationDB,
	}, nil
}

// Close releases the session service and the application database.
func (r *Resources) Close() error {
	if r == nil {
		return nil
	}
	var sessionErr, applicationErr error
	if r.SessionService != nil {
		sessionErr = r.SessionService.Close()
	}
	if r.applicationDB != nil {
		applicationErr = r.applicationDB.Close()
	}
	return errors.Join(sessionErr, applicationErr)
}

func openDB(ctx context.Context, path string) (db *sql.DB, err error) {
	// busy_timeout is a per-connection PRAGMA. Put it in the modernc DSN so
	// every connection opened by database/sql receives the policy; executing
	// PRAGMA once would configure only whichever pooled connection happened to
	// serve that call.
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	dsn := path + separator + "_pragma=" + url.QueryEscape("busy_timeout(5000)")
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(0)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite database: %w", err)
	}
	return db, nil
}

func initializeSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, sqliteDDL); err != nil {
		return fmt.Errorf("initialize persistence schema: %w", err)
	}
	return nil
}

func ensureParentDir(path string) error {
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create sqlite parent directory %s: %w", parent, err)
	}
	return nil
}
