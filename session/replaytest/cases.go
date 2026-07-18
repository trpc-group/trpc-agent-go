// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AllCases returns the public replay cases covering issue #2001 scenarios.
func AllCases() []ReplayCase {
	return []ReplayCase{
		CaseSingleTurnText(),
		CaseMultiTurnConversation(),
		CaseToolCallConversation(),
		CaseStateCRUD(),
		CaseMemoryWriteAndRead(),
		CaseSummaryGeneration(),
		CaseSummaryWithTruncation(),
		CaseSummaryFilterKey(),
		CaseTrackEvents(),
		CaseConcurrentInterleaved(),
		CaseRecoveryDuplicateEvent(),
	}
}

// CaseSingleTurnText covers one user message and one assistant response.
func CaseSingleTurnText() ReplayCase {
	key := SessionKeyFor("single_turn_text")
	return ReplayCase{
		Name:        "single_turn_text",
		Description: "single user and assistant turn",
		Steps: []Step{
			AppendEventStep{StepKey: "c1.user.1", SessionKey: key, Event: UserEvent("c1.user.1", "hello")},
			AppendEventStep{StepKey: "c1.assistant.1", SessionKey: key, Event: AssistantEvent("c1.assistant.1", "hello back")},
			GetSessionStep{StepKey: "c1.get", SessionKey: key},
		},
	}
}

// CaseMultiTurnConversation covers sequential multi-turn event ordering.
func CaseMultiTurnConversation() ReplayCase {
	key := SessionKeyFor("multi_turn_conversation")
	return ReplayCase{
		Name:        "multi_turn_conversation",
		Description: "three user/assistant turns",
		Steps: []Step{
			AppendEventStep{StepKey: "c2.user.1", SessionKey: key, Event: UserEvent("c2.user.1", "u1")},
			AppendEventStep{StepKey: "c2.assistant.1", SessionKey: key, Event: AssistantEvent("c2.assistant.1", "a1")},
			AppendEventStep{StepKey: "c2.user.2", SessionKey: key, Event: UserEvent("c2.user.2", "u2")},
			AppendEventStep{StepKey: "c2.assistant.2", SessionKey: key, Event: AssistantEvent("c2.assistant.2", "a2")},
			AppendEventStep{StepKey: "c2.user.3", SessionKey: key, Event: UserEvent("c2.user.3", "u3")},
			AppendEventStep{StepKey: "c2.assistant.3", SessionKey: key, Event: AssistantEvent("c2.assistant.3", "a3")},
			GetSessionStep{StepKey: "c2.get", SessionKey: key},
		},
	}
}

// CaseToolCallConversation covers tool-call and tool-response payloads.
func CaseToolCallConversation() ReplayCase {
	key := SessionKeyFor("tool_call_conversation")
	return ReplayCase{
		Name:        "tool_call_conversation",
		Description: "assistant tool call and tool response",
		Steps: []Step{
			AppendEventStep{StepKey: "c3.tool_call.1", SessionKey: key, Event: ToolCallEvent("c3.tool_call.1")},
			AppendEventStep{StepKey: "c3.tool_response.1", SessionKey: key, Event: ToolResponseEvent("c3.tool_response.1")},
			GetSessionStep{StepKey: "c3.get", SessionKey: key},
		},
	}
}

// CaseStateCRUD covers session state write/overwrite and state delta events.
func CaseStateCRUD() ReplayCase {
	key := SessionKeyFor("state_crud")
	return ReplayCase{
		Name:        "state_crud",
		Description: "session state write and overwrite",
		Steps: []Step{
			UpdateStateStep{
				StepKey: "c4.state.init", Scope: "session", SessionKey: key,
				State: session.StateMap{"color": []byte("red")},
			},
			UpdateStateStep{
				StepKey: "c4.state.overwrite", Scope: "session", SessionKey: key,
				State: session.StateMap{"color": []byte("blue")},
			},
			AppendEventStep{
				StepKey: "c4.state.delta", SessionKey: key,
				Event: StateDeltaEvent("c4.state.delta", map[string][]byte{"delta_key": []byte("delta")}),
			},
			GetSessionStep{StepKey: "c4.get", SessionKey: key},
		},
	}
}

// CaseMemoryWriteAndRead covers memory add and read.
func CaseMemoryWriteAndRead() ReplayCase {
	key := SessionKeyFor("memory_write_and_read")
	muk := MemoryUserKeyDefault()
	return ReplayCase{
		Name:         "memory_write_and_read",
		Description:  "memory write and read",
		RequiredCaps: Caps{NeedsMemory: true},
		Steps: []Step{
			// create a session so dual-backend path still has session snapshot
			AppendEventStep{StepKey: "c5.user.1", SessionKey: key, Event: UserEvent("c5.user.1", "remember me")},
			AddMemoryStep{StepKey: "c5.mem.add", UserKey: muk, Memory: "likes tea", Topics: []string{"prefs", "drink"}},
			CaptureMemoryStep{StepKey: "c5.mem.read", UserKey: muk, Limit: 10},
			GetSessionStep{StepKey: "c5.get", SessionKey: key},
		},
	}
}

// CaseSummaryGeneration covers full-session summary creation.
func CaseSummaryGeneration() ReplayCase {
	key := SessionKeyFor("summary_generation")
	return ReplayCase{
		Name:        "summary_generation",
		Description: "generate and persist full-session summary",
		Steps: []Step{
			AppendEventStep{StepKey: "c6.user.1", SessionKey: key, Event: UserEvent("c6.user.1", "hello")},
			AppendEventStep{StepKey: "c6.assistant.1", SessionKey: key, Event: AssistantEvent("c6.assistant.1", "hi")},
			CreateSummaryStep{StepKey: "c6.summary", SessionKey: key, FilterKey: "", Force: true},
			GetSessionStep{StepKey: "c6.get", SessionKey: key},
		},
	}
}

// CaseSummaryWithTruncation covers summary after longer history plus later events.
func CaseSummaryWithTruncation() ReplayCase {
	key := SessionKeyFor("summary_with_truncation")
	steps := []Step{}
	for i := 1; i <= 5; i++ {
		steps = append(steps,
			AppendEventStep{StepKey: "c7.user." + itoa(i), SessionKey: key, Event: UserEvent("c7.user."+itoa(i), "u"+itoa(i))},
			AppendEventStep{StepKey: "c7.assistant." + itoa(i), SessionKey: key, Event: AssistantEvent("c7.assistant."+itoa(i), "a"+itoa(i))},
		)
	}
	steps = append(steps,
		CreateSummaryStep{StepKey: "c7.summary", SessionKey: key, FilterKey: "", Force: true},
		AppendEventStep{StepKey: "c7.user.after", SessionKey: key, Event: UserEvent("c7.user.after", "after summary")},
		GetSessionStep{StepKey: "c7.get", SessionKey: key},
	)
	return ReplayCase{
		Name:        "summary_with_truncation",
		Description: "summary over long conversation then new events",
		Steps:       steps,
	}
}

// CaseSummaryFilterKey covers filter-key scoped summary ownership.
func CaseSummaryFilterKey() ReplayCase {
	key := SessionKeyFor("summary_filter_key")
	branch := "agent/child"
	return ReplayCase{
		Name:        "summary_filter_key",
		Description: "summary stored under specific filter key",
		Steps: []Step{
			AppendEventStep{StepKey: "c8.branch.1", SessionKey: key, Event: withFilter(UserEvent("c8.branch.1", "branch hello"), branch)},
			AppendEventStep{StepKey: "c8.branch.2", SessionKey: key, Event: withFilter(AssistantEvent("c8.branch.2", "branch reply"), branch)},
			CreateSummaryStep{StepKey: "c8.summary", SessionKey: key, FilterKey: branch, Force: true},
			GetSessionStep{StepKey: "c8.get", SessionKey: key},
		},
	}
}

// CaseTrackEvents covers track event persistence.
func CaseTrackEvents() ReplayCase {
	key := SessionKeyFor("track_events")
	return ReplayCase{
		Name:         "track_events",
		Description:  "append and read track events",
		RequiredCaps: Caps{NeedsTrack: true},
		Steps: []Step{
			AppendEventStep{StepKey: "c9.user.1", SessionKey: key, Event: UserEvent("c9.user.1", "start track")},
			AppendTrackStep{StepKey: "c9.track.1", SessionKey: key, Event: TrackPayload("tool", `{"step":1}`)},
			AppendTrackStep{StepKey: "c9.track.2", SessionKey: key, Event: TrackPayload("tool", `{"step":2}`)},
			GetSessionStep{StepKey: "c9.get", SessionKey: key},
		},
	}
}

// CaseConcurrentInterleaved covers branch-local order under true concurrent appends.
// Branches start together via ParallelGroupStep; each branch keeps local order.
func CaseConcurrentInterleaved() ReplayCase {
	key := SessionKeyFor("concurrent_interleaved")
	return ReplayCase{
		Name:             "concurrent_interleaved",
		EventCompareMode: EventCompareBranchLocal,
		Description:      "interleaved branches keep local order under concurrent append",
		Steps: []Step{
			// Ensure session exists before concurrent writers attach.
			AppendEventStep{StepKey: "c10.seed", SessionKey: key, Event: UserEvent("c10.seed", "seed")},
			ParallelGroupStep{
				StepKey: "c10.parallel",
				Branches: [][]Step{
					{
						AppendEventStep{StepKey: "c10.a.1", SessionKey: key, Event: BranchEvent("c10.a.1", "branchA", "a1")},
						AppendEventStep{StepKey: "c10.a.2", SessionKey: key, Event: BranchEvent("c10.a.2", "branchA", "a2")},
					},
					{
						AppendEventStep{StepKey: "c10.b.1", SessionKey: key, Event: BranchEvent("c10.b.1", "branchB", "b1")},
						AppendEventStep{StepKey: "c10.b.2", SessionKey: key, Event: BranchEvent("c10.b.2", "branchB", "b2")},
					},
				},
			},
			GetSessionStep{StepKey: "c10.get", SessionKey: key},
		},
		AllowedDiffs: []AllowedDiff{
			// Global interleaving is nondeterministic under concurrency; branch-local
			// mode already relaxes global order while preserving per-branch sequence.
			{PathPattern: "events[*].id", Rule: RuleIgnore, Reason: "global order may interleave differently across backends"},
		},
	}
}

// CaseRecoveryDuplicateEvent covers duplicate logical event writes after a reload boundary.
// Both appends share the same LogicalKey so normalizer/comparator treat them as the same
// logical identity; backends that do not dedupe will retain two physical events.
func CaseRecoveryDuplicateEvent() ReplayCase {
	key := SessionKeyFor("recovery_duplicate_event")
	logical := "c11.user.1"
	return ReplayCase{
		Name:        "recovery_duplicate_event",
		Description: "duplicate logical event append after recovery",
		Steps: []Step{
			AppendEventStep{StepKey: "c11.user.1", SessionKey: key, LogicalKey: logical, Event: UserEvent(logical, "once")},
			ReloadSessionStep{StepKey: "c11.reload", SessionKey: key},
			AppendEventStep{StepKey: "c11.user.1.dup", SessionKey: key, LogicalKey: logical, Event: UserEvent(logical, "once")},
			GetSessionStep{StepKey: "c11.get", SessionKey: key},
		},
	}
}

func withFilter(evt *event.Event, filterKey string) *event.Event {
	evt.FilterKey = filterKey
	evt.Branch = filterKey
	return evt
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
