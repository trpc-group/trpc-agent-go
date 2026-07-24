//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Difference is one precisely located cross-backend difference.
type Difference struct {
	Category    string `json:"category"`
	Path        string `json:"path"`
	SessionID   string `json:"session_id,omitempty"`
	EventIndex  *int   `json:"event_index,omitempty"`
	SummaryID   string `json:"summary_id,omitempty"`
	Reference   any    `json:"reference"`
	Actual      any    `json:"actual"`
	Allowed     bool   `json:"allowed"`
	Explanation string `json:"explanation"`
}

// ComparisonResult is the result of comparing two canonical snapshots.
type ComparisonResult struct {
	ReferenceBackend string       `json:"reference_backend"`
	ActualBackend    string       `json:"actual_backend"`
	Equal            bool         `json:"equal"`
	Differences      []Difference `json:"differences"`
}

// CompareSnapshots recursively compares all canonical business fields.
func CompareSnapshots(
	reference CanonicalSnapshot,
	actual CanonicalSnapshot,
	referenceBackend string,
	actualBackend string,
	allowed []AllowedDiffRule,
) ComparisonResult {
	result := ComparisonResult{
		ReferenceBackend: referenceBackend,
		ActualBackend:    actualBackend,
		Equal:            true,
		Differences:      []Difference{},
	}
	var left, right any
	leftRaw, leftErr := json.Marshal(reference.Snapshot)
	rightRaw, rightErr := json.Marshal(actual.Snapshot)
	if leftErr != nil || rightErr != nil {
		result.Equal = false
		result.Differences = append(result.Differences, Difference{
			Category: "serialization", Path: "$",
			Reference: errorText(leftErr), Actual: errorText(rightErr),
			Explanation: "canonical snapshot serialization failed",
		})
		return result
	}
	_ = json.Unmarshal(leftRaw, &left)
	_ = json.Unmarshal(rightRaw, &right)
	compareValue("$", left, right, &result.Differences)
	for i := range result.Differences {
		diff := &result.Differences[i]
		diff.Category = categoryForPath(diff.Path)
		diff.SessionID, diff.EventIndex, diff.SummaryID = locateContext(
			diff.Path, reference.Snapshot, actual.Snapshot,
		)
		for _, rule := range allowed {
			if (rule.Backend == "" || rule.Backend == actualBackend) &&
				pathMatches(rule.Path, diff.Path) {
				diff.Allowed = true
				diff.Explanation = rule.Reason
				break
			}
		}
		if !diff.Allowed {
			result.Equal = false
			if diff.Explanation == "" {
				diff.Explanation = "persisted business values differ"
			}
		}
	}
	return result
}

func compareValue(path string, left, right any, out *[]Difference) {
	if reflect.DeepEqual(left, right) {
		return
	}
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap && rightIsMap {
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
			compareValue(path+"."+key, leftMap[key], rightMap[key], out)
		}
		return
	}
	leftSlice, leftIsSlice := left.([]any)
	rightSlice, rightIsSlice := right.([]any)
	if leftIsSlice && rightIsSlice {
		max := len(leftSlice)
		if len(rightSlice) > max {
			max = len(rightSlice)
		}
		for i := 0; i < max; i++ {
			var lv, rv any
			if i < len(leftSlice) {
				lv = leftSlice[i]
			}
			if i < len(rightSlice) {
				rv = rightSlice[i]
			}
			compareValue(fmt.Sprintf("%s[%d]", path, i), lv, rv, out)
		}
		return
	}
	*out = append(*out, Difference{Path: path, Reference: left, Actual: right})
}

func categoryForPath(path string) string {
	switch {
	case strings.Contains(path, ".tracks"):
		return "track"
	case strings.Contains(path, ".events"):
		return "event"
	case strings.Contains(path, ".state"):
		return "state"
	case strings.Contains(path, ".memories"):
		return "memory"
	case strings.Contains(path, ".summaries"):
		return "summary"
	default:
		return "session"
	}
}

func locateContext(path string, left, right Snapshot) (string, *int, string) {
	sessionPattern := regexp.MustCompile(`\.sessions\[(\d+)\]`)
	eventPattern := regexp.MustCompile(`\.events\[(\d+)\]`)
	summaryPattern := regexp.MustCompile(`\.summaries\.([^\.\[]*)`)
	var sessionID string
	if match := sessionPattern.FindStringSubmatch(path); len(match) == 2 {
		var index int
		_, _ = fmt.Sscanf(match[1], "%d", &index)
		if index < len(left.Sessions) {
			sessionID = left.Sessions[index].ID
		} else if index < len(right.Sessions) {
			sessionID = right.Sessions[index].ID
		}
	}
	if !strings.Contains(path, ".tracks") {
		if match := eventPattern.FindStringSubmatch(path); len(match) == 2 {
			var index int
			_, _ = fmt.Sscanf(match[1], "%d", &index)
			return sessionID, &index, ""
		}
	}
	if match := summaryPattern.FindStringSubmatch(path); len(match) == 2 {
		summaryID := match[1]
		if summaryID == "" {
			summaryID = "<all>"
		}
		return sessionID, nil, summaryID
	}
	return sessionID, nil, ""
}

func pathMatches(pattern, path string) bool {
	if pattern == path {
		return true
	}
	expr := regexp.QuoteMeta(pattern)
	expr = strings.ReplaceAll(expr, `\*`, `[^.\[\]]+`)
	matched, err := regexp.MatchString("^"+expr+"$", path)
	return err == nil && matched
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
