//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package geminifetch

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTool(t *testing.T) {
	// This test just ensures the tool can be created
	tool, err := NewTool("gemini-2.5-flash")
	require.NoError(t, err)
	assert.NotNil(t, tool)
}

func TestNewTool_EmptyModel(t *testing.T) {
	// Test that empty model name returns error
	tool, err := NewTool("")
	require.Error(t, err)
	assert.Nil(t, tool)
	assert.Contains(t, err.Error(), "model name is required")
}

func TestNewTool_WithOptions(t *testing.T) {
	tool, err := NewTool(
		"gemini-2.5-flash",
		WithAPIKey("test-key"),
	)
	require.NoError(t, err)
	assert.NotNil(t, tool)
}

func TestGeminiFetch_NoPrompt(t *testing.T) {
	// Skip if no API key
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set")
	}

	tool, err := NewTool("gemini-2.5-flash")
	require.NoError(t, err)

	res, err := tool.Call(context.Background(), []byte(`{"prompt": ""}`))
	require.NoError(t, err)

	resp, ok := res.(fetchResponse)
	require.True(t, ok)
	assert.Empty(t, resp.Content)
}

func TestGeminiFetch_PromptFormat(t *testing.T) {
	// Test that prompt is properly formatted
	req := fetchRequest{
		Prompt: "Compare https://example.com/page1 with https://example.com/page2",
	}

	assert.Contains(t, req.Prompt, "https://example.com/page1")
	assert.Contains(t, req.Prompt, "https://example.com/page2")
}
