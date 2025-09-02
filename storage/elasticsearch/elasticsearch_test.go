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
