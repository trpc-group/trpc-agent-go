//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package modelrequest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolsDisabled(t *testing.T) {
	require.False(t, ToolsDisabled(nil))
	require.False(t, ToolsDisabled(context.Background()))
	require.True(t, ToolsDisabled(WithToolsDisabled(context.Background())))
}

func TestDeleteToolControlFields(t *testing.T) {
	fields := map[string]any{
		"tool_choice":         "required",
		"parallel_tool_calls": true,
		"tools":               []any{},
		"function_call":       "auto",
		"functions":           []any{},
		"keep":                "value",
	}

	DeleteToolControlFields(fields)

	require.Equal(t, map[string]any{"keep": "value"}, fields)
	require.True(t, IsToolControlField("ToolChoice"))
	require.False(t, IsToolControlField("keep"))

	unfiltered := map[string]any{"tool_choice": "required", "keep": "value"}
	require.Equal(t, unfiltered, FilterToolControlFields(unfiltered, false))
	require.Equal(
		t,
		map[string]any{"keep": "value"},
		FilterToolControlFields(unfiltered, true),
	)
}
