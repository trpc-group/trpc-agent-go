//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// The functions in state.go are intentionally trivial nil-safe
// wrappers around agent.Invocation state helpers. Their value lies
// almost entirely in their robustness to a nil invocation (which
// AfterModel/BeforeModel can legitimately receive in pure unit
// tests, see invocationSession docstring), so these tests focus
// on the nil branches + happy-path round trip rather than the
// agent.SetState plumbing itself.

func TestState_NilInvocation_AllReadersReturnZero(t *testing.T) {
	assert.Equal(t, 0, retryCount(nil),
		"retryCount on nil invocation must return zero, not panic")
	assert.False(t, reminderPending(nil),
		"reminderPending on nil invocation must return false")
	assert.False(t, blockerDeclared(nil),
		"blockerDeclared on nil invocation must return false")
	assert.Equal(t, "", blockerReason(nil),
		"blockerReason on nil invocation must return empty string")
}

func TestState_NilInvocation_AllMutatorsAreNoOps(t *testing.T) {
	// Each of these would panic on a real *Invocation if SetState
	// dereferenced an uninitialised noticeMu; the nil-guard turns
	// the call into a no-op. We don't assert anything beyond "does
	// not panic" — that IS the contract.
	assert.NotPanics(t, func() { _ = incRetryCount(nil) })
	assert.NotPanics(t, func() { resetRetryCount(nil) })
	assert.NotPanics(t, func() { setReminderPending(nil, true) })
	assert.NotPanics(t, func() { setReminderPending(nil, false) })
	assert.NotPanics(t, func() { markBlockerDeclared(nil, "any reason") })

	assert.Equal(t, 0, incRetryCount(nil),
		"incRetryCount on nil invocation must surface the same zero retryCount sees")
}

func TestState_RetryCounter_HappyPath(t *testing.T) {
	_, inv, _ := newTestInvocation(t, "agent-A")

	assert.Equal(t, 0, retryCount(inv),
		"fresh invocation must read zero")
	assert.Equal(t, 1, incRetryCount(inv))
	assert.Equal(t, 2, incRetryCount(inv))
	assert.Equal(t, 2, retryCount(inv),
		"retryCount must observe what incRetryCount stored")

	resetRetryCount(inv)
	assert.Equal(t, 0, retryCount(inv),
		"after reset the counter must read zero (DeleteState path)")
}

func TestState_ReminderPending_SetFalse_DeletesKey(t *testing.T) {
	_, inv, _ := newTestInvocation(t, "agent-A")

	setReminderPending(inv, true)
	assert.True(t, reminderPending(inv))

	// The "false" branch deletes the key rather than writing a
	// zero value (see setReminderPending docstring). Subsequent
	// reads must still return false — i.e. "absent key" and
	// "key=false" are observationally equivalent through the
	// getter, which is the contract we expose to AfterModel.
	setReminderPending(inv, false)
	assert.False(t, reminderPending(inv),
		"setting false must leave the getter reading false")
}

func TestState_Blocker_LatchAndReason(t *testing.T) {
	_, inv, _ := newTestInvocation(t, "agent-A")

	assert.False(t, blockerDeclared(inv))
	assert.Equal(t, "", blockerReason(inv))

	markBlockerDeclared(inv, "missing kubeconfig")
	assert.True(t, blockerDeclared(inv),
		"latch must surface after markBlockerDeclared")
	assert.Equal(t, "missing kubeconfig", blockerReason(inv),
		"reason must round-trip verbatim")

	// markBlockerDeclared is intentionally not idempotent-checked
	// — the latest call wins on the reason. AfterModel never calls
	// it twice today but we don't want a future caller to be
	// surprised by a "first reason wins" semantics that isn't in
	// the docstring.
	markBlockerDeclared(inv, "ambiguous requirements")
	assert.Equal(t, "ambiguous requirements", blockerReason(inv),
		"latest reason must overwrite the previous one")
}

// TestState_KeysAreNamespaced is a defensive assertion: every state
// key this package writes must carry the "todoenforcer:" prefix so
// it cannot collide with other extensions / framework state on the
// same invocation. The constants live in state.go; if a future
// refactor ever rename-drops the prefix this test catches it
// immediately.
func TestState_KeysAreNamespaced(t *testing.T) {
	keys := []string{
		stateKeyRetryCount,
		stateKeyReminderPending,
		stateKeyBlockerDeclared,
		stateKeyBlockerReason,
	}
	for _, k := range keys {
		assert.Contains(t, k, "todoenforcer:",
			"state key %q must be namespaced under todoenforcer:", k)
	}

	// Also assert the four keys are pairwise distinct — a
	// copy-paste typo that shared a key would silently couple
	// independent state pieces.
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		_, dup := seen[k]
		assert.False(t, dup, "duplicate state key: %s", k)
		seen[k] = struct{}{}
	}
}

// TestState_InvocationScopedIsolation verifies that two
// concurrent invocations cannot read each other's counter — the
// extension's whole concurrency story (Sharing one Enforcer across
// agents is safe, see package doc) depends on this.
func TestState_InvocationScopedIsolation(t *testing.T) {
	_, invA, _ := newTestInvocation(t, "agent-A")
	_, invB, _ := newTestInvocation(t, "agent-B")

	incRetryCount(invA)
	incRetryCount(invA)
	incRetryCount(invB)

	assert.Equal(t, 2, retryCount(invA))
	assert.Equal(t, 1, retryCount(invB),
		"invB's counter must not see invA's mutations")

	markBlockerDeclared(invA, "A blocked")
	assert.True(t, blockerDeclared(invA))
	assert.False(t, blockerDeclared(invB),
		"blocker latch is per-invocation; invB must remain clean")
	assert.Equal(t, "", blockerReason(invB))

	// agent.Invocation is the explicit isolation boundary; this
	// asserts we kept that contract intact for every key we write.
	_ = agent.Invocation{}
}
