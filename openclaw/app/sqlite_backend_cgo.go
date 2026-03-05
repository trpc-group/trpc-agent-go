//go:build cgo

//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func newSQLiteSessionBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	var cfg sqliteSessionConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	dsn := strings.TrimSpace(cfg.DSN)
	path := strings.TrimSpace(cfg.Path)
	if dsn == "" {
		dsn = path
	}
	if dsn == "" {
		return nil, errors.New(sqliteSessionConfigErrMissingPath)
	}

	if err := ensureSQLiteDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	opts := make([]sessionsqlite.ServiceOpt, 0, 4)
	if cfg.SkipDBInit {
		opts = append(opts, sessionsqlite.WithSkipDBInit(true))
	}
	if pref := strings.TrimSpace(cfg.TablePref); pref != "" {
		if err := sqldb.ValidateTablePrefix(pref); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf(
				"invalid sqlite table prefix %q: %w",
				pref,
				err,
			)
		}
		opts = append(opts, sessionsqlite.WithTablePrefix(pref))
	}
	if deps.Summarizer != nil {
		opts = append(opts, sessionsqlite.WithSummarizer(deps.Summarizer))
	}

	svc, err := sessionsqlite.NewService(db, opts...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return svc, nil
}

func ensureSQLiteDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" || path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if strings.TrimSpace(dir) == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir session sqlite dir: %w", err)
	}
	return nil
}
