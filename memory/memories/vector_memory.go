// Package memories provides specialized implementations of memory systems.
package memories

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/message"
)

// ErrEmbeddingProviderNotSet is returned when an embedding provider is required but not set.
var ErrEmbeddingProviderNotSet = errors.New("embedding provider not set")

// EmbeddingProvider is the interface for services that can embed text into vectors.
type EmbeddingProvider interface {
	// Embed converts text to a vector embedding.
	Embed(ctx context.Context, text string) ([]float32, error)

	// BatchEmbed converts multiple texts to vector embeddings.
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the dimensionality of the embedding vectors.
	Dimensions() int
}

// VectorEntry represents a message with its embedding vector.
type VectorEntry struct {
	Message   *message.Message
	Embedding []float32
}

// VectorMemory extends the base memory with vector embeddings for semantic search.
type VectorMemory struct {
	memory.BaseMemory
	provider   EmbeddingProvider
	entries    []VectorEntry
	mutex      sync.RWMutex
	autoEmbed  bool
	dimensions int
}

// VectorMemoryOptions holds configuration options for VectorMemory.
type VectorMemoryOptions struct {
	// AutoEmbed determines if messages should be automatically embedded when stored.
	AutoEmbed bool
}

// NewVectorMemory creates a new VectorMemory with the given embedding provider.
func NewVectorMemory(provider EmbeddingProvider, options *VectorMemoryOptions) *VectorMemory {
	if options == nil {
		options = &VectorMemoryOptions{
			AutoEmbed: true,
		}
	}

	var dimensions int
	if provider != nil {
		dimensions = provider.Dimensions()
	}

	return &VectorMemory{
		BaseMemory: *memory.NewBaseMemory(),
		provider:   provider,
		entries:    make([]VectorEntry, 0),
		autoEmbed:  options.AutoEmbed,
		dimensions: dimensions,
	}
}

// Store adds a message to the memory, optionally computing its embedding.
func (m *VectorMemory) Store(ctx context.Context, msg *message.Message) error {
	if msg == nil {
		return nil
	}

	// First store in the base memory
	if err := m.BaseMemory.Store(ctx, msg); err != nil {
		return err
	}

	// If auto-embed is enabled and provider exists, compute and store the embedding
	if m.autoEmbed && m.provider != nil && msg.Content != "" {
		embedding, err := m.provider.Embed(ctx, msg.Content)
		if err != nil {
			return err
		}

		m.mutex.Lock()
		defer m.mutex.Unlock()

		m.entries = append(m.entries, VectorEntry{
			Message:   msg,
			Embedding: embedding,
		})
	} else if m.autoEmbed && m.provider == nil {
		// If auto-embed is on but no provider, store without embedding
		m.mutex.Lock()
		defer m.mutex.Unlock()

		m.entries = append(m.entries, VectorEntry{
			Message:   msg,
			Embedding: nil,
		})
	}

	return nil
}

// Clear empties the memory.
func (m *VectorMemory) Clear(ctx context.Context) error {
	if err := m.BaseMemory.Clear(ctx); err != nil {
		return err
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.entries = make([]VectorEntry, 0)
	return nil
}

// SearchSimilar finds messages that are semantically similar to the query.
func (m *VectorMemory) SearchSimilar(ctx context.Context, query string, topK int) ([]*message.Message, error) {
	if m.provider == nil {
		return nil, ErrEmbeddingProviderNotSet
	}

	// Compute embedding for the query
	queryEmbedding, err := m.provider.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Create a list of entries with similarity scores
	type scoredEntry struct {
		Message    *message.Message
		Similarity float32
	}

	var scoredEntries []scoredEntry
	for _, entry := range m.entries {
		if entry.Embedding != nil {
			similarity := cosineSimilarity(queryEmbedding, entry.Embedding)
			scoredEntries = append(scoredEntries, scoredEntry{
				Message:    entry.Message,
				Similarity: similarity,
			})
		}
	}

	// Sort by similarity (highest first)
	sort.Slice(scoredEntries, func(i, j int) bool {
		return scoredEntries[i].Similarity > scoredEntries[j].Similarity
	})

	// Limit results to topK
	if topK <= 0 || topK > len(scoredEntries) {
		topK = len(scoredEntries)
	}
	scoredEntries = scoredEntries[:topK]

	// Extract messages
	results := make([]*message.Message, len(scoredEntries))
	for i, entry := range scoredEntries {
		results[i] = entry.Message
	}

	return results, nil
}

// Search overrides the BaseMemory search with a semantic search if a provider is available.
// If no provider is set, it falls back to the base implementation.
func (m *VectorMemory) Search(ctx context.Context, query string) ([]*message.Message, error) {
	if m.provider != nil && query != "" {
		return m.SearchSimilar(ctx, query, 10) // Default to top 10 results
	}
	return m.BaseMemory.Search(ctx, query)
}

// SetEmbeddingProvider sets or changes the embedding provider.
// This allows initializing a memory without a provider and adding one later.
func (m *VectorMemory) SetEmbeddingProvider(provider EmbeddingProvider) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.provider = provider
	if provider != nil {
		m.dimensions = provider.Dimensions()
	}
}

// UpdateEmbeddings recomputes all message embeddings using the current provider.
// This is useful after changing the embedding provider.
func (m *VectorMemory) UpdateEmbeddings(ctx context.Context) error {
	if m.provider == nil {
		return ErrEmbeddingProviderNotSet
	}

	messages, err := m.Retrieve(ctx)
	if err != nil {
		return err
	}

	// Extract text from messages
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Content != "" {
			texts = append(texts, msg.Content)
		}
	}

	// Batch compute embeddings
	embeddings, err := m.provider.BatchEmbed(ctx, texts)
	if err != nil {
		return err
	}

	// Update entries
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.entries = make([]VectorEntry, 0, len(messages))
	embeddingIndex := 0

	for _, msg := range messages {
		if msg.Content != "" {
			m.entries = append(m.entries, VectorEntry{
				Message:   msg,
				Embedding: embeddings[embeddingIndex],
			})
			embeddingIndex++
		} else {
			// For messages without content, don't assign an embedding
			m.entries = append(m.entries, VectorEntry{
				Message:   msg,
				Embedding: nil,
			})
		}
	}

	return nil
}

// cosineSimilarity computes the cosine similarity between two vectors.
// Returns a value between -1 and 1, where 1 means identical vectors.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}
