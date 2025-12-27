//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

// Default connection settings.
const (
	defaultHost = "localhost"
	defaultPort = 6334
)

// ClientBuilderOpt is a functional option for configuring the Qdrant client.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts holds the configuration options for creating a Qdrant client.
type ClientBuilderOpts struct {
	Host   string
	Port   int
	APIKey string
	UseTLS bool
}

// WithHost sets the Qdrant server host.
func WithHost(host string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if host != "" {
			o.Host = host
		}
	}
}

// WithPort sets the Qdrant server gRPC port.
func WithPort(port int) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		if port > 0 && port <= 65535 {
			o.Port = port
		}
	}
}

// WithAPIKey sets the API key for Qdrant Cloud authentication.
func WithAPIKey(apiKey string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.APIKey = apiKey
	}
}

// WithTLS enables TLS for secure connections (required for Qdrant Cloud).
func WithTLS(enabled bool) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) {
		o.UseTLS = enabled
	}
}
