//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build cgo && sqliteveccgo

package main

import (
	"context"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

func createSQLiteVecService(
	ctx context.Context,
) (memory.Service, func(), error) {
	db, path, err := openTempSQLiteDB(sqliteVecTempDBPattern)
	if err != nil {
		return nil, func() {}, err
	}

	emb := newOpenAIEmbedderFromEnv()
	svc, err := memorysqlitevec.NewService(
		db,
		memorysqlitevec.WithEmbedder(emb),
	)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(path)
		return nil, func() {}, err
	}
	_ = ctx

	cleanup := func() {
		_ = svc.Close()
		_ = os.Remove(path)
	}
	return svc, cleanup, nil
}
