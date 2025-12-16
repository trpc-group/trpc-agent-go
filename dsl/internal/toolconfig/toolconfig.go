//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package toolconfig provides helpers for parsing common "tools" / "mcp_tools"
// config blocks used by DSL components such as builtin.llmagent.
package toolconfig

import (
	"fmt"
	"strings"
)

const (
	MCPTransportStreamableHTTP = "streamable_http"
	MCPTransportSSE            = "sse"
)

type MCPToolSpec struct {
	ServerURL    string
	Transport    string
	ServerLabel  string
	AllowedTools []string
	Headers      map[string]string
}

// ParseStringSlice validates that value is an array of strings and returns a
// trimmed slice. Empty strings are dropped.
func ParseStringSlice(value any, path string) ([]string, error) {
	switch v := value.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			out = append(out, item)
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a string", path, i)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an array", path)
	}
}

// ParseMCPTools parses and validates the builtin.llmagent mcp_tools block.
func ParseMCPTools(value any) ([]MCPToolSpec, error) {
	var list []any
	switch v := value.(type) {
	case []any:
		list = v
	case []map[string]any:
		list = make([]any, 0, len(v))
		for _, item := range v {
			list = append(list, item)
		}
	default:
		return nil, fmt.Errorf("mcp_tools must be an array")
	}

	out := make([]MCPToolSpec, 0, len(list))
	for i, item := range list {
		itemMap, ok := item.(map[string]any)
		if !ok || itemMap == nil {
			return nil, fmt.Errorf("mcp_tools[%d] must be an object", i)
		}

		serverURL, _ := itemMap["server_url"].(string)
		serverURL = strings.TrimSpace(serverURL)
		if serverURL == "" {
			return nil, fmt.Errorf("mcp_tools[%d].server_url is required and must be a non-empty string", i)
		}

		transport := MCPTransportStreamableHTTP
		if rawTransport, ok := itemMap["transport"]; ok && rawTransport != nil {
			t, ok := rawTransport.(string)
			if !ok {
				return nil, fmt.Errorf("mcp_tools[%d].transport must be a string when present", i)
			}
			t = strings.TrimSpace(t)
			if t != "" {
				transport = t
			}
		}
		if transport != MCPTransportStreamableHTTP && transport != MCPTransportSSE {
			return nil, fmt.Errorf("mcp_tools[%d].transport must be one of: streamable_http, sse", i)
		}

		var serverLabel string
		if rawLabel, ok := itemMap["server_label"]; ok && rawLabel != nil {
			label, ok := rawLabel.(string)
			if !ok {
				return nil, fmt.Errorf("mcp_tools[%d].server_label must be a string when present", i)
			}
			serverLabel = strings.TrimSpace(label)
		}

		var allowedTools []string
		if rawAllowed, ok := itemMap["allowed_tools"]; ok && rawAllowed != nil {
			parsed, err := ParseStringSlice(rawAllowed, fmt.Sprintf("mcp_tools[%d].allowed_tools", i))
			if err != nil {
				return nil, err
			}
			if len(parsed) > 0 {
				allowedTools = parsed
			}
		}

		headers, err := ParseStringMap(itemMap["headers"], fmt.Sprintf("mcp_tools[%d].headers", i))
		if err != nil {
			return nil, err
		}

		out = append(out, MCPToolSpec{
			ServerURL:    serverURL,
			Transport:    transport,
			ServerLabel:  serverLabel,
			AllowedTools: allowedTools,
			Headers:      headers,
		})
	}

	return out, nil
}

func ParseStringMap(value any, path string) (map[string]string, error) {
	if value == nil {
		return nil, nil
	}

	switch m := value.(type) {
	case map[string]string:
		if len(m) == 0 {
			return nil, nil
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			out[k] = v
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	case map[string]any:
		if len(m) == 0 {
			return nil, nil
		}
		out := make(map[string]string, len(m))
		for k, raw := range m {
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%q] must be a string", path, k)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out[k] = s
		}
		if len(out) == 0 {
			return nil, nil
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an object", path)
	}
}
