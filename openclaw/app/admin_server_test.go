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
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAdminBinding_AutoPortFallback(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = busy.Close()
	})

	binding, err := openAdminBinding(
		busy.Addr().String(),
		true,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = binding.listener.Close()
	})

	require.NotNil(t, binding.listener)
	require.NotEqual(t, busy.Addr().String(), binding.addr)
	require.True(t, binding.relocated)
	require.NotEmpty(t, binding.url)
}

func TestOpenAdminBinding_ExactPortFailure(t *testing.T) {
	t.Parallel()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = busy.Close()
	})

	_, err = openAdminBinding(
		busy.Addr().String(),
		false,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listen on")
}
