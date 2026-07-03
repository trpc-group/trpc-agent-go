//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package latencydiag

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnabledDefaultAndSetInvalidatesCache(t *testing.T) {
	stateDir := t.TempDir()

	enabled, err := Enabled(stateDir)
	require.NoError(t, err)
	require.True(t, enabled)

	require.NoError(t, SetEnabled(stateDir, false))
	enabled, err = Enabled(stateDir)
	require.NoError(t, err)
	require.False(t, enabled)

	require.NoError(t, SetEnabled(stateDir, true))
	enabled, err = Enabled(stateDir)
	require.NoError(t, err)
	require.True(t, enabled)
}
