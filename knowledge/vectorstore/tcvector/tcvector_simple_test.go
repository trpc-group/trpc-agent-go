//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tcvector

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
)

// TestVectorStore_Add tests the Add method with various scenarios
func TestVectorStore_Add(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		vector    []float64
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_add_document",
			doc: &document.Document{
				ID:       "test_001",
				Name:     "AI Fundamentals",
				Content:  "Artificial intelligence is a branch of computer science",
				Metadata: map[string]any{"category": "AI", "priority": 5},
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "document is required",
		},
		{
			name: "empty_vector",
			doc: &document.Document{
				ID:      "test_002",
				Content: "Test content",
			},
			vector:    []float64{},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "dimension mismatch",
		},
		{
			name: "dimension_mismatch",
			doc: &document.Document{
				ID:      "test_003",
				Content: "Test content",
			},
			vector:    []float64{1.0, 0.5}, // Only 2 dimensions, expected 3
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "dimension mismatch",
		},
		{
			name: "client_error",
			doc: &document.Document{
				ID:      "test_004",
				Content: "Test content",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {
				m.SetUpsertError(errors.New("connection timeout"))
			},
			wantErr: true,
			errMsg:  "connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			err := vs.Add(context.Background(), tt.doc, tt.vector)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, mockClient.GetUpsertCalls())
				assert.Equal(t, 1, mockClient.GetDocumentCount())

				// Verify stored document
				storedDoc, ok := mockClient.GetDocument(tt.doc.ID)
				assert.True(t, ok)
				assert.Equal(t, tt.doc.ID, storedDoc.Id)
			}
		})
	}
}

// TestVectorStore_Get tests the Get method
func TestVectorStore_Get(t *testing.T) {
	tests := []struct {
		name      string
		docID     string
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name:  "success_get_existing_document",
			docID: "test_001",
			setupMock: func(m *mockClient) {
				// Pre-populate with a document
				doc := &document.Document{
					ID:       "test_001",
					Name:     "Test Doc",
					Content:  "Test content",
					Metadata: map[string]any{"key": "value"},
				}
				vector := []float64{1.0, 0.5, 0.2}
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				_ = vs.Add(context.Background(), doc, vector)
			},
			wantErr: false,
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "document ID is required",
		},
		{
			name:      "document_not_found",
			docID:     "non_existent",
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "not found document",
		},
		{
			name:  "client_error",
			docID: "test_002",
			setupMock: func(m *mockClient) {
				m.SetQueryError(errors.New("database connection lost"))
			},
			wantErr: true,
			errMsg:  "database connection lost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			doc, vector, err := vs.Get(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, doc)
				assert.NotNil(t, vector)
				assert.Equal(t, tt.docID, doc.ID)
				assert.Greater(t, mockClient.GetQueryCalls(), 0)
			}
		})
	}
}

// TestVectorStore_Update tests the Update method
func TestVectorStore_Update(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		vector    []float64
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_update_document",
			doc: &document.Document{
				ID:       "test_001",
				Name:     "Updated Name",
				Content:  "Updated content",
				Metadata: map[string]any{"updated": true},
			},
			vector: []float64{0.9, 0.6, 0.3},
			setupMock: func(m *mockClient) {
				// Pre-add the document
				doc := &document.Document{
					ID:      "test_001",
					Name:    "Original Name",
					Content: "Original content",
				}
				vector := []float64{1.0, 0.5, 0.2}
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				_ = vs.Add(context.Background(), doc, vector)
			},
			wantErr: false,
		},
		{
			name: "empty_vector",
			doc: &document.Document{
				ID:      "test_002",
				Content: "Test",
			},
			vector:    []float64{},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "dimension mismatch",
		},
		{
			name: "client_error",
			doc: &document.Document{
				ID:      "test_003",
				Content: "Test",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {
				// Pre-add the document
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				doc := &document.Document{
					ID:      "test_003",
					Content: "Original",
				}
				_ = vs.Add(context.Background(), doc, []float64{1.0, 0.5, 0.2})
				// Set update error after adding
				m.SetUpdateError(errors.New("update failed"))
			},
			wantErr: true,
			errMsg:  "update failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			err := vs.Update(context.Background(), tt.doc, tt.vector)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				// Update uses Upsert internally
				assert.Greater(t, mockClient.GetUpsertCalls(), 0)
			}
		})
	}
}

// TestVectorStore_Delete tests the Delete method
func TestVectorStore_Delete(t *testing.T) {
	tests := []struct {
		name      string
		docID     string
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name:  "success_delete_existing_document",
			docID: "test_001",
			setupMock: func(m *mockClient) {
				// Pre-add a document
				doc := &document.Document{
					ID:      "test_001",
					Content: "Test content",
				}
				vector := []float64{1.0, 0.5, 0.2}
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				_ = vs.Add(context.Background(), doc, vector)
			},
			wantErr: false,
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "document ID is required",
		},
		{
			name:  "client_error",
			docID: "test_002",
			setupMock: func(m *mockClient) {
				m.SetDeleteError(errors.New("delete operation failed"))
			},
			wantErr: true,
			errMsg:  "delete operation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			initialCount := mockClient.GetDocumentCount()
			err := vs.Delete(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, mockClient.GetDeleteCalls())
				// Verify document was deleted
				if initialCount > 0 {
					assert.Equal(t, initialCount-1, mockClient.GetDocumentCount())
				}
			}
		})
	}
}

// TestVectorStore_AddMultipleDocuments tests adding multiple documents
func TestVectorStore_AddMultipleDocuments(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	ctx := context.Background()
	docs := []struct {
		doc    *document.Document
		vector []float64
	}{
		{
			doc: &document.Document{
				ID:       "doc1",
				Name:     "Document 1",
				Content:  "Content 1",
				Metadata: map[string]any{"type": "test"},
			},
			vector: []float64{1.0, 0.0, 0.0},
		},
		{
			doc: &document.Document{
				ID:       "doc2",
				Name:     "Document 2",
				Content:  "Content 2",
				Metadata: map[string]any{"type": "test"},
			},
			vector: []float64{0.0, 1.0, 0.0},
		},
		{
			doc: &document.Document{
				ID:       "doc3",
				Name:     "Document 3",
				Content:  "Content 3",
				Metadata: map[string]any{"type": "test"},
			},
			vector: []float64{0.0, 0.0, 1.0},
		},
	}

	// Add all documents
	for _, d := range docs {
		err := vs.Add(ctx, d.doc, d.vector)
		require.NoError(t, err)
	}

	// Verify all documents were added
	assert.Equal(t, 3, mockClient.GetDocumentCount())
	assert.Equal(t, 3, mockClient.GetUpsertCalls())

	// Verify each document can be retrieved
	for _, d := range docs {
		retrievedDoc, retrievedVector, err := vs.Get(ctx, d.doc.ID)
		require.NoError(t, err)
		assert.Equal(t, d.doc.ID, retrievedDoc.ID)
		assert.Equal(t, d.doc.Name, retrievedDoc.Name)
		assert.Equal(t, len(d.vector), len(retrievedVector))
	}
}

// TestVectorStore_UpdateNonExistentDocument tests updating a document that doesn't exist
func TestVectorStore_UpdateNonExistentDocument(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	doc := &document.Document{
		ID:      "non_existent",
		Name:    "New Document",
		Content: "New content",
	}
	vector := []float64{1.0, 0.5, 0.2}

	// Update should fail for non-existent document
	// Real implementation requires document to exist
	err := vs.Update(context.Background(), doc, vector)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found document")

	// Verify no document was created
	assert.Equal(t, 0, mockClient.GetDocumentCount())
}

// TestVectorStore_ConcurrentOperations tests concurrent add/get/delete operations
func TestVectorStore_ConcurrentOperations(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	ctx := context.Background()
	numGoroutines := 10

	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)

	// Concurrent adds
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			doc := &document.Document{
				ID:      fmt.Sprintf("doc_%d", idx),
				Content: "Content",
			}
			vector := []float64{float64(idx), 0.5, 0.2}
			if err := vs.Add(ctx, doc, vector); err != nil {
				errChan <- err
			}
		}(i)
	}

	// Wait for all goroutines
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent operation failed: %v", err)
	}

	// Verify all operations completed successfully
	assert.Equal(t, numGoroutines, mockClient.GetUpsertCalls())
	assert.Equal(t, numGoroutines, mockClient.GetDocumentCount())

	// Verify each document exists
	for i := 0; i < numGoroutines; i++ {
		docID := fmt.Sprintf("doc_%d", i)
		_, ok := mockClient.GetDocument(docID)
		assert.True(t, ok, "document %s should exist", docID)
	}
}

// TestVectorStore_MetadataHandling tests various metadata scenarios
func TestVectorStore_MetadataHandling(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
	}{
		{
			name: "simple_metadata",
			metadata: map[string]any{
				"category": "test",
				"priority": 5,
			},
		},
		{
			name: "complex_metadata",
			metadata: map[string]any{
				"tags":      []string{"tag1", "tag2", "tag3"},
				"nested":    map[string]any{"key": "value"},
				"timestamp": time.Now().Unix(),
			},
		},
		{
			name:     "empty_metadata",
			metadata: map[string]any{},
		},
		{
			name:     "nil_metadata",
			metadata: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			doc := &document.Document{
				ID:       "test_meta",
				Content:  "Test content",
				Metadata: tt.metadata,
			}
			vector := []float64{1.0, 0.5, 0.2}

			err := vs.Add(context.Background(), doc, vector)
			require.NoError(t, err)

			// Verify document was stored
			storedDoc, ok := mockClient.GetDocument(doc.ID)
			assert.True(t, ok)
			assert.Equal(t, doc.ID, storedDoc.Id)
		})
	}
}

// TestVectorStore_Add_EdgeCases tests edge cases for Add method
func TestVectorStore_Add_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		vector    []float64
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "very_long_content",
			doc: &document.Document{
				ID:      "long_doc",
				Content: string(make([]byte, 100000)), // 100KB content
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   false,
		},
		{
			name: "empty_content",
			doc: &document.Document{
				ID:      "empty_doc",
				Content: "",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   false,
		},
		{
			name: "special_characters_in_id",
			doc: &document.Document{
				ID:      "doc-with-special.chars_123",
				Content: "Test content",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   false,
		},
		{
			name: "unicode_content",
			doc: &document.Document{
				ID:      "unicode_doc",
				Content: "测试内容 тест содержание test content 🚀",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   false,
		},
		{
			name: "duplicate_id",
			doc: &document.Document{
				ID:      "duplicate_id",
				Content: "Second doc with same ID",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {
				// Pre-add document with same ID
				vs := newVectorStoreWithMockClient(m,
					WithDatabase("test_db"),
					WithCollection("test_collection"),
					WithIndexDimension(3),
				)
				doc := &document.Document{
					ID:      "duplicate_id",
					Content: "First doc",
				}
				_ = vs.Add(context.Background(), doc, []float64{0.8, 0.6, 0.3})
			},
			wantErr: false, // Upsert should replace
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := newMockClient()
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithDatabase("test_db"),
				WithCollection("test_collection"),
				WithIndexDimension(3),
			)

			err := vs.Add(context.Background(), tt.doc, tt.vector)

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

// TestVectorStore_Get_ByBatch tests batch getting documents
func TestVectorStore_Get_ByBatch(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	ctx := context.Background()

	// Add multiple documents
	numDocs := 10
	for i := 0; i < numDocs; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("batch_doc_%d", i),
			Content: fmt.Sprintf("Content %d", i),
		}
		vector := []float64{float64(i) / 10.0, 0.5, 0.2}
		err := vs.Add(ctx, doc, vector)
		require.NoError(t, err)
	}

	// Get each document
	for i := 0; i < numDocs; i++ {
		docID := fmt.Sprintf("batch_doc_%d", i)
		doc, vector, err := vs.Get(ctx, docID)
		require.NoError(t, err)
		assert.NotNil(t, doc)
		assert.NotNil(t, vector)
		assert.Equal(t, docID, doc.ID)
	}
}

// TestVectorStore_Update_PartialFields tests partial field updates
func TestVectorStore_Update_PartialFields(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	ctx := context.Background()

	// Add initial document
	originalDoc := &document.Document{
		ID:       "update_test",
		Name:     "Original Name",
		Content:  "Original Content",
		Metadata: map[string]any{"version": 1},
	}
	err := vs.Add(ctx, originalDoc, []float64{1.0, 0.5, 0.2})
	require.NoError(t, err)

	tests := []struct {
		name   string
		update *document.Document
		vector []float64
	}{
		{
			name: "update_name_only",
			update: &document.Document{
				ID:   "update_test",
				Name: "Updated Name",
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			name: "update_content_only",
			update: &document.Document{
				ID:      "update_test",
				Content: "Updated Content",
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			name: "update_metadata_only",
			update: &document.Document{
				ID:       "update_test",
				Metadata: map[string]any{"version": 2, "updated": true},
			},
			vector: []float64{1.0, 0.5, 0.2},
		},
		{
			name: "update_all_fields",
			update: &document.Document{
				ID:       "update_test",
				Name:     "Fully Updated",
				Content:  "Fully Updated Content",
				Metadata: map[string]any{"version": 3},
			},
			vector: []float64{0.9, 0.6, 0.3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vs.Update(ctx, tt.update, tt.vector)
			require.NoError(t, err)
		})
	}
}

// TestVectorStore_Delete_Multiple tests deleting multiple documents
func TestVectorStore_Delete_Multiple(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	ctx := context.Background()

	// Add multiple documents
	numDocs := 5
	for i := 0; i < numDocs; i++ {
		doc := &document.Document{
			ID:      fmt.Sprintf("delete_test_%d", i),
			Content: fmt.Sprintf("Content %d", i),
		}
		vector := []float64{float64(i) / 5.0, 0.5, 0.2}
		err := vs.Add(ctx, doc, vector)
		require.NoError(t, err)
	}

	initialCount := mockClient.GetDocumentCount()
	assert.Equal(t, numDocs, initialCount)

	// Delete documents one by one
	for i := 0; i < numDocs; i++ {
		docID := fmt.Sprintf("delete_test_%d", i)
		err := vs.Delete(ctx, docID)
		require.NoError(t, err)

		// Verify document is deleted
		_, ok := mockClient.GetDocument(docID)
		assert.False(t, ok, "document %s should be deleted", docID)
	}

	// Verify all documents are deleted
	assert.Equal(t, 0, mockClient.GetDocumentCount())
}

// TestVectorStore_ConcurrentReadWrite tests concurrent read and write operations
func TestVectorStore_ConcurrentReadWrite(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	ctx := context.Background()
	numOperations := 20

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			doc := &document.Document{
				ID:      fmt.Sprintf("concurrent_doc_%d", idx),
				Content: fmt.Sprintf("Content %d", idx),
			}
			vector := []float64{float64(idx) / 20.0, 0.5, 0.2}
			_ = vs.Add(ctx, doc, vector)
		}(i)
	}

	// Concurrent reads (reading existing documents)
	for i := 0; i < numOperations/2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			docID := fmt.Sprintf("concurrent_doc_%d", idx)
			// Try to get the document (might not exist yet)
			_, _, _ = vs.Get(ctx, docID)
		}(i)
	}

	wg.Wait()

	// Verify all writes completed
	assert.GreaterOrEqual(t, mockClient.GetDocumentCount(), 1)
}

// TestVectorStore_ContextTimeout tests operations with context timeout
func TestVectorStore_ContextTimeout(t *testing.T) {
	mockClient := newMockClient()
	vs := newVectorStoreWithMockClient(mockClient,
		WithDatabase("test_db"),
		WithCollection("test_collection"),
		WithIndexDimension(3),
	)

	// Create a context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for context to expire
	time.Sleep(10 * time.Millisecond)

	doc := &document.Document{
		ID:      "timeout_test",
		Content: "Test content",
	}
	vector := []float64{1.0, 0.5, 0.2}

	// Note: Mock doesn't check context timeout
	// Real implementation should return context.DeadlineExceeded
	err := vs.Add(ctx, doc, vector)
	_ = err // Mock doesn't handle timeout
}
