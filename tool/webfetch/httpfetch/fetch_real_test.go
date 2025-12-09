//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package httpfetch_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
)

func TestWebFetch_1(t *testing.T) {
	wft := httpfetch.NewTool()

	// The Call method expects JSON input with a "urls" field
	args := `{"urls": ["https://geminicli.com/docs/tools/web-fetch/"]}`

	res, err := wft.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	// Print the result for inspection
	t.Logf("Result: %+v", res)
}

func TestWebFetch_2(t *testing.T) {

	wft := httpfetch.NewTool()

	// The Call method expects JSON input with a "urls" field
	args := `{"urls": ["https://ai.google.dev/gemini-api/docs/url-context"]}`

	res, err := wft.Call(context.Background(), []byte(args))
	require.NoError(t, err)

	// Print the result for inspection
	t.Logf("Result: %+v", res)

}
