//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestRedactJSON_NestedStrings(t *testing.T) {
	t.Parallel()
	token := "sk-" + strings.Repeat("a", 32)
	raw, err := json.Marshal(map[string]any{
		"stdout": "Bearer " + token,
		"nested": map[string]any{"token": "token=supersecretvalue123"},
		"n":      1,
	})
	require.NoError(t, err)
	out := safety.RedactJSON(raw)
	require.NotContains(t, string(out), token)
	require.NotContains(t, string(out), "supersecretvalue123")
	require.Contains(t, string(out), "REDACTED")
	require.Contains(t, string(out), `"n":1`)
}

func TestRedactJSON_PlainTextFallback(t *testing.T) {
	t.Parallel()
	token := "sk-" + strings.Repeat("b", 32)
	out := safety.RedactJSON([]byte("leak " + token))
	require.NotContains(t, string(out), token)
}

func TestAfterToolRedact_ReplacesStringResult(t *testing.T) {
	t.Parallel()
	token := "sk-" + strings.Repeat("c", 32)
	cb := safety.AfterToolRedact()
	res, err := cb(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result:   "Authorization: Bearer " + token,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	s, ok := res.CustomResult.(string)
	require.True(t, ok)
	require.NotContains(t, s, token)
	require.Contains(t, s, "REDACTED")
}

func TestAfterToolRedact_SkipsUnknownTypes(t *testing.T) {
	t.Parallel()
	cb := safety.AfterToolRedact()
	res, err := cb(context.Background(), &tool.AfterToolArgs{
		Result: 42,
	})
	require.NoError(t, err)
	require.Nil(t, res)
}
