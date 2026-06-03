//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolsnapshot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type testTool struct {
	name string
}

func (t testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func TestSetGetCopiesToolSlice(t *testing.T) {
	inv := &agent.Invocation{}
	first := testTool{name: "first"}
	second := testTool{name: "second"}
	tools := []tool.Tool{first}
	Set(inv, tools, true)
	tools[0] = second
	cached, ok := Get(inv)
	require.True(t, ok)
	require.Equal(t, "first", cached[0].Declaration().Name)
	cached[0] = second
	cachedAgain, ok := Get(inv)
	require.True(t, ok)
	require.Equal(t, "first", cachedAgain[0].Declaration().Name)
}
