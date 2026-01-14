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
	if j.Compare != nil {
		return j.Compare(actual, expected)
	}
	tolerance := defaultNumberTolerance
	if j.NumberTolerance != nil {
		tolerance = *j.NumberTolerance
	}
	switch j.MatchStrategy {
	case JSONMatchStrategyExact, "":
		return matchValue(actual, expected, j.IgnoreTree, tolerance)
	default:
		return false, fmt.Errorf("invalid match strategy %s", j.MatchStrategy)
	}
}

func matchValue(actual, expected any, ignoreTree map[string]any, tolerance float64) (bool, error) {
	if err := compareValue(actual, expected, ignoreTree, tolerance); err != nil {
		return false, fmt.Errorf("actual %v and expected %v do not match: %w", actual, expected, err)
	}
	return true, nil
}

func compareValue(actual, expected any, ignoreTree map[string]any, tolerance float64) error {
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
		return compareObject(actualMap, expectedMap, ignoreTree, tolerance)
	}
	if _, ok := expected.(map[string]any); ok {
		return fmt.Errorf("actual %v is not a map but expected %v is a map", actual, expected)
	}
	if actualList, ok := actual.([]any); ok {
		expectedList, ok := expected.([]any)
		if !ok {
			return fmt.Errorf("actual %v is an array but expected %v is not an array", actual, expected)
		}
		return compareArray(actualList, expectedList, tolerance)
	}
	if _, ok := expected.([]any); ok {
		return fmt.Errorf("actual %v is not an array but expected %v is an array", actual, expected)
	}
	if equalWithTolerance(actual, expected, tolerance) {
		return nil
	}
	return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
}

func compareObject(actual, expected, ignoreTree map[string]any, tolerance float64) error {
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
		if err := compareValue(actual[k], expected[k], childIgnoreTree, tolerance); err != nil {
			return fmt.Errorf("compare %s: %w", k, err)
		}
	}
	return nil
}

func compareArray(actual, expected []any, tolerance float64) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("array length mismatch: actual(%d) != expected(%d)", len(actual), len(expected))
	}
	for i := range actual {
		if err := compareValue(actual[i], expected[i], nil, tolerance); err != nil {
			return fmt.Errorf("compare index %d: %w", i, err)
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
