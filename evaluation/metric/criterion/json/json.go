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
		if reflect.DeepEqual(actual, expected) {
			return true, nil
		}
		return false, fmt.Errorf("actual %v and expected %v do not match", actual, expected)
	default:
		return false, fmt.Errorf("invalid match strategy %s", j.MatchStrategy)
	}
}
