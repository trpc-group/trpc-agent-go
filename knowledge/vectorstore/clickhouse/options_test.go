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
)

func TestValidateOptionsOK(t *testing.T) {
	o := defaultOptions
	o.tableName = "docs"
	require.NoError(t, validateOptions(&o))
}

func TestValidateOptionsErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*options)
		wantErr string
	}{
		{"empty table name", func(o *options) { o.tableName = "" }, "table name is required"},
		{"invalid table name", func(o *options) { o.tableName = "bad name!" }, "not a valid identifier"},
		{"zero dimension", func(o *options) { o.tableName = "t"; o.vectorDimension = 0 }, "vectorDimension must be > 0"},
		{"zero max results", func(o *options) { o.tableName = "t"; o.maxResults = 0 }, "maxResults must be > 0"},
		{"empty id field", func(o *options) { o.tableName = "t"; o.idFieldName = "" }, "id field name must not be empty"},
		{"invalid id field", func(o *options) { o.tableName = "t"; o.idFieldName = "1bad" }, "not a valid identifier"},
		{"duplicate builtin field", func(o *options) { o.tableName = "t"; o.nameFieldName = "id" }, "must differ"},
		{"empty filter field", func(o *options) {
			o.tableName = "t"
			o.filterFields = []FilterFieldSpec{{Name: "", Type: FilterFieldString}}
		}, "Name must not be empty"},
		{"filter field conflicts with builtin", func(o *options) {
			o.tableName = "t"
			o.filterFields = []FilterFieldSpec{{Name: "id", Type: FilterFieldString}}
		}, "conflicts with"},
		{"unsupported filter type", func(o *options) {
			o.tableName = "t"
			o.filterFields = []FilterFieldSpec{{Name: "f", Type: FilterFieldType(99)}}
		}, "not a supported FilterFieldType"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := defaultOptions
			o.tableName = "docs"
			tt.mutate(&o)
			err := validateOptions(&o)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestMetricHelpers(t *testing.T) {
	assert.Equal(t, "cosineDistance", MetricCosine.distanceFunction())
	assert.Equal(t, "L2Distance", MetricL2.distanceFunction())
	assert.Equal(t, "dotProduct", MetricInnerProduct.distanceFunction())

	assert.Equal(t, 1.0, MetricCosine.toScore(0.0))
	assert.Equal(t, 0.5, MetricCosine.toScore(1.0))
	assert.Equal(t, 0.0, MetricCosine.toScore(2.0))
	assert.Equal(t, 0.5, MetricL2.toScore(1.0))

	// Inner product is squashed into [0, 1]: a zero product maps to the
	// midpoint, and negative or large products stay inside the range while
	// preserving order.
	assert.InDelta(t, 0.5, MetricInnerProduct.toScore(0.0), 1e-9)
	assert.Greater(t, MetricInnerProduct.toScore(2.0), 0.5)
	assert.Less(t, MetricInnerProduct.toScore(-2.0), 0.5)
	for _, raw := range []float64{-100, -1, 0, 1, 100} {
		got := MetricInnerProduct.toScore(raw)
		assert.GreaterOrEqual(t, got, 0.0, "raw=%v", raw)
		assert.LessOrEqual(t, got, 1.0, "raw=%v", raw)
	}
	// The mapping must stay strictly increasing so SQL ordering is preserved.
	assert.Less(t, MetricInnerProduct.toScore(1.0), MetricInnerProduct.toScore(1.5))

	assert.Equal(t, "ASC", MetricCosine.orderByDirection())
	assert.Equal(t, "ASC", MetricL2.orderByDirection())
	assert.Equal(t, "DESC", MetricInnerProduct.orderByDirection())
}

func TestFilterFieldTypeClickHouseType(t *testing.T) {
	assert.Equal(t, "String", FilterFieldString.clickhouseType())
	assert.Equal(t, "Int64", FilterFieldInt64.clickhouseType())
	assert.Equal(t, "Float64", FilterFieldFloat64.clickhouseType())
	assert.Equal(t, "String", FilterFieldType(99).clickhouseType())
}

func TestWithOptions(t *testing.T) {
	o := defaultOptions
	WithTableName("mytable")(&o)
	WithVectorDimension(512)(&o)
	WithMetric(MetricL2)(&o)
	WithFilterFields(FilterFieldSpec{Name: "cat", Type: FilterFieldString})(&o)
	WithAutoCreateTable(false)(&o)
	WithAllowDestructiveDeleteAll(true)(&o)
	WithInstanceName("inst")(&o)
	WithDSN("clickhouse://x")(&o)
	WithExtraOptions("e1")(&o)
	WithMaxResults(20)(&o)
	WithIDFieldName("doc_id")(&o)
	WithNameFieldName("title")(&o)
	WithContentFieldName("body")(&o)
	WithEmbeddingFieldName("vec")(&o)
	WithMetadataFieldName("meta")(&o)
	WithCreatedAtFieldName("c_at")(&o)
	WithUpdatedAtFieldName("u_at")(&o)

	assert.Equal(t, "mytable", o.tableName)
	assert.Equal(t, 512, o.vectorDimension)
	assert.Equal(t, MetricL2, o.metric)
	assert.Len(t, o.filterFields, 1)
	assert.False(t, o.autoCreateTable)
	assert.True(t, o.allowDestructiveDeleteAll)
	assert.Equal(t, "inst", o.instanceName)
	assert.Equal(t, "clickhouse://x", o.dsn)
	assert.Equal(t, []any{"e1"}, o.extraOptions)
	assert.Equal(t, 20, o.maxResults)
	assert.Equal(t, "doc_id", o.idFieldName)
	assert.Equal(t, "title", o.nameFieldName)
	assert.Equal(t, "body", o.contentFieldName)
	assert.Equal(t, "vec", o.embeddingFieldName)
	assert.Equal(t, "meta", o.metadataFieldName)
	assert.Equal(t, "c_at", o.createdAtFieldName)
	assert.Equal(t, "u_at", o.updatedAtFieldName)
}
