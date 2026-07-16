// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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
		if snap.Session == nil || len(snap.Session.Events) == 0 {
			return fmt.Errorf("no events to drop")
		}
		snap.Session.Events = snap.Session.Events[:len(snap.Session.Events)-1]
	case FaultMutateLastContent:
		if snap.Session == nil || len(snap.Session.Events) == 0 {
			return fmt.Errorf("no events to mutate")
		}
		i := len(snap.Session.Events) - 1
		e := snap.Session.Events[i]
		if e.Response == nil || len(e.Response.Choices) == 0 {
			return fmt.Errorf("no content to mutate")
		}
		rsp := *e.Response
		rsp.Choices = append([]model.Choice(nil), e.Response.Choices...)
		msg := rsp.Choices[0].Message
		msg.Content = msg.Content + "|fault"
		rsp.Choices[0].Message = msg
		e.Response = &rsp
		snap.Session.Events[i] = e
	case FaultDropSummary:
		if snap.Session == nil {
			return fmt.Errorf("no session")
		}
		if len(snap.Session.Summaries) == 0 {
			return fmt.Errorf("no summaries to drop")
		}
		snap.Session.Summaries = map[string]*session.Summary{}
	case FaultOverwriteSummary:
		if snap.Session == nil {
			return fmt.Errorf("no session")
		}
		if snap.Session.Summaries == nil {
			snap.Session.Summaries = map[string]*session.Summary{}
		}
		if sum, ok := snap.Session.Summaries[""]; ok && sum != nil {
			cp := *sum
			cp.Summary = sum.Summary + "|overwrite"
			snap.Session.Summaries[""] = &cp
		} else {
			snap.Session.Summaries[""] = &session.Summary{Summary: "fault-overwrite"}
		}
	case FaultWrongSummaryFilterKey:
		if snap.Session == nil || snap.Session.Summaries == nil {
			return fmt.Errorf("no summaries")
		}
		var sum *session.Summary
		if v, ok := snap.Session.Summaries[""]; ok {
			sum = v
			delete(snap.Session.Summaries, "")
		} else {
			keys := make([]string, 0, len(snap.Session.Summaries))
			for k := range snap.Session.Summaries {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) == 0 {
				return fmt.Errorf("no summary to rekey")
			}
			k := keys[0]
			sum = snap.Session.Summaries[k]
			delete(snap.Session.Summaries, k)
		}
		if sum == nil {
			return fmt.Errorf("no summary to rekey")
		}
		snap.Session.Summaries["wrong-filter-key"] = sum
	case FaultMutateState:
		if snap.Session == nil {
			return fmt.Errorf("no session")
		}
		if snap.Session.State == nil {
			snap.Session.State = session.StateMap{}
		}
		snap.Session.State["color"] = []byte("fault-color")
	case FaultDropTrack:
		if snap.Session == nil {
			return fmt.Errorf("no session")
		}
		if len(snap.Session.Tracks) == 0 {
			return fmt.Errorf("no tracks to drop")
		}
		snap.Session.Tracks = map[session.Track]*session.TrackEvents{}
	case FaultMutateMemoryContent:
		if len(snap.Memories) == 0 || snap.Memories[0] == nil || snap.Memories[0].Memory == nil {
			return fmt.Errorf("no memory")
		}
		cp := *snap.Memories[0]
		m := *snap.Memories[0].Memory
		m.Memory = m.Memory + "|fault"
		cp.Memory = &m
		snap.Memories[0] = &cp
	case FaultDropMemory:
		if len(snap.Memories) == 0 {
			return fmt.Errorf("no memories to drop")
		}
		snap.Memories = nil
	case FaultReorderEvents:
		if snap.Session == nil || len(snap.Session.Events) < 2 {
			return fmt.Errorf("need >=2 events")
		}
		snap.Session.Events[0], snap.Session.Events[1] = snap.Session.Events[1], snap.Session.Events[0]
	case FaultDuplicateEvent:
		if snap.Session == nil || len(snap.Session.Events) == 0 {
			return fmt.Errorf("no events")
		}
		last := snap.Session.Events[len(snap.Session.Events)-1]
		snap.Session.Events = append(snap.Session.Events, last)
	default:
		return fmt.Errorf("unknown fault %q", kind)
	}
	return nil
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
