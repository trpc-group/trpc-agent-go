//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the ClickHouse vector store with fixed vectors.
//
// Unlike the postgres/tcvector examples, this example does NOT require an LLM or
// an embedding service. It uses small fixed vectors so you can verify the core
// vector store operations (Add/Get/Search/Update/Delete/Count) end-to-end against
// a real ClickHouse instance with a single `go run`.
//
// Required environment:
//   - CLICKHOUSE_DSN: (Optional) ClickHouse DSN, defaults to
//     "clickhouse://default:@localhost:9000/default"
//   - CLICKHOUSE_TABLE: (Optional) Table name, defaults to "documents"
//
// Example usage:
//
//	docker run -d --name clickhouse -p 9000:9000 clickhouse/clickhouse-server:latest
//	go run main.go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/clickhouse"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
)

const vectorDim = 3

func main() {
	ctx := context.Background()

	dsn := util.GetEnvOrDefault("CLICKHOUSE_DSN", "clickhouse://default:@localhost:9000/default")
	table := util.GetEnvOrDefault("CLICKHOUSE_TABLE", "clickhouse_vectorstore_example")

	fmt.Println("🏠 ClickHouse Vector Store Demo")
	fmt.Println("===============================")
	fmt.Printf("📊 DSN: %s\n", redactDSN(dsn))
	fmt.Printf("📦 Table: %s\n", table)

	// Build the vector store. autoCreateTable creates the backing table on the
	// first run. The "category" field is materialized as a typed column so it can
	// be used in filters.
	vs, err := clickhouse.New(
		clickhouse.WithDSN(dsn),
		clickhouse.WithTableName(table),
		clickhouse.WithVectorDimension(vectorDim),
		clickhouse.WithMetric(clickhouse.MetricCosine),
		clickhouse.WithFilterFields(
			clickhouse.FilterFieldSpec{Name: "category", Type: clickhouse.FilterFieldString},
		),
	)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	defer vs.Close()

	// ── 1. Add documents ──────────────────────────────────────────────────────
	fmt.Println("\n📥 Adding 3 documents ...")
	docs := []struct {
		doc       *document.Document
		embedding []float64
	}{
		{
			doc: &document.Document{
				ID: "doc1", Name: "Hello", Content: "hello world",
				Metadata: map[string]any{"category": "news"},
			},
			embedding: []float64{1, 0, 0},
		},
		{
			doc: &document.Document{
				ID: "doc2", Name: "ML", Content: "machine learning basics",
				Metadata: map[string]any{"category": "tech"},
			},
			embedding: []float64{1, 1, 0},
		},
		{
			doc: &document.Document{
				ID: "doc3", Name: "DL", Content: "deep learning neural networks",
				Metadata: map[string]any{"category": "tech"},
			},
			embedding: []float64{0, 1, 0},
		},
	}
	for _, d := range docs {
		if err := vs.Add(ctx, d.doc, d.embedding); err != nil {
			log.Fatalf("Add %s failed: %v", d.doc.ID, err)
		}
		fmt.Printf("  ✓ added %s\n", d.doc.ID)
	}

	// ── 2. Get ────────────────────────────────────────────────────────────────
	fmt.Println("\n🔍 Get doc1 ...")
	got, emb, err := vs.Get(ctx, "doc1")
	if err != nil {
		log.Fatalf("Get failed: %v", err)
	}
	fmt.Printf("  ✓ id=%s name=%s content=%q embedding=%v metadata=%v\n",
		got.ID, got.Name, got.Content, emb, got.Metadata)

	// ── 3. Vector search ──────────────────────────────────────────────────────
	fmt.Println("\n🎯 Vector search (query vector [1, 0, 0]) ...")
	results, err := vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1, 0, 0},
		Limit:      3,
	})
	if err != nil {
		log.Fatalf("Vector search failed: %v", err)
	}
	printResults(results)

	// ── 4. Filter search (metadata) ───────────────────────────────────────────
	fmt.Println("\n🎯 Filter search (category = tech) ...")
	results, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Limit:      3,
		Filter:     &vectorstore.SearchFilter{Metadata: map[string]any{"category": "tech"}},
	})
	if err != nil {
		log.Fatalf("Filter search failed: %v", err)
	}
	printResults(results)

	// ── 5. Filter search (searchfilter condition) ─────────────────────────────
	fmt.Println("\n🎯 Filter search (searchfilter: category = tech AND content LIKE '%learning%') ...")
	results, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Limit:      3,
		Filter: &vectorstore.SearchFilter{
			FilterCondition: searchfilter.And(
				searchfilter.Equal("category", "tech"),
				searchfilter.Like("content", "%learning%"),
			),
		},
	})
	if err != nil {
		log.Fatalf("searchfilter search failed: %v", err)
	}
	printResults(results)

	// ── 6. Keyword search ─────────────────────────────────────────────────────
	fmt.Println("\n🎯 Keyword search (query = \"learning\") ...")
	results, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeKeyword,
		Query:      "learning",
		Limit:      3,
	})
	if err != nil {
		log.Fatalf("Keyword search failed: %v", err)
	}
	printResults(results)

	// ── 7. Hybrid search ──────────────────────────────────────────────────────
	fmt.Println("\n🎯 Hybrid search (query = \"learning\", vector [1, 1, 0]) ...")
	results, err = vs.Search(ctx, &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Query:      "learning",
		Vector:     []float64{1, 1, 0},
		Limit:      3,
	})
	if err != nil {
		log.Fatalf("Hybrid search failed: %v", err)
	}
	printResults(results)

	// ── 8. Update ─────────────────────────────────────────────────────────────
	fmt.Println("\n✏️  Update doc1 content ...")
	updated := *docs[0].doc
	updated.Content = "hello world (updated)"
	if err := vs.Update(ctx, &updated, nil); err != nil {
		log.Fatalf("Update failed: %v", err)
	}
	got, _, err = vs.Get(ctx, "doc1")
	if err != nil {
		log.Fatalf("Get after update failed: %v", err)
	}
	fmt.Printf("  ✓ doc1 content now = %q\n", got.Content)

	// ── 9. Count ──────────────────────────────────────────────────────────────
	fmt.Println("\n🔢 Count ...")
	n, err := vs.Count(ctx)
	if err != nil {
		log.Fatalf("Count failed: %v", err)
	}
	fmt.Printf("  ✓ total = %d\n", n)

	// ── 10. Delete ────────────────────────────────────────────────────────────
	fmt.Println("\n🗑️  Delete doc3 ...")
	if err := vs.Delete(ctx, "doc3"); err != nil {
		log.Fatalf("Delete failed: %v", err)
	}
	// ClickHouse deletes run as asynchronous mutations, so the row can still be
	// counted right after Delete returns. Poll until the expected count shows up.
	n, err = waitForCount(ctx, vs, 2)
	if err != nil {
		log.Fatalf("Count after delete failed: %v", err)
	}
	fmt.Printf("  ✓ total after delete = %d\n", n)

	fmt.Println("\n✅ ClickHouse vector store verification passed.")
}

// waitForCount polls Count until it reports want, and fails if that does not
// happen before the deadline. It exists because Delete is implemented as an
// asynchronous ClickHouse mutation, so the row can still be counted right after
// Delete returns.
func waitForCount(ctx context.Context, vs *clickhouse.VectorStore, want int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var last int
	for {
		n, err := vs.Count(ctx)
		if err != nil {
			return 0, err
		}
		if n == want {
			return n, nil
		}
		last = n
		select {
		case <-ctx.Done():
			return last, fmt.Errorf(
				"timed out waiting for count to reach %d, last observed %d: %w", want, last, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func printResults(results *vectorstore.SearchResult) {
	if len(results.Results) == 0 {
		fmt.Println("  (no results)")
		return
	}
	for i, r := range results.Results {
		fmt.Printf("  %d. [score=%.4f] id=%s content=%q\n",
			i+1, r.Score, r.Document.ID, r.Document.Content)
	}
}

// redactDSN hides the password in a ClickHouse DSN so credentials never leak
// into demo output. It preserves the user and the host/port/database parts.
func redactDSN(dsn string) string {
	scheme, rest := "", dsn
	if i := strings.Index(dsn, "://"); i >= 0 {
		scheme, rest = dsn[:i+3], dsn[i+3:]
	}
	if at := strings.Index(rest, "@"); at >= 0 {
		auth, tail := rest[:at], rest[at:]
		if c := strings.LastIndex(auth, ":"); c >= 0 {
			return scheme + auth[:c] + ":****" + tail
		}
	}
	return dsn
}
