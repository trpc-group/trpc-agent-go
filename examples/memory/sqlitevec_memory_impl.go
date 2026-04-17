//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build cgo && sqliteveccgo

package util

import (
	"database/sql"

	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

func newSQLiteVecMemoryService(
	cfg MemoryServiceConfig,
) (memory.Service, error) {
	dsn := GetEnvOrDefault(
		sqliteVecMemoryDSNEnvKey,
		defaultSQLiteVecMemoryDBDSN,
	)
	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(defaultSQLiteMaxOpenConns)
	db.SetMaxIdleConns(defaultSQLiteMaxIdleConns)

	embedderModel := GetEnvOrDefault(
		sqliteVecEmbedderModelEnvKey,
		openaiembedder.DefaultModel,
	)
	emb := newOpenAIEmbedder(embedderModel)

	opts := []memorysqlitevec.ServiceOpt{
		memorysqlitevec.WithEmbedder(emb),
		memorysqlitevec.WithSoftDelete(cfg.SoftDelete),
	}

	if cfg.Extractor != nil {
		opts = append(opts, memorysqlitevec.WithExtractor(cfg.Extractor))
		if cfg.AsyncMemoryNum > 0 {
			opts = append(
				opts,
				memorysqlitevec.WithAsyncMemoryNum(cfg.AsyncMemoryNum),
			)
		}
		if cfg.MemoryQueueSize > 0 {
			opts = append(
				opts,
				memorysqlitevec.WithMemoryQueueSize(cfg.MemoryQueueSize),
			)
		}
		if cfg.MemoryJobTimeout > 0 {
			opts = append(
				opts,
				memorysqlitevec.WithMemoryJobTimeout(
					cfg.MemoryJobTimeout,
				),
			)
		}
	}

	svc, err := memorysqlitevec.NewService(db, opts...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return svc, nil
}
