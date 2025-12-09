//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package geminifetch_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/geminifetch"
)

func TestGeminiFetch_Real(t *testing.T) {
	// Skip if no API key
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set, skipping real API test")
	}

	tool, err := geminifetch.NewTool("gemini-2.5-flash")
	require.NoError(t, err)

	// Test with a real URL embedded in the prompt
	args := `{"prompt": "Summarize the key features described at https://ai.google.dev/gemini-api/docs/url-context"}`

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	// Print the result for inspection
	t.Logf("Result: %+v", res)
}

func TestGeminiFetch_MultipleURLs(t *testing.T) {
	// Skip if no API key
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY not set, skipping real API test")
	}

	tool, err := geminifetch.NewTool("gemini-2.5-flash")
	require.NoError(t, err)

	// Test with multiple URLs in the prompt
	args := `{"prompt": "Compare the ingredients and cooking times from the recipes at https://www.foodnetwork.com/recipes/ina-garten/perfect-roast-chicken-recipe-1940592 and https://www.allrecipes.com/recipe/21151/simple-whole-roast-chicken/"}`

	res, err := tool.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	// Print the result for inspection
	t.Logf("Result: %+v", res)
}
