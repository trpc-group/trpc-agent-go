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
	"strings"

	_ "github.com/mattn/go-sqlite3"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"

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

func newSQLiteVecMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	var cfg sqliteVecMemoryConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	db, err := openSQLiteMemoryDB(
		cfg.Path,
		cfg.DSN,
		sqliteVecMemoryConfigErrMissingPath,
	)
	if err != nil {
		return nil, err
	}

	emb, err := newOpenAIEmbedder(cfg.Embedder)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	opts := make([]memorysqlitevec.ServiceOpt, 0, 8)
	opts = append(opts, memorysqlitevec.WithEmbedder(emb))
	if spec.Limit > 0 {
		opts = append(opts, memorysqlitevec.WithMemoryLimit(spec.Limit))
	}
	if deps.Extractor != nil {
		opts = append(opts, memorysqlitevec.WithExtractor(deps.Extractor))
	}
	if cfg.SkipDBInit {
		opts = append(opts, memorysqlitevec.WithSkipDBInit(true))
	}
	if cfg.SoftDelete != nil {
		opts = append(opts, memorysqlitevec.WithSoftDelete(*cfg.SoftDelete))
	}
	if name := strings.TrimSpace(cfg.TableName); name != "" {
		opt, err := safeOption(memorysqlitevec.WithTableName, name)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		opts = append(opts, opt)
	}
	if cfg.IndexDimension > 0 {
		opts = append(
			opts,
			memorysqlitevec.WithIndexDimension(cfg.IndexDimension),
		)
	}
	if cfg.MaxResults > 0 {
		opts = append(opts, memorysqlitevec.WithMaxResults(cfg.MaxResults))
	}

	svc, err := memorysqlitevec.NewService(db, opts...)
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
	resolvedDSN := strings.TrimSpace(dsn)
	resolvedPath := strings.TrimSpace(path)
	if resolvedDSN == "" {
		resolvedDSN = resolvedPath
	}
	if resolvedDSN == "" {
		return nil, errors.New(missingPathErr)
	}

	if err := ensureSQLiteDir(resolvedPath); err != nil {
		return nil, err
	}

	db, err := sql.Open(sqliteDriverName, resolvedDSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(defaultSQLiteMaxOpenConns)
	db.SetMaxIdleConns(defaultSQLiteMaxIdleConns)
	return db, nil
}
