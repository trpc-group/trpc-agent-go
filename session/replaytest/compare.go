//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Run executes all cases against all backends and returns a diff report.
func Run(ctx context.Context, cases []ReplayCase, backends []Backend) (*Report, error) {
	report := &Report{
		GeneratedAt: time.Now().UTC(),
		Cases:       make([]CaseReport, 0, len(cases)),
	}
	if len(backends) == 0 {
		return report, nil
	}
	report.BaseBackend = backends[0].Name()
	for _, c := range cases {
		caseReport := CaseReport{
			Case:      c.Name,
			SessionID: c.Key.SessionID,
		}
		var base *Snapshot
		for i, backend := range backends {
			snapshot, err := backend.Apply(ctx, c)
			if err != nil {
				return nil, fmt.Errorf("run case %s on backend %s: %w", c.Name, backend.Name(), err)
			}
			caseReport.Compared = append(caseReport.Compared, backend.Name())
			if len(snapshot.Unsupported) > 0 {
				caseReport.Unsupported = append(caseReport.Unsupported, BackendUnsupported{
					Backend:     backend.Name(),
					Unsupported: snapshot.Unsupported,
				})
			}
			caseReport.Differences = append(
				caseReport.Differences,
				ValidateReplaySnapshot(snapshot, c)...,
			)
			if i == 0 {
				base = snapshot
				continue
			}
			caseReport.Differences = append(
				caseReport.Differences,
				CompareSnapshots(base, snapshot)...,
			)
		}
		report.Cases = append(report.Cases, caseReport)
	}
	return report, nil
}

// CompareSnapshots returns all normalized differences between base and compare.
func CompareSnapshots(base, compare *Snapshot) []Difference {
	if base == nil || compare == nil {
		return nil
	}
	var diffs []Difference
	add := func(path, locator string, b, c any, explanation string) {
		if reflect.DeepEqual(b, c) {
			return
		}
		allowed, allowedReason := allowedByUnsupported(compare, path)
		if !allowed {
			allowed, allowedReason = allowedByUnsupported(base, path)
		}
		if allowedReason != "" {
			explanation = allowedReason
		}
		diffs = append(diffs, Difference{
			Case:         base.Case,
			Backend:      compare.Backend,
			SessionID:    base.SessionID,
			Locator:      locator,
			FieldPath:    path,
			BaseValue:    b,
			CompareValue: c,
			AllowedDiff:  allowed,
			Explanation:  explanation,
		})
	}
	add("$.session_id", "session", base.SessionID, compare.SessionID, "session ownership changed")
	add("$.app_name", "session", base.AppName, compare.AppName, "session ownership changed")
	add("$.user_id", "session", base.UserID, compare.UserID, "session ownership changed")
	add("$.event_order", "event_order", base.EventOrder, compare.EventOrder, "raw event replay order mismatch")
	compareSlices("$.events", base.Events, compare.Events,
		func(i int, b, c *NormalizedEvent) string {
			return fmt.Sprintf("event[%d]", i)
		},
		func(i int, field, locator string, b, c any) {
			add(fmt.Sprintf("$.events[%d].%s", i, field), locator, b, c, "event replay mismatch")
		})
	compareMaps("$.state", base.State, compare.State, func(key, field string, b, c any) {
		add(fmt.Sprintf("$.state[%q].%s", key, field), "state:"+key, b, c, "state final value mismatch")
	})
	compareSlices("$.memories", base.Memories, compare.Memories,
		func(i int, b, c *NormalizedMemory) string {
			if b != nil {
				return "memory:" + b.ID
			}
			if c != nil {
				return "memory:" + c.ID
			}
			return fmt.Sprintf("memory[%d]", i)
		},
		func(i int, field, locator string, b, c any) {
			add(fmt.Sprintf("$.memories[%d].%s", i, field), locator, b, c, "memory replay mismatch")
		})
	compareSlices("$.memory_queries", base.MemoryQuery, compare.MemoryQuery,
		func(i int, b, c *NormalizedMemoryQuery) string {
			if b != nil {
				return "memory_query:" + b.Name
			}
			if c != nil {
				return "memory_query:" + c.Name
			}
			return fmt.Sprintf("memory_query[%d]", i)
		},
		func(i int, field, locator string, b, c any) {
			add(fmt.Sprintf("$.memory_queries[%d].%s", i, field), locator, b, c, "memory retrieval mismatch")
		})
	compareSlices("$.summaries", base.Summaries, compare.Summaries,
		func(i int, b, c *NormalizedSummary) string {
			if b != nil {
				return "summary:" + b.FilterKey
			}
			if c != nil {
				return "summary:" + c.FilterKey
			}
			return fmt.Sprintf("summary[%d]", i)
		},
		func(i int, field, locator string, b, c any) {
			add(fmt.Sprintf("$.summaries[%d].%s", i, field), locator, b, c, "summary replay mismatch")
		})
	compareSlices("$.tracks", base.Tracks, compare.Tracks,
		func(i int, b, c *NormalizedTrack) string {
			if b != nil {
				return "track:" + b.Name
			}
			if c != nil {
				return "track:" + c.Name
			}
			return fmt.Sprintf("track[%d]", i)
		},
		func(i int, field, locator string, b, c any) {
			add(fmt.Sprintf("$.tracks[%d].%s", i, field), locator, b, c, "track replay mismatch")
		})
	return diffs
}

// ValidateSnapshot returns semantic invariant failures within one backend
// snapshot. These checks catch cases where every compared backend makes the
// same replay mistake, which pairwise diffing alone cannot detect.
func ValidateSnapshot(snapshot *Snapshot) []Difference {
	return ValidateReplaySnapshot(snapshot, ReplayCase{})
}

// ValidateReplaySnapshot validates one backend snapshot against replay-level
// invariants and deterministic expectations derived from the input case.
func ValidateReplaySnapshot(snapshot *Snapshot, c ReplayCase) []Difference {
	if snapshot == nil {
		return nil
	}
	seen := make(map[string]int, len(snapshot.Events))
	var diffs []Difference
	for i, evt := range snapshot.Events {
		if evt.ID == "" {
			continue
		}
		first, ok := seen[evt.ID]
		if !ok {
			seen[evt.ID] = i
			continue
		}
		diffs = append(diffs, Difference{
			Case:         snapshot.Case,
			Backend:      snapshot.Backend,
			SessionID:    snapshot.SessionID,
			Locator:      fmt.Sprintf("event[%d]", i),
			FieldPath:    fmt.Sprintf("$.events[%d].id", i),
			BaseValue:    fmt.Sprintf("first seen at event[%d]", first),
			CompareValue: evt.ID,
			AllowedDiff:  false,
			Explanation:  "event replay invariant failed: duplicate event id after retry/recovery",
		})
	}
	diffs = append(diffs, validateExpectedState(snapshot, c)...)
	diffs = append(diffs, validateExpectedMemories(snapshot, c)...)
	diffs = append(diffs, validateExpectedSummaries(snapshot, c)...)
	return diffs
}

func allowedByUnsupported(snapshot *Snapshot, path string) (bool, string) {
	if snapshot == nil {
		return false, ""
	}
	for _, feature := range snapshot.Unsupported {
		if !feature.AllowedDiff {
			continue
		}
		if unsupportedPathMatches(feature.Capability, path) {
			return true, feature.Explanation
		}
	}
	return false, ""
}

func unsupportedPathMatches(cap Capability, path string) bool {
	switch cap {
	case CapabilityTrack:
		return strings.HasPrefix(path, "$.tracks")
	case CapabilityMemorySearch:
		return strings.HasPrefix(path, "$.memory_queries")
	case CapabilityStateDelete, CapabilityStateClear:
		return strings.HasPrefix(path, "$.state") && strings.HasSuffix(path, ".presence")
	default:
		return false
	}
}

func validateExpectedState(snapshot *Snapshot, c ReplayCase) []Difference {
	expected, ok := expectedState(c)
	if !ok {
		return nil
	}
	var diffs []Difference
	add := func(path, locator string, want, got any) {
		if reflect.DeepEqual(want, got) {
			return
		}
		allowed, explanation := allowedByUnsupported(snapshot, path)
		if explanation == "" {
			explanation = "state replay invariant failed: final state does not match replay operations"
		}
		diffs = append(diffs, Difference{
			Case:         snapshot.Case,
			Backend:      snapshot.Backend,
			SessionID:    snapshot.SessionID,
			Locator:      locator,
			FieldPath:    path,
			BaseValue:    want,
			CompareValue: got,
			AllowedDiff:  allowed,
			Explanation:  explanation,
		})
	}
	seen := map[string]struct{}{}
	for key, want := range expected {
		seen[key] = struct{}{}
		got, ok := snapshot.State[key]
		if !ok {
			add(fmt.Sprintf("$.state[%q].presence", key), "state:"+key, true, false)
			continue
		}
		if want.Kind != got.Kind {
			add(fmt.Sprintf("$.state[%q].kind", key), "state:"+key, want.Kind, got.Kind)
		}
		if want.Value != got.Value {
			add(fmt.Sprintf("$.state[%q].value", key), "state:"+key, want.Value, got.Value)
		}
	}
	extras := make([]string, 0, len(snapshot.State))
	for key := range snapshot.State {
		if _, ok := seen[key]; !ok {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		add(fmt.Sprintf("$.state[%q].presence", key), "state:"+key, false, true)
	}
	return diffs
}

func validateExpectedMemories(snapshot *Snapshot, c ReplayCase) []Difference {
	expected := expectedMemories(c)
	if expected == nil {
		return nil
	}
	got := make(map[string]NormalizedMemory, len(snapshot.Memories))
	var diffs []Difference
	for i, mem := range snapshot.Memories {
		if prev, ok := got[mem.ID]; ok {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "memory:" + mem.ID,
				FieldPath:    fmt.Sprintf("$.memories[%d].id", i),
				BaseValue:    prev.ID,
				CompareValue: mem.ID,
				AllowedDiff:  false,
				Explanation:  "memory replay invariant failed: duplicate memory id after retry/recovery",
			})
			continue
		}
		got[mem.ID] = mem
	}
	for _, want := range expected {
		mem, ok := got[want.ID]
		if !ok {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "memory:" + want.ID,
				FieldPath:    fmt.Sprintf("$.memories[%q].presence", want.ID),
				BaseValue:    true,
				CompareValue: false,
				AllowedDiff:  false,
				Explanation:  "memory replay invariant failed: expected memory is missing",
			})
			continue
		}
		if mem.Content != want.Content {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "memory:" + want.ID,
				FieldPath:    fmt.Sprintf("$.memories[%q].content", want.ID),
				BaseValue:    want.Content,
				CompareValue: mem.Content,
				AllowedDiff:  false,
				Explanation:  "memory replay invariant failed: memory content changed",
			})
		}
		if !reflect.DeepEqual(mem.Topics, want.Topics) {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "memory:" + want.ID,
				FieldPath:    fmt.Sprintf("$.memories[%q].topics", want.ID),
				BaseValue:    want.Topics,
				CompareValue: mem.Topics,
				AllowedDiff:  false,
				Explanation:  "memory replay invariant failed: memory topics changed",
			})
		}
		if !reflect.DeepEqual(mem.Metadata, want.Metadata) {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "memory:" + want.ID,
				FieldPath:    fmt.Sprintf("$.memories[%q].metadata", want.ID),
				BaseValue:    want.Metadata,
				CompareValue: mem.Metadata,
				AllowedDiff:  false,
				Explanation:  "memory replay invariant failed: memory metadata changed",
			})
		}
	}
	for _, mem := range snapshot.Memories {
		if _, ok := expected[mem.ID]; ok {
			continue
		}
		diffs = append(diffs, Difference{
			Case:         snapshot.Case,
			Backend:      snapshot.Backend,
			SessionID:    snapshot.SessionID,
			Locator:      "memory:" + mem.ID,
			FieldPath:    fmt.Sprintf("$.memories[%q].presence", mem.ID),
			BaseValue:    false,
			CompareValue: true,
			AllowedDiff:  false,
			Explanation:  "memory replay invariant failed: unexpected memory remained after delete/clear/retry",
		})
	}
	return diffs
}

func expectedState(c ReplayCase) (map[string]NormalizedValue, bool) {
	if len(c.Operations) == 0 {
		return nil, false
	}
	state := make(map[string]NormalizedValue)
	changed := applyExpectedStateOperations(state, c.Operations)
	if !changed {
		return nil, false
	}
	return state, true
}

func expectedMemories(c ReplayCase) map[string]NormalizedMemory {
	if len(c.Operations) == 0 {
		return nil
	}
	memoriesByStableID := make(map[string]NormalizedMemory)
	if !applyExpectedMemoryOperations(memoriesByStableID, c.Key, c.Operations) {
		return nil
	}
	memoriesByID := make(map[string]NormalizedMemory, len(memoriesByStableID))
	for _, mem := range memoriesByStableID {
		memoriesByID[mem.ID] = mem
	}
	return memoriesByID
}

func applyExpectedMemoryOperations(
	memories map[string]NormalizedMemory,
	key session.Key,
	ops []Operation,
) bool {
	changed := false
	logicalToStable := map[string]string{}
	for stable, mem := range memories {
		logicalToStable[mem.ID] = stable
	}
	changed = applyExpectedMemoryOperationsWithIndex(memories, logicalToStable, key, ops)
	return changed
}

func applyExpectedMemoryOperationsWithIndex(
	memories map[string]NormalizedMemory,
	logicalToStable map[string]string,
	key session.Key,
	ops []Operation,
) bool {
	changed := false
	for _, op := range ops {
		switch op.Kind {
		case OpAddMemory, OpUpdateMemory:
			if op.Memory == nil {
				continue
			}
			if oldStable := logicalToStable[op.Memory.ID]; oldStable != "" {
				delete(memories, oldStable)
			}
			mem := expectedMemoryFromSpec(key, op.Memory)
			memories[mem.StableID] = mem
			if op.Memory.ID != "" {
				logicalToStable[op.Memory.ID] = mem.StableID
			}
			changed = true
		case OpDeleteMemory:
			if op.Memory == nil {
				continue
			}
			if stable := logicalToStable[op.Memory.ID]; stable != "" {
				delete(memories, stable)
				delete(logicalToStable, op.Memory.ID)
			}
			changed = true
		case OpClearMemory:
			for stable := range memories {
				delete(memories, stable)
			}
			for logical := range logicalToStable {
				delete(logicalToStable, logical)
			}
			changed = true
		case OpConcurrent:
			changed = applyExpectedMemoryOperationsWithIndex(memories, logicalToStable, key, op.Concurrent) || changed
		}
	}
	return changed
}

func expectedMemoryFromSpec(key session.Key, spec *MemorySpec) NormalizedMemory {
	topics := append([]string{}, spec.Topics...)
	sort.Strings(topics)
	kind := memory.KindFact
	metadata := map[string]string{"kind": string(kind)}
	if spec.Metadata != nil {
		if spec.Metadata.Kind != "" {
			kind = spec.Metadata.Kind
			metadata["kind"] = string(kind)
		}
		if spec.Metadata.EventTime != nil && !spec.Metadata.EventTime.IsZero() {
			metadata["event_time"] = spec.Metadata.EventTime.UTC().Format(time.RFC3339Nano)
		}
		if len(spec.Metadata.Participants) > 0 {
			participants := append([]string{}, spec.Metadata.Participants...)
			sort.Strings(participants)
			metadata["participants"] = strings.Join(participants, ",")
		}
		if spec.Metadata.Location != "" {
			metadata["location"] = spec.Metadata.Location
		}
	}
	stable := stableID(key.AppName, key.UserID, spec.Content, strings.Join(topics, ","), string(kind))
	id := stable
	if spec.ID != "" {
		id = spec.ID
	}
	return NormalizedMemory{
		ID:       id,
		StableID: stable,
		Content:  spec.Content,
		Topics:   topics,
		Metadata: metadata,
		Scope:    key.AppName + "/" + key.UserID,
	}
}

func validateExpectedSummaries(snapshot *Snapshot, c ReplayCase) []Difference {
	expected := expectedSummaries(c)
	if len(expected) == 0 {
		return nil
	}
	got := make(map[string]NormalizedSummary, len(snapshot.Summaries))
	for _, summary := range snapshot.Summaries {
		got[summary.FilterKey] = summary
	}
	var diffs []Difference
	for _, want := range expected {
		summary, ok := got[want.FilterKey]
		if !ok {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].presence", want.FilterKey),
				BaseValue:    true,
				CompareValue: false,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: expected summary filter-key is missing",
			})
			continue
		}
		if summary.SessionID != c.Key.SessionID {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].session_id", want.FilterKey),
				BaseValue:    c.Key.SessionID,
				CompareValue: summary.SessionID,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: summary belongs to the wrong session",
			})
		}
		if owner := normalizeSummaryOwner(summary.Text, ""); owner != c.Key.SessionID {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].text.owner", want.FilterKey),
				BaseValue:    c.Key.SessionID,
				CompareValue: owner,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: summary text owner marker is missing or points at another session",
			})
		}
		if filter := summaryFilterMarker(summary.Text); filter != want.FilterKey {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].text.filter", want.FilterKey),
				BaseValue:    want.FilterKey,
				CompareValue: filter,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: summary text filter-key marker is missing or wrong",
			})
		}
		if want.Version != 0 && summary.Version != want.Version {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].version", want.FilterKey),
				BaseValue:    want.Version,
				CompareValue: summary.Version,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: boundary version changed",
			})
		}
		if want.CutoffEventRef != "" && summary.CutoffEventRef != want.CutoffEventRef {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].cutoff_event_ref", want.FilterKey),
				BaseValue:    want.CutoffEventRef,
				CompareValue: summary.CutoffEventRef,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: summary cutoff does not match replay operations",
			})
		}
		if want.UpdatedAt != "" && summary.UpdatedAt != want.UpdatedAt {
			diffs = append(diffs, Difference{
				Case:         snapshot.Case,
				Backend:      snapshot.Backend,
				SessionID:    snapshot.SessionID,
				Locator:      "summary:" + want.FilterKey,
				FieldPath:    fmt.Sprintf("$.summaries[%q].updated_at", want.FilterKey),
				BaseValue:    want.UpdatedAt,
				CompareValue: summary.UpdatedAt,
				AllowedDiff:  false,
				Explanation:  "summary replay invariant failed: summary update time does not match cutoff event",
			})
		}
	}
	return diffs
}

func summaryFilterMarker(text string) string {
	for _, part := range strings.Split(text, " | ") {
		if filter, ok := strings.CutPrefix(part, "filter="); ok {
			return filter
		}
	}
	return ""
}

func expectedSummaries(c ReplayCase) []NormalizedSummary {
	summaries := map[string]NormalizedSummary{}
	events := make([]NormalizedEvent, 0)
	replayExpectedSummaries(c.Operations, &events, summaries)
	keys := make([]string, 0, len(summaries))
	for key := range summaries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]NormalizedSummary, 0, len(keys))
	for _, filterKey := range keys {
		want := summaries[filterKey]
		want.SessionID = c.Key.SessionID
		want = normalizeExpectedSummaryWindowRef(want, len(events), c.ReadEventLimit)
		out = append(out, want)
	}
	return out
}

func replayExpectedSummaries(
	ops []Operation,
	events *[]NormalizedEvent,
	summaries map[string]NormalizedSummary,
) {
	for _, op := range ops {
		switch op.Kind {
		case OpAppendEvent, OpRetryEvent:
			if op.Event == nil || op.Event.Partial {
				continue
			}
			*events = append(*events, expectedEventFromSpec(*op.Event, len(*events)))
		case OpWriteSummary:
			if op.Summary == nil {
				continue
			}
			filterKey := op.Summary.FilterKey
			want := NormalizedSummary{
				FilterKey: filterKey,
				Version:   1,
			}
			if evt, ok := latestExpectedSummaryEvent(*events, filterKey); ok {
				want.CutoffEventRef = fmt.Sprintf("event[%d]", evt.Index)
				want.UpdatedAt = want.CutoffEventRef
			}
			summaries[filterKey] = want
		case OpConcurrent:
			replayExpectedSummaries(op.Concurrent, events, summaries)
		}
	}
}

func normalizeExpectedSummaryWindowRef(
	want NormalizedSummary,
	totalEvents int,
	readEventLimit int,
) NormalizedSummary {
	if want.CutoffEventRef == "" {
		return want
	}
	cutoffIndex, ok := parseExpectedEventRef(want.CutoffEventRef)
	if !ok {
		return want
	}
	firstRetained := 0
	if readEventLimit > 0 && totalEvents > readEventLimit {
		firstRetained = totalEvents - readEventLimit
	}
	if cutoffIndex < firstRetained {
		want.CutoffEventRef = "event[missing]"
		want.UpdatedAt = deterministicEventTime(cutoffIndex).UTC().Format(time.RFC3339Nano)
		return want
	}
	retainedIndex := cutoffIndex - firstRetained
	want.CutoffEventRef = fmt.Sprintf("event[%d]", retainedIndex)
	want.UpdatedAt = want.CutoffEventRef
	return want
}

func parseExpectedEventRef(ref string) (int, bool) {
	if !strings.HasPrefix(ref, "event[") || !strings.HasSuffix(ref, "]") {
		return 0, false
	}
	var index int
	if _, err := fmt.Sscanf(ref, "event[%d]", &index); err != nil {
		return 0, false
	}
	return index, true
}

func latestExpectedSummaryEvent(events []NormalizedEvent, filterKey string) (NormalizedEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if normalizedEventMatchesSummaryFilter(events[i], filterKey) {
			return events[i], true
		}
	}
	return NormalizedEvent{}, false
}

func expectedEventFromSpec(spec EventSpec, index int) NormalizedEvent {
	filterKey := spec.FilterKey
	if filterKey == "" {
		filterKey = spec.Branch
	}
	return NormalizedEvent{
		Index:     index,
		Branch:    spec.Branch,
		FilterKey: filterKey,
	}
}

func normalizedEventMatchesSummaryFilter(evt NormalizedEvent, filterKey string) bool {
	eventFilterKey := evt.FilterKey
	if eventFilterKey == "" {
		eventFilterKey = evt.Branch
	}
	if filterKey == "" || eventFilterKey == "" {
		return true
	}
	filterKey += "/"
	eventFilterKey += "/"
	return strings.HasPrefix(filterKey, eventFilterKey) || strings.HasPrefix(eventFilterKey, filterKey)
}

func expectedSummaryFilterKeys(ops []Operation) []string {
	seen := expectedSummaryFilterKeySet(ops)
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func expectedSummaryFilterKeySet(ops []Operation) map[string]struct{} {
	seen := map[string]struct{}{}
	collectExpectedSummaryFilterKeys(seen, ops)
	return seen
}

func collectExpectedSummaryFilterKeys(seen map[string]struct{}, ops []Operation) {
	for _, op := range ops {
		switch op.Kind {
		case OpWriteSummary:
			if op.Summary != nil {
				seen[op.Summary.FilterKey] = struct{}{}
			}
		case OpConcurrent:
			collectExpectedSummaryFilterKeys(seen, op.Concurrent)
		}
	}
}

func applyExpectedStateOperations(state map[string]NormalizedValue, ops []Operation) bool {
	changed := false
	for _, op := range ops {
		switch op.Kind {
		case OpAppendEvent, OpRetryEvent:
			if op.Event == nil {
				continue
			}
			for key, value := range op.Event.StateDelta {
				state[key] = normalizeBytes(value)
				changed = true
			}
		case OpSetState:
			if op.State == nil {
				continue
			}
			state[op.State.Key] = normalizeBytes(op.State.Value)
			changed = true
		case OpDeleteState:
			if op.State == nil {
				continue
			}
			delete(state, op.State.Key)
			changed = true
		case OpClearState:
			for key := range state {
				delete(state, key)
			}
			changed = true
		case OpConcurrent:
			changed = applyExpectedConcurrentStateOperations(state, op.Concurrent) || changed
		}
	}
	return changed
}

func applyExpectedConcurrentStateOperations(state map[string]NormalizedValue, ops []Operation) bool {
	changed := false
	for i := len(ops) - 1; i >= 0; i-- {
		changed = applyExpectedStateOperations(state, []Operation{ops[i]}) || changed
	}
	return changed
}

func compareSlices[T any](
	path string,
	base, compare []T,
	locator func(int, *T, *T) string,
	add func(int, string, string, any, any),
) {
	common := len(base)
	if len(compare) < common {
		common = len(compare)
	}
	for i := 0; i < common; i++ {
		compareStruct(path, base[i], compare[i], func(field string, b, c any) {
			add(i, field, locator(i, &base[i], &compare[i]), b, c)
		})
	}
	for i := common; i < len(base); i++ {
		add(i, "presence", locator(i, &base[i], nil), true, false)
	}
	for i := common; i < len(compare); i++ {
		add(i, "presence", locator(i, nil, &compare[i]), false, true)
	}
}

func compareMaps[T any](path string, base, compare map[string]T, add func(string, string, any, any)) {
	seen := map[string]struct{}{}
	for k := range base {
		seen[k] = struct{}{}
	}
	for k := range compare {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, bok := base[k]
		c, cok := compare[k]
		if !bok || !cok {
			add(k, "presence", bok, cok)
			continue
		}
		compareStruct(path, b, c, func(field string, bv, cv any) {
			add(k, field, bv, cv)
		})
	}
}

func compareStruct(path string, base, compare any, add func(string, any, any)) {
	bb, _ := json.Marshal(base)
	cb, _ := json.Marshal(compare)
	var bv any
	var cv any
	if json.Unmarshal(bb, &bv) != nil || json.Unmarshal(cb, &cv) != nil {
		if !reflect.DeepEqual(base, compare) {
			add("value", base, compare)
		}
		return
	}
	compareJSONValue("", bv, cv, add)
	_ = path
}

func compareJSONValue(path string, base, compare any, add func(string, any, any)) {
	if reflect.DeepEqual(base, compare) {
		return
	}
	switch b := base.(type) {
	case map[string]any:
		c, ok := compare.(map[string]any)
		if !ok {
			add(jsonPathFragment(path), base, compare)
			return
		}
		keys := map[string]struct{}{}
		for k := range b {
			keys[k] = struct{}{}
		}
		for k := range c {
			keys[k] = struct{}{}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			bv, bok := b[k]
			cv, cok := c[k]
			next := joinJSONPath(path, k)
			if !bok || !cok {
				add(joinJSONPath(next, "presence"), bok, cok)
				continue
			}
			compareJSONValue(next, bv, cv, add)
		}
	case []any:
		c, ok := compare.([]any)
		if !ok {
			add(jsonPathFragment(path), base, compare)
			return
		}
		common := len(b)
		if len(c) < common {
			common = len(c)
		}
		for i := 0; i < common; i++ {
			compareJSONValue(fmt.Sprintf("%s[%d]", path, i), b[i], c[i], add)
		}
		for i := common; i < len(b); i++ {
			add(joinJSONPath(fmt.Sprintf("%s[%d]", path, i), "presence"), true, false)
		}
		for i := common; i < len(c); i++ {
			add(joinJSONPath(fmt.Sprintf("%s[%d]", path, i), "presence"), false, true)
		}
	default:
		add(jsonPathFragment(path), base, compare)
	}
}

func joinJSONPath(prefix, field string) string {
	if prefix == "" {
		return field
	}
	return prefix + "." + field
}

func jsonPathFragment(path string) string {
	if path == "" {
		return "value"
	}
	return path
}

// MarshalReport renders report JSON with stable indentation.
func MarshalReport(report *Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

// HasBlockingDiff reports whether any non-allowed diff exists.
func HasBlockingDiff(report *Report) bool {
	if report == nil {
		return false
	}
	for _, c := range report.Cases {
		for _, d := range c.Differences {
			if !d.AllowedDiff {
				return true
			}
		}
	}
	return false
}
