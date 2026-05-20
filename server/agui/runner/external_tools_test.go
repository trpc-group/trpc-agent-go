//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

func TestExternalToolsFromRunAgentInput(t *testing.T) {
	const (
		toolName        = "client_search"
		toolDescription = "Search a frontend-owned source."
		argName         = "query"
		argType         = "string"
		schemaType      = "object"
	)

	var input adapter.RunAgentInput
	err := json.Unmarshal([]byte(`{
		"tools": [
			{
				"name": "client_search",
				"description": "Search a frontend-owned source.",
				"parameters": {
					"type": "object",
					"properties": {
						"query": {"type": "string"}
					},
					"required": ["query"]
				}
			}
		]
	}`), &input)
	require.NoError(t, err)

	tools, err := ExternalToolsFromRunAgentInput(&input)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	decl := tools[0].Declaration()
	require.NotNil(t, decl)
	assert.Equal(t, toolName, decl.Name)
	assert.Equal(t, toolDescription, decl.Description)
	require.NotNil(t, decl.InputSchema)
	assert.Equal(t, schemaType, decl.InputSchema.Type)
	require.Contains(t, decl.InputSchema.Properties, argName)
	assert.Equal(t, argType, decl.InputSchema.Properties[argName].Type)
	assert.Equal(t, []string{argName}, decl.InputSchema.Required)
}

func TestExternalToolsFromRunAgentInputEmpty(t *testing.T) {
	tools, err := ExternalToolsFromRunAgentInput(nil)

	require.NoError(t, err)
	assert.Nil(t, tools)
}

func TestExternalToolsFromRunAgentInputDefaultsNilParameters(t *testing.T) {
	const toolName = "client_notify"

	var input adapter.RunAgentInput
	err := json.Unmarshal([]byte(`{
		"tools": [{"name": "client_notify"}]
	}`), &input)
	require.NoError(t, err)

	tools, err := ExternalToolsFromRunAgentInput(&input)

	require.NoError(t, err)
	require.Len(t, tools, 1)
	decl := tools[0].Declaration()
	require.NotNil(t, decl)
	require.NotNil(t, decl.InputSchema)
	assert.Equal(t, toolName, decl.Name)
	assert.Equal(t, jsonSchemaTypeObject, decl.InputSchema.Type)
}

func TestExternalToolsFromRunAgentInputRejectsEmptyName(t *testing.T) {
	var input adapter.RunAgentInput
	err := json.Unmarshal([]byte(`{
		"tools": [{"description": "missing name"}]
	}`), &input)
	require.NoError(t, err)

	tools, err := ExternalToolsFromRunAgentInput(&input)

	require.Error(t, err)
	assert.Nil(t, tools)
}
