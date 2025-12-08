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
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter string
		wantParams map[string]any
	}{
		{
			name:      "nil condition",
			condition: nil,
			wantErr:   true,
		},
		{
			name: "equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			wantErr:    false,
			wantFilter: `name == {name}`,
			wantParams: map[string]any{"name": "test"},
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantErr:    false,
			wantFilter: `age == {age}`,
			wantParams: map[string]any{"age": 25},
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantErr:    false,
			wantFilter: `status != {status}`,
			wantParams: map[string]any{"status": "active"},
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantErr:    false,
			wantFilter: `score > {score}`,
			wantParams: map[string]any{"score": 90},
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantErr:    false,
			wantFilter: `score >= {score}`,
			wantParams: map[string]any{"score": 80},
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantErr:    false,
			wantFilter: `price < {price}`,
			wantParams: map[string]any{"price": 100},
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			},
			wantErr:    false,
			wantFilter: `price <= {price}`,
			wantParams: map[string]any{"price": 50},
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr:    false,
			wantFilter: `active == {active}`,
			wantParams: map[string]any{"active": true},
		},
		{
			name: "metadata.xx field",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr:    false,
			wantFilter: `metadata["active"] == {metadata.active}`,
			wantParams: map[string]any{"metadata.active": true},
		},
		{
			name: "in operator with string values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorIn,
				Value:    []any{"Alice", "Bob", "Charlie"},
			},
			wantFilter: `name in {name}`,
			wantErr:    false,
			wantParams: map[string]any{"name": []any{"Alice", "Bob", "Charlie"}},
		},
		{
			name: "not in operator with numeric values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []any{18, 25, 30},
			},
			wantFilter: `age not in {age}`,
			wantErr:    false,
			wantParams: map[string]any{"age": []any{18, 25, 30}},
		},
		{
			name: "like operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    "test",
			},
			wantFilter: `name like {name}`,
			wantErr:    false,
			wantParams: map[string]any{"name": "test"},
		},
		{
			name: "not like operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorNotLike,
				Value:    "test",
			},
			wantFilter: `name not like {name}`,
			wantErr:    false,
			wantParams: map[string]any{"name": "test"},
		},
		{
			name: "logical AND operator",
			condition: &searchfilter.UniversalFilterCondition{
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
			},
			wantFilter: `(name == {name}) and (age > {age})`,
			wantErr:    false,
			wantParams: map[string]any{"name": "test", "age": 25},
		},
		{
			name: "logical OR operator",
			condition: &searchfilter.UniversalFilterCondition{
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
			wantFilter: `(status == {status}) or (score < {score})`,
			wantErr:    false,
			wantParams: map[string]any{"status": "active", "score": 80},
		},
		{
			name: "composite condition with nested operators",
			condition: &searchfilter.UniversalFilterCondition{
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
			},
			wantFilter: `(name == {name}) and ((status == {status}) or (score < {score}))`,
			wantErr:    false,
			wantParams: map[string]any{"name": "test", "status": "active", "score": 80},
		},
		{
			name: "between operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{18, 30},
			},
			wantErr:    false,
			wantFilter: `age >= {age_0} and age <= {age_1}`,
			wantParams: map[string]any{"age_0": 18, "age_1": 30},
		},
		{
			name: "invalid operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: "invalid",
				Value:    true,
			},
			wantErr: true,
		},
	}

	c := &milvusFilterConverter{metadataFieldName: "metadata"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := c.Convert(tt.condition)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantFilter, cr.exprStr)
			if tt.wantParams != nil {
				assert.Equal(t, tt.wantParams, cr.params)
			}
		})
	}
}

func TestMilvusFilterConverter_ConvertCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter string
	}{
		{
			name: "equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			wantErr:    false,
			wantFilter: `name == {name}`,
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantErr:    false,
			wantFilter: `age == {age}`,
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantErr:    false,
			wantFilter: `status != {status}`,
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantErr:    false,
			wantFilter: `score > {score}`,
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantErr:    false,
			wantFilter: `score >= {score}`,
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantErr:    false,
			wantFilter: `price < {price}`,
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			},
			wantErr:    false,
			wantFilter: `price <= {price}`,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr:    false,
			wantFilter: `active == {active}`,
		},
		{
			name: "between operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{18, 30},
			},
			wantErr:    false,
			wantFilter: `age >= {age_0} and age <= {age_1}`,
		},
		{
			name: "and operator",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Field:    "name",
						Operator: searchfilter.OperatorEqual,
						Value:    "test",
					},
				},
			},
			wantErr:    false,
			wantFilter: `name == {name}`,
		},
		{
			name: "invalid operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: "invalid",
				Value:    true,
			},
			wantErr: true,
		},
		{
			name: "empty filed equal to string",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			wantErr: true,
		},
		{
			name: "empty field not equal to string",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "test",
			},
			wantErr: true,
		},
		{
			name: "empty field greater than",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    10,
			},
			wantErr: true,
		},
		{
			name: "empty field greater than or equal",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    10,
			},
			wantErr: true,
		},
		{
			name: "empty field less than",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorLessThan,
				Value:    10,
			},
			wantErr: true,
		},
		{
			name: "empty field less than or equal",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    10,
			},
			wantErr: true,
		},
		{
			name: "empty field like",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorLike,
				Value:    "test",
			},
			wantErr: true,
		},
		{
			name: "invalid in",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []any{},
			},
			wantErr: true,
		},
		{
			name: "invalid in with empty field",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorIn,
				Value:    []any{1, 2, 3},
			},
			wantErr: true,
		},
		{
			name: "invalid between",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{},
			},
			wantErr: true,
		},
		{
			name: "invalid not in with empty field",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{1, 2},
			},
			wantErr: true,
		},
	}

	c := &milvusFilterConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := c.convertCondition(tt.condition)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.wantFilter, cr.exprStr)
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
