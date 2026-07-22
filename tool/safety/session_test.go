//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGuard_CloseResetsSessionTracker verifies that Guard.Close drops the
// session tracking state so the tracker does not grow without bound over
// the guard's lifetime.
func TestGuard_CloseResetsSessionTracker(t *testing.T) {
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()))
	require.NoError(t, err)

	g.sessions.register("sess-1")
	g.sessions.kill("sess-2")
	require.True(t, g.sessions.isKnown("sess-1"))
	require.True(t, g.sessions.isKilled("sess-2"))

	require.NoError(t, g.Close())
	require.False(t, g.sessions.isKnown("sess-1"))
	require.False(t, g.sessions.isKilled("sess-2"))
}
