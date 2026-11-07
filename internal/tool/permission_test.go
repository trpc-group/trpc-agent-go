//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"encoding/json"
	"math/big"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionSnapshotIsolatesArgumentsWithoutInterpretingSchema(t *testing.T) {
	// Both JSON-compatible values with opaque state and values that a custom
	// policy can inspect without serialization retain their original Go types.
	for _, value := range []any{netip.MustParseAddr("127.0.0.1"), big.NewInt(123), make(chan int)} {
		input := &tool.PermissionRequest{
			ToolName: "connect", Arguments: []byte(`{"id":9007199254740993}`),
			Declaration: &tool.Declaration{InputSchema: &tool.Schema{Default: value}},
		}
		out, err := SnapshotPermissionRequest(input)
		require.NoError(t, err)
		require.Same(t, input.Declaration, out.Declaration)
		require.Equal(t, value, out.Declaration.InputSchema.Default)
		out.Arguments[0] = '!'
		out.Arguments = []byte(`{}`)
		out.ToolName = "other"
		out.Declaration = nil
		require.Equal(t, "connect", input.ToolName)
		require.Equal(t, `{"id":9007199254740993}`, string(input.Arguments))
		require.NotNil(t, input.Declaration)
	}
}

func TestPermissionSnapshotDoesNotInvokeSchemaCodecs(t *testing.T) {
	calls := 0
	input := &tool.PermissionRequest{Declaration: &tool.Declaration{
		InputSchema: &tool.Schema{Default: permissionJSONValue{calls: &calls}},
	}}
	_, err := json.Marshal(input.Declaration)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	_, err = SnapshotPermissionRequest(input)
	require.NoError(t, err)
	require.Equal(t, 1, calls, "permission input isolation must not invoke custom codecs")
	_, err = SnapshotPermissionRequest(nil)
	require.Error(t, err)
}

type permissionJSONValue struct{ calls *int }

func (v permissionJSONValue) MarshalJSON() ([]byte, error) {
	*v.calls++
	return []byte(`"value"`), nil
}
