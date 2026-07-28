//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chunking

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
)

func TestOrderedJSONKeysMixedNumericAndLexical(t *testing.T) {
	data := map[string]any{
		"-100000000000000000000": nil,
		"-99999999999999999999":  nil,
		"1a":                     nil,
		"10":                     nil,
		"2":                      nil,
		"02":                     nil,
		"99999999999999999999":   nil,
		"100000000000000000000":  nil,
	}
	want := []string{
		"-100000000000000000000",
		"-99999999999999999999",
		"02",
		"2",
		"10",
		"99999999999999999999",
		"100000000000000000000",
		"1a",
	}

	for i := 0; i < 1000; i++ {
		if got := orderedJSONKeys(data); !slices.Equal(got, want) {
			t.Fatalf("orderedJSONKeys() = %v, want %v", got, want)
		}
	}
}

func TestJSONChunkingSplitJSONRejectsCycles(t *testing.T) {
	data := map[string]any{}
	data["a"] = data
	data["z"] = "after cycle"

	_, err := NewJSONChunking().SplitJSON(data, false)
	if err == nil {
		t.Fatal("SplitJSON() error = nil, want cycle error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("SplitJSON() error = %q, want cycle error", err)
	}
}

func TestJSONChunking(t *testing.T) {
	// Test case 1: Simple JSON object.
	simpleJSON := `{
		"name": "John Doe",
		"age": 30,
		"email": "john@example.com",
		"address": {
			"street": "123 Main St",
			"city": "Anytown",
			"zip": "12345"
		}
	}`

	doc := &document.Document{
		ID:      "test_doc",
		Name:    "test.json",
		Content: simpleJSON,
	}

	chunker := NewJSONChunking(WithJSONChunkSize(100))
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		t.Fatalf("Failed to chunk JSON: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify each chunk is valid JSON.
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk.Content), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}
	}
}

func TestJSONChunkingWithArrays(t *testing.T) {
	// Test case 2: JSON with arrays.
	arrayJSON := `{
		"users": [
			{"id": 1, "name": "Alice", "roles": ["admin", "user"]},
			{"id": 2, "name": "Bob", "roles": ["user"]},
			{"id": 3, "name": "Charlie", "roles": ["moderator", "user"]}
		],
		"settings": {
			"theme": "dark",
			"notifications": true
		}
	}`

	doc := &document.Document{
		ID:      "test_doc_arrays",
		Name:    "test_arrays.json",
		Content: arrayJSON,
	}

	chunker := NewJSONChunking(WithJSONChunkSize(150))
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		t.Fatalf("Failed to chunk JSON with arrays: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify each chunk is valid JSON.
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk.Content), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}
	}
}

func TestJSONChunkingLargeDocument(t *testing.T) {
	// Test case 3: Large JSON document that should be split.
	largeJSON := `{
		"metadata": {
			"version": "1.0",
			"created": "2024-01-01"
		},
		"data": {
			"section1": {
				"title": "First Section",
				"content": "This is a very long content that should be split into multiple chunks when the chunk size is small enough to trigger splitting behavior.",
				"items": ["item1", "item2", "item3", "item4", "item5"]
			},
			"section2": {
				"title": "Second Section", 
				"content": "Another long content section that should also be split if the chunk size is configured appropriately.",
				"items": ["item6", "item7", "item8", "item9", "item10"]
			},
			"section3": {
				"title": "Third Section",
				"content": "Yet another section with substantial content that should be processed by the chunking algorithm.",
				"items": ["item11", "item12", "item13", "item14", "item15"]
			}
		}
	}`

	doc := &document.Document{
		ID:      "test_large_doc",
		Name:    "large_test.json",
		Content: largeJSON,
	}

	// Use a small chunk size to force splitting.
	chunker := NewJSONChunking(WithJSONChunkSize(200))
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		t.Fatalf("Failed to chunk large JSON: %v", err)
	}

	if len(chunks) < 2 {
		t.Error("Expected multiple chunks for large document")
	}

	// Verify each chunk is valid JSON.
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk.Content), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}

		if chunk.Metadata[source.MetaChunkType] != "json" {
			t.Errorf("Chunk %d missing chunk_type metadata", i)
		}
	}
}

func TestJSONChunkingSplitJSONString(t *testing.T) {
	// Test the SplitJSONString method directly.
	jsonStr := `{"name": "Test", "values": [1, 2, 3, 4, 5]}`

	chunker := NewJSONChunking(WithJSONChunkSize(50))
	chunks, err := chunker.SplitJSONString(jsonStr, false)
	if err != nil {
		t.Fatalf("Failed to split JSON string: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify each chunk is valid JSON.
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}
	}
}

// TestWithJSONMinChunkSize tests the WithJSONMinChunkSize option
func TestWithJSONMinChunkSize(t *testing.T) {
	tests := []struct {
		name        string
		minSize     int
		expectedMin int
	}{
		{
			name:        "custom_min_size",
			minSize:     500,
			expectedMin: 500,
		},
		{
			name:        "small_min_size",
			minSize:     10,
			expectedMin: 10,
		},
		{
			name:        "zero_min_size",
			minSize:     0,
			expectedMin: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunker := NewJSONChunking(WithJSONMinChunkSize(tt.minSize))
			if chunker.minChunkSize != tt.expectedMin {
				t.Errorf("Expected minChunkSize %d, got %d", tt.expectedMin, chunker.minChunkSize)
			}
		})
	}
}

// TestJSONChunkingWithConvertLists tests splitJSON with convertLists=true
func TestJSONChunkingWithConvertLists(t *testing.T) {
	jsonStr := `{
		"items": [
			{"id": 1, "name": "item1"},
			{"id": 2, "name": "item2"},
			{"id": 3, "name": "item3"}
		],
		"nested": {
			"list": [10, 20, 30, 40]
		}
	}`

	chunker := NewJSONChunking(WithJSONChunkSize(100))
	chunks, err := chunker.SplitJSONString(jsonStr, true)
	if err != nil {
		t.Fatalf("Failed to split JSON with convertLists: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify each chunk is valid JSON
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}
	}
}

// TestJSONChunkingNameAndString tests Name() and String() methods
func TestJSONChunkingNameAndString(t *testing.T) {
	chunker := NewJSONChunking(WithJSONChunkSize(1000), WithJSONMinChunkSize(800))

	if name := chunker.Name(); name != "JSONChunking" {
		t.Errorf("Expected name 'JSONChunking', got '%s'", name)
	}

	str := chunker.String()
	expectedStr := "JSONChunking(maxChunkSize=1000, minChunkSize=800)"
	if str != expectedStr {
		t.Errorf("Expected string '%s', got '%s'", expectedStr, str)
	}
}

// TestJSONChunkingSplitJSONWithConvertLists tests SplitJSON with convertLists
func TestJSONChunkingSplitJSONWithConvertLists(t *testing.T) {
	data := map[string]any{
		"array": []any{1, 2, 3, 4, 5},
		"nested": map[string]any{
			"inner_array": []any{"a", "b", "c"},
		},
	}

	chunker := NewJSONChunking(WithJSONChunkSize(80))
	chunks, err := chunker.SplitJSON(data, true)
	if err != nil {
		t.Fatalf("Failed to split JSON: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify each chunk is valid JSON
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}
	}
}

// TestJSONChunkingInvalidJSON tests error handling for invalid JSON
func TestJSONChunkingInvalidJSON(t *testing.T) {
	invalidJSON := `{"invalid": json}`

	doc := &document.Document{
		ID:      "invalid",
		Content: invalidJSON,
	}

	chunker := NewJSONChunking()
	_, err := chunker.Chunk(doc)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
}

// TestJSONChunkingSplitJSONStringInvalidJSON tests error handling for SplitJSONString
func TestJSONChunkingSplitJSONStringInvalidJSON(t *testing.T) {
	invalidJSON := `{invalid json content}`

	chunker := NewJSONChunking()
	_, err := chunker.SplitJSONString(invalidJSON, false)
	if err == nil {
		t.Error("Expected error for invalid JSON string, got nil")
	}
}

// TestJSONChunkingNonMapRoot tests handling of non-map root elements
func TestJSONChunkingNonMapRoot(t *testing.T) {
	// Test with array at root
	arrayJSON := `[1, 2, 3, 4, 5]`

	doc := &document.Document{
		ID:      "array_root",
		Content: arrayJSON,
	}

	chunker := NewJSONChunking(WithJSONChunkSize(50))
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		t.Fatalf("Failed to chunk array JSON: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify it's wrapped in a "content" key
	var jsonData map[string]any
	if err := json.Unmarshal([]byte(chunks[0].Content), &jsonData); err != nil {
		t.Fatalf("Chunk is not valid JSON: %v", err)
	}

	if _, exists := jsonData["content"]; !exists {
		t.Error("Expected 'content' key in chunk")
	}
}

// TestJSONChunkingDeepNesting tests deeply nested JSON structures
func TestJSONChunkingDeepNesting(t *testing.T) {
	deepJSON := `{
		"level1": {
			"level2": {
				"level3": {
					"level4": {
						"data": "This is deeply nested data that should be properly chunked",
						"values": [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
					}
				}
			}
		}
	}`

	doc := &document.Document{
		ID:      "deep_nesting",
		Content: deepJSON,
	}

	chunker := NewJSONChunking(WithJSONChunkSize(100))
	chunks, err := chunker.Chunk(doc)
	if err != nil {
		t.Fatalf("Failed to chunk deeply nested JSON: %v", err)
	}

	if len(chunks) == 0 {
		t.Error("Expected at least one chunk")
	}

	// Verify each chunk is valid JSON
	for i, chunk := range chunks {
		var jsonData any
		if err := json.Unmarshal([]byte(chunk.Content), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}

		// Verify chunk metadata
		if chunk.Metadata[source.MetaChunkType] != "json" {
			t.Errorf("Chunk %d missing chunk_type metadata", i)
		}
	}
}

func TestJSONChunkingSplitsLongStringWithinByteBudget(t *testing.T) {
	value := strings.Repeat("中文abc", 30)
	content, err := json.Marshal(map[string]any{
		"payload": map[string]any{
			"text": value,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	chunks, err := NewJSONChunking(
		WithJSONChunkSize(100),
	).Chunk(&document.Document{
		ID:      "long-string",
		Content: string(content),
	})
	if err != nil {
		t.Fatalf("Chunk() error = %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("len(chunks) = %d, want multiple chunks", len(chunks))
	}

	var reconstructed strings.Builder
	for i, chunk := range chunks {
		if len(chunk.Content) > 100 {
			t.Errorf("chunk %d size = %d bytes, want <= 100", i, len(chunk.Content))
		}
		if !utf8.ValidString(chunk.Content) {
			t.Errorf("chunk %d is not valid UTF-8", i)
		}

		var data map[string]any
		if err := json.Unmarshal([]byte(chunk.Content), &data); err != nil {
			t.Fatalf("unmarshal chunk %d: %v", i, err)
		}
		payload, ok := data["payload"].(map[string]any)
		if !ok {
			t.Fatalf("chunk %d payload type = %T", i, data["payload"])
		}
		fragment, ok := payload["text"].(string)
		if !ok {
			t.Fatalf("chunk %d text type = %T", i, payload["text"])
		}
		reconstructed.WriteString(fragment)
	}
	if reconstructed.String() != value {
		t.Errorf("reconstructed value does not match original")
	}
}

func TestJSONChunkingOrderIsDeterministic(t *testing.T) {
	doc := &document.Document{
		ID: "deterministic",
		Content: `{
			"zeta": "last value with enough text to require splitting",
			"alpha": "first value with enough text to require splitting",
			"middle": {
				"item10": "ten",
				"item2": "two"
			}
		}`,
	}
	chunker := NewJSONChunking(WithJSONChunkSize(60))

	var baseline string
	for run := 0; run < 20; run++ {
		chunks, err := chunker.Chunk(doc)
		if err != nil {
			t.Fatalf("Chunk() run %d error = %v", run, err)
		}
		contents := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			contents = append(contents, chunk.Content)
		}
		current := strings.Join(contents, "\x00")
		if run == 0 {
			baseline = current
			if !strings.Contains(chunks[0].Content, `"alpha"`) {
				t.Fatalf("first chunk = %s, want alpha key first", chunks[0].Content)
			}
			continue
		}
		if current != baseline {
			t.Fatalf("Chunk() run %d produced a different order", run)
		}
	}
}
