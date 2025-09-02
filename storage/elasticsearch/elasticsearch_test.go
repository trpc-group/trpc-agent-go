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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	esv7 "github.com/elastic/go-elasticsearch/v7"
	esv8 "github.com/elastic/go-elasticsearch/v8"
	esv9 "github.com/elastic/go-elasticsearch/v9"
	"github.com/stretchr/testify/require"
)

// roundTripper allows mocking http.Transport.
type roundTripper func(*http.Request) *http.Response

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

func newResponse(status int, body string) *http.Response {
	resp := &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
	resp.Header.Set("X-Elastic-Product", "Elasticsearch")
	return resp
}

func TestSetGetClientBuilder(t *testing.T) {
	old := GetClientBuilder()
	defer func() { SetClientBuilder(old) }()

	called := false
	SetClientBuilder(func(opts ...ClientBuilderOpt) (Client, error) {
		called = true
		return nil, nil
	})

	b := GetClientBuilder()
	_, err := b(WithAddresses([]string{"http://es"}))
	require.NoError(t, err)
	require.True(t, called)
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	// Isolate global state.
	old := esRegistry
	esRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { esRegistry = old }()

	const name = "es"
	RegisterElasticsearchInstance(name,
		WithAddresses([]string{"http://a"}),
		WithUsername("u"),
	)

	opts, ok := GetElasticsearchInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 2)

	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, []string{"http://a"}, cfg.Addresses)
	require.Equal(t, "u", cfg.Username)
}

func TestRegistry_NotFound(t *testing.T) {
	old := esRegistry
	esRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { esRegistry = old }()

	opts, ok := GetElasticsearchInstance("missing")
	require.False(t, ok)
	require.Nil(t, opts)
}

func TestDefaultClientBuilder_CreateClient_V9_Default(t *testing.T) {
	c, err := DefaultClientBuilder(
		WithVersion(ESVersionUnspecified),
		WithAddresses([]string{"http://localhost:9200"}),
	)
	require.NoError(t, err)
	_, ok := c.(*clientV9)
	require.True(t, ok)
}

func TestDefaultClientBuilder_CreateClient_V9(t *testing.T) {
	c, err := DefaultClientBuilder(
		WithVersion(ESVersionV9),
		WithAddresses([]string{"http://localhost:9200"}),
	)
	require.NoError(t, err)
	_, ok := c.(*clientV9)
	require.True(t, ok)
}

func TestDefaultClientBuilder_CreateClient_V8(t *testing.T) {
	c, err := DefaultClientBuilder(
		WithVersion(ESVersionV8),
		WithAddresses([]string{"http://localhost:9200"}),
	)
	require.NoError(t, err)
	_, ok := c.(*clientV8)
	require.True(t, ok)
}

func TestDefaultClientBuilder_CreateClient_V7(t *testing.T) {
	c, err := DefaultClientBuilder(
		WithVersion(ESVersionV7),
		WithAddresses([]string{"http://localhost:9200"}),
	)
	require.NoError(t, err)
	_, ok := c.(*clientV7)
	require.True(t, ok)
}

func TestClientV9_Ping_SuccessAndError(t *testing.T) {
	// Success.
	es, err := esv9.NewClient(esv9.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }),
	})
	require.NoError(t, err)
	c := &clientV9{esClient: es}
	require.NoError(t, c.Ping(context.Background()))

	// Error.
	esErr, err := esv9.NewClient(esv9.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(500, "err") }),
	})
	require.NoError(t, err)
	c = &clientV9{esClient: esErr}
	err = c.Ping(context.Background())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "ping failed"))
}

func TestClientV8_Ping_SuccessAndError(t *testing.T) {
	// Success.
	es, err := esv8.NewClient(esv8.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }),
	})
	require.NoError(t, err)
	c := &clientV8{esClient: es}
	require.NoError(t, c.Ping(context.Background()))

	// Error.
	esErr, err := esv8.NewClient(esv8.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(500, "err") }),
	})
	require.NoError(t, err)
	c = &clientV8{esClient: esErr}
	err = c.Ping(context.Background())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "ping failed"))
}

func TestClientV7_Ping_SuccessAndError(t *testing.T) {
	// Success.
	es, err := esv7.NewClient(esv7.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }),
	})
	require.NoError(t, err)
	c := &clientV7{esClient: es}
	require.NoError(t, c.Ping(context.Background()))

	// Error.
	esErr, err := esv7.NewClient(esv7.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(500, "err") }),
	})
	require.NoError(t, err)
	c = &clientV7{esClient: esErr}
	err = c.Ping(context.Background())
	require.Error(t, err)
}

// v9MockTransport helps simulate Elasticsearch HTTP API for CRUD and search tests.
func v9MockTransport(handler func(r *http.Request) *http.Response) roundTripper {
	return roundTripper(func(r *http.Request) *http.Response { return handler(r) })
}

func TestClientV9_CRUDAndSearch(t *testing.T) {
	// In-memory storage to simulate index documents.
	docs := make(map[string][]byte)
	indexExists := false

	rt := v9MockTransport(func(r *http.Request) *http.Response {
		// Always set ES product header.
		ok := func(code int, body string) *http.Response { return newResponse(code, body) }

		p := r.URL.Path
		m := r.Method

		// HEAD /{index}.
		if m == http.MethodHead && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			if indexExists {
				return ok(http.StatusOK, "")
			}
			return ok(http.StatusNotFound, "")
		}
		// PUT /{index}.
		if m == http.MethodPut && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			indexExists = true
			return ok(http.StatusOK, `{}`)
		}
		// POST/PUT /{index}/_doc/{id}.
		if (m == http.MethodPost || m == http.MethodPut) && strings.Contains(p, "/_doc/") && !strings.Contains(p, "_update") {
			b, _ := io.ReadAll(r.Body)
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			docs[id] = b
			return ok(http.StatusOK, `{}`)
		}
		// GET /{index}/_doc/{id}.
		if m == http.MethodGet && strings.Contains(p, "/_doc/") {
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			if b, ok1 := docs[id]; ok1 {
				return ok(http.StatusOK, `{"found":true,"_source":`+string(b)+`}`)
			}
			return ok(http.StatusNotFound, `{"found":false}`)
		}
		// POST /{index}/_update/{id}.
		if m == http.MethodPost && strings.Contains(p, "_update") {
			// Minimal update emulation that merges top-level fields from doc.
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			b, _ := io.ReadAll(r.Body)
			if src, ok1 := docs[id]; ok1 {
				var payload struct {
					Doc map[string]any `json:"doc"`
				}
				_ = json.Unmarshal(b, &payload)
				var current map[string]any
				_ = json.Unmarshal(src, &current)
				for k, v := range payload.Doc {
					current[k] = v
				}
				nb, _ := json.Marshal(current)
				docs[id] = nb
			}
			return ok(http.StatusOK, `{}`)
		}
		// DELETE /{index}/_doc/{id}.
		if m == http.MethodDelete && strings.Contains(p, "/_doc/") {
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			if _, ok1 := docs[id]; ok1 {
				delete(docs, id)
				return ok(http.StatusOK, `{}`)
			}
			return ok(http.StatusNotFound, `{}`)
		}
		// POST /{index}/_search.
		if m == http.MethodPost && strings.Contains(p, "_search") {
			// Return a response with a single hit if any doc exists.
			if len(docs) == 0 {
				return ok(http.StatusOK, `{"hits":{"hits":[]}}`)
			}
			return ok(http.StatusOK, `{"hits":{"hits":[{"_score":1.0,"_source":`+string(docs[mapKey(docs)])+`}]}}`)
		}
		return ok(http.StatusOK, `{}`)
	})

	es, err := esv9.NewClient(esv9.Config{Addresses: []string{"http://mock"}, Transport: rt})
	require.NoError(t, err)
	c := &clientV9{esClient: es}

	ctx := context.Background()
	// IndexExists should be false before create.
	exists, err := c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.False(t, exists)

	// CreateIndex.
	require.NoError(t, c.CreateIndex(ctx, "idx", []byte(`{"mappings":{}}`)))

	// IndexExists should be true after create.
	exists, err = c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.True(t, exists)

	// IndexDoc.
	doc := []byte(`{"id":"1","name":"n","content":"c"}`)
	require.NoError(t, c.IndexDoc(ctx, "idx", "1", doc))

	// GetDoc success.
	b, err := c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "\"found\":true")

	// UpdateDoc.
	upd := []byte(`{"doc":{"name":"n2"}}`)
	require.NoError(t, c.UpdateDoc(ctx, "idx", "1", upd))

	// GetDoc reflects update.
	b, err = c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "n2")

	// Search returns one hit.
	searchRes, err := c.Search(ctx, "idx", []byte(`{}`))
	require.NoError(t, err)
	require.Contains(t, string(searchRes), "hits")

	// DeleteDoc success.
	require.NoError(t, c.DeleteDoc(ctx, "idx", "1"))

	// GetDoc not found now.
	_, err = c.GetDoc(ctx, "idx", "1")
	require.Error(t, err)
}

// mapKey returns an arbitrary key from the map to keep example simple.
func mapKey(m map[string][]byte) string {
	for k := range m {
		return k
	}
	return ""
}

func TestClientV8_CRUDAndSearch(t *testing.T) {
	// In-memory storage to simulate index documents.
	docs := make(map[string][]byte)
	indexExists := false
	updateCalled := false

	rt := roundTripper(func(r *http.Request) *http.Response {
		ok := func(code int, body string) *http.Response { return newResponse(code, body) }
		p := r.URL.Path
		m := r.Method
		if m == http.MethodHead && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			if indexExists {
				return ok(http.StatusOK, "")
			}
			return ok(http.StatusNotFound, "")
		}
		if m == http.MethodPut && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			indexExists = true
			return ok(http.StatusOK, `{}`)
		}
		if (m == http.MethodPost || m == http.MethodPut) && strings.Contains(p, "/_doc/") && !strings.Contains(p, "_update") {
			b, _ := io.ReadAll(r.Body)
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			docs[id] = b
			return ok(http.StatusOK, `{}`)
		}
		if m == http.MethodGet && strings.Contains(p, "/_doc/") {
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			if b, ok1 := docs[id]; ok1 {
				return ok(http.StatusOK, `{"found":true,"_source":`+string(b)+`}`)
			}
			return ok(http.StatusNotFound, `{"found":false}`)
		}
		if m == http.MethodPost && strings.Contains(p, "_update") {
			updateCalled = true
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			b, _ := io.ReadAll(r.Body)
			if src, ok1 := docs[id]; ok1 {
				var payload struct {
					Doc map[string]any `json:"doc"`
				}
				_ = json.Unmarshal(b, &payload)
				var current map[string]any
				_ = json.Unmarshal(src, &current)
				for k, v := range payload.Doc {
					current[k] = v
				}
				nb, _ := json.Marshal(current)
				docs[id] = nb
			}
			return ok(http.StatusOK, `{}`)
		}
		if m == http.MethodDelete && strings.Contains(p, "/_doc/") {
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			if _, ok1 := docs[id]; ok1 {
				delete(docs, id)
				return ok(http.StatusOK, `{}`)
			}
			return ok(http.StatusNotFound, `{}`)
		}
		if m == http.MethodPost && strings.Contains(p, "_search") {
			if len(docs) == 0 {
				return ok(http.StatusOK, `{"hits":{"hits":[]}}`)
			}
			return ok(http.StatusOK, `{"hits":{"hits":[{"_score":1.0,"_source":`+string(docs[mapKey(docs)])+`}]}}`)
		}
		return ok(http.StatusOK, `{}`)
	})

	es, err := esv8.NewClient(esv8.Config{Addresses: []string{"http://mock"}, Transport: rt})
	require.NoError(t, err)
	c := &clientV8{esClient: es}

	ctx := context.Background()
	exists, err := c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, c.CreateIndex(ctx, "idx", []byte(`{"mappings":{}}`)))
	exists, err = c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, c.IndexDoc(ctx, "idx", "1", []byte(`{"id":"1","name":"n","content":"c"}`)))
	b, err := c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "\"found\":true")
	require.NoError(t, c.UpdateDoc(ctx, "idx", "1", []byte(`{"doc":{"name":"n2"}}`)))
	require.True(t, updateCalled)
	b, err = c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "\"found\":true")
	searchRes, err := c.Search(ctx, "idx", []byte(`{}`))
	require.NoError(t, err)
	require.Contains(t, string(searchRes), "hits")
	require.NoError(t, c.DeleteDoc(ctx, "idx", "1"))
	_, err = c.GetDoc(ctx, "idx", "1")
	require.Error(t, err)
}

func TestClientV7_CRUDAndSearch(t *testing.T) {
	docs := make(map[string][]byte)
	indexExists := false
	updateCalled := false

	rt := roundTripper(func(r *http.Request) *http.Response {
		ok := func(code int, body string) *http.Response { return newResponse(code, body) }
		p := r.URL.Path
		m := r.Method
		if m == http.MethodHead && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			if indexExists {
				return ok(http.StatusOK, "")
			}
			return ok(http.StatusNotFound, "")
		}
		if m == http.MethodPut && !strings.Contains(p, "_doc") && !strings.Contains(p, "_search") {
			indexExists = true
			return ok(http.StatusOK, `{}`)
		}
		if (m == http.MethodPost || m == http.MethodPut) && strings.Contains(p, "/_doc/") && !strings.Contains(p, "_update") {
			b, _ := io.ReadAll(r.Body)
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			docs[id] = b
			return ok(http.StatusOK, `{}`)
		}
		if m == http.MethodGet && strings.Contains(p, "/_doc/") {
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			if b, ok1 := docs[id]; ok1 {
				return ok(http.StatusOK, `{"found":true,"_source":`+string(b)+`}`)
			}
			return ok(http.StatusNotFound, `{"found":false}`)
		}
		if m == http.MethodPost && strings.Contains(p, "_update") {
			updateCalled = true
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			b, _ := io.ReadAll(r.Body)
			if src, ok1 := docs[id]; ok1 {
				var payload struct {
					Doc map[string]any `json:"doc"`
				}
				_ = json.Unmarshal(b, &payload)
				var current map[string]any
				_ = json.Unmarshal(src, &current)
				for k, v := range payload.Doc {
					current[k] = v
				}
				nb, _ := json.Marshal(current)
				docs[id] = nb
			}
			return ok(http.StatusOK, `{}`)
		}
		if m == http.MethodDelete && strings.Contains(p, "/_doc/") {
			parts := strings.Split(strings.Trim(p, "/"), "/")
			id := parts[len(parts)-1]
			if _, ok1 := docs[id]; ok1 {
				delete(docs, id)
				return ok(http.StatusOK, `{}`)
			}
			return ok(http.StatusNotFound, `{}`)
		}
		if m == http.MethodPost && strings.Contains(p, "_search") {
			if len(docs) == 0 {
				return ok(http.StatusOK, `{"hits":{"hits":[]}}`)
			}
			return ok(http.StatusOK, `{"hits":{"hits":[{"_score":1.0,"_source":`+string(docs[mapKey(docs)])+`}]}}`)
		}
		return ok(http.StatusOK, `{}`)
	})

	es, err := esv7.NewClient(esv7.Config{Addresses: []string{"http://mock"}, Transport: rt})
	require.NoError(t, err)
	c := &clientV7{esClient: es}

	ctx := context.Background()
	exists, err := c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.False(t, exists)
	require.NoError(t, c.CreateIndex(ctx, "idx", []byte(`{"mappings":{}}`)))
	exists, err = c.IndexExists(ctx, "idx")
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, c.IndexDoc(ctx, "idx", "1", []byte(`{"id":"1","name":"n","content":"c"}`)))
	b, err := c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "\"found\":true")
	require.NoError(t, c.UpdateDoc(ctx, "idx", "1", []byte(`{"doc":{"name":"n2"}}`)))
	require.True(t, updateCalled)
	b, err = c.GetDoc(ctx, "idx", "1")
	require.NoError(t, err)
	require.Contains(t, string(b), "\"found\":true")
	searchRes, err := c.Search(ctx, "idx", []byte(`{}`))
	require.NoError(t, err)
	require.Contains(t, string(searchRes), "hits")
	require.NoError(t, c.DeleteDoc(ctx, "idx", "1"))
	_, err = c.GetDoc(ctx, "idx", "1")
	require.Error(t, err)
}
