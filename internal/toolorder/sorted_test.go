//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolorder

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestSortedTools(t *testing.T) {
	tools := map[string]tool.Tool{
		"gamma": testOrderTool{name: "gamma"},
		"alpha": testOrderTool{name: "alpha"},
		"skip":  nil,
		"delta": testOrderTool{name: "delta"},
		"beta":  testOrderTool{name: "beta"},
	}

	sorted := SortedTools(tools)
	require.Len(t, sorted, 4)
	require.Equal(t, "alpha", sorted[0].Declaration().Name)
	require.Equal(t, "beta", sorted[1].Declaration().Name)
	require.Equal(t, "delta", sorted[2].Declaration().Name)
	require.Equal(t, "gamma", sorted[3].Declaration().Name)
}

func TestSortedToolsFiltersNilDeclarationAndSortsByKey(t *testing.T) {
	tools := map[string]tool.Tool{
		"b-key": testOrderTool{name: "alpha"},
		"a-key": testOrderTool{name: "zeta"},
		"c-key": nilDeclarationTool{},
	}

	sorted := SortedTools(tools)
	require.Len(t, sorted, 2)
	require.Equal(t, "zeta", sorted[0].Declaration().Name)
	require.Equal(t, "alpha", sorted[1].Declaration().Name)
}

type testOrderTool struct {
	name string
}

func (t testOrderTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

type nilDeclarationTool struct{}

func (nilDeclarationTool) Declaration() *tool.Declaration { return nil }
