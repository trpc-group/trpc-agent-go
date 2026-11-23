//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acp

import (
	"context"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	internaltool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolmcp "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

const defaultMCPTimeout = 30 * time.Second

type mcpServerConfig struct {
	stdio *acpsdk.McpServerStdio
	name  string
}

func initializeMCPServers(
	ctx context.Context,
	servers []acpsdk.McpServer,
) ([]tool.Tool, []tool.ToolSet, error) {
	log.DebugContext(ctx, "Initializing ACP MCP servers", "server_count", len(servers))
	configs := make([]mcpServerConfig, 0, len(servers))
	usedNames := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		stdio, normalizedName, err := validateMCPServer(server, usedNames)
		if err != nil {
			return nil, nil, err
		}
		configs = append(configs, mcpServerConfig{
			stdio: stdio,
			name:  normalizedName,
		})
	}

	var tools []tool.Tool
	var toolSets []tool.ToolSet
	for _, config := range configs {
		log.DebugContext(
			ctx,
			"Starting ACP stdio MCP server",
			"mcp_server",
			config.name,
		)
		toolSet := toolmcp.NewMCPToolSet(
			toolmcp.ConnectionConfig{
				Transport: "stdio",
				Command:   config.stdio.Command,
				Args:      append([]string(nil), config.stdio.Args...),
				Env:       mcpEnv(config.stdio.Env),
				Timeout:   defaultMCPTimeout,
			},
			toolmcp.WithName(config.name),
		)
		toolSets = append(toolSets, toolSet)
		if err := toolSet.Init(ctx); err != nil {
			log.WarnContext(
				ctx,
				"Failed to initialize ACP MCP server",
				"mcp_server",
				config.name,
				"error",
				err,
			)
			closeMCPServers(ctx, toolSets)
			return nil, nil, acpsdk.NewInternalError(map[string]any{
				"mcpServer": config.name,
				"reason":    "failed to initialize stdio MCP server",
			})
		}
		serverTools := internaltool.NewNamedToolSet(toolSet).Tools(ctx)
		tools = append(tools, serverTools...)
		log.DebugContext(
			ctx,
			"ACP MCP server initialized",
			"mcp_server",
			config.name,
			"tool_count",
			len(serverTools),
		)
	}
	log.DebugContext(
		ctx,
		"ACP MCP server initialization completed",
		"server_count",
		len(toolSets),
		"tool_count",
		len(tools),
	)
	return tools, toolSets, nil
}

func validateMCPServer(
	server acpsdk.McpServer,
	usedNames map[string]struct{},
) (*acpsdk.McpServerStdio, string, error) {
	if server.Stdio == nil {
		return nil, "", acpsdk.NewInvalidParams(map[string]any{
			"mcpServers": "only stdio MCP servers are supported",
		})
	}
	if server.Http != nil || server.Sse != nil || server.Acp != nil {
		return nil, "", acpsdk.NewInvalidParams(map[string]any{
			"mcpServers": "exactly one MCP transport must be specified",
		})
	}
	stdio := server.Stdio
	if strings.TrimSpace(stdio.Name) == "" {
		return nil, "", acpsdk.NewInvalidParams(map[string]any{
			"mcpServers": "MCP server name is required",
		})
	}
	if strings.TrimSpace(stdio.Command) == "" {
		return nil, "", acpsdk.NewInvalidParams(map[string]any{
			"mcpServer": stdio.Name,
			"command":   "MCP server command is required",
		})
	}
	for _, env := range stdio.Env {
		if strings.TrimSpace(env.Name) == "" {
			return nil, "", acpsdk.NewInvalidParams(map[string]any{
				"mcpServer": stdio.Name,
				"env":       "environment variable name is required",
			})
		}
	}
	normalizedName, err := normalizeMCPServerName(stdio.Name)
	if err != nil {
		return nil, "", err
	}
	if _, exists := usedNames[normalizedName]; exists {
		return nil, "", acpsdk.NewInvalidParams(map[string]any{
			"mcpServer": stdio.Name,
			"name":      "MCP server name conflicts after normalization",
		})
	}
	usedNames[normalizedName] = struct{}{}
	return stdio, normalizedName, nil
}

func normalizeMCPServerName(name string) (string, error) {
	var normalized strings.Builder
	hasAlphaNumeric := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z':
			normalized.WriteRune(char)
			hasAlphaNumeric = true
		case char >= '0' && char <= '9':
			normalized.WriteRune(char)
			hasAlphaNumeric = true
		case char == '_':
			normalized.WriteRune(char)
		default:
			normalized.WriteByte('_')
		}
	}
	if !hasAlphaNumeric {
		return "", acpsdk.NewInvalidParams(map[string]any{
			"mcpServer": name,
			"name":      "MCP server name must contain a letter or digit",
		})
	}
	return normalized.String(), nil
}

func mcpEnv(env []acpsdk.EnvVariable) map[string]string {
	if len(env) == 0 {
		return nil
	}
	result := make(map[string]string, len(env))
	for _, variable := range env {
		result[variable.Name] = variable.Value
	}
	return result
}

func closeMCPServers(ctx context.Context, toolSets []tool.ToolSet) {
	for index := len(toolSets) - 1; index >= 0; index-- {
		if err := toolSets[index].Close(); err != nil {
			log.WarnContext(
				ctx,
				"Failed to close ACP MCP server",
				"mcp_server",
				toolSets[index].Name(),
				"error",
				err,
			)
			continue
		}
		log.DebugContext(
			ctx,
			"Closed ACP MCP server",
			"mcp_server",
			toolSets[index].Name(),
		)
	}
}
