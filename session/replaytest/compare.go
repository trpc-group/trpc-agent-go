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
	"bytes"
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

type missingValueType string

const missingValueMarker = missingValueType(missingValue)

func (value missingValueType) String() string {
	return string(value)
}

func isMissingValue(value any) bool {
	_, ok := value.(missingValueType)
	return ok
}

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
	rules             *allowedDiffTracker
	differences       []Difference
}

// CompareSnapshots compares two normalized snapshots.
func CompareSnapshots(input CompareInput) ([]Difference, error) {
	rules, err := newAllowedDiffTracker(input.Options.AllowedDiffRules)
	if err != nil {
		return nil, err
	}
	differences, err := compareSnapshots(input, rules)
	if err != nil {
		return nil, err
	}
	if err := rules.validateConsumed(); err != nil {
		return nil, err
	}
	return differences, nil
}

func compareSnapshots(input CompareInput, rules *allowedDiffTracker) ([]Difference, error) {
	options := input.Options
	if math.IsNaN(options.ScoreTolerance) || math.IsInf(options.ScoreTolerance, 0) {
		return nil, fmt.Errorf("score tolerance must be finite")
	}
	if options.ScoreTolerance <= 0 {
		options.ScoreTolerance = defaultScoreTolerance
	}
	if options.DurationTolerance <= 0 {
		options.DurationTolerance = time.Millisecond
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
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return overlaySpecialSnapshotValues(reflect.ValueOf(snapshot), value), nil
}

func overlaySpecialSnapshotValues(source reflect.Value, target any) any {
	source = unwrapSnapshotValue(source)
	if !source.IsValid() {
		return target
	}
	if source.CanInterface() {
		if invalid, ok := normalizeInvalidRawJSON(source.Interface()); ok {
			return invalid
		}
	}
	switch source.Kind() {
	case reflect.Map:
		return overlaySpecialMapValues(source, target)
	case reflect.Slice, reflect.Array:
		return overlaySpecialSliceValues(source, target)
	case reflect.Struct:
		if source.Type() == reflect.TypeOf(time.Time{}) {
			return target
		}
		return overlaySpecialStructValues(source, target)
	default:
		return target
	}
}

func unwrapSnapshotValue(value reflect.Value) reflect.Value {
	for value.IsValid() && (value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer) {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func overlaySpecialMapValues(source reflect.Value, target any) any {
	targetMap, ok := target.(map[string]any)
	if !ok {
		return target
	}
	iterator := source.MapRange()
	for iterator.Next() {
		key := iterator.Key()
		if key.Kind() != reflect.String {
			continue
		}
		name := key.String()
		child, ok := targetMap[name]
		if !ok {
			continue
		}
		targetMap[name] = overlaySpecialSnapshotValues(iterator.Value(), child)
	}
	return targetMap
}

func overlaySpecialSliceValues(source reflect.Value, target any) any {
	targetSlice, ok := target.([]any)
	if !ok {
		return target
	}
	limit := source.Len()
	if len(targetSlice) < limit {
		limit = len(targetSlice)
	}
	for i := 0; i < limit; i++ {
		targetSlice[i] = overlaySpecialSnapshotValues(source.Index(i), targetSlice[i])
	}
	return targetSlice
}

func overlaySpecialStructValues(source reflect.Value, target any) any {
	targetMap, ok := target.(map[string]any)
	if !ok {
		return target
	}
	sourceType := source.Type()
	for i := 0; i < source.NumField(); i++ {
		field := sourceType.Field(i)
		name, ok := jsonFieldName(field)
		if !ok {
			continue
		}
		child, exists := targetMap[name]
		if !exists {
			continue
		}
		targetMap[name] = overlaySpecialSnapshotValues(source.Field(i), child)
	}
	return targetMap
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" {
		return "", false
	}
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		name = field.Name
	}
	return name, true
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
	if exactJSONNumbersEqual(baseline, actual) {
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

func exactJSONNumbersEqual(baseline, actual any) bool {
	baselineNumber, baselineOK := baseline.(json.Number)
	actualNumber, actualOK := actual.(json.Number)
	if !baselineOK || !actualOK {
		return false
	}
	baselineCanonical, baselineOK := canonicalJSONNumber(baselineNumber.String())
	actualCanonical, actualOK := canonicalJSONNumber(actualNumber.String())
	return baselineOK && actualOK && baselineCanonical == actualCanonical
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
		childPath := appendMapPath(path, key)
		childLocator := locatorForValue(childPath, baselineValue, actualValue, locator)
		if isStateMapPath(path) {
			childLocator.StateKey = key
		}
		switch {
		case !baselineOK:
			if isKnownSnapshotSlicePath(childPath, actualValue) {
				comparator.compareValues(childPath, []any{}, actualValue, childLocator)
				continue
			}
			comparator.differences = append(comparator.differences,
				comparator.newDifference(childPath, childLocator, missingValueMarker, actualValue))
		case !actualOK:
			if isKnownSnapshotSlicePath(childPath, baselineValue) {
				comparator.compareValues(childPath, baselineValue, []any{}, childLocator)
				continue
			}
			comparator.differences = append(comparator.differences,
				comparator.newDifference(childPath, childLocator, baselineValue, missingValueMarker))
		default:
			comparator.compareValues(childPath, baselineValue, actualValue, childLocator)
		}
	}
}

func isKnownSnapshotSlicePath(path string, value any) bool {
	if _, ok := value.([]any); !ok {
		return false
	}
	switch path {
	case "$.sessions", "$.memories", "$.memory_searches":
		return true
	}
	return isSnapshotFieldPath(path, "events", isSessionItemPath) ||
		isSnapshotFieldPath(path, "summaries", isSessionItemPath) ||
		isSnapshotFieldPath(path, "tracks", isSessionItemPath) ||
		isSnapshotFieldPath(path, "tool_calls", isSessionEventItem) ||
		isSnapshotFieldPath(path, "results", isMemorySearchItemPath) ||
		isSnapshotFieldPath(path, "topics", isMemoryItemPath) ||
		isSnapshotFieldPath(path, "events", isTrackItemPath)
}

func appendMapPath(path, key string) string {
	if key != "" && !strings.ContainsAny(key, ".[]*") {
		return path + "." + key
	}
	encoded, _ := json.Marshal(key)
	return path + "[" + string(encoded) + "]"
}

func isStateMapPath(path string) bool {
	if strings.HasSuffix(path, ".state") {
		return isSessionItemPath(strings.TrimSuffix(path, ".state"))
	}
	if strings.HasSuffix(path, ".state_delta") {
		return isSessionEventItem(strings.TrimSuffix(path, ".state_delta"))
	}
	return false
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
			comparator.newDifference(itemPath, itemLocator, baseline[i], missingValueMarker))
	}
	for i := length; i < len(actual); i++ {
		itemPath := path + "[" + strconv.Itoa(i) + "]"
		itemLocator := locatorForValue(itemPath, nil, actual[i], locator)
		comparator.differences = append(comparator.differences,
			comparator.newDifference(itemPath, itemLocator, missingValueMarker, actual[i]))
	}
}

func (comparator *snapshotComparator) newDifference(
	path string,
	locator Locator,
	baseline any,
	actual any,
) Difference {
	difference := Difference{
		Case:                   comparator.caseName,
		Backend:                comparator.backend,
		Path:                   path,
		Locator:                locator,
		Baseline:               baseline,
		Actual:                 actual,
		BaselineMissing:        isMissingValue(baseline),
		ActualMissing:          isMissingValue(actual),
		BaselineInvalidRawJSON: isInvalidRawJSON(baseline),
		ActualInvalidRawJSON:   isInvalidRawJSON(actual),
		Explanation:            "unexpected normalized snapshot difference",
	}
	if rule, ok := comparator.rules.consume(comparator.caseName, comparator.backend, path); ok {
		difference.AllowedDiff = true
		difference.Explanation = rule.Explanation
	}
	return difference
}

func isInvalidRawJSON(value any) bool {
	_, ok := normalizeInvalidRawJSON(value)
	return ok
}

type allowedDiffTracker struct {
	rules    []AllowedDiffRule
	consumed []bool
}

func newAllowedDiffTracker(rules []AllowedDiffRule) (*allowedDiffTracker, error) {
	if err := validateAllowedDiffRules(rules); err != nil {
		return nil, err
	}
	return &allowedDiffTracker{
		rules:    append([]AllowedDiffRule(nil), rules...),
		consumed: make([]bool, len(rules)),
	}, nil
}

func (tracker *allowedDiffTracker) consume(
	caseName string,
	backend string,
	path string,
) (AllowedDiffRule, bool) {
	for i, rule := range tracker.rules {
		if ruleMatches(rule, caseName, backend, path) {
			tracker.consumed[i] = true
			return rule, true
		}
	}
	return AllowedDiffRule{}, false
}

func (tracker *allowedDiffTracker) validateConsumed() error {
	for i, consumed := range tracker.consumed {
		if consumed {
			continue
		}
		rule := tracker.rules[i]
		return fmt.Errorf(
			"unused allowed diff rule %d: case=%q backend=%q path=%q",
			i, rule.Case, rule.Backend, rule.Path,
		)
	}
	return nil
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
	case isSessionItemPath(path):
		locator.SessionID = stringValue(value["id"])
	case isSessionEventItem(path):
		if index, ok := finalIndex(path); ok {
			locator.EventIndex = &index
		}
	case isMemoryItemPath(path):
		locator.MemoryID = stringValue(value["id"])
		locator.MemoryAppName, locator.MemoryUserID = memoryScope(value)
	case isSummaryItemPath(path):
		locator.SummaryFilterKey = stringValue(value["filter_key"])
	case isTrackItemPath(path):
		locator.TrackName = stringValue(value["name"])
	}
	return locator
}

func memoryScope(value map[string]any) (string, string) {
	scope, ok := value["scope"].(map[string]any)
	if ok {
		return stringValue(scope["app_name"]), stringValue(scope["user_id"])
	}
	return stringValue(value["app_name"]), stringValue(value["user_id"])
}

func validateAllowedDiffRules(rules []AllowedDiffRule) error {
	seen := make(map[allowedDiffMatchKey]struct{}, len(rules))
	for i, rule := range rules {
		if rule.Case == "" || rule.Backend == "" || rule.Path == "" || rule.Explanation == "" {
			return fmt.Errorf(
				"allowed diff rule %d requires case, backend, path, and explanation",
				i,
			)
		}
		if hasPathWildcard(rule.Path) {
			return fmt.Errorf("allowed diff rule %d contains a wildcard path", i)
		}
		if rule.PathPrefix && rule.Path == "$" {
			return fmt.Errorf("allowed diff rule %d uses a whole-snapshot path prefix", i)
		}
		key := allowedDiffMatchKey{
			caseName:   rule.Case,
			backend:    rule.Backend,
			path:       rule.Path,
			pathPrefix: rule.PathPrefix,
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("allowed diff rule %d is duplicated", i)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type allowedDiffMatchKey struct {
	caseName   string
	backend    string
	path       string
	pathPrefix bool
}

func hasPathWildcard(path string) bool {
	inQuotedSegment := false
	escaped := false
	for i := 0; i < len(path); i++ {
		switch {
		case escaped:
			escaped = false
		case inQuotedSegment && path[i] == '\\':
			escaped = true
		case inQuotedSegment && path[i] == '"':
			inQuotedSegment = false
		case !inQuotedSegment && path[i] == '[' && i+1 < len(path) && path[i+1] == '"':
			inQuotedSegment = true
			i++
		case !inQuotedSegment && path[i] == '*':
			return true
		}
	}
	return false
}

func scoreValuesEqual(path string, baseline, actual any, tolerance float64) bool {
	itemPath := strings.TrimSuffix(path, ".score")
	if itemPath == path || !isMemoryItemPath(itemPath) {
		return false
	}
	baselineScore, baselineOK := numericFloat64(baseline)
	actualScore, actualOK := numericFloat64(actual)
	return baselineOK && actualOK && math.Abs(baselineScore-actualScore) <= tolerance
}

func durationValuesEqual(path string, baseline, actual any, tolerance time.Duration) bool {
	if !isTrackEventDurationPath(path) {
		return false
	}
	baselineDuration, baselineOK := numericFloat64(baseline)
	actualDuration, actualOK := numericFloat64(actual)
	return baselineOK && actualOK && math.Abs(baselineDuration-actualDuration) <= float64(tolerance)
}

func isTrackEventDurationPath(path string) bool {
	const suffix = ".duration"
	itemPath := strings.TrimSuffix(path, suffix)
	if itemPath == path {
		return false
	}
	return isTrackEventItemPath(itemPath)
}

func isTrackEventItemPath(path string) bool {
	trackPath, ok := parentForIndexedChild(path, "events")
	return ok && isTrackItemPath(trackPath)
}

func numericFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func isMemoryItemPath(path string) bool {
	if isRootCollectionItem(path, "memories") {
		return true
	}
	return isMemorySearchResultItemPath(path)
}

func isRootCollectionItem(path, collection string) bool {
	prefix := "$." + collection + "["
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "]") {
		return false
	}
	return isPathIndex(path[len(prefix) : len(path)-1])
}

func isSessionItemPath(path string) bool {
	return isRootCollectionItem(path, "sessions")
}

func isSessionEventItem(path string) bool {
	sessionPath, ok := parentForIndexedChild(path, "events")
	return ok && isSessionItemPath(sessionPath)
}

func isSummaryItemPath(path string) bool {
	sessionPath, ok := parentForIndexedChild(path, "summaries")
	return ok && isSessionItemPath(sessionPath)
}

func isTrackItemPath(path string) bool {
	sessionPath, ok := parentForIndexedChild(path, "tracks")
	return ok && isSessionItemPath(sessionPath)
}

func isMemorySearchItemPath(path string) bool {
	return isRootCollectionItem(path, "memory_searches")
}

func isMemorySearchResultItemPath(path string) bool {
	searchPath, ok := parentForIndexedChild(path, "results")
	return ok && isMemorySearchItemPath(searchPath)
}

func isSnapshotFieldPath(path, field string, parentMatch func(string) bool) bool {
	parent := strings.TrimSuffix(path, "."+field)
	return parent != path && parentMatch(parent)
}

func parentForIndexedChild(path, collection string) (string, bool) {
	marker := "." + collection + "["
	position := strings.LastIndex(path, marker)
	if position < 0 || !strings.HasSuffix(path, "]") {
		return "", false
	}
	index := path[position+len(marker) : len(path)-1]
	if !isPathIndex(index) {
		return "", false
	}
	parent := path[:position]
	return parent, parent != ""
}

func isPathIndex(index string) bool {
	if index == "" {
		return false
	}
	for i := 0; i < len(index); i++ {
		if index[i] < '0' || index[i] > '9' {
			return false
		}
	}
	return true
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
