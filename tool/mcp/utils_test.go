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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestConvertMCPSchema_Basic(t *testing.T) {
	mcpSchema := map[string]any{
		"type":        "object",
		"description": "test schema",
		"required":    []any{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "number", "description": "bbb"},
		},
	}

	s := convertMCPSchemaToSchema(mcpSchema)
	require.Equal(t, "object", s.Type)
	require.Equal(t, "test schema", s.Description)
	require.ElementsMatch(t, []string{"a", "b"}, s.Required)
	require.Equal(t, "string", s.Properties["a"].Type)
	require.Equal(t, "number", s.Properties["b"].Type)
	require.Equal(t, "bbb", s.Properties["b"].Description)
}

func TestConvertProperties_Nil(t *testing.T) {
	require.Nil(t, convertProperties(nil))
}

func TestConvertMCPSchema_InvalidJSON(t *testing.T) {
	// Channel cannot marshal, expect fallback schema.
	schema := convertMCPSchemaToSchema(make(chan int))
	require.Equal(t, &tool.Schema{Type: "object"}, schema)
}
