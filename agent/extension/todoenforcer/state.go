//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import "trpc.group/trpc-go/trpc-agent-go/agent"

// Invocation-state keys owned by todoenforcer.
//
// All keys live on agent.Invocation (set via inv.SetState, read
// via agent.GetStateValue) so that retries and blocker
// declarations are strictly per-run. Concurrent runs that happen
// to share a Session each get their own Invocation and therefore
// their own counter and flags. The keys are namespaced under
// "todoenforcer:" to stay easy to grep for and to prevent
// collisions with other extensions.
//
// All entries here die with the Invocation that owns them. When
// the user submits a follow-up turn the runner constructs a fresh
// Invocation, so a previously declared blocker does NOT carry
// over — the model gets a clean slate to attempt the work again
// once the missing precondition has been supplied.
const (
	// stateKeyRetryCount counts how many times AfterModel has
	// blocked a final response on the current invocation. Bounded
	// by Options.MaxRetries.
	stateKeyRetryCount = "todoenforcer:retry_count"

	// stateKeyReminderPending is set by AfterModel when a response
	// is blocked, and consumed by BeforeModel on the next turn to
	// trigger nudge-injection.
	stateKeyReminderPending = "todoenforcer:reminder_pending"

	// stateKeyBlockerDeclared latches to true once
	// todo_declare_blocker has been invoked. Subsequent final
	// responses on this invocation are then allowed through
	// regardless of the open-items state — the model has formally
	// signalled that it cannot make further progress without
	// user-supplied input, and forcing it back into the loop
	// would be exactly the bullying behaviour this extension is
	// designed to avoid.
	stateKeyBlockerDeclared = "todoenforcer:blocker_declared"

	// stateKeyBlockerReason holds the operator-readable reason
	// the model supplied to todo_declare_blocker. Surfaced
	// through EnforceEvent.BlockerReason for OnEnforce observers
	// and kept on the invocation for trace-export consumers.
	stateKeyBlockerReason = "todoenforcer:blocker_reason"
)

// retryCount returns the current counter (0 if unset / nil inv).
func retryCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	v, _ := agent.GetStateValue[int](inv, stateKeyRetryCount)
	return v
}

// incRetryCount increments and returns the new value. Inlining
// the read avoids a separate Get → Set round-trip and keeps the
// AfterModel hot path small.
func incRetryCount(inv *agent.Invocation) int {
	if inv == nil {
		return 0
	}
	n := retryCount(inv) + 1
	inv.SetState(stateKeyRetryCount, n)
	return n
}

// resetRetryCount zeroes the counter. Called whenever the
// retry budget is fully consumed (fail-open path) so that any
// downstream code that re-reads the counter sees a stable
// "no enforcement attempts pending" value. The Invocation itself
// will be discarded shortly afterwards, so this is mostly for
// observability cleanliness rather than correctness.
func resetRetryCount(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(stateKeyRetryCount)
}

// reminderPending reports whether AfterModel asked the next
// BeforeModel to inject a nudge.
func reminderPending(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	v, _ := agent.GetStateValue[bool](inv, stateKeyReminderPending)
	return v
}

// setReminderPending sets / clears the flag. We DeleteState on
// false rather than writing a zero value so introspection tools
// see "no key" instead of "key set to false" — slightly less
// noisy in trace dumps.
func setReminderPending(inv *agent.Invocation, pending bool) {
	if inv == nil {
		return
	}
	if pending {
		inv.SetState(stateKeyReminderPending, true)
		return
	}
	inv.DeleteState(stateKeyReminderPending)
}

// blockerDeclared reports whether todo_declare_blocker has been
// called on this invocation.
func blockerDeclared(inv *agent.Invocation) bool {
	if inv == nil {
		return false
	}
	v, _ := agent.GetStateValue[bool](inv, stateKeyBlockerDeclared)
	return v
}

// markBlockerDeclared latches the flag and stores the reason
// atomically from the caller's viewpoint (two SetState calls,
// but the AfterModel decision tree only inspects the flag and
// reason in strict order so a torn read is safe).
func markBlockerDeclared(inv *agent.Invocation, reason string) {
	if inv == nil {
		return
	}
	inv.SetState(stateKeyBlockerDeclared, true)
	inv.SetState(stateKeyBlockerReason, reason)
}

// blockerReason returns the stored reason, or "" when no blocker
// has been declared on this invocation.
func blockerReason(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	v, _ := agent.GetStateValue[string](inv, stateKeyBlockerReason)
	return v
}
