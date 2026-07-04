package replaytest

import (
	"fmt"
	"reflect"
	"strings"
)

// AllowedDiffRule defines a rule that marks certain field-level
// differences as acceptable between backends. Each rule specifies
// which section, path pattern, and backend pair it applies to.
type AllowedDiffRule struct {
	Section    string `json:"section"`     // "events", "state", "memory", "summary", "tracks"
	PathPattern string `json:"path_pattern"` // dotted path, e.g. "events[0].content"
	BackendA   string `json:"backend_a,omitempty"`
	BackendB   string `json:"backend_b,omitempty"`
	Reason     string `json:"reason"`
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
	diffs = append(diffs, compareState(left, right, backendA, backendB)...)
	diffs = append(diffs, compareEvents(left, right, backendA, backendB)...)
	diffs = append(diffs, compareMemories(left, right, backendA, backendB)...)
	diffs = append(diffs, compareSummaries(left, right, backendA, backendB)...)
	diffs = append(diffs, compareTracks(left, right, backendA, backendB)...)

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
// Examples:
//
//	"state.key1"        → "state"
//	"events[0].content" → "events"
//	"memories[1]"       → "memories"
//	"summaries"         → "summaries"
//	"tracks[0].payload" → "tracks"
func extractSection(path string) string {
	// Strip array index suffix first: "events[0]" → "events".
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
// Array indices (e.g., "[0]") are normalized away before comparison
// so "events[0].content" matches "events[1].content".
func matchPathPattern(path, pattern string) bool {
	// Normalize both path and pattern: remove array indices.
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

func compareState(left, right Snapshot, backendA, backendB string) []FieldDiff {
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

func compareEvents(left, right Snapshot, backendA, backendB string) []FieldDiff {
	var diffs []FieldDiff
	// Compare by index for now; more sophisticated ordering handled
	// by normalizer's deterministic output.
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

func compareMemories(left, right Snapshot, backendA, backendB string) []FieldDiff {
	var diffs []FieldDiff
	maxLen := len(left.Memories)
	if len(right.Memories) > maxLen {
		maxLen = len(right.Memories)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(left.Memories) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  fmt.Sprintf("mem[%d]", i),
				FieldPath: fmt.Sprintf("memories[%d]", i),
				ValueA:    nil,
				ValueB:    right.Memories[i],
			})
			continue
		}
		if i >= len(right.Memories) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  fmt.Sprintf("mem[%d]", i),
				FieldPath: fmt.Sprintf("memories[%d]", i),
				ValueA:    left.Memories[i],
				ValueB:    nil,
			})
			continue
		}
		lm := left.Memories[i]
		rm := right.Memories[i]
		if lm.Content != rm.Content {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  lm.Content,
				FieldPath: fmt.Sprintf("memories[%d].content", i),
				ValueA:    lm.Content,
				ValueB:    rm.Content,
			})
		}
		if !reflect.DeepEqual(lm.Topics, rm.Topics) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				MemoryID:  lm.Content,
				FieldPath: fmt.Sprintf("memories[%d].topics", i),
				ValueA:    lm.Topics,
				ValueB:    rm.Topics,
			})
		}
	}
	return diffs
}

func compareSummaries(left, right Snapshot, backendA, backendB string) []FieldDiff {
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

func compareTracks(left, right Snapshot, backendA, backendB string) []FieldDiff {
	var diffs []FieldDiff
	maxLen := len(left.Tracks)
	if len(right.Tracks) > maxLen {
		maxLen = len(right.Tracks)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(left.Tracks) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				FieldPath: fmt.Sprintf("tracks[%d]", i),
				ValueA:    nil,
				ValueB:    right.Tracks[i],
			})
			continue
		}
		if i >= len(right.Tracks) {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				FieldPath: fmt.Sprintf("tracks[%d]", i),
				ValueA:    left.Tracks[i],
				ValueB:    nil,
			})
			continue
		}
		lt := left.Tracks[i]
		rt := right.Tracks[i]
		if lt.Track != rt.Track {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				TrackName: lt.Track,
				FieldPath: fmt.Sprintf("tracks[%d].track", i),
				ValueA:    lt.Track,
				ValueB:    rt.Track,
			})
		}
		if lt.Payload != rt.Payload {
			diffs = append(diffs, FieldDiff{
				SessionID: left.SessionID,
				TrackName: lt.Track,
				FieldPath: fmt.Sprintf("tracks[%d].payload", i),
				ValueA:    lt.Payload,
				ValueB:    rt.Payload,
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

// compareStructs uses reflection to compare two structs field by field.
func compareStructs(a, b interface{}, prefix, sessionID string) []FieldDiff {
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
