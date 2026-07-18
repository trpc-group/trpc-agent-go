// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"fmt"
)

// FaultKind names a deliberate inconsistency injection.
type FaultKind string

const (
	// FaultDropLastEvent removes the last event.
	FaultDropLastEvent FaultKind = "drop_last_event"
	// FaultMutateLastContent changes the last event content.
	FaultMutateLastContent FaultKind = "mutate_last_content"
	// FaultDropSummary removes all summaries.
	FaultDropSummary FaultKind = "drop_summary"
	// FaultOverwriteSummary overwrites summary text under the empty filter key.
	FaultOverwriteSummary FaultKind = "overwrite_summary"
	// FaultWrongSummaryFilterKey renames a summary to a wrong key.
	FaultWrongSummaryFilterKey FaultKind = "wrong_summary_filter_key"
	// FaultMutateState changes one session state value.
	FaultMutateState FaultKind = "mutate_state"
	// FaultDropTrack drops all track events.
	FaultDropTrack FaultKind = "drop_track"
	// FaultMutateMemoryContent mutates first memory content.
	FaultMutateMemoryContent FaultKind = "mutate_memory_content"
	// FaultDropMemory drops all memories.
	FaultDropMemory FaultKind = "drop_memory"
	// FaultReorderEvents swaps first two events when possible.
	FaultReorderEvents FaultKind = "reorder_events"
	// FaultDuplicateEvent appends a copy of the last event.
	FaultDuplicateEvent FaultKind = "duplicate_event"
)

// InjectFault mutates a snapshot in-place to simulate a backend inconsistency.
func InjectFault(snap *Snapshot, kind FaultKind) error {
	if snap == nil {
		return fmt.Errorf("nil snapshot")
	}
	switch kind {
	case FaultDropLastEvent:
		return faultDropLastEvent(snap)
	case FaultMutateLastContent:
		return faultMutateLastContent(snap)
	case FaultDropSummary:
		return faultDropSummary(snap)
	case FaultOverwriteSummary:
		return faultOverwriteSummary(snap)
	case FaultWrongSummaryFilterKey:
		return faultWrongSummaryFilterKey(snap)
	case FaultMutateState:
		return faultMutateState(snap)
	case FaultDropTrack:
		return faultDropTrack(snap)
	case FaultMutateMemoryContent:
		return faultMutateMemoryContent(snap)
	case FaultDropMemory:
		return faultDropMemory(snap)
	case FaultReorderEvents:
		return faultReorderEvents(snap)
	case FaultDuplicateEvent:
		return faultDuplicateEvent(snap)
	default:
		return fmt.Errorf("unknown fault %q", kind)
	}
}

// AllFaultKinds returns the public fault set used by detection tests.
func AllFaultKinds() []FaultKind {
	return []FaultKind{
		FaultDropLastEvent,
		FaultMutateLastContent,
		FaultDropSummary,
		FaultOverwriteSummary,
		FaultWrongSummaryFilterKey,
		FaultMutateState,
		FaultDropTrack,
		FaultMutateMemoryContent,
		FaultDropMemory,
		FaultReorderEvents,
		FaultDuplicateEvent,
	}
}
