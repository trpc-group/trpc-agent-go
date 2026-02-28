//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

const (
	sqliteDriverName = "sqlite3"

	envSQLiteDSN    = "SQLITE_DSN"
	envSQLiteVecDSN = "SQLITEVEC_DSN"

	sqliteDBDirName      = "sqlite_dbs"
	sqliteBusyTimeoutMS  = 5000
	defaultSQLiteMaxConn = 1
)

func createSQLiteService(
	opts memoryServiceOptions,
) (memory.Service, error) {
	tableName := sqliteTableDefault
	var ext extractor.MemoryExtractor
	if opts.enableExtractor {
		log.Printf("Creating sqlite memory service with extractor")
		tableName = sqliteTableAuto
		ext = extractor.NewExtractor(
			opts.extractorModel,
			extractor.WithPrompt(benchmarkExtractorPrompt),
		)
	} else {
		log.Printf("Creating sqlite memory service")
	}

	dsn, err := buildSQLiteDSN(envSQLiteDSN, tableName)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(defaultSQLiteMaxConn)
	db.SetMaxIdleConns(defaultSQLiteMaxConn)

	svcOpts := []memorysqlite.ServiceOpt{
		memorysqlite.WithTableName(tableName),
		memorysqlite.WithExtractor(ext),
	}
	if opts.enableExtractor {
		svcOpts = append(
			svcOpts,
			memorysqlite.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			memorysqlite.WithMemoryQueueSize(autoMemoryQueueSize),
			memorysqlite.WithMemoryJobTimeout(autoMemoryJobTimeout),
		)
	}

	svc, err := memorysqlite.NewService(db, svcOpts...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return svc, nil
}

func createSQLiteVecService(
	opts memoryServiceOptions,
) (memory.Service, error) {
	tableName := sqliteVecTableDefault
	var ext extractor.MemoryExtractor
	if opts.enableExtractor {
		log.Printf("Creating sqlitevec memory service with extractor")
		tableName = sqliteVecTableAuto
		ext = extractor.NewExtractor(
			opts.extractorModel,
			extractor.WithPrompt(benchmarkExtractorPrompt),
		)
	} else {
		log.Printf("Creating sqlitevec memory service")
	}

	dsn, err := buildSQLiteDSN(envSQLiteVecDSN, tableName)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(sqliteDriverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlitevec db: %w", err)
	}
	db.SetMaxOpenConns(defaultSQLiteMaxConn)
	db.SetMaxIdleConns(defaultSQLiteMaxConn)

	embedModelName := getEmbedModelName()
	emb := newEmbeddingEmbedder(embedModelName)

	svcOpts := []memorysqlitevec.ServiceOpt{
		memorysqlitevec.WithTableName(tableName),
		memorysqlitevec.WithEmbedder(emb),
		memorysqlitevec.WithExtractor(ext),
	}
	if opts.enableExtractor {
		svcOpts = append(
			svcOpts,
			memorysqlitevec.WithAsyncMemoryNum(autoMemoryAsyncWorkers),
			memorysqlitevec.WithMemoryQueueSize(autoMemoryQueueSize),
			memorysqlitevec.WithMemoryJobTimeout(autoMemoryJobTimeout),
		)
	}

	svc, err := memorysqlitevec.NewService(db, svcOpts...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return svc, nil
}

func buildSQLiteDSN(envKey string, tableName string) (string, error) {
	if dsn := os.Getenv(envKey); dsn != "" {
		return dsn, nil
	}

	outDir := *flagOutput
	dbDir := filepath.Join(outDir, sqliteDBDirName)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return "", fmt.Errorf("create sqlite db dir: %w", err)
	}

	dbPath := filepath.Join(dbDir, tableName+".db")
	_ = os.Remove(dbPath)

	return fmt.Sprintf(
		"file:%s?_busy_timeout=%d",
		dbPath,
		sqliteBusyTimeoutMS,
	), nil
}
