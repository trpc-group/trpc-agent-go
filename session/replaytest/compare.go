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

// CompareSnapshots performs a recursive semantic comparison.
func CompareSnapshots(
	caseName, backendA, backendB string,
	baseline, compared Snapshot,
	allowed []AllowedDiff,
	checkpoint string,
) ([]Diff, error) {
	if err := validateAllowedDiffs(allowed); err != nil {
		return nil, err
	}
	left, err := toGeneric(baseline)
	if err != nil {
		return nil, err
	}
	right, err := toGeneric(compared)
	if err != nil {
		return nil, err
	}
	skipped := unsupportedSections(baseline.Unsupported, compared.Unsupported)
	// Capability declarations are report metadata, not replayed business data.
	// They determine which sections may be skipped but must not themselves
	// produce semantic differences between heterogeneous backends.
	delete(left.(map[string]any), "unsupported")
	delete(right.(map[string]any), "unsupported")
	var diffs []Diff
	walkDiff("$", left, right, func(path string, leftValue, rightValue any) {
		section := sectionForPath(path)
		if _, skip := skipped[section]; skip {
			return
		}
		leftMissing := isMissingValue(leftValue)
		rightMissing := isMissingValue(rightValue)
		diff := Diff{
			Case: caseName, SessionID: firstNonEmpty(baseline.SessionID, compared.SessionID),
			BackendA: backendA, BackendB: backendB, Section: section, Path: path,
			Baseline: leftValue, Compared: rightValue,
			BaselinePresent: !leftMissing, ComparedPresent: !rightMissing,
			AllowedDiff: false, Explanation: "unexpected backend mismatch",
			Checkpoint: checkpoint,
		}
		applyDiffContext(&diff, path, baseline, compared)
		for _, rule := range allowed {
			if allowedDiffMatches(rule, diff) {
				diff.AllowedDiff = true
				diff.Explanation = rule.Reason
				break
			}
		}
		diffs = append(diffs, diff)
	})
	return diffs, nil
}

// CompareTraces compares matching transition checkpoints and final snapshots.
func CompareTraces(
	caseName, backendA, backendB string,
	baseline, compared Trace,
	allowed []AllowedDiff,
) ([]Diff, error) {
	metadataDiffs, err := compareCheckpointMetadata(
		caseName,
		backendA,
		backendB,
		baseline,
		compared,
		allowed,
	)
	if err != nil {
		return nil, err
	}
	leftCheckpoints, err := indexCheckpoints(baseline.Checkpoints)
	if err != nil {
		return nil, fmt.Errorf("baseline trace: %w", err)
	}
	rightCheckpoints, err := indexCheckpoints(compared.Checkpoints)
	if err != nil {
		return nil, fmt.Errorf("compared trace: %w", err)
	}
	keys := make(map[string]struct{}, len(leftCheckpoints)+len(rightCheckpoints))
	for key := range leftCheckpoints {
		keys[key] = struct{}{}
	}
	for key := range rightCheckpoints {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	diffs := metadataDiffs
	for _, key := range ordered {
		left, leftOK := leftCheckpoints[key]
		right, rightOK := rightCheckpoints[key]
		if !leftOK || !rightOK {
			diff := Diff{
				Case: caseName, SessionID: firstNonEmpty(baseline.Final.SessionID, compared.Final.SessionID),
				BackendA: backendA, BackendB: backendB, Section: "checkpoints",
				Path:     "$.checkpoints[" + strconv.Quote(key) + "]",
				Baseline: MissingValue{Missing: !leftOK}, Compared: MissingValue{Missing: !rightOK},
				BaselinePresent: leftOK, ComparedPresent: rightOK,
				Explanation: "transition checkpoint mismatch", Checkpoint: key,
			}
			for _, rule := range allowed {
				if allowedDiffMatches(rule, diff) {
					diff.AllowedDiff = true
					diff.Explanation = rule.Reason
				}
			}
			diffs = append(diffs, diff)
			continue
		}
		checkpointDiffs, err := CompareSnapshots(
			caseName, backendA, backendB, left.Snapshot, right.Snapshot, allowed, key,
		)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, checkpointDiffs...)
	}
	finalDiffs, err := CompareSnapshots(
		caseName, backendA, backendB, baseline.Final, compared.Final, allowed, "final",
	)
	if err != nil {
		return nil, err
	}
	return append(diffs, finalDiffs...), nil
}

func compareCheckpointMetadata(
	caseName, backendA, backendB string,
	baseline, compared Trace,
	allowed []AllowedDiff,
) ([]Diff, error) {
	type checkpointMetadata struct {
		Name    string `json:"name"`
		AfterOp string `json:"after_op"`
	}
	left := make([]checkpointMetadata, len(baseline.Checkpoints))
	for i := range baseline.Checkpoints {
		left[i] = checkpointMetadata{
			Name:    baseline.Checkpoints[i].Name,
			AfterOp: baseline.Checkpoints[i].AfterOp,
		}
	}
	right := make([]checkpointMetadata, len(compared.Checkpoints))
	for i := range compared.Checkpoints {
		right[i] = checkpointMetadata{
			Name:    compared.Checkpoints[i].Name,
			AfterOp: compared.Checkpoints[i].AfterOp,
		}
	}
	leftValue, err := toGeneric(left)
	if err != nil {
		return nil, err
	}
	rightValue, err := toGeneric(right)
	if err != nil {
		return nil, err
	}
	var diffs []Diff
	walkDiff(
		"$.checkpoints",
		leftValue,
		rightValue,
		func(path string, leftItem, rightItem any) {
			diff := Diff{
				Case: caseName,
				SessionID: firstNonEmpty(
					baseline.Final.SessionID,
					compared.Final.SessionID,
				),
				BackendA:        backendA,
				BackendB:        backendB,
				Section:         "checkpoints",
				Path:            path,
				Baseline:        leftItem,
				Compared:        rightItem,
				BaselinePresent: !isMissingValue(leftItem),
				ComparedPresent: !isMissingValue(rightItem),
				Explanation:     "checkpoint sequence mismatch",
			}
			for _, rule := range allowed {
				if allowedDiffMatches(rule, diff) {
					diff.AllowedDiff = true
					diff.Explanation = rule.Reason
					break
				}
			}
			diffs = append(diffs, diff)
		},
	)
	return diffs, nil
}

// HasBlockingDiff reports whether any difference is not explicitly allowed.
func HasBlockingDiff(diffs []Diff) bool {
	for _, diff := range diffs {
		if !diff.AllowedDiff {
			return true
		}
	}
	return false
}

func walkDiff(path string, left, right any, add func(string, any, any)) {
	if reflect.DeepEqual(left, right) {
		return
	}
	leftMap, leftMapOK := left.(map[string]any)
	rightMap, rightMapOK := right.(map[string]any)
	if leftMapOK && rightMapOK {
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
			leftValue, leftExists := leftMap[key]
			if !leftExists {
				leftValue = MissingValue{Missing: true}
			}
			rightValue, rightExists := rightMap[key]
			if !rightExists {
				rightValue = MissingValue{Missing: true}
			}
			walkDiff(appendMapPath(path, key), leftValue, rightValue, add)
		}
		return
	}
	leftSlice, leftSliceOK := left.([]any)
	rightSlice, rightSliceOK := right.([]any)
	if leftSliceOK && rightSliceOK {
		maximum := len(leftSlice)
		if len(rightSlice) > maximum {
			maximum = len(rightSlice)
		}
		for i := 0; i < maximum; i++ {
			var leftValue any = MissingValue{Missing: true}
			var rightValue any = MissingValue{Missing: true}
			if i < len(leftSlice) {
				leftValue = leftSlice[i]
			}
			if i < len(rightSlice) {
				rightValue = rightSlice[i]
			}
			walkDiff(fmt.Sprintf("%s[%d]", path, i), leftValue, rightValue, add)
		}
		return
	}
	add(path, left, right)
}

func toGeneric(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal replay snapshot: %w", err)
	}
	var out any
	if err := decodeJSON(raw, &out); err != nil {
		return nil, fmt.Errorf("decode replay snapshot: %w", err)
	}
	return out, nil
}

func validateAllowedDiffs(rules []AllowedDiff) error {
	for i, rule := range rules {
		if strings.TrimSpace(rule.Section) == "" || strings.TrimSpace(rule.Path) == "" ||
			strings.TrimSpace(rule.BackendA) == "" || strings.TrimSpace(rule.BackendB) == "" ||
			strings.TrimSpace(rule.Reason) == "" {
			return fmt.Errorf("allowed diff %d requires section, path, both backends, and reason", i)
		}
		if strings.ContainsAny(rule.Section, "*") || strings.ContainsAny(rule.Path, "*") {
			return fmt.Errorf("allowed diff %d must use an exact section and path", i)
		}
		if !strings.HasPrefix(rule.Path, "$.") || sectionForPath(rule.Path) != rule.Section {
			return fmt.Errorf("allowed diff %d path %q does not belong to section %q", i, rule.Path, rule.Section)
		}
	}
	return nil
}

func allowedDiffMatches(rule AllowedDiff, diff Diff) bool {
	backendsMatch := (rule.BackendA == diff.BackendA && rule.BackendB == diff.BackendB) ||
		(rule.BackendA == diff.BackendB && rule.BackendB == diff.BackendA)
	return backendsMatch && rule.Section == diff.Section && rule.Path == diff.Path
}

func sectionForPath(path string) string {
	trimmed := strings.TrimPrefix(path, "$.")
	if trimmed == path {
		return strings.TrimPrefix(path, "$")
	}
	if index := strings.IndexAny(trimmed, ".["); index >= 0 {
		return trimmed[:index]
	}
	return trimmed
}

func appendMapPath(path, key string) string {
	if isSimplePathKey(key) {
		return path + "." + key
	}
	raw, _ := json.Marshal(key)
	return path + "[" + string(raw) + "]"
}

func isSimplePathKey(key string) bool {
	if key == "" {
		return false
	}
	for i := range key {
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

func applyDiffContext(diff *Diff, path string, baseline, compared Snapshot) {
	if index, ok := pathIndex(path, "$.events["); ok {
		diff.EventIndex = intPointer(index)
	}
	if index, ok := pathIndex(path, "$.memories["); ok {
		if index < len(baseline.Memories) {
			diff.MemoryID = baseline.Memories[index].ID
		} else if index < len(compared.Memories) {
			diff.MemoryID = compared.Memories[index].ID
		}
	}
	if query, index, ok := memoryQueryPath(path); ok {
		if index < len(baseline.MemoryQueries[query]) {
			diff.MemoryID = baseline.MemoryQueries[query][index].ID
		} else if index < len(compared.MemoryQueries[query]) {
			diff.MemoryID = compared.MemoryQueries[query][index].ID
		}
	}
	if key, ok := mapPathKey(path, "$.summaries"); ok {
		diff.SummaryID = summaryIdentity(baseline, compared, key)
		diff.SummaryFilterKey = &key
	}
	if key, ok := mapPathKey(path, "$.tracks"); ok {
		diff.TrackName = key
	}
}

func summaryIdentity(baseline, compared Snapshot, filterKey string) string {
	value, ok := baseline.Summaries[filterKey]
	if !ok {
		value, ok = compared.Summaries[filterKey]
	}
	if !ok {
		return ""
	}
	return strings.Join(
		[]string{
			firstNonEmpty(value.SessionID, baseline.SessionID, compared.SessionID),
			filterKey,
			strconv.Itoa(value.Version),
		},
		":",
	)
}

func pathIndex(path, prefix string) (int, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, false
	}
	remaining := strings.TrimPrefix(path, prefix)
	end := strings.IndexByte(remaining, ']')
	if end < 0 {
		return 0, false
	}
	index, err := strconv.Atoi(remaining[:end])
	return index, err == nil
}

func memoryQueryPath(path string) (string, int, bool) {
	query, ok := mapPathKey(path, "$.memory_queries")
	if !ok {
		return "", 0, false
	}
	marker := appendMapPath("$.memory_queries", query) + "["
	index, ok := pathIndex(path, marker)
	return query, index, ok
}

func mapPathKey(path, prefix string) (string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if strings.HasPrefix(rest, ".") {
		rest = rest[1:]
		if end := strings.IndexAny(rest, ".["); end >= 0 {
			rest = rest[:end]
		}
		return rest, rest != ""
	}
	if !strings.HasPrefix(rest, "[\"") {
		return "", false
	}
	for i := 2; i < len(rest); i++ {
		if rest[i] != '"' || rest[i-1] == '\\' {
			continue
		}
		if i+1 >= len(rest) || rest[i+1] != ']' {
			return "", false
		}
		var key string
		if err := json.Unmarshal([]byte(rest[1:i+1]), &key); err != nil {
			return "", false
		}
		return key, true
	}
	return "", false
}

func unsupportedSections(left, right map[CapabilityName]string) map[string]struct{} {
	result := make(map[string]struct{})
	for capability := range left {
		if section := capabilitySection(capability); section != "" {
			result[section] = struct{}{}
		}
	}
	for capability := range right {
		if section := capabilitySection(capability); section != "" {
			result[section] = struct{}{}
		}
	}
	return result
}

func capabilitySection(capability CapabilityName) string {
	switch capability {
	case CapabilityEvents:
		return "events"
	case CapabilityState:
		return "state"
	case CapabilityAppState:
		return "app_state"
	case CapabilityUserState:
		return "user_state"
	case CapabilityMemory:
		return "memories"
	case CapabilityMemorySearch:
		return "memory_queries"
	case CapabilitySummary:
		return "summaries"
	case CapabilityTracks:
		return "tracks"
	default:
		return ""
	}
}

func indexCheckpoints(
	values []CheckpointSnapshot,
) (map[string]CheckpointSnapshot, error) {
	result := make(map[string]CheckpointSnapshot, len(values))
	for _, checkpoint := range values {
		if strings.TrimSpace(checkpoint.Name) == "" {
			return nil, fmt.Errorf("checkpoint name is empty")
		}
		if _, exists := result[checkpoint.Name]; exists {
			return nil, fmt.Errorf(
				"duplicate checkpoint name %q",
				checkpoint.Name,
			)
		}
		result[checkpoint.Name] = checkpoint
	}
	return result, nil
}

func isMissingValue(value any) bool {
	if missing, ok := value.(MissingValue); ok {
		return missing.Missing
	}
	if object, ok := value.(map[string]any); ok {
		flag, _ := object["__missing"].(bool)
		return flag
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
