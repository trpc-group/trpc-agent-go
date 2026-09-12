//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func TestToAnySlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []any
		ok   bool
	}{
		{"[]any", []any{"a", 1}, []any{"a", 1}, true},
		{"[]string", []string{"a", "b"}, []any{"a", "b"}, true},
		{"[]int", []int{1, 2}, []any{1, 2}, true},
		{"[]int64", []int64{1, 2}, []any{int64(1), int64(2)}, true},
		{"[]uint64", []uint64{1, 2}, []any{uint64(1), uint64(2)}, true},
		{"[]float64", []float64{1.5}, []any{1.5}, true},
		{"scalar wraps", "x", []any{"x"}, true},
		{"nil", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toAnySlice(tt.in)
			if !tt.ok {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToConditionSlice(t *testing.T) {
	c := searchfilter.Equal("f", 1)

	// []*UniversalFilterCondition.
	got, err := toConditionSlice([]*searchfilter.UniversalFilterCondition{c})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	// []UniversalFilterCondition (value).
	got, err = toConditionSlice([]searchfilter.UniversalFilterCondition{*c})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	// []any.
	got, err = toConditionSlice([]any{c})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	// []any with invalid element.
	_, err = toConditionSlice([]any{"bad"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be *UniversalFilterCondition")

	// invalid type.
	_, err = toConditionSlice("bad")
	require.Error(t, err)
}

func TestFormatLiteralAllTypes(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{false, "false"},
		{int8(1), "1"},
		{int16(2), "2"},
		{int32(3), "3"},
		{uint(4), "4"},
		{uint8(5), "5"},
		{uint16(6), "6"},
		{uint32(7), "7"},
		{float32(1.25), "1.25"},
	}
	for _, tt := range tests {
		got, err := formatLiteral(tt.in)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestFormatInEdgeCases(t *testing.T) {
	allowed := map[string]struct{}{"f": {}}

	// Scalar wraps to single element.
	got, err := formatIn("f", "IN", "x", allowed)
	require.NoError(t, err)
	assert.Equal(t, "f IN ('x')", got)

	// nil.
	_, err = formatIn("f", "IN", nil, allowed)
	require.Error(t, err)

	// Invalid literal.
	_, err = formatIn("f", "IN", []any{struct{}{}}, allowed)
	require.Error(t, err)
}

func TestFormatBetweenEdgeCases(t *testing.T) {
	allowed := map[string]struct{}{"f": {}}

	// Wrong element count.
	_, err := formatBetween("f", []any{1}, allowed)
	require.Error(t, err)
	_, err = formatBetween("f", []any{1, 2, 3}, allowed)
	require.Error(t, err)

	// Non-slice value.
	_, err = formatBetween("f", "x", allowed)
	require.Error(t, err)
}

func TestFormatLogicalEdgeCases(t *testing.T) {
	allowed := map[string]struct{}{"f": {}}

	// A single subcondition keeps its parentheses so an inner OR cannot escape
	// its scope when the result is AND-combined by a caller.
	got, err := formatLogical([]*searchfilter.UniversalFilterCondition{
		searchfilter.Equal("f", 1),
	}, "AND", allowed)
	require.NoError(t, err)
	assert.Equal(t, "(f = 1)", got)

	// Empty conditions.
	_, err = formatLogical([]*searchfilter.UniversalFilterCondition{}, "AND", allowed)
	require.Error(t, err)

	// Invalid value type.
	_, err = formatLogical("bad", "AND", allowed)
	require.Error(t, err)
}

func TestCombineWhere(t *testing.T) {
	assert.Equal(t, " WHERE a > 0", combineWhere("a > 0", ""))
	assert.Equal(t, " WHERE (a > 0) AND (b = 1)", combineWhere("a > 0", " WHERE b = 1"))
	// The existing clause may contain a top-level OR, which must stay grouped.
	assert.Equal(t,
		" WHERE (a > 0) AND (b = 1 OR c = 2)",
		combineWhere("a > 0", " WHERE b = 1 OR c = 2"),
	)
}

func TestToFloat64AllTypes(t *testing.T) {
	tests := []struct {
		in   any
		want float64
	}{
		{int(1), 1},
		{int8(2), 2},
		{int16(3), 3},
		{int32(4), 4},
		{int64(5), 5},
		{uint(6), 6},
		{uint8(7), 7},
		{uint16(8), 8},
		{uint32(9), 9},
		{uint64(10), 10},
		{float32(1.5), 1.5},
		{float64(2.5), 2.5},
		{json.Number("3.5"), 3.5},
	}
	for _, tt := range tests {
		got, err := toFloat64(tt.in)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
	_, err := toFloat64("bad")
	require.Error(t, err)
}

func TestMarshalMetadataError(t *testing.T) {
	_, err := marshalMetadata(map[string]any{"ch": make(chan int)})
	require.Error(t, err)
}

func TestScanMetadataRow(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
	))
	rows := newMockRows([][]any{{"doc1", `{"x":1,"category":"news"}`, "news"}})
	require.True(t, rows.Next())
	id, md, err := vs.scanMetadataRow(rows)
	require.NoError(t, err)
	assert.Equal(t, "doc1", id)
	assert.Equal(t, float64(1), md["x"])
	assert.Equal(t, "news", md["category"])
}
