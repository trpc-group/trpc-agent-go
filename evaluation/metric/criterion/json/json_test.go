//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapCriterionCompareOverride(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Compare: func(actual, expected any) (bool, error) {
			_, actualIsMap := actual.(map[string]any)
			_, expectedIsMap := expected.(map[string]any)
			if !actualIsMap || !expectedIsMap {
				return false, nil
			}
			called = true
			return true, nil
		},
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestMapCriterionDeepEqualMismatch(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestMapCriterionDeepEqualSuccess(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreSkipsCompare(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Ignore: true,
		Compare: func(actual, expected any) (bool, error) {
			called = true
			return false, nil
		},
		MatchStrategy: JSONMatchStrategyExact,
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "diff"})
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.False(t, called)
}

func TestJSONCriterionInvalidMatchStrategy(t *testing.T) {
	criterion := &JSONCriterion{
		MatchStrategy: JSONMatchStrategy("invalid"),
	}
	ok, err := criterion.Match(map[string]any{"k": "v"}, map[string]any{"k": "v"})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTree(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"time": true,
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"city": "Shanghai",
		"time": "2025-03-01T12:00:00Z",
		"meta": map[string]any{
			"time": "12:00",
			"id":   "ticket-1",
		},
	}
	expected := map[string]any{
		"city": "Shanghai",
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeMismatch(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"time": true,
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"city": "Beijing",
		"time": "2025-03-01T12:00:00Z",
		"meta": map[string]any{
			"time": "12:00",
			"id":   "ticket-1",
		},
	}
	expected := map[string]any{
		"city": "Shanghai",
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTreeNoIgnoreConfigured(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{},
	}
	actual := map[string]any{
		"city": "Shanghai",
	}
	expected := map[string]any{
		"city": "Shanghai",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNestedOnlyChildIgnored(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":   "1",
			"time": "12:00",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNestedMismatchOnNonIgnored(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":   "1",
			"time": "12:00",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "2",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTreeOrderIndependentKeys(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"time": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
		"time": "12:00",
	}
	expected := map[string]any{
		"time": "ignored",
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNestedMapsWithExtraKeys(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": map[string]any{
				"time": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":      "1",
			"time":    "12:00",
			"country": "CN",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionOnlyTreeTopLevel(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"city": true,
		},
	}
	actual := map[string]any{
		"city": "Shanghai",
		"time": "2025-03-01T12:00:00Z",
	}
	expected := map[string]any{
		"city":          "Shanghai",
		"unexpectedKey": "ignored",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeNested(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": map[string]any{
				"id": true,
			},
		},
	}
	actual := map[string]any{
		"city": "Beijing",
		"meta": map[string]any{
			"id":      "ticket-1",
			"time":    "12:00",
			"country": "CN",
		},
	}
	expected := map[string]any{
		"city": "Shanghai",
		"meta": map[string]any{
			"id":   "ticket-1",
			"time": "ignored",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeMismatchOnSelectedKey(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": map[string]any{
				"id": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "ticket-2",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionOnlyTreeMissingSelectedKey(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": map[string]any{
				"id": true,
			},
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	expected := map[string]any{}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "onlyTree")
}

func TestJSONCriterionOnlyTreeInvalidSelectorType(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": "true",
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "ticket-1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "onlyTree[meta]")
}

func TestJSONCriterionOnlyTreeAndIgnoreTreeConflict(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"skip": true,
		},
		OnlyTree: map[string]any{
			"keep": true,
		},
	}
	ok, err := criterion.Match(map[string]any{"keep": "v"}, map[string]any{"keep": "v"})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "onlyTree and ignoreTree")
}

func TestJSONCriterionOnlyTreeEmptyDoesNotConflictWithIgnoreTree(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"skip": true,
		},
		OnlyTree: map[string]any{},
	}
	actual := map[string]any{
		"keep": "v",
		"skip": "x",
	}
	expected := map[string]any{
		"keep": "v",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeEmptyDoesNotConflictWithOnlyTree(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{},
		OnlyTree: map[string]any{
			"keep": true,
		},
	}
	actual := map[string]any{
		"keep":  "v",
		"other": "x",
	}
	expected := map[string]any{
		"keep":  "v",
		"other": "y",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeScalarFallbackToExact(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"keep": true,
		},
	}
	ok, err := criterion.Match("x", "x")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeLeafTrueComparesSubtreeExact(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":    "1",
			"extra": "x",
		},
		"other": "ignored",
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id":    "1",
			"extra": "x",
		},
		"unexpected": "ignored",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeMissingKeyInExpected(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":    "1",
			"extra": "x",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key extra in actual but not in expected")
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeMissingKeyInActual(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id":    "1",
			"extra": "x",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key extra in expected but not in actual")
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeMismatch(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "2",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "compare meta")
}

func TestJSONCriterionOnlyTreeSkipsFalseSelector(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"skip": false,
			"keep": true,
		},
	}
	actual := map[string]any{
		"keep": "v",
		"skip": "x",
	}
	expected := map[string]any{
		"keep": "v",
		"skip": "y",
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeSelectedKeyMissingInActual(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"missing": true,
		},
	}
	actual := map[string]any{}
	expected := map[string]any{
		"missing": "v",
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key missing in onlyTree but not in actual")
}

func TestJSONCriterionOnlyTreeMapTypeMismatchActualMapExpectedNonMap(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"keep": true,
		},
	}
	ok, err := criterion.Match(map[string]any{"keep": "v"}, "x")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a map")
}

func TestJSONCriterionOnlyTreeMapTypeMismatchActualNonMapExpectedMap(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"keep": true,
		},
	}
	ok, err := criterion.Match("x", map[string]any{"keep": "v"})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a map")
}

func TestJSONCriterionOnlyTreeNilValues(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"keep": true,
		},
	}

	ok, err := criterion.Match(nil, nil)
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = criterion.Match(nil, "x")
	assert.False(t, ok)
	assert.Error(t, err)

	ok, err = criterion.Match("x", nil)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionOnlyTreeNestedSelectorMissingKeyInActual(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": map[string]any{
				"id": true,
			},
		},
	}
	actual := map[string]any{}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key meta in onlyTree but not in actual")
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeNilValues(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": nil,
	}
	expected := map[string]any{
		"meta": nil,
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeNilMismatch(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": nil,
	}
	expected := map[string]any{
		"meta": "x",
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeTypeMismatchActualMapExpectedScalar(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	expected := map[string]any{
		"meta": "x",
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a map")
}

func TestJSONCriterionOnlyTreeLeafTrueSubtreeTypeMismatchActualScalarExpectedMap(t *testing.T) {
	criterion := &JSONCriterion{
		OnlyTree: map[string]any{
			"meta": true,
		},
	}
	actual := map[string]any{
		"meta": "x",
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id": "1",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a map")
}

func TestJSONCriterionNumberToleranceDefault(t *testing.T) {
	criterion := &JSONCriterion{}
	actual := map[string]any{"value": 0.3000005}
	expected := map[string]any{"value": 0.3}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionNumberToleranceFail(t *testing.T) {
	criterion := &JSONCriterion{}
	actual := map[string]any{"value": 0.301}
	expected := map[string]any{"value": 0.3}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionNumberToleranceJSONNumber(t *testing.T) {
	criterion := &JSONCriterion{}
	actual := map[string]any{"value": json.Number("1.0000001")}
	expected := map[string]any{"value": 1}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionNumberToleranceWithIgnoreTree(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"skip": true,
		},
	}
	actual := map[string]any{
		"skip":  123.456,
		"value": 10.0000004,
	}
	expected := map[string]any{
		"skip":  200.0,
		"value": 10,
	}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionNumberToleranceExactZero(t *testing.T) {
	criterion := &JSONCriterion{
		NumberTolerance: floatPtr(0),
	}
	actual := map[string]any{"value": 1.0000001}
	expected := map[string]any{"value": 1.0}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionNumberToleranceWithOption(t *testing.T) {
	criterion := New(WithNumberTolerance(0.2))
	actual := map[string]any{"value": 1.1}
	expected := map[string]any{"value": 1.0}
	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionNumberToleranceNegative(t *testing.T) {
	criterion := &JSONCriterion{
		NumberTolerance: floatPtr(-0.1),
	}
	actual := map[string]any{"value": 1.0}
	expected := map[string]any{"value": 1.0}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestToFloatCoversNumericTypes(t *testing.T) {
	cases := []struct {
		name   string
		input  any
		expect float64
		ok     bool
	}{
		{"float32", float32(1.5), 1.5, true},
		{"float64", float64(2.5), 2.5, true},
		{"int", int(3), 3, true},
		{"int8", int8(4), 4, true},
		{"int16", int16(5), 5, true},
		{"int32", int32(6), 6, true},
		{"int64", int64(7), 7, true},
		{"uint", uint(8), 8, true},
		{"uint8", uint8(9), 9, true},
		{"uint16", uint16(10), 10, true},
		{"uint32", uint32(11), 11, true},
		{"uint64", uint64(12), 12, true},
		{"json.Number", json.Number("13.5"), 13.5, true},
		{"invalid json.Number", json.Number("not-number"), 0, false},
		{"unsupported type", "string", 0, false},
	}
	for _, tc := range cases {
		val, ok := toFloat(tc.input)
		if tc.ok {
			assert.True(t, ok, tc.name)
			assert.InDelta(t, tc.expect, val, 1e-9, tc.name)
		} else {
			assert.False(t, ok, tc.name)
		}
	}
}

func TestJSONCriterionMatchArrayNumberTolerance(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match([]any{0.3000005}, []any{0.3})
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionMatchArrayMismatch(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match([]any{float64(1)}, []any{float64(2)})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionMatchScalarSuccess(t *testing.T) {
	criterion := &JSONCriterion{}
	ok, err := criterion.Match("ok", "ok")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionMatchNilValues(t *testing.T) {
	criterion := &JSONCriterion{}

	ok, err := criterion.Match(nil, nil)
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = criterion.Match(nil, "x")
	assert.False(t, ok)
	assert.Error(t, err)

	ok, err = criterion.Match("x", nil)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionMatchMapTypeMismatch(t *testing.T) {
	criterion := &JSONCriterion{}

	ok, err := criterion.Match(map[string]any{"k": "v"}, "x")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a map")

	ok, err = criterion.Match("x", map[string]any{"k": "v"})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is a map")
}

func TestJSONCriterionMatchArrayTypeMismatch(t *testing.T) {
	criterion := &JSONCriterion{}

	ok, err := criterion.Match([]any{float64(1)}, "x")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is an array")

	ok, err = criterion.Match("x", []any{float64(1)})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is an array")
}

func TestJSONCriterionMatchArrayLengthMismatch(t *testing.T) {
	criterion := &JSONCriterion{}

	ok, err := criterion.Match([]any{float64(1)}, []any{float64(1), float64(2)})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "array length mismatch")
}

func TestJSONCriterionCompareOverrideScalar(t *testing.T) {
	called := false
	criterion := &JSONCriterion{
		Compare: func(actual, expected any) (bool, error) {
			called = true
			return actual == expected, nil
		},
	}

	ok, err := criterion.Match("a", "a")
	assert.True(t, ok)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestJSONCriterionCompareOverrideErrorPropagation(t *testing.T) {
	criterion := &JSONCriterion{
		Compare: func(actual, expected any) (bool, error) {
			return false, errors.New("boom")
		},
	}

	ok, err := criterion.Match("a", "a")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestJSONCriterionIgnoreTreeSkipsMissingKeysBothDirections(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"skipActual":   true,
			"skipExpected": true,
		},
	}

	actual := map[string]any{
		"keep":       "v",
		"skipActual": "x",
	}
	expected := map[string]any{
		"keep":         "v",
		"skipExpected": "y",
	}

	ok, err := criterion.Match(actual, expected)
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestJSONCriterionIgnoreTreeNonBoolDoesNotIgnore(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"skip": "true",
		},
	}

	ok, err := criterion.Match(map[string]any{"skip": "x"}, map[string]any{})
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionIgnoreTreeChildNonBoolDoesNotIgnoreNested(t *testing.T) {
	criterion := &JSONCriterion{
		IgnoreTree: map[string]any{
			"meta": "true",
		},
	}
	actual := map[string]any{
		"meta": map[string]any{
			"id":   "1",
			"time": "12:00",
		},
	}
	expected := map[string]any{
		"meta": map[string]any{
			"id":   "1",
			"time": "ignored",
		},
	}
	ok, err := criterion.Match(actual, expected)
	assert.False(t, ok)
	assert.Error(t, err)
}

func TestJSONCriterionExpectedKeyMissingInActual(t *testing.T) {
	criterion := &JSONCriterion{}

	ok, err := criterion.Match(map[string]any{"a": float64(1)}, map[string]any{"a": float64(1), "b": float64(2)})
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key b in expected but not in actual")
}

func floatPtr(v float64) *float64 {
	return &v
}
