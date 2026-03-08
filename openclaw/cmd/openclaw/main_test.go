//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestRun_UnknownFlagReturnsUsageCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, run([]string{"-unknown-flag"}))
}

func TestBundledChannels(t *testing.T) {
	t.Helper()

	_, ok := registry.LookupChannel("telegram")
	require.True(t, ok)

	_, ok = registry.LookupChannel("stdin")
	require.True(t, ok)
}
