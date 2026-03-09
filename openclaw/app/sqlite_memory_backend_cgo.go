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
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func newSQLiteMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg sqliteMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	db, err := openSQLiteMemoryDB(
		cfg.Path,
		cfg.DSN,
		sqliteMemoryConfigErrMissingPath,
	)
	if err != nil {
		return nil, err
	}

	opts := make([]memorysqlite.ServiceOpt, 0, 5)
	if spec.Limit > 0 {
		opts = append(opts, memorysqlite.WithMemoryLimit(spec.Limit))
	}
	if deps.Extractor != nil {
		opts = append(opts, memorysqlite.WithExtractor(deps.Extractor))
	}
	if cfg.SkipDBInit {
		opts = append(opts, memorysqlite.WithSkipDBInit(true))
	}
	if cfg.SoftDelete != nil {
		opts = append(opts, memorysqlite.WithSoftDelete(*cfg.SoftDelete))
	}
	if name := strings.TrimSpace(cfg.TableName); name != "" {
		opt, err := safeOption(memorysqlite.WithTableName, name)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		opts = append(opts, opt)
	}

	svc, err := memorysqlite.NewService(db, opts...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return svc, nil
}

func openSQLiteMemoryDB(
	path string,
	dsn string,
	missingPathErr string,
) (*sql.DB, error) {
	resolvedPath, resolvedDSN, err := resolveSQLiteDSN(
		path,
		dsn,
		missingPathErr,
	)
	if err != nil {
		return nil, err
	}

	if resolvedPath != "" {
		if err := ensureSQLiteDir(resolvedPath); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open(sqliteDriverName, resolvedDSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(defaultSQLiteMaxOpenConns)
	db.SetMaxIdleConns(defaultSQLiteMaxIdleConns)
	return db, nil
}
