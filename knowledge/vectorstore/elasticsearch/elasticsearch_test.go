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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	istorage "trpc.group/trpc-go/trpc-agent-go/internal/storage/elasticsearch"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// mockClient is a mock implementation of istorage.Client for testing.
type mockClient struct {
	mu sync.RWMutex

	// In-memory storage
	indexExists bool
	docs        map[string]map[string]any

	// Configurable responses
	searchHits  []map[string]any
	countResult int

	// Error simulation
	pingError          error
	createIndexError   error
	indexDocError      error
	getDocError        error
	updateDocError     error
	deleteDocError     error
	searchError        error
	countError         error
	deleteByQueryError error
	refreshError       error

	// Call tracking
	indexDocCalls      int
	getDocCalls        int
	updateDocCalls     int
	deleteDocCalls     int
	searchCalls        int
	countCalls         int
	deleteByQueryCalls int
	refreshCalls       int
}

var _ istorage.Client = (*mockClient)(nil)

func newMockClient() *mockClient {
	return &mockClient{
		docs:       make(map[string]map[string]any),
		searchHits: []map[string]any{},
	}
}

func (m *mockClient) Ping(ctx context.Context) error {
	return m.pingError
}

func (m *mockClient) CreateIndex(ctx context.Context, indexName string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createIndexError != nil {
		return m.createIndexError
	}
	m.indexExists = true
	return nil
}

func (m *mockClient) DeleteIndex(ctx context.Context, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexExists = false
	return nil
}

func (m *mockClient) IndexExists(ctx context.Context, indexName string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.indexExists, nil
}

func (m *mockClient) IndexDoc(ctx context.Context, indexName, id string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.indexDocCalls++

	if m.indexDocError != nil {
		return m.indexDocError
	}

	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return err
	}
	m.docs[id] = doc
	return nil
}

func (m *mockClient) GetDoc(ctx context.Context, indexName, id string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.getDocCalls++

	if m.getDocError != nil {
		return nil, m.getDocError
	}

	if doc, ok := m.docs[id]; ok {
		result := map[string]any{
			"found":   true,
			"_source": doc,
			"_id":     id,
		}
		return json.Marshal(result)
	}

	return json.Marshal(map[string]any{"found": false})
}

func (m *mockClient) UpdateDoc(ctx context.Context, indexName, id string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updateDocCalls++

	if m.updateDocError != nil {
		return m.updateDocError
	}

	var updateReq struct {
		Doc json.RawMessage `json:"doc"`
	}
	if err := json.Unmarshal(body, &updateReq); err != nil {
		return err
	}

	if doc, ok := m.docs[id]; ok {
		var updates map[string]any
		if err := json.Unmarshal(updateReq.Doc, &updates); err != nil {
			return err
		}
		for k, v := range updates {
			doc[k] = v
		}
		m.docs[id] = doc
		return nil
	}

	return errors.New("document not found")
}

func (m *mockClient) DeleteDoc(ctx context.Context, indexName, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteDocCalls++

	if m.deleteDocError != nil {
		return m.deleteDocError
	}

	delete(m.docs, id)
	return nil
}

func (m *mockClient) Search(ctx context.Context, indexName string, body []byte) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.searchCalls++

	if m.searchError != nil {
		return nil, m.searchError
	}

	result := map[string]any{
		"hits": map[string]any{
			"hits": m.searchHits,
		},
	}
	return json.Marshal(result)
}

func (m *mockClient) Count(ctx context.Context, indexName string, body []byte) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.countCalls++

	if m.countError != nil {
		return 0, m.countError
	}

	return m.countResult, nil
}

func (m *mockClient) DeleteByQuery(ctx context.Context, indexName string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deleteByQueryCalls++

	if m.deleteByQueryError != nil {
		return m.deleteByQueryError
	}

	// Simple implementation: clear all docs (for DeleteAll test)
	m.docs = make(map[string]map[string]any)
	return nil
}

func (m *mockClient) Refresh(ctx context.Context, indexName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.refreshCalls++

	return m.refreshError
}

// Helper methods
func (m *mockClient) SetSearchError(err error)        { m.searchError = err }
func (m *mockClient) SetIndexDocError(err error)      { m.indexDocError = err }
func (m *mockClient) SetGetDocError(err error)        { m.getDocError = err }
func (m *mockClient) SetUpdateDocError(err error)     { m.updateDocError = err }
func (m *mockClient) SetDeleteDocError(err error)     { m.deleteDocError = err }
func (m *mockClient) SetCountError(err error)         { m.countError = err }
func (m *mockClient) SetDeleteByQueryError(err error) { m.deleteByQueryError = err }

func (m *mockClient) GetDocCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.docs)
}

func (m *mockClient) SetSearchHits(hits []map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchHits = hits
}

func (m *mockClient) SetCountResult(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.countResult = count
}

// Helper function to create VectorStore with mock client
func newTestVectorStore(t *testing.T, mc *mockClient, opts ...Option) *VectorStore {
	t.Helper()

	option := defaultOptions
	// Override defaults for testing
	option.vectorDimension = 3
	option.indexName = "test_index"

	for _, opt := range opts {
		opt(&option)
	}

	vs := &VectorStore{
		client:          mc,
		option:          option,
		filterConverter: &esConverter{},
	}

	return vs
}

func TestSearchFilterMode(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.Contains(r.URL.Path, "_search") {
			payload := map[string]any{
				"hits": map[string]any{
					"hits": []map[string]any{{"_score": 0.9, "_source": map[string]any{"id": "d1", "name": "N", "content": "C"}}},
				},
			}
			json.NewEncoder(w).Encode(payload)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3), WithScoreThreshold(0.5))
	require.NoError(t, err)

	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		Filter: &vectorstore.SearchFilter{
			IDs: []string{"d1"},
		},
		SearchMode: vectorstore.SearchModeFilter,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, len(res.Results))
	assert.Equal(t, "N", res.Results[0].Document.Name)
}
