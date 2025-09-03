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

func TestDefaultClientBuilder_VersionSelection(t *testing.T) {
	// Default (unspecified) -> v9
	c, err := DefaultClientBuilder(
		WithVersion(ESVersionUnspecified),
	)
	require.NoError(t, err)
	_, ok := c.(*clientV9)
	require.True(t, ok)

	// Explicit v9
	c, err = DefaultClientBuilder(WithVersion(ESVersionV9))
	require.NoError(t, err)
	_, ok = c.(*clientV9)
	require.True(t, ok)

	// v8
	c, err = DefaultClientBuilder(WithVersion(ESVersionV8))
	require.NoError(t, err)
	_, ok = c.(*clientV8)
	require.True(t, ok)

	// v7
	c, err = DefaultClientBuilder(WithVersion(ESVersionV7))
	require.NoError(t, err)
	_, ok = c.(*clientV7)
	require.True(t, ok)

	// unknown
	_, err = DefaultClientBuilder(WithVersion(ESVersion("unknown")))
	require.Error(t, err)
	require.Equal(t, "elasticsearch: unknown version unknown", err.Error())
}

func TestNewClient_WrapsSupportedAndUnsupported(t *testing.T) {
	// v9
	es9, err := esv9.NewClient(esv9.Config{
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }),
	})
	require.NoError(t, err)
	c, err := NewClient(es9)
	require.NoError(t, err)
	_, ok := c.(*clientV9)
	require.True(t, ok)

	// v8
	es8, err := esv8.NewClient(esv8.Config{
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }),
	})
	require.NoError(t, err)
	c, err = NewClient(es8)
	require.NoError(t, err)
	_, ok = c.(*clientV8)
	require.True(t, ok)

	// v7
	es7, err := esv7.NewClient(esv7.Config{
		Transport: roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }),
	})
	require.NoError(t, err)
	c, err = NewClient(es7)
	require.NoError(t, err)
	_, ok = c.(*clientV7)
	require.True(t, ok)

	// unsupported
	_, err = NewClient(123)
	require.Error(t, err)
}

func TestPing_SuccessAndError_Table(t *testing.T) {
	versions := []string{"v7", "v8", "v9"}
	for _, v := range versions {
		t.Run(v+"_success", func(t *testing.T) {
			c := newClientForVersion(t, v, roundTripper(func(r *http.Request) *http.Response { return newResponse(200, "{}") }))
			require.NoError(t, c.Ping(context.Background()))
		})
		t.Run(v+"_error", func(t *testing.T) {
			c := newClientForVersion(t, v, roundTripper(func(r *http.Request) *http.Response { return newResponse(500, "err") }))
			require.Error(t, c.Ping(context.Background()))
		})
	}
}

// newClientForVersion constructs an adapter client using NewClient and a mocked transport.
func newClientForVersion(t *testing.T, version string, rt roundTripper) Client {
	t.Helper()
	switch version {
	case "v7":
		es, err := esv7.NewClient(esv7.Config{Transport: rt})
		require.NoError(t, err)
		c, err := NewClient(es)
		require.NoError(t, err)
		return c
	case "v8":
		es, err := esv8.NewClient(esv8.Config{Transport: rt})
		require.NoError(t, err)
		c, err := NewClient(es)
		require.NoError(t, err)
		return c
	case "v9":
		es, err := esv9.NewClient(esv9.Config{Transport: rt})
		require.NoError(t, err)
		c, err := NewClient(es)
		require.NoError(t, err)
		return c
	default:
		require.FailNow(t, "unsupported version", version)
		return nil
	}
}
