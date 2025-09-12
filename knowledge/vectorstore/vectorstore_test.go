package vectorstore

import (
	"testing"
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
	filter := map[string]interface{}{"key": "value"}
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
	filter := map[string]interface{}{"test": "data"}
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
	filter := map[string]interface{}{"meta": "test"}
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
