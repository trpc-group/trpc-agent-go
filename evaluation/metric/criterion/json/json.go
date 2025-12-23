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
	"fmt"
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
	// Compare overrides default comparison when provided.
	Compare func(actual, expected map[string]any) (bool, error) `json:"-"`
}

// JSONMatchStrategy enumerates supported JSON comparison strategies.
type JSONMatchStrategy string

const (
	// JSONMatchStrategyExact matches json objects exactly.
	JSONMatchStrategyExact JSONMatchStrategy = "exact"
)

// Match compares two JSON objects using custom logic or deep equality.
func (j *JSONCriterion) Match(actual, expected map[string]any) (bool, error) {
	if j.Ignore {
		return true, nil
	}
	if j.Compare != nil {
		return j.Compare(actual, expected)
	}
	switch j.MatchStrategy {
	// Default to exact match.
	case JSONMatchStrategyExact, "":
		if len(j.IgnoreTree) > 0 {
			if err := compareWithIgnoreTree(actual, expected, j.IgnoreTree); err != nil {
				return false, fmt.Errorf("actual %v and expected %v do not match: %w", actual, expected, err)
			}
			return true, nil
		}
		if reflect.DeepEqual(actual, expected) {
			return true, nil
		}
		return false, fmt.Errorf("actual %v and expected %v do not match", actual, expected)
	default:
		return false, fmt.Errorf("invalid match strategy %s", j.MatchStrategy)
	}
}

// compareWithIgnoreTree compares two JSON objects using ignore tree.
func compareWithIgnoreTree(actual, expected, ignoreTree map[string]any) error {
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
			if reflect.DeepEqual(actual[k], expected[k]) {
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
			if reflect.DeepEqual(actual[k], expected[k]) {
				continue
			}
			return fmt.Errorf("actual[%s] %v and expected[%s] %v do not match", k, actual[k], k, expected[k])
		}
		if err := compareWithIgnoreTree(actualValue, expectedValue, ignoreTreeValue); err != nil {
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
