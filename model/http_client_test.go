//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides interfaces for working with LLMs.
package model

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultNewHTTPClient(t *testing.T) {
	client := DefaultNewHTTPClient()

	require.NotNil(t, client)
}

func TestDefaultNewHTTPClient_WithOptions(t *testing.T) {
	customTransport := &http.Transport{}

	client := DefaultNewHTTPClient(
		WithHTTPClientName("test-client"),
		WithHTTPClientTransport(customTransport),
	)

	require.NotNil(t, client)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Same(t, customTransport, httpClient.Transport)
}

func TestDefaultNewHTTPClient_NoOptions(t *testing.T) {
	client := DefaultNewHTTPClient()

	require.NotNil(t, client)

	if httpClient, ok := client.(*http.Client); ok {
		assert.Nil(t, httpClient.Transport)
	}
}

func TestWithHTTPClientName(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientName("test-name")(options)

	assert.Equal(t, "test-name", options.Name)
}

func TestWithHTTPClientTransport(t *testing.T) {
	customTransport := &http.Transport{}
	options := &HTTPClientOptions{}

	WithHTTPClientTransport(customTransport)(options)

	assert.Same(t, customTransport, options.Transport)
}

func TestHTTPClientOptions_Merge(t *testing.T) {
	customTransport := &http.Transport{}
	options := &HTTPClientOptions{}

	WithHTTPClientName("merged-client")(options)
	WithHTTPClientTransport(customTransport)(options)

	assert.Equal(t, "merged-client", options.Name)
	assert.Same(t, customTransport, options.Transport)
}

func TestHTTPClientInterface(t *testing.T) {
	var _ HTTPClient = &http.Client{}
}

func TestHTTPClientNewFunc(t *testing.T) {
	var newFunc HTTPClientNewFunc = func(opts ...HTTPClientOption) HTTPClient {
		return &http.Client{}
	}

	client := newFunc()
	require.NotNil(t, client)
}

func TestDefaultImplementationCompleteness(t *testing.T) {
	options := &HTTPClientOptions{}

	opts := []HTTPClientOption{
		WithHTTPClientName("complete-test"),
		WithHTTPClientTransport(http.DefaultTransport),
	}

	for _, opt := range opts {
		opt(options)
	}

	assert.Equal(t, "complete-test", options.Name)
	assert.Same(t, http.DefaultTransport, options.Transport)
}

func TestEdgeCases(t *testing.T) {
	options := &HTTPClientOptions{}
	WithHTTPClientName("")(options)
	assert.Empty(t, options.Name)

	options = &HTTPClientOptions{}
	WithHTTPClientTransport(nil)(options)
	assert.Nil(t, options.Transport)

	client := DefaultNewHTTPClient()
	require.NotNil(t, client)
}

func TestMultipleOptionApplications(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientName("first")(options)
	WithHTTPClientName("second")(options)
	WithHTTPClientName("third")(options)

	assert.Equal(t, "third", options.Name)
}

func TestDefaultNewHTTPClient_DefaultTimeout(t *testing.T) {
	client := DefaultNewHTTPClient()
	require.NotNil(t, client)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, defaultHTTPClientTimeout, httpClient.Timeout)
}

func TestDefaultNewHTTPClient_CustomTimeout(t *testing.T) {
	custom := 30 * time.Second
	client := DefaultNewHTTPClient(WithHTTPClientTimeout(custom))
	require.NotNil(t, client)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, custom, httpClient.Timeout)
}

func TestDefaultNewHTTPClient_DisableTimeout(t *testing.T) {
	client := DefaultNewHTTPClient(WithHTTPClientTimeout(0))
	require.NotNil(t, client)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Equal(t, time.Duration(0), httpClient.Timeout)
}

func TestWithHTTPClientTimeout(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientTimeout(10 * time.Second)(options)
	assert.Equal(t, 10*time.Second, options.Timeout)
	assert.False(t, options.DisableTimeout)

	options = &HTTPClientOptions{}
	WithHTTPClientTimeout(0)(options)
	assert.Equal(t, time.Duration(0), options.Timeout)
	assert.True(t, options.DisableTimeout)
}
