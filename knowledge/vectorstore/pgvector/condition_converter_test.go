//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func Test_pgVectorConverter_convertCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter condConvertResult
	}{
		{
			name: "equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "name = $%d",
				args: []any{"test"},
			},
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "age = $%d",
				args: []any{25},
			},
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "status != $%d",
				args: []any{"active"},
			},
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "score > $%d",
				args: []any{90},
			},
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "score >= $%d",
				args: []any{80},
			},
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "price < $%d",
				args: []any{100},
			},
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "price <= $%d",
				args: []any{50},
			},
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr: false,
			wantFilter: condConvertResult{
				cond: "active = $%d",
				args: []any{true},
			},
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
			name: "in operator with string values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorIn,
				Value:    []string{"Alice", "Bob", "Charlie"},
			},
			wantFilter: condConvertResult{
				cond: "name IN ($%d, $%d, $%d)",
				args: []any{"Alice", "Bob", "Charlie"},
			},
			wantErr: false,
		},
		{
			name: "like operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    `%name%`,
			},
			wantFilter: condConvertResult{
				cond: "name LIKE $%d",
				args: []any{`%name%`},
			},
			wantErr: false,
		},
		{
			name: "between operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{18, 30},
			},
			wantFilter: condConvertResult{
				cond: "age >= $%d AND age <= $%d",
				args: []any{18, 30},
			},
			wantErr: false,
		},
		{
			name: "not in operator with numeric values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []int{18, 25, 30},
			},
			wantFilter: condConvertResult{
				cond: "age NOT IN ($%d, $%d, $%d)",
				args: []any{18, 25, 30},
			},
			wantErr: false,
		},
		{
			name: "empty field",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorIn,
				Value:    []string{"Alice", "Bob", "Charlie"},
			},
			wantErr: true,
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
			wantFilter: condConvertResult{
				cond: "(name = $%d) AND (age > $%d)",
				args: []any{"test", 25},
			},
			wantErr: false,
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
			wantFilter: condConvertResult{
				cond: "(status = $%d) OR (score < $%d)",
				args: []any{"active", 80},
			},
			wantErr: false,
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
			wantFilter: condConvertResult{
				cond: "(name = $%d) AND ((status = $%d) OR (score < $%d))",
				args: []any{"test", "active", 80},
			},
			wantErr: false,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Field:    "age",
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty slice",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Field:    "age",
				Value:    []*searchfilter.UniversalFilterCondition{},
			},
			wantErr: true,
		},
		{
			name: "nil element slice",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Field:    "age",
				Value:    []*searchfilter.UniversalFilterCondition{nil, nil},
			},
			wantErr: true,
		},
		{
			name: "nil value between operator",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "age",
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty value between operator",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "age",
				Value:    []any{},
			},
			wantErr: true,
		},
		{
			name: "nil element value between operator",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "age",
				Value:    []any{nil, nil},
			},
			wantFilter: condConvertResult{
				cond: "age >= $%d AND age <= $%d",
				args: []any{nil, nil},
			},
			wantErr: false,
		},
	}

	c := &pgVectorConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			filter, err := c.convertCondition(tt.condition)
			if tt.wantErr {
				if err == nil {
					t.Errorf("convertCondition() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("convertCondition() unexpected error = %v", err)
				return
			}

			if filter == nil {
				t.Error("convertCondition() returned nil filter")
				return
			}

			if !reflect.DeepEqual(*filter, tt.wantFilter) {
				t.Errorf("convertCondition() args = %v, want %v", *filter, tt.wantFilter)
			}
		})
	}
}

func Test_pgVectorConverter_buildLogicalCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter condConvertResult
	}{
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
			wantFilter: condConvertResult{
				cond: "(name = $%d) AND (age > $%d)",
				args: []any{"test", 25},
			},
			wantErr: false,
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
			wantFilter: condConvertResult{
				cond: "(status = $%d) OR (score < $%d)",
				args: []any{"active", 80},
			},
			wantErr: false,
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
			wantFilter: condConvertResult{
				cond: "(name = $%d) AND ((status = $%d) OR (score < $%d))",
				args: []any{"test", "active", 80},
			},
			wantErr: false,
		},
	}

	converter := &pgVectorConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := converter.buildLogicalCondition(tc.condition)

			if tc.wantErr {
				if err == nil {
					t.Errorf("buildLogicalCondition() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("buildLogicalCondition() unexpected error = %v", err)
				return
			}

			if filter == nil {
				t.Error("buildLogicalCondition() returned nil filter")
				return
			}

			if !reflect.DeepEqual(*filter, tc.wantFilter) {
				t.Errorf("buildLogicalCondition() args = %v, want %v", *filter, tc.wantFilter)
			}
		})
	}
}

func Test_pgVectorConverter_buildInCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter condConvertResult
	}{
		{
			name: "in operator with string values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorIn,
				Value:    []string{"Alice", "Bob", "Charlie"},
			},
			wantFilter: condConvertResult{
				cond: "name IN ($%d, $%d, $%d)",
				args: []any{"Alice", "Bob", "Charlie"},
			},
			wantErr: false,
		},
		{
			name: "not in operator with numeric values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []int{18, 25, 30},
			},
			wantFilter: condConvertResult{
				cond: "age NOT IN ($%d, $%d, $%d)",
				args: []any{18, 25, 30},
			},
			wantErr: false,
		},
		{
			name: "empty field",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "",
				Operator: searchfilter.OperatorIn,
				Value:    []string{"Alice", "Bob", "Charlie"},
			},
			wantErr: true,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []any{},
			},
			wantErr: true,
		},
		{
			name: "nil element value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []any{nil, nil},
			},
			wantFilter: condConvertResult{
				cond: "age IN ($%d, $%d)",
				args: []any{nil, nil},
			},
			wantErr: false,
		},
	}

	converter := &pgVectorConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := converter.buildInCondition(tc.condition)

			if tc.wantErr {
				if err == nil {
					t.Errorf("buildInCondition() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("buildInCondition() unexpected error = %v", err)
				return
			}

			if filter == nil {
				t.Error("buildInCondition() returned nil filter")
				return
			}

			if !reflect.DeepEqual(*filter, tc.wantFilter) {
				t.Errorf("buildInCondition() args = %v, want %v", *filter, tc.wantFilter)
			}
		})
	}
}

func Test_pgVectorConverter_buildComparisonCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter condConvertResult
	}{
		{
			name: "equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			wantFilter: condConvertResult{
				cond: "name = $%d",
				args: []any{"test"},
			},
			wantErr: false,
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantFilter: condConvertResult{
				cond: "age = $%d",
				args: []any{25},
			},
			wantErr: false,
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantFilter: condConvertResult{
				cond: "status != $%d",
				args: []any{"active"},
			},
			wantErr: false,
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantFilter: condConvertResult{
				cond: "score > $%d",
				args: []any{90},
			},
			wantErr: false,
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantFilter: condConvertResult{
				cond: "score >= $%d",
				args: []any{80},
			},
			wantErr: false,
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantFilter: condConvertResult{
				cond: "price < $%d",
				args: []any{100},
			},
			wantErr: false,
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			},
			wantFilter: condConvertResult{
				cond: "price <= $%d",
				args: []any{50},
			},
			wantErr: false,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantFilter: condConvertResult{
				cond: "active = $%d",
				args: []any{true},
			},
			wantErr: false,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    nil,
			},
			wantFilter: condConvertResult{
				cond: "active = $%d",
				args: []any{nil},
			},
			wantErr: false,
		},
	}

	converter := &pgVectorConverter{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter, err := converter.buildComparisonCondition(tc.condition)

			if tc.wantErr {
				if err == nil {
					t.Errorf("buildComparisonCondition() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("buildComparisonCondition() unexpected error = %v", err)
				return
			}

			if filter == nil {
				t.Error("buildComparisonCondition() returned nil filter")
				return
			}

			if filter.cond != tc.wantFilter.cond {
				t.Errorf("buildComparisonCondition() cond = %v, want %v", filter.cond, tc.wantFilter.cond)
			}

			if !reflect.DeepEqual(*filter, tc.wantFilter) {
				t.Errorf("buildComparisonCondition() args = %v, want %v", *filter, tc.wantFilter)
			}
		})
	}
}
