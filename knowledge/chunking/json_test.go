package chunking

import (
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

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
		var jsonData interface{}
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
		var jsonData interface{}
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
		var jsonData interface{}
		if err := json.Unmarshal([]byte(chunk.Content), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}

		// Verify chunk metadata.
		if chunk.Metadata["json_chunk"] != true {
			t.Errorf("Chunk %d missing json_chunk metadata", i)
		}
		if chunk.Metadata["chunk_type"] != "json" {
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
		var jsonData interface{}
		if err := json.Unmarshal([]byte(chunk), &jsonData); err != nil {
			t.Errorf("Chunk %d is not valid JSON: %v", i, err)
		}
	}
}
