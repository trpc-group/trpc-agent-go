//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import "net/http"

// Option configures a tRPC-Agent API runner.
type Option func(*options)

type options struct {
	target            string
	basePath          string
	httpClient        HTTPClient
	httpClientOptions []HTTPClientOption
	headers           http.Header
}

// WithTarget sets the tRPC-Agent API service target.
func WithTarget(target string) Option {
	return func(opts *options) {
		opts.target = target
	}
}

// WithBasePath sets the tRPC-Agent API base path.
func WithBasePath(basePath string) Option {
	return func(opts *options) {
		opts.basePath = basePath
	}
}

// WithHTTPClient sets the HTTP client used by the runner.
// Custom transports may resolve non-http targets such as service discovery schemes.
func WithHTTPClient(client *http.Client) Option {
	return func(opts *options) {
		opts.httpClient = client
	}
}

// WithHTTPClientOptions sets options for the default HTTP client builder.
func WithHTTPClientOptions(httpOpts ...HTTPClientOption) Option {
	return func(opts *options) {
		opts.httpClientOptions = httpOpts
	}
}

// WithHeader sets one HTTP header on every tRPC-Agent API request.
func WithHeader(key string, value string) Option {
	return func(opts *options) {
		if key == "" {
			return
		}
		if opts.headers == nil {
			opts.headers = make(http.Header)
		}
		opts.headers.Set(key, value)
	}
}

func newOptions(opts ...Option) options {
	options := options{
		basePath: defaultBasePath,
		headers:  make(http.Header),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.httpClient == nil {
		options.httpClient = DefaultNewHTTPClient(options.httpClientOptions...)
	}
	return options
}
