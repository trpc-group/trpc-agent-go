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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/toolconfig"
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
				Default:     toolconfig.MCPTransportStreamableHTTP,
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
	rawServerURL, ok := config["server_url"].(string)
	serverURL := strings.TrimSpace(rawServerURL)
	if !ok || serverURL == "" {
		return fmt.Errorf("server_url is required in MCP node config")
	}

	rawTool, ok := config["tool"].(string)
	toolName := strings.TrimSpace(rawTool)
	if !ok || toolName == "" {
		return fmt.Errorf("tool is required in MCP node config")
	}

	if transportRaw, ok := config["transport"]; ok && transportRaw != nil {
		transport, ok := transportRaw.(string)
		if !ok {
			return fmt.Errorf("transport must be a string when present")
		}
		transport = strings.TrimSpace(transport)
		if transport != "" && transport != toolconfig.MCPTransportStreamableHTTP && transport != toolconfig.MCPTransportSSE {
			return fmt.Errorf("unsupported MCP transport %q; expected %q or %q", transport, toolconfig.MCPTransportStreamableHTTP, toolconfig.MCPTransportSSE)
		}
	}

	if headersRaw, ok := config["headers"]; ok && headersRaw != nil {
		switch h := headersRaw.(type) {
		case map[string]any:
			for k, v := range h {
				if _, ok := v.(string); !ok {
					return fmt.Errorf("headers[%q] must be a string", k)
				}
			}
		case map[string]string:
			// ok
		default:
			return fmt.Errorf("headers must be an object")
		}
	}

	if rawSchema, ok := config["input_schema"]; ok && rawSchema != nil {
		if _, ok := rawSchema.(map[string]any); !ok {
			return fmt.Errorf("input_schema must be an object when present")
		}
	}

	if rawSchema, ok := config["output_schema"]; ok && rawSchema != nil {
		if _, ok := rawSchema.(map[string]any); !ok {
			return fmt.Errorf("output_schema must be an object when present")
		}
	}

	if rawParams, ok := config["params"]; ok && rawParams != nil {
		params, ok := rawParams.(map[string]any)
		if !ok {
			return fmt.Errorf("params must be an object when present")
		}
		for name, raw := range params {
			exprMap, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("params[%q] must be an object", name)
			}
			if expr, ok := exprMap["expression"]; ok && expr != nil {
				if _, ok := expr.(string); !ok {
					return fmt.Errorf("params[%q].expression must be a string when present", name)
				}
			}
		}
	}

	return nil
}
