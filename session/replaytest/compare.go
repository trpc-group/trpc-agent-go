//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const missingValue = "<missing>"

const defaultScoreTolerance = 1e-6

// CompareInput describes one normalized snapshot comparison.
type CompareInput struct {
	Case     string
	Backend  string
	Baseline Snapshot
	Actual   Snapshot
	Options  CompareOptions
}

type snapshotComparator struct {
	caseName          string
	backend           string
	scoreTolerance    float64
	durationTolerance time.Duration
	rules             []AllowedDiffRule
	differences       []Difference
}

// CompareSnapshots compares two normalized snapshots.
func CompareSnapshots(input CompareInput) ([]Difference, error) {
	options := input.Options
	if options.ScoreTolerance <= 0 {
		options.ScoreTolerance = defaultScoreTolerance
	}
	if options.DurationTolerance <= 0 {
		options.DurationTolerance = time.Millisecond
	}
	rules := options.AllowedDiffRules
	if err := validateAllowedDiffRules(rules); err != nil {
		return nil, err
	}
	baselineValue, err := snapshotValue(input.Baseline)
	if err != nil {
		return nil, fmt.Errorf("encode baseline snapshot: %w", err)
	}
	actualValue, err := snapshotValue(input.Actual)
	if err != nil {
		return nil, fmt.Errorf("encode actual snapshot: %w", err)
	}
	comparator := snapshotComparator{
		caseName:          input.Case,
		backend:           input.Backend,
		scoreTolerance:    options.ScoreTolerance,
		durationTolerance: options.DurationTolerance,
		rules:             rules,
		differences:       make([]Difference, 0),
	}
	comparator.compareValues("$", baselineValue, actualValue, Locator{})
	differences := comparator.differences
	sort.Slice(differences, func(i, j int) bool {
		if differences[i].Path != differences[j].Path {
			return differences[i].Path < differences[j].Path
		}
		return fmt.Sprint(differences[i].Actual) < fmt.Sprint(differences[j].Actual)
	})
	return differences, nil
}

func snapshotValue(snapshot Snapshot) (any, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (comparator *snapshotComparator) compareValues(
	path string,
	baseline any,
	actual any,
	locator Locator,
) {
	locator = locatorForValue(path, baseline, actual, locator)
	baselineMap, baselineIsMap := baseline.(map[string]any)
	actualMap, actualIsMap := actual.(map[string]any)
	if baselineIsMap && actualIsMap {
		comparator.compareMaps(path, baselineMap, actualMap, locator)
		return
	}
	baselineSlice, baselineIsSlice := baseline.([]any)
	actualSlice, actualIsSlice := actual.([]any)
	if baselineIsSlice && actualIsSlice {
		comparator.compareSlices(path, baselineSlice, actualSlice, locator)
		return
	}
	if reflect.DeepEqual(baseline, actual) {
		return
	}
	if scoreValuesEqual(path, baseline, actual, comparator.scoreTolerance) {
		return
	}
	if durationValuesEqual(path, baseline, actual, comparator.durationTolerance) {
		return
	}
	comparator.differences = append(
		comparator.differences,
		comparator.newDifference(path, locator, baseline, actual),
	)
}

func (comparator *snapshotComparator) compareMaps(
	path string,
	baseline map[string]any,
	actual map[string]any,
	locator Locator,
) {
	keys := make(map[string]struct{}, len(baseline)+len(actual))
	for key := range baseline {
		keys[key] = struct{}{}
	}
	for key := range actual {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		baselineValue, baselineOK := baseline[key]
		actualValue, actualOK := actual[key]
		childPath := path + "." + key
		childLocator := locatorForValue(childPath, baselineValue, actualValue, locator)
		switch {
		case !baselineOK:
			if _, ok := actualValue.([]any); ok {
				comparator.compareValues(childPath, []any{}, actualValue, locator)
				continue
			}
			comparator.differences = append(comparator.differences,
				comparator.newDifference(childPath, childLocator, missingValue, actualValue))
		case !actualOK:
			if _, ok := baselineValue.([]any); ok {
				comparator.compareValues(childPath, baselineValue, []any{}, locator)
				continue
			}
			comparator.differences = append(comparator.differences,
				comparator.newDifference(childPath, childLocator, baselineValue, missingValue))
		default:
			comparator.compareValues(childPath, baselineValue, actualValue, locator)
		}
	}
}

func (comparator *snapshotComparator) compareSlices(
	path string,
	baseline []any,
	actual []any,
	locator Locator,
) {
	if len(baseline) != len(actual) {
		comparator.differences = append(comparator.differences,
			comparator.newDifference(path+".length", locator, len(baseline), len(actual)))
	}
	length := len(baseline)
	if len(actual) < length {
		length = len(actual)
	}
	for i := 0; i < length; i++ {
		comparator.compareValues(
			path+"["+strconv.Itoa(i)+"]", baseline[i], actual[i], locator,
		)
	}
	for i := length; i < len(baseline); i++ {
		itemPath := path + "[" + strconv.Itoa(i) + "]"
		itemLocator := locatorForValue(itemPath, baseline[i], nil, locator)
		comparator.differences = append(comparator.differences,
			comparator.newDifference(itemPath, itemLocator, baseline[i], missingValue))
	}
	for i := length; i < len(actual); i++ {
		itemPath := path + "[" + strconv.Itoa(i) + "]"
		itemLocator := locatorForValue(itemPath, nil, actual[i], locator)
		comparator.differences = append(comparator.differences,
			comparator.newDifference(itemPath, itemLocator, missingValue, actual[i]))
	}
}

func (comparator *snapshotComparator) newDifference(
	path string,
	locator Locator,
	baseline any,
	actual any,
) Difference {
	difference := Difference{
		Case:        comparator.caseName,
		Backend:     comparator.backend,
		Path:        path,
		Locator:     locator,
		Baseline:    baseline,
		Actual:      actual,
		Explanation: "unexpected normalized snapshot difference",
	}
	for _, rule := range comparator.rules {
		if ruleMatches(rule, comparator.caseName, comparator.backend, path) {
			difference.AllowedDiff = true
			difference.Explanation = rule.Explanation
			break
		}
	}
	return difference
}

func ruleMatches(rule AllowedDiffRule, caseName, backend, differencePath string) bool {
	if rule.Case != caseName {
		return false
	}
	if rule.Backend != backend {
		return false
	}
	if rule.PathPrefix {
		return differencePath == rule.Path ||
			strings.HasPrefix(differencePath, rule.Path+".") ||
			strings.HasPrefix(differencePath, rule.Path+"[")
	}
	return differencePath == rule.Path
}

func locatorForValue(path string, baseline, actual any, locator Locator) Locator {
	locator.StateKey = stateKeyForPath(path, locator.StateKey)
	value, ok := actual.(map[string]any)
	if !ok {
		baselineValue, baselineOK := baseline.(map[string]any)
		if baselineOK {
			value = baselineValue
		}
	}
	if value == nil {
		return locator
	}
	switch {
	case isCollectionItem(path, "sessions"):
		locator.SessionID = stringValue(value["id"])
	case isSessionEventItem(path):
		if index, ok := finalIndex(path); ok {
			locator.EventIndex = &index
		}
	case isMemoryItemPath(path):
		locator.MemoryID = stringValue(value["id"])
		locator.MemoryAppName, locator.MemoryUserID = memoryScope(value)
	case isCollectionItem(path, "summaries"):
		locator.SummaryFilterKey = stringValue(value["filter_key"])
	case isCollectionItem(path, "tracks"):
		locator.TrackName = stringValue(value["name"])
	}
	return locator
}

func stateKeyForPath(path, current string) string {
	separator := strings.LastIndex(path, ".")
	if separator < 0 {
		return current
	}
	parent := path[:separator]
	if strings.HasSuffix(parent, ".state") || strings.HasSuffix(parent, ".state_delta") {
		return path[separator+1:]
	}
	return current
}

func memoryScope(value map[string]any) (string, string) {
	scope, ok := value["scope"].(map[string]any)
	if ok {
		return stringValue(scope["app_name"]), stringValue(scope["user_id"])
	}
	return stringValue(value["app_name"]), stringValue(value["user_id"])
}

func validateAllowedDiffRules(rules []AllowedDiffRule) error {
	seen := make(map[AllowedDiffRule]struct{}, len(rules))
	for i, rule := range rules {
		if rule.Case == "" || rule.Backend == "" || rule.Path == "" || rule.Explanation == "" {
			return fmt.Errorf(
				"allowed diff rule %d requires case, backend, path, and explanation",
				i,
			)
		}
		if strings.Contains(rule.Path, "*") {
			return fmt.Errorf("allowed diff rule %d contains a wildcard path", i)
		}
		if _, exists := seen[rule]; exists {
			return fmt.Errorf("allowed diff rule %d is duplicated", i)
		}
		seen[rule] = struct{}{}
	}
	return nil
}

func scoreValuesEqual(path string, baseline, actual any, tolerance float64) bool {
	itemPath := strings.TrimSuffix(path, ".score")
	if itemPath == path || !isMemoryItemPath(itemPath) {
		return false
	}
	baselineScore, baselineOK := baseline.(float64)
	actualScore, actualOK := actual.(float64)
	return baselineOK && actualOK && math.Abs(baselineScore-actualScore) <= tolerance
}

func durationValuesEqual(path string, baseline, actual any, tolerance time.Duration) bool {
	if !strings.HasSuffix(path, ".duration") || !strings.Contains(path, ".tracks[") ||
		!strings.Contains(path, "].events[") {
		return false
	}
	baselineDuration, baselineOK := baseline.(float64)
	actualDuration, actualOK := actual.(float64)
	return baselineOK && actualOK && math.Abs(baselineDuration-actualDuration) <= float64(tolerance)
}

func isMemoryItemPath(path string) bool {
	if isRootCollectionItem(path, "memories") {
		return true
	}
	const prefix = "$.memory_searches["
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "]") {
		return false
	}
	searchEnd := strings.Index(path[len(prefix):], "]")
	if searchEnd < 0 {
		return false
	}
	searchEnd += len(prefix)
	if _, err := strconv.Atoi(path[len(prefix):searchEnd]); err != nil {
		return false
	}
	const resultsMarker = ".results["
	if !strings.HasPrefix(path[searchEnd+1:], resultsMarker) {
		return false
	}
	resultStart := searchEnd + 1 + len(resultsMarker)
	_, err := strconv.Atoi(path[resultStart : len(path)-1])
	return err == nil
}

func isRootCollectionItem(path, collection string) bool {
	prefix := "$." + collection + "["
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "]") {
		return false
	}
	_, err := strconv.Atoi(path[len(prefix) : len(path)-1])
	return err == nil
}

func isSessionEventItem(path string) bool {
	const marker = ".events["
	position := strings.LastIndex(path, marker)
	if position < 0 || !strings.HasSuffix(path, "]") {
		return false
	}
	return isCollectionItem(path[:position], "sessions")
}

func isCollectionItem(path, collection string) bool {
	marker := "." + collection + "["
	position := strings.LastIndex(path, marker)
	if position < 0 || !strings.HasSuffix(path, "]") {
		return false
	}
	return !strings.Contains(path[position+len(marker):len(path)-1], ".")
}

func finalIndex(path string) (int, bool) {
	open := strings.LastIndex(path, "[")
	if open < 0 || !strings.HasSuffix(path, "]") {
		return 0, false
	}
	index, err := strconv.Atoi(path[open+1 : len(path)-1])
	return index, err == nil
}

func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}
