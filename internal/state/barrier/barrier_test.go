//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package barrier

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestEnableAndClone(t *testing.T) {
	inv := agent.NewInvocation()
	require.False(t, Enabled(inv))

	Enable(inv)
	require.True(t, Enabled(inv))

	clone := inv.Clone()
	require.True(t, Enabled(clone))
}

func TestEnableNilInvocation(t *testing.T) {
	Enable(nil)
	require.False(t, Enabled(nil))
}
