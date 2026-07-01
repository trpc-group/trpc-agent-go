//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"encoding/json"
	"fmt"
	"sort"
)

// missingValue marks a field present on one side but absent on the other.
const missingValue = "<missing>"

// Diff is a single field-level discrepancy between two normalized snapshots.
type Diff struct {
	Category      string  `json:"category"`
	Locator       Locator `json:"locator"`
	FieldPath     string  `json:"fieldPath"`
	BaselineValue string  `json:"baselineValue"`
	CompareValue  string  `json:"compareValue"`
}

// Compare walks two normalized snapshots field by field and returns every
// discrepancy. Both snapshots must already be passed through Normalize so that
// volatile fields (IDs, timestamps, ordering) do not produce spurious diffs.
func Compare(caseName, backend string, baseline, other *Snapshot) []Diff {
	var diffs []Diff
	diffs = append(diffs, compareEvents(baseline, other)...)
	diffs = append(diffs, compareState(baseline, other)...)
	diffs = append(diffs, compareMemories(baseline, other)...)
	diffs = append(diffs, compareSummaries(baseline, other)...)
	diffs = append(diffs, compareTracks(baseline, other)...)
	return diffs
}

func compareEvents(baseline, other *Snapshot) []Diff {
	var diffs []Diff
	n := max(len(baseline.Events), len(other.Events))
	for i := 0; i < n; i++ {
		idx := i
		loc := Locator{SessionID: baseline.SessionID, EventIndex: &idx}
		if i >= len(baseline.Events) {
			diffs = append(diffs, Diff{
				Category:      "event",
				Locator:       loc,
				FieldPath:     fmt.Sprintf("events[%d]", i),
				BaselineValue: missingValue,
				CompareValue:  renderEvent(other.Events[i]),
			})
			continue
		}
		if i >= len(other.Events) {
			diffs = append(diffs, Diff{
				Category:      "event",
				Locator:       loc,
				FieldPath:     fmt.Sprintf("events[%d]", i),
				BaselineValue: renderEvent(baseline.Events[i]),
				CompareValue:  missingValue,
			})
			continue
		}
		diffs = append(diffs, compareEventFields(loc, i, baseline.Events[i], other.Events[i])...)
	}
	return diffs
}

func compareEventFields(loc Locator, i int, a, b EventView) []Diff {
	var diffs []Diff
	add := func(field, av, bv string) {
		if av != bv {
			diffs = append(diffs, Diff{
				Category:      "event",
				Locator:       loc,
				FieldPath:     fmt.Sprintf("events[%d].%s", i, field),
				BaselineValue: av,
				CompareValue:  bv,
			})
		}
	}
	add("author", a.Author, b.Author)
	add("role", a.Role, b.Role)
	add("content", a.Content, b.Content)
	add("toolID", a.ToolID, b.ToolID)
	add("branch", a.Branch, b.Branch)
	add("tag", a.Tag, b.Tag)
	add("filterKey", a.FilterKey, b.FilterKey)
	add("toolCalls", renderJSON(a.ToolCalls), renderJSON(b.ToolCalls))
	add("stateDelta", renderJSON(a.StateDelta), renderJSON(b.StateDelta))
	add("extensions", renderJSON(a.Extensions), renderJSON(b.Extensions))
	return diffs
}

func compareState(baseline, other *Snapshot) []Diff {
	var diffs []Diff
	keys := unionKeys(baseline.State, other.State)
	for _, k := range keys {
		av, aok := baseline.State[k]
		bv, bok := other.State[k]
		loc := Locator{SessionID: baseline.SessionID}
		switch {
		case aok && !bok:
			diffs = append(diffs, Diff{Category: "state", Locator: loc,
				FieldPath: "state." + k, BaselineValue: av, CompareValue: missingValue})
		case !aok && bok:
			diffs = append(diffs, Diff{Category: "state", Locator: loc,
				FieldPath: "state." + k, BaselineValue: missingValue, CompareValue: bv})
		case av != bv:
			diffs = append(diffs, Diff{Category: "state", Locator: loc,
				FieldPath: "state." + k, BaselineValue: av, CompareValue: bv})
		}
	}
	return diffs
}

func compareMemories(baseline, other *Snapshot) []Diff {
	var diffs []Diff
	n := max(len(baseline.Memories), len(other.Memories))
	for i := 0; i < n; i++ {
		switch {
		case i >= len(baseline.Memories):
			b := other.Memories[i]
			diffs = append(diffs, Diff{Category: "memory",
				Locator:       Locator{SessionID: baseline.SessionID, MemoryID: b.ID},
				FieldPath:     fmt.Sprintf("memories[%d]", i),
				BaselineValue: missingValue, CompareValue: renderJSON(b)})
		case i >= len(other.Memories):
			a := baseline.Memories[i]
			diffs = append(diffs, Diff{Category: "memory",
				Locator:       Locator{SessionID: baseline.SessionID, MemoryID: a.ID},
				FieldPath:     fmt.Sprintf("memories[%d]", i),
				BaselineValue: renderJSON(a), CompareValue: missingValue})
		default:
			diffs = append(diffs, compareMemoryFields(baseline.SessionID, i, baseline.Memories[i], other.Memories[i])...)
		}
	}
	return diffs
}

func compareMemoryFields(sessionID string, i int, a, b MemoryView) []Diff {
	var diffs []Diff
	loc := Locator{SessionID: sessionID, MemoryID: a.ID}
	add := func(field, av, bv string) {
		if av != bv {
			diffs = append(diffs, Diff{Category: "memory", Locator: loc,
				FieldPath: fmt.Sprintf("memories[%d].%s", i, field), BaselineValue: av, CompareValue: bv})
		}
	}
	add("content", a.Content, b.Content)
	add("kind", a.Kind, b.Kind)
	add("topics", renderJSON(a.Topics), renderJSON(b.Topics))
	add("score", fmt.Sprintf("%v", a.Score), fmt.Sprintf("%v", b.Score))
	add("metadata", renderJSON(a.Metadata), renderJSON(b.Metadata))
	return diffs
}

func compareSummaries(baseline, other *Snapshot) []Diff {
	var diffs []Diff
	baseByKey := indexSummaries(baseline.Summaries)
	otherByKey := indexSummaries(other.Summaries)
	for _, fk := range unionSummaryKeys(baseline.Summaries, other.Summaries) {
		key := fk
		loc := Locator{SessionID: baseline.SessionID, SummaryFilterKey: &key}
		a, aok := baseByKey[fk]
		b, bok := otherByKey[fk]
		switch {
		case aok && !bok:
			diffs = append(diffs, Diff{Category: "summary", Locator: loc,
				FieldPath: fmt.Sprintf("summaries[%q]", fk), BaselineValue: a.Text, CompareValue: missingValue})
		case !aok && bok:
			diffs = append(diffs, Diff{Category: "summary", Locator: loc,
				FieldPath: fmt.Sprintf("summaries[%q]", fk), BaselineValue: missingValue, CompareValue: b.Text})
		default:
			diffs = append(diffs, compareSummaryFields(loc, fk, a, b)...)
		}
	}
	return diffs
}

func compareSummaryFields(loc Locator, fk string, a, b SummaryView) []Diff {
	var diffs []Diff
	add := func(field, av, bv string) {
		if av != bv {
			diffs = append(diffs, Diff{Category: "summary", Locator: loc,
				FieldPath: fmt.Sprintf("summaries[%q].%s", fk, field), BaselineValue: av, CompareValue: bv})
		}
	}
	add("text", a.Text, b.Text)
	add("version", fmt.Sprintf("%d", a.Version), fmt.Sprintf("%d", b.Version))
	add("sessionID", a.SessionID, b.SessionID)
	add("topics", renderJSON(a.Topics), renderJSON(b.Topics))
	return diffs
}

func compareTracks(baseline, other *Snapshot) []Diff {
	var diffs []Diff
	n := max(len(baseline.Tracks), len(other.Tracks))
	for i := 0; i < n; i++ {
		switch {
		case i >= len(baseline.Tracks):
			b := other.Tracks[i]
			diffs = append(diffs, Diff{Category: "track",
				Locator:       Locator{SessionID: baseline.SessionID, TrackName: b.Name},
				FieldPath:     fmt.Sprintf("tracks[%d]", i),
				BaselineValue: missingValue, CompareValue: renderJSON(b)})
		case i >= len(other.Tracks):
			a := baseline.Tracks[i]
			diffs = append(diffs, Diff{Category: "track",
				Locator:       Locator{SessionID: baseline.SessionID, TrackName: a.Name},
				FieldPath:     fmt.Sprintf("tracks[%d]", i),
				BaselineValue: renderJSON(a), CompareValue: missingValue})
		default:
			a, b := baseline.Tracks[i], other.Tracks[i]
			loc := Locator{SessionID: baseline.SessionID, TrackName: a.Name}
			if a.Name != b.Name {
				diffs = append(diffs, Diff{Category: "track", Locator: loc,
					FieldPath: fmt.Sprintf("tracks[%d].name", i), BaselineValue: a.Name, CompareValue: b.Name})
			}
			if renderJSON(a.Payload) != renderJSON(b.Payload) {
				diffs = append(diffs, Diff{Category: "track", Locator: loc,
					FieldPath:     fmt.Sprintf("tracks[%d].payload", i),
					BaselineValue: renderJSON(a.Payload), CompareValue: renderJSON(b.Payload)})
			}
		}
	}
	return diffs
}

func indexSummaries(sums []SummaryView) map[string]SummaryView {
	out := make(map[string]SummaryView, len(sums))
	for _, s := range sums {
		out[s.FilterKey] = s
	}
	return out
}

func unionSummaryKeys(a, b []SummaryView) []string {
	seen := make(map[string]struct{})
	var keys []string
	for _, s := range a {
		if _, ok := seen[s.FilterKey]; !ok {
			seen[s.FilterKey] = struct{}{}
			keys = append(keys, s.FilterKey)
		}
	}
	for _, s := range b {
		if _, ok := seen[s.FilterKey]; !ok {
			seen[s.FilterKey] = struct{}{}
			keys = append(keys, s.FilterKey)
		}
	}
	return keys
}

func unionKeys(a, b map[string]string) []string {
	seen := make(map[string]struct{})
	var keys []string
	for k := range a {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func renderEvent(e EventView) string {
	return renderJSON(e)
}

func renderJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}
