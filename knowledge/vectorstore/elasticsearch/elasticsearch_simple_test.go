//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/elasticsearch"
)

// TestNew tests the New function using SetClientBuilder for dependency injection
func TestNew(t *testing.T) {
	tests := []struct {
		name          string
		opts          []Option
		setupMock     func() *mockClient
		builderError  error
		wantErr       bool
		errMsg        string
		validateStore func(*testing.T, *VectorStore)
	}{
		{
			name: "success_with_existing_index",
			opts: []Option{
				WithIndexName("test_index"),
				WithVectorDimension(768),
			},
			setupMock: func() *mockClient {
				mc := newMockClient()
				mc.indexExists = true // Index already exists
				return mc
			},
			wantErr: false,
			validateStore: func(t *testing.T, vs *VectorStore) {
				assert.NotNil(t, vs)
				assert.Equal(t, "test_index", vs.option.indexName)
				assert.Equal(t, 768, vs.option.vectorDimension)
				assert.NotNil(t, vs.client)
				assert.NotNil(t, vs.filterConverter)
			},
		},
		{
			name: "success_with_default_options",
			opts: []Option{},
			setupMock: func() *mockClient {
				mc := newMockClient()
				mc.indexExists = true
				return mc
			},
			wantErr: false,
			validateStore: func(t *testing.T, vs *VectorStore) {
				assert.NotNil(t, vs)
				assert.Equal(t, defaultIndexName, vs.option.indexName)
				assert.Equal(t, defaultVectorDimension, vs.option.vectorDimension)
			},
		},
		{
			name: "success_create_index_if_not_exists",
			opts: []Option{
				WithIndexName("new_index"),
				WithVectorDimension(512),
			},
			setupMock: func() *mockClient {
				mc := newMockClient()
				mc.indexExists = false // Index doesn't exist, will be created
				return mc
			},
			wantErr: false,
			validateStore: func(t *testing.T, vs *VectorStore) {
				assert.NotNil(t, vs)
				assert.Equal(t, "new_index", vs.option.indexName)
				assert.Equal(t, 512, vs.option.vectorDimension)
				// Verify index was created
				mc := vs.client.(*mockClient)
				assert.True(t, mc.indexExists)
			},
		},
		{
			name: "error_client_builder_fails",
			opts: []Option{
				WithIndexName("test_index"),
			},
			builderError: assert.AnError,
			wantErr:      true,
			errMsg:       "elasticsearch create client",
		},
		{
			name: "error_ensure_index_fails",
			opts: []Option{
				WithIndexName("test_index"),
			},
			setupMock: func() *mockClient {
				mc := newMockClient()
				mc.indexExists = false
				mc.createIndexError = assert.AnError
				return mc
			},
			wantErr: true,
			errMsg:  "elasticsearch ensure index",
		},
		{
			name: "success_with_custom_options",
			opts: []Option{
				WithIndexName("custom_index"),
				WithVectorDimension(384),
				WithMaxResults(50),
				WithScoreThreshold(0.75),
				WithAddresses([]string{"http://es1:9200", "http://es2:9200"}),
				WithUsername("testuser"),
				WithPassword("testpass"),
			},
			setupMock: func() *mockClient {
				mc := newMockClient()
				mc.indexExists = true
				return mc
			},
			wantErr: false,
			validateStore: func(t *testing.T, vs *VectorStore) {
				assert.Equal(t, "custom_index", vs.option.indexName)
				assert.Equal(t, 384, vs.option.vectorDimension)
				assert.Equal(t, 50, vs.option.maxResults)
				assert.Equal(t, 0.75, vs.option.scoreThreshold)
				assert.Equal(t, []string{"http://es1:9200", "http://es2:9200"}, vs.option.addresses)
				assert.Equal(t, "testuser", vs.option.username)
				assert.Equal(t, "testpass", vs.option.password)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original builder
			originalBuilder := storage.GetClientBuilder()
			defer storage.SetClientBuilder(originalBuilder)

			// Setup mock builder
			if tt.builderError != nil {
				// Builder returns error
				storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (any, error) {
					return nil, tt.builderError
				})
			} else if tt.setupMock != nil {
				// Builder returns mock client
				mc := tt.setupMock()
				storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (any, error) {
					return mc, nil
				})
			}

			// Call New
			vs, err := New(tt.opts...)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, vs)
				if tt.validateStore != nil {
					tt.validateStore(t, vs)
				}
			}
		})
	}
}

// TestVectorStore_Add tests Add method
func TestVectorStore_Add(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		embedding []float64
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_add_document",
			doc: &document.Document{
				ID:        "doc1",
				Name:      "Test Document",
				Content:   "Test content",
				Metadata:  map[string]any{"type": "test"},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   true,
			errMsg:    "document cannot be nil",
		},
		{
			name: "empty_embedding",
			doc: &document.Document{
				ID:        "doc2",
				Name:      "Test",
				Content:   "Content",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			embedding: []float64{},
			wantErr:   true,
			errMsg:    "embedding vector cannot be empty",
		},
		{
			name: "wrong_dimension",
			doc: &document.Document{
				ID:        "doc3",
				Name:      "Test",
				Content:   "Content",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			embedding: []float64{0.1, 0.2}, // Only 2 dimensions, expected 3
			wantErr:   true,
			errMsg:    "dimension",
		},
		{
			name: "unicode_content",
			doc: &document.Document{
				ID:        "doc_unicode",
				Name:      "测试文档",
				Content:   "测试内容 тест содержание test content 🚀",
				Metadata:  map[string]any{"lang": "multi"},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			embedding: []float64{0.1, 0.2, 0.3},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true // Skip index creation
			vs := newTestVectorStore(t, mc)

			err := vs.Add(context.Background(), tt.doc, tt.embedding)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				// Verify document was stored
				if tt.doc != nil {
					assert.Equal(t, 1, mc.GetDocCount())
				}
			}
		})
	}
}

// TestVectorStore_Get tests Get method
func TestVectorStore_Get(t *testing.T) {
	tests := []struct {
		name      string
		docID     string
		setupDocs func(*mockClient)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *document.Document, []float64)
	}{
		{
			name:  "success_get_existing_document",
			docID: "doc1",
			setupDocs: func(mc *mockClient) {
				mc.docs["doc1"] = map[string]any{
					"id":         "doc1",
					"name":       "Test Doc",
					"content":    "Test content",
					"metadata":   map[string]any{"key": "value"},
					"created_at": "2024-01-01T00:00:00Z",
					"updated_at": "2024-01-01T00:00:00Z",
					"embedding":  []any{0.1, 0.2, 0.3},
				}
			},
			wantErr: false,
			validate: func(t *testing.T, doc *document.Document, emb []float64) {
				assert.Equal(t, "doc1", doc.ID)
				assert.Equal(t, "Test Doc", doc.Name)
				assert.Equal(t, 3, len(emb))
			},
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupDocs: func(mc *mockClient) {},
			wantErr:   true,
			errMsg:    "ID cannot be empty",
		},
		{
			name:      "document_not_found",
			docID:     "nonexistent",
			setupDocs: func(mc *mockClient) {},
			wantErr:   true,
		},
		{
			name:  "missing_embedding",
			docID: "doc_no_emb",
			setupDocs: func(mc *mockClient) {
				mc.docs["doc_no_emb"] = map[string]any{
					"id":         "doc_no_emb",
					"name":       "No Embedding",
					"content":    "Content",
					"created_at": "2024-01-01T00:00:00Z",
					"updated_at": "2024-01-01T00:00:00Z",
					// embedding missing
				}
			},
			wantErr: true,
			errMsg:  "embedding vector not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			tt.setupDocs(mc)
			vs := newTestVectorStore(t, mc)

			doc, emb, err := vs.Get(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, doc)
				assert.NotNil(t, emb)
				if tt.validate != nil {
					tt.validate(t, doc, emb)
				}
			}
		})
	}
}

// TestVectorStore_Update tests Update method
func TestVectorStore_Update(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		embedding []float64
		setupDocs func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_update_existing_document",
			doc: &document.Document{
				ID:        "doc1",
				Name:      "Updated Name",
				Content:   "Updated content",
				Metadata:  map[string]any{"updated": true},
				UpdatedAt: time.Now(),
			},
			embedding: []float64{0.9, 0.8, 0.7},
			setupDocs: func(mc *mockClient) {
				mc.docs["doc1"] = map[string]any{
					"id":      "doc1",
					"name":    "Original",
					"content": "Original",
				}
			},
			wantErr: false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			embedding: []float64{0.1, 0.2, 0.3},
			setupDocs: func(mc *mockClient) {},
			wantErr:   true,
			errMsg:    "document cannot be nil",
		},
		{
			name: "empty_embedding",
			doc: &document.Document{
				ID:        "doc2",
				Name:      "Test",
				UpdatedAt: time.Now(),
			},
			embedding: []float64{},
			setupDocs: func(mc *mockClient) {},
			wantErr:   true,
			errMsg:    "embedding vector cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			tt.setupDocs(mc)
			vs := newTestVectorStore(t, mc)

			err := vs.Update(context.Background(), tt.doc, tt.embedding)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestVectorStore_Delete tests Delete method
func TestVectorStore_Delete(t *testing.T) {
	tests := []struct {
		name      string
		docID     string
		setupDocs func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name:  "success_delete_existing_document",
			docID: "doc1",
			setupDocs: func(mc *mockClient) {
				mc.docs["doc1"] = map[string]any{"id": "doc1"}
			},
			wantErr: false,
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupDocs: func(mc *mockClient) {},
			wantErr:   true,
			errMsg:    "ID cannot be empty",
		},
		{
			name:      "delete_nonexistent_document",
			docID:     "nonexistent",
			setupDocs: func(mc *mockClient) {},
			wantErr:   false, // ES delete is idempotent
		},
		{
			name:  "client_error",
			docID: "doc2",
			setupDocs: func(mc *mockClient) {
				mc.SetDeleteDocError(errors.New("connection lost"))
			},
			wantErr: true,
			errMsg:  "connection lost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			tt.setupDocs(mc)
			vs := newTestVectorStore(t, mc)

			initialCount := mc.GetDocCount()
			err := vs.Delete(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				// Verify document was deleted
				if initialCount > 0 {
					assert.Equal(t, initialCount-1, mc.GetDocCount())
				}
			}
		})
	}
}

// TestVectorStore_Count tests Count method
func TestVectorStore_Count(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*mockClient)
		opts      []vectorstore.CountOption
		wantErr   bool
		wantCount int
	}{
		{
			name: "success_count_all",
			setupMock: func(mc *mockClient) {
				mc.SetCountResult(42)
			},
			opts:      nil,
			wantErr:   false,
			wantCount: 42,
		},
		{
			name: "count_with_filter",
			setupMock: func(mc *mockClient) {
				mc.SetCountResult(10)
			},
			opts: []vectorstore.CountOption{
				vectorstore.WithCountFilter(map[string]any{"type": "test"}),
			},
			wantErr:   false,
			wantCount: 10,
		},
		{
			name: "client_error",
			setupMock: func(mc *mockClient) {
				mc.SetCountError(errors.New("count failed"))
			},
			opts:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			tt.setupMock(mc)
			vs := newTestVectorStore(t, mc)

			count, err := vs.Count(context.Background(), tt.opts...)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
		})
	}
}

// TestVectorStore_DeleteByFilter tests DeleteByFilter method
func TestVectorStore_DeleteByFilter(t *testing.T) {
	tests := []struct {
		name    string
		opts    []vectorstore.DeleteOption
		wantErr bool
		errMsg  string
	}{
		{
			name: "delete_by_ids",
			opts: []vectorstore.DeleteOption{
				vectorstore.WithDeleteDocumentIDs([]string{"doc1", "doc2"}),
			},
			wantErr: false,
		},
		{
			name: "delete_by_filter",
			opts: []vectorstore.DeleteOption{
				vectorstore.WithDeleteFilter(map[string]any{"type": "test"}),
			},
			wantErr: false,
		},
		{
			name: "delete_all",
			opts: []vectorstore.DeleteOption{
				vectorstore.WithDeleteAll(true),
			},
			wantErr: false,
		},
		{
			name:    "no_filter",
			opts:    []vectorstore.DeleteOption{},
			wantErr: true,
			errMsg:  "no filter conditions",
		},
		{
			name: "delete_all_with_filter_conflict",
			opts: []vectorstore.DeleteOption{
				vectorstore.WithDeleteAll(true),
				vectorstore.WithDeleteFilter(map[string]any{"type": "test"}),
			},
			wantErr: true,
			errMsg:  "delete all documents",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := newMockClient()
			mc.indexExists = true
			vs := newTestVectorStore(t, mc)

			err := vs.DeleteByFilter(context.Background(), tt.opts...)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestVectorStore_ConcurrentOperations tests concurrent access
func TestVectorStore_ConcurrentOperations(t *testing.T) {
	mc := newMockClient()
	mc.indexExists = true
	vs := newTestVectorStore(t, mc)

	ctx := context.Background()
	numGoroutines := 10

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			doc := &document.Document{
				ID:        fmt.Sprintf("doc_%d", idx),
				Name:      fmt.Sprintf("Doc %d", idx),
				Content:   "Content",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}
			embedding := []float64{float64(idx) / 10.0, 0.5, 0.2}
			if err := vs.Add(ctx, doc, embedding); err != nil {
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent operation failed: %v", err)
	}

	// Verify documents were stored
	assert.Equal(t, numGoroutines, mc.GetDocCount())

	// Verify each document exists
	for i := 0; i < numGoroutines; i++ {
		docID := fmt.Sprintf("doc_%d", i)
		doc, emb, err := vs.Get(ctx, docID)
		require.NoError(t, err)
		assert.Equal(t, docID, doc.ID)
		assert.Equal(t, 3, len(emb))
	}
}
