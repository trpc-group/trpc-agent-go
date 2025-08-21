//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"fmt"
	"time"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// filterMode defines how the filter should behave.
type filterMode string

// transport specifies the transport method: "stdio", "sse", "streamable_http".
type transport string

const (
	// transportStdio is the stdio transport.
	transportStdio transport = "stdio"
	// transportSSE is the Server-Sent Events transport.
	transportSSE transport = "sse"
	// transportStreamable is the streamable HTTP transport.
	transportStreamable transport = "streamable"

	// FilterModeInclude specifies that only listed tools should be included.
	FilterModeInclude filterMode = "include"
	// FilterModeExclude specifies that listed tools should be excluded.
	FilterModeExclude filterMode = "exclude"
)

// Default configurations.
var (
	defaultClientInfo = mcp.Implementation{
		Name:    "trpc-agent-go",
		Version: "1.0.0",
	}

	// defaultRetryConfig provides sensible defaults for retry configuration.
	// Uses industry standard values: simple and conservative settings.
	defaultRetryConfig = RetryConfig{
		MaxRetries:     2,                      // Conservative retry count
		InitialBackoff: 500 * time.Millisecond, // 0.5s initial delay
		BackoffFactor:  2.0,                    // Standard exponential backoff
		MaxBackoff:     8 * time.Second,        // Maximum delay cap
	}
)

// ConnectionConfig defines the configuration for connecting to an MCP server.
type ConnectionConfig struct {
	// Transport specifies the transport method: "stdio", "sse", "streamable".
	Transport string `json:"transport"`

	// Streamable/SSE configuration.
	ServerURL string            `json:"server_url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`

	// STDIO configuration.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// Common configuration.
	Timeout time.Duration `json:"timeout,omitempty"`

	// Advanced configuration.
	ClientInfo mcp.Implementation `json:"client_info,omitempty"`
}

// RetryConfig defines configuration for MCP tool call retry behavior.
type RetryConfig struct {
	// MaxRetries specifies the maximum number of retry attempts for tool calls.
	MaxRetries int `json:"max_retries"`

	// InitialBackoff specifies the initial backoff duration before the first retry.
	InitialBackoff time.Duration `json:"initial_backoff"`

	// BackoffFactor specifies the factor to multiply the backoff duration for each retry.
	// For example, with factor 2.0: 100ms -> 200ms -> 400ms -> 800ms
	BackoffFactor float64 `json:"backoff_factor"`

	// MaxBackoff specifies the maximum backoff duration to cap exponential growth.
	MaxBackoff time.Duration `json:"max_backoff"`
}

// toolSetConfig holds internal configuration for ToolSet.
type toolSetConfig struct {
	connectionConfig ConnectionConfig
	toolFilter       ToolFilter
	mcpOptions       []mcp.ClientOption // MCP client options.
	retryConfig      *RetryConfig       // Retry configuration for tool calls.
}

// ToolSetOption is a function type for configuring ToolSet.
type ToolSetOption func(*toolSetConfig)

// WithToolFilter configures tool filtering.
func WithToolFilter(filter ToolFilter) ToolSetOption {
	return func(c *toolSetConfig) {
		c.toolFilter = filter
	}
}

// WithMCPOptions sets additional MCP client options.
// This can be used to pass options to the underlying MCP client.
func WithMCPOptions(options ...mcp.ClientOption) ToolSetOption {
	return func(c *toolSetConfig) {
		c.mcpOptions = append(c.mcpOptions, options...)
	}
}

// WithSimpleRetry configures simple retry behavior with default settings.
// This is the recommended way to enable basic retry functionality.
// maxRetries must be between 0 and 10 (inclusive).
//
// Example:
//
//	toolSet := mcp.NewMCPToolSet(
//	    config,
//	    mcp.WithSimpleRetry(3), // Retry up to 3 times
//	)
func WithSimpleRetry(maxRetries int) ToolSetOption {
	return func(c *toolSetConfig) {
		// Validate input to prevent unreasonable values.
		if maxRetries < 0 {
			maxRetries = 0
		} else if maxRetries > 10 {
			maxRetries = 10
		}

		// Create new config based on defaults - avoid unnecessary copying
		config := defaultRetryConfig
		config.MaxRetries = maxRetries
		c.retryConfig = &config
	}
}

// WithRetry configures retry behavior with custom settings.
// Use this for advanced retry configuration requirements.
// All parameters are validated and clamped to reasonable ranges.
//
// Example:
//
//	toolSet := mcp.NewMCPToolSet(
//	    config,
//	    mcp.WithRetry(mcp.RetryConfig{
//	        MaxRetries:      5,
//	        InitialBackoff:  200 * time.Millisecond,
//	        BackoffFactor:   1.5,
//	        MaxBackoff:      10 * time.Second,
//	    }),
//	)
func WithRetry(config RetryConfig) ToolSetOption {
	return func(c *toolSetConfig) {
		// Validate and sanitize config values to prevent unreasonable settings.
		validated := validateRetryConfig(config)
		c.retryConfig = &validated
	}
}

// validateRetryConfig validates and sanitizes retry configuration values.
func validateRetryConfig(config RetryConfig) RetryConfig {
	validated := config

	// Validate MaxRetries: 0-10 range.
	if validated.MaxRetries < 0 {
		validated.MaxRetries = 0
	} else if validated.MaxRetries > 10 {
		validated.MaxRetries = 10
	}

	// Validate InitialBackoff: 1ms-30s range.
	if validated.InitialBackoff < time.Millisecond {
		validated.InitialBackoff = time.Millisecond
	} else if validated.InitialBackoff > 30*time.Second {
		validated.InitialBackoff = 30 * time.Second
	}

	// Validate BackoffFactor: 1.0-10.0 range.
	if validated.BackoffFactor < 1.0 {
		validated.BackoffFactor = 1.0
	} else if validated.BackoffFactor > 10.0 {
		validated.BackoffFactor = 10.0
	}

	// Validate MaxBackoff: InitialBackoff-5min range.
	minMaxBackoff := validated.InitialBackoff
	maxMaxBackoff := 5 * time.Minute
	if validated.MaxBackoff < minMaxBackoff {
		validated.MaxBackoff = minMaxBackoff
	} else if validated.MaxBackoff > maxMaxBackoff {
		validated.MaxBackoff = maxMaxBackoff
	}

	return validated
}

// validateTransport validates the transport string and returns the internal transport type.
func validateTransport(t string) (transport, error) {
	switch t {
	case "stdio":
		return transportStdio, nil
	case "sse":
		return transportSSE, nil
	case "streamable", "streamable_http":
		return transportStreamable, nil
	default:
		return "", fmt.Errorf("unsupported transport: %s, supported: stdio, sse, streamable", t)
	}
}
