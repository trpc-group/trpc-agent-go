//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tencentdb integrates with TencentDB Agent Memory through its
// local gateway sidecar.
package tencentdb

import (
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultGatewayURL       = "http://127.0.0.1:8420"
	defaultTimeout          = 5 * time.Second
	defaultMaxBodyBytes     = 10 << 20
	defaultIngestWorkers    = 1
	defaultIngestQueueSize  = 10
	defaultIngestJobTimeout = 30 * time.Second
)

// SessionKeyFunc maps a framework session to the TencentDB Agent Memory
// session_key. The default avoids collisions across app/user/session IDs but
// does not provide strong multi-tenant isolation in a shared sidecar.
type SessionKeyFunc func(*session.Session) string

// Options configures Service.
type Options struct {
	GatewayURL       string
	Timeout          time.Duration
	HTTPClient       *http.Client
	MaxBodyBytes     int64
	IngestWorkers    int
	IngestQueueSize  int
	IngestJobTimeout time.Duration

	SessionKeyFunc SessionKeyFunc

	RecallEnabled                bool
	EnableConversationSearchTool bool
	EnableStandardAliases        bool
	ToolPrefix                   string
}

// Option configures Service.
type Option func(*Options)

func defaultOptions() Options {
	return Options{
		GatewayURL:                   defaultGatewayURL,
		Timeout:                      defaultTimeout,
		MaxBodyBytes:                 defaultMaxBodyBytes,
		IngestWorkers:                defaultIngestWorkers,
		IngestQueueSize:              defaultIngestQueueSize,
		IngestJobTimeout:             defaultIngestJobTimeout,
		SessionKeyFunc:               defaultSessionKey,
		RecallEnabled:                true,
		EnableConversationSearchTool: true,
		ToolPrefix:                   "tdai",
	}
}

// WithGatewayURL sets the TencentDB Agent Memory gateway URL.
func WithGatewayURL(url string) Option {
	return func(o *Options) {
		if url != "" {
			o.GatewayURL = url
		}
	}
}

// WithTimeout sets the request timeout used by the gateway client.
func WithTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.Timeout = timeout
		}
	}
}

// WithHTTPClient injects a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(o *Options) {
		if client != nil {
			o.HTTPClient = client
		}
	}
}

// WithMaxBodyBytes limits gateway response bodies.
func WithMaxBodyBytes(max int64) Option {
	return func(o *Options) {
		if max > 0 {
			o.MaxBodyBytes = max
		}
	}
}

// WithIngestWorkers sets the number of async capture workers.
func WithIngestWorkers(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.IngestWorkers = n
		}
	}
}

// WithIngestQueueSize sets the per-worker capture queue size.
func WithIngestQueueSize(size int) Option {
	return func(o *Options) {
		if size > 0 {
			o.IngestQueueSize = size
		}
	}
}

// WithIngestJobTimeout sets the timeout applied to queued capture jobs.
func WithIngestJobTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.IngestJobTimeout = timeout
		}
	}
}

// WithSessionKeyFunc overrides the session_key mapping.
func WithSessionKeyFunc(fn SessionKeyFunc) Option {
	return func(o *Options) {
		if fn != nil {
			o.SessionKeyFunc = fn
		}
	}
}

// WithRecallEnabled controls whether Plugin performs automatic recall.
func WithRecallEnabled(enabled bool) Option {
	return func(o *Options) {
		o.RecallEnabled = enabled
	}
}

// WithConversationSearchTool controls whether tdai_conversation_search is exposed.
func WithConversationSearchTool(enabled bool) Option {
	return func(o *Options) {
		o.EnableConversationSearchTool = enabled
	}
}

// WithStandardAliases exposes memory_search as an additional alias.
//
// This can conflict with the framework's built-in memory tools, so it is off by
// default. TencentDB-native tdai_* names are always preferred.
func WithStandardAliases(enabled bool) Option {
	return func(o *Options) {
		o.EnableStandardAliases = enabled
	}
}

// WithToolPrefix changes the native tool name prefix. The default prefix "tdai"
// yields tdai_memory_search and tdai_conversation_search.
func WithToolPrefix(prefix string) Option {
	return func(o *Options) {
		if prefix != "" {
			o.ToolPrefix = prefix
		}
	}
}
