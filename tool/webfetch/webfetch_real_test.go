//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package webfetch_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch"
)

func TestWebFetch_1(t *testing.T) {
	wft := webfetch.NewTool()

	// The Call method expects JSON input with a "prompt" field
	prompt := "summary web fetch feature from https://geminicli.com/docs/tools/web-fetch/"
	args := fmt.Sprintf(`{"prompt": "%s"}`, prompt)

	res, err := wft.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	// Print the result for inspection
	t.Logf("Result: %+v", res)
}

func TestWebFetch_2(t *testing.T) {

	wft := webfetch.NewTool()

	// The Call method expects JSON input with a "prompt" field
	prompt := "gemini-cli web_fetch feature is powered  by gemini api https://ai.google.dev/gemini-api/docs/url-context, figure out Supported and unsupported content types"
	args := fmt.Sprintf(`{"prompt": "%s"}`, prompt)

	res, err := wft.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	// Print the result for inspection
	t.Logf("Result: %+v", res)

}
