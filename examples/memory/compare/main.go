//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main compares keyword-based SQLite memory with sqlite-vec
// vector memory on a small retrieval-focused dataset.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	memorysqlitevec "trpc.group/trpc-go/trpc-agent-go/memory/sqlitevec"
)

const (
	sqliteDriverName = "sqlite3"

	sqliteTempDBPattern    = "trpc-agent-go-mem-sqlite-*.db"
	sqliteVecTempDBPattern = "trpc-agent-go-mem-sqlitevec-*.db"

	envOpenAIAPIKey  = "OPENAI_API_KEY"
	envOpenAIBaseURL = "OPENAI_BASE_URL"

	envOpenAIEmbeddingAPIKey  = "OPENAI_EMBEDDING_API_KEY"
	envOpenAIEmbeddingBaseURL = "OPENAI_EMBEDDING_BASE_URL"
	envOpenAIEmbeddingModel   = "OPENAI_EMBEDDING_MODEL"
)

type memorySeed struct {
	text   string
	topics []string
}

type queryCase struct {
	query    string
	expected string
}

func main() {
	var topK int
	flag.IntVar(&topK, "k", 3, "Top-k results to evaluate")
	flag.Parse()

	ctx := context.Background()
	userKey := memory.UserKey{
		AppName: "memory-compare",
		UserID:  "user1",
	}

	seeds := []memorySeed{
		{
			text:   "I have a golden retriever named Coco.",
			topics: []string{"pet"},
		},
		{
			text:   "I commute by subway on weekdays.",
			topics: []string{"transport"},
		},
		{
			text:   "My sister is a pediatrician.",
			topics: []string{"family"},
		},
		{
			text:   "I moved to Tokyo in 2021.",
			topics: []string{"location"},
		},
	}

	queries := []queryCase{
		{
			query:    "Do I have a dog? What's its name?",
			expected: seeds[0].text,
		},
		{
			query:    "How do I get to the office?",
			expected: seeds[1].text,
		},
		{
			query:    "What does my sibling do for work?",
			expected: seeds[2].text,
		},
		{
			query:    "When did I relocate to Japan?",
			expected: seeds[3].text,
		},
	}

	sqliteSvc, sqliteCleanup := mustCreateSQLiteService(ctx)
	defer sqliteCleanup()

	sqliteVecSvc, sqliteVecCleanup := mustCreateSQLiteVecService(ctx)
	defer sqliteVecCleanup()

	mustSeed(ctx, sqliteSvc, userKey, seeds)
	mustSeed(ctx, sqliteVecSvc, userKey, seeds)

	fmt.Printf("Dataset: %d memories, %d queries\n", len(seeds), len(queries))
	fmt.Printf("Evaluating hit@%d\n", topK)
	fmt.Println(strings.Repeat("=", 60))

	sqliteHits := 0
	sqliteVecHits := 0

	for i, tc := range queries {
		fmt.Printf("[%d/%d] Query: %q\n", i+1, len(queries), tc.query)

		sqliteStart := time.Now()
		gotSQLite, err := sqliteSvc.SearchMemories(ctx, userKey, tc.query)
		mustNoErr(err, "sqlite search")
		sqliteDur := time.Since(sqliteStart)

		sqliteVecStart := time.Now()
		gotSQLiteVec, err := sqliteVecSvc.SearchMemories(ctx, userKey, tc.query)
		mustNoErr(err, "sqlitevec search")
		sqliteVecDur := time.Since(sqliteVecStart)

		sqliteHit := containsExpected(gotSQLite, tc.expected, topK)
		sqliteVecHit := containsExpected(gotSQLiteVec, tc.expected, topK)

		if sqliteHit {
			sqliteHits++
		}
		if sqliteVecHit {
			sqliteVecHits++
		}

		fmt.Printf(
			"  sqlite:    hit=%t, results=%d, time=%s\n",
			sqliteHit,
			len(gotSQLite),
			sqliteDur.Round(time.Millisecond),
		)
		fmt.Printf(
			"  sqlitevec: hit=%t, results=%d, time=%s\n",
			sqliteVecHit,
			len(gotSQLiteVec),
			sqliteVecDur.Round(time.Millisecond),
		)
		fmt.Println()
	}

	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("sqlite hit@%d: %d/%d\n", topK, sqliteHits, len(queries))
	fmt.Printf(
		"sqlitevec hit@%d: %d/%d\n",
		topK,
		sqliteVecHits,
		len(queries),
	)
}

func mustSeed(
	ctx context.Context,
	svc memory.Service,
	userKey memory.UserKey,
	seeds []memorySeed,
) {
	for _, s := range seeds {
		if err := svc.AddMemory(ctx, userKey, s.text, s.topics); err != nil {
			log.Fatalf("seed memory: %v", err)
		}
	}
}

func containsExpected(
	entries []*memory.Entry,
	expected string,
	k int,
) bool {
	if k <= 0 {
		return false
	}
	if len(entries) < k {
		k = len(entries)
	}
	for i := 0; i < k; i++ {
		if entries[i] == nil || entries[i].Memory == nil {
			continue
		}
		if entries[i].Memory.Memory == expected {
			return true
		}
	}
	return false
}

func mustCreateSQLiteService(
	ctx context.Context,
) (memory.Service, func()) {
	db, path, err := openTempSQLiteDB(sqliteTempDBPattern)
	mustNoErr(err, "open sqlite db")

	svc, err := memorysqlite.NewService(db)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(path)
		log.Fatalf("create sqlite service: %v", err)
	}
	_ = ctx

	cleanup := func() {
		_ = svc.Close()
		_ = os.Remove(path)
	}
	return svc, cleanup
}

func mustCreateSQLiteVecService(
	ctx context.Context,
) (memory.Service, func()) {
	db, path, err := openTempSQLiteDB(sqliteVecTempDBPattern)
	mustNoErr(err, "open sqlitevec db")

	emb := newOpenAIEmbedderFromEnv()
	svc, err := memorysqlitevec.NewService(
		db,
		memorysqlitevec.WithEmbedder(emb),
	)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(path)
		log.Fatalf("create sqlitevec service: %v", err)
	}
	_ = ctx

	cleanup := func() {
		_ = svc.Close()
		_ = os.Remove(path)
	}
	return svc, cleanup
}

func openTempSQLiteDB(pattern string) (*sql.DB, string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return nil, "", err
	}

	db, err := sql.Open(sqliteDriverName, f.Name())
	if err != nil {
		_ = os.Remove(f.Name())
		return nil, "", err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	return db, f.Name(), nil
}

func newOpenAIEmbedderFromEnv() *openaiembedder.Embedder {
	modelName := openaiembedder.DefaultModel
	if env := os.Getenv(envOpenAIEmbeddingModel); env != "" {
		modelName = env
	}

	opts := []openaiembedder.Option{
		openaiembedder.WithModel(modelName),
	}

	apiKey := os.Getenv(envOpenAIEmbeddingAPIKey)
	if apiKey == "" {
		apiKey = os.Getenv(envOpenAIAPIKey)
	}
	if apiKey != "" {
		opts = append(opts, openaiembedder.WithAPIKey(apiKey))
	}

	baseURL := os.Getenv(envOpenAIEmbeddingBaseURL)
	if baseURL == "" {
		baseURL = os.Getenv(envOpenAIBaseURL)
	}
	if baseURL != "" {
		opts = append(opts, openaiembedder.WithBaseURL(baseURL))
	}

	return openaiembedder.New(opts...)
}

func mustNoErr(err error, action string) {
	if err == nil {
		return
	}
	log.Fatalf("%s: %v", action, err)
}
