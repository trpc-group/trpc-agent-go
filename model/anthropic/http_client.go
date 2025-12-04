//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package anthropic provides Anthropic-compatible model implementations.
package anthropic

import (
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	// WithHTTPClientName is the option for the HTTP client name.
	WithHTTPClientName = model.WithHTTPClientName
	// WithHTTPClientTransport is the option for the HTTP client transport.
	WithHTTPClientTransport = model.WithHTTPClientTransport
)

// HTTPClientOption is the option for the HTTP client.
type HTTPClientOption = model.HTTPClientOption

// HTTPClientOptions is the options for the HTTP client.
type HTTPClientOptions = model.HTTPClientOptions
