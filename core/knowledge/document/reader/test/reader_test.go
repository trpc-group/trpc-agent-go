package test

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/text"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/markdown"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/readerfactory"
)

func TestTextReader(t *testing.T) {
	config := &reader.Config{
		Chunk:     true,
		ChunkSize: 100,
		Overlap:   0,
	}

	reader := text.New(config)
	content := "This is a test document with some content that should be chunked properly."
	documents, err := reader.Read(content, "test.txt")

	if err != nil {
		t.Fatalf("Failed to read document: %v", err)
	}

	if len(documents) == 0 {
		t.Fatal("Expected at least one document")
	}

	// Check that the first document has content.
	firstDoc := documents[0]
	if firstDoc.Content == "" {
		t.Fatal("Document should have content")
	}

	// Check that content is not empty.
	if len(firstDoc.Content) == 0 {
		t.Fatal("Document content should not be empty")
	}
}

func TestMarkdownReader(t *testing.T) {
	config := &reader.Config{
		Chunk:     true,
		ChunkSize: 200,
		Overlap:   0,
	}

	reader := markdown.New(config)
	content := "# Test Document\n\nThis is a test markdown document.\n\n## Section 1\n\nSome content here.\n\n## Section 2\n\nMore content here."
	documents, err := reader.Read(content, "test.md")

	if err != nil {
		t.Fatalf("Failed to read markdown document: %v", err)
	}

	if len(documents) == 0 {
		t.Fatal("Expected at least one document")
	}

	// Check that the first document has content.
	firstDoc := documents[0]
	if firstDoc.Content == "" {
		t.Fatal("Document should have content")
	}
}

func TestReaderFactory(t *testing.T) {
	factory := readerfactory.NewFactory()

	// Test text file reader.
	reader := factory.CreateReader("test.txt")
	if reader == nil {
		t.Fatal("Expected text reader")
	}

	// Test markdown file reader.
	reader = factory.CreateReader("test.md")
	if reader == nil {
		t.Fatal("Expected markdown reader")
	}

	// Test CSV file reader.
	reader = factory.CreateReader("test.csv")
	if reader == nil {
		t.Fatal("Expected CSV reader")
	}

	// Test JSON file reader.
	reader = factory.CreateReader("test.json")
	if reader == nil {
		t.Fatal("Expected JSON reader")
	}

	// Test PDF file reader.
	reader = factory.CreateReader("test.pdf")
	if reader == nil {
		t.Fatal("Expected PDF reader")
	}

	// Test unknown file type (should default to text).
	reader = factory.CreateReader("test.unknown")
	if reader == nil {
		t.Fatal("Expected default text reader")
	}
}
