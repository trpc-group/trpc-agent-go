//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultNewHTTPClientWithOptions(t *testing.T) {
	transport := &http.Transport{}
	client := DefaultNewHTTPClient(
		WithHTTPClientName("trpc-agent-client"),
		WithHTTPClientTransport(transport),
	)
	require.IsType(t, &http.Client{}, client)
	httpClient := client.(*http.Client)
	assert.Same(t, transport, httpClient.Transport)
}

func TestWithHTTPClientOptionsUsesDefaultBuilder(t *testing.T) {
	originalBuilder := DefaultNewHTTPClient
	t.Cleanup(func() { DefaultNewHTTPClient = originalBuilder })
	transport := &http.Transport{}
	builtClient := &http.Client{}
	var gotOptions HTTPClientOptions
	DefaultNewHTTPClient = func(opts ...HTTPClientOption) HTTPClient {
		for _, opt := range opts {
			opt(&gotOptions)
		}
		return builtClient
	}
	options := newOptions(
		WithHTTPClientOptions(
			WithHTTPClientName("trpc-agent-client"),
			WithHTTPClientTransport(transport),
		),
	)
	require.Same(t, builtClient, options.httpClient)
	assert.Equal(t, "trpc-agent-client", gotOptions.Name)
	assert.Same(t, transport, gotOptions.Transport)
}

func TestNewUsesDefaultHTTPClientBuilder(t *testing.T) {
	originalBuilder := DefaultNewHTTPClient
	t.Cleanup(func() { DefaultNewHTTPClient = originalBuilder })
	var gotOptions HTTPClientOptions
	var gotRequest *http.Request
	DefaultNewHTTPClient = func(opts ...HTTPClientOption) HTTPClient {
		for _, opt := range opts {
			opt(&gotOptions)
		}
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			gotRequest = request
			body, err := json.Marshal(structureResponse{Structure: testStructureSnapshot()})
			require.NoError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
				Request:    request,
			}, nil
		})}
	}
	runner, err := New(
		"sports-agent",
		WithTarget("polaris://trpc.foo.bar"),
		WithHTTPClientOptions(WithHTTPClientName("agent-http")),
	)
	require.NoError(t, err)
	_, err = runner.Describe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "agent-http", gotOptions.Name)
	require.NotNil(t, gotRequest)
	assert.Equal(t, "polaris", gotRequest.URL.Scheme)
	assert.Equal(t, "/trpc-agent/v1/apps/sports-agent/structure", gotRequest.URL.Path)
}

func TestWithHTTPClientOverridesDefaultBuilder(t *testing.T) {
	originalBuilder := DefaultNewHTTPClient
	t.Cleanup(func() { DefaultNewHTTPClient = originalBuilder })
	explicitClient := &http.Client{}
	called := false
	DefaultNewHTTPClient = func(opts ...HTTPClientOption) HTTPClient {
		called = true
		return &http.Client{}
	}
	options := newOptions(
		WithHTTPClientOptions(WithHTTPClientName("unused")),
		WithHTTPClient(explicitClient),
	)
	require.Same(t, explicitClient, options.httpClient)
	assert.False(t, called)
}

func TestHTTPClientOptionMerge(t *testing.T) {
	transport := &http.Transport{}
	options := &HTTPClientOptions{}
	WithHTTPClientName("first")(options)
	WithHTTPClientName("second")(options)
	WithHTTPClientTransport(transport)(options)
	assert.Equal(t, "second", options.Name)
	assert.Same(t, transport, options.Transport)
}
