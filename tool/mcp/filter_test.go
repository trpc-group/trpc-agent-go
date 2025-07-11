//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

var toolsSample = []ToolInfo{
	{Name: "alpha", Description: "first"},
	{Name: "beta", Description: "second tool"},
	{Name: "gamma", Description: "third"},
}

func TestToolNameFilter_Include(t *testing.T) {
	f := NewIncludeFilter("alpha", "gamma")
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2)
	require.Equal(t, "alpha", got[0].Name)
	require.Equal(t, "gamma", got[1].Name)
}

func TestToolNameFilter_Exclude(t *testing.T) {
	f := NewExcludeFilter("beta")
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2)
	for _, ti := range got {
		require.NotEqual(t, "beta", ti.Name)
	}
}

func TestPatternFilter_NameInclude(t *testing.T) {
	f := NewPatternIncludeFilter("^a", "^b") // names starting with a or b
	got := f.Filter(context.Background(), toolsSample)
	require.Len(t, got, 2)
}

func TestCompositeFilter(t *testing.T) {
	f1 := NewIncludeFilter("alpha", "beta")
	f2 := NewPatternExcludeFilter("^a") // exclude starting with a

	composite := NewCompositeFilter(f1, f2)
	got := composite.Filter(context.Background(), toolsSample)
	require.Len(t, got, 1)
	require.Equal(t, "beta", got[0].Name)
}
