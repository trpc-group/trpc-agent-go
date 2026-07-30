//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import "fmt"

// CompareSnapshots performs a field-level comparison between a baseline snapshot and a backend snapshot.
func CompareSnapshots(caseName string, backend string, baseline, actual NormalizedSnapshot) []Diff {
	baseline = NormalizeSnapshot(baseline)
	actual = NormalizeSnapshot(actual)

	var diffs []Diff
	diffs = append(diffs, compareEvents(caseName, backend, baseline.Events, actual.Events)...)
	diffs = append(diffs, compareStates(caseName, backend, baseline.State, actual.State)...)
	diffs = append(diffs, compareMemories(caseName, backend, baseline.Memories, actual.Memories)...)
	diffs = append(diffs, compareSummaries(caseName, backend, baseline.Summaries, actual.Summaries)...)
	diffs = append(diffs, compareTracks(caseName, backend, baseline.Tracks, actual.Tracks)...)
	return diffs
}

func compareEvents(caseName, backend string, baseline, actual []NormalizedEvent) []Diff {
	var diffs []Diff
	max := maxLen(len(baseline), len(actual))
	for i := 0; i < max; i++ {
		path := fmt.Sprintf("events[%d]", i)
		if i >= len(baseline) {
			diffs = append(diffs, newDiff(caseName, backend, path, "<missing>", fmt.Sprintf("%+v", actual[i]), false, "extra event in backend"))
			continue
		}
		if i >= len(actual) {
			diffs = append(diffs, newDiff(caseName, backend, path, fmt.Sprintf("%+v", baseline[i]), "<missing>", false, "event missing from backend"))
			continue
		}
		left := baseline[i]
		right := actual[i]
		compareField := func(field, l, r string, allowed bool, explanation string) {
			if l == r {
				return
			}
			diffs = append(diffs, newDiff(caseName, backend, path+"."+field, l, r, allowed, explanation))
		}
		compareField("id", left.ID, right.ID, true, "auto-generated event ids may differ")
		compareField("author", left.Author, right.Author, false, "author changed")
		compareField("role", left.Role, right.Role, false, "role changed")
		compareField("content", left.Content, right.Content, false, "content changed")
		compareField("tool_call", left.ToolCall, right.ToolCall, false, "tool call changed")
		compareField("tool_name", left.ToolName, right.ToolName, false, "tool name changed")
		compareField("tool_id", left.ToolID, right.ToolID, true, "tool call ids may differ")
		compareField("branch", left.Branch, right.Branch, false, "branch changed")
		compareField("tag", left.Tag, right.Tag, false, "tag changed")
		compareField("filter_key", left.FilterKey, right.FilterKey, false, "filter key changed")
		compareField("timestamp", left.Timestamp, right.Timestamp, true, "timestamps are normalized separately")
		compareField("object", left.Object, right.Object, false, "response object changed")
		compareKVList(caseName, backend, path+".state_delta", left.StateDelta, right.StateDelta, &diffs, false, "state delta changed")
		compareKVList(caseName, backend, path+".extensions", left.Extensions, right.Extensions, &diffs, true, "extension key order or provider metadata changed")
	}
	return diffs
}

func compareStates(caseName, backend string, baseline, actual []NormalizedState) []Diff {
	var diffs []Diff
	max := maxLen(len(baseline), len(actual))
	for i := 0; i < max; i++ {
		path := fmt.Sprintf("state[%d]", i)
		if i >= len(baseline) {
			diffs = append(diffs, newDiff(caseName, backend, path, "<missing>", actual[i].Value, false, "extra state key"))
			continue
		}
		if i >= len(actual) {
			diffs = append(diffs, newDiff(caseName, backend, path, baseline[i].Value, "<missing>", false, "state key missing"))
			continue
		}
		if baseline[i].Key != actual[i].Key {
			diffs = append(diffs, newDiff(caseName, backend, path+".key", baseline[i].Key, actual[i].Key, false, "state key changed"))
		}
		if baseline[i].Value != actual[i].Value {
			diffs = append(diffs, newDiff(caseName, backend, path+".value", baseline[i].Value, actual[i].Value, false, "state value changed"))
		}
	}
	return diffs
}

func compareMemories(caseName, backend string, baseline, actual []NormalizedMemory) []Diff {
	var diffs []Diff
	max := maxLen(len(baseline), len(actual))
	for i := 0; i < max; i++ {
		path := fmt.Sprintf("memories[%d]", i)
		if i >= len(baseline) {
			diffs = append(diffs, newDiff(caseName, backend, path, "<missing>", actual[i].Content, false, "extra memory entry"))
			continue
		}
		if i >= len(actual) {
			diffs = append(diffs, newDiff(caseName, backend, path, baseline[i].Content, "<missing>", false, "memory entry missing"))
			continue
		}
		compareField := func(field, l, r string, allowed bool, explanation string) {
			if l == r {
				return
			}
			diffs = append(diffs, newDiff(caseName, backend, path+"."+field, l, r, allowed, explanation))
		}
		compareField("id", baseline[i].ID, actual[i].ID, false, "memory id changed")
		compareField("content", baseline[i].Content, actual[i].Content, false, "memory content changed")
		compareField("metadata", baseline[i].Metadata, actual[i].Metadata, true, "metadata normalization may differ by backend")
		compareField("score", baseline[i].Score, actual[i].Score, true, "score is backend dependent")
	}
	return diffs
}

func compareSummaries(caseName, backend string, baseline, actual []NormalizedSummary) []Diff {
	var diffs []Diff
	max := maxLen(len(baseline), len(actual))
	for i := 0; i < max; i++ {
		path := fmt.Sprintf("summaries[%d]", i)
		if i >= len(baseline) {
			diffs = append(diffs, newDiff(caseName, backend, path, "<missing>", actual[i].Summary, false, "extra summary"))
			continue
		}
		if i >= len(actual) {
			diffs = append(diffs, newDiff(caseName, backend, path, baseline[i].Summary, "<missing>", false, "summary missing"))
			continue
		}
		compareField := func(field, l, r string, allowed bool, explanation string) {
			if l == r {
				return
			}
			diffs = append(diffs, newDiff(caseName, backend, path+"."+field, l, r, allowed, explanation))
		}
		compareField("session_id", baseline[i].SessionID, actual[i].SessionID, false, "summary belongs to different session")
		compareField("filter_key", baseline[i].FilterKey, actual[i].FilterKey, false, "summary filter key changed")
		compareField("summary", baseline[i].Summary, actual[i].Summary, false, "summary text changed")
		compareField("updated_at", baseline[i].UpdatedAt, actual[i].UpdatedAt, true, "updated_at is normalized")
		compareField("boundary", baseline[i].Boundary, actual[i].Boundary, false, "summary boundary changed")
		compareField("version", baseline[i].Version, actual[i].Version, false, "summary version changed")
	}
	return diffs
}

func compareTracks(caseName, backend string, baseline, actual []NormalizedTrack) []Diff {
	var diffs []Diff
	max := maxLen(len(baseline), len(actual))
	for i := 0; i < max; i++ {
		path := fmt.Sprintf("tracks[%d]", i)
		if i >= len(baseline) {
			diffs = append(diffs, newDiff(caseName, backend, path, "<missing>", actual[i].Payload, false, "extra track event"))
			continue
		}
		if i >= len(actual) {
			diffs = append(diffs, newDiff(caseName, backend, path, baseline[i].Payload, "<missing>", false, "track event missing"))
			continue
		}
		compareField := func(field, l, r string, allowed bool, explanation string) {
			if l == r {
				return
			}
			diffs = append(diffs, newDiff(caseName, backend, path+"."+field, l, r, allowed, explanation))
		}
		compareField("track", baseline[i].Track, actual[i].Track, false, "track name changed")
		compareField("timestamp", baseline[i].Timestamp, actual[i].Timestamp, true, "track timestamp normalized")
		compareField("payload", baseline[i].Payload, actual[i].Payload, false, "track payload changed")
		compareField("type", baseline[i].Type, actual[i].Type, true, "type is optional and backend specific")
	}
	return diffs
}

func compareKVList(caseName, backend, path string, baseline, actual []NormalizedKV, diffs *[]Diff, allowed bool, explanation string) {
	max := maxLen(len(baseline), len(actual))
	for i := 0; i < max; i++ {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if i >= len(baseline) {
			*diffs = append(*diffs, newDiff(caseName, backend, itemPath, "<missing>", actual[i].Value, allowed, explanation))
			continue
		}
		if i >= len(actual) {
			*diffs = append(*diffs, newDiff(caseName, backend, itemPath, baseline[i].Value, "<missing>", allowed, explanation))
			continue
		}
		if baseline[i].Key != actual[i].Key {
			*diffs = append(*diffs, newDiff(caseName, backend, itemPath+".key", baseline[i].Key, actual[i].Key, allowed, explanation))
		}
		if baseline[i].Value != actual[i].Value {
			*diffs = append(*diffs, newDiff(caseName, backend, itemPath+".value", baseline[i].Value, actual[i].Value, allowed, explanation))
		}
	}
}

func newDiff(caseName, backend, path, baseline, actual string, allowed bool, explanation string) Diff {
	return Diff{
		CaseName:    caseName,
		Backend:     backend,
		Path:        path,
		Baseline:    baseline,
		Actual:      actual,
		AllowedDiff: allowed,
		Explanation: explanation,
	}
}

func maxLen(a, b int) int {
	if a > b {
		return a
	}
	return b
}
