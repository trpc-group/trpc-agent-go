//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package mcpconfig provides helpers for parsing and validating MCP-related
// configuration blocks shared across DSL compiler and validator paths.
package mcpconfig

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/toolconfig"
)

type NodeConfig struct {
	ServerURL string
	ToolName  string
	Transport string
	Headers   map[string]string

	InputSchema  map[string]any
	OutputSchema map[string]any
	Params       map[string]any
}

func ParseNodeConfig(config map[string]any) (NodeConfig, error) {
	var out NodeConfig

	rawServerURL, ok := config["server_url"].(string)
	serverURL := strings.TrimSpace(rawServerURL)
	if !ok || serverURL == "" {
		return out, fmt.Errorf("server_url is required in MCP node config")
	}
	out.ServerURL = serverURL

	rawTool, ok := config["tool"].(string)
	toolName := strings.TrimSpace(rawTool)
	if !ok || toolName == "" {
		return out, fmt.Errorf("tool is required in MCP node config")
	}
	out.ToolName = toolName

	transport := toolconfig.MCPTransportStreamableHTTP
	if transportRaw, ok := config["transport"]; ok && transportRaw != nil {
		t, ok := transportRaw.(string)
		if !ok {
			return out, fmt.Errorf("transport must be a string when present")
		}
		t = strings.TrimSpace(t)
		if t != "" {
			transport = t
		}
	}
	if transport != toolconfig.MCPTransportStreamableHTTP && transport != toolconfig.MCPTransportSSE {
		return out, fmt.Errorf("unsupported MCP transport %q; expected %q or %q", transport, toolconfig.MCPTransportStreamableHTTP, toolconfig.MCPTransportSSE)
	}
	out.Transport = transport

	headers, err := toolconfig.ParseStringMap(config["headers"], "headers")
	if err != nil {
		return out, err
	}
	out.Headers = headers

	if rawSchema, ok := config["input_schema"]; ok && rawSchema != nil {
		schema, ok := rawSchema.(map[string]any)
		if !ok {
			return out, fmt.Errorf("input_schema must be an object when present")
		}
		out.InputSchema = schema
	}

	if rawSchema, ok := config["output_schema"]; ok && rawSchema != nil {
		schema, ok := rawSchema.(map[string]any)
		if !ok {
			return out, fmt.Errorf("output_schema must be an object when present")
		}
		out.OutputSchema = schema
	}

	if rawParams, ok := config["params"]; ok && rawParams != nil {
		params, ok := rawParams.(map[string]any)
		if !ok {
			return out, fmt.Errorf("params must be an object when present")
		}
		for name, raw := range params {
			exprMap, ok := raw.(map[string]any)
			if !ok {
				return out, fmt.Errorf("params[%q] must be an object", name)
			}
			if expr, ok := exprMap["expression"]; ok && expr != nil {
				if _, ok := expr.(string); !ok {
					return out, fmt.Errorf("params[%q].expression must be a string when present", name)
				}
			}
			if format, ok := exprMap["format"]; ok && format != nil {
				if _, ok := format.(string); !ok {
					return out, fmt.Errorf("params[%q].format must be a string when present", name)
				}
			}
		}
		out.Params = params
	}

	return out, nil
}
