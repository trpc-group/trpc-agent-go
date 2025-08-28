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
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/stretchr/testify/require"
)

// defaultConfig returns default Elasticsearch configuration.
func defaultConfig() *Config {
	return &Config{
		Addresses:           []string{"http://localhost:9200"},
		MaxRetries:          3,
		RetryOnTimeout:      true,
		RequestTimeout:      30 * time.Second,
		IndexPrefix:         "trpc_agent",
		VectorDimension:     1536,
		CompressRequestBody: true,
		EnableMetrics:       false,
		EnableDebugLogger:   false,
		RetryOnStatus: []int{
			http.StatusRequestTimeout,     // 408
			http.StatusConflict,           // 409
			http.StatusTooManyRequests,    // 429
			http.StatusBadGateway,         // 502
			http.StatusServiceUnavailable, // 503
			http.StatusGatewayTimeout,     // 504
		},
	}
}

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

func TestDefaultClientBuilder_PassesOptions(t *testing.T) {
	// Replace builder to capture options mapping without creating real client.
	old := GetClientBuilder()
	defer func() { SetClientBuilder(old) }()

	captured := &ClientBuilderOpts{}
	SetClientBuilder(func(opts ...ClientBuilderOpt) (Client, error) {
		for _, o := range opts {
			o(captured)
		}
		return nil, nil
	})

	_, err := GetClientBuilder()(
		WithAddresses([]string{"http://x"}),
		WithUsername("name"),
		WithPassword("pwd"),
		WithAPIKey("api"),
		WithCertificateFingerprint("fp"),
		WithCompressRequestBody(true),
		WithEnableMetrics(true),
		WithEnableDebugLogger(true),
		WithRetryOnStatus([]int{429, 503}),
		WithMaxRetries(5),
		WithRetryOnTimeout(true),
		WithRequestTimeout(defaultConfig().RequestTimeout),
		WithIndexPrefix("pfx"),
		WithVectorDimension(42),
	)
	require.NoError(t, err)
	require.Equal(t, []string{"http://x"}, captured.Addresses)
	require.Equal(t, "name", captured.Username)
	require.Equal(t, "pwd", captured.Password)
	require.Equal(t, "api", captured.APIKey)
	require.Equal(t, "fp", captured.CertificateFingerprint)
	require.True(t, captured.CompressRequestBody)
	require.True(t, captured.EnableMetrics)
	require.True(t, captured.EnableDebugLogger)
	require.Equal(t, []int{429, 503}, captured.RetryOnStatus)
	require.Equal(t, 5, captured.MaxRetries)
	require.True(t, captured.RetryOnTimeout)
	require.Equal(t, "pfx", captured.IndexPrefix)
	require.Equal(t, 42, captured.VectorDimension)
}

func TestDefaultClientBuilder_CreateClient(t *testing.T) {
	c, err := DefaultClientBuilder(
		WithAddresses([]string{"http://localhost:9200"}),
		WithUsername("u"),
		WithPassword("p"),
		WithAPIKey("ak"),
		WithCertificateFingerprint("fp"),
		WithCompressRequestBody(true),
		WithEnableMetrics(true),
		WithEnableDebugLogger(true),
		WithRetryOnStatus([]int{502}),
		WithMaxRetries(4),
		WithRetryOnTimeout(true),
		WithRequestTimeout(5*time.Second),
		WithIndexPrefix("px"),
		WithVectorDimension(64),
	)
	require.NoError(t, err)
	require.NotNil(t, c)

	cc, ok := c.(*client)
	require.True(t, ok)
	require.Equal(t, []string{"http://localhost:9200"}, cc.config.Addresses)
	require.Equal(t, "u", cc.config.Username)
	require.Equal(t, "p", cc.config.Password)
	require.Equal(t, "ak", cc.config.APIKey)
	require.Equal(t, "fp", cc.config.CertificateFingerprint)
	require.True(t, cc.config.CompressRequestBody)
	require.True(t, cc.config.EnableMetrics)
	require.True(t, cc.config.EnableDebugLogger)
	require.Equal(t, []int{502}, cc.config.RetryOnStatus)
	require.Equal(t, 4, cc.config.MaxRetries)
	require.True(t, cc.config.RetryOnTimeout)
	require.Equal(t, 5*time.Second, cc.config.RequestTimeout)
	require.Equal(t, "px", cc.config.IndexPrefix)
	require.Equal(t, 64, cc.config.VectorDimension)
}

func TestNewClient_Create(t *testing.T) {
	cfg := &Config{Addresses: []string{"http://localhost:9200"}}
	c, err := NewClient(cfg)
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestClient_Ping_SuccessAndError(t *testing.T) {
	// Success.
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response {
			return newResponse(200, "{}")
		}),
	})
	require.NoError(t, err)
	c := &client{esClient: es, config: defaultConfig()}
	require.NoError(t, c.Ping(context.Background()))

	// Error.
	esErr, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response {
			return newResponse(500, "err")
		}),
	})
	require.NoError(t, err)
	c = &client{esClient: esErr, config: defaultConfig()}
	err = c.Ping(context.Background())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "ping failed"))
}

func TestClient_IndexLifecycle(t *testing.T) {
	// Create, Exists, Delete happy paths.
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response {
			if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "_doc") {
				return newResponse(200, "{}")
			}
			if r.Method == http.MethodPut { // create index
				return newResponse(200, "{}")
			}
			if r.Method == http.MethodHead { // exists
				return newResponse(200, "")
			}
			if r.Method == http.MethodDelete { // delete index or doc
				return newResponse(200, "{}")
			}
			return newResponse(200, "{}")
		}),
	})
	require.NoError(t, err)
	c := &client{esClient: es, config: defaultConfig()}

	require.NoError(t, c.CreateIndex(context.Background(), "idx", map[string]any{"m": 1}))
	exists, err := c.IndexExists(context.Background(), "idx")
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, c.DeleteIndex(context.Background(), "idx"))
}

func TestClient_DocumentCRUD(t *testing.T) {
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response {
			if r.Method == http.MethodGet {
				return newResponse(200, "{\"_source\":{\"a\":1}}")
			}
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "_update") {
				return newResponse(200, "{}")
			}
			if r.Method == http.MethodDelete {
				return newResponse(200, "{}")
			}
			return newResponse(200, "{}")
		}),
	})
	require.NoError(t, err)
	c := &client{esClient: es, config: defaultConfig()}

	require.NoError(t, c.IndexDocument(context.Background(), "idx", "id1", map[string]any{"k": "v"}))
	body, err := c.GetDocument(context.Background(), "idx", "id1")
	require.NoError(t, err)
	require.True(t, strings.Contains(string(body), "_source"))
	require.NoError(t, c.UpdateDocument(context.Background(), "idx", "id1", map[string]any{"k": "v2"}))
	require.NoError(t, c.DeleteDocument(context.Background(), "idx", "id1"))
}

func TestClient_GetDocument_Error(t *testing.T) {
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response {
			return newResponse(404, "not found")
		}),
	})
	require.NoError(t, err)
	c := &client{esClient: es, config: defaultConfig()}

	_, err = c.GetDocument(context.Background(), "idx", "id1")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "get document failed"))
}

func TestClient_Search_And_Bulk(t *testing.T) {
	es, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses: []string{"http://x"},
		Transport: roundTripper(func(r *http.Request) *http.Response {
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "_search") {
				return newResponse(200, "{\"hits\":{}}")
			}
			if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "_bulk") {
				return newResponse(200, "{}")
			}
			return newResponse(200, "{}")
		}),
	})
	require.NoError(t, err)
	c := &client{esClient: es, config: defaultConfig()}

	resp, err := c.Search(context.Background(), "idx", map[string]any{"q": 1})
	require.NoError(t, err)
	require.True(t, strings.Contains(string(resp), "hits"))

	docs := []BulkDocument{
		{ID: "1", Document: map[string]any{"a": 1}, Action: BulkActionIndex},
		{ID: "2", Document: map[string]any{"a": 2}, Action: BulkActionUpdate},
		{ID: "3", Action: BulkActionDelete},
	}
	require.NoError(t, c.BulkIndex(context.Background(), "idx", docs))
}
