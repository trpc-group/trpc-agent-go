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
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

func TestDocToRow(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	doc := &document.Document{
		ID:       "doc1",
		Name:     "Doc",
		Content:  "hello",
		Metadata: map[string]any{"category": "news"},
	}
	r, err := vs.docToRow(doc, []float64{1, 2, 3}, now)
	require.NoError(t, err)
	assert.Equal(t, "doc1", r.id)
	assert.Equal(t, "Doc", r.name)
	assert.Equal(t, "hello", r.content)
	assert.Equal(t, []float64{1, 2, 3}, r.embedding)
	assert.Equal(t, "news", r.metadata["category"])
	assert.Equal(t, now, r.createdAt)
	assert.Equal(t, now, r.updatedAt)
}

func TestDocToRowErrors(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	_, err := vs.docToRow(nil, nil, time.Now())
	require.ErrorIs(t, err, errDocumentRequired)

	_, err = vs.docToRow(&document.Document{}, nil, time.Now())
	require.ErrorIs(t, err, errDocumentIDRequired)
}

func TestRowToDoc(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	r := &row{
		id:        "doc1",
		name:      "Doc",
		content:   "hello",
		embedding: []float64{1, 2},
		metadata:  map[string]any{"k": "v"},
		createdAt: now,
		updatedAt: now,
	}
	doc, emb, err := vs.rowToDoc(r)
	require.NoError(t, err)
	assert.Equal(t, "doc1", doc.ID)
	assert.Equal(t, "Doc", doc.Name)
	assert.Equal(t, "hello", doc.Content)
	assert.Equal(t, "v", doc.Metadata["k"])
	assert.Equal(t, []float64{1, 2}, emb)

	_, _, err = vs.rowToDoc(nil)
	require.Error(t, err)
}

func TestMarshalUnmarshalMetadata(t *testing.T) {
	// nil.
	s, err := marshalMetadata(nil)
	require.NoError(t, err)
	assert.Equal(t, "{}", s)

	// empty map.
	s, err = marshalMetadata(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "{}", s)

	// populated.
	s, err = marshalMetadata(map[string]any{"a": 1, "b": "x"})
	require.NoError(t, err)
	m, err := unmarshalMetadata(s)
	require.NoError(t, err)
	assert.Equal(t, float64(1), m["a"])
	assert.Equal(t, "x", m["b"])

	// empty string.
	m, err = unmarshalMetadata("")
	require.NoError(t, err)
	assert.Empty(t, m)

	// invalid JSON.
	_, err = unmarshalMetadata("{invalid")
	require.Error(t, err)
}

func TestFilterFieldValues(t *testing.T) {
	vs := &VectorStore{option: defaultOptions}
	vs.option.filterFields = []FilterFieldSpec{
		{Name: "category", Type: FilterFieldString},
		{Name: "count", Type: FilterFieldInt64},
		{Name: "ratio", Type: FilterFieldFloat64},
	}
	vals, err := vs.filterFieldValues(map[string]any{
		"category": "news",
		"count":    10,
		"ratio":    0.5,
	})
	require.NoError(t, err)
	assert.Equal(t, "news", vals[0])
	assert.Equal(t, int64(10), vals[1])
	assert.Equal(t, float64(0.5), vals[2])

	// Missing values map to zero values.
	vals, err = vs.filterFieldValues(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "", vals[0])
	assert.Equal(t, int64(0), vals[1])
	assert.Equal(t, float64(0), vals[2])

	// No filter fields.
	vs2 := &VectorStore{option: defaultOptions}
	vals, err = vs2.filterFieldValues(map[string]any{"x": 1})
	require.NoError(t, err)
	assert.Nil(t, vals)
}

func TestConvertFilterFieldValue(t *testing.T) {
	// String.
	v, err := convertFilterFieldValue(FilterFieldString, "x")
	require.NoError(t, err)
	assert.Equal(t, "x", v)
	v, err = convertFilterFieldValue(FilterFieldString, nil)
	require.NoError(t, err)
	assert.Equal(t, "", v)
	_, err = convertFilterFieldValue(FilterFieldString, 123)
	require.Error(t, err)

	// Int64.
	v, err = convertFilterFieldValue(FilterFieldInt64, int64(5))
	require.NoError(t, err)
	assert.Equal(t, int64(5), v)
	v, err = convertFilterFieldValue(FilterFieldInt64, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), v)

	// Float64.
	v, err = convertFilterFieldValue(FilterFieldFloat64, float64(1.5))
	require.NoError(t, err)
	assert.Equal(t, float64(1.5), v)

	// Unknown type.
	_, err = convertFilterFieldValue(FilterFieldType(99), nil)
	require.Error(t, err)
}

func TestNumericConversions(t *testing.T) {
	tests := []struct {
		in   any
		want int64
		ok   bool
	}{
		{int(1), 1, true},
		{int8(2), 2, true},
		{int16(3), 3, true},
		{int32(4), 4, true},
		{int64(5), 5, true},
		{uint(6), 6, true},
		{uint8(7), 7, true},
		{uint16(8), 8, true},
		{uint32(9), 9, true},
		{uint64(10), 10, true},
		{float32(11), 11, true},
		{float64(12), 12, true},
		{json.Number("13"), 13, true},
		{"bad", 0, false},
	}
	for _, tt := range tests {
		got, err := toInt64(tt.in)
		if !tt.ok {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}

	// overflow and non-integer floats.
	_, err := toInt64(uint64(1) << 63)
	require.Error(t, err)
	_, err = toInt64(3.14)
	require.Error(t, err)
	_, err = toInt64(math.NaN())
	require.Error(t, err)
	_, err = toInt64(math.Inf(1))
	require.Error(t, err)
	// 2^63 is not representable as int64. Comparing against math.MaxInt64 would
	// let it through, because that constant rounds up to 2^63 in float64.
	_, err = toInt64(float64(1) * (1 << 63))
	require.Error(t, err)
	_, err = toInt64(-float64(1) * (1 << 63) * 1.5)
	require.Error(t, err)
	// -2^63 is exactly representable and must be accepted.
	got, err := toInt64(-float64(1) * (1 << 63))
	require.NoError(t, err)
	assert.Equal(t, int64(math.MinInt64), got)

	// toFloat64.
	f, err := toFloat64(int64(3))
	require.NoError(t, err)
	assert.Equal(t, float64(3), f)
	_, err = toFloat64("bad")
	require.Error(t, err)
}
