//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package maptext defines map-based comparison criteria.
package maptext

import (
	"encoding/json"
	"fmt"
	"reflect"

	itext "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/internal/text"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/maptext"
)

// Match compares two maps using custom logic, text-based matching, or deep equality.
func Match(m *maptext.MapTextCriterion, actual, expected map[string]any) error {
	if m.Compare != nil {
		return m.Compare(actual, expected)
	}
	if m.TextCriterion != nil {
		// Although the keys in a map are unordered, json.Marshal guarantees the order of the keys,
		// so we can directly use json.Marshal for comparison.
		actualData, err := json.Marshal(actual)
		if err != nil {
			return fmt.Errorf("marshal actual: %w", err)
		}
		expectedData, err := json.Marshal(expected)
		if err != nil {
			return fmt.Errorf("marshal expected: %w", err)
		}
		return itext.Match(m.TextCriterion, string(actualData), string(expectedData))
	}
	if reflect.DeepEqual(actual, expected) {
		return nil
	}
	return fmt.Errorf("actual %v and expected %v do not match", actual, expected)
}
