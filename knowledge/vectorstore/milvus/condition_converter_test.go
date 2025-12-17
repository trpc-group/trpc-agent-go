//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package milvus

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func TestMilvusFilterConverter_Convert(t *testing.T) {
	type args struct {
		cond *searchfilter.UniversalFilterCondition
	}
	type testHandler func(t *testing.T, res *convertResult, err error)

	c := newMilvusFilterConverter("metadata")

	tests := []struct {
		name    string
		args    args
		handler testHandler
	}{
		{
			name: "nil condition",
			args: args{cond: nil},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "equal operator with string value",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `name == {name_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"name_1": "test"}, res.params)
			},
		},
		{
			name: "equal operator with numeric value",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `age == {age_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"age_1": 25}, res.params)
			},
		},
		{
			name: "not equal operator with string value",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `status != {status_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"status_1": "active"}, res.params)
			},
		},
		{
			name: "greater than operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `score > {score_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"score_1": 90}, res.params)
			},
		},
		{
			name: "greater than or equal operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `score >= {score_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"score_1": 80}, res.params)
			},
		},
		{
			name: "less than operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `price < {price_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"price_1": 100}, res.params)
			},
		},
		{
			name: "less than or equal operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `price <= {price_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"price_1": 50}, res.params)
			},
		},
		{
			name: "boolean value",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `active == {active_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"active_1": true}, res.params)
			},
		},
		{
			name: "metadata.xx field",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["active"] == {metadata_active_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_active_1": true}, res.params)
			},
		},
		{
			name: "in operator with string values",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorIn,
				Value:    []any{"Alice", "Bob", "Charlie"},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `name in {name_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"name_1": []any{"Alice", "Bob", "Charlie"}}, res.params)
			},
		},
		{
			name: "not in operator with numeric values",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []any{18, 25, 30},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `age not in {age_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"age_1": []any{18, 25, 30}}, res.params)
			},
		},
		{
			name: "like operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    "test",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `name like {name_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"name_1": "test"}, res.params)
			},
		},
		{
			name: "not like operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorNotLike,
				Value:    "test",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `name not like {name_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"name_1": "test"}, res.params)
			},
		},
		{
			name: "logical AND operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "name",
						Operator: searchfilter.OperatorEqual,
						Value:    "test",
					},
					{
						Field:    "age",
						Operator: searchfilter.OperatorGreaterThan,
						Value:    25,
					},
				},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `(name == {name_1}) and (age > {age_2})`, res.exprStr)
				assert.Equal(t, map[string]any{"name_1": "test", "age_2": 25}, res.params)
			},
		},
		{
			name: "logical OR operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "status",
						Operator: searchfilter.OperatorEqual,
						Value:    "active",
					},
					{
						Field:    "score",
						Operator: searchfilter.OperatorLessThan,
						Value:    80,
					},
				},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `(status == {status_1}) or (score < {score_2})`, res.exprStr)
				assert.Equal(t, map[string]any{"status_1": "active", "score_2": 80}, res.params)
			},
		},
		{
			name: "composite condition with nested operators",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "name",
						Operator: searchfilter.OperatorEqual,
						Value:    "test",
					},
					{
						Operator: searchfilter.OperatorOr,
						Value: []*searchfilter.UniversalFilterCondition{
							{
								Field:    "status",
								Operator: searchfilter.OperatorEqual,
								Value:    "active",
							},
							{
								Field:    "score",
								Operator: searchfilter.OperatorLessThan,
								Value:    80,
							},
						},
					},
				},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `(name == {name_1}) and ((status == {status_2}) or (score < {score_3}))`, res.exprStr)
				assert.Equal(t, map[string]any{"name_1": "test", "status_2": "active", "score_3": 80}, res.params)
			},
		},
		{
			name: "between operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{18, 30},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `age >= {age_1_0} and age <= {age_1_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"age_1_0": 18, "age_1_1": 30}, res.params)
			},
		},
		{
			name: "metadata field equal operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.topic",
				Operator: searchfilter.OperatorEqual,
				Value:    "AI",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["topic"] == {metadata_topic_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_topic_1": "AI"}, res.params)
			},
		},
		{
			name: "metadata field in operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.tags",
				Operator: searchfilter.OperatorIn,
				Value:    []any{"AI", "ML", "NLP"},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["tags"] in {metadata_tags_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_tags_1": []any{"AI", "ML", "NLP"}}, res.params)
			},
		},
		{
			name: "metadata field between operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.score",
				Operator: searchfilter.OperatorBetween,
				Value:    []float64{0.5, 1.0},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["score"] >= {metadata_score_1_0} and metadata["score"] <= {metadata_score_1_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_score_1_0": 0.5, "metadata_score_1_1": 1.0}, res.params)
			},
		},
		{
			name: "metadata field greater than operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.count",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    100,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["count"] > {metadata_count_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_count_1": 100}, res.params)
			},
		},
		{
			name: "metadata field not in operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.category",
				Operator: searchfilter.OperatorNotIn,
				Value:    []any{"spam", "ads"},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["category"] not in {metadata_category_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_category_1": []any{"spam", "ads"}}, res.params)
			},
		},
		{
			name: "mixed schema and metadata fields with AND",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "doc_id",
						Operator: searchfilter.OperatorEqual,
						Value:    "doc123",
					},
					{
						Field:    "metadata.topic",
						Operator: searchfilter.OperatorIn,
						Value:    []any{"AI", "ML"},
					},
				},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `(doc_id == {doc_id_1}) and (metadata["topic"] in {metadata_topic_2})`, res.exprStr)
				assert.Equal(t, map[string]any{"doc_id_1": "doc123", "metadata_topic_2": []any{"AI", "ML"}}, res.params)
			},
		},
		{
			name: "metadata field with string containing quotes",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.title",
				Operator: searchfilter.OperatorEqual,
				Value:    `He said "Hello"`,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `metadata["title"] == {metadata_title_1}`, res.exprStr)
				assert.Equal(t, map[string]any{"metadata_title_1": `He said "Hello"`}, res.params)
			},
		},
		{
			name: "invalid operator",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: "invalid",
				Value:    true,
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "empty field equal to string",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "empty field not equal to string",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "test",
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "invalid in",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []any{},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "invalid between",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.Error(t, err)
			},
		},
		{
			name: "same field used multiple times (conflict check)",
			args: args{cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "age",
						Operator: searchfilter.OperatorGreaterThan,
						Value:    10,
					},
					{
						Field:    "age",
						Operator: searchfilter.OperatorLessThan,
						Value:    5,
					},
				},
			}},
			handler: func(t *testing.T, res *convertResult, err error) {
				assert.NoError(t, err)
				assert.Equal(t, `(age > {age_1}) or (age < {age_2})`, res.exprStr)
				assert.Equal(t, map[string]any{"age_1": 10, "age_2": 5}, res.params)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := c.Convert(tt.args.cond)
			tt.handler(t, res, err)
		})
	}
}

func Test_formatValue(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		value   any
		want    string
		wantErr bool
	}{
		{
			name:  "string value",
			value: "test",
			want:  "\"test\"",
		},
		{
			name:  "int value",
			value: 10,
			want:  `10`,
		},
		{
			name:  "float value",
			value: 3.14,
			want:  `3.14`,
		},
		{
			name:  "bool value",
			value: true,
			want:  `true`,
		},
		{
			name:  "false value",
			value: false,
			want:  `false`,
		},
		{
			name:  "uint",
			value: uint(10),
			want:  `10`,
		},
		{
			name:  "time value",
			value: now,
			want:  fmt.Sprintf("%d", now.Unix()),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatValue(tt.value)
			assert.Equal(t, tt.want, got)
		})
	}
}
