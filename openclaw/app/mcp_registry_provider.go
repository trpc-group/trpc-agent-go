//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/mcpregistry"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcpbroker"
)

const toolProviderMCPRegistry = "mcp_registry"

type mcpRegistryProviderConfig struct {
	Dir            string `yaml:"dir,omitempty"`
	AllowAdHocHTTP bool   `yaml:"allow_adhoc_http,omitempty"`
}

func init() {
	must(registry.RegisterToolProvider(
		toolProviderMCPRegistry,
		newMCPRegistryTools,
	))
}

func newMCPRegistryTools(
	deps registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg mcpRegistryProviderConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	dir := strings.TrimSpace(cfg.Dir)
	if dir == "" {
		dir = mcpregistry.DefaultDir(deps.StateDir)
	}
	store := mcpregistry.NewFileStore(dir)
	broker := mcpbroker.New(
		mcpbroker.WithAllowAdHocHTTP(cfg.AllowAdHocHTTP),
		mcpbroker.WithServerResolver(func(
			ctx context.Context,
		) (map[string]mcp.ConnectionConfig, error) {
			runtime, err := mcpregistry.RuntimeContextFromContext(
				ctx,
				deps.AppName,
			)
			if err != nil {
				return nil, err
			}
			return store.ServerConfigs(ctx, runtime)
		}),
	)

	tools := mcpregistry.NewTools(store, deps.AppName)
	tools = append(tools, broker.Tools()...)
	return tools, nil
}
