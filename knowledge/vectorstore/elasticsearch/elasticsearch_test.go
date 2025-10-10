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
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

// TestVectorStore_MockedTransport covers ensureIndex + CRUD without real ES using httptest.Server.
func TestVectorStore_MockedTransport(t *testing.T) {
	// In-memory state to mimic ES index + docs
	indexCreated := false
	docs := make(map[string]map[string]any)

	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")

		p := r.URL.Path
		_, last := path.Split(p)

		// HEAD /{index}
		if r.Method == http.MethodHead && !strings.Contains(p, "_doc") {
			if indexCreated {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		// PUT /{index}
		if r.Method == http.MethodPut && !strings.Contains(p, "_doc") && !strings.Contains(p, "_update") {
			indexCreated = true
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		// POST/PUT /{index}/_doc/{id}
		if (r.Method == http.MethodPost || r.Method == http.MethodPut) && strings.Contains(p, "/_doc/") && !strings.Contains(p, "_update") {
			id := last
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			docs[id] = m
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		// GET /{index}/_doc/{id}
		if r.Method == http.MethodGet && strings.Contains(p, "/_doc/") {
			id := last
			if d, ok := docs[id]; ok {
				resp := map[string]any{"found": true, "_source": d}
				json.NewEncoder(w).Encode(resp)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"found":false}`))
			return
		}
		// POST /{index}/_update/{id}
		if r.Method == http.MethodPost && strings.Contains(p, "_update/") {
			id := last
			var upd struct {
				Doc map[string]any `json:"doc"`
			}
			_ = json.NewDecoder(r.Body).Decode(&upd)
			if d, ok := docs[id]; ok {
				for k, v := range upd.Doc {
					d[k] = v
				}
				docs[id] = d
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		// DELETE /{index}/_doc/{id}
		if r.Method == http.MethodDelete && strings.Contains(p, "/_doc/") {
			id := last
			delete(docs, id)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	// Instantiate VectorStore pointing to httptest.Server
	vs, err := New(
		WithAddresses([]string{hs.URL}),
		WithVectorDimension(3),
		WithIndexName("test_index"),
	)
	require.NoError(t, err, "Failed to create vector store")
	require.NotNil(t, vs, "Vector store should not be nil")
	assert.Equal(t, "test_index", vs.option.indexName, "Expected index name 'test_index', got '%s'", vs.option.indexName)

	ctx := context.Background()

	// Add
	doc := &document.Document{
		ID:        "doc1",
		Name:      "Name",
		Content:   "Content",
		Metadata:  map[string]any{"type": "test"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	embedding := []float64{0.1, 0.2, 0.3}
	require.NoError(t, vs.Add(ctx, doc, embedding), "Failed to add document")

	// Update
	doc.Name = "Updated Name"
	doc.UpdatedAt = time.Now()
	require.NoError(t, vs.Update(ctx, doc, embedding), "Failed to update document")

	// Delete
	require.NoError(t, vs.Delete(ctx, "doc1"), "Failed to delete document")
}

func TestParseSearchResultsVariants(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	vs.option.scoreThreshold = 0.5

	// Case 1: empty hits
	res, err := vs.parseSearchResults([]byte(`{"hits":{"hits":[]}}`))
	require.NoError(t, err)
	assert.Len(t, res.Results, 0)

	// Case 2: one below threshold, one above
	payload := map[string]any{
		"hits": map[string]any{
			"hits": []map[string]any{
				{"_score": 0.4, "_source": map[string]any{"id": "a", "name": "A", "content": "x"}},
				{"_score": 0.7, "_source": map[string]any{"id": "b", "name": "B", "content": "y"}},
			},
		},
	}
	b, _ := json.Marshal(payload)
	res, err = vs.parseSearchResults(b)
	require.NoError(t, err)
	assert.Len(t, res.Results, 1)
	assert.Equal(t, "B", res.Results[0].Document.Name)
}

func TestValidationErrors(t *testing.T) {
	// Server that only handles HEAD/PUT for index and returns 200 for others
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)

	ctx := context.Background()
	doc := &document.Document{ID: "x", Name: "n", Content: "c", CreatedAt: time.Now(), UpdatedAt: time.Now()}

	// Add invalids
	assert.Error(t, vs.Add(ctx, nil, []float64{0.1, 0.2, 0.3})) // nil doc
	assert.Error(t, vs.Add(ctx, doc, []float64{}))              // empty embedding
	assert.Error(t, vs.Add(ctx, doc, []float64{0.1}))           // wrong dim

	// Update invalids
	assert.Error(t, vs.Update(ctx, nil, []float64{0.1, 0.2, 0.3})) // nil doc
	assert.Error(t, vs.Update(ctx, doc, []float64{}))              // empty embedding
	assert.Error(t, vs.Update(ctx, doc, []float64{0.1}))           // wrong dim

	// Get invalid id
	_, _, err = vs.Get(ctx, "")
	assert.Error(t, err)

	// Search nil query
	_, err = vs.Search(ctx, nil)
	assert.Error(t, err)

	// Search empty vector
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{Vector: []float64{}, SearchMode: vectorstore.SearchModeVector})
	assert.Error(t, err)

	// Search wrong vector dimension
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{Vector: []float64{0.1, 0.2}, SearchMode: vectorstore.SearchModeVector})
	assert.Error(t, err)
}

func TestServerErrorPaths(t *testing.T) {
	// Mock server returns 500 on specific endpoints
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if strings.Contains(r.URL.Path, "_search") {
			// Return a valid 200 for HEAD/PUT index; but here we need a failure on search body
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("err"))
			return
		}
		if strings.Contains(r.URL.Path, "_doc/") && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
			// Indexing error for Add
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Path, "_update/") && r.Method == http.MethodPost {
			// Update error
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if strings.Contains(r.URL.Path, "_doc/") && r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "_doc") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	ctx := context.Background()

	// Search should error
	_, err = vs.Search(ctx, &vectorstore.SearchQuery{Vector: []float64{0.1, 0.2, 0.3}, SearchMode: vectorstore.SearchModeVector})
	assert.Error(t, err)

	// Add should error
	err = vs.Add(ctx, &document.Document{ID: "id", Name: "n", Content: "c", CreatedAt: time.Now(), UpdatedAt: time.Now()}, []float64{0.1, 0.2, 0.3})
	assert.Error(t, err)

	// Update should error
	err = vs.Update(ctx, &document.Document{ID: "id", Name: "n", Content: "c", CreatedAt: time.Now(), UpdatedAt: time.Now()}, []float64{0.1, 0.2, 0.3})
	assert.Error(t, err)

	// Delete should error
	err = vs.Delete(ctx, "nope")
	assert.Error(t, err)
}

func TestEnsureIndexExistsSkipsCreate(t *testing.T) {
	var putCalls int32
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		// HEAD exists
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Count any PUT index calls (should be zero)
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "_doc") {
			atomic.AddInt32(&putCalls, 1)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3), WithIndexName("idx"))
	require.NoError(t, err)
	require.NotNil(t, vs)
	assert.Equal(t, int32(0), atomic.LoadInt32(&putCalls), "Index should not be created when already exists")
}

func TestGetNotFound(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "_doc") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	_, _, err = vs.Get(context.Background(), "missing")
	assert.Error(t, err)
}

func TestSearchSuccess(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "_doc") {
			w.WriteHeader(http.StatusOK)
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
		Vector:     []float64{0.1, 0.2, 0.3},
		SearchMode: vectorstore.SearchModeVector,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 1, len(res.Results))
	assert.Equal(t, "N", res.Results[0].Document.Name)
}

// {"id":"i","title":"title-a","content":"content-a","page":1,"author":"author-a","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","embedding":[0.1,0.2,0.3]}}
func TestSearchSuccess_withDocBuilder(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "_doc") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "_search") {
			payload := map[string]any{
				"hits": map[string]any{
					"hits": []map[string]any{
						{"_score": 0.9, "_source": map[string]any{"id": "d1", "title": "title-a", "content": "content-a", "page": 1, "author": "author-a", "created_at": "2024-01-01T00:00:00Z", "updated_at": "2024-01-01T00:00:00Z"}},
						{"_score": 0.9, "_source": map[string]any{"id": "d2", "title": "title-b", "content": "content-b", "page": 2, "author": "author-b", "created_at": "2024-01-01T00:00:00Z", "updated_at": "2024-01-01T00:00:00Z"}},
					},
				},
			}
			json.NewEncoder(w).Encode(payload)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	docBuilder := func(hitSource json.RawMessage) (*document.Document, []float64, error) {
		var source struct {
			ID        string    `json:"id"`
			Title     string    `json:"title"`
			Content   string    `json:"content"`
			Page      int       `json:"page"`
			Author    string    `json:"author"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
			Embedding []float64 `json:"embedding"`
		}
		if err := json.Unmarshal(hitSource, &source); err != nil {
			return nil, nil, err
		}
		// Create document.
		doc := &document.Document{
			ID:        source.ID,
			Name:      source.Title,
			Content:   source.Content,
			CreatedAt: source.CreatedAt,
			UpdatedAt: source.UpdatedAt,
			Metadata: map[string]any{
				"page":   source.Page,
				"author": source.Author,
			},
		}
		return doc, source.Embedding, nil
	}
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3), WithScoreThreshold(0.5), WithDocBuilder(docBuilder))
	require.NoError(t, err)

	res, err := vs.Search(context.Background(), &vectorstore.SearchQuery{
		Vector:     []float64{0.1, 0.2, 0.3},
		SearchMode: vectorstore.SearchModeVector,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 2, len(res.Results))
	assert.Equal(t, "title-a", res.Results[0].Document.Name)
	assert.Equal(t, 2, len(res.Results[0].Document.Metadata))
	assert.Equal(t, 2, res.Results[1].Document.Metadata["page"])
}

func TestDeleteTwice(t *testing.T) {
	var deletedOnce bool
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut && !strings.Contains(r.URL.Path, "_doc") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "_doc/") {
			if deletedOnce {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			deletedOnce = true
			w.WriteHeader(http.StatusOK)
			return
		}
		// Index doc to allow deletion
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()

	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	ctx := context.Background()

	// Add then delete twice
	require.NoError(t, vs.Add(ctx, &document.Document{ID: "d", Name: "n", Content: "c", CreatedAt: time.Now(), UpdatedAt: time.Now()}, []float64{0.1, 0.2, 0.3}))
	require.NoError(t, vs.Delete(ctx, "d"))
	err = vs.Delete(ctx, "d")
	assert.Error(t, err)
}

func TestGetFoundFalse200(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"found":false}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	_, _, err = vs.Get(context.Background(), "id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "document not found")
}

func TestGetInvalidResponseJSON(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{")) // invalid JSON
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	_, _, err = vs.Get(context.Background(), "id")
	assert.Error(t, err)
}

func TestGetInvalidSource(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"found":true, "_source":"oops"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	_, _, err = vs.Get(context.Background(), "id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid document source")
}

func TestGetMissingEmbedding(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			// embedding absent
			w.Write([]byte(`{"found":true, "_source": {"id":"i","name":"n","content":"c","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	_, _, err = vs.Get(context.Background(), "id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding vector not found")
}

func TestGetSuccess(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"found":true, "_source": {"id":"i","name":"n","content":"c","metadata":{"a":1},"created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","embedding":[0.1,0.2,0.3]}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3))
	require.NoError(t, err)
	d, emb, err := vs.Get(context.Background(), "id")
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, "i", d.ID)
	assert.Equal(t, "n", d.Name)
	assert.Equal(t, "c", d.Content)
	assert.Equal(t, 3, len(emb))
}

func TestGetSuccess_withDocBuilder(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "_doc/") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"found":true, "_source": {"id":"i","title":"title-a","content":"content-a","page":1,"author":"author-a","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z","embedding":[0.1,0.2,0.3]}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer hs.Close()
	docBuilder := func(hitSource json.RawMessage) (*document.Document, []float64, error) {
		var source struct {
			ID        string    `json:"id"`
			Title     string    `json:"title"`
			Content   string    `json:"content"`
			Page      int       `json:"page"`
			Author    string    `json:"author"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
			Embedding []float64 `json:"embedding"`
		}
		if err := json.Unmarshal(hitSource, &source); err != nil {
			return nil, nil, err
		}
		// Create document.
		doc := &document.Document{
			ID:        source.ID,
			Name:      source.Title,
			Content:   source.Content,
			CreatedAt: source.CreatedAt,
			UpdatedAt: source.UpdatedAt,
			Metadata: map[string]any{
				"page":   source.Page,
				"author": source.Author,
			},
		}
		return doc, source.Embedding, nil
	}
	vs, err := New(WithAddresses([]string{hs.URL}), WithVectorDimension(3), WithDocBuilder(docBuilder))
	require.NoError(t, err)
	d, emb, err := vs.Get(context.Background(), "id")
	require.NoError(t, err)
	require.NotNil(t, d)
	assert.Equal(t, "i", d.ID)
	assert.Equal(t, "title-a", d.Name)
	assert.Equal(t, "content-a", d.Content)
	assert.Equal(t, 3, len(emb))
	assert.Equal(t, 2, len(d.Metadata))
	assert.Equal(t, 1, d.Metadata["page"])
}
