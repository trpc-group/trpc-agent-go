//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package milvus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/milvus-io/milvus/client/v2/column"
	client "github.com/milvus-io/milvus/client/v2/milvusclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// Helper function to create a VectorStore with mock client for testing
func newVectorStoreWithMockClient(mockClient *mockClient, opts ...Option) *VectorStore {
	option := defaultOptions
	for _, opt := range opts {
		opt(&option)
	}
	option.allFields = []string{
		option.idField,
		option.nameField,
		option.contentField,
		option.vectorField,
		option.metadataField,
		option.createdAtField,
		option.updatedAtField,
	}
	if mockClient.documents == nil {
		mockClient.documents = make(map[string]*mockDocument)
	}

	vs := &VectorStore{
		client:          mockClient,
		option:          option,
		filterConverter: newMilvusFilterConverter(option.metadataField),
	}

	return vs
}

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
			name: "empty_document_id",
			doc: &document.Document{
				ID:      "",
				Content: "Test content",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
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
			setupMock: func(m *mockClient) {},
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
				m.SetInsertError(errors.New("connection timeout"))
			},
			wantErr: true,
			errMsg:  "connection timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			err := vs.Add(context.Background(), tt.doc, tt.vector)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, mockClient.GetInsertCalls())
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
		validate  func(*testing.T, *document.Document, []float64, error)
		wantErr   bool
		errMsg    string
	}{
		{
			name:  "success_get_existing_document",
			docID: "test_001",
			setupMock: func(m *mockClient) {
				// Pre-populate with a document
				m.AddDocument("test_001", &document.Document{
					ID:       "test_001",
					Name:     "Test Doc",
					Content:  "Test content",
					Metadata: map[string]any{"key": "value"},
				}, []float64{1.0, 0.5, 0.2})
			},
			validate: func(t *testing.T, doc *document.Document, vector []float64, err error) {
				require.NoError(t, err)
				assert.Equal(t, "test_001", doc.ID)
				assert.Equal(t, "Test Doc", doc.Name)
				assert.Equal(t, "Test content", doc.Content)
				assert.NotNil(t, doc.Metadata)
				assert.Equal(t, []float64{1.0, 0.5, 0.2}, vector)
			},
			wantErr: false,
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "id is required",
		},
		{
			name:      "document_not_found",
			docID:     "non_existent",
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "not found",
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
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
				WithDocBuilder(customDocBuilder),
			)

			doc, vector, err := vs.Get(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				tt.validate(t, doc, vector, err)
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
				m.AddDocument("test_001", &document.Document{
					ID:      "test_001",
					Name:    "Original Name",
					Content: "Original content",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
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
			name: "empty_document_id",
			doc: &document.Document{
				ID:      "",
				Content: "Test",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "document ID is required",
		},
		{
			name: "document_not_found",
			doc: &document.Document{
				ID:      "non_existent",
				Content: "Test",
			},
			vector:    []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "not found",
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
				m.AddDocument("test_003", &document.Document{
					ID:      "test_003",
					Content: "Original",
				}, []float64{1.0, 0.5, 0.2})
				// Set upsert error
				m.SetUpsertError(errors.New("update failed"))
			},
			wantErr: true,
			errMsg:  "update failed",
		},
		{
			name: "query_error",
			doc: &document.Document{
				ID:      "test_004",
				Content: "Test",
			},
			vector: []float64{1.0, 0.5, 0.2},
			setupMock: func(m *mockClient) {
				m.SetQueryError(errors.New("query failed"))
			},
			wantErr: true,
			errMsg:  "milvus check document existence failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			err := vs.Update(context.Background(), tt.doc, tt.vector)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
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
				m.AddDocument("test_001", &document.Document{
					ID:      "test_001",
					Content: "Test content",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
		},
		{
			name:      "empty_document_id",
			docID:     "",
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "id is required",
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
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			err := vs.Delete(context.Background(), tt.docID)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, mockClient.GetDeleteCalls())
			}
		})
	}
}

// TestVectorStore_Search tests the Search method with vector search
func TestVectorStore_Search(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
		validate  func(*testing.T, *vectorstore.SearchResult)
	}{
		{
			name: "success_vector_search",
			query: &vectorstore.SearchQuery{
				MinScore:   1.0,
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      5,
			},
			setupMock: func(m *mockClient) {
				// Pre-populate with documents
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Name:    "AI Document",
					Content: "AI content",
				}, []float64{1.0, 0.5, 0.2})
				m.AddDocument("doc2", &document.Document{
					ID:      "doc2",
					Name:    "ML Document",
					Content: "ML content",
				}, []float64{0.8, 0.6, 0.3})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
				require.Greater(t, len(result.Results), 0)
			},
		},
		{
			name:      "nil_query",
			query:     nil,
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "query is required",
		},
		{
			name: "empty_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "vector is required",
		},
		{
			name: "client_error",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
			},
			setupMock: func(m *mockClient) {
				m.SetSearchError(errors.New("search service unavailable"))
			},
			wantErr: true,
			errMsg:  "search service unavailable",
		},
		{
			name: "search_with_filter",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				SearchMode: vectorstore.SearchModeVector,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					IDs: []string{"doc1", "doc2"},
					Metadata: map[string]any{
						"category": "AI",
					},
				},
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:       "doc1",
					Content:  "AI content",
					Metadata: map[string]any{"category": "AI"},
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
			},
		},
		{
			name: "search_with_filter_client_error",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeFilter,
				Limit:      10,
				Filter: &vectorstore.SearchFilter{
					IDs: []string{"doc1", "doc2"},
					Metadata: map[string]any{
						"category": "AI",
					},
				},
			},
			setupMock: func(m *mockClient) {
				m.SetQueryError(errors.New("query failed"))
			},
			wantErr: true,
			errMsg:  "search failed",
		},
		{
			name: "search_with_hybrid_client_error",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Query:      "machine learning",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      10,
			},
			setupMock: func(m *mockClient) {
				m.SetHybridSearchError(errors.New("hybrid search failed"))
			},
			wantErr: true,
			errMsg:  "hybrid search failed",
		},
		{
			name: "default_search_mode",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Limit:      10,
				SearchMode: vectorstore.SearchMode(10),
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Content: "AI content",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.NotNil(t, result)
			},
		},
		{
			name: "default_search_mode_with_filter",
			query: &vectorstore.SearchQuery{
				Limit:      10,
				SearchMode: vectorstore.SearchMode(10),
				Filter: &vectorstore.SearchFilter{
					IDs: []string{"doc1", "doc2"},
					Metadata: map[string]any{
						"category": "AI",
					},
				},
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:       "doc1",
					Content:  "AI content",
					Metadata: map[string]any{"category": "AI"},
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
			validate: func(t *testing.T, result *vectorstore.SearchResult) {
				require.Equal(t, result.Results[0].Document.ID, "doc1")
				require.NotNil(t, result)
				require.Equal(t, result.Results[0].Document.Metadata["category"], "AI")
				require.Equal(t, result.Results[0].Document.Content, "AI content")
			},
		},
		{
			name: "default_search_mode_with_filter_invalid",
			query: &vectorstore.SearchQuery{
				Limit:      10,
				SearchMode: vectorstore.SearchMode(10),
				Filter: &vectorstore.SearchFilter{
					FilterCondition: &searchfilter.UniversalFilterCondition{
						Operator: "INVALID",
						Field:    "invalid",
						Value:    "test",
					},
				},
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Content: "AI content",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: true,
			errMsg:  "unsupported operator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
				WithReranker(client.NewRRFReranker()),
				WithDocBuilder(customDocBuilder),
			)

			result, err := vs.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}

// TestVectorStore_SearchByKeyword tests keyword-based search
func TestVectorStore_SearchByKeyword(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_keyword_search",
			query: &vectorstore.SearchQuery{
				Query:      "machine learning",
				SearchMode: vectorstore.SearchModeKeyword,
				MinScore:   1.0,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "AI",
					},
					FilterCondition: &searchfilter.UniversalFilterCondition{
						Operator: searchfilter.OperatorEqual,
						Field:    "category",
						Value:    "AI",
					},
				},
				Limit: 5,
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Content: "machine learning basics",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
		},
		{
			name: "empty_query_keyword",
			query: &vectorstore.SearchQuery{
				Query:      "",
				SearchMode: vectorstore.SearchModeKeyword,
				Limit:      5,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "query text is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			result, err := vs.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestVectorStore_SearchByFilter(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_filter_search",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeFilter,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{"type": "test"},
				},
				Limit: 5,
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:       "doc1",
					Content:  "machine learning basics",
					Metadata: map[string]any{"type": "test"},
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
		},
		{
			name: "empty_filter_search",
			query: &vectorstore.SearchQuery{
				SearchMode: vectorstore.SearchModeFilter,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{},
				},
				Limit: 5,
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Content: "machine learning basics",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)
			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)
			result, err := vs.Search(context.Background(), tt.query)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

// TestVectorStore_SearchByHybrid tests hybrid search
func TestVectorStore_SearchByHybrid(t *testing.T) {
	tests := []struct {
		name      string
		query     *vectorstore.SearchQuery
		setupMock func(*mockClient)
		wantErr   bool
		errMsg    string
	}{
		{
			name: "success_hybrid_search",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Query:      "machine learning",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      5,
				Filter: &vectorstore.SearchFilter{
					Metadata: map[string]any{
						"category": "AI",
					},
				},
				MinScore: 0.5,
			},
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Content: "machine learning and AI",
				}, []float64{1.0, 0.5, 0.2})
			},
			wantErr: false,
		},
		{
			name: "hybrid_search_missing_vector",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{},
				Query:      "test query",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      5,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "vector",
		},
		{
			name: "hybrid_search_missing_query",
			query: &vectorstore.SearchQuery{
				Vector:     []float64{1.0, 0.5, 0.2},
				Query:      "",
				SearchMode: vectorstore.SearchModeHybrid,
				Limit:      5,
			},
			setupMock: func(m *mockClient) {},
			wantErr:   true,
			errMsg:    "query text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			result, err := vs.Search(context.Background(), tt.query)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
			}
		})
	}
}

// TestVectorStore_DeleteByFilter tests the DeleteByFilter method
func TestVectorStore_DeleteByFilter(t *testing.T) {
	tests := []struct {
		name       string
		setupMock  func(*mockClient)
		deleteOpts func() []vectorstore.DeleteOption
		wantErr    bool
		errMsg     string
	}{
		{
			name: "delete_by_document_ids",
			setupMock: func(m *mockClient) {
				for i := 0; i < 5; i++ {
					m.AddDocument(fmt.Sprintf("del_doc_%d", i), &document.Document{
						ID:      fmt.Sprintf("del_doc_%d", i),
						Content: fmt.Sprintf("Content %d", i),
					}, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			deleteOpts: func() []vectorstore.DeleteOption {
				return []vectorstore.DeleteOption{
					vectorstore.WithDeleteDocumentIDs([]string{"del_doc_0", "del_doc_1"}),
				}
			},
			wantErr: false,
		},
		{
			name: "delete_by_filter",
			setupMock: func(m *mockClient) {
				for i := 0; i < 5; i++ {
					m.AddDocument(fmt.Sprintf("del_doc_%d", i), &document.Document{
						ID:      fmt.Sprintf("del_doc_%d", i),
						Content: fmt.Sprintf("Content %d", i),
					}, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			deleteOpts: func() []vectorstore.DeleteOption {
				return []vectorstore.DeleteOption{
					vectorstore.WithDeleteFilter(map[string]any{
						"category": "AI",
					}),
				}
			},
			wantErr: false,
		},
		{
			name: "delete_all",
			setupMock: func(m *mockClient) {
				m.AddDocument("doc1", &document.Document{
					ID:      "doc1",
					Content: "Content",
				}, []float64{1.0, 0.5, 0.2})
			},
			deleteOpts: func() []vectorstore.DeleteOption {
				return []vectorstore.DeleteOption{
					vectorstore.WithDeleteAll(true),
				}
			},
			wantErr: false,
		},
		{
			name:      "delete_no_filter_error",
			setupMock: func(m *mockClient) {},
			deleteOpts: func() []vectorstore.DeleteOption {
				return []vectorstore.DeleteOption{}
			},
			wantErr: true,
			errMsg:  "no filter conditions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			err := vs.DeleteByFilter(context.Background(), tt.deleteOpts()...)

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

// TestVectorStore_Count tests the Count method
func TestVectorStore_Count(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*mockClient)
		countOpts []vectorstore.CountOption
		wantCount int
		wantErr   bool
	}{
		{
			name:      "count_empty_store",
			setupMock: func(m *mockClient) {},
			wantCount: 0,
			wantErr:   false,
		},
		{
			name: "count_multiple_documents",
			setupMock: func(m *mockClient) {
				for i := 0; i < 5; i++ {
					m.AddDocument(fmt.Sprintf("count_doc_%d", i), &document.Document{
						ID:      fmt.Sprintf("count_doc_%d", i),
						Content: fmt.Sprintf("Content %d", i),
					}, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			wantCount: 5,
			wantErr:   false,
		},
		{
			name: "count_by_filter",
			setupMock: func(m *mockClient) {
				for i := 0; i < 5; i++ {
					m.AddDocument(fmt.Sprintf("count_doc_%d", i), &document.Document{
						ID:      fmt.Sprintf("count_doc_%d", i),
						Content: fmt.Sprintf("Content %d", i),
					}, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			countOpts: []vectorstore.CountOption{
				vectorstore.WithCountFilter(map[string]any{"category": "AI"}),
			},
			wantCount: 5,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			count, err := vs.Count(context.Background(), tt.countOpts...)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
		})
	}
}

// TestVectorStore_GetMetadata tests the GetMetadata method
func TestVectorStore_GetMetadata(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*mockClient)
		getOpts   func() []vectorstore.GetMetadataOption
		wantCount int
		wantErr   bool
	}{
		{
			name: "get_all_metadata",
			setupMock: func(m *mockClient) {
				for i := 0; i < 5; i++ {
					m.AddDocument(fmt.Sprintf("meta_doc_%d", i), &document.Document{
						ID:       fmt.Sprintf("meta_doc_%d", i),
						Content:  fmt.Sprintf("Content %d", i),
						Metadata: map[string]any{"index": i, "type": "test"},
					}, []float64{float64(i) / 5.0, 0.5, 0.2})
				}
			},
			getOpts: func() []vectorstore.GetMetadataOption {
				return []vectorstore.GetMetadataOption{
					vectorstore.WithGetMetadataLimit(-1),
					vectorstore.WithGetMetadataOffset(-1),
				}
			},
			wantCount: 5,
			wantErr:   false,
		},
		{
			name: "get_metadata_with_limit",
			setupMock: func(m *mockClient) {
				for i := 0; i < 10; i++ {
					m.AddDocument(fmt.Sprintf("limit_doc_%d", i), &document.Document{
						ID:       fmt.Sprintf("limit_doc_%d", i),
						Content:  fmt.Sprintf("Content %d", i),
						Metadata: map[string]any{"index": i},
					}, []float64{float64(i) / 10.0, 0.5, 0.2})
				}
			},
			getOpts: func() []vectorstore.GetMetadataOption {
				return []vectorstore.GetMetadataOption{
					vectorstore.WithGetMetadataLimit(5),
					vectorstore.WithGetMetadataOffset(0),
				}
			},
			wantCount: 10,
			wantErr:   false,
		},
		{
			name: "get_metadata_with_ids",
			setupMock: func(m *mockClient) {
				for i := 0; i < 10; i++ {
					m.AddDocument(fmt.Sprintf("limit_doc_%d", i), &document.Document{
						ID:       fmt.Sprintf("limit_doc_%d", i),
						Content:  fmt.Sprintf("Content %d", i),
						Metadata: map[string]any{"index": i},
					}, []float64{float64(i) / 10.0, 0.5, 0.2})
				}
			},
			getOpts: func() []vectorstore.GetMetadataOption {
				return []vectorstore.GetMetadataOption{
					vectorstore.WithGetMetadataLimit(5),
					vectorstore.WithGetMetadataOffset(0),
					vectorstore.WithGetMetadataIDs([]string{"limit_doc_1", "limit_doc_2"}),
				}
			},
			wantCount: 10,
			wantErr:   false,
		},
		{
			name: "get_metadata_with_filter",
			setupMock: func(m *mockClient) {
				for i := 0; i < 10; i++ {
					m.AddDocument(fmt.Sprintf("limit_doc_%d", i), &document.Document{
						ID:       fmt.Sprintf("limit_doc_%d", i),
						Content:  fmt.Sprintf("Content %d", i),
						Metadata: map[string]any{"index": i},
					}, []float64{float64(i) / 10.0, 0.5, 0.2})
				}
			},
			getOpts: func() []vectorstore.GetMetadataOption {
				return []vectorstore.GetMetadataOption{
					vectorstore.WithGetMetadataLimit(5),
					vectorstore.WithGetMetadataOffset(0),
					vectorstore.WithGetMetadataFilter(map[string]any{"index": 5}),
				}
			},
			wantCount: 10,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockClient{
				documents: map[string]*mockDocument{},
			}
			tt.setupMock(mockClient)

			vs := newVectorStoreWithMockClient(mockClient,
				WithCollectionName("test_collection"),
				WithDimension(3),
			)

			metadata, err := vs.GetMetadata(context.Background(), tt.getOpts()...)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, len(metadata))
			}
		})
	}
}

// TestVectorStore_Close tests the Close method
func TestVectorStore_Close(t *testing.T) {
	mockClient := &mockClient{
		documents: map[string]*mockDocument{},
	}
	vs := newVectorStoreWithMockClient(mockClient,
		WithCollectionName("test_collection"),
		WithDimension(3),
	)

	err := vs.Close()
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.GetCloseCalls())

	// Close again to test idempotency
	err = vs.Close()
	require.NoError(t, err)

	vs.client = nil
	err = vs.Close()
	require.NoError(t, err)
}

// TestVectorStore_ConcurrentOperations tests concurrent add/get/delete operations
func TestVectorStore_ConcurrentOperations(t *testing.T) {
	mockClient := &mockClient{
		documents: map[string]*mockDocument{},
	}
	vs := newVectorStoreWithMockClient(mockClient,
		WithCollectionName("test_collection"),
		WithDimension(3),
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

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent operation failed: %v", err)
	}

	assert.Equal(t, numGoroutines, mockClient.GetInsertCalls())
}

// TestConvertResultToDocument tests the convertResultToDocument method
func TestConvertResultToDocument(t *testing.T) {
	vs := &VectorStore{
		option: options{
			idField:        "id",
			nameField:      "name",
			contentField:   "content",
			vectorField:    "vector",
			metadataField:  "metadata",
			createdAtField: "created_at",
			updatedAtField: "updated_at",
			allFields:      []string{"id", "name", "content", "vector", "metadata", "created_at", "updated_at"},
		},
	}

	// Create mock result set
	now := time.Now().Unix()
	resultSet := client.ResultSet{
		ResultCount: 2,
		Scores:      []float32{0.95, 0.85},
		Fields: []column.Column{
			column.NewColumnVarChar("id", []string{"doc1", "doc2"}),
			column.NewColumnVarChar("name", []string{"Name1", "Name2"}),
			column.NewColumnVarChar("content", []string{"Content1", "Content2"}),
			column.NewColumnInt64("created_at", []int64{now, now}),
			column.NewColumnInt64("updated_at", []int64{now, now}),
		},
	}

	docs, embeddings, scores, err := vs.convertResultToDocument(resultSet)

	require.NoError(t, err)
	assert.Equal(t, 2, len(docs))
	assert.Equal(t, "doc1", docs[0].ID)
	assert.Equal(t, "Name1", docs[0].Name)
	assert.Equal(t, "Content1", docs[0].Content)
	assert.Equal(t, 2, len(scores))
	assert.Equal(t, float64(0.949999988079071), scores[0])
	_ = embeddings // embeddings might be empty in this mock
}

// TestConvertToFloat32Vector tests the convertToFloat32Vector helper function
func TestConvertToFloat32Vector(t *testing.T) {
	tests := []struct {
		name     string
		input    []float64
		expected []float32
	}{
		{
			name:     "normal_conversion",
			input:    []float64{1.0, 2.0, 3.0},
			expected: []float32{1.0, 2.0, 3.0},
		},
		{
			name:     "empty_slice",
			input:    []float64{},
			expected: []float32{},
		},
		{
			name:     "negative_values",
			input:    []float64{-1.0, -2.5, -3.7},
			expected: []float32{-1.0, -2.5, -3.7},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToFloat32Vector(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetMaxResults tests the getMaxResults helper method
func TestGetMaxResults(t *testing.T) {
	vs := &VectorStore{
		option: options{
			maxResults: 10,
		},
	}

	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"zero_uses_default", 0, 10},
		{"negative_uses_default", -1, 10},
		{"positive_uses_input", 5, 5},
		{"large_uses_input", 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := vs.getMaxResults(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestValidateDeleteConfig tests the validateDeleteConfig method
func TestValidateDeleteConfig(t *testing.T) {
	vs := &VectorStore{}

	tests := []struct {
		name    string
		config  *vectorstore.DeleteConfig
		wantErr bool
	}{
		{
			name: "delete_all_with_document_ids",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   true,
				DocumentIDs: []string{"id1", "id2"},
			},
			wantErr: true,
		},
		{
			name: "delete_all_with_filter",
			config: &vectorstore.DeleteConfig{
				DeleteAll: true,
				Filter:    map[string]any{"key": "value"},
			},
			wantErr: true,
		},
		{
			name: "no_conditions",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   false,
				DocumentIDs: []string{},
				Filter:      map[string]any{},
			},
			wantErr: true,
		},
		{
			name: "valid_delete_all",
			config: &vectorstore.DeleteConfig{
				DeleteAll: true,
			},
			wantErr: false,
		},
		{
			name: "valid_delete_by_ids",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   false,
				DocumentIDs: []string{"id1", "id2"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vs.validateDeleteConfig(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func Test_buildDeleteFilterExpression(t *testing.T) {
	vs := &VectorStore{
		option: defaultOptions,
	}

	tests := []struct {
		name    string
		config  *vectorstore.DeleteConfig
		want    string
		wantErr bool
	}{
		{
			name: "document_ids",
			config: &vectorstore.DeleteConfig{
				DocumentIDs: []string{"id1", "id2"},
			},
			want:    "id in [\"id1\",\"id2\"]",
			wantErr: false,
		},
		{
			name: "delete_all_with_filter",
			config: &vectorstore.DeleteConfig{
				DeleteAll: true,
				Filter:    map[string]any{"key": "value"},
			},
			want:    "metadata[\"key\"] == \"value\"",
			wantErr: false,
		},
		{
			name: "no_conditions",
			config: &vectorstore.DeleteConfig{
				DeleteAll:   false,
				DocumentIDs: []string{},
				Filter:      map[string]any{},
			},
			want:    "",
			wantErr: true,
		},
		{
			name:    "nil_config",
			config:  nil,
			want:    "",
			wantErr: true,
		},
		{
			name: "ids_and_filter",
			config: &vectorstore.DeleteConfig{
				DocumentIDs: []string{"id1", "id2"},
				Filter:      map[string]any{"key": "value"},
			},
			want:    "(id in [\"id1\",\"id2\"] and metadata[\"key\"] == \"value\")",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vs.buildDeleteFilterExpression(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}
