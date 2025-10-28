//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
		setupMock func(sqlmock.Sqlmock)
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
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO documents").
					WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
			wantErr: false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "document is required",
		},
		{
			name: "empty_document_id",
			doc: &document.Document{
				ID:      "",
				Content: "Test content",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "document ID is required",
		},
		{
			name: "empty_vector",
			doc: &document.Document{
				ID:      "test_002",
				Content: "Test content",
			},
			vector:    []float64{},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "embedding is required",
		},
		{
			name: "dimension_mismatch",
			doc: &document.Document{
				ID:      "test_003",
				Content: "Test content",
			},
			vector:    []float64{1.0, 0.5}, // Only 2 dimensions, expected 3
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "dimension mismatch",
		},
		{
			name: "database_error",
			doc: &document.Document{
				ID:      "test_004",
				Content: "Test content",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO documents").
					WillReturnError(errors.New("connection timeout"))
			},
			wantErr: true,
			errMsg:  "connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

			err := vs.Add(context.Background(), tt.doc, tt.vector)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}

			tc.AssertExpectations(t)
		})
	}
}

// TestVectorStore_Get tests the Get method
func TestVectorStore_Get(t *testing.T) {
	tests := []struct {
		name      string
		docID     string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *document.Document, []float64)
	}{
		{
			name:  "success_get_existing_document",
			docID: "test_001",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := mockDocumentRow("test_001", "Test Doc", "Test content",
					[]float64{1.0, 0.5, 0.2}, map[string]any{"key": "value"})
				mock.ExpectQuery("SELECT (.+) FROM documents WHERE id").
					WithArgs("test_001").
					WillReturnRows(rows)
			},
			wantErr: false,
			validate: func(t *testing.T, doc *document.Document, vector []float64) {
				assert.Equal(t, "test_001", doc.ID)
				assert.Equal(t, "Test Doc", doc.Name)
				assert.Equal(t, "Test content", doc.Content)
				assert.NotNil(t, doc.Metadata)
			},
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "id is required",
		},
		{
			name:  "document_not_found",
			docID: "non_existent",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"})
				mock.ExpectQuery("SELECT (.+) FROM documents WHERE id").
					WithArgs("non_existent").
					WillReturnRows(rows)
			},
			wantErr: true,
			errMsg:  "not found",
		},
		{
			name:  "database_error",
			docID: "test_002",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT (.+) FROM documents WHERE id").
					WithArgs("test_002").
					WillReturnError(errors.New("database connection lost"))
			},
			wantErr: true,
			errMsg:  "database connection lost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

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
				if tt.validate != nil {
					tt.validate(t, doc, vector)
				}
			}

			tc.AssertExpectations(t)
		})
	}
}

// TestVectorStore_Update tests the Update method
func TestVectorStore_Update(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		vector    []float64
		setupMock func(sqlmock.Sqlmock)
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
			setupMock: func(mock sqlmock.Sqlmock) {
				// Expect document exists check
				existsRows := mockExistsRow(true)
				mock.ExpectQuery("SELECT 1 FROM documents WHERE id").
					WithArgs("test_001").
					WillReturnRows(existsRows)

				// Expect update (6 args: id, updated_at, name, content, embedding, metadata)
				mock.ExpectExec("UPDATE documents SET").
					WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:      "nil_document",
			doc:       nil,
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "document is required",
		},
		{
			name: "empty_document_id",
			doc: &document.Document{
				ID:      "",
				Content: "Test",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "document ID is required",
		},
		{
			name: "document_not_found",
			doc: &document.Document{
				ID:      "non_existent",
				Content: "Test",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {
				existsRows := mockExistsRow(false)
				mock.ExpectQuery("SELECT 1 FROM documents WHERE id").
					WithArgs("non_existent").
					WillReturnRows(existsRows)
			},
			wantErr: true,
			errMsg:  "not found",
		},
		{
			name: "dimension_mismatch",
			doc: &document.Document{
				ID:      "test_002",
				Content: "Test",
			},
			vector: []float64{1.0, 0.5}, // Only 2 dimensions
			setupMock: func(mock sqlmock.Sqlmock) {
				existsRows := mockExistsRow(true)
				mock.ExpectQuery("SELECT 1 FROM documents WHERE id").
					WithArgs("test_002").
					WillReturnRows(existsRows)
			},
			wantErr: true,
			errMsg:  "dimension mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

			err := vs.Update(context.Background(), tt.doc, tt.vector)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}

			tc.AssertExpectations(t)
		})
	}
}

// TestVectorStore_Delete tests the Delete method
func TestVectorStore_Delete(t *testing.T) {
	tests := []struct {
		name      string
		docID     string
		setupMock func(sqlmock.Sqlmock)
		wantErr   bool
		errMsg    string
	}{
		{
			name:  "success_delete_existing_document",
			docID: "test_001",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("DELETE FROM documents WHERE id").
					WithArgs("test_001").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupMock: func(mock sqlmock.Sqlmock) {},
			wantErr:   true,
			errMsg:    "id is required",
		},
		{
			name:  "document_not_found",
			docID: "non_existent",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("DELETE FROM documents WHERE id").
					WithArgs("non_existent").
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantErr: true,
			errMsg:  "not found",
		},
		{
			name:  "database_error",
			docID: "test_002",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("DELETE FROM documents WHERE id").
					WithArgs("test_002").
					WillReturnError(errors.New("delete operation failed"))
			},
			wantErr: true,
			errMsg:  "delete operation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

			err := vs.Delete(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}

			tc.AssertExpectations(t)
		})
	}
}

// TestVectorStore_Count tests the Count method
func TestVectorStore_Count(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
		errMsg    string
	}{
		{
			name: "count_with_results",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := mockCountRow(5)
				mock.ExpectQuery("SELECT COUNT").
					WillReturnRows(rows)
			},
			wantCount: 5,
			wantErr:   false,
		},
		{
			name: "count_empty_store",
			setupMock: func(mock sqlmock.Sqlmock) {
				rows := mockCountRow(0)
				mock.ExpectQuery("SELECT COUNT").
					WillReturnRows(rows)
			},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name: "count_error",
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT COUNT").
					WillReturnError(errors.New("query failed"))
			},
			wantErr: true,
			errMsg:  "query failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

			count, err := vs.Count(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}

			tc.AssertExpectations(t)
		})
	}
}

// TestVectorStore_ConcurrentOperations tests concurrent operations
func TestVectorStore_ConcurrentOperations(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	ctx := context.Background()
	numGoroutines := 10

	// Setup mock expectations for concurrent operations
	for i := 0; i < numGoroutines; i++ {
		tc.mock.ExpectExec("INSERT INTO documents").
			WillReturnResult(sqlmock.NewResult(1, 1))
	}

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

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent operation failed: %v", err)
	}

	tc.AssertExpectations(t)
}

// TestVectorStore_EdgeCases tests edge cases
func TestVectorStore_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		doc       *document.Document
		vector    []float64
		setupMock func(sqlmock.Sqlmock)
	}{
		{
			name: "very_long_content",
			doc: &document.Document{
				ID:      "long_doc",
				Content: string(make([]byte, 100000)), // 100KB content
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO documents").
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
		},
		{
			name: "unicode_content",
			doc: &document.Document{
				ID:      "unicode_doc",
				Content: "测试内容 тест содержание test content 🚀",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO documents").
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
		},
		{
			name: "complex_metadata",
			doc: &document.Document{
				ID:      "meta_doc",
				Content: "Test",
				Metadata: map[string]any{
					"tags":      []string{"tag1", "tag2"},
					"nested":    map[string]any{"key": "value"},
					"timestamp": time.Now().Unix(),
				},
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO documents").
					WillReturnResult(sqlmock.NewResult(1, 1))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			tt.setupMock(tc.mock)

			err := vs.Add(context.Background(), tt.doc, tt.vector)
			require.NoError(t, err)

			tc.AssertExpectations(t)
		})
	}
}
