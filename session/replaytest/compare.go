//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	pathpkg "path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Compare returns every semantic difference between two normalized snapshots.
func Compare(caseName string, baseline, actual Snapshot, allowed []AllowedDiff) ([]Diff, error) {
	if err := validateAllowedDiffs(allowed); err != nil {
		return nil, err
	}
	left, err := snapshotValue(baseline)
	if err != nil {
		return nil, fmt.Errorf("encode baseline snapshot: %w", err)
	}
	right, err := snapshotValue(actual)
	if err != nil {
		return nil, fmt.Errorf("encode actual snapshot: %w", err)
	}
	comparator := comparator{
		caseName: caseName,
		baseline: baseline,
		actual:   actual,
		allowed:  allowed,
	}
	comparator.compareNode("", left, true, right, true)
	return comparator.diffs, nil
}

type comparator struct {
	caseName string
	baseline Snapshot
	actual   Snapshot
	allowed  []AllowedDiff
	diffs    []Diff
}

func (c *comparator) compareNode(path string, left any, leftOK bool, right any, rightOK bool) {
	if !leftOK || !rightOK {
		c.addDiff(path, left, right)
		return
	}
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap || rightIsMap {
		if !leftIsMap || !rightIsMap {
			c.addDiff(path, left, right)
			return
		}
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
			leftValue, lok := leftMap[key]
			rightValue, rok := rightMap[key]
			c.compareNode(path+"/"+escapePointer(key), leftValue, lok, rightValue, rok)
		}
		return
	}
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if leftIsArray || rightIsArray {
		if !leftIsArray || !rightIsArray {
			c.addDiff(path, left, right)
			return
		}
		if len(leftArray) != len(rightArray) {
			c.addDiff(path+"/length", len(leftArray), len(rightArray))
		}
		length := len(leftArray)
		if len(rightArray) > length {
			length = len(rightArray)
		}
		for index := 0; index < length; index++ {
			var leftValue, rightValue any
			lok := index < len(leftArray)
			rok := index < len(rightArray)
			if lok {
				leftValue = leftArray[index]
			}
			if rok {
				rightValue = rightArray[index]
			}
			c.compareNode(path+"/"+strconv.Itoa(index), leftValue, lok, rightValue, rok)
		}
		return
	}
	if !reflect.DeepEqual(left, right) {
		c.addDiff(path, left, right)
	}
}

func (c *comparator) addDiff(path string, left, right any) {
	if path == "" {
		path = "/"
	}
	diff := Diff{
		Case:        c.caseName,
		BackendA:    c.baseline.Backend,
		BackendB:    c.actual.Backend,
		SessionID:   stringValue(c.baseline.Session["id"]),
		Path:        path,
		Baseline:    left,
		Actual:      right,
		Explanation: "semantic values differ",
	}
	c.addLocator(&diff)
	for _, rule := range c.allowed {
		if !backendMatches(rule.BackendA, c.baseline.Backend) ||
			!backendMatches(rule.BackendB, c.actual.Backend) {
			continue
		}
		matched, err := pathpkg.Match(rule.Path, path)
		if err != nil || !matched {
			continue
		}
		if allowedByRule(rule, left, right) {
			diff.Allowed = true
			diff.Explanation = rule.Reason
			break
		}
	}
	c.diffs = append(c.diffs, diff)
}

func (c *comparator) addLocator(diff *Diff) {
	parts := pointerParts(diff.Path)
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case "events":
		if index, err := strconv.Atoi(parts[1]); err == nil {
			diff.EventIndex = &index
		}
	case "summaries":
		diff.SummaryFilterKey = parts[1]
	case "tracks":
		diff.TrackName = parts[1]
	case "memories":
		index, err := strconv.Atoi(parts[1])
		if err != nil {
			return
		}
		if index < len(c.baseline.Memories) {
			diff.MemoryID = stringValue(c.baseline.Memories[index]["id"])
		} else if index < len(c.actual.Memories) {
			diff.MemoryID = stringValue(c.actual.Memories[index]["id"])
		}
	}
}

func snapshotValue(snapshot Snapshot) (map[string]any, error) {
	comparable := CanonicalMap{
		"session":     snapshot.Session,
		"events":      snapshot.Events,
		"event_order": snapshot.EventOrder,
		"state":       snapshot.State,
		"memories":    snapshot.Memories,
		"summaries":   snapshot.Summaries,
		"tracks":      snapshot.Tracks,
	}
	raw, err := json.Marshal(comparable)
	if err != nil {
		return nil, err
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func validateAllowedDiffs(rules []AllowedDiff) error {
	for index, rule := range rules {
		if rule.BackendA == "" || rule.BackendB == "" || rule.Path == "" || rule.Reason == "" {
			return fmt.Errorf("allowed_diff %d requires backend_a, backend_b, path, and reason", index)
		}
		if !strings.HasPrefix(rule.Path, "/") {
			return fmt.Errorf("allowed_diff %d path must be a JSON pointer glob", index)
		}
		if _, err := pathpkg.Match(rule.Path, rule.Path); err != nil {
			return fmt.Errorf("allowed_diff %d has invalid path glob: %w", index, err)
		}
		switch rule.Rule {
		case AllowedIgnore, AllowedSameType:
		case AllowedWithinDelta:
			if rule.Delta < 0 {
				return fmt.Errorf("allowed_diff %d delta must be non-negative", index)
			}
		default:
			return fmt.Errorf("allowed_diff %d has unknown rule %q", index, rule.Rule)
		}
	}
	return nil
}

func allowedByRule(rule AllowedDiff, left, right any) bool {
	switch rule.Rule {
	case AllowedIgnore:
		return true
	case AllowedSameType:
		return reflect.TypeOf(left) == reflect.TypeOf(right)
	case AllowedWithinDelta:
		leftNumber, leftOK := number(left)
		rightNumber, rightOK := number(right)
		return leftOK && rightOK && math.Abs(leftNumber-rightNumber) <= rule.Delta
	default:
		return false
	}
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func backendMatches(pattern, backend string) bool {
	return pattern == "*" || pattern == backend
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func unescapePointer(value string) string {
	value = strings.ReplaceAll(value, "~1", "/")
	return strings.ReplaceAll(value, "~0", "~")
}

func pointerParts(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	raw := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index := range raw {
		raw[index] = unescapePointer(raw[index])
	}
	return raw
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// IsClean reports whether a report contains no blocking differences.
func (r Report) IsClean() bool {
	return r.BlockingDiffs == 0 && r.FailedCases == 0
}

// Validate checks report accounting before it is written or consumed.
func (r Report) Validate() error {
	if r.GeneratedAt.IsZero() {
		return errors.New("replaytest: report generated_at is required")
	}
	if r.Reference == "" || len(r.Backends) < 2 {
		return errors.New("replaytest: report requires a reference and two backends")
	}
	backendNames := make(map[string]struct{}, len(r.Backends))
	for _, backend := range r.Backends {
		if backend == "" {
			return errors.New("replaytest: report backend name is required")
		}
		if _, exists := backendNames[backend]; exists {
			return fmt.Errorf("replaytest: duplicate report backend %q", backend)
		}
		backendNames[backend] = struct{}{}
	}
	if _, ok := backendNames[r.Reference]; !ok {
		return fmt.Errorf("replaytest: reference backend %q is not in backends", r.Reference)
	}
	if r.TotalCases != len(r.Cases) {
		return fmt.Errorf("replaytest: total_cases=%d but cases=%d", r.TotalCases, len(r.Cases))
	}
	if r.TotalCases == 0 {
		return errors.New("replaytest: report requires at least one case")
	}
	caseNames := make(map[string]struct{}, len(r.Cases))
	var passed, failed, unsupported, blockingDiffs, allowedDiffs int
	for _, result := range r.Cases {
		if result.Name == "" {
			return errors.New("replaytest: report case name is required")
		}
		if _, exists := caseNames[result.Name]; exists {
			return fmt.Errorf("replaytest: duplicate report case %q", result.Name)
		}
		caseNames[result.Name] = struct{}{}
		switch result.Status {
		case StatusPassed:
			passed++
		case StatusFailed:
			failed++
		case StatusUnsupported:
			unsupported++
		default:
			return fmt.Errorf("replaytest: case %q has unknown status %q", result.Name, result.Status)
		}
		caseBlocking, caseAllowed := countDiffs(result.Diffs)
		if result.Status == StatusFailed && caseBlocking == 0 {
			return fmt.Errorf("replaytest: failed case %q has no blocking diff", result.Name)
		}
		if result.Status != StatusFailed && caseBlocking > 0 {
			return fmt.Errorf("replaytest: case %q has blocking diffs but status %q", result.Name, result.Status)
		}
		blockingDiffs += caseBlocking
		allowedDiffs += caseAllowed
		for index, diff := range result.Diffs {
			if diff.Case != result.Name || diff.BackendA == "" || diff.BackendB == "" ||
				diff.SessionID == "" || !strings.HasPrefix(diff.Path, "/") {
				return fmt.Errorf("replaytest: case %q diff %d has an invalid locator", result.Name, index)
			}
			if _, ok := backendNames[diff.BackendA]; !ok {
				return fmt.Errorf("replaytest: case %q diff %d names unknown backend %q", result.Name, index, diff.BackendA)
			}
			if _, ok := backendNames[diff.BackendB]; !ok {
				return fmt.Errorf("replaytest: case %q diff %d names unknown backend %q", result.Name, index, diff.BackendB)
			}
			if diff.Allowed && diff.Explanation == "" {
				return fmt.Errorf("replaytest: case %q diff %d has no allowed_diff explanation", result.Name, index)
			}
		}
	}
	if passed != r.PassedCases || failed != r.FailedCases || unsupported != r.UnsupportedCases {
		return errors.New("replaytest: case status counters do not add up")
	}
	if blockingDiffs != r.BlockingDiffs || allowedDiffs != r.AllowedDiffs {
		return errors.New("replaytest: diff counters do not add up")
	}
	return nil
}
