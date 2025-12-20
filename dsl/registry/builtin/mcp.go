// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package builtin

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/mcpconfig"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func init() {
	registry.MustRegister(&MCPComponent{})
}

// MCPComponent is a built-in component for invoking an MCP server tool.
//
// NOTE: This component is handled specially by the compiler (createMCPNodeFunc).
// Execute should not be called directly.
type MCPComponent struct{}

func (c *MCPComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.mcp",
		DisplayName: "MCP Node",
		Description: "Call a Model Context Protocol server/tool",
		Category:    "Tools",
		Version:     "1.0.0",
		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "server_url",
				DisplayName: "Server URL",
				Description: "MCP server URL (e.g., https://mcp.example.com/mcp).",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
			},
			{
				Name:        "tool",
				DisplayName: "Tool",
				Description: "MCP tool name to invoke (as declared by the MCP server).",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
			},
			{
				Name:        "transport",
				DisplayName: "Transport",
				Description: "Transport mechanism for MCP (streamable_http or sse).",
				Type:        "string",
				TypeID:      "string",
				Kind:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Default:     mcpconfig.TransportStreamableHTTP,
			},
			{
				Name:        "headers",
				DisplayName: "Headers",
				Description: "Optional HTTP headers to include when connecting to the MCP server.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "input_schema",
				DisplayName: "Input Schema",
				Description: "Optional JSON Schema snapshot of the tool input for editor hints.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "output_schema",
				DisplayName: "Output Schema",
				Description: "Optional JSON Schema for the normalized MCP tool output exposed via node_structured.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
			{
				Name:        "params",
				DisplayName: "Params",
				Description: "Optional per-parameter CEL expressions mapping input.* to MCP tool arguments.",
				Type:        "map[string]any",
				TypeID:      "object",
				Kind:        "object",
				GoType:      reflect.TypeOf(map[string]any{}),
				Required:    false,
			},
		},
	}
}

func (c *MCPComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	return nil, fmt.Errorf("builtin.mcp.Execute should not be called directly - component is handled by compiler")
}

func (c *MCPComponent) Validate(config registry.ComponentConfig) error {
	_, err := mcpconfig.ParseNodeConfig(map[string]any(config))
	return err
}
