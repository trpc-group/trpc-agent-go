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
	"strconv"
	"strings"
)

const missingMarker = "replay:missing"

// DiffEntry records a single field-level difference between two backends.
type DiffEntry struct {
	Case      string         `json:"case"`
	SessionID string         `json:"session_id"`
	BackendA  string         `json:"backend_a"`
	BackendB  string         `json:"backend_b"`
	Section   string         `json:"section"`
	Path      string         `json:"path"`
	Left      any            `json:"left"`
	Right     any            `json:"right"`
	Allowed   bool           `json:"allowed"`
	Reason    string         `json:"reason,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

type valueDiff struct {
	Path  string
	Left  any
	Right any
}

// CompareSnapshots compares two ReplaySnapshots section-by-section,
// producing a list of field-level differences.
func CompareSnapshots(
	caseName string,
	a, b *ReplaySnapshot,
	rules []AllowedDiffRule,
) []DiffEntry {
	sessionID := a.Session.ID
	if sessionID == "" {
		sessionID = b.Session.ID
	}
	backendA, backendB := a.BackendName, b.BackendName

	sections := []struct {
		name  string
		path  string
		left  any
		right any
	}{
		{"session", "$.session", a.Session, b.Session},
		{"events", "$.events", a.Events, b.Events},
		{"state", "$.state", a.State, b.State},
		{"memory", "$.memory", a.Memories, b.Memories},
		{"summary", "$.summary", a.Summaries, b.Summaries},
		{"tracks", "$.tracks", a.Tracks, b.Tracks},
	}

	var diffs []DiffEntry
	for _, sec := range sections {
		vdiffs := recursiveDiff(
			sec.path,
			jsonNormalize(sec.left),
			jsonNormalize(sec.right),
		)
		for _, vd := range vdiffs {
			diffs = append(diffs, DiffEntry{
				Case:      caseName,
				SessionID: sessionID,
				BackendA:  backendA,
				BackendB:  backendB,
				Section:   sec.name,
				Path:      vd.Path,
				Left:      vd.Left,
				Right:     vd.Right,
				Context:   buildDiffContext(sec.name, vd.Path, a, b),
			})
		}
	}
	applyAllowedRules(diffs, rules)
	sort.SliceStable(diffs, func(i, j int) bool {
		if diffs[i].Section != diffs[j].Section {
			return diffs[i].Section < diffs[j].Section
		}
		return diffs[i].Path < diffs[j].Path
	})
	return diffs
}

func jsonNormalize(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("marshal diff value: " + err.Error())
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		panic("unmarshal diff value: " + err.Error())
	}
	return out
}

func recursiveDiff(path string, left, right any) []valueDiff {
	if reflect.DeepEqual(left, right) {
		return nil
	}
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap {
		return diffMap(path, leftMap, rightMap)
	}
	leftList, leftIsList := left.([]any)
	rightList, rightIsList := right.([]any)
	if leftIsList && rightIsList {
		return diffList(path, leftList, rightList)
	}
	return []valueDiff{{Path: path, Left: left, Right: right}}
}

func diffMap(path string, left, right map[string]any) []valueDiff {
	keys := make([]string, 0, len(left)+len(right))
	seen := make(map[string]struct{}, len(left)+len(right))
	for k := range left {
		keys = append(keys, k)
		seen[k] = struct{}{}
	}
	for k := range right {
		if _, ok := seen[k]; ok {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var diffs []valueDiff
	for _, k := range keys {
		childPath := appendPath(path, k)
		lv, lOK := left[k]
		rv, rOK := right[k]
		switch {
		case !lOK:
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: missingValue(), Right: rv,
			})
		case !rOK:
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: lv, Right: missingValue(),
			})
		default:
			diffs = append(diffs, recursiveDiff(childPath, lv, rv)...)
		}
	}
	return diffs
}

func diffList(path string, left, right []any) []valueDiff {
	maxLen := len(left)
	if len(right) > maxLen {
		maxLen = len(right)
	}
	var diffs []valueDiff
	for i := 0; i < maxLen; i++ {
		childPath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(left):
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: missingValue(), Right: right[i],
			})
		case i >= len(right):
			diffs = append(diffs, valueDiff{
				Path: childPath, Left: left[i], Right: missingValue(),
			})
		default:
			diffs = append(diffs, recursiveDiff(childPath, left[i], right[i])...)
		}
	}
	return diffs
}

func missingValue() map[string]string {
	return map[string]string{"replay": "missing"}
}

func appendPath(path, key string) string {
	if isIdentKey(key) {
		return path + "." + key
	}
	quoted, _ := json.Marshal(key)
	return path + "[" + string(quoted) + "]"
}

func isIdentKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 {
			if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				continue
			}
			return false
		}
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func buildDiffContext(
	section, path string, a, b *ReplaySnapshot,
) map[string]any {
	ctx := map[string]any{}
	switch section {
	case "events":
		if idx, ok := parseJSONPathIndex(path, "$.events"); ok {
			ctx["event_index"] = idx
		}
	case "memory":
		if idx, ok := parseJSONPathIndex(path, "$.memory"); ok {
			if idx < len(a.Memories) {
				ctx["left_memory_key"] = a.Memories[idx].Key
				ctx["left_memory_id"] = a.Memories[idx].RawID
			}
			if idx < len(b.Memories) {
				ctx["right_memory_key"] = b.Memories[idx].Key
				ctx["right_memory_id"] = b.Memories[idx].RawID
			}
		}
	case "summary":
		if fk, ok := parseSummaryFilterKey(path); ok {
			ctx["summary_filter_key"] = fk
		}
	case "tracks":
		if idx, ok := parseJSONPathIndex(path, "$.tracks"); ok {
			if idx < len(a.Tracks) {
				ctx["track_name"] = a.Tracks[idx].Name
			} else if idx < len(b.Tracks) {
				ctx["track_name"] = b.Tracks[idx].Name
			}
		}
		if idx, ok := parseNestedJSONPathIndex(path, ".events"); ok {
			ctx["track_event_index"] = idx
		}
	}
	if len(ctx) == 0 {
		return nil
	}
	return ctx
}

func parseJSONPathIndex(path, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix+"[") {
		return 0, false
	}
	start := len(prefix) + 1
	end := strings.Index(path[start:], "]")
	if end < 0 {
		return 0, false
	}
	idx, err := strconv.Atoi(path[start : start+end])
	if err != nil {
		return 0, false
	}
	return idx, true
}

func parseNestedJSONPathIndex(path, marker string) (int, bool) {
	pos := strings.Index(path, marker+"[")
	if pos < 0 {
		return 0, false
	}
	start := pos + len(marker) + 1
	end := strings.Index(path[start:], "]")
	if end < 0 {
		return 0, false
	}
	idx, err := strconv.Atoi(path[start : start+end])
	if err != nil {
		return 0, false
	}
	return idx, true
}

func parseSummaryFilterKey(path string) (string, bool) {
	const bracketPrefix = "$.summary["
	if strings.HasPrefix(path, bracketPrefix) {
		start := len(bracketPrefix)
		end := strings.Index(path[start:], "]")
		if end < 0 {
			return "", false
		}
		quoted := path[start : start+end]
		val, err := strconv.Unquote(quoted)
		if err != nil {
			return "", false
		}
		return val, true
	}
	const dotPrefix = "$.summary."
	if !strings.HasPrefix(path, dotPrefix) {
		return "", false
	}
	remaining := strings.TrimPrefix(path, dotPrefix)
	key := remaining
	if dot := strings.Index(key, "."); dot >= 0 {
		key = key[:dot]
	}
	if bracket := strings.Index(key, "["); bracket >= 0 {
		key = key[:bracket]
	}
	return key, true
}

func applyAllowedRules(diffs []DiffEntry, rules []AllowedDiffRule) {
	for i := range diffs {
		for _, rule := range rules {
			if !ruleMatches(rule, diffs[i]) {
				continue
			}
			diffs[i].Allowed = true
			diffs[i].Reason = strings.TrimSpace(rule.Reason)
			break
		}
	}
}

func ruleMatches(rule AllowedDiffRule, diff DiffEntry) bool {
	section := strings.TrimSpace(rule.Section)
	path := strings.TrimSpace(rule.Path)
	backendA := strings.TrimSpace(rule.BackendA)
	backendB := strings.TrimSpace(rule.BackendB)
	reason := strings.TrimSpace(rule.Reason)
	if section == "" || path == "" || backendA == "" || backendB == "" || reason == "" {
		return false
	}
	if section != diff.Section {
		return false
	}
	if !wildcardMatch(path, diff.Path) {
		return false
	}
	return backendPairMatches(backendA, backendB, diff.BackendA, diff.BackendB)
}

func backendPairMatches(ruleA, ruleB, entryA, entryB string) bool {
	if ruleA == entryA && ruleB == entryB {
		return true
	}
	return ruleA == entryB && ruleB == entryA
}

func wildcardMatch(pattern, value string) bool {
	if pattern == value || pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return false
	}
	if parts[0] != "" && !strings.HasPrefix(value, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		idx := strings.Index(value[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(value, last)
}
