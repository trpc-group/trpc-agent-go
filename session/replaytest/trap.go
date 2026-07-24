//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import "time"

// TrapInjector defines a trap injection strategy for testing the comparator.
// It deliberately mutates a BackendResult to simulate a backend inconsistency,
// and the harness verifies that the comparator can detect the injected difference.
type TrapInjector struct {
	// Name is a human-readable name for the trap, e.g. "swap_event_order".
	Name string `json:"name"`
	// Description describes what the trap does.
	Description string `json:"description,omitempty"`
	// Inject is the mutation function that tampers with the result.
	Inject func(result *BackendResult) `json:"-"`
	// ExpectKeys lists the field paths that should appear in the diff report.
	ExpectKeys []string `json:"expectKeys,omitempty"`
	// ExpectCount is the expected number of diffs detected.
	ExpectCount int `json:"expectCount"`
}

// Predefined trap injectors.

// TrapSwapEventOrder swaps the order of two events in a session.
func TrapSwapEventOrder() TrapInjector {
	return TrapInjector{
		Name:        "swap_event_order",
		Description: "Swap the first two events in the session to verify event order detection.",
		Inject: func(result *BackendResult) {
			if result.Session == nil || len(result.Session.Events) < 2 {
				return
			}
			result.Session.Events[0], result.Session.Events[1] = result.Session.Events[1], result.Session.Events[0]
		},
		ExpectKeys:  []string{"events[0].content", "events[1].content"},
		ExpectCount: 2,
	}
}

// TrapAlterMemoryContent alters the content of the first memory entry.
func TrapAlterMemoryContent() TrapInjector {
	return TrapInjector{
		Name:        "alter_memory_content",
		Description: "Change the text of the first memory entry.",
		Inject: func(result *BackendResult) {
			if len(result.Memories) == 0 || result.Memories[0] == nil || result.Memories[0].Memory == nil {
				return
			}
			result.Memories[0].Memory.Memory = result.Memories[0].Memory.Memory + "_tampered"
		},
		ExpectKeys:  []string{"memories[0].memory.memory"},
		ExpectCount: 1,
	}
}

// TrapRemoveSummary removes the first summary entry from the session.
func TrapRemoveSummary() TrapInjector {
	return TrapInjector{
		Name:        "remove_summary",
		Description: "Delete the first summary from the session summaries map.",
		Inject: func(result *BackendResult) {
			if result.Session == nil || len(result.Session.Summaries) == 0 {
				return
			}
			for k := range result.Session.Summaries {
				delete(result.Session.Summaries, k)
				break
			}
		},
		ExpectKeys:  []string{"session.summaries"},
		ExpectCount: 1,
	}
}

// TrapShiftTimestamp shifts all event timestamps by 10 seconds.
func TrapShiftTimestamp() TrapInjector {
	return TrapInjector{
		Name:        "shift_timestamp",
		Description: "Shift all event timestamps by +10 seconds to exceed the allowed ±1s tolerance.",
		Inject: func(result *BackendResult) {
			if result.Session == nil {
				return
			}
			for i := range result.Session.Events {
				result.Session.Events[i].Timestamp = result.Session.Events[i].Timestamp.Add(10 * time.Second)
			}
		},
		ExpectKeys:  []string{"events[0].timestamp"},
		ExpectCount: -1, // dynamic: depends on event count
	}
}

// TrapAlterStateValue alters the value of a session state key.
func TrapAlterStateValue() TrapInjector {
	return TrapInjector{
		Name:        "alter_state_value",
		Description: "Change the value of the first state key.",
		Inject: func(result *BackendResult) {
			if result.Session == nil || result.Session.State == nil {
				return
			}
			for k, v := range result.Session.State {
				result.Session.State[k] = append(v, []byte("_tampered")...)
				break
			}
		},
		ExpectKeys:  []string{"session.state"},
		ExpectCount: 1,
	}
}

// TrapDuplicateEvent duplicates the first event in the session.
func TrapDuplicateEvent() TrapInjector {
	return TrapInjector{
		Name:        "duplicate_event",
		Description: "Duplicate the first event to create an extra event.",
		Inject: func(result *BackendResult) {
			if result.Session == nil || len(result.Session.Events) == 0 {
				return
			}
			dup := result.Session.Events[0]
			result.Session.Events = append(result.Session.Events, dup)
		},
		ExpectKeys:  []string{"events"},
		ExpectCount: 1,
	}
}

// TrapAlterFilterKey alters the filter-key of the first summary.
func TrapAlterFilterKey() TrapInjector {
	return TrapInjector{
		Name:        "alter_filter_key",
		Description: "Change the filter-key of the first summary.",
		Inject: func(result *BackendResult) {
			if result.Session == nil || len(result.Session.Summaries) == 0 {
				return
			}
			for k := range result.Session.Summaries {
				if result.Session.Summaries[k] != nil && result.Session.Summaries[k].Boundary != nil {
					result.Session.Summaries[k].Boundary.FilterKey = "_tampered"
				}
				break
			}
		},
		ExpectKeys:  []string{"session.summaries"},
		ExpectCount: 1,
	}
}

// PredefinedTraps returns all predefined trap injectors.
func PredefinedTraps() []TrapInjector {
	return []TrapInjector{
		TrapSwapEventOrder(),
		TrapAlterMemoryContent(),
		TrapRemoveSummary(),
		TrapShiftTimestamp(),
		TrapAlterStateValue(),
		TrapDuplicateEvent(),
		TrapAlterFilterKey(),
	}
}
