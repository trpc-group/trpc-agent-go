//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSplitToolArgumentGuardPatterns(t *testing.T) {
	t.Parallel()

	got := splitToolArgumentGuardPatterns("one,two\nthree\r\nfour")
	require.Equal(t, []string{"one", "two", "three", "four"}, got)
}

func TestNormalizeToolArgumentGuardPatterns(t *testing.T) {
	t.Parallel()

	got := normalizeToolArgumentGuardPatterns([]string{
		"  ServiceNow/TapeAgents ",
		"",
		"servicenow/tapeagents",
		"gaia_agent/tapes",
	})
	require.Equal(t, []string{
		"servicenow/tapeagents",
		"gaia_agent/tapes",
	}, got)
}

func TestToolArgumentGuardCallbackBlocksConfiguredPattern(t *testing.T) {
	t.Parallel()

	callback := newToolArgumentGuardCallback([]string{
		"ServiceNow/TapeAgents",
	})
	require.NotNil(t, callback)

	_, err := callback(context.Background(), &tool.BeforeToolArgs{
		ToolName: "web_fetch",
		Arguments: []byte(
			`{"urls":["https://github.com/ServiceNow/TapeAgents"]}`,
		),
	})
	require.ErrorContains(t, err, errToolArgumentBlocked)
}

func TestToolArgumentGuardCallbackAllowsUnmatchedArgs(t *testing.T) {
	t.Parallel()

	callback := newToolArgumentGuardCallback([]string{"gaia_agent/tapes"})
	require.NotNil(t, callback)

	result, err := callback(context.Background(), &tool.BeforeToolArgs{
		ToolName:  "web_fetch",
		Arguments: []byte(`{"urls":["https://example.com/source"]}`),
	})
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestToolArgumentGuardCallbackEmptyPatterns(t *testing.T) {
	t.Parallel()

	require.Nil(t, newToolArgumentGuardCallback(nil))
	require.Nil(t, newToolArgumentGuardCallback([]string{" ", ""}))
}

func TestRegisterToolArgumentGuardCallback(t *testing.T) {
	t.Parallel()

	callbacks := tool.NewCallbacks()
	registerToolArgumentGuardCallback(callbacks, " ")
	require.Empty(t, callbacks.BeforeTool)

	registerToolArgumentGuardCallback(callbacks, "gaia_agent/tapes")
	require.Len(t, callbacks.BeforeTool, 1)
}
