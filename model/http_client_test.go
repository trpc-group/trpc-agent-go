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
		WithHTTPClientTimeout(3*time.Second),
	)

	require.NotNil(t, client)

	httpClient, ok := client.(*http.Client)
	require.True(t, ok)
	assert.Same(t, customTransport, httpClient.Transport)
	assert.Equal(t, 3*time.Second, httpClient.Timeout)
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

func TestWithHTTPClientTimeout(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientTimeout(5 * time.Second)(options)

	assert.Equal(t, 5*time.Second, options.Timeout)
}

func TestHTTPClientOptions_Merge(t *testing.T) {
	customTransport := &http.Transport{}
	options := &HTTPClientOptions{}

	WithHTTPClientName("merged-client")(options)
	WithHTTPClientTransport(customTransport)(options)
	WithHTTPClientTimeout(7 * time.Second)(options)

	assert.Equal(t, "merged-client", options.Name)
	assert.Same(t, customTransport, options.Transport)
	assert.Equal(t, 7*time.Second, options.Timeout)
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
		WithHTTPClientTimeout(time.Second),
	}

	for _, opt := range opts {
		opt(options)
	}

	assert.Equal(t, "complete-test", options.Name)
	assert.Same(t, http.DefaultTransport, options.Transport)
	assert.Equal(t, time.Second, options.Timeout)
}

func TestEdgeCases(t *testing.T) {
	options := &HTTPClientOptions{}
	WithHTTPClientName("")(options)
	assert.Empty(t, options.Name)

	options = &HTTPClientOptions{}
	WithHTTPClientTransport(nil)(options)
	assert.Nil(t, options.Transport)

	options = &HTTPClientOptions{}
	WithHTTPClientTimeout(0)(options)
	assert.Zero(t, options.Timeout)

	client := DefaultNewHTTPClient()
	require.NotNil(t, client)
}

func TestMultipleOptionApplications(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientName("first")(options)
	WithHTTPClientName("second")(options)
	WithHTTPClientName("third")(options)
	WithHTTPClientTimeout(time.Second)(options)
	WithHTTPClientTimeout(2 * time.Second)(options)

	assert.Equal(t, "third", options.Name)
	assert.Equal(t, 2*time.Second, options.Timeout)
}
