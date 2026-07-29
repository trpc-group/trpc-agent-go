//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// sec is a shorthand for placing an event n seconds after the run base.
func sec(n int) time.Duration { return time.Duration(n) * time.Second }

// ref builds a session reference inside the harness app.
func ref(user, sessionID string) SessionRef {
	return SessionRef{AppName: "replay", UserID: user, SessionID: sessionID}
}

// Every case begins with a user message, and that is a requirement rather than
// a stylistic choice.
//
// session.ApplyEventFiltering runs on every append and anchors the event list
// at a user message: events preceding the first user message are truncated,
// and a session whose events contain no user message at all reads back empty.
// A case built only from assistant events therefore observes nothing on every
// backend, and uniform emptiness is indistinguishable from agreement. The
// fault-injection tests exist partly to catch a case that has quietly become
// vacuous this way.

// Scenarios returns the public replay cases.
//
// Each case isolates one way backends drift apart. They are deliberately small:
// a case that exercises everything at once reports a wall of differences and
// tells you nothing about which behavior actually broke.
func Scenarios() []Scenario {
	return []Scenario{
		singleTurn(),
		multiTurn(),
		toolCall(),
		stateLifecycle(),
		memoryLifecycle(),
		summaryLifecycle(),
		summaryWithTruncation(),
		trackEvents(),
		interleavedWrites(),
		retryAndRecovery(),
	}
}

// singleTurn is the simplest possible exchange. If backends disagree here,
// nothing more elaborate is worth reading.
func singleTurn() Scenario {
	r := ref("u-single", "s-single")
	return Scenario{
		Name:        "single-turn",
		Description: "one user message and one assistant reply",
		Sessions:    []SessionRef{r},
		Ops: []Op{
			CreateSession{Ref: r},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "e1", InvocationID: "inv-1", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "what is the deploy status?",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "e2", InvocationID: "inv-1", Author: "assistant",
				Offset: sec(1), Role: model.RoleAssistant, Content: "the rollout finished ten minutes ago",
			}},
		},
	}
}

// multiTurn checks that read-back order survives several turns. Order is the
// contract that replay depends on most directly.
func multiTurn() Scenario {
	r := ref("u-multi", "s-multi")
	ops := []Op{CreateSession{Ref: r}}
	turns := []struct {
		user      string
		assistant string
	}{
		{"list the failing tests", "three tests fail in the storage package"},
		{"which ones", "TestAppend, TestExpiry and TestPaging"},
		{"fix the first", "TestAppend now passes after the ordering fix"},
	}
	for i, turn := range turns {
		ops = append(ops,
			AppendEvent{Ref: r, Event: EventSpec{
				ID: idxID("u", i), InvocationID: idxID("inv", i), Author: "user",
				Offset: sec(i * 2), Role: model.RoleUser, Content: turn.user,
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: idxID("a", i), InvocationID: idxID("inv", i), Author: "assistant",
				Offset: sec(i*2 + 1), Role: model.RoleAssistant, Content: turn.assistant,
			}},
		)
	}
	return Scenario{
		Name:        "multi-turn-ordering",
		Description: "three consecutive turns read back in append order",
		Sessions:    []SessionRef{r},
		Ops:         ops,
	}
}

// toolCall covers the assistant/tool round trip, including the argument blob
// and the extension metadata that rides alongside it. Arguments are compared
// as canonical JSON, so a backend that reorders object members is fine while a
// backend that mangles a value is not.
func toolCall() Scenario {
	r := ref("u-tool", "s-tool")
	return Scenario{
		Name:        "tool-call-round-trip",
		Description: "assistant tool call, tool response, and call metadata extensions",
		Sessions:    []SessionRef{r},
		Ops: []Op{
			CreateSession{Ref: r},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "t1", InvocationID: "inv-tool", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "how many open PRs are there?",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "t2", InvocationID: "inv-tool", Author: "assistant",
				Offset: sec(1), Role: model.RoleAssistant,
				ToolCalls: []ToolCallSpec{{
					ID:        "call-1",
					Name:      "search_pulls",
					Arguments: `{"state":"open","repo":"trpc-agent-go","limit":50}`,
				}},
				Extensions: map[string]string{
					"tool_meta": `{"attempt":1,"timeoutMs":3000}`,
				},
				Tag:    "tooling",
				Branch: "main",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "t3", InvocationID: "inv-tool", Author: "tool",
				Offset: sec(2), Role: model.RoleTool,
				ToolID: "call-1", ToolName: "search_pulls",
				Content: `{"count":42}`,
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "t4", InvocationID: "inv-tool", Author: "assistant",
				Offset: sec(3), Role: model.RoleAssistant, Content: "there are 42 open pull requests",
			}},
		},
	}
}

// stateLifecycle covers all three state scopes and, critically, the difference
// between a key set to nil and a key removed. A backend that collapses the two
// loses information that callers can observe.
func stateLifecycle() Scenario {
	r := ref("u-state", "s-state")
	return Scenario{
		Name:        "state-write-overwrite-clear",
		Description: "session, app and user state through write, overwrite, nil and delete",
		Sessions:    []SessionRef{r},
		Ops: []Op{
			CreateSession{Ref: r, State: map[string][]byte{"seed": []byte("initial")}},
			UpdateSessionState{Ref: r, State: map[string][]byte{
				"lang":  []byte("en"),
				"stage": []byte("draft"),
			}},
			// Overwrite an existing key.
			UpdateSessionState{Ref: r, State: map[string][]byte{"stage": []byte("review")}},
			// A nil state delta stores nil under the key rather than removing
			// it, so the key must still be present afterwards.
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "s1", InvocationID: "inv-state", Author: "assistant",
				Offset: sec(0), Role: model.RoleAssistant, Content: "clearing the draft marker",
				StateDelta: map[string][]byte{"lang": nil},
			}},
			// App and user state keys are written unprefixed. The scopes are
			// selected by the operation, and the reader re-applies the app: and
			// user: prefixes when merging the scopes into session state.
			UpdateAppState{Ref: r, State: map[string][]byte{
				"region":  []byte("eu"),
				"version": []byte("2.1"),
			}},
			UpdateUserState{Ref: r, State: map[string][]byte{
				"tier": []byte("gold"),
				"tz":   []byte("UTC"),
			}},
			// A delete removes the key outright, unlike the nil delta above.
			DeleteAppState{Ref: r, Key: "version"},
			DeleteUserState{Ref: r, Key: "tz"},
		},
	}
}

// memoryLifecycle covers add, topic-only update, content update and delete.
// Identifiers are content-derived, so a topic change must keep the identifier
// while a content change must rotate it, and every backend must agree.
func memoryLifecycle() Scenario {
	r := ref("u-memory", "s-memory")
	return Scenario{
		Name:        "memory-write-update-delete",
		Description: "memory add, topic-only update, content update with identifier rotation, and delete",
		Sessions:    []SessionRef{r},
		MemoryUser:  &r,
		Ops: []Op{
			CreateSession{Ref: r},
			// The conversation the memories are drawn from. It also anchors the
			// event list, without which the session would read back empty.
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "m1", InvocationID: "inv-mem", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "remember how I like to work",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "m2", InvocationID: "inv-mem", Author: "assistant",
				Offset: sec(1), Role: model.RoleAssistant, Content: "noted",
			}},
			AddMemory{Ref: r, Memory: MemorySpec{
				Content: "prefers short answers", Topics: []string{"style"},
			}},
			AddMemory{Ref: r, Memory: MemorySpec{
				Content: "works on the storage team", Topics: []string{"role", "org"},
			}},
			AddMemory{Ref: r, Memory: MemorySpec{
				Content: "deploys on fridays", Topics: []string{"habit"},
			}},
			// Topics do not participate in identifier generation, so this must
			// update the topics in place without rotating the identifier.
			UpdateMemory{Ref: r, MatchContent: "prefers short answers", Memory: MemorySpec{
				Content: "prefers short answers", Topics: []string{"style", "tone"},
			}},
			// Content does participate, so this must rotate the identifier.
			UpdateMemory{Ref: r, MatchContent: "works on the storage team", Memory: MemorySpec{
				Content: "works on the session storage team", Topics: []string{"role", "org"},
			}},
			DeleteMemory{Ref: r, MatchContent: "deploys on fridays"},
		},
	}
}

// summaryLifecycle covers generation, regeneration and branch scoping. The
// whole-session summary and a branch summary must stay independent, and the
// stored boundary must point at the events each one actually covers.
func summaryLifecycle() Scenario {
	r := ref("u-summary", "s-summary")
	return Scenario{
		Name:        "summary-generate-and-update",
		Description: "whole-session and branch summaries, then a regeneration that supersedes the first",
		Sessions:    []SessionRef{r},
		Ops: []Op{
			CreateSession{Ref: r},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "sm1", InvocationID: "inv-sum", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "summarize the incident",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "sm2", InvocationID: "inv-sum", Author: "assistant",
				Offset: sec(1), Role: model.RoleAssistant, Content: "the cache expired early and traffic fell through",
			}},
			CreateSummary{Ref: r, Summary: SummarySpec{
				Text: "incident: cache expired early", Force: true,
			}},
			// A branch summary is filed under its own filter key and must not
			// disturb the whole-session summary.
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "sm3", InvocationID: "inv-sum", Author: "assistant",
				Offset: sec(2), Role: model.RoleAssistant, Content: "the fix raises the TTL",
				FilterKey: "remediation",
			}},
			CreateSummary{Ref: r, Summary: SummarySpec{
				FilterKey: "remediation", Text: "remediation: raise the TTL", Force: true,
			}},
			// Regenerating the whole-session summary must replace the earlier
			// text and advance the boundary to the newest covered event.
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "sm4", InvocationID: "inv-sum", Author: "assistant",
				Offset: sec(3), Role: model.RoleAssistant, Content: "the rollout completed",
			}},
			CreateSummary{Ref: r, Summary: SummarySpec{
				Text: "incident resolved after the TTL change", Force: true,
			}},
		},
	}
}

// summaryWithTruncation models a long conversation that has been compressed:
// a summary covers the early turns, and new turns arrive afterwards. Replay
// has to reconstruct context from the summary plus the events that follow it,
// so the boundary separating the two must land in the same place everywhere.
func summaryWithTruncation() Scenario {
	r := ref("u-truncate", "s-truncate")
	ops := []Op{CreateSession{Ref: r}}
	for i := 0; i < 6; i++ {
		ops = append(ops, AppendEvent{Ref: r, Event: EventSpec{
			ID: idxID("old", i), InvocationID: idxID("inv-old", i), Author: "user",
			Offset: sec(i), Role: model.RoleUser, Content: "early turn " + itoa(i),
		}})
	}
	ops = append(ops, CreateSummary{Ref: r, Summary: SummarySpec{
		Text: "the first six turns covered setup questions", Force: true,
	}})
	for i := 0; i < 3; i++ {
		ops = append(ops, AppendEvent{Ref: r, Event: EventSpec{
			ID: idxID("new", i), InvocationID: idxID("inv-new", i), Author: "assistant",
			Offset: sec(10 + i), Role: model.RoleAssistant, Content: "later turn " + itoa(i),
		}})
	}
	return Scenario{
		Name:        "summary-with-event-truncation",
		Description: "a summary covers the early turns while later turns stay as raw events",
		Sessions:    []SessionRef{r},
		Ops:         ops,
	}
}

// trackEvents covers the observability side channel: timings, subtask status
// and an error record, spread across two tracks.
func trackEvents() Scenario {
	r := ref("u-track", "s-track")
	return Scenario{
		Name:        "track-events",
		Description: "timing, status and error entries across two tracks",
		Sessions:    []SessionRef{r},
		Ops: []Op{
			CreateSession{Ref: r},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "tr1", InvocationID: "inv-track", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "run the migration",
			}},
			AppendTrackEvent{Ref: r, Event: TrackEventSpec{
				Track: "timing", Offset: sec(1),
				Payload: `{"stage":"plan","durationMs":120}`,
			}},
			AppendTrackEvent{Ref: r, Event: TrackEventSpec{
				Track: "timing", Offset: sec(2),
				Payload: `{"stage":"apply","durationMs":4300}`,
			}},
			AppendTrackEvent{Ref: r, Event: TrackEventSpec{
				Track: "subtask", Offset: sec(3),
				Payload: `{"id":"migrate-01","status":"succeeded"}`,
			}},
			AppendTrackEvent{Ref: r, Event: TrackEventSpec{
				Track: "subtask", Offset: sec(4),
				Payload: `{"id":"migrate-02","status":"failed","error":"lock timeout"}`,
			}},
		},
	}
}

// interleavedWrites appends events from two invocations out of timestamp
// order. Backends order events by write order rather than by timestamp, so the
// read-back order must follow the appends, and every backend must make the
// same choice.
func interleavedWrites() Scenario {
	r := ref("u-interleaved", "s-interleaved")
	return Scenario{
		Name:        "interleaved-out-of-order-writes",
		Description: "two invocations appended out of timestamp order",
		Sessions:    []SessionRef{r},
		Ops: []Op{
			CreateSession{Ref: r},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "i0", InvocationID: "inv-a", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "run both branches",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "i1", InvocationID: "inv-a", Author: "assistant",
				Offset: sec(5), Role: model.RoleAssistant, Content: "branch a step one",
				Branch: "a",
			}},
			// Earlier timestamp, later append: ordering must follow the append.
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "i2", InvocationID: "inv-b", Author: "assistant",
				Offset: sec(1), Role: model.RoleAssistant, Content: "branch b step one",
				Branch: "b",
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "i3", InvocationID: "inv-a", Author: "assistant",
				Offset: sec(9), Role: model.RoleAssistant, Content: "branch a step two",
				Branch: "a", StateDelta: map[string][]byte{"a_steps": []byte("2")},
			}},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "i4", InvocationID: "inv-b", Author: "assistant",
				Offset: sec(3), Role: model.RoleAssistant, Content: "branch b step two",
				Branch: "b", StateDelta: map[string][]byte{"b_steps": []byte("2")},
			}},
		},
	}
}

// retryAndRecovery replays the writes a crashed-and-restarted caller would
// produce: the same event appended twice, the same memory added twice, and the
// same summary generated twice. Backends need not agree with any particular
// policy, but they must agree with each other, or a retry means something
// different depending on where the data lives.
func retryAndRecovery() Scenario {
	r := ref("u-retry", "s-retry")
	dup := EventSpec{
		ID: "r2", InvocationID: "inv-retry", Author: "assistant",
		Offset: sec(1), Role: model.RoleAssistant, Content: "charging the card",
		StateDelta: map[string][]byte{"attempts": []byte("1")},
	}
	mem := MemorySpec{Content: "billing runs on the first", Topics: []string{"billing"}}
	return Scenario{
		Name:        "retry-and-recovery",
		Description: "a replayed event, a replayed memory write and a regenerated summary after a retry",
		Sessions:    []SessionRef{r},
		MemoryUser:  &r,
		Ops: []Op{
			CreateSession{Ref: r},
			AppendEvent{Ref: r, Event: EventSpec{
				ID: "r1", InvocationID: "inv-retry", Author: "user",
				Offset: sec(0), Role: model.RoleUser, Content: "charge my card",
			}},
			AppendEvent{Ref: r, Event: dup},
			// The caller retries the same event after failing to record the
			// first attempt's outcome.
			AppendEvent{Ref: r, Event: dup},
			AddMemory{Ref: r, Memory: mem},
			// The same memory is written again by the retry.
			AddMemory{Ref: r, Memory: mem},
			CreateSummary{Ref: r, Summary: SummarySpec{Text: "payment attempted", Force: true}},
			CreateSummary{Ref: r, Summary: SummarySpec{Text: "payment attempted", Force: true}},
		},
	}
}

// idxID builds a stable identifier such as "u0", "u1".
func idxID(prefix string, i int) string { return prefix + itoa(i) }

// itoa avoids pulling strconv into the scenario definitions for single digits
// while still behaving correctly beyond them.
func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}
