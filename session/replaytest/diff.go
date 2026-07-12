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
	"reflect"
	"sort"
	"strings"
)

// Compare returns every semantic mismatch between two normalized snapshots.
func Compare(caseName, backendA, backendB string, left, right Snapshot, allowed []AllowedDiff) ([]Diff, error) {
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
			Case: caseName, SessionID: left.SessionID, BackendA: backendA, BackendB: backendB,
			Section: section, Path: path, Baseline: baseline, Compared: compared,
			BaselinePresent: !baselineMissing, ComparedPresent: !comparedMissing,
			Explanation: "unexpected backend mismatch",
		}
		applyContext(&diff, path, left, right)
		for _, rule := range allowed {
			if ruleMatches(rule, diff) {
				diff.Allowed = true
				diff.Explanation = rule.Reason
				break
			}
		}
		diffs = append(diffs, diff)
	})
	return diffs, nil
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
				leftItem = MissingValue{Missing: true}
			}
			rightItem, rightExists := rightMap[key]
			if !rightExists {
				rightItem = MissingValue{Missing: true}
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
			var leftItem any = MissingValue{Missing: true}
			var rightItem any = MissingValue{Missing: true}
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
	if err := decodeJSON(raw, &result); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	return result, nil
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
		if strings.TrimSpace(rule.Section) == "" || strings.TrimSpace(rule.Path) == "" ||
			strings.TrimSpace(rule.BackendA) == "" || strings.TrimSpace(rule.BackendB) == "" ||
			strings.TrimSpace(rule.Reason) == "" {
			return fmt.Errorf("allowed diff %d requires section, path, both backends, and reason", i)
		}
		if strings.Contains(rule.Path, "*") || strings.Contains(rule.Section, "*") {
			return fmt.Errorf("allowed diff %d must use an exact section and path", i)
		}
		if !strings.HasPrefix(rule.Path, "$.") || sectionForPath(rule.Path) != rule.Section {
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

func unsupportedSections(left, right map[string]string) map[string]struct{} {
	result := make(map[string]struct{})
	for capability := range left {
		if section := sectionForCapability(capability); section != "" {
			result[section] = struct{}{}
		}
	}
	for capability := range right {
		if section := sectionForCapability(capability); section != "" {
			result[section] = struct{}{}
		}
	}
	return result
}

func sectionForCapability(capability string) string {
	switch capability {
	case CapabilityEvents:
		return "events"
	case CapabilityState:
		return "state"
	case CapabilityMemory:
		return "memories"
	case CapabilitySummary:
		return "summaries"
	case CapabilityTracks:
		return "tracks"
	default:
		return ""
	}
}
