//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// AllowedDiffRule defines a rule that marks certain field-level
// differences as acceptable between backends. Each rule specifies
// which section, path pattern, and backend pair it applies to.
type AllowedDiffRule struct {
	Section     string `json:"section"`      // "events", "state", "memory", "summary", "tracks"
	PathPattern string `json:"path_pattern"` // dotted path, e.g. "events[0].content"
	BackendA    string `json:"backend_a,omitempty"`
	BackendB    string `json:"backend_b,omitempty"`
	Reason      string `json:"reason"`
}

// CompareSnapshots performs a deep comparison between two snapshots
// and returns a list of field-level differences. AllowedDiffRules
// are applied to mark expected differences.
func CompareSnapshots(
	left, right Snapshot,
	backendA, backendB string,
	allowedDiffs []AllowedDiffRule,
) []FieldDiff {
	var diffs []FieldDiff
	diffs = append(diffs, compareState(left, right)...)
	diffs = append(diffs, compareEvents(left, right)...)
	diffs = append(diffs, compareMemories(left, right)...)
	diffs = append(diffs, compareSummaries(left, right)...)
	diffs = append(diffs, compareTracks(left, right)...)

	// Apply allowed diff rules.
	for i := range diffs {
		for _, rule := range allowedDiffs {
			if matchAllowedDiff(diffs[i], rule, backendA, backendB) {
				diffs[i].Allowed = true
				diffs[i].Reason = rule.Reason
				break
			}
		}
	}
	return diffs
}

// matchAllowedDiff checks whether a diff matches an allowed-diff rule.
func matchAllowedDiff(d FieldDiff, rule AllowedDiffRule, backendA, backendB string) bool {
	// Check backend pair constraint.
	if rule.BackendA != "" && rule.BackendB != "" {
		if !(rule.BackendA == backendA && rule.BackendB == backendB) &&
			!(rule.BackendA == backendB && rule.BackendB == backendA) {
			return false
		}
	}
	// Check section.
	section := extractSection(d.FieldPath)
	if section != rule.Section {
		return false
	}
	// Check path pattern.
	return matchPathPattern(d.FieldPath, rule.PathPattern)
}

// extractSection returns the top-level section from a field path.
func extractSection(path string) string {
	bracketIdx := strings.Index(path, "[")
	dotIdx := strings.Index(path, ".")
	if dotIdx < 0 {
		dotIdx = len(path)
	}
	if bracketIdx >= 0 && bracketIdx < dotIdx {
		return path[:bracketIdx]
	}
	if dotIdx > 0 {
		return path[:dotIdx]
	}
	return path
}

// matchPathPattern checks if a field path matches a dotted path pattern.
// The pattern supports "*" as a wildcard for a single segment.
func matchPathPattern(path, pattern string) bool {
	normalizedPath := normalizeArrayIndices(path)
	normalizedPattern := normalizeArrayIndices(pattern)

	pathParts := strings.Split(normalizedPath, ".")
	patternParts := strings.Split(normalizedPattern, ".")
	if len(pathParts) != len(patternParts) {
		return false
	}
	for i := range pathParts {
		if patternParts[i] == "*" {
			continue
		}
		if pathParts[i] != patternParts[i] {
			return false
		}
	}
	return true
}

// normalizeArrayIndices replaces array index suffixes like "[0]"
// with a placeholder for pattern matching.
func normalizeArrayIndices(path string) string {
	result := path
	for {
		start := strings.Index(result, "[")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "]")
		if end < 0 {
			break
		}
		end += start
		result = result[:start] + result[end+1:]
	}
	return result
}

func compareState(left, right Snapshot) []FieldDiff {
	var diffs []FieldDiff
	allKeys := mergeKeys(left.State, right.State)
	for _, k := range allKeys {
		leftVal := left.State[k]
		rightVal := right.State[k]
		if leftVal != rightVal {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				FieldPath: fmt.Sprintf("state.%s", k),
				ValueA:    leftVal,
				ValueB:    rightVal,
			})
		}
	}
	return diffs
}

func compareEvents(left, right Snapshot) []FieldDiff {
	var diffs []FieldDiff
	maxLen := len(left.Events)
	if len(right.Events) > maxLen {
		maxLen = len(right.Events)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(left.Events) {
			diffs = append(diffs, FieldDiff{
				SessionID:  left.SessionID,
				EventIndex: i,
				FieldPath:  fmt.Sprintf("events[%d]", i),
				ValueA:     nil,
				ValueB:     right.Events[i],
			})
			continue
		}
		if i >= len(right.Events) {
			diffs = append(diffs, FieldDiff{
				SessionID:  left.SessionID,
				EventIndex: i,
				FieldPath:  fmt.Sprintf("events[%d]", i),
				ValueA:     left.Events[i],
				ValueB:     nil,
			})
			continue
		}
		diffs = append(diffs, compareStructs(
			left.Events[i], right.Events[i],
			fmt.Sprintf("events[%d]", i), left.SessionID,
		)...)
	}
	return diffs
}

// compareMemories matches memory entries by content identity rather
// than positional index, so that different backends returning the
// same entries in a different order are not falsely reported as
// different. Matching is one-to-one: when left contains duplicate
// entries with the same content, each entry is matched against a
// distinct right-side entry with that content. Unmatched entries
// (missing or extra) are flagged.
func compareMemories(left, right Snapshot) []FieldDiff {
	var diffs []FieldDiff

	// Build index by content for right side, allowing multiple
	// entries with the same content (one-to-one matching).
	rightByContent := make(map[string][]int)
	for i, m := range right.Memories {
		rightByContent[m.Content] = append(rightByContent[m.Content], i)
	}

	matchedRight := make([]bool, len(right.Memories))
	for i, lm := range left.Memories {
		indices, ok := rightByContent[lm.Content]
		if !ok {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  lm.Content,
				FieldPath: fmt.Sprintf("memories[%d]", i),
				ValueA:    lm,
				ValueB:    nil,
			})
			continue
		}
		// Find first unmatched right entry with matching content.
		var rIdx int
		found := false
		for _, idx := range indices {
			if !matchedRight[idx] {
				rIdx = idx
				found = true
				break
			}
		}
		if !found {
			// All right-side entries with this content are already
			// matched; flag this as an extra left entry.
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  lm.Content,
				FieldPath: fmt.Sprintf("memories[%d]", i),
				ValueA:    lm,
				ValueB:    nil,
			})
			continue
		}
		matchedRight[rIdx] = true
		rm := right.Memories[rIdx]

		if !reflect.DeepEqual(lm.Topics, rm.Topics) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  lm.Content,
				FieldPath: fmt.Sprintf("memories[%s].topics", lm.Content),
				ValueA:    lm.Topics,
				ValueB:    rm.Topics,
			})
		}
		if math.Abs(lm.Score-rm.Score) > 1e-9 {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  lm.Content,
				FieldPath: fmt.Sprintf("memories[%s].score", lm.Content),
				ValueA:    lm.Score,
				ValueB:    rm.Score,
			})
		}
	}

	// Extra memories in right that weren't matched.
	for i, matched := range matchedRight {
		if !matched {
			rm := right.Memories[i]
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  rm.Content,
				FieldPath: fmt.Sprintf("memories[%d]", i),
				ValueA:    nil,
				ValueB:    rm,
			})
		}
	}

	return diffs
}

func compareSummaries(left, right Snapshot) []FieldDiff {
	var diffs []FieldDiff
	maxLen := len(left.Summaries)
	if len(right.Summaries) > maxLen {
		maxLen = len(right.Summaries)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(left.Summaries) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				FieldPath: fmt.Sprintf("summaries[%d]", i),
				ValueA:    nil,
				ValueB:    right.Summaries[i],
			})
			continue
		}
		if i >= len(right.Summaries) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				FieldPath: fmt.Sprintf("summaries[%d]", i),
				ValueA:    left.Summaries[i],
				ValueB:    nil,
			})
			continue
		}
		ls := left.Summaries[i]
		rs := right.Summaries[i]
		if ls.FilterKey != rs.FilterKey {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				SummaryID: ls.FilterKey,
				FieldPath: fmt.Sprintf("summaries[%d].filter_key", i),
				ValueA:    ls.FilterKey,
				ValueB:    rs.FilterKey,
			})
		}
		if ls.Summary != rs.Summary {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				SummaryID: ls.FilterKey,
				FieldPath: fmt.Sprintf("summaries[%d].summary", i),
				ValueA:    ls.Summary,
				ValueB:    rs.Summary,
			})
		}
	}
	return diffs
}

// compareTracks matches track events by track name + payload
// identity rather than positional index, so that different backends
// returning the same events in a different order are not falsely
// reported as different.
func compareTracks(left, right Snapshot) []FieldDiff {
	var diffs []FieldDiff

	// Build index by track+payload for right side.
	type trackKey struct {
		track   string
		payload string
	}
	rightByKey := make(map[trackKey]int)
	for i, t := range right.Tracks {
		rightByKey[trackKey{t.Track, t.Payload}] = i
	}

	matchedRight := make([]bool, len(right.Tracks))
	for i, lt := range left.Tracks {
		tk := trackKey{lt.Track, lt.Payload}
		rIdx, ok := rightByKey[tk]
		if !ok {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				TrackName: lt.Track,
				FieldPath: fmt.Sprintf("tracks[%d]", i),
				ValueA:    lt,
				ValueB:    nil,
			})
			continue
		}
		matchedRight[rIdx] = true
		// Track matched by identity; compare remaining fields
		// (currently Track and Payload already matched by key).
		// If new fields are added to NormalizedTrack in the future,
		// this is where they would be compared.
		_ = right.Tracks[rIdx]
	}

	// Extra tracks in right that weren't matched.
	for i, matched := range matchedRight {
		if !matched {
			rt := right.Tracks[i]
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				TrackName: rt.Track,
				FieldPath: fmt.Sprintf("tracks[%d]", i),
				ValueA:    nil,
				ValueB:    rt,
			})
		}
	}

	return diffs
}

// mergeKeys returns the union of keys from two string maps.
func mergeKeys(a, b map[string]string) []string {
	seen := make(map[string]bool)
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	var keys []string
	for k := range seen {
		keys = append(keys, k)
	}
	return keys
}

// isNilOrEmptyMap returns true when both values are maps and one is
// nil while the other has zero length. This prevents false-positive
// diffs for nil-vs-empty-map comparisons (e.g. StateDelta).
func isNilOrEmptyMap(va, vb reflect.Value) bool {
	if va.Kind() != reflect.Map || vb.Kind() != reflect.Map {
		return false
	}
	aNil := va.IsNil()
	bNil := vb.IsNil()
	return (aNil && vb.Len() == 0) || (bNil && va.Len() == 0)
}

// compareStructs uses reflection to compare two structs field by field.
func compareStructs(a, b any, prefix, sessionID string) []FieldDiff {
	var diffs []FieldDiff
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)

	if va.Type() != vb.Type() {
		diffs = append(diffs, FieldDiff{
			SessionID: sessionID,
			FieldPath: prefix,
			ValueA:    a,
			ValueB:    b,
		})
		return diffs
	}

	t := va.Type()
	for i := 0; i < t.NumField(); i++ {
		fa := va.Field(i)
		fb := vb.Field(i)
		sf := t.Field(i)

		// Skip unexported fields.
		if !sf.IsExported() {
			continue
		}

		fieldName := sf.Name
		// Convert to snake_case for JSON-compatible path.
		jsonTag := sf.Tag.Get("json")
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" && parts[0] != "-" {
				fieldName = parts[0]
			}
		}

		path := prefix + "." + fieldName
		// Normalize: nil map and empty map should be treated as equal.
		if isNilOrEmptyMap(fa, fb) {
			continue
		}
		if !reflect.DeepEqual(fa.Interface(), fb.Interface()) {
			diffs = append(diffs, FieldDiff{
				SessionID: sessionID,
				FieldPath: path,
				ValueA:    fa.Interface(),
				ValueB:    fb.Interface(),
			})
		}
	}
	return diffs
}
