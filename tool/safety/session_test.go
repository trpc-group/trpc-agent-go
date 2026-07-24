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

func TestSessionTracker_KilledSessionCannotBeReused(t *testing.T) {
	sessions := newSessionTracker()
	sessions.register("sess-1")
	sessions.kill("sess-1")
	require.False(t, sessions.isKnown("sess-1"))
	require.True(t, sessions.isKilled("sess-1"))

	findings := ruleHost(ScanInput{
		ToolName:     "write_stdin",
		SessionID:    "sess-1",
		SessionInput: "echo hello",
	}, &analysis{}, DefaultPolicy(), sessions)
	require.Contains(t, ruleIDSet(findings), "host.residual_session")
}

func TestSessionTracker_BoundsKilledTombstones(t *testing.T) {
	sessions := newSessionTracker()
	for i := 0; i < maxKilledSessions+10; i++ {
		sessions.kill(itoa(i))
	}
	require.LessOrEqual(t, len(sessions.killed), maxKilledSessions)

	// A newly registered session may safely reuse an expired or killed
	// identifier.
	sessions.register("100")
	require.True(t, sessions.isKnown("100"))
	require.False(t, sessions.isKilled("100"))

}

func TestSessionTracker_BoundsKnownSessions(t *testing.T) {
	sessions := newSessionTracker()
	for i := 0; i < maxKnownSessions+10; i++ {
		sessions.register(itoa(i))
	}
	require.LessOrEqual(t, len(sessions.known), maxKnownSessions)
	require.LessOrEqual(t, len(sessions.knownOrder),
		maxKnownSessions)
}
