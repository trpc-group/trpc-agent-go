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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
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
				// Expect update (6 args: id, updated_at, name, content, embedding, metadata)
				mock.ExpectExec("UPDATE documents SET").
					WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
			wantErr: false,
		},
		{
			name:    "nil_document",
			doc:     nil,
			vector:  []float64{1.0, 0.5, 0.2},
			wantErr: true,
			errMsg:  "document is required",
		},
		{
			name: "empty_document_id",
			doc: &document.Document{
				ID:      "",
				Content: "Test",
			},
			vector:  []float64{1.0, 0.5, 0.2},
			wantErr: true,
			errMsg:  "document ID is required",
		},
		{
			name: "document_not_found",
			doc: &document.Document{
				ID:      "non_existent",
				Content: "Test",
			},
			setupMock: func(mock sqlmock.Sqlmock) {
				// Expect update (6 args: id, updated_at, name, content, embedding, metadata)
				mock.ExpectExec("UPDATE documents SET").
					WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			vector:  []float64{1.0, 0.5, 0.2},
			wantErr: true,
			errMsg:  "pgvector document not updated",
		},
		{
			name: "dimension_mismatch",
			doc: &document.Document{
				ID:      "test_002",
				Content: "Test",
			},
			vector:  []float64{1.0, 0.5}, // Only 2 dimensions
			wantErr: true,
			errMsg:  "dimension mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs, tc := newTestVectorStore(t, WithIndexDimension(3))
			defer tc.Close()

			if tt.setupMock != nil {
				tt.setupMock(tc.mock)
			}

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

// TestVectorStore_GetMetadata tests metadata retrieval
func TestVectorStore_GetMetadata(t *testing.T) {
	t.Run("get_metadata_with_limit_and_offset", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		rows := mockDocumentRow("doc_1", "Test Doc", "Test content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"category": "test"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT .+ OFFSET").
			WillReturnRows(rows)

		result, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataLimit(10),
			vectorstore.WithGetMetadataOffset(0))
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Contains(t, result, "doc_1")
		tc.AssertExpectations(t)
	})

	t.Run("get_all_metadata", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		// Return a single result which is less than metadataBatchSize
		// This should not trigger a second query
		rows1 := mockDocumentRow("doc_1", "Test Doc 1", "Content 1",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"category": "test"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT .+ OFFSET").
			WillReturnRows(rows1)

		result, err := vs.GetMetadata(context.Background())
		require.NoError(t, err)
		require.Len(t, result, 1)
		tc.AssertExpectations(t)
	})

	t.Run("get_metadata_with_ids_filter", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		rows := mockDocumentRow("doc_1", "Test Doc", "Content",
			[]float64{1.0, 0.5, 0.2}, map[string]any{"key": "value"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT .+ OFFSET").
			WillReturnRows(rows)

		result, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataIDs([]string{"doc_1"}),
			vectorstore.WithGetMetadataLimit(10),
			vectorstore.WithGetMetadataOffset(0))
		require.NoError(t, err)
		require.Len(t, result, 1)
		tc.AssertExpectations(t)
	})

	t.Run("get_metadata_with_metadata_filter", func(t *testing.T) {
		vs, tc := newTestVectorStore(t)
		defer tc.Close()

		rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"})
		tc.mock.ExpectQuery("SELECT .+ FROM documents .+ LIMIT .+ OFFSET").
			WillReturnRows(rows)

		result, err := vs.GetMetadata(context.Background(),
			vectorstore.WithGetMetadataFilter(map[string]any{"category": "AI"}),
			vectorstore.WithGetMetadataLimit(5),
			vectorstore.WithGetMetadataOffset(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		tc.AssertExpectations(t)
	})
}

// TestVectorStore_Update_RowsAffectedError tests Update when RowsAffected returns error
func TestVectorStore_Update_RowsAffectedError(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	doc := &document.Document{
		ID:      "test_001",
		Name:    "Updated Name",
		Content: "Updated content",
	}
	vector := []float64{1.0, 0.5, 0.2}

	// Mock update that succeeds but RowsAffected fails
	tc.mock.ExpectExec("UPDATE documents").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err := vs.Update(context.Background(), doc, vector)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rows affected")
}

// TestVectorStore_Delete_RowsAffectedError tests Delete when RowsAffected returns error
func TestVectorStore_Delete_RowsAffectedError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Mock delete that succeeds but RowsAffected fails
	tc.mock.ExpectExec("DELETE FROM documents WHERE id").
		WithArgs("test_001").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err := vs.Delete(context.Background(), "test_001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rows affected")
}

// TestVectorStore_SearchByKeyword_EmptyQuery tests searchByKeyword with empty query
func TestVectorStore_SearchByKeyword_EmptyQuery(t *testing.T) {
	vs, tc := newTestVectorStoreWithTSVector(t, WithIndexDimension(3))
	defer tc.Close()

	query := &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeKeyword,
		Query:      "",
		Limit:      10,
	}

	result, err := vs.searchByKeyword(context.Background(), query)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "keyword is required")
}

// TestVectorStore_SearchByKeyword_FilterError tests searchByKeyword with filter error
func TestVectorStore_SearchByKeyword_FilterError(t *testing.T) {
	vs, tc := newTestVectorStoreWithTSVector(t, WithIndexDimension(3))
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	query := &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeKeyword,
		Query:      "test",
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "invalid",
				Operator: "INVALID",
				Value:    "test",
			},
		},
		Limit: 10,
	}

	result, err := vs.searchByKeyword(context.Background(), query)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "converter error")
}

// TestVectorStore_SearchByVector_FilterError tests searchByVector with filter error
func TestVectorStore_SearchByVector_FilterError(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	query := &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeVector,
		Vector:     []float64{1.0, 0.5, 0.2},
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "invalid",
				Operator: "INVALID",
				Value:    "test",
			},
		},
		Limit: 10,
	}

	result, err := vs.searchByVector(context.Background(), query)
	require.Error(t, err)
	assert.Nil(t, result)
}

// TestVectorStore_SearchByHybrid_FilterError tests searchByHybrid with filter error
func TestVectorStore_SearchByHybrid_FilterError(t *testing.T) {
	vs, tc := newTestVectorStoreWithTSVector(t, WithIndexDimension(3))
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	query := &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeHybrid,
		Vector:     []float64{1.0, 0.5, 0.2},
		Query:      "test",
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "invalid",
				Operator: "INVALID",
				Value:    "test",
			},
		},
		Limit: 10,
	}

	result, err := vs.searchByHybrid(context.Background(), query)
	require.Error(t, err)
	assert.Nil(t, result)
}

// TestVectorStore_SearchByFilter_FilterError tests searchByFilter with filter error
func TestVectorStore_SearchByFilter_FilterError(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	query := &vectorstore.SearchQuery{
		SearchMode: vectorstore.SearchModeFilter,
		Filter: &vectorstore.SearchFilter{
			FilterCondition: &searchfilter.UniversalFilterCondition{
				Field:    "invalid",
				Operator: "INVALID",
				Value:    "test",
			},
		},
		Limit: 10,
	}

	result, err := vs.searchByFilter(context.Background(), query)
	require.Error(t, err)
	assert.Nil(t, result)
}

// TestVectorStore_ExecuteSearch_ParseError tests executeSearch with document parse error
func TestVectorStore_ExecuteSearch_ParseError(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	// Mock query that returns invalid data causing parse error
	tc.mock.ExpectQuery("SELECT (.+) FROM documents").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"}).
			AddRow("test_001", "Test", "Content", "invalid_json", "{}", 1000000, 2000000, 0.9))

	query := "SELECT * FROM documents"
	result, err := vs.executeSearch(context.Background(), query, []any{}, vectorstore.SearchModeVector)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "parse document")
}

// TestVectorStore_DeleteAll tests deleteAll method
func TestVectorStore_DeleteAll(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	tc.mock.ExpectExec("TRUNCATE TABLE documents").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := vs.deleteAll(context.Background())
	require.NoError(t, err)
	tc.AssertExpectations(t)
}

// TestVectorStore_DeleteByFilter_Success tests deleteByFilter with successful deletion
func TestVectorStore_DeleteByFilter_Success(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	config := &vectorstore.DeleteConfig{
		DocumentIDs: []string{"test_001", "test_002"},
	}

	tc.mock.ExpectExec("DELETE FROM documents").
		WillReturnResult(sqlmock.NewResult(0, 2))

	err := vs.deleteByFilter(context.Background(), config)
	require.NoError(t, err)
	tc.AssertExpectations(t)
}

// TestVectorStore_DeleteByFilter_ExecError tests deleteByFilter with exec error
func TestVectorStore_DeleteByFilter_ExecError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	config := &vectorstore.DeleteConfig{
		DocumentIDs: []string{"test_001"},
	}

	tc.mock.ExpectExec("DELETE FROM documents").
		WillReturnError(errors.New("exec error"))

	err := vs.deleteByFilter(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete by filter")
	assert.Contains(t, err.Error(), "exec error")
}

// TestVectorStore_DeleteByFilter_RowsAffectedError tests deleteByFilter when RowsAffected fails
func TestVectorStore_DeleteByFilter_RowsAffectedError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	config := &vectorstore.DeleteConfig{
		DocumentIDs: []string{"test_001"},
	}

	tc.mock.ExpectExec("DELETE FROM documents").
		WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

	err := vs.deleteByFilter(context.Background(), config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get rows affected")
}

// TestVectorStore_Count_FilterError tests Count with filter error
func TestVectorStore_Count_FilterError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	count, err := vs.Count(context.Background(), vectorstore.WithCountFilter(map[string]any{"invalid": "filter"}))
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "count documents")
}

// TestVectorStore_Count_ScanError tests Count with scan error
func TestVectorStore_Count_ScanError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Mock query that returns invalid data type for count
	tc.mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow("invalid_number"))

	count, err := vs.Count(context.Background())
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "count documents")
}

// TestVectorStore_GetMetadata_FilterError tests GetMetadata with filter error
func TestVectorStore_GetMetadata_FilterError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	result, err := vs.GetMetadata(context.Background(),
		vectorstore.WithGetMetadataFilter(map[string]any{"invalid": "filter"}))
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get metadata")
}

// TestVectorStore_GetAllMetadata_BatchError tests getAllMetadata with batch error
func TestVectorStore_GetAllMetadata_BatchError(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	// Create enough rows to trigger a second batch (metadataBatchSize = 5000)
	// We'll mock the first batch with exactly 5000 rows to trigger continuation
	rows := sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"})
	for i := 0; i < 5000; i++ {
		rows.AddRow(fmt.Sprintf("test_%d", i), "Test", "Content", "[1.0,0.5,0.2]", `{"key":"value"}`, 1000000, 2000000, 0.0)
	}
	tc.mock.ExpectQuery("SELECT (.+) FROM documents").
		WillReturnRows(rows)

	// Second batch returns error
	tc.mock.ExpectQuery("SELECT (.+) FROM documents").
		WillReturnError(errors.New("batch query error"))

	config := &vectorstore.GetMetadataConfig{
		Limit:  -1,
		Offset: -1,
	}

	result, err := vs.getAllMetadata(context.Background(), config)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "batch query error")
}

// TestVectorStore_QueryMetadataBatch_FilterError tests queryMetadataBatch with filter error
func TestVectorStore_QueryMetadataBatch_FilterError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Use a mock converter that returns error
	vs.filterConverter = &mockFilterConverter{shouldError: true}

	result, err := vs.queryMetadataBatch(context.Background(), 10, 0, nil, map[string]any{"invalid": "filter"})
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get metadata")
}

// TestVectorStore_QueryMetadataBatch_BuildError tests queryMetadataBatch with build document error
func TestVectorStore_QueryMetadataBatch_BuildError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Mock query that returns invalid embedding data causing parse error
	tc.mock.ExpectQuery("SELECT (.+) FROM documents").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "score"}).
			AddRow("test_001", "Test", "Content", "invalid_vector", "{}", 1000000, 2000000, 0.0))

	result, err := vs.queryMetadataBatch(context.Background(), 10, 0, nil, nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "build document")
}

// TestVectorStore_QueryMetadataBatch_QueryError tests queryMetadataBatch with query error
func TestVectorStore_QueryMetadataBatch_QueryError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	tc.mock.ExpectQuery("SELECT (.+) FROM documents").
		WillReturnError(errors.New("query error"))

	result, err := vs.queryMetadataBatch(context.Background(), 10, 0, nil, nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "get metadata batch")
}

// TestMapToJSON_MarshalError tests mapToJSON with unmarshalable data
func TestMapToJSON_MarshalError(t *testing.T) {
	// Create a map with a value that cannot be marshaled to JSON
	m := map[string]any{
		"invalid": make(chan int), // channels cannot be marshaled
	}

	result := mapToJSON(m)
	// Should return "{}" on marshal error
	assert.Equal(t, "{}", result)
}

// TestVectorStore_Update_ExecError tests Update with exec error (line 298)
func TestVectorStore_Update_ExecError(t *testing.T) {
	vs, tc := newTestVectorStore(t, WithIndexDimension(3))
	defer tc.Close()

	doc := &document.Document{
		ID:      "test_001",
		Name:    "Updated Test",
		Content: "Updated Content",
	}

	tc.mock.ExpectExec("UPDATE documents").
		WillReturnError(errors.New("exec error"))

	err := vs.Update(context.Background(), doc, []float64{1.0, 0.5, 0.2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec error")
}

// TestVectorStore_DeleteAll_Error tests deleteAll with error (line 526)
func TestVectorStore_DeleteAll_Error(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	tc.mock.ExpectExec("TRUNCATE TABLE documents").
		WillReturnError(errors.New("truncate error"))

	err := vs.deleteAll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "truncate error")
}

// TestVectorStore_Count_QueryError tests Count with query error (line 568)
func TestVectorStore_Count_QueryError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	tc.mock.ExpectQuery("SELECT COUNT").
		WillReturnError(errors.New("query error"))

	count, err := vs.Count(context.Background())
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "query error")
}

// TestVectorStore_GetMetadata_OptionsError tests GetMetadata with options error (line 599)
func TestVectorStore_GetMetadata_OptionsError(t *testing.T) {
	vs, tc := newTestVectorStore(t)
	defer tc.Close()

	// Use limit=0 which causes validation error in ApplyGetMetadataOptions
	result, err := vs.GetMetadata(context.Background(), vectorstore.WithGetMetadataLimit(0))
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "limit should be greater than 0")
}
