//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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

	FilterModeInclude filterMode = "include" // Only include listed tools
	FilterModeExclude filterMode = "exclude" // Exclude listed tools
)

// Default configurations.
var (
	defaultClientInfo = mcp.Implementation{
		Name:    "trpc-agent-go",
		Version: "1.0.0",
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

// toolSetConfig holds internal configuration for ToolSet.
type toolSetConfig struct {
	connectionConfig ConnectionConfig
	toolFilter       ToolFilter
	mcpOptions       []mcp.ClientOption // MCP client options.
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
