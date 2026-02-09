//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package json defines json-based comparison criteria.
package json

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

// JSONCriterion compares two JSON objects using exact matching.
type JSONCriterion struct {
	// Ignore skips comparison when true.
	Ignore bool `json:"ignore,omitempty"`
	// IgnoreTree skips nested keys using a structured tree; true leaf ignores the key and its subtree.
	IgnoreTree map[string]any `json:"ignoreTree,omitempty"`
	// OnlyTree compares only selected nested keys; true leaf compares the key and its subtree.
	OnlyTree map[string]any `json:"onlyTree,omitempty"`
	// MatchStrategy selects the comparison rule.
	MatchStrategy JSONMatchStrategy `json:"matchStrategy,omitempty"`
	// NumberTolerance defines the allowed absolute difference between numeric values. 1e-6 is the default.
	NumberTolerance *float64 `json:"numberTolerance,omitempty"`
	// Compare overrides default comparison when provided.
	Compare func(actual, expected any) (bool, error) `json:"-"`
}

// JSONMatchStrategy enumerates supported JSON comparison strategies.
type JSONMatchStrategy string

const (
	// JSONMatchStrategyExact matches json objects exactly.
	JSONMatchStrategyExact JSONMatchStrategy = "exact"
)

// New creates a new JSONCriterion with the provided options.
func New(opt ...Option) *JSONCriterion {
	opts := newOptions(opt...)
	return &JSONCriterion{
		Ignore:          opts.ignore,
		IgnoreTree:      opts.ignoreTree,
		OnlyTree:        opts.onlyTree,
		MatchStrategy:   opts.matchStrategy,
		NumberTolerance: opts.numberTolerance,
		Compare:         opts.compare,
	}
}

// Match compares two JSON values using custom logic or deep equality with numeric tolerance.
func (j *JSONCriterion) Match(actual, expected any) (bool, error) {
	if j.Ignore {
		return true, nil
	}
	if len(j.OnlyTree) > 0 && len(j.IgnoreTree) > 0 {
		return false, fmt.Errorf("onlyTree and ignoreTree cannot be set at the same time")
	}
	if j.Compare != nil {
		return j.Compare(actual, expected)
	}
	tolerance := defaultNumberTolerance
	if j.NumberTolerance != nil {
		tolerance = *j.NumberTolerance
	}
	switch j.MatchStrategy {
	case JSONMatchStrategyExact, "":
		if len(j.OnlyTree) > 0 {
			return matchValueOnlyTree(actual, expected, j.OnlyTree, tolerance)
		}
		return matchValueIgnoreTree(actual, expected, j.IgnoreTree, tolerance)
	default:
		return false, fmt.Errorf("invalid match strategy %s", j.MatchStrategy)
	}
}

func matchValueOnlyTree(actual, expected any, onlyTree map[string]any, tolerance float64) (bool, error) {
	if err := compareValueOnlyTree(actual, expected, onlyTree, tolerance); err != nil {
		return false, fmt.Errorf("actual %v and expected %v do not match: %w", actual, expected, err)
	}
	return true, nil
}

func compareValueOnlyTree(actual, expected any, onlyTree map[string]any, tolerance float64) error {
	if actual == nil && expected == nil {
		return nil
	}
	if actual == nil || expected == nil {
		return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
	}
	if actualMap, ok := actual.(map[string]any); ok {
		expectedMap, ok := expected.(map[string]any)
		if !ok {
			return fmt.Errorf("actual %v is a map but expected %v is not a map", actual, expected)
		}
		return compareObjectOnlyTree(actualMap, expectedMap, onlyTree, tolerance)
	}
	if _, ok := expected.(map[string]any); ok {
		return fmt.Errorf("actual %v is not a map but expected %v is a map", actual, expected)
	}
	return compareValueExact(actual, expected, tolerance)
}

func compareObjectOnlyTree(actual, expected, onlyTree map[string]any, tolerance float64) error {
	for k, v := range onlyTree {
		switch sel := v.(type) {
		case bool:
			if !sel {
				continue
			}
			actualValue, ok := actual[k]
			if !ok {
				return fmt.Errorf("key %s in onlyTree but not in actual", k)
			}
			expectedValue, ok := expected[k]
			if !ok {
				return fmt.Errorf("key %s in onlyTree but not in expected", k)
			}
			if err := compareValueExact(actualValue, expectedValue, tolerance); err != nil {
				return fmt.Errorf("compare %s: %w", k, err)
			}
		case map[string]any:
			actualValue, ok := actual[k]
			if !ok {
				return fmt.Errorf("key %s in onlyTree but not in actual", k)
			}
			expectedValue, ok := expected[k]
			if !ok {
				return fmt.Errorf("key %s in onlyTree but not in expected", k)
			}
			if err := compareValueOnlyTree(actualValue, expectedValue, sel, tolerance); err != nil {
				return fmt.Errorf("compare %s: %w", k, err)
			}
		default:
			return fmt.Errorf("onlyTree[%s] must be a bool or an object", k)
		}
	}
	return nil
}

func matchValueIgnoreTree(actual, expected any, ignoreTree map[string]any, tolerance float64) (bool, error) {
	if err := compareValueIgnoreTree(actual, expected, ignoreTree, tolerance); err != nil {
		return false, fmt.Errorf("actual %v and expected %v do not match: %w", actual, expected, err)
	}
	return true, nil
}

func compareValueIgnoreTree(actual, expected any, ignoreTree map[string]any, tolerance float64) error {
	if actual == nil && expected == nil {
		return nil
	}
	if actual == nil || expected == nil {
		return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
	}
	if actualMap, ok := actual.(map[string]any); ok {
		expectedMap, ok := expected.(map[string]any)
		if !ok {
			return fmt.Errorf("actual %v is a map but expected %v is not a map", actual, expected)
		}
		return compareObjectIgnoreTree(actualMap, expectedMap, ignoreTree, tolerance)
	}
	if _, ok := expected.(map[string]any); ok {
		return fmt.Errorf("actual %v is not a map but expected %v is a map", actual, expected)
	}
	return compareValueExact(actual, expected, tolerance)
}

func compareObjectIgnoreTree(actual, expected, ignoreTree map[string]any, tolerance float64) error {
	for k := range actual {
		if isIgnore(ignoreTree, k) {
			continue
		}
		if _, ok := expected[k]; !ok {
			return fmt.Errorf("key %s in actual but not in expected", k)
		}
	}
	for k := range expected {
		if isIgnore(ignoreTree, k) {
			continue
		}
		if _, ok := actual[k]; !ok {
			return fmt.Errorf("key %s in expected but not in actual", k)
		}
	}
	for k := range actual {
		if isIgnore(ignoreTree, k) {
			continue
		}
		childIgnoreTree, ok := ignoreTree[k].(map[string]any)
		if !ok {
			childIgnoreTree = nil
		}
		if err := compareValueIgnoreTree(actual[k], expected[k], childIgnoreTree, tolerance); err != nil {
			return fmt.Errorf("compare %s: %w", k, err)
		}
	}
	return nil
}

// isIgnore checks if a key is in the ignore tree.
func isIgnore(ignoreTree map[string]any, key string) bool {
	if ignoreTree == nil {
		return false
	}
	v, ok := ignoreTree[key]
	if !ok {
		return false
	}
	ignore, ok := v.(bool)
	if !ok {
		return false
	}
	return ignore
}

func compareValueExact(actual, expected any, tolerance float64) error {
	if actual == nil && expected == nil {
		return nil
	}
	if actual == nil || expected == nil {
		return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
	}
	if actualMap, ok := actual.(map[string]any); ok {
		expectedMap, ok := expected.(map[string]any)
		if !ok {
			return fmt.Errorf("actual %v is a map but expected %v is not a map", actual, expected)
		}
		return compareObjectExact(actualMap, expectedMap, tolerance)
	}
	if _, ok := expected.(map[string]any); ok {
		return fmt.Errorf("actual %v is not a map but expected %v is a map", actual, expected)
	}
	if actualList, ok := actual.([]any); ok {
		expectedList, ok := expected.([]any)
		if !ok {
			return fmt.Errorf("actual %v is an array but expected %v is not an array", actual, expected)
		}
		return compareArrayExact(actualList, expectedList, tolerance)
	}
	if _, ok := expected.([]any); ok {
		return fmt.Errorf("actual %v is not an array but expected %v is an array", actual, expected)
	}
	if equalWithTolerance(actual, expected, tolerance) {
		return nil
	}
	return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
}

func compareObjectExact(actual, expected map[string]any, tolerance float64) error {
	for k := range actual {
		if _, ok := expected[k]; !ok {
			return fmt.Errorf("key %s in actual but not in expected", k)
		}
	}
	for k := range expected {
		if _, ok := actual[k]; !ok {
			return fmt.Errorf("key %s in expected but not in actual", k)
		}
	}
	for k := range actual {
		if err := compareValueExact(actual[k], expected[k], tolerance); err != nil {
			return fmt.Errorf("compare %s: %w", k, err)
		}
	}
	return nil
}

func compareArrayExact(actual, expected []any, tolerance float64) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("array length mismatch: actual(%d) != expected(%d)", len(actual), len(expected))
	}
	for i := range actual {
		if err := compareValueExact(actual[i], expected[i], tolerance); err != nil {
			return fmt.Errorf("compare index %d: %w", i, err)
		}
	}
	return nil
}

// equalWithTolerance compares two values and applies numeric tolerance when both are numbers.
func equalWithTolerance(actual, expected any, tolerance float64) bool {
	actualFloat, actualIsFloat := toFloat(actual)
	expectedFloat, expectedIsFloat := toFloat(expected)
	if actualIsFloat && expectedIsFloat {
		return math.Abs(actualFloat-expectedFloat) <= tolerance
	}
	return reflect.DeepEqual(actual, expected)
}

// toFloat converts supported numeric types to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
