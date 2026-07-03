//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonrepair

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadsRepair_ValidJSONSkipsRepair verifies valid JSON is parsed without repair.
func TestLoadsRepair_ValidJSONSkipsRepair(t *testing.T) {
	result, err := LoadsRepair(`{"a":1}`)
	require.NoError(t, err)
	require.False(t, result.Repaired)
	require.Equal(t, map[string]any{"a": float64(1)}, result.Value)
}

// TestLoadsRepair_MalformedJSONUsesRepair verifies malformed JSON is repaired before parse.
func TestLoadsRepair_MalformedJSONUsesRepair(t *testing.T) {
	result, err := LoadsRepair(`{a:1}`)
	require.NoError(t, err)
	require.True(t, result.Repaired)
	require.Equal(t, map[string]any{"a": float64(1)}, result.Value)
}

// TestLoads_ReturnsValue verifies Loads returns the parsed value.
func TestLoads_ReturnsValue(t *testing.T) {
	value, err := Loads(`[1,2,3]`)
	require.NoError(t, err)
	require.Equal(t, []any{float64(1), float64(2), float64(3)}, value)
}
