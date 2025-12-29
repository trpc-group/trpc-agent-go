//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func TestFilterConverter_NilCondition(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()
	filter, err := converter.Convert(nil)
	assert.NoError(t, err)
	assert.Nil(t, filter)
}

func TestFilterConverter_EqualOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorEqual,
		Field:    "metadata.category",
		Value:    "documents",
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_NotEqualOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorNotEqual,
		Field:    "metadata.status",
		Value:    "deleted",
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_GreaterThanOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorGreaterThan,
		Field:    "metadata.score",
		Value:    50,
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_LessThanOrEqualOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorLessThanOrEqual,
		Field:    "metadata.price",
		Value:    100.50,
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorWithStrings(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.tags",
		Value:    []any{"golang", "python", "rust"},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorWithTypedStringSlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.tags",
		Value:    []string{"golang", "python", "rust"},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorWithInts(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.ids",
		Value:    []any{int64(1), int64(2), int64(3)},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorWithTypedIntSlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.ids",
		Value:    []int{1, 2, 3},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorWithTypedInt64Slice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.ids",
		Value:    []int64{1, 2, 3},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorEmptySlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	tests := []struct {
		name  string
		value any
	}{
		{"empty []string", []string{}},
		{"empty []int", []int{}},
		{"empty []int64", []int64{}},
		{"empty []any", []any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "metadata.field",
				Value:    tt.value,
			}

			filter, err := converter.Convert(cond)
			require.NoError(t, err)
			assert.Nil(t, filter)
		})
	}
}

func TestFilterConverter_NotInOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorNotIn,
		Field:    "metadata.status",
		Value:    []any{"archived", "deleted"},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_BetweenOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorBetween,
		Field:    "metadata.age",
		Value:    []any{18, 65},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_BetweenOperatorInvalidValue(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	// Not an array
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorBetween,
		Field:    "metadata.age",
		Value:    18,
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)

	// Array with wrong length
	cond2 := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorBetween,
		Field:    "metadata.age",
		Value:    []any{18},
	}

	_, err = converter.Convert(cond2)
	assert.Error(t, err)
}

func TestFilterConverter_LikeOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorLike,
		Field:    "metadata.description",
		Value:    "search term",
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_NotLikeOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorNotLike,
		Field:    "metadata.description",
		Value:    "excluded term",
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_AndOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value: []*searchfilter.UniversalFilterCondition{
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.category",
				Value:    "docs",
			},
			{
				Operator: searchfilter.OperatorGreaterThan,
				Field:    "metadata.version",
				Value:    1,
			},
		},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 2)
}

func TestFilterConverter_OrOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorOr,
		Value: []*searchfilter.UniversalFilterCondition{
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.lang",
				Value:    "en",
			},
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.lang",
				Value:    "fr",
			},
		},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Should, 2)
}

func TestFilterConverter_NestedLogicalOperators(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value: []*searchfilter.UniversalFilterCondition{
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.project",
				Value:    "test",
			},
			{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Operator: searchfilter.OperatorEqual,
						Field:    "metadata.status",
						Value:    "active",
					},
					{
						Operator: searchfilter.OperatorEqual,
						Field:    "metadata.status",
						Value:    "pending",
					},
				},
			},
		},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 2)
}

func TestFilterConverter_UnsupportedOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: "unknown_operator",
		Field:    "metadata.field",
		Value:    "value",
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported operator")
}

func TestFilterConverter_AndOperatorInvalidValue(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value:    "not an array",
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires array of conditions")
}

func TestFilterConverter_OrOperatorInvalidValue(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorOr,
		Value:    "not an array",
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires array of conditions")
}

func TestFilterConverter_MatchConditionTypes(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	tests := []struct {
		name  string
		value any
	}{
		{"string", "test"},
		{"int", 42},
		{"int32", int32(42)},
		{"int64", int64(42)},
		{"bool_true", true},
		{"bool_false", false},
		{"float32", float32(3.14)},
		{"float64", 3.14},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.field",
				Value:    tt.value,
			}

			filter, err := converter.Convert(cond)
			require.NoError(t, err)
			require.NotNil(t, filter)
		})
	}
}

// TestFilterConverter_FloatEqualityUsesRange verifies that float equality
// uses Range condition (gte=lte) instead of Match, since Qdrant's Match
// doesn't support float values.
func TestFilterConverter_FloatEqualityUsesRange(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	tests := []struct {
		name  string
		value any
	}{
		{"float32", float32(3.14)},
		{"float64", float64(3.14)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.price",
				Value:    tt.value,
			}

			filter, err := converter.Convert(cond)
			require.NoError(t, err)
			require.NotNil(t, filter)
			require.Len(t, filter.Must, 1)

			// Verify it uses Range condition, not Match
			fieldCond := filter.Must[0].GetField()
			require.NotNil(t, fieldCond, "expected Field condition")
			require.NotNil(t, fieldCond.Range, "expected Range condition for float equality")
			require.Nil(t, fieldCond.Match, "expected no Match condition for float")

			// Verify gte == lte (equality via range)
			require.NotNil(t, fieldCond.Range.Gte)
			require.NotNil(t, fieldCond.Range.Lte)
			assert.Equal(t, *fieldCond.Range.Gte, *fieldCond.Range.Lte)
		})
	}
}

func TestFilterConverter_ResolveField(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	tests := []struct {
		input    string
		expected string
	}{
		{"metadata.category", "metadata.category"},
		{"metadata.nested.field", "metadata.nested.field"},
		{"name", "name"},
		{"content", "content"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := converter.resolveField(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToFloat64Ptr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    any
		expected *float64
	}{
		{"nil", nil, nil},
		{"float64", float64(3.14), ptrFloat64(3.14)},
		{"float32", float32(3.14), ptrFloat64(float64(float32(3.14)))},
		{"int", 42, ptrFloat64(42)},
		{"int32", int32(42), ptrFloat64(42)},
		{"int64", int64(42), ptrFloat64(42)},
		{"uint", uint(42), ptrFloat64(42)},
		{"uint32", uint32(42), ptrFloat64(42)},
		{"uint64", uint64(42), ptrFloat64(42)},
		{"string", "invalid", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toFloat64Ptr(tt.input)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.InDelta(t, *tt.expected, *result, 0.001)
			}
		})
	}
}

func ptrFloat64(f float64) *float64 {
	return &f
}

func TestFilterConverter_LessThanOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorLessThan,
		Field:    "metadata.count",
		Value:    10,
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_GreaterThanOrEqualOperator(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorGreaterThanOrEqual,
		Field:    "metadata.rating",
		Value:    4.0,
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Len(t, filter.Must, 1)
}

func TestFilterConverter_InOperatorMixedTypes(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	// Mixed types should fallback to OR filter
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.mixed",
		Value:    []any{true, false},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
}

func TestFilterConverter_InOperatorInt32Slice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.ids",
		Value:    []any{int32(1), int32(2), int32(3)},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
}

func TestFilterConverter_InOperatorReflection(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	// Test with custom slice type that requires reflection
	type CustomInt int
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.custom",
		Value:    []CustomInt{1, 2, 3},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
}

func TestFilterConverter_InOperatorNonSlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.field",
		Value:    "not a slice",
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires array value")
}

func TestFilterConverter_NilValue(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorEqual,
		Field:    "metadata.field",
		Value:    nil,
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	assert.Nil(t, filter)
}

func TestFilterConverter_NestedLogicalWithError(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	// Nested AND with an invalid operator should propagate error
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value: []*searchfilter.UniversalFilterCondition{
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.category",
				Value:    "docs",
			},
			{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Operator: "invalid_operator",
						Field:    "metadata.status",
						Value:    "active",
					},
				},
			},
		},
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)
}

func TestFilterConverter_NestedOrWithError(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	// Nested OR with an invalid subcondition
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorOr,
		Value: []*searchfilter.UniversalFilterCondition{
			{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Operator: searchfilter.OperatorBetween,
						Field:    "metadata.age",
						Value:    "invalid", // Should be array with 2 elements
					},
				},
			},
		},
	}

	_, err := converter.Convert(cond)
	assert.Error(t, err)
}

func TestFilterConverter_InOperatorFloat64Slice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.scores",
		Value:    []any{float64(1.5), float64(2.5), float64(3.5)},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
}

func TestFilterConverter_InOperatorEmptyReflectSlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	type CustomType struct{ Value int }
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorIn,
		Field:    "metadata.custom",
		Value:    []CustomType{},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	assert.Nil(t, filter)
}

func TestFilterConverter_NotInOperatorWithTypedSlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorNotIn,
		Field:    "metadata.tags",
		Value:    []string{"excluded1", "excluded2"},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
}

func TestFilterConverter_AndWithNilConditionResult(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	// AND with a nil value condition should skip it
	cond := &searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorAnd,
		Value: []*searchfilter.UniversalFilterCondition{
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.category",
				Value:    "docs",
			},
			{
				Operator: searchfilter.OperatorEqual,
				Field:    "metadata.field",
				Value:    nil, // This results in nil condition
			},
		},
	}

	filter, err := converter.Convert(cond)
	require.NoError(t, err)
	require.NotNil(t, filter)
	// Only the non-nil condition should be in the result
	assert.Len(t, filter.Must, 1)
}

func TestFilterConverter_NotInOperatorEmptySlice(t *testing.T) {
	t.Parallel()
	converter := newFilterConverter()

	tests := []struct {
		name  string
		value any
	}{
		{"empty []string", []string{}},
		{"empty []int", []int{}},
		{"empty []int64", []int64{}},
		{"empty []any", []any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond := &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorNotIn,
				Field:    "metadata.field",
				Value:    tt.value,
			}

			filter, err := converter.Convert(cond)
			require.NoError(t, err)
			// NotIn with empty slice should return nil (no filter needed)
			assert.Nil(t, filter, "NotIn with empty slice should return nil")
		})
	}
}
