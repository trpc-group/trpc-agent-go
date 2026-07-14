//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Compare returns every semantic mismatch between two normalized snapshots.
func Compare(
	caseName, backendA, backendB string,
	left, right Snapshot,
	allowed []AllowedDiff,
) ([]Diff, error) {
	if err := validateAllowedDiffs(allowed); err != nil {
		return nil, err
	}

	ignoredSections := unsupportedSections(left.Unsupported, right.Unsupported)
	left.Unsupported = nil
	right.Unsupported = nil

	leftValue, err := toGeneric(left)
	if err != nil {
		return nil, err
	}
	rightValue, err := toGeneric(right)
	if err != nil {
		return nil, err
	}

	var diffs []Diff
	walkDiff("$", leftValue, rightValue, func(path string, baseline, compared any) {
		section := sectionForPath(path)
		if _, ignored := ignoredSections[section]; ignored {
			return
		}

		_, baselineMissing := baseline.(MissingValue)
		_, comparedMissing := compared.(MissingValue)

		diff := Diff{
			Case:            caseName,
			SessionID:       extractSessionID(left, right),
			BackendA:        backendA,
			BackendB:        backendB,
			Section:         section,
			Path:            path,
			ValueA:          baseline,
			ValueB:          compared,
			BaselinePresent: !baselineMissing,
			ComparedPresent: !comparedMissing,
			Severity:        classifySeverity(baselineMissing, comparedMissing, baseline, compared),
			Explanation:     "unexpected backend mismatch",
		}
		applyContext(&diff, path, left, right)

		for _, rule := range allowed {
			if ruleMatches(rule, diff) {
				diff.Allowed = true
				diff.Severity = SeverityMinor
				diff.Explanation = rule.Reason
				break
			}
		}
		diffs = append(diffs, diff)
	})
	return diffs, nil
}

// HasUnexpectedDiff reports whether a CaseResult contains a non-allowlisted mismatch.
// Inconclusive results are treated as unhealthy.
func HasUnexpectedDiff(result CaseResult) bool {
	if result.Status == StatusInconclusive {
		return true
	}
	for _, diff := range result.Diffs {
		if !diff.Allowed {
			return true
		}
	}
	return false
}

func walkDiff(path string, left, right any, add func(string, any, any)) {
	if reflect.DeepEqual(left, right) {
		return
	}

	leftMap, leftOK := left.(map[string]any)
	rightMap, rightOK := right.(map[string]any)
	if leftOK && rightOK {
		keys := make(map[string]struct{}, len(leftMap)+len(rightMap))
		for key := range leftMap {
			keys[key] = struct{}{}
		}
		for key := range rightMap {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			leftItem, leftExists := leftMap[key]
			if !leftExists {
				leftItem = MissingValue{}
			}
			rightItem, rightExists := rightMap[key]
			if !rightExists {
				rightItem = MissingValue{}
			}
			walkDiff(appendMapPath(path, key), leftItem, rightItem, add)
		}
		return
	}

	leftSlice, leftOK := left.([]any)
	rightSlice, rightOK := right.([]any)
	if leftOK && rightOK {
		maximum := len(leftSlice)
		if len(rightSlice) > maximum {
			maximum = len(rightSlice)
		}
		for i := 0; i < maximum; i++ {
			var leftItem any = MissingValue{}
			var rightItem any = MissingValue{}
			if i < len(leftSlice) {
				leftItem = leftSlice[i]
			}
			if i < len(rightSlice) {
				rightItem = rightSlice[i]
			}
			walkDiff(fmt.Sprintf("%s[%d]", path, i), leftItem, rightItem, add)
		}
		return
	}

	add(path, left, right)
}

func toGeneric(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	var result any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return restoreMissingValues(convertNumbers(result)), nil
}

// restoreMissingValues walks a generic structure and replaces
// {"__missing": true} maps (produced by MissingValue.MarshalJSON)
// back into MissingValue instances so walkDiff can detect them.
func restoreMissingValues(v any) any {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 1 && val["__missing"] == true {
			return MissingValue{}
		}
		m := make(map[string]any, len(val))
		for k, v2 := range val {
			m[k] = restoreMissingValues(v2)
		}
		return m
	case []any:
		out := make([]any, len(val))
		for i, v2 := range val {
			out[i] = restoreMissingValues(v2)
		}
		return out
	default:
		return v
	}
}

func convertNumbers(v any) any {
	switch val := v.(type) {
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
		if f, err := val.Float64(); err == nil {
			return f
		}
		return val.String()
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, v2 := range val {
			m[k] = convertNumbers(v2)
		}
		return m
	case []any:
		out := make([]any, len(val))
		for i, v2 := range val {
			out[i] = convertNumbers(v2)
		}
		return out
	default:
		return v
	}
}

func sectionForPath(path string) string {
	trimmed := strings.TrimPrefix(path, "$.")
	if index := strings.IndexAny(trimmed, ".["); index >= 0 {
		return trimmed[:index]
	}
	return trimmed
}

func validateAllowedDiffs(rules []AllowedDiff) error {
	for i, rule := range rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("allowed diff %d: %w", i, err)
		}
		if strings.Contains(rule.Path, "*") || strings.Contains(rule.Section, "*") {
			return fmt.Errorf("allowed diff %d must use an exact section and path (no wildcards)", i)
		}
		if !strings.HasPrefix(rule.Path, "$.") {
			return fmt.Errorf("allowed diff %d path must start with $.", i)
		}
		if sectionForPath(rule.Path) != rule.Section {
			return fmt.Errorf("allowed diff %d path does not belong to section %q", i, rule.Section)
		}
	}
	return nil
}

func ruleMatches(rule AllowedDiff, diff Diff) bool {
	backendMatch := (rule.BackendA == diff.BackendA && rule.BackendB == diff.BackendB) ||
		(rule.BackendA == diff.BackendB && rule.BackendB == diff.BackendA)
	return backendMatch && rule.Section == diff.Section && rule.Path == diff.Path
}

func applyContext(diff *Diff, path string, left, right Snapshot) {
	if index, ok := indexedPath(path, "$.events["); ok {
		diff.EventIndex = intPointer(index)
	}
	if index, ok := indexedPath(path, "$.memories["); ok {
		if index < len(left.Memories) {
			diff.MemoryID = left.Memories[index].ID
		} else if index < len(right.Memories) {
			diff.MemoryID = right.Memories[index].ID
		}
	}
	if name, ok := contextPathKey(path, "$.summaries"); ok {
		diff.SummaryKey = &name
	}
	if name, ok := contextPathKey(path, "$.tracks"); ok {
		diff.TrackName = name
	}
}

func appendMapPath(path, key string) string {
	if simplePathKey(key) {
		return path + "." + key
	}
	raw, _ := json.Marshal(key)
	return path + "[" + string(raw) + "]"
}

func simplePathKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		character := key[i]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') || character == '_' ||
			(i > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func contextPathKey(path, prefix string) (string, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || rest == "" {
		return "", false
	}
	if rest[0] == '.' {
		rest = rest[1:]
		if index := strings.IndexAny(rest, ".["); index >= 0 {
			rest = rest[:index]
		}
		return rest, rest != ""
	}
	if !strings.HasPrefix(rest, "[\"") {
		return "", false
	}
	escaped := false
	for i := 2; i < len(rest); i++ {
		switch {
		case escaped:
			escaped = false
		case rest[i] == '\\':
			escaped = true
		case rest[i] == '"':
			if i+1 >= len(rest) || rest[i+1] != ']' {
				return "", false
			}
			var key string
			if err := json.Unmarshal([]byte(rest[1:i+1]), &key); err != nil {
				return "", false
			}
			return key, true
		}
	}
	return "", false
}

func indexedPath(path, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	var index int
	_, err := fmt.Sscanf(strings.TrimPrefix(path, prefix), "%d]", &index)
	return index, err == nil
}

func unsupportedSections(left, right []string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, cap := range left {
		if section := sectionForCapability(cap); section != "" {
			result[section] = struct{}{}
		}
	}
	for _, cap := range right {
		if section := sectionForCapability(cap); section != "" {
			result[section] = struct{}{}
		}
	}
	return result
}

func sectionForCapability(cap string) string {
	switch cap {
	case CapEvents:
		return "events"
	case CapState:
		return "state"
	case CapMemory:
		return "memories"
	case CapSummary:
		return "summaries"
	case CapTrack:
		return "tracks"
	default:
		return ""
	}
}

func extractSessionID(left, right Snapshot) string {
	// Try to find session_id in summaries.
	for _, s := range left.Summaries {
		if s.SessionID != "" {
			return s.SessionID
		}
	}
	for _, s := range right.Summaries {
		if s.SessionID != "" {
			return s.SessionID
		}
	}
	return ""
}

// compareScopedStates compares AppState and UserState sections independently.
// It normalizes paths under $.app_state and $.user_state using the same walkDiff engine.
func compareScopedStates(
	caseName, backendA, backendB string,
	left, right Snapshot,
	allowed []AllowedDiff,
) []Diff {
	var diffs []Diff
	for _, section := range []struct {
		name  string
		left  map[string]any
		right map[string]any
	}{
		{"app_state", left.AppState, right.AppState},
		{"user_state", left.UserState, right.UserState},
	} {
		if section.left == nil && section.right == nil {
			continue
		}
		leftVal := section.left
		rightVal := section.right
		if leftVal == nil {
			leftVal = make(map[string]any)
		}
		if rightVal == nil {
			rightVal = make(map[string]any)
		}
		leftMissing := section.left == nil
		rightMissing := section.right == nil
		walkDiff("$."+section.name, leftVal, rightVal, func(path string, baseline, compared any) {
			d := Diff{
				Case:            caseName,
				SessionID:       extractSessionID(left, right),
				BackendA:        backendA,
				BackendB:        backendB,
				Section:         section.name,
				Path:            path,
				ValueA:          baseline,
				ValueB:          compared,
				BaselinePresent: true,
				ComparedPresent: true,
				Severity:        classifySeverity(leftMissing, rightMissing, baseline, compared),
				Explanation:     "unexpected scoped state mismatch",
			}
			for _, rule := range allowed {
				if ruleMatches(rule, d) {
					d.Allowed = true
					d.Severity = SeverityMinor
					d.Explanation = rule.Reason
					break
				}
			}
			diffs = append(diffs, d)
		})
	}
	return diffs
}

// classifySeverity assigns a severity level based on the nature of the mismatch.
func classifySeverity(baselineMissing, comparedMissing bool, baseline, compared any) DiffSeverity {
	// Both missing — no real diff (shouldn't reach here but be safe).
	if baselineMissing && comparedMissing {
		return SeverityMinor
	}
	// One side is entirely absent (MissingValue) while the other has data → data loss.
	if baselineMissing || comparedMissing {
		return SeverityCritical
	}
	// Both sides have data but values differ.
	return SeverityMajor
}
