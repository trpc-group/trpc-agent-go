//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package echotool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestInit_RegistersProvider(t *testing.T) {
	f, ok := registry.LookupToolProvider(pluginType)
	require.True(t, ok)
	require.NotNil(t, f)
}

func TestNewTools_MissingNameFails(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, "description: hi\n")
	_, err := newTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing config.name")
}

func TestNewTools_UnknownFieldFails(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, "name: x\nunknown: y\n")
	_, err := newTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.Error(t, err)
}

func TestNewTools_DefaultDescriptionAndCallWork(t *testing.T) {
	t.Parallel()

	cfg := yamlNode(t, "name: echo\n")
	tools, err := newTools(
		registry.ToolProviderDeps{},
		registry.PluginSpec{Config: cfg},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)

	decl := tools[0].Declaration()
	require.Equal(t, "echo", decl.Name)
	require.Equal(t, "Echo one string.", decl.Description)
	require.NotNil(t, decl.InputSchema)
	require.Equal(t, schemaTypeObject, decl.InputSchema.Type)
	require.Contains(t, decl.InputSchema.Properties, argText)
	require.Contains(t, decl.InputSchema.Required, argText)

	callable, ok := tools[0].(tool.CallableTool)
	require.True(t, ok)

	out, err := callable.Call(
		context.Background(),
		[]byte(`{"text":"hi"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "hi", out)
}

func yamlNode(t *testing.T, raw string) *yaml.Node {
	t.Helper()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &node))
	return &node
}
