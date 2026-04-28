//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package mem0 provides an ingest-first integration with mem0.ai.
package mem0

import (
	"net/http"
	"time"
)

const (
	defaultHost             = "https://api.mem0.ai"
	defaultTimeout          = 10 * time.Second
	defaultAsyncMemoryNum   = 1
	defaultMemoryQueueSize  = 10
	defaultMemoryJobTimeout = 30 * time.Second
)

type serviceOpts struct {
	host   string
	apiKey string

	orgID     string
	projectID string

	asyncMode bool
	version   string

	timeout time.Duration
	client  *http.Client

	loadToolEnabled bool

	asyncMemoryNum   int
	memoryQueueSize  int
	memoryJobTimeout time.Duration
}

func (o serviceOpts) clone() serviceOpts {
	return o
}

var defaultOptions = serviceOpts{
	host:             defaultHost,
	asyncMode:        true,
	version:          "v2",
	timeout:          defaultTimeout,
	asyncMemoryNum:   defaultAsyncMemoryNum,
	memoryQueueSize:  defaultMemoryQueueSize,
	memoryJobTimeout: defaultMemoryJobTimeout,
}

// ServiceOpt configures a mem0 service.
type ServiceOpt func(*serviceOpts)

// WithHost sets the mem0 API host or base URL.
func WithHost(host string) ServiceOpt {
	return func(opts *serviceOpts) {
		if host != "" {
			opts.host = host
		}
	}
}

// WithAPIKey sets the mem0 API key used for all requests.
func WithAPIKey(apiKey string) ServiceOpt {
	return func(opts *serviceOpts) {
		if apiKey != "" {
			opts.apiKey = apiKey
		}
	}
}

// WithOrgProject sets optional mem0 organization and project identifiers.
func WithOrgProject(orgID, projectID string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.orgID = orgID
		opts.projectID = projectID
	}
}

// WithAsyncMode controls whether mem0 ingest requests are async.
func WithAsyncMode(async bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.asyncMode = async
	}
}

// WithVersion sets the mem0 ingestion API version for create requests.
func WithVersion(version string) ServiceOpt {
	return func(opts *serviceOpts) {
		if version != "" {
			opts.version = version
		}
	}
}

// WithTimeout sets the HTTP timeout for mem0 requests.
func WithTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout > 0 {
			opts.timeout = timeout
		}
	}
}

// WithHTTPClient injects a custom HTTP client for mem0 requests.
func WithHTTPClient(c *http.Client) ServiceOpt {
	return func(opts *serviceOpts) {
		if c != nil {
			opts.client = c
		}
	}
}

// WithLoadToolEnabled controls whether memory_load is exposed in Tools().
func WithLoadToolEnabled(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.loadToolEnabled = enabled
	}
}

// WithAsyncMemoryNum sets the number of async mem0 ingestion workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num > 0 {
			opts.asyncMemoryNum = num
		}
	}
}

// WithMemoryQueueSize sets the queue size for async mem0 ingestion jobs.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size > 0 {
			opts.memoryQueueSize = size
		}
	}
}

// WithMemoryJobTimeout sets the timeout applied to each ingest job. This
// governs both queued async worker jobs and the synchronous fallback path
// when the queue is full.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		if timeout > 0 {
			opts.memoryJobTimeout = timeout
		}
	}
}
