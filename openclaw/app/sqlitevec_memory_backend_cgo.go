//go:build cgo && openclaw_sqlitevec

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
	"strings"

	memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const sqliteVecMemoryBackendEnabled = true

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
