//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package echotool registers a tiny tool provider plugin.
//
// It is intended as a reference implementation for writing custom tool
// providers. The registered tool simply echoes one string argument.
package echotool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	pluginType = "echotool"

	schemaTypeObject = "object"
	schemaTypeString = "string"

	argText = "text"
)

func init() {
	if err := registry.RegisterToolProvider(pluginType, newTools); err != nil {
		panic(err)
	}
}

type providerCfg struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func newTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg providerCfg
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, errors.New("echotool: missing config.name")
	}
	desc := strings.TrimSpace(cfg.Description)
	if desc == "" {
		desc = "Echo one string."
	}

	return []tool.Tool{echoTool{name: name, desc: desc}}, nil
}

type echoTool struct {
	name string
	desc string
}

func (t echoTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: t.desc,
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
			Properties: map[string]*tool.Schema{
				argText: {
					Type:        schemaTypeString,
					Description: "Text to echo back.",
				},
			},
			Required: []string{argText},
		},
	}
}

func (t echoTool) Call(_ context.Context, jsonArgs []byte) (any, error) {
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	return args.Text, nil
}
