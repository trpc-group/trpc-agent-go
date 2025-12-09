//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tcvector

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func Test_tcVectorConverter_convertCondition(t *testing.T) {
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
			wantFilter: `name = "test"`,
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantErr:    false,
			wantFilter: `age = 25`,
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantErr:    false,
			wantFilter: `status != "active"`,
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantErr:    false,
			wantFilter: `score > 90`,
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantErr:    false,
			wantFilter: `score >= 80`,
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantErr:    false,
			wantFilter: `price < 100`,
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			},
			wantErr:    false,
			wantFilter: `price <= 50`,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr:    false,
			wantFilter: `active = true`,
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
			wantFilter: `name in ("Alice","Bob","Charlie")`,
			wantErr:    false,
		},
		{
			name: "not in operator with numeric values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []int{18, 25, 30},
			},
			wantFilter: "age not in (18,25,30)",
			wantErr:    false,
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
			wantFilter: `(name = "test") and (age > 25)`,
			wantErr:    false,
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
			wantFilter: `(status = "active") or (score < 80)`,
			wantErr:    false,
		},
		{
			name: "string between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "date",
				Operator: searchfilter.OperatorBetween,
				Value:    []string{"2020-01-01", "2020-01-31"},
			},
			wantFilter: `date >= "2020-01-01" and (date <= "2020-01-31")`,
			wantErr:    false,
		},
		{
			name: "number between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{20, 30},
			},
			wantFilter: `age >= 20 and (age <= 30)`,
			wantErr:    false,
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
			wantFilter: `(name = "test") and ((status = "active") or (score < 80))`,
			wantErr:    false,
		},
		{
			name: "multiple names grouped with permissions",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{
						Operator: searchfilter.OperatorOr,
						Value: []*searchfilter.UniversalFilterCondition{
							{
								Field:    "name",
								Operator: searchfilter.OperatorEqual,
								Value:    "name1",
							},
							{
								Field:    "name",
								Operator: searchfilter.OperatorEqual,
								Value:    "name2",
							},
						},
					},
					{
						Operator: searchfilter.OperatorOr,
						Value: []*searchfilter.UniversalFilterCondition{
							{
								Field:    "permission",
								Operator: searchfilter.OperatorEqual,
								Value:    1,
							},
							{
								Field:    "user",
								Operator: searchfilter.OperatorEqual,
								Value:    "user1",
							},
						},
					},
				},
			},
			wantFilter: `((name = "name1") or (name = "name2")) and ((permission = 1) or (user = "user1"))`,
			wantErr:    false,
		},
		{
			name: "nil between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{},
			},
			wantErr: true,
		},
		{
			name: "nil element between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{nil, nil},
			},
			wantFilter: `age >= <nil> and (age <= <nil>)`,
			wantErr:    false,
		},
	}

	c := &tcVectorConverter{}

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

			if filter.Cond() != tt.wantFilter {
				t.Errorf("convertCondition() filter = %v, want %v", filter.Cond(), tt.wantFilter)
			}
		})
	}
}

func TestTcVectorConverter_buildLogicalCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter string
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
			wantFilter: `(name = "test") and (age > 25)`,
			wantErr:    false,
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
			wantFilter: `(status = "active") or (score < 80)`,
			wantErr:    false,
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
			wantFilter: `(name = "test") and ((status = "active") or (score < 80))`,
			wantErr:    false,
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
	}

	converter := &tcVectorConverter{}

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

			if filter.Cond() != tc.wantFilter {
				t.Errorf("buildLogicalCondition() filter = %v, want %v", filter.Cond(), tc.wantFilter)
			}
		})
	}
}

func TestTcVectorConverter_buildInCondition(t *testing.T) {
	tests := []struct {
		name       string
		condition  *searchfilter.UniversalFilterCondition
		wantErr    bool
		wantFilter string
	}{
		{
			name: "in operator with string values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorIn,
				Value:    []string{"Alice", "Bob", "Charlie"},
			},
			wantFilter: `name in ("Alice","Bob","Charlie")`,
			wantErr:    false,
		},
		{
			name: "not in operator with numeric values",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []int{18, 25, 30},
			},
			wantFilter: "age not in (18,25,30)",
			wantErr:    false,
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
			wantErr:    false,
			wantFilter: `age in (<nil>,<nil>)`,
		},
	}

	converter := &tcVectorConverter{}

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

			if filter.Cond() != tc.wantFilter {
				t.Errorf("buildInCondition() filter = %v, want %v", filter.Cond(), tc.wantFilter)
			}
		})
	}
}

func TestTcVectorConverter_buildComparisonCondition(t *testing.T) {
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
			wantFilter: `name = "test"`,
			wantErr:    false,
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantFilter: "age = 25",
			wantErr:    false,
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantFilter: `status != "active"`,
			wantErr:    false,
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantFilter: "score > 90",
			wantErr:    false,
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantFilter: "score >= 80",
			wantErr:    false,
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantFilter: "price < 100",
			wantErr:    false,
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50.0,
			},
			wantFilter: "price <= 50",
			wantErr:    false,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantFilter: "active = true",
			wantErr:    false,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    nil,
			},
			wantFilter: "active = <nil>",
			wantErr:    false,
		},
	}

	converter := &tcVectorConverter{}

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

			if filter.Cond() != tc.wantFilter {
				t.Errorf("buildComparisonCondition() filter = %v, want %v", filter.Cond(), tc.wantFilter)
			}
		})
	}
}
