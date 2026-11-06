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
	"math/big"
	pathpkg "path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Compare returns every semantic difference between two normalized snapshots.
// It rejects empty or inconsistent case and backend identities, invalid
// AllowedDiff rules, values that cannot be cloned and encoded safely, and
// either snapshot when its encoded comparison value exceeds 8 MiB.
func Compare(caseName string, baseline, actual Snapshot, allowed []AllowedDiff) ([]Diff, error) {
	if caseName == "" {
		return nil, errors.New("replaytest: comparison case name is required")
	}
	if err := validateSnapshotMetadata(caseName, baseline, actual); err != nil {
		return nil, err
	}
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
	return compareSnapshotValues(caseName, baseline, actual, left, right, allowed)
}

func compareSnapshotValues(
	caseName string,
	baseline Snapshot,
	actual Snapshot,
	left map[string]any,
	right map[string]any,
	allowed []AllowedDiff,
) ([]Diff, error) {
	comparator := comparator{
		caseName: caseName,
		baseline: baseline,
		actual:   actual,
		allowed:  allowed,
	}
	comparator.compareNode("", left, true, right, true)
	if comparator.err != nil {
		return nil, comparator.err
	}
	return comparator.diffs, nil
}

func validateSnapshotMetadata(caseName string, baseline, actual Snapshot) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "comparison case name", value: caseName},
		{name: "baseline backend name", value: baseline.Backend},
		{name: "actual backend name", value: actual.Backend},
		{name: "baseline case name", value: baseline.Case},
		{name: "actual case name", value: actual.Case},
	} {
		if err := validateBoundedUTF8String(field.name, field.value, maxReplayIdentifierSize); err != nil {
			return fmt.Errorf("replaytest: %w", err)
		}
	}
	if baseline.Backend == "" || actual.Backend == "" {
		return errors.New("replaytest: comparison backend names are required")
	}
	if baseline.Backend == "*" || actual.Backend == "*" {
		return errors.New("replaytest: comparison backend name \"*\" is reserved")
	}
	if baseline.Backend == actual.Backend {
		return fmt.Errorf("replaytest: comparison backend %q is repeated", baseline.Backend)
	}
	if baseline.Case != caseName || actual.Case != caseName {
		return fmt.Errorf(
			"replaytest: comparison case %q does not match snapshots %q and %q",
			caseName,
			baseline.Case,
			actual.Case,
		)
	}
	return nil
}

type comparator struct {
	caseName string
	baseline Snapshot
	actual   Snapshot
	allowed  []AllowedDiff
	diffs    []Diff
	err      error
}

func (c *comparator) compareNode(path string, left any, leftExists bool, right any, rightExists bool) {
	if c.err != nil {
		return
	}
	if !leftExists || !rightExists {
		c.addDiff(path, left, leftExists, right, rightExists)
		return
	}
	if c.compareMaps(path, left, right) {
		return
	}
	if c.compareArrays(path, left, right) {
		return
	}
	if !reflect.DeepEqual(left, right) {
		c.addDiff(path, left, true, right, true)
	}
}

func (c *comparator) compareMaps(path string, left, right any) bool {
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if !leftIsMap && !rightIsMap {
		return false
	}
	if !leftIsMap || !rightIsMap {
		c.addDiff(path, left, true, right, true)
		return true
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
		leftValue, leftExists := leftMap[key]
		rightValue, rightExists := rightMap[key]
		c.compareNode(path+"/"+escapePointer(key), leftValue, leftExists, rightValue, rightExists)
	}
	return true
}

func (c *comparator) compareArrays(path string, left, right any) bool {
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if !leftIsArray && !rightIsArray {
		return false
	}
	if !leftIsArray || !rightIsArray {
		c.addDiff(path, left, true, right, true)
		return true
	}
	if len(leftArray) != len(rightArray) {
		c.addDiff(path+"/length", len(leftArray), true, len(rightArray), true)
	}
	length := max(len(leftArray), len(rightArray))
	for index := 0; index < length; index++ {
		var leftValue, rightValue any
		leftExists := index < len(leftArray)
		rightExists := index < len(rightArray)
		if leftExists {
			leftValue = leftArray[index]
		}
		if rightExists {
			rightValue = rightArray[index]
		}
		c.compareNode(path+"/"+strconv.Itoa(index), leftValue, leftExists, rightValue, rightExists)
	}
	return true
}

func (c *comparator) addDiff(
	path string,
	left any,
	leftExists bool,
	right any,
	rightExists bool,
) {
	if c.err != nil {
		return
	}
	if len(c.diffs) >= maxReplayDiffsPerCase {
		c.err = fmt.Errorf("replaytest: case %q exceeds %d diffs", c.caseName, maxReplayDiffsPerCase)
		return
	}
	if path == "" {
		path = "/"
	}
	explanation := "semantic values differ"
	if !leftExists {
		explanation = "baseline path is missing"
	} else if !rightExists {
		explanation = "actual path is missing"
	}
	diff := Diff{
		Case:        c.caseName,
		BackendA:    c.baseline.Backend,
		BackendB:    c.actual.Backend,
		SessionID:   c.diffSessionID(),
		Path:        path,
		Baseline:    left,
		Actual:      right,
		Explanation: explanation,
	}
	c.addLocator(&diff)
	for _, rule := range c.allowed {
		if !backendPairMatches(rule, c.baseline.Backend, c.actual.Backend) {
			continue
		}
		matched, err := pathpkg.Match(rule.Path, path)
		if err != nil || !matched {
			continue
		}
		if allowedByRule(rule, left, leftExists, right, rightExists) {
			diff.Allowed = true
			diff.Explanation = rule.Reason
			break
		}
	}
	c.diffs = append(c.diffs, diff)
}

func (c *comparator) diffSessionID() string {
	if sessionID := stringValue(c.baseline.Session["id"]); sessionID != "" {
		return sessionID
	}
	if sessionID := stringValue(c.actual.Session["id"]); sessionID != "" {
		return sessionID
	}
	return c.caseName
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
	case "event_pages":
		if len(parts) >= 3 {
			if index, err := strconv.Atoi(parts[2]); err == nil {
				diff.EventIndex = &index
			}
		}
	case "summaries":
		filterKey := parts[1]
		diff.SummaryFilterKey = &filterKey
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
	case "memory_searches":
		if len(parts) < 3 {
			return
		}
		index, err := strconv.Atoi(parts[2])
		if err != nil {
			return
		}
		baseline := c.baseline.MemorySearches[parts[1]]
		actual := c.actual.MemorySearches[parts[1]]
		if index < len(baseline) {
			diff.MemoryID = stringValue(baseline[index]["id"])
		} else if index < len(actual) {
			diff.MemoryID = stringValue(actual[index]["id"])
		}
	}
}

func snapshotValue(snapshot Snapshot) (map[string]any, error) {
	comparable := CanonicalMap{
		"session":           snapshot.Session,
		"events":            snapshot.Events,
		"event_pages":       snapshot.EventPages,
		"expiration_checks": snapshot.ExpirationChecks,
		"event_order":       snapshot.EventOrder,
		"state":             snapshot.State,
		"memories":          snapshot.Memories,
		"memory_searches":   snapshot.MemorySearches,
		"summaries":         snapshot.Summaries,
		"tracks":            snapshot.Tracks,
	}
	if err := validateJSONStrings("snapshot", reflect.ValueOf(comparable)); err != nil {
		return nil, err
	}
	raw, err := marshalJSONValue("snapshot", comparable, maxReplaySnapshotSize)
	if err != nil {
		return nil, err
	}
	var output map[string]any
	if err := decodeJSON(raw, &output); err != nil {
		return nil, err
	}
	return output, nil
}

func validateAllowedDiffs(rules []AllowedDiff) error {
	return validateAllowedDiffsWithPathValidator(rules, validateSnapshotPathPattern)
}

func validateRunnerAllowedDiffs(rules []AllowedDiff) error {
	return validateAllowedDiffsWithPathValidator(rules, validateRunnerSnapshotPathPattern)
}

//nolint:gocyclo // Keeping the complete allowed-diff policy in one validator makes rule interactions auditable.
func validateAllowedDiffsWithPathValidator(
	rules []AllowedDiff,
	validatePath func(string) error,
) error {
	if len(rules) > maxReplayAllowedDiffs {
		return fmt.Errorf("%d allowed_diff rules exceed limit %d", len(rules), maxReplayAllowedDiffs)
	}
	for index, rule := range rules {
		if rule.BackendA == "" || rule.BackendB == "" || rule.Path == "" || rule.Reason == "" {
			return fmt.Errorf("allowed_diff %d requires backend_a, backend_b, path, and reason", index)
		}
		if rule.BackendA != "*" && rule.BackendA == rule.BackendB {
			return fmt.Errorf("allowed_diff %d must name two distinct backends", index)
		}
		for _, field := range []struct {
			name  string
			value string
			limit int
		}{
			{name: "backend_a", value: rule.BackendA, limit: maxReplayIdentifierSize},
			{name: "backend_b", value: rule.BackendB, limit: maxReplayIdentifierSize},
			{name: "path", value: rule.Path, limit: maxReplayPathSize},
			{name: "reason", value: rule.Reason, limit: maxReplayExplanationSize},
		} {
			if err := validateBoundedUTF8String("allowed_diff "+field.name, field.value, field.limit); err != nil {
				return fmt.Errorf("allowed_diff %d: %w", index, err)
			}
		}
		if err := validateBoundedUTF8String("allowed_diff rule", string(rule.Rule), maxReplayIdentifierSize); err != nil {
			return fmt.Errorf("allowed_diff %d: %w", index, err)
		}
		if !strings.HasPrefix(rule.Path, "/") {
			return fmt.Errorf("allowed_diff %d path must be a JSON pointer glob", index)
		}
		if err := validateJSONPointerEscapes(rule.Path); err != nil {
			return fmt.Errorf("allowed_diff %d has invalid JSON pointer escape: %w", index, err)
		}
		if _, err := pathpkg.Match(rule.Path, rule.Path); err != nil {
			return fmt.Errorf("allowed_diff %d has invalid path glob: %w", index, err)
		}
		if err := validatePath(rule.Path); err != nil {
			return fmt.Errorf("allowed_diff %d: %w", index, err)
		}
		switch rule.Rule {
		case AllowedIgnore, AllowedSameType:
		case AllowedWithinDelta:
			if rule.Delta < 0 || math.IsNaN(rule.Delta) || math.IsInf(rule.Delta, 0) {
				return fmt.Errorf("allowed_diff %d delta must be finite and non-negative", index)
			}
		default:
			return fmt.Errorf("allowed_diff %d has unknown rule %q", index, rule.Rule)
		}
	}
	return nil
}

func validateAllowedDiffBackends(rules []AllowedDiff, backendNames map[string]struct{}) error {
	for index, rule := range rules {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "backend_a", value: rule.BackendA},
			{name: "backend_b", value: rule.BackendB},
		} {
			if field.value == "*" {
				continue
			}
			if _, ok := backendNames[field.value]; !ok {
				return fmt.Errorf("allowed_diff %d names unknown %s %q", index, field.name, field.value)
			}
		}
	}
	return nil
}

func validateSnapshotPathPattern(path string) error {
	parts := pointerParts(path)
	if len(parts) == 0 {
		return nil
	}
	if err := validatePathShape(parts, true); err != nil {
		return fmt.Errorf("path is outside the normalized snapshot domains: %w", err)
	}
	return nil
}

func validateRunnerSnapshotPathPattern(path string) error {
	if err := validateSnapshotPathPattern(path); err != nil {
		return err
	}
	return validateNormalizedSessionPath(pointerParts(path), true)
}

var normalizedSessionFields = []string{
	"id",
	"app_name",
	"user_id",
	"created_at",
	"updated_at",
}

func validateNormalizedSessionPath(parts []string, allowWildcards bool) error {
	if len(parts) == 0 || parts[0] != "session" || len(parts) == 1 {
		return nil
	}
	if len(parts) != 2 {
		return errors.New("session scalar path has an invalid suffix")
	}
	field := parts[1]
	if allowWildcards && hasUnescapedGlobMeta(field) {
		for _, candidate := range normalizedSessionFields {
			if matched, _ := pathpkg.Match(field, candidate); matched {
				return nil
			}
		}
		return fmt.Errorf("session path pattern %q matches no normalized field", field)
	}
	for _, candidate := range normalizedSessionFields {
		if field == candidate {
			return nil
		}
	}
	return fmt.Errorf("session field %q is not in the normalized schema", field)
}

//nolint:gocyclo // The normalized JSON-pointer grammar is centralized so domain path rules cannot drift apart.
func validatePathShape(parts []string, allowWildcards bool) error {
	if len(parts) == 0 {
		return nil
	}
	domain := parts[0]
	isWildcard := func(value string) bool {
		return allowWildcards && hasUnescapedGlobMeta(value)
	}
	requireObjectFields := func(start int) error {
		for _, field := range parts[start:] {
			if field == "" || (!allowWildcards && isWildcard(field)) {
				return errors.New("object path has an invalid field")
			}
		}
		return nil
	}
	requireArrayIndex := func(part string) error {
		if isWildcard(part) {
			return nil
		}
		if part == "length" {
			return nil
		}
		if _, err := parseCanonicalIndex(part); err != nil {
			return errors.New("array path has an invalid index")
		}
		return nil
	}
	requireSchemaFields := func(start int, allowed map[string]struct{}) error {
		for _, field := range parts[start:] {
			if field == "" {
				return errors.New("object path has an invalid field")
			}
			if isWildcard(field) {
				continue
			}
			if _, ok := allowed[field]; !ok && !isWildcard(field) {
				return fmt.Errorf("object path field %q is not in the normalized schema", field)
			}
		}
		return nil
	}
	requireArrayField := func(index int, allowed map[string]struct{}) error {
		if index >= len(parts) {
			return nil
		}
		if parts[index] == "length" {
			if index != len(parts)-1 {
				return errors.New("array length path has an invalid suffix")
			}
			return nil
		}
		if err := requireArrayIndex(parts[index]); err != nil {
			return err
		}
		if index == len(parts)-1 {
			return nil
		}
		return requireSchemaFields(index+1, allowed)
	}
	validateEventFields := func(start int) error {
		if start >= len(parts) {
			return nil
		}
		// Event extensions contain arbitrary JSON. Empty object keys are valid,
		// including an empty extension-map key on a directly constructed Event.
		if parts[start] == "extensions" {
			return nil
		}
		// JSON state values and provider-specific tool-call fields are the other
		// arbitrary JSON regions in the normalized Event schema.
		if len(parts) > start+2 && parts[start] == "stateDelta" &&
			parts[start+1] != "" && parts[start+2] == "json" {
			return nil
		}
		if len(parts) > start+5 && parts[start] == "choices" &&
			(parts[start+2] == "message" || parts[start+2] == "delta") &&
			parts[start+3] == "tool_calls" && parts[start+5] == "extra_fields" {
			return nil
		}
		return requireObjectFields(start)
	}
	validateStateValue := func(index int) error {
		if index >= len(parts) {
			return nil
		}
		field := parts[index]
		if isWildcard(field) {
			if len(parts) != index+1 {
				return errors.New("state wildcard field has an invalid suffix")
			}
			return nil
		}
		switch field {
		case "kind", "base64":
			if len(parts) != index+1 {
				return errors.New("state scalar path has an invalid suffix")
			}
			return nil
		case "json":
			// JSON state values are arbitrary JSON, so empty object keys and
			// additional nested tokens are valid below this point.
			return nil
		default:
			return fmt.Errorf("object path field %q is not in the normalized schema", field)
		}
	}
	switch domain {
	case "session":
		// Session metadata may contain backend-specific fields. Keep the path
		// grammar structural here rather than freezing the extensible object
		// schema to the fields emitted by the current normalizer.
		return requireObjectFields(1)
	case "events":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] == "length" {
			if len(parts) != 2 {
				return errors.New("event length path has an invalid suffix")
			}
			return nil
		}
		if err := requireArrayIndex(parts[1]); err != nil {
			return err
		}
		return validateEventFields(2)
	case "event_pages":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] == "" {
			return errors.New("event-page path has an empty page name")
		}
		if len(parts) == 2 {
			return nil
		}
		if parts[2] == "length" {
			if len(parts) != 3 {
				return errors.New("event-page length path has an invalid suffix")
			}
			return nil
		}
		if err := requireArrayIndex(parts[2]); err != nil {
			return err
		}
		return validateEventFields(3)
	case "expiration_checks":
		if len(parts) == 1 {
			return nil
		}
		if len(parts) != 2 || parts[1] == "" {
			return errors.New("expiration-check path has an invalid name or suffix")
		}
		return nil
	case "event_order":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] == "" {
			return errors.New("event-order path has an empty lane")
		}
		if len(parts) == 2 {
			return nil
		}
		if err := requireArrayIndex(parts[2]); err != nil {
			return err
		}
		if len(parts) != 3 {
			return errors.New("event-order array path has an invalid suffix")
		}
		return nil
	case "state":
		if len(parts) == 1 {
			return nil
		}
		if isWildcard(parts[1]) {
			if len(parts) == 2 {
				return nil
			}
			if parts[2] == "" {
				return errors.New("state path has an empty key")
			}
			if len(parts) == 3 {
				return nil
			}
			return validateStateValue(3)
		}
		switch parts[1] {
		case "app", "user", "session":
		default:
			return fmt.Errorf("unknown state scope %q", parts[1])
		}
		if len(parts) == 2 {
			return nil
		}
		if parts[2] == "" {
			return errors.New("state path has an empty key")
		}
		if len(parts) == 3 {
			return nil
		}
		return validateStateValue(3)
	case "memories":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] == "length" {
			if len(parts) != 2 {
				return errors.New("memory length path has an invalid suffix")
			}
			return nil
		}
		if err := requireArrayIndex(parts[1]); err != nil {
			return err
		}
		if len(parts) == 2 {
			return nil
		}
		switch parts[2] {
		case "id", "app_name", "user_id", "created_at", "updated_at", "score", "content":
			if len(parts) != 3 {
				return errors.New("memory scalar path has an invalid suffix")
			}
			return nil
		case "memory":
			if len(parts) == 3 {
				return nil
			}
			switch parts[3] {
			case "memory", "last_updated", "kind", "event_time", "location":
				if len(parts) != 4 {
					return errors.New("memory scalar path has an invalid suffix")
				}
				return nil
			case "topics", "participants":
				return requireArrayField(4, nil)
			default:
				return fmt.Errorf("object path field %q is not in the normalized schema", parts[3])
			}
		default:
			return fmt.Errorf("object path field %q is not in the normalized schema", parts[2])
		}
	case "memory_searches":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] == "" {
			return errors.New("memory-search path has an empty name")
		}
		if len(parts) == 2 {
			return nil
		}
		if parts[2] == "length" {
			if len(parts) != 3 {
				return errors.New("memory-search length path has an invalid suffix")
			}
			return nil
		}
		if err := requireArrayIndex(parts[2]); err != nil {
			return err
		}
		if len(parts) == 3 {
			return nil
		}
		switch parts[3] {
		case "id", "app_name", "user_id", "created_at", "updated_at", "score", "content":
			if len(parts) != 4 {
				return errors.New("memory scalar path has an invalid suffix")
			}
			return nil
		case "memory":
			if len(parts) == 4 {
				return nil
			}
			switch parts[4] {
			case "memory", "last_updated", "kind", "event_time", "location":
				if len(parts) != 5 {
					return errors.New("memory scalar path has an invalid suffix")
				}
				return nil
			case "topics", "participants":
				return requireArrayField(5, nil)
			default:
				if isWildcard(parts[4]) {
					if len(parts) != 5 {
						return errors.New("memory wildcard field has an invalid suffix")
					}
					return nil
				}
				return fmt.Errorf("object path field %q is not in the normalized schema", parts[4])
			}
		default:
			return fmt.Errorf("object path field %q is not in the normalized schema", parts[3])
		}
	case "summaries":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] != "" && isWildcard(parts[1]) {
			// Wildcard filter keys are valid only in allowed-diff patterns.
		}
		if len(parts) == 2 {
			return nil
		}
		switch parts[2] {
		case "text", "updated_at":
			if len(parts) != 3 {
				return errors.New("summary scalar path has an invalid suffix")
			}
			return nil
		case "topics", "retained_event_ids":
			return requireArrayField(3, nil)
		case "boundary":
			if len(parts) == 3 {
				return nil
			}
			return requireSchemaFields(3, map[string]struct{}{
				"version": {}, "filter_key": {}, "cutoff_at": {}, "last_event_id": {},
			})
		default:
			if isWildcard(parts[2]) {
				if len(parts) != 3 {
					return errors.New("summary wildcard field has an invalid suffix")
				}
				return nil
			}
			return fmt.Errorf("object path field %q is not in the normalized schema", parts[2])
		}
	case "tracks":
		if len(parts) == 1 {
			return nil
		}
		if parts[1] == "" {
			return errors.New("track path has an empty name")
		}
		if len(parts) == 2 {
			return nil
		}
		if parts[2] == "length" {
			if len(parts) != 3 {
				return errors.New("track length path has an invalid suffix")
			}
			return nil
		}
		if err := requireArrayIndex(parts[2]); err != nil {
			return err
		}
		if len(parts) == 3 {
			return nil
		}
		switch parts[3] {
		case "track", "timestamp":
			if len(parts) != 4 {
				return errors.New("track scalar path has an invalid suffix")
			}
			return nil
		case "payload":
			// Payload is arbitrary JSON. Empty object keys are valid JSON and are
			// represented by empty JSON Pointer tokens, including a trailing "/".
			return nil
		default:
			return fmt.Errorf("object path field %q is not in the normalized schema", parts[3])
		}
	case "execution":
		if len(parts) != 1 {
			return errors.New("execution evidence path has an invalid suffix")
		}
		return nil
	case "capabilities":
		if len(parts) != 2 || isWildcard(parts[1]) || !isKnownCapability(Capability(parts[1])) {
			return errors.New("capability evidence path is invalid")
		}
		return nil
	default:
		return fmt.Errorf("unknown snapshot path domain %q", domain)
	}
}

func validateJSONPointerEscapes(pointer string) error {
	for index := 0; index < len(pointer); index++ {
		if pointer[index] != '~' {
			continue
		}
		if index+1 >= len(pointer) ||
			(pointer[index+1] != '0' && pointer[index+1] != '1') {
			return fmt.Errorf("invalid escape at byte %d", index)
		}
		index++
	}
	return nil
}

func hasUnescapedGlobMeta(value string) bool {
	escaped := false
	for index := 0; index < len(value); index++ {
		if escaped {
			escaped = false
			continue
		}
		if value[index] == '\\' {
			escaped = true
			continue
		}
		if strings.ContainsRune("*?[", rune(value[index])) {
			return true
		}
	}
	return false
}

func allowedByRule(
	rule AllowedDiff,
	left any,
	leftExists bool,
	right any,
	rightExists bool,
) bool {
	switch rule.Rule {
	case AllowedIgnore:
		return true
	case AllowedSameType:
		return leftExists && rightExists && reflect.TypeOf(left) == reflect.TypeOf(right)
	case AllowedWithinDelta:
		return leftExists && rightExists && numbersWithinDelta(left, right, rule.Delta)
	default:
		return false
	}
}

func numbersWithinDelta(left, right any, delta float64) bool {
	leftNumber, leftOK := exactNumber(left)
	rightNumber, rightOK := exactNumber(right)
	deltaNumber, deltaOK := exactNumber(delta)
	if !leftOK || !rightOK || !deltaOK {
		return false
	}
	difference := new(big.Rat).Sub(leftNumber, rightNumber)
	difference.Abs(difference)
	return difference.Cmp(deltaNumber) <= 0
}

func exactNumber(value any) (*big.Rat, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, false
		}
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		text = strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case int:
		text = strconv.FormatInt(int64(typed), 10)
	case int64:
		text = strconv.FormatInt(typed, 10)
	case uint:
		text = strconv.FormatUint(uint64(typed), 10)
	case uint64:
		text = strconv.FormatUint(typed, 10)
	default:
		return nil, false
	}
	return parseBoundedDecimal(text)
}

const (
	maxExactNumberCharacters = 1024
	maxExactNumberExponent   = 1024
)

func parseBoundedDecimal(text string) (*big.Rat, bool) {
	if text == "" || len(text) > maxExactNumberCharacters {
		return nil, false
	}
	mantissa := text
	exponent := 0
	if exponentIndex := strings.IndexAny(text, "eE"); exponentIndex >= 0 {
		if strings.IndexAny(text[exponentIndex+1:], "eE") >= 0 {
			return nil, false
		}
		mantissa = text[:exponentIndex]
		parsed, err := strconv.ParseInt(text[exponentIndex+1:], 10, 32)
		if err != nil || parsed < -maxExactNumberExponent || parsed > maxExactNumberExponent {
			return nil, false
		}
		exponent = int(parsed)
	}

	negative := strings.HasPrefix(mantissa, "-")
	if negative {
		mantissa = strings.TrimPrefix(mantissa, "-")
	}
	parts := strings.Split(mantissa, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		return nil, false
	}
	fractionDigits := 0
	digits := parts[0]
	if len(parts) == 2 {
		fractionDigits = len(parts[1])
		digits += parts[1]
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return nil, false
		}
	}
	numerator, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}
	if negative {
		numerator.Neg(numerator)
	}
	scale := exponent - fractionDigits
	if scale >= 0 {
		numerator.Mul(numerator, decimalPower(scale))
		return new(big.Rat).SetInt(numerator), true
	}
	return new(big.Rat).SetFrac(numerator, decimalPower(-scale)), true
}

func decimalPower(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func backendMatches(pattern, backend string) bool {
	return pattern == "*" || pattern == backend
}

func backendPairMatches(rule AllowedDiff, backendA, backendB string) bool {
	direct := backendMatches(rule.BackendA, backendA) && backendMatches(rule.BackendB, backendB)
	reverse := backendMatches(rule.BackendA, backendB) && backendMatches(rule.BackendB, backendA)
	return direct || reverse
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

// IsClean reports whether a structurally valid report contains no blocking
// differences or failed cases.
func (r Report) IsClean() bool {
	return r.Validate() == nil && r.BlockingDiffs == 0 && r.FailedCases == 0
}

// Validate checks report metadata, resource limits, status and diff accounting,
// locator and exclusion evidence, and the completeness of the selected
// comparison matrix. Validation is structural and is not a cryptographic
// authenticity check for reports received from an untrusted source.
func (r Report) Validate() error {
	if err := validateReportSizeBudget(r); err != nil {
		return err
	}
	backendNames, err := validateReportMetadata(r)
	if err != nil {
		return err
	}
	totals, err := validateReportCases(r, backendNames)
	if err != nil {
		return err
	}
	if err := validateReportTotals(r, totals); err != nil {
		return err
	}
	return validateReportEncodedSize(r)
}

type reportTotals struct {
	passed      int
	failed      int
	unsupported int
	blocking    int
	allowed     int
	diffs       int
}

func validateReportMetadata(r Report) (map[string]struct{}, error) {
	if r.GeneratedAt.IsZero() {
		return nil, errors.New("replaytest: report generated_at is required")
	}
	if err := validateBoundedUTF8String("report comparison mode", string(r.ComparisonMode), maxReplayIdentifierSize); err != nil {
		return nil, fmt.Errorf("replaytest: %w", err)
	}
	if err := validateBoundedUTF8String("report reference backend", r.Reference, maxReplayIdentifierSize); err != nil {
		return nil, fmt.Errorf("replaytest: %w", err)
	}
	if len(r.Backends) < 2 {
		return nil, errors.New("replaytest: report requires at least two backends")
	}
	if len(r.Backends) > maxReplayBackends {
		return nil, fmt.Errorf("replaytest: report has %d backends, limit is %d", len(r.Backends), maxReplayBackends)
	}
	if r.ComparisonMode != ComparisonReference && r.ComparisonMode != ComparisonConsensus {
		return nil, fmt.Errorf("replaytest: report has unknown comparison mode %q", r.ComparisonMode)
	}
	backendNames := make(map[string]struct{}, len(r.Backends))
	for _, backend := range r.Backends {
		if backend == "" {
			return nil, errors.New("replaytest: report backend name is required")
		}
		if backend == "*" {
			return nil, errors.New("replaytest: report backend name \"*\" is reserved")
		}
		if err := validateBoundedUTF8String("report backend name", backend, maxReplayIdentifierSize); err != nil {
			return nil, fmt.Errorf("replaytest: %w", err)
		}
		if _, exists := backendNames[backend]; exists {
			return nil, fmt.Errorf("replaytest: duplicate report backend %q", backend)
		}
		backendNames[backend] = struct{}{}
	}
	if r.ComparisonMode == ComparisonReference {
		if _, ok := backendNames[r.Reference]; !ok {
			return nil, fmt.Errorf("replaytest: reference backend %q is not in backends", r.Reference)
		}
	} else if r.Reference != "" {
		return nil, errors.New("replaytest: consensus report must not name a reference backend")
	}
	if r.TotalCases != len(r.Cases) {
		return nil, fmt.Errorf("replaytest: total_cases=%d but cases=%d", r.TotalCases, len(r.Cases))
	}
	if r.TotalCases == 0 {
		return nil, errors.New("replaytest: report requires at least one case")
	}
	if r.TotalCases > maxReplayCases {
		return nil, fmt.Errorf("replaytest: report has %d cases, limit is %d", r.TotalCases, maxReplayCases)
	}
	return backendNames, nil
}

func validateReportCases(r Report, backendNames map[string]struct{}) (reportTotals, error) {
	caseNames := make(map[string]struct{}, len(r.Cases))
	var totals reportTotals
	for _, result := range r.Cases {
		if len(result.Diffs) > maxReplayDiffsPerCase {
			return reportTotals{}, fmt.Errorf(
				"replaytest: case %q has %d diffs, limit is %d",
				result.Name,
				len(result.Diffs),
				maxReplayDiffsPerCase,
			)
		}
		if len(result.Diffs) > maxReplayReportDiffs-totals.diffs {
			return reportTotals{}, fmt.Errorf("replaytest: report exceeds %d diffs", maxReplayReportDiffs)
		}
		totals.diffs += len(result.Diffs)
		if result.Name == "" {
			return reportTotals{}, errors.New("replaytest: report case name is required")
		}
		if err := validateBoundedUTF8String("report case name", result.Name, maxReplayIdentifierSize); err != nil {
			return reportTotals{}, fmt.Errorf("replaytest: %w", err)
		}
		if _, exists := caseNames[result.Name]; exists {
			return reportTotals{}, fmt.Errorf("replaytest: duplicate report case %q", result.Name)
		}
		caseNames[result.Name] = struct{}{}
		if err := totals.addStatus(result); err != nil {
			return reportTotals{}, err
		}
		caseBlocking, caseAllowed, err := validateCaseResult(
			r.ComparisonMode,
			r.Reference,
			result,
			backendNames,
		)
		if err != nil {
			return reportTotals{}, err
		}
		totals.blocking += caseBlocking
		totals.allowed += caseAllowed
	}
	return totals, nil
}

func (t *reportTotals) addStatus(result CaseResult) error {
	if err := validateBoundedUTF8String("report case status", string(result.Status), maxReplayIdentifierSize); err != nil {
		return fmt.Errorf("replaytest: case %q: %w", result.Name, err)
	}
	switch result.Status {
	case StatusPassed:
		t.passed++
	case StatusFailed:
		t.failed++
	case StatusUnsupported:
		t.unsupported++
	default:
		return fmt.Errorf("replaytest: case %q has unknown status %q", result.Name, result.Status)
	}
	return nil
}

func validateCaseResult(
	mode ComparisonMode,
	reference string,
	result CaseResult,
	backendNames map[string]struct{},
) (int, int, error) {
	if result.Duration < 0 {
		return 0, 0, fmt.Errorf("replaytest: case %q has negative duration", result.Name)
	}
	if err := validateLocatorEvidence(result.LocatorEvidence, backendNames); err != nil {
		return 0, 0, fmt.Errorf("replaytest: case %q: %w", result.Name, err)
	}
	blocking, allowed := countDiffs(result.Diffs)
	hasCapabilityEvidence, err := validateCaseDiffs(result, backendNames)
	if err != nil {
		return 0, 0, err
	}
	if err := validateCaseComparison(mode, reference, result, backendNames); err != nil {
		return 0, 0, err
	}
	expectedStatus := expectedCaseStatus(blocking, hasCapabilityEvidence)
	if result.Status != expectedStatus {
		return 0, 0, fmt.Errorf(
			"replaytest: case %q has status %q, want %q from its evidence",
			result.Name,
			result.Status,
			expectedStatus,
		)
	}
	return blocking, allowed, nil
}

func validateCaseDiffs(result CaseResult, backendNames map[string]struct{}) (bool, error) {
	hasCapabilityEvidence := false
	type semanticDiffKey struct {
		backendA string
		backendB string
		path     string
	}
	seenSemanticDiffs := make(map[semanticDiffKey]struct{}, len(result.Diffs))
	for index, diff := range result.Diffs {
		capabilityEvidence, err := validateDiff(result.Name, index, diff, backendNames, result.LocatorEvidence)
		if err != nil {
			return false, err
		}
		if diff.Exclusion == nil {
			key := semanticDiffKey{
				backendA: diff.BackendA,
				backendB: diff.BackendB,
				path:     diff.Path,
			}
			if _, exists := seenSemanticDiffs[key]; exists {
				return false, fmt.Errorf(
					"replaytest: case %q repeats semantic diff for backends %q/%q at %q",
					result.Name,
					diff.BackendA,
					diff.BackendB,
					diff.Path,
				)
			}
			seenSemanticDiffs[key] = struct{}{}
		}
		hasCapabilityEvidence = hasCapabilityEvidence || capabilityEvidence
	}
	return hasCapabilityEvidence, nil
}

func validateDiff(
	caseName string,
	index int,
	diff Diff,
	backendNames map[string]struct{},
	locatorEvidence *LocatorEvidence,
) (bool, error) {
	if err := validateDiffStrings(diff); err != nil {
		return false, fmt.Errorf("replaytest: case %q diff %d: %w", caseName, index, err)
	}
	if err := validateExclusionDiff(caseName, diff, backendNames); err != nil {
		return false, err
	}
	capabilityEvidence, err := validateCapabilityEvidence(caseName, diff)
	if err != nil {
		return false, err
	}
	if diff.Case != caseName || diff.BackendA == "" || diff.BackendB == "" || diff.SessionID == "" || !strings.HasPrefix(diff.Path, "/") {
		return false, fmt.Errorf("replaytest: case %q diff %d has an invalid locator", caseName, index)
	}
	for _, value := range []struct {
		name  string
		value any
	}{{"diff baseline", diff.Baseline}, {"diff actual", diff.Actual}} {
		if err := validateJSONValue(value.name, value.value); err != nil {
			return false, fmt.Errorf("replaytest: case %q diff %d %s: %w", caseName, index, value.name, err)
		}
	}
	if err := validateJSONPointerEscapes(diff.Path); err != nil {
		return false, fmt.Errorf("replaytest: case %q diff %d has invalid JSON pointer escape: %w", caseName, index, err)
	}
	if err := validateDomainLocators(diff, locatorEvidence); err != nil {
		return false, fmt.Errorf("replaytest: case %q diff %d: %w", caseName, index, err)
	}
	if _, ok := backendNames[diff.BackendA]; !ok {
		return false, fmt.Errorf("replaytest: case %q diff %d names unknown backend %q", caseName, index, diff.BackendA)
	}
	if _, ok := backendNames[diff.BackendB]; !ok {
		return false, fmt.Errorf("replaytest: case %q diff %d names unknown backend %q", caseName, index, diff.BackendB)
	}
	if diff.Allowed && diff.Explanation == "" {
		return false, fmt.Errorf("replaytest: case %q diff %d has no allowed_diff explanation", caseName, index)
	}
	return capabilityEvidence, nil
}

func validateDiffStrings(diff Diff) error {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"diff case", diff.Case, maxReplayIdentifierSize},
		{"diff backend_a", diff.BackendA, maxReplayIdentifierSize},
		{"diff backend_b", diff.BackendB, maxReplayIdentifierSize},
		{"diff session id", diff.SessionID, maxReplayIdentifierSize},
		{"diff path", diff.Path, maxReplayPathSize},
		{"diff track name", diff.TrackName, maxReplayTrackNameSize},
		{"diff memory id", diff.MemoryID, maxReplayMemorySearchNameSize},
		{"diff explanation", diff.Explanation, maxReplayExplanationSize},
	} {
		if err := validateBoundedUTF8String(field.name, field.value, field.limit); err != nil {
			return err
		}
	}
	if diff.SummaryFilterKey != nil {
		if err := validateBoundedUTF8String("diff summary filter key", *diff.SummaryFilterKey, maxReplaySummaryKeySize); err != nil {
			return err
		}
	}
	if diff.Exclusion != nil {
		if err := validateBoundedUTF8String("diff exclusion backend", diff.Exclusion.Backend, maxReplayIdentifierSize); err != nil {
			return err
		}
		if err := validateBoundedUTF8String("diff exclusion kind", string(diff.Exclusion.Kind), maxReplayIdentifierSize); err != nil {
			return err
		}
		if err := validateBoundedUTF8String("diff exclusion capability", string(diff.Exclusion.Capability), maxReplayIdentifierSize); err != nil {
			return err
		}
		if err := validateBoundedUTF8String("diff exclusion error", diff.Exclusion.Error, maxReplayErrorSize); err != nil {
			return err
		}
	}
	return nil
}

func validateExclusionDiff(caseName string, diff Diff, backendNames map[string]struct{}) error {
	if diff.Path == "/execution" {
		if diff.Exclusion == nil {
			return fmt.Errorf("replaytest: case %q has execution evidence without structured exclusion", caseName)
		}
		evidence := diff.Exclusion
		if evidence.Kind != ExclusionExecutionFailure {
			return fmt.Errorf("replaytest: case %q has invalid execution exclusion kind %q", caseName, evidence.Kind)
		}
		if evidence.Backend == "" || evidence.Error == "" {
			return fmt.Errorf("replaytest: case %q has incomplete execution exclusion evidence", caseName)
		}
		if _, ok := backendNames[evidence.Backend]; !ok {
			return fmt.Errorf("replaytest: case %q execution exclusion names unknown backend %q", caseName, evidence.Backend)
		}
		if evidence.Capability != "" {
			return fmt.Errorf("replaytest: case %q execution exclusion names a capability", caseName)
		}
		if diff.Allowed || evidence.Error != stringValue(diff.Actual) {
			return fmt.Errorf("replaytest: case %q has malformed execution exclusion error", caseName)
		}
		baseline, baselineOK := diff.Baseline.(string)
		if !baselineOK || baseline != "success" || diff.Explanation != "backend replay failed" {
			return fmt.Errorf("replaytest: case %q has malformed execution exclusion payload", caseName)
		}
		if diff.BackendA != evidence.Backend && diff.BackendB != evidence.Backend {
			return fmt.Errorf("replaytest: case %q execution exclusion backend is not in diff", caseName)
		}
		if diff.BackendB != evidence.Backend {
			return fmt.Errorf("replaytest: case %q execution exclusion backend is not the compared backend", caseName)
		}
		return nil
	}
	if strings.HasPrefix(diff.Path, "/capabilities/") {
		if diff.Exclusion == nil {
			return fmt.Errorf("replaytest: case %q has capability evidence without structured exclusion", caseName)
		}
		if diff.Exclusion.Backend != diff.BackendB {
			return fmt.Errorf("replaytest: case %q capability exclusion backend is not the compared backend", caseName)
		}
		return nil
	}
	if diff.Exclusion != nil {
		return fmt.Errorf("replaytest: case %q attaches exclusion evidence to a semantic diff", caseName)
	}
	return nil
}

func validateCapabilityEvidence(caseName string, diff Diff) (bool, error) {
	if !strings.HasPrefix(diff.Path, "/capabilities/") {
		return false, nil
	}
	if _, ok := capabilityFromEvidencePath(diff.Path); !ok {
		return false, fmt.Errorf(
			"replaytest: case %q has unknown capability evidence path %q",
			caseName,
			diff.Path,
		)
	}
	if !diff.Allowed {
		return false, fmt.Errorf("replaytest: case %q has blocking capability evidence", caseName)
	}
	baseline, baselineOK := diff.Baseline.(bool)
	actual, actualOK := diff.Actual.(bool)
	if !baselineOK || !actualOK || !baseline || actual {
		return false, fmt.Errorf("replaytest: case %q has malformed capability evidence", caseName)
	}
	evidence := diff.Exclusion
	if evidence == nil || evidence.Kind != ExclusionUnsupportedCapability {
		return false, fmt.Errorf("replaytest: case %q has malformed capability exclusion", caseName)
	}
	capability, _ := capabilityFromEvidencePath(diff.Path)
	if evidence.Capability != capability || evidence.Backend == "" || evidence.Error != "" ||
		(diff.BackendA != evidence.Backend && diff.BackendB != evidence.Backend) ||
		diff.Explanation != "backend reports this capability as unsupported" {
		return false, fmt.Errorf("replaytest: case %q has malformed capability exclusion payload", caseName)
	}
	return true, nil
}

//nolint:gocyclo // Locator catalogs share cross-map uniqueness and membership invariants that must be checked together.
func validateLocatorEvidence(evidence *LocatorEvidence, backendNames map[string]struct{}) error {
	if evidence == nil {
		return nil
	}
	if len(evidence.MemoryIDs) > len(backendNames) {
		return fmt.Errorf("locator evidence contains %d memory catalogs, want at most %d", len(evidence.MemoryIDs), len(backendNames))
	}
	if len(evidence.MemorySearchIDs) > len(backendNames) {
		return fmt.Errorf("locator evidence contains %d search catalogs, want at most %d", len(evidence.MemorySearchIDs), len(backendNames))
	}
	totalMemoryIDBytes := 0
	for backend, ids := range evidence.MemoryIDs {
		if err := validateBoundedUTF8String("locator evidence backend", backend, maxReplayIdentifierSize); err != nil {
			return err
		}
		if _, ok := backendNames[backend]; !ok {
			return fmt.Errorf("locator evidence names unknown backend %q", backend)
		}
		seen := make(map[string]struct{}, len(ids))
		if len(ids) > maxReplayMemories {
			return fmt.Errorf("locator evidence for backend %q exceeds %d memories", backend, maxReplayMemories)
		}
		for index, id := range ids {
			if id == "" {
				return fmt.Errorf("locator evidence for backend %q memory %d has no id", backend, index)
			}
			if err := validateUTF8String("locator memory id", id); err != nil {
				return err
			}
			if len(id) > maxReplayMemorySearchNameSize {
				return fmt.Errorf("locator evidence for backend %q memory %d id exceeds %d bytes", backend, index, maxReplayMemorySearchNameSize)
			}
			if totalMemoryIDBytes > maxReplayMemorySearchNameTotalSize-len(id) {
				return fmt.Errorf("locator memory ids exceed %d total bytes", maxReplayMemorySearchNameTotalSize)
			}
			totalMemoryIDBytes += len(id)
			if _, exists := seen[id]; exists {
				return fmt.Errorf("locator evidence for backend %q repeats memory id %q", backend, id)
			}
			seen[id] = struct{}{}
		}
	}
	for backend, searches := range evidence.MemorySearchIDs {
		if err := validateBoundedUTF8String("locator evidence backend", backend, maxReplayIdentifierSize); err != nil {
			return err
		}
		if _, ok := backendNames[backend]; !ok {
			return fmt.Errorf("locator evidence names unknown backend %q", backend)
		}
		catalog := make(map[string]struct{}, len(evidence.MemoryIDs[backend]))
		for _, id := range evidence.MemoryIDs[backend] {
			catalog[id] = struct{}{}
		}
		if len(searches) > maxReplayMemorySearchCount {
			return fmt.Errorf("locator evidence for backend %q exceeds %d searches", backend, maxReplayMemorySearchCount)
		}
		total := 0
		totalNameBytes := 0
		totalIDBytes := 0
		for name, ids := range searches {
			if name == "" {
				return fmt.Errorf("locator evidence for backend %q has an empty search name", backend)
			}
			if err := validateUTF8String("locator memory search name", name); err != nil {
				return err
			}
			if len(name) > maxReplayMemorySearchNameSize {
				return fmt.Errorf("locator evidence for backend %q search name exceeds %d bytes", backend, maxReplayMemorySearchNameSize)
			}
			if totalNameBytes > maxReplayMemorySearchNameTotalSize-len(name) {
				return fmt.Errorf("locator memory search names exceed %d total bytes", maxReplayMemorySearchNameTotalSize)
			}
			totalNameBytes += len(name)
			if len(ids) > maxReplayMemories || total > maxReplayMemories-len(ids) {
				return fmt.Errorf("locator evidence for backend %q exceeds %d search results", backend, maxReplayMemories)
			}
			total += len(ids)
			seen := make(map[string]struct{}, len(ids))
			for _, id := range ids {
				if id == "" {
					return fmt.Errorf("locator evidence for backend %q search %q has an empty id", backend, name)
				}
				if len(id) > maxReplayMemorySearchNameSize {
					return fmt.Errorf("locator evidence for backend %q search %q id exceeds %d bytes", backend, name, maxReplayMemorySearchNameSize)
				}
				if totalIDBytes > maxReplayMemorySearchNameTotalSize-len(id) {
					return fmt.Errorf("locator search ids for backend %q exceed %d total bytes", backend, maxReplayMemorySearchNameTotalSize)
				}
				totalIDBytes += len(id)
				if _, ok := catalog[id]; !ok {
					return fmt.Errorf("locator evidence for backend %q search %q names unknown memory %q", backend, name, id)
				}
				if _, exists := seen[id]; exists {
					return fmt.Errorf("locator evidence for backend %q search %q repeats memory id %q", backend, name, id)
				}
				seen[id] = struct{}{}
			}
		}
	}
	return nil
}

//nolint:gocyclo // Domain locator requirements form one closed schema dispatch and are easier to audit centrally.
func validateDomainLocators(diff Diff, evidence *LocatorEvidence) error {
	parts := pointerParts(diff.Path)
	if err := validatePathShape(parts, false); err != nil {
		return err
	}
	if len(parts) == 0 {
		if diff.EventIndex != nil || diff.SummaryFilterKey != nil || diff.TrackName != "" || diff.MemoryID != "" {
			return errors.New("locator is set for the snapshot root")
		}
		return nil
	}
	domain := parts[0]
	if domain != "events" && domain != "event_pages" && diff.EventIndex != nil {
		return errors.New("event locator is set for a non-event path")
	}
	if domain != "summaries" && diff.SummaryFilterKey != nil {
		return errors.New("summary locator is set for a non-summary path")
	}
	if domain != "tracks" && diff.TrackName != "" {
		return errors.New("track locator is set for a non-track path")
	}
	if domain != "memories" && domain != "memory_searches" && diff.MemoryID != "" {
		return errors.New("memory locator is set for a non-memory path")
	}

	switch domain {
	case "session":
		return validateNormalizedSessionPath(parts, false)
	case "events":
		return validateEventLocator(parts, diff.EventIndex)
	case "event_pages":
		return validateEventPageLocator(parts, diff.EventIndex)
	case "expiration_checks":
		if len(parts) != 2 || parts[1] == "" {
			return errors.New("expiration-check path has an invalid name or suffix")
		}
	case "event_order":
		return validateNestedArrayPath(parts, 1)
	case "state":
		return validateStatePath(parts)
	case "summaries":
		return validateSummaryLocator(parts, diff.SummaryFilterKey)
	case "tracks":
		return validateTrackLocator(parts, diff.TrackName)
	case "memories":
		return validateMemoryLocator(parts, 1, diff, evidence)
	case "memory_searches":
		return validateMemoryLocator(parts, 2, diff, evidence)
	case "execution":
		if len(parts) != 1 {
			return errors.New("execution evidence path has an invalid suffix")
		}
	case "capabilities":
		if len(parts) != 2 {
			return errors.New("capability evidence path has an invalid suffix")
		}
	default:
		return fmt.Errorf("unknown snapshot path domain %q", domain)
	}
	return nil
}

func validateEventPageLocator(parts []string, eventIndex *int) error {
	if len(parts) < 2 || parts[1] == "" {
		return errors.New("event-page path has no page name")
	}
	if len(parts) < 3 {
		if eventIndex != nil {
			return errors.New("event locator is set for an unindexed event-page path")
		}
		return nil
	}
	if parts[2] == "length" {
		if len(parts) != 3 || eventIndex != nil {
			return errors.New("event-page length path has an invalid locator")
		}
		return nil
	}
	index, err := parseCanonicalIndex(parts[2])
	if err != nil || eventIndex == nil || *eventIndex != index {
		return errors.New("event-page index locator does not match its path")
	}
	return nil
}

func validateEventLocator(parts []string, eventIndex *int) error {
	if len(parts) < 2 {
		if eventIndex != nil {
			return errors.New("event locator is set for an unindexed event path")
		}
		return nil
	}
	if parts[1] == "length" {
		if len(parts) != 2 || eventIndex != nil {
			return errors.New("event length path has an invalid locator")
		}
		return nil
	}
	index, err := parseCanonicalIndex(parts[1])
	if err != nil || eventIndex == nil || *eventIndex != index {
		return errors.New("event index locator does not match its path")
	}
	return nil
}

func validateSummaryLocator(parts []string, filterKey *string) error {
	if len(parts) < 2 {
		if filterKey != nil {
			return errors.New("summary locator is set for an unkeyed summary path")
		}
		return nil
	}
	if filterKey == nil {
		return errors.New("summary path has no filter-key locator")
	}
	if *filterKey != parts[1] {
		return errors.New("summary filter-key locator does not match its path")
	}
	return nil
}

func validateTrackLocator(parts []string, trackName string) error {
	if len(parts) < 2 {
		if trackName != "" {
			return errors.New("track locator is set for an unkeyed track path")
		}
		return nil
	}
	if trackName == "" {
		return errors.New("track path has no track-name locator")
	}
	if trackName != parts[1] {
		return errors.New("track-name locator does not match its path")
	}
	if len(parts) == 2 {
		return nil
	}
	if parts[2] == "length" {
		if len(parts) != 3 {
			return errors.New("track length path has an invalid suffix")
		}
		return nil
	}
	if _, err := parseCanonicalIndex(parts[2]); err != nil {
		return errors.New("track path has an invalid event index")
	}
	if len(parts) > 3 && parts[3] == "" {
		return errors.New("track path has an empty field")
	}
	return nil
}

//nolint:gocyclo // Memory locator shape, index, and evidence checks intentionally remain one atomic validator.
func validateMemoryLocator(parts []string, indexPart int, diff Diff, evidence *LocatorEvidence) error {
	if indexPart == 2 && len(parts) > 1 && parts[1] == "" {
		return errors.New("memory search path has an empty search name")
	}
	if len(parts) <= indexPart {
		if diff.MemoryID != "" {
			return errors.New("memory locator is set for an unindexed memory path")
		}
		return nil
	}
	if parts[indexPart] == "length" {
		if len(parts) != indexPart+1 || diff.MemoryID != "" {
			return errors.New("memory length path has an invalid locator")
		}
		return nil
	}
	index, err := parseCanonicalIndex(parts[indexPart])
	if err != nil {
		return errors.New("memory path has an invalid index")
	}
	if diff.MemoryID == "" {
		return errors.New("indexed memory path has no memory-id locator")
	}
	if evidence == nil {
		return errors.New("indexed memory path has no locator evidence")
	}
	var query string
	if indexPart == 2 {
		query = parts[1]
		if query == "" {
			return errors.New("memory search path has an empty search name")
		}
	}
	matched := false
	for _, backend := range []string{diff.BackendA, diff.BackendB} {
		var ids []string
		var present bool
		if indexPart == 1 {
			ids, present = evidence.MemoryIDs[backend]
		} else {
			var searches map[string][]string
			searches, present = evidence.MemorySearchIDs[backend]
			if present {
				ids, present = searches[query]
			}
		}
		if !present {
			return fmt.Errorf("memory locator evidence is missing backend %q", backend)
		}
		if index < len(ids) && ids[index] == diff.MemoryID {
			matched = true
		}
	}
	if !matched {
		return errors.New("memory-id locator does not match indexed locator evidence")
	}
	if parts[len(parts)-1] == "id" {
		baselineID, baselineOK := diff.Baseline.(string)
		actualID, actualOK := diff.Actual.(string)
		if (baselineOK && diff.MemoryID == baselineID) || (actualOK && diff.MemoryID == actualID) {
			return nil
		}
		return errors.New("memory-id locator does not match either id value")
	}
	return nil
}

func parseCanonicalIndex(value string) (int, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("index is not canonical")
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return 0, errors.New("index is not a non-negative decimal")
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("index is out of range")
	}
	return parsed, nil
}

func validateNestedArrayPath(parts []string, mapKeyPart int) error {
	if len(parts) <= mapKeyPart {
		return nil
	}
	if parts[mapKeyPart] == "" {
		return errors.New("array map path has an empty key")
	}
	if len(parts) == mapKeyPart+1 {
		return nil
	}
	indexPart := parts[mapKeyPart+1]
	if indexPart == "length" {
		if len(parts) != mapKeyPart+2 {
			return errors.New("array length path has an invalid suffix")
		}
		return nil
	}
	if _, err := parseCanonicalIndex(indexPart); err != nil {
		return errors.New("array path has an invalid index")
	}
	if len(parts) > mapKeyPart+2 {
		return errors.New("array element path has an invalid suffix")
	}
	return nil
}

func validateStatePath(parts []string) error {
	if len(parts) == 1 {
		return nil
	}
	switch parts[1] {
	case "app", "user", "session":
	default:
		return fmt.Errorf("unknown state scope %q", parts[1])
	}
	if len(parts) == 2 {
		return nil
	}
	if parts[2] == "" {
		return errors.New("state path has an empty key")
	}
	if len(parts) == 3 {
		return nil
	}
	switch parts[3] {
	case "kind", "base64":
		if len(parts) != 4 {
			return errors.New("state scalar path has an invalid suffix")
		}
	case "json":
		return nil
	default:
		return fmt.Errorf("object path field %q is not in the normalized schema", parts[3])
	}
	return nil
}

func capabilityFromEvidencePath(path string) (Capability, bool) {
	const prefix = "/capabilities/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	capability := Capability(strings.TrimPrefix(path, prefix))
	return capability, isKnownCapability(capability)
}

func validateCaseComparison(
	mode ComparisonMode,
	reference string,
	result CaseResult,
	backendNames map[string]struct{},
) error {
	if mode == ComparisonReference {
		if result.Consensus != nil {
			return fmt.Errorf("replaytest: reference case %q contains consensus data", result.Name)
		}
		if result.Reference == nil {
			return fmt.Errorf("replaytest: reference case %q has no reference comparison data", result.Name)
		}
		return validateReferenceResult(result, reference, backendNames)
	}
	if result.Reference != nil {
		return fmt.Errorf("replaytest: consensus case %q contains reference comparison data", result.Name)
	}
	if result.Consensus == nil {
		return fmt.Errorf("replaytest: consensus case %q has no consensus data", result.Name)
	}
	return validateConsensusResult(result.Name, *result.Consensus, result.Diffs, backendNames)
}

//nolint:gocyclo // Reference manifests require pair, exclusion, membership, and counter checks as one invariant.
func validateReferenceResult(
	result CaseResult,
	reference string,
	knownBackends map[string]struct{},
) error {
	if err := validateReferenceDiffs(result, reference); err != nil {
		return err
	}
	comparable, err := validateReferenceBackends(result.Name, result.Reference.ComparableBackends, knownBackends)
	if err != nil {
		return err
	}
	excluded := make(map[string]struct{})
	for _, diff := range result.Diffs {
		if diff.Exclusion != nil {
			excluded[diff.Exclusion.Backend] = struct{}{}
		}
	}
	for backend := range knownBackends {
		_, isComparable := comparable[backend]
		_, isExcluded := excluded[backend]
		if isComparable == isExcluded {
			return fmt.Errorf(
				"replaytest: reference case %q backend %q must be exactly one of comparable or excluded",
				result.Name,
				backend,
			)
		}
	}
	referenceComparable := false
	if _, ok := comparable[reference]; ok {
		referenceComparable = true
	}
	wantPairs := 0
	if referenceComparable {
		wantPairs = len(comparable) - 1
	}
	if len(result.Reference.Pairs) != wantPairs {
		return fmt.Errorf(
			"replaytest: reference case %q has %d pairs, want %d",
			result.Name,
			len(result.Reference.Pairs),
			wantPairs,
		)
	}
	seenPairs := make(map[consensusPairKey]struct{}, len(result.Reference.Pairs))
	var previous consensusPairKey
	for index, pair := range result.Reference.Pairs {
		for _, field := range []struct {
			name  string
			value string
		}{
			{name: "reference pair backend_a", value: pair.BackendA},
			{name: "reference pair backend_b", value: pair.BackendB},
		} {
			if err := validateBoundedUTF8String(field.name, field.value, maxReplayIdentifierSize); err != nil {
				return fmt.Errorf("replaytest: reference case %q: %w", result.Name, err)
			}
		}
		key := consensusPairKey{backendA: pair.BackendA, backendB: pair.BackendB}
		if pair.BackendA >= pair.BackendB {
			return fmt.Errorf("replaytest: reference case %q pair is not ordered", result.Name)
		}
		if index > 0 && (previous.backendA > key.backendA ||
			(previous.backendA == key.backendA && previous.backendB >= key.backendB)) {
			return fmt.Errorf("replaytest: reference case %q pairs are not sorted", result.Name)
		}
		previous = key
		if pair.BackendA != reference && pair.BackendB != reference {
			return fmt.Errorf("replaytest: reference case %q pair omits reference backend", result.Name)
		}
		if _, ok := comparable[pair.BackendA]; !ok {
			return fmt.Errorf("replaytest: reference case %q pair names unavailable backend %q", result.Name, pair.BackendA)
		}
		if _, ok := comparable[pair.BackendB]; !ok {
			return fmt.Errorf("replaytest: reference case %q pair names unavailable backend %q", result.Name, pair.BackendB)
		}
		if _, exists := seenPairs[key]; exists {
			return fmt.Errorf("replaytest: reference case %q repeats pair %q/%q", result.Name, pair.BackendA, pair.BackendB)
		}
		blocking, allowed := countReferencePairDiffs(result.Diffs, key)
		if blocking != pair.BlockingDiffs || allowed != pair.AllowedDiffs {
			return fmt.Errorf("replaytest: reference case %q pair counters do not add up", result.Name)
		}
		seenPairs[key] = struct{}{}
	}
	if referenceComparable {
		for backend := range comparable {
			if backend == reference {
				continue
			}
			backendA, backendB := reference, backend
			if backendA > backendB {
				backendA, backendB = backendB, backendA
			}
			if _, ok := seenPairs[consensusPairKey{backendA: backendA, backendB: backendB}]; !ok {
				return fmt.Errorf("replaytest: reference case %q has no pair for backend %q", result.Name, backend)
			}
		}
	}
	return nil
}

func validateReferenceBackends(
	caseName string,
	backends []string,
	knownBackends map[string]struct{},
) (map[string]struct{}, error) {
	if !sort.StringsAreSorted(backends) {
		return nil, fmt.Errorf("replaytest: reference case %q comparable backends are not sorted", caseName)
	}
	seen := make(map[string]struct{}, len(backends))
	for _, backend := range backends {
		if err := validateBoundedUTF8String("reference comparable backend", backend, maxReplayIdentifierSize); err != nil {
			return nil, fmt.Errorf("replaytest: reference case %q: %w", caseName, err)
		}
		if _, ok := knownBackends[backend]; !ok {
			return nil, fmt.Errorf("replaytest: reference case %q names unknown backend %q", caseName, backend)
		}
		if _, exists := seen[backend]; exists {
			return nil, fmt.Errorf("replaytest: reference case %q repeats backend %q", caseName, backend)
		}
		seen[backend] = struct{}{}
	}
	return seen, nil
}

func countReferencePairDiffs(diffs []Diff, key consensusPairKey) (blocking, allowed int) {
	for _, diff := range diffs {
		if diff.Exclusion != nil {
			continue
		}
		backendA, backendB := diff.BackendA, diff.BackendB
		if backendA > backendB {
			backendA, backendB = backendB, backendA
		}
		if backendA != key.backendA || backendB != key.backendB {
			continue
		}
		if diff.Allowed {
			allowed++
		} else {
			blocking++
		}
	}
	return blocking, allowed
}

func validateReferenceDiffs(result CaseResult, reference string) error {
	exclusionEvidence := make(map[exclusionEvidenceKey]struct{})
	exclusionKinds := make(map[string]ExclusionKind)
	excludedBackends := make(map[string]struct{})
	for index, diff := range result.Diffs {
		if diff.BackendA != reference {
			return fmt.Errorf(
				"replaytest: reference case %q diff %d does not start with reference backend %q",
				result.Name,
				index,
				reference,
			)
		}
		if diff.Exclusion != nil {
			if err := validateExclusionKindConflict(exclusionKinds, diff.Exclusion.Backend, diff.Exclusion.Kind); err != nil {
				return fmt.Errorf("replaytest: reference case %q diff %d: %w", result.Name, index, err)
			}
			if diff.Exclusion.Backend != diff.BackendB && diff.Exclusion.Backend != reference {
				return fmt.Errorf("replaytest: reference case %q diff %d exclusion backend is not in diff", result.Name, index)
			}
			key := exclusionEvidenceKey{
				backend:    diff.Exclusion.Backend,
				kind:       diff.Exclusion.Kind,
				capability: diff.Exclusion.Capability,
			}
			if _, exists := exclusionEvidence[key]; exists {
				return fmt.Errorf("replaytest: reference case %q repeats exclusion evidence for backend %q", result.Name, diff.Exclusion.Backend)
			}
			exclusionEvidence[key] = struct{}{}
			excludedBackends[diff.Exclusion.Backend] = struct{}{}
		}
	}
	for index, diff := range result.Diffs {
		if diff.Exclusion != nil {
			continue
		}
		if diff.BackendB == reference {
			return fmt.Errorf("replaytest: reference case %q diff %d is an invalid self diff", result.Name, index)
		}
		if _, excluded := excludedBackends[diff.BackendA]; excluded {
			return fmt.Errorf(
				"replaytest: reference case %q diff %d uses excluded backend %q",
				result.Name,
				index,
				diff.BackendA,
			)
		}
		if _, excluded := excludedBackends[diff.BackendB]; excluded {
			return fmt.Errorf(
				"replaytest: reference case %q diff %d uses excluded backend %q",
				result.Name,
				index,
				diff.BackendB,
			)
		}
	}
	return nil
}

func validateExclusionKindConflict(kinds map[string]ExclusionKind, backend string, kind ExclusionKind) error {
	previous, exists := kinds[backend]
	if exists && previous != kind && (previous == ExclusionExecutionFailure || kind == ExclusionExecutionFailure) {
		return fmt.Errorf("backend %q has conflicting exclusion kinds %q and %q", backend, previous, kind)
	}
	kinds[backend] = kind
	return nil
}

func expectedCaseStatus(blocking int, hasCapabilityEvidence bool) CaseStatus {
	if blocking > 0 {
		return StatusFailed
	}
	if hasCapabilityEvidence {
		return StatusUnsupported
	}
	return StatusPassed
}

func validateReportTotals(r Report, totals reportTotals) error {
	if totals.passed != r.PassedCases || totals.failed != r.FailedCases || totals.unsupported != r.UnsupportedCases {
		return errors.New("replaytest: case status counters do not add up")
	}
	if totals.blocking != r.BlockingDiffs || totals.allowed != r.AllowedDiffs {
		return errors.New("replaytest: diff counters do not add up")
	}
	return nil
}
