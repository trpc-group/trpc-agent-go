//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import "testing"

func TestWithTableName(t *testing.T) {
	opts := defaultOptions
	WithTableName("replay_memories")(&opts)
	if opts.tableName != "replay_memories" {
		t.Fatalf("table name = %q, want replay_memories", opts.tableName)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("WithTableName accepted an unsafe name")
		}
	}()
	WithTableName("memories; DROP TABLE memories")(&opts)
}

func TestSearchOptionsRejectNegativeValues(t *testing.T) {
	opts := defaultOptions
	WithMinSearchScore(-1)(&opts)
	WithMaxResults(-1)(&opts)
	if opts.searchMinScore != defaultOptions.searchMinScore {
		t.Fatalf("search score = %v, want %v", opts.searchMinScore, defaultOptions.searchMinScore)
	}
	if opts.maxSearchResults != defaultOptions.maxSearchResults {
		t.Fatalf("max results = %d, want %d", opts.maxSearchResults, defaultOptions.maxSearchResults)
	}
}
