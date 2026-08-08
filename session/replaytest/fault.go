//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"fmt"
)

// FaultKind enumerates deterministic snapshot mutations used to verify the
// comparator detects injected inconsistencies on real backends (acceptance
// criterion #2: 100% detection of injected inconsistencies; criterion #4:
// summary loss/overwrite/wrong-session/wrong-filter-key).
type FaultKind string

// Supported fault kinds for deterministic fault injection.
const (
	FaultDropMemory          FaultKind = "drop_memory"
	FaultDropSummary         FaultKind = "drop_summary"
	FaultOverwriteSummary    FaultKind = "overwrite_summary"
	FaultWrongSummaryFilter  FaultKind = "wrong_summary_filter"
	FaultWrongSummarySession FaultKind = "wrong_summary_session"
	FaultDropTrack           FaultKind = "drop_track"
	FaultDropEvent           FaultKind = "drop_event"
	FaultCorruptState        FaultKind = "corrupt_state"
)

// FaultKinds lists every supported injection, so a test can iterate over all.
var FaultKinds = []FaultKind{
	FaultDropMemory,
	FaultDropSummary,
	FaultOverwriteSummary,
	FaultWrongSummaryFilter,
	FaultWrongSummarySession,
	FaultDropTrack,
	FaultDropEvent,
	FaultCorruptState,
}

// InjectFault returns a deep copy of snapshot with one deterministic
// inconsistency injected per kind. An error is returned when the snapshot
// has no target for that fault (e.g. no summaries to drop), which callers
// treat as "not applicable to this case".
func InjectFault(snapshot Snapshot, kind FaultKind) (Snapshot, error) {
	clone, err := cloneSnapshot(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	switch kind {
	case FaultDropMemory:
		if len(clone.Memories) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no memories to drop", kind)
		}
		clone.Memories = clone.Memories[1:]
	case FaultDropSummary:
		if len(clone.Summaries) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no summaries to drop", kind)
		}
		clone.Summaries = nil
	case FaultOverwriteSummary:
		if len(clone.Summaries) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no summaries to overwrite", kind)
		}
		clone.Summaries[0].Summary = "INJECTED-different-summary"
	case FaultWrongSummaryFilter:
		if len(clone.Summaries) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no summaries to re-filter", kind)
		}
		clone.Summaries[0].FilterKey = "INJECTED-wrong-filter"
	case FaultWrongSummarySession:
		if len(clone.Summaries) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no summaries to re-attribute", kind)
		}
		// The summary was recorded under another session's key. In the
		// normalized view the session dimension is carried by the
		// filter key, so this surfaces as a cross-session mismatch.
		clone.Summaries[0].FilterKey = "INJECTED-other-session|default"
	case FaultDropTrack:
		if len(clone.Tracks) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no tracks to drop", kind)
		}
		clone.Tracks = clone.Tracks[1:]
	case FaultDropEvent:
		if len(clone.Events) == 0 {
			return Snapshot{}, fmt.Errorf("%s: no events to drop", kind)
		}
		clone.Events = clone.Events[1:]
	case FaultCorruptState:
		if clone.State == nil {
			clone.State = map[string]string{}
		}
		clone.State["__injected__"] = "value"
	default:
		return Snapshot{}, fmt.Errorf("unknown fault kind %q", kind)
	}
	return clone, nil
}

// cloneSnapshot deep-copies a Snapshot via JSON round-trip.
func cloneSnapshot(s Snapshot) (Snapshot, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return Snapshot{}, err
	}
	var out Snapshot
	if err := json.Unmarshal(data, &out); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}
