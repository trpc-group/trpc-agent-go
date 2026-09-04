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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
)

func TestBuildFilterExprOperators(t *testing.T) {
	allowed := map[string]struct{}{"category": {}, "score": {}, "tags": {}, "price": {}}
	tests := []struct {
		name string
		cond *searchfilter.UniversalFilterCondition
		want string
	}{
		{"eq", searchfilter.Equal("category", "news"), "category = 'news'"},
		{"ne", searchfilter.NotEqual("category", "news"), "category != 'news'"},
		{"gt", searchfilter.GreaterThan("score", 10), "score > 10"},
		{"gte", searchfilter.GreaterThanOrEqual("score", 10), "score >= 10"},
		{"lt", searchfilter.LessThan("score", 10), "score < 10"},
		{"lte", searchfilter.LessThanOrEqual("score", 10), "score <= 10"},
		{"in", searchfilter.In("category", "a", "b"), "category IN ('a', 'b')"},
		{"not in", searchfilter.NotIn("category", "a", "b"), "category NOT IN ('a', 'b')"},
		{"like", searchfilter.Like("category", "ne%"), "category LIKE 'ne%'"},
		{"not like", searchfilter.NotLike("category", "ne%"), "category NOT LIKE 'ne%'"},
		{"between", searchfilter.Between("price", 1, 10), "price BETWEEN 1 AND 10"},
		{
			"and",
			searchfilter.And(searchfilter.Equal("category", "news"), searchfilter.GreaterThan("score", 10)),
			"(category = 'news') AND (score > 10)",
		},
		{
			"or",
			searchfilter.Or(searchfilter.Equal("category", "news"), searchfilter.Equal("category", "sports")),
			"(category = 'news') OR (category = 'sports')",
		},
		{
			"and single",
			searchfilter.And(searchfilter.Equal("category", "news")),
			"(category = 'news')",
		},
		{
			// A nested OR must stay grouped when it is the only subcondition,
			// otherwise an AND-appending caller would widen the row set.
			"and single nested or",
			searchfilter.And(searchfilter.Or(
				searchfilter.Equal("category", "news"),
				searchfilter.Equal("category", "sports"),
			)),
			"((category = 'news') OR (category = 'sports'))",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildFilterExpr(tt.cond, allowed)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildFilterExprErrors(t *testing.T) {
	allowed := map[string]struct{}{"category": {}}

	// Field not allowed.
	_, err := buildFilterExpr(searchfilter.Equal("notallowed", "x"), allowed)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFieldNotAllowed)

	// Invalid field name.
	_, err = buildFilterExpr(searchfilter.Equal("bad name", "x"), allowed)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFieldNameInvalid)

	// Empty IN array.
	_, err = buildFilterExpr(searchfilter.In("category"), allowed)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyValueArray)

	// Unknown operator.
	_, err = buildFilterExpr(&searchfilter.UniversalFilterCondition{Operator: "??", Field: "category"}, allowed)
	require.Error(t, err)

	// Between with a non-2-element array.
	_, err = buildFilterExpr(&searchfilter.UniversalFilterCondition{
		Operator: searchfilter.OperatorBetween, Field: "category", Value: []any{"x"},
	}, allowed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "2-element")

	// nil condition yields empty.
	got, err := buildFilterExpr(nil, allowed)
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestBuildFilterExprNilValue(t *testing.T) {
	allowed := map[string]struct{}{"category": {}}
	_, err := buildFilterExpr(searchfilter.Equal("category", nil), allowed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be nil")
}

func TestFormatLiteral(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{"hello", "'hello'"},
		{"o'brien", "'o''brien'"},
		{true, "true"},
		{false, "false"},
		{int(42), "42"},
		{int64(42), "42"},
		{uint64(42), "42"},
		{float64(3.14), "3.14"},
		{float32(1.5), "1.5"},
	}
	for _, tt := range tests {
		got, err := formatLiteral(tt.in)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}

	_, err := formatLiteral(struct{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported filter literal type")
}

func TestQuoteString(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "'hello'"},
		{"o'brien", "'o''brien'"},
		{"", "''"},
		// Backslash must be escaped first, otherwise it can escape a following
		// single quote and break out of the literal.
		{`a\b`, `'a\\b'`},
		{`a\'`, `'a\\'''`},
		// SQL injection attempt with adjacent backslash + quote is neutralized.
		{`a\' OR 1=1 --`, `'a\\'' OR 1=1 --'`},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, quoteString(tt.in))
	}
}

func TestMetadataMapToExpr(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	vs.option.filterFields = []FilterFieldSpec{{Name: "category", Type: FilterFieldString}}

	got, err := vs.metadataMapToExpr(map[string]any{"category": "news", "name": "doc"})
	require.NoError(t, err)
	assert.Contains(t, got, "category = 'news'")
	assert.Contains(t, got, "name = 'doc'")

	// Empty map.
	got, err = vs.metadataMapToExpr(nil)
	require.NoError(t, err)
	assert.Equal(t, "", got)

	// Not-allowed field.
	_, err = vs.metadataMapToExpr(map[string]any{"notallowed": "x"})
	require.Error(t, err)
}

func TestAllowedFilterFields(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	vs.option.filterFields = []FilterFieldSpec{{Name: "category", Type: FilterFieldString}}
	allowed := vs.allowedFilterFields()
	assert.Contains(t, allowed, "name")
	assert.Contains(t, allowed, "content")
	assert.Contains(t, allowed, "created_at")
	assert.Contains(t, allowed, "updated_at")
	assert.Contains(t, allowed, "category")
}

func TestBuildFilterFromSearch(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	vs.option.filterFields = []FilterFieldSpec{
		{Name: "category", Type: FilterFieldString},
		{Name: "score", Type: FilterFieldInt64},
	}

	// nil filter.
	got, err := vs.buildFilterFromSearch(nil)
	require.NoError(t, err)
	assert.Equal(t, "", got)

	// Metadata + condition combined with AND.
	f := &vectorstore.SearchFilter{
		Metadata:        map[string]any{"category": "news"},
		FilterCondition: searchfilter.GreaterThan("score", 10),
	}
	got, err = vs.buildFilterFromSearch(f)
	require.NoError(t, err)
	assert.Contains(t, got, "category = 'news'")
	assert.Contains(t, got, "score > 10")
}

func TestJoinAnd(t *testing.T) {
	assert.Equal(t, "", joinAnd())
	assert.Equal(t, "", joinAnd("", "  "))
	// A single clause keeps its parentheses so callers can AND-append safely.
	assert.Equal(t, "(a = 1)", joinAnd("a = 1"))
	assert.Equal(t, "(a = 1) AND (b = 2)", joinAnd("a = 1", "b = 2"))
	// A top-level OR must stay parenthesized, otherwise AND would bind tighter
	// and widen the matched row set.
	assert.Equal(t, "(a = 1 OR b = 2)", joinAnd("a = 1 OR b = 2"))
	assert.Equal(t,
		"(id IN ('x')) AND (a = 1 OR b = 2)",
		joinAnd("id IN ('x')", "a = 1 OR b = 2"),
	)
}
