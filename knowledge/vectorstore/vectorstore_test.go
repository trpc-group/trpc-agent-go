//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package vectorstore

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func TestDeleteConfigDefaults(t *testing.T) {
	config := ApplyDeleteOptions()

	if config == nil {
		t.Fatal("Expected non-nil config")
	}
	if config.DeleteAll {
		t.Error("Expected DeleteAll to be false by default")
	}
	if len(config.DocumentIDs) != 0 {
		t.Error("Expected empty DocumentIDs by default")
	}
	if config.Filter != nil {
		t.Error("Expected nil Filter by default")
	}
}

func TestCountConfigDefaults(t *testing.T) {
	config := ApplyCountOptions()

	if config == nil {
		t.Fatal("Expected non-nil config")
	}
	if config.Filter != nil {
		t.Error("Expected nil Filter by default")
	}
}

func TestGetMetadataConfigDefaults(t *testing.T) {
	config, err := ApplyGetMetadataOptions()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if config.Limit != -1 {
		t.Errorf("Expected Limit -1, got %d", config.Limit)
	}
	if config.Offset != -1 {
		t.Errorf("Expected Offset -1, got %d", config.Offset)
	}
	if len(config.IDs) != 0 {
		t.Error("Expected empty IDs by default")
	}
	if config.Filter != nil {
		t.Error("Expected nil Filter by default")
	}
}

func TestApplyGetMetadataOptionsValidation(t *testing.T) {
	// Test zero limit
	_, err := ApplyGetMetadataOptions(WithGetMetadataLimit(0))
	if err == nil {
		t.Error("Expected error for zero limit")
	}

	// Test negative limit with positive offset
	_, err = ApplyGetMetadataOptions(WithGetMetadataLimit(-1), WithGetMetadataOffset(10))
	if err == nil {
		t.Error("Expected error for negative limit with positive offset")
	}

	// Test valid case: positive limit, zero offset
	config, err := ApplyGetMetadataOptions(WithGetMetadataLimit(10), WithGetMetadataOffset(0))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if config.Offset != 0 {
		t.Errorf("Expected offset 0, got %d", config.Offset)
	}
}

func TestSearchQueryDefaults(t *testing.T) {
	query := &SearchQuery{}

	if query.Limit != 0 {
		t.Errorf("Expected Limit 0, got %d", query.Limit)
	}
	if query.MinScore != 0 {
		t.Errorf("Expected MinScore 0, got %f", query.MinScore)
	}
	if query.Vector != nil {
		t.Error("Expected nil Vector by default")
	}
	if query.Filter != nil {
		t.Error("Expected nil Filter by default")
	}
}

func TestSearchModeConstants(t *testing.T) {
	if SearchModeHybrid != 0 {
		t.Errorf("Expected SearchModeHybrid 0, got %d", SearchModeHybrid)
	}
	if SearchModeVector != 1 {
		t.Errorf("Expected SearchModeVector 1, got %d", SearchModeVector)
	}
	if SearchModeKeyword != 2 {
		t.Errorf("Expected SearchModeKeyword 2, got %d", SearchModeKeyword)
	}
	if SearchModeFilter != 3 {
		t.Errorf("Expected SearchModeFilter 3, got %d", SearchModeFilter)
	}
}

func TestScoredDocumentDefaults(t *testing.T) {
	doc := &ScoredDocument{}

	if doc.Document != nil {
		t.Error("Expected nil Document by default")
	}
	if doc.Score != 0 {
		t.Errorf("Expected Score 0, got %f", doc.Score)
	}
}

func TestSearchResultDefaults(t *testing.T) {
	result := &SearchResult{}

	if result.Results != nil {
		t.Error("Expected nil Results slice by default")
	}
}

func TestDocumentMetadataDefaults(t *testing.T) {
	meta := &DocumentMetadata{}

	if meta.Metadata != nil {
		t.Error("Expected nil Metadata map by default")
	}
}

func TestSearchFilterDefaults(t *testing.T) {
	filter := &SearchFilter{}

	if filter.IDs != nil {
		t.Error("Expected nil IDs slice by default")
	}
	if filter.Metadata != nil {
		t.Error("Expected nil Metadata map by default")
	}
}

func TestDeleteOptions(t *testing.T) {
	// Test WithDeleteDocumentIDs
	config := ApplyDeleteOptions(WithDeleteDocumentIDs([]string{"id1", "id2"}))
	if len(config.DocumentIDs) != 2 {
		t.Errorf("Expected 2 document IDs, got %d", len(config.DocumentIDs))
	}

	// Test WithDeleteFilter
	filter := map[string]any{"key": "value"}
	config = ApplyDeleteOptions(WithDeleteFilter(filter))
	if config.Filter["key"] != "value" {
		t.Error("Expected filter to contain key=value")
	}

	// Test WithDeleteAll
	config = ApplyDeleteOptions(WithDeleteAll(true))
	if !config.DeleteAll {
		t.Error("Expected DeleteAll to be true")
	}
}

func TestCountOptions(t *testing.T) {
	filter := map[string]any{"test": "data"}
	config := ApplyCountOptions(WithCountFilter(filter))

	if config.Filter["test"] != "data" {
		t.Error("Expected filter to contain test=data")
	}
}

func TestGetMetadataOptions(t *testing.T) {
	// Test WithGetMetadataIDs
	config, err := ApplyGetMetadataOptions(WithGetMetadataIDs([]string{"id1", "id2"}))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(config.IDs) != 2 {
		t.Errorf("Expected 2 IDs, got %d", len(config.IDs))
	}

	// Test WithGetMetadataFilter
	filter := map[string]any{"meta": "test"}
	config, err = ApplyGetMetadataOptions(WithGetMetadataFilter(filter))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if config.Filter["meta"] != "test" {
		t.Error("Expected filter to contain meta=test")
	}

	// Test WithGetMetadataLimit
	config, err = ApplyGetMetadataOptions(WithGetMetadataLimit(50))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if config.Limit != 50 {
		t.Errorf("Expected limit 50, got %d", config.Limit)
	}

	// Test WithGetMetadataOffset
	config, err = ApplyGetMetadataOptions(WithGetMetadataLimit(10), WithGetMetadataOffset(20))
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if config.Offset != 20 {
		t.Errorf("Expected offset 20, got %d", config.Offset)
	}
}

func TestUpdateByFilterConfigDefaults(t *testing.T) {
	config := &UpdateByFilterConfig{}

	if config.DocumentIDs != nil {
		t.Error("Expected nil DocumentIDs by default")
	}
	if config.FilterCondition != nil {
		t.Error("Expected nil FilterCondition by default")
	}
	if config.Updates != nil {
		t.Error("Expected nil Updates by default")
	}
}

func TestApplyUpdateByFilterOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    []UpdateByFilterOption
		wantErr bool
		errMsg  string
	}{
		{
			name:    "no filter conditions",
			opts:    []UpdateByFilterOption{WithUpdateByFilterUpdates(map[string]any{"name": "test"})},
			wantErr: true,
			errMsg:  "no filter conditions specified",
		},
		{
			name:    "no updates",
			opts:    []UpdateByFilterOption{WithUpdateByFilterDocumentIDs([]string{"id1"})},
			wantErr: true,
			errMsg:  "no updates specified",
		},
		{
			name: "valid with document IDs",
			opts: []UpdateByFilterOption{
				WithUpdateByFilterDocumentIDs([]string{"id1", "id2"}),
				WithUpdateByFilterUpdates(map[string]any{"name": "updated"}),
			},
			wantErr: false,
		},
		{
			name:    "empty document IDs and nil filter",
			opts:    []UpdateByFilterOption{WithUpdateByFilterDocumentIDs([]string{}), WithUpdateByFilterUpdates(map[string]any{"name": "test"})},
			wantErr: true,
			errMsg:  "no filter conditions specified",
		},
		{
			name:    "empty updates map",
			opts:    []UpdateByFilterOption{WithUpdateByFilterDocumentIDs([]string{"id1"}), WithUpdateByFilterUpdates(map[string]any{})},
			wantErr: true,
			errMsg:  "no updates specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := ApplyUpdateByFilterOptions(tt.opts...)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got nil")
				} else if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("Expected error containing %q, got %q", tt.errMsg, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			if config == nil {
				t.Error("Expected non-nil config")
			}
		})
	}
}

func TestWithUpdateByFilterDocumentIDs(t *testing.T) {
	ids := []string{"doc1", "doc2", "doc3"}
	config := &UpdateByFilterConfig{}
	opt := WithUpdateByFilterDocumentIDs(ids)
	opt(config)

	if len(config.DocumentIDs) != 3 {
		t.Errorf("Expected 3 document IDs, got %d", len(config.DocumentIDs))
	}
	for i, id := range ids {
		if config.DocumentIDs[i] != id {
			t.Errorf("Expected DocumentIDs[%d] = %q, got %q", i, id, config.DocumentIDs[i])
		}
	}
}

func TestWithUpdateByFilterCondition(t *testing.T) {
	cond := &searchfilter.UniversalFilterCondition{
		Field:    "status",
		Operator: "=",
		Value:    "active",
	}
	config := &UpdateByFilterConfig{}
	opt := WithUpdateByFilterCondition(cond)
	opt(config)

	if config.FilterCondition == nil {
		t.Error("Expected non-nil FilterCondition")
		return
	}
	if config.FilterCondition.Field != "status" {
		t.Errorf("Expected Field = 'status', got %q", config.FilterCondition.Field)
	}
}

func TestWithUpdateByFilterUpdates(t *testing.T) {
	updates := map[string]any{
		"name":              "new name",
		"content":           "new content",
		"metadata.category": "updated",
	}
	config := &UpdateByFilterConfig{}
	opt := WithUpdateByFilterUpdates(updates)
	opt(config)

	if len(config.Updates) != 3 {
		t.Errorf("Expected 3 updates, got %d", len(config.Updates))
	}
	if config.Updates["name"] != "new name" {
		t.Errorf("Expected name = 'new name', got %v", config.Updates["name"])
	}
	if config.Updates["content"] != "new content" {
		t.Errorf("Expected content = 'new content', got %v", config.Updates["content"])
	}
	if config.Updates["metadata.category"] != "updated" {
		t.Errorf("Expected metadata.category = 'updated', got %v", config.Updates["metadata.category"])
	}
}

func TestApplyUpdateByFilterOptionsWithFilterCondition(t *testing.T) {
	cond := &searchfilter.UniversalFilterCondition{
		Field:    "status",
		Operator: "=",
		Value:    "active",
	}
	config, err := ApplyUpdateByFilterOptions(
		WithUpdateByFilterCondition(cond),
		WithUpdateByFilterUpdates(map[string]any{"name": "test"}),
	)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if config.FilterCondition == nil {
		t.Error("Expected non-nil FilterCondition")
	}
	if len(config.Updates) != 1 {
		t.Errorf("Expected 1 update, got %d", len(config.Updates))
	}
}

func TestApplyUpdateByFilterOptionsCombined(t *testing.T) {
	cond := &searchfilter.UniversalFilterCondition{
		Field:    "status",
		Operator: "=",
		Value:    "active",
	}
	config, err := ApplyUpdateByFilterOptions(
		WithUpdateByFilterDocumentIDs([]string{"id1"}),
		WithUpdateByFilterCondition(cond),
		WithUpdateByFilterUpdates(map[string]any{
			"name":              "updated",
			"metadata.category": "new-category",
		}),
	)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(config.DocumentIDs) != 1 {
		t.Errorf("Expected 1 document ID, got %d", len(config.DocumentIDs))
	}
	if config.FilterCondition == nil {
		t.Error("Expected non-nil FilterCondition")
	}
	if len(config.Updates) != 2 {
		t.Errorf("Expected 2 updates, got %d", len(config.Updates))
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
