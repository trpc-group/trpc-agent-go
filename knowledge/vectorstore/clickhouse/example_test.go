//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse_test

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/clickhouse"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// Example_connectByDSN demonstrates constructing a ClickHouse vector store from
// a DSN connection string. It is compiled but not executed (no output), because
// it requires a live ClickHouse instance.
func Example_connectByDSN() {
	vs, err := clickhouse.New(
		clickhouse.WithDSN("clickhouse://user:password@localhost:9000/default"),
		clickhouse.WithTableName("documents"),
		clickhouse.WithVectorDimension(1536),
		clickhouse.WithMetric(clickhouse.MetricCosine),
		clickhouse.WithFilterFields(
			clickhouse.FilterFieldSpec{Name: "category", Type: clickhouse.FilterFieldString},
			clickhouse.FilterFieldSpec{Name: "year", Type: clickhouse.FilterFieldInt64},
		),
	)
	if err != nil {
		panic(err)
	}
	defer vs.Close()
}

// Example_connectByInstance demonstrates constructing a ClickHouse vector store
// from a pre-registered named instance.
func Example_connectByInstance() {
	storage.RegisterClickHouseInstance(
		"my-clickhouse",
		storage.WithClientBuilderDSN("clickhouse://user:password@localhost:9000/default"),
	)

	vs, err := clickhouse.New(
		clickhouse.WithInstanceName("my-clickhouse"),
		clickhouse.WithTableName("documents"),
		clickhouse.WithVectorDimension(3),
	)
	if err != nil {
		panic(err)
	}
	defer vs.Close()
}

// Example_search demonstrates vector search with a metadata filter.
func Example_search() {
	vs, err := clickhouse.New(
		clickhouse.WithDSN("clickhouse://user:password@localhost:9000/default"),
		clickhouse.WithTableName("documents"),
		clickhouse.WithVectorDimension(3),
		clickhouse.WithFilterFields(clickhouse.FilterFieldSpec{Name: "category", Type: clickhouse.FilterFieldString}),
	)
	if err != nil {
		return
	}
	defer vs.Close()

	query := &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{0.1, 0.2, 0.3},
		Limit:      10,
		Filter: &vectorstore.SearchFilter{
			FilterCondition: searchfilter.Equal("category", "news"),
		},
	}
	_, _ = vs.Search(context.Background(), query)
}

// Example_add demonstrates adding a document with its embedding.
func Example_add() {
	vs, err := clickhouse.New(
		clickhouse.WithDSN("clickhouse://user:password@localhost:9000/default"),
		clickhouse.WithTableName("documents"),
		clickhouse.WithVectorDimension(3),
	)
	if err != nil {
		return
	}
	defer vs.Close()

	doc := &document.Document{
		ID:       "doc-1",
		Name:     "Introduction",
		Content:  "This is a sample document.",
		Metadata: map[string]any{"category": "news"},
	}
	_ = vs.Add(context.Background(), doc, []float64{0.1, 0.2, 0.3})
}
