//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package length

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLengthCriterionJSONRoundTrip(t *testing.T) {
	criterion := New(WithIgnore(true), WithMin(1), WithMax(3))
	data, err := json.Marshal(criterion)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"ignore":true,"min":1,"max":3}`, string(data))

	var decoded LengthCriterion
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.True(t, decoded.Ignore)
	if assert.NotNil(t, decoded.Min) {
		assert.Equal(t, 1, *decoded.Min)
	}
	if assert.NotNil(t, decoded.Max) {
		assert.Equal(t, 3, *decoded.Max)
	}
}

func TestLengthCriterionMatch(t *testing.T) {
	min := 2
	max := 4
	criterion := &LengthCriterion{Min: &min, Max: &max}

	ok, err := criterion.Match("你好")
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = criterion.Match("abcd")
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = criterion.Match("a")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "less than min 2")
	assert.Contains(t, err.Error(), "expected range [2, 4]")

	ok, err = criterion.Match("abcde")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "greater than max 4")
	assert.Contains(t, err.Error(), "expected range [2, 4]")
}

func TestLengthCriterionSingleBound(t *testing.T) {
	min := 1
	max := 2

	ok, err := (&LengthCriterion{Min: &min}).Match("a")
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = (&LengthCriterion{Max: &max}).Match("ab")
	assert.True(t, ok)
	assert.NoError(t, err)
}

func TestLengthCriterionSingleBoundMismatchReportsRange(t *testing.T) {
	min := 2
	max := 1

	ok, err := (&LengthCriterion{Min: &min}).Match("a")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "less than min 2")
	assert.Contains(t, err.Error(), "expected range [2, +inf)")

	ok, err = (&LengthCriterion{Max: &max}).Match("ab")
	assert.False(t, ok)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "greater than max 1")
	assert.Contains(t, err.Error(), "expected range [0, 1]")
}

func TestLengthCriterionInvalidConfig(t *testing.T) {
	min := 3
	max := 2
	negative := -1

	cases := []struct {
		name      string
		criterion *LengthCriterion
	}{
		{name: "empty", criterion: &LengthCriterion{}},
		{name: "negative min", criterion: &LengthCriterion{Min: &negative}},
		{name: "negative max", criterion: &LengthCriterion{Max: &negative}},
		{name: "min greater than max", criterion: &LengthCriterion{Min: &min, Max: &max}},
	}

	for _, tc := range cases {
		ok, err := tc.criterion.Match("abc")
		assert.False(t, ok, tc.name)
		assert.Error(t, err, tc.name)
	}
}

func TestLengthCriterionIgnore(t *testing.T) {
	ok, err := (&LengthCriterion{Ignore: true}).Match("")
	assert.True(t, ok)
	assert.NoError(t, err)

	ok, err = (*LengthCriterion)(nil).Match("")
	assert.True(t, ok)
	assert.NoError(t, err)
}
