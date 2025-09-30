//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package knowledge

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
)

func TestVectorStoreFunctionalOptions(t *testing.T) {
	// Create an in-memory vector store
	vs := inmemory.New()
	ctx := context.Background()

	// Test Count with no options
	count, err := vs.Count(ctx)
	if err != nil {
		t.Fatalf("Count with no options failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected count 0, got %d", count)
	}

	// Test Count with filter option
	count, err = vs.Count(ctx, vectorstore.WithCountFilter(map[string]any{"test": "value"}))
	if err != nil {
		t.Fatalf("Count with filter failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("Expected count 0, got %d", count)
	}

	// Test GetMetadata with no options
	metadata, err := vs.GetMetadata(ctx)
	if err != nil {
		t.Fatalf("GetMetadata with no options failed: %v", err)
	}
	if len(metadata) != 0 {
		t.Fatalf("Expected empty metadata, got %d items", len(metadata))
	}

	// Test GetMetadata with options
	metadata, err = vs.GetMetadata(ctx,
		vectorstore.WithGetMetadataLimit(10),
		vectorstore.WithGetMetadataOffset(0),
		vectorstore.WithGetMetadataFilter(map[string]any{"test": "value"}))
	if err != nil {
		t.Fatalf("GetMetadata with options failed: %v", err)
	}
	if len(metadata) != 0 {
		t.Fatalf("Expected empty metadata, got %d items", len(metadata))
	}

	// Test DeleteByFilter with no options (should fail as no conditions specified)
	err = vs.DeleteByFilter(ctx)
	if err == nil {
		t.Fatal("Expected DeleteByFilter with no options to fail")
	}

	// Test DeleteByFilter with delete all option
	err = vs.DeleteByFilter(ctx, vectorstore.WithDeleteAll(true))
	if err != nil {
		t.Fatalf("DeleteByFilter with delete all failed: %v", err)
	}

	// Test DeleteByFilter with filter option
	err = vs.DeleteByFilter(ctx, vectorstore.WithDeleteFilter(map[string]any{"test": "value"}))
	if err != nil {
		t.Fatalf("DeleteByFilter with filter failed: %v", err)
	}

	// Test DeleteByFilter with document IDs option
	err = vs.DeleteByFilter(ctx, vectorstore.WithDeleteDocumentIDs([]string{"doc1", "doc2"}))
	if err != nil {
		t.Fatalf("DeleteByFilter with document IDs failed: %v", err)
	}
}

func TestVectorStoreOptionsParsing(t *testing.T) {
	// Test DeleteOptions parsing
	config := vectorstore.ApplyDeleteOptions(
		vectorstore.WithDeleteAll(true),
		vectorstore.WithDeleteFilter(map[string]any{"key": "value"}),
		vectorstore.WithDeleteDocumentIDs([]string{"doc1", "doc2"}),
	)

	if !config.DeleteAll {
		t.Error("Expected DeleteAll to be true")
	}
	if config.Filter["key"] != "value" {
		t.Error("Expected filter key to be 'value'")
	}
	if len(config.DocumentIDs) != 2 {
		t.Errorf("Expected 2 document IDs, got %d", len(config.DocumentIDs))
	}

	// Test CountOptions parsing
	countConfig := vectorstore.ApplyCountOptions(
		vectorstore.WithCountFilter(map[string]any{"test": "data"}),
	)

	if countConfig.Filter["test"] != "data" {
		t.Error("Expected filter test to be 'data'")
	}

	// Test GetMetadataOptions parsing
	metaConfig, err := vectorstore.ApplyGetMetadataOptions(
		vectorstore.WithGetMetadataIDs([]string{"id1", "id2"}),
		vectorstore.WithGetMetadataFilter(map[string]any{"meta": "test"}),
		vectorstore.WithGetMetadataLimit(100),
		vectorstore.WithGetMetadataOffset(10),
	)

	if err != nil {
		t.Errorf("ApplyGetMetadataOptions failed: %v", err)
	}

	if len(metaConfig.IDs) != 2 {
		t.Errorf("Expected 2 IDs, got %d", len(metaConfig.IDs))
	}
	if metaConfig.Filter["meta"] != "test" {
		t.Error("Expected filter meta to be 'test'")
	}
	if metaConfig.Limit != 100 {
		t.Errorf("Expected limit 100, got %d", metaConfig.Limit)
	}
	if metaConfig.Offset != 10 {
		t.Errorf("Expected offset 10, got %d", metaConfig.Offset)
	}
}
