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
	Compare func(actual, expected map[string]any) (bool, error) `json:"-"`
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

// Match compares two JSON objects using custom logic or deep equality with numeric tolerance.
func (j *JSONCriterion) Match(actual, expected map[string]any) (bool, error) {
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
		if err := compareTree(actual, expected, j.IgnoreTree, tolerance); err != nil {
			return false, fmt.Errorf("actual %v and expected %v do not match: %w", actual, expected, err)
		}
		return true, nil
	default:
		return false, fmt.Errorf("invalid match strategy %s", j.MatchStrategy)
	}
}

// compareTree compares two JSON objects using ignore tree and numeric tolerance.
func compareTree(actual, expected, ignoreTree map[string]any, tolerance float64) error {
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
		actualValue, ok := actual[k].(map[string]any)
		if !ok {
			if equalWithTolerance(actual[k], expected[k], tolerance) {
				continue
			}
			return fmt.Errorf("actual[%s] %v and expected[%s] %v do not match", k, actual[k], k, expected[k])
		}
		expectedValue, ok := expected[k].(map[string]any)
		if !ok {
			return fmt.Errorf("expected[%s] %v is not a map", k, expected[k])
		}
		ignoreTreeValue, ok := ignoreTree[k].(map[string]any)
		if !ok {
			ignoreTreeValue = nil
		}
		if err := compareTree(actualValue, expectedValue, ignoreTreeValue, tolerance); err != nil {
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
