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
	"time"
)

const (
	// defaultHTTPClientTimeout is the default timeout for HTTP clients
	// created by DefaultNewHTTPClient. This prevents goroutine leaks when
	// a server becomes unresponsive and no caller-specified context deadline
	// is set. Users can override this via WithHTTPClientTimeout.
	defaultHTTPClientTimeout = 5 * time.Minute
)

// HTTPClient is the interface for the HTTP client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClientNewFunc is the function type for creating a new HTTP client.
type HTTPClientNewFunc func(opts ...HTTPClientOption) HTTPClient

// DefaultNewHTTPClient is the default HTTP client for Anthropic.
var DefaultNewHTTPClient HTTPClientNewFunc = func(opts ...HTTPClientOption) HTTPClient {
	options := &HTTPClientOptions{}
	for _, opt := range opts {
		opt(options)
	}
	timeout := options.Timeout
	if timeout == 0 && !options.DisableTimeout {
		timeout = defaultHTTPClientTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: options.Transport,
	}
}

// HTTPClientOption is the option for the HTTP client.
type HTTPClientOption func(*HTTPClientOptions)

// WithHTTPClientName is the option for the HTTP client name.
func WithHTTPClientName(name string) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Name = name
	}
}

// WithHTTPClientTransport is the option for the HTTP client transport.
func WithHTTPClientTransport(transport http.RoundTripper) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Transport = transport
	}
}

// WithHTTPClientTimeout sets the timeout for the HTTP client.
// Use 0 to explicitly disable the timeout (not recommended for production).
// If not called, DefaultNewHTTPClient applies a 5-minute default timeout.
func WithHTTPClientTimeout(timeout time.Duration) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Timeout = timeout
		options.DisableTimeout = timeout == 0
	}
}

// HTTPClientOptions is the options for the HTTP client.
type HTTPClientOptions struct {
	// Name is the name of the HTTP client, used for identification and logging.
	Name string

	// Transport is the custom HTTP transport to use. If nil, the default transport is used.
	Transport http.RoundTripper

	// Timeout is the timeout for the HTTP client. A zero value with DisableTimeout
	// set to false causes DefaultNewHTTPClient to apply the default 5-minute timeout.
	Timeout time.Duration

	// DisableTimeout indicates whether to explicitly disable the HTTP client timeout.
	// When true, the client will have no timeout regardless of the Timeout field value.
	DisableTimeout bool
}
