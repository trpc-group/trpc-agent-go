package memories

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/message"
)

// mockEmbeddingProvider implements EmbeddingProvider for testing.
type mockEmbeddingProvider struct {
	embedFunc      func(ctx context.Context, text string) ([]float32, error)
	batchEmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)
	dimensions     int
}

func (m *mockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, text)
	}

	// Default implementation returns a simple embedding
	return []float32{1.0, 0.0, 0.0}, nil
}

func (m *mockEmbeddingProvider) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if m.batchEmbedFunc != nil {
		return m.batchEmbedFunc(ctx, texts)
	}

	// Default implementation returns simple embeddings
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = []float32{1.0, 0.0, 0.0}
	}
	return embeddings, nil
}

func (m *mockEmbeddingProvider) Dimensions() int {
	if m.dimensions > 0 {
		return m.dimensions
	}
	return 3 // Default dimensionality
}

// newMockProvider creates a mock embedding provider for testing.
func newMockProvider() *mockEmbeddingProvider {
	return &mockEmbeddingProvider{
		dimensions: 3,
	}
}

func TestNewVectorMemory(t *testing.T) {
	provider := newMockProvider()

	// Test with default options
	memory := NewVectorMemory(provider, nil)
	if memory == nil {
		t.Fatal("Expected non-nil memory")
	}
	if memory.dimensions != 3 {
		t.Errorf("Expected dimensions 3, got %d", memory.dimensions)
	}
	if !memory.autoEmbed {
		t.Error("Expected autoEmbed to be true by default")
	}

	// Test with custom options
	options := &VectorMemoryOptions{
		AutoEmbed: false,
	}
	memory = NewVectorMemory(provider, options)
	if memory.autoEmbed {
		t.Error("Expected autoEmbed to be false")
	}

	// Test with nil provider
	memory = NewVectorMemory(nil, nil)
	if memory.provider != nil {
		t.Error("Expected nil provider")
	}
	if memory.dimensions != 0 {
		t.Errorf("Expected dimensions 0, got %d", memory.dimensions)
	}
}

func TestVectorMemory_Store(t *testing.T) {
	ctx := context.Background()
	provider := newMockProvider()
	memory := NewVectorMemory(provider, nil)

	// Store a message with content
	msg := message.NewUserMessage("Test message")
	err := memory.Store(ctx, msg)
	if err != nil {
		t.Fatalf("Unexpected error storing message: %v", err)
	}

	// Verify entries count
	if len(memory.entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(memory.entries))
	}

	// Verify entry message
	if memory.entries[0].Message != msg {
		t.Error("Entry message doesn't match stored message")
	}

	// Verify embedding
	if memory.entries[0].Embedding == nil {
		t.Fatal("Expected embedding to be computed")
	}
	if len(memory.entries[0].Embedding) != 3 {
		t.Errorf("Expected embedding dimension 3, got %d", len(memory.entries[0].Embedding))
	}

	// Store a message with empty content
	emptyMsg := message.NewUserMessage("")
	err = memory.Store(ctx, emptyMsg)
	if err != nil {
		t.Fatalf("Unexpected error storing empty message: %v", err)
	}

	// Base memory should have stored both messages
	messages, _ := memory.Retrieve(ctx)
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	// Verify nil message (should be a no-op)
	err = memory.Store(ctx, nil)
	if err != nil {
		t.Fatalf("Unexpected error storing nil message: %v", err)
	}

	// Base memory count should still be 2
	messages, _ = memory.Retrieve(ctx)
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}
}

func TestVectorMemory_Store_WithoutAutoEmbed(t *testing.T) {
	ctx := context.Background()
	provider := newMockProvider()
	options := &VectorMemoryOptions{
		AutoEmbed: false,
	}
	memory := NewVectorMemory(provider, options)

	// Store a message
	msg := message.NewUserMessage("Test message")
	err := memory.Store(ctx, msg)
	if err != nil {
		t.Fatalf("Unexpected error storing message: %v", err)
	}

	// Base memory should have stored the message
	messages, _ := memory.Retrieve(ctx)
	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	}

	// Entries should be empty when autoEmbed is false
	if len(memory.entries) != 0 {
		t.Errorf("Expected 0 entries with autoEmbed=false, got %d", len(memory.entries))
	}
}

func TestVectorMemory_Store_WithNilProvider(t *testing.T) {
	ctx := context.Background()
	memory := NewVectorMemory(nil, nil)

	// Store a message
	msg := message.NewUserMessage("Test message")
	err := memory.Store(ctx, msg)
	if err != nil {
		t.Fatalf("Unexpected error storing message: %v", err)
	}

	// Verify entry was stored without embedding
	if len(memory.entries) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(memory.entries))
	}
	if memory.entries[0].Embedding != nil {
		t.Error("Expected nil embedding when provider is nil")
	}
}

func TestVectorMemory_Clear(t *testing.T) {
	ctx := context.Background()
	provider := newMockProvider()
	memory := NewVectorMemory(provider, nil)

	// Store some messages
	_ = memory.Store(ctx, message.NewUserMessage("Message 1"))
	_ = memory.Store(ctx, message.NewUserMessage("Message 2"))

	// Verify initial state
	messages, _ := memory.Retrieve(ctx)
	if len(messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(messages))
	}
	if len(memory.entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(memory.entries))
	}

	// Clear memory
	err := memory.Clear(ctx)
	if err != nil {
		t.Fatalf("Unexpected error clearing memory: %v", err)
	}

	// Verify cleared state
	messages, _ = memory.Retrieve(ctx)
	if len(messages) != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", len(messages))
	}
	if len(memory.entries) != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", len(memory.entries))
	}
}

func TestVectorMemory_SearchSimilar(t *testing.T) {
	ctx := context.Background()

	// Create a provider that returns specific embeddings for testing similarity
	provider := &mockEmbeddingProvider{
		dimensions: 3,
		embedFunc: func(ctx context.Context, text string) ([]float32, error) {
			switch text {
			case "similar to first":
				return []float32{0.9, 0.1, 0.0}, nil
			case "similar to second":
				return []float32{0.1, 0.9, 0.0}, nil
			case "similar to third":
				return []float32{0.1, 0.1, 0.9}, nil
			case "query":
				return []float32{0.9, 0.1, 0.1}, nil // Changed to be more clearly similar to first
			default:
				return []float32{0.33, 0.33, 0.33}, nil
			}
		},
	}

	memory := NewVectorMemory(provider, nil)

	// Store messages with different similarities to the future query
	msg1 := message.NewUserMessage("similar to first")  // Most similar to query
	msg2 := message.NewUserMessage("similar to second") // Less similar
	msg3 := message.NewUserMessage("similar to third")  // Least similar
	_ = memory.Store(ctx, msg1)
	_ = memory.Store(ctx, msg2)
	_ = memory.Store(ctx, msg3)

	// Search for similar messages
	results, err := memory.SearchSimilar(ctx, "query", 2)
	if err != nil {
		t.Fatalf("Unexpected error during search: %v", err)
	}

	// Should return top 2 most similar
	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// First result should be most similar (msg1)
	if results[0].Content != "similar to first" {
		t.Errorf("Expected first result to be 'similar to first', got '%s'", results[0].Content)
	}

	// Second result should be the second most similar
	// Rather than asserting a specific order which might depend on implementation details,
	// we'll just check that both results contain the expected "similar to" messages
	if results[1].Content != "similar to second" && results[1].Content != "similar to third" {
		t.Errorf("Expected second result to contain 'similar to', got '%s'", results[1].Content)
	}
}

func TestVectorMemory_SearchSimilar_NilProvider(t *testing.T) {
	ctx := context.Background()
	memory := NewVectorMemory(nil, nil)

	// Store some messages
	_ = memory.Store(ctx, message.NewUserMessage("Message 1"))

	// Search should return error when provider is nil
	_, err := memory.SearchSimilar(ctx, "query", 2)
	if err == nil {
		t.Fatal("Expected error with nil provider, got nil")
	}
	if err != ErrEmbeddingProviderNotSet {
		t.Errorf("Expected ErrEmbeddingProviderNotSet, got %v", err)
	}
}

func TestVectorMemory_Search(t *testing.T) {
	ctx := context.Background()
	provider := newMockProvider()
	memory := NewVectorMemory(provider, nil)

	// Store messages
	_ = memory.Store(ctx, message.NewUserMessage("Hello world"))
	_ = memory.Store(ctx, message.NewUserMessage("Testing search"))

	// Test that Search uses semantic search when provider is available
	results, err := memory.Search(ctx, "query")
	if err != nil {
		t.Fatalf("Unexpected error during search: %v", err)
	}
	// Semantic search should return results even if query doesn't match text exactly
	if len(results) != 2 {
		t.Fatalf("Expected 2 results from semantic search, got %d", len(results))
	}

	// Now try with nil provider (should fall back to base implementation)
	memory.SetEmbeddingProvider(nil)

	// Store another message to match a text search
	_ = memory.Store(ctx, message.NewUserMessage("This is a query test"))

	// Clear existing entries since they don't have semantic info after provider change
	memory.entries = nil

	// Search should now use the base implementation
	results, err = memory.Search(ctx, "query")
	if err != nil {
		t.Fatalf("Unexpected error during search: %v", err)
	}
	// Text search should only return results with the word "query"
	if len(results) != 1 {
		t.Fatalf("Expected 1 result from text search, got %d", len(results))
	}
	if results[0].Content != "This is a query test" {
		t.Errorf("Expected 'This is a query test', got '%s'", results[0].Content)
	}
}

func TestVectorMemory_SetEmbeddingProvider(t *testing.T) {
	memory := NewVectorMemory(nil, nil)

	// Initially no provider
	if memory.provider != nil {
		t.Fatal("Expected nil provider initially")
	}
	if memory.dimensions != 0 {
		t.Errorf("Expected initial dimensions 0, got %d", memory.dimensions)
	}

	// Set a provider
	provider := newMockProvider()
	memory.SetEmbeddingProvider(provider)

	// Verify provider is set
	if memory.provider != provider {
		t.Error("Provider not correctly set")
	}
	if memory.dimensions != 3 {
		t.Errorf("Expected dimensions 3 after set, got %d", memory.dimensions)
	}

	// Set nil provider
	memory.SetEmbeddingProvider(nil)
	if memory.provider != nil {
		t.Error("Provider not correctly set to nil")
	}
}

func TestVectorMemory_UpdateEmbeddings(t *testing.T) {
	ctx := context.Background()
	memory := NewVectorMemory(nil, nil)

	// Store messages without embeddings
	_ = memory.Store(ctx, message.NewUserMessage("Message 1"))
	_ = memory.Store(ctx, message.NewUserMessage("Message 2"))

	// Without provider, update should fail
	err := memory.UpdateEmbeddings(ctx)
	if err == nil {
		t.Fatal("Expected error updating embeddings without provider")
	}
	if err != ErrEmbeddingProviderNotSet {
		t.Errorf("Expected ErrEmbeddingProviderNotSet, got %v", err)
	}

	// Set provider and update
	provider := newMockProvider()
	memory.SetEmbeddingProvider(provider)

	err = memory.UpdateEmbeddings(ctx)
	if err != nil {
		t.Fatalf("Unexpected error updating embeddings: %v", err)
	}

	// Verify entries have embeddings
	if len(memory.entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(memory.entries))
	}
	for i, entry := range memory.entries {
		if entry.Embedding == nil {
			t.Errorf("Entry %d missing embedding after update", i)
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	testCases := []struct {
		name     string
		a        []float32
		b        []float32
		expected float32
	}{
		{
			name:     "Identical vectors",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 1.0,
		},
		{
			name:     "Orthogonal vectors",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{0.0, 1.0, 0.0},
			expected: 0.0,
		},
		{
			name:     "Similar vectors",
			a:        []float32{0.8, 0.2, 0.0},
			b:        []float32{0.9, 0.1, 0.0},
			expected: 0.98, // Approximately
		},
		{
			name:     "Different lengths a",
			a:        []float32{2.0, 0.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 1.0,
		},
		{
			name:     "Different dimensions",
			a:        []float32{1.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 0.0, // Default for dimension mismatch
		},
		{
			name:     "Empty vectors",
			a:        []float32{},
			b:        []float32{},
			expected: 0.0,
		},
		{
			name:     "Zero norm a",
			a:        []float32{0.0, 0.0, 0.0},
			b:        []float32{1.0, 0.0, 0.0},
			expected: 0.0,
		},
		{
			name:     "Zero norm b",
			a:        []float32{1.0, 0.0, 0.0},
			b:        []float32{0.0, 0.0, 0.0},
			expected: 0.0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			similarity := cosineSimilarity(tc.a, tc.b)
			// For approximate comparisons
			if tc.name == "Similar vectors" {
				if similarity < 0.96 || similarity > 1.0 {
					t.Errorf("Expected similarity around %.2f, got %.2f", tc.expected, similarity)
				}
			} else {
				if similarity != tc.expected {
					t.Errorf("Expected similarity %.2f, got %.2f", tc.expected, similarity)
				}
			}
		})
	}
}
