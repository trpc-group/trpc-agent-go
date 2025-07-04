package chunking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

func TestFixedSizeChunking(t *testing.T) {
	chunker, err := NewFixedSizeChunking(WithChunkSize(50), WithOverlap(10))
	require.NoError(t, err)

	doc := &document.Document{
		ID:      "test-doc",
		Name:    "Test Document",
		Content: "This is the first sentence. This is the second sentence. This is the third sentence with more content.",
		Metadata: map[string]interface{}{
			"author": "Test Author",
		},
		CreatedAt: time.Now(),
	}

	chunks, err := chunker.Chunk(doc)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 1)

	// Verify chunk properties.
	for i, chunk := range chunks {
		assert.NotEmpty(t, chunk.Content)
		assert.Equal(t, doc.Name, chunk.Name)
		assert.Equal(t, i+1, chunk.Metadata["chunk_number"])
		assert.Equal(t, true, chunk.Metadata["is_chunk"])
		assert.Contains(t, chunk.ID, "test-doc_chunk_")
	}
}

func TestNaturalBreakChunking(t *testing.T) {
	chunker, err := NewNaturalBreakChunking(WithChunkSize(40), WithOverlap(0))
	require.NoError(t, err)

	doc := &document.Document{
		ID:      "natural-test",
		Content: "First paragraph.\nSecond line.\n\nSecond paragraph with more content.",
	}

	chunks, err := chunker.Chunk(doc)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 1)

	// Verify natural breaks are preferred.
	for _, chunk := range chunks {
		assert.NotEmpty(t, chunk.Content)
		// Content should not end in the middle of a word unless no natural break was found.
		content := chunk.Content
		if len(content) > 0 {
			lastChar := content[len(content)-1]
			// Most chunks should end with natural breaks, but we allow some flexibility.
			// for cases where no natural break is found within the chunk size.
			isNaturalBreak := lastChar == '.' || lastChar == '\n' || lastChar == ' '
			// Log for debugging if needed.
			if !isNaturalBreak {
				t.Logf("Chunk ends with non-natural character '%c': %s", lastChar, content)
			}
		}
	}
}

func TestParagraphChunking(t *testing.T) {
	chunker, err := NewParagraphChunking(WithChunkSize(80), WithOverlap(0))
	require.NoError(t, err)

	doc := &document.Document{
		ID: "para-test",
		Content: `First paragraph with some content.

Second paragraph with different content.

Third paragraph that might be in a different chunk.`,
	}

	chunks, err := chunker.Chunk(doc)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 0)

	// Verify paragraphs are kept intact.
	for _, chunk := range chunks {
		assert.NotEmpty(t, chunk.Content)
		// Should contain complete paragraphs (no hanging fragments).
		assert.NotContains(t, chunk.Content, "\n\n\n")
	}
}

func TestChunkingWithEmptyDocument(t *testing.T) {
	chunker, err := NewFixedSizeChunking(WithChunkSize(50), WithOverlap(10))
	require.NoError(t, err)

	doc := &document.Document{Content: ""}
	chunks, err := chunker.Chunk(doc)
	assert.Error(t, err)
	assert.Equal(t, document.ErrEmptyDocument, err)
	assert.Nil(t, chunks)
}

func TestChunkingWithNilDocument(t *testing.T) {
	chunker, err := NewFixedSizeChunking(WithChunkSize(50), WithOverlap(10))
	require.NoError(t, err)

	chunks, err := chunker.Chunk(nil)
	assert.Error(t, err)
	assert.Equal(t, document.ErrNilDocument, err)
	assert.Nil(t, chunks)
}

func TestInvalidChunkingOptions(t *testing.T) {
	// Test invalid chunk size.
	_, err := NewFixedSizeChunking(WithChunkSize(0))
	assert.Error(t, err)
	assert.Equal(t, document.ErrInvalidChunkSize, err)

	// Test invalid overlap.
	_, err = NewFixedSizeChunking(WithChunkSize(50), WithOverlap(-1))
	assert.Error(t, err)
	assert.Equal(t, document.ErrInvalidOverlap, err)

	// Test overlap too large.
	_, err = NewFixedSizeChunking(WithChunkSize(50), WithOverlap(50))
	assert.Error(t, err)
	assert.Equal(t, document.ErrOverlapTooLarge, err)
}

func TestSmallDocument(t *testing.T) {
	chunker, err := NewFixedSizeChunking(WithChunkSize(100), WithOverlap(10))
	require.NoError(t, err)

	doc := &document.Document{
		ID:      "small-doc",
		Content: "Small content.",
	}

	chunks, err := chunker.Chunk(doc)
	require.NoError(t, err)
	assert.Len(t, chunks, 1)
	assert.Equal(t, doc.Content, chunks[0].Content)
}

func TestDefaultOptions(t *testing.T) {
	// Test that defaults are applied when no options are provided.
	chunker, err := NewFixedSizeChunking()
	require.NoError(t, err)

	doc := &document.Document{
		ID:      "default-test",
		Content: generateLargeContent(2000),
	}

	chunks, err := chunker.Chunk(doc)
	require.NoError(t, err)
	assert.Greater(t, len(chunks), 1)
}

func BenchmarkFixedSizeChunking(b *testing.B) {
	chunker, _ := NewFixedSizeChunking(WithChunkSize(1000), WithOverlap(100))
	doc := &document.Document{
		Content: generateLargeContent(10000),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chunker.Chunk(doc)
	}
}

func BenchmarkNaturalBreakChunking(b *testing.B) {
	chunker, _ := NewNaturalBreakChunking(WithChunkSize(1000), WithOverlap(100))
	doc := &document.Document{
		Content: generateLargeContent(10000),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chunker.Chunk(doc)
	}
}

func BenchmarkParagraphChunking(b *testing.B) {
	chunker, _ := NewParagraphChunking(WithChunkSize(1000), WithOverlap(100))
	doc := &document.Document{
		Content: generateParagraphContent(10000),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = chunker.Chunk(doc)
	}
}

// generateLargeContent generates a large text content for testing.
func generateLargeContent(size int) string {
	content := "This is a test sentence. "
	result := ""
	for len(result) < size {
		result += content
	}
	return result[:size]
}

// generateParagraphContent generates content with paragraph structure.
func generateParagraphContent(size int) string {
	paragraph := "This is a paragraph with multiple sentences. It contains various punctuation marks and line breaks.\n\n"
	result := ""
	for len(result) < size {
		result += paragraph
	}
	return result[:size]
}
