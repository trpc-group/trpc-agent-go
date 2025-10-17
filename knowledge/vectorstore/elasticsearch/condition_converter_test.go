//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch-based vector storage implementation.
package elasticsearch

import (
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func Test_esConverter_convertCondition(t *testing.T) {
	tests := []struct {
		name      string
		condition *searchfilter.UniversalFilterCondition
		want      string
		wantErr   bool
	}{
		{
			name: "string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "test",
			},
			want:    `{"bool":{"must_not":[{"term":{"name":{"value":"test"}}}]}}`,
			wantErr: false,
		},
		{
			name: "numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotEqual,
				Value:    25,
			},
			want:    `{"bool":{"must_not":[{"term":{"age":{"value":25}}}]}}`,
			wantErr: false,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorNotEqual,
				Value:    true,
			},
			want:    `{"bool":{"must_not":[{"term":{"active":{"value":true}}}]}}`,
			wantErr: false,
		},
		{
			name: "like condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorLike,
				Value:    "*test*",
			},
			want:    `{"wildcard":{"active":{"value":"*test*"}}}`,
			wantErr: false,
		},
		{
			name: "nil value like condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorLike,
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty value like condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorLike,
				Value:    "",
			},
			want:    `{"wildcard":{"active":{"value":""}}}`,
			wantErr: false,
		},
		{
			name: "test1",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			want:    `{"term":{"name":{"value":"test"}}}`,
			wantErr: false,
		},
		{
			name: "test2",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			want:    `{"term":{"age":{"value":25}}}`,
			wantErr: false,
		},
		{
			name: "test3",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			want:    `{"term":{"active":{"value":true}}}`,
			wantErr: false,
		},
		{
			name: "test1",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    25,
			},
			wantErr: false,
			want:    `{"range":{"age":{"gt":25}}}`,
		},
		{
			name: "test2",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    25,
			},
			wantErr: false,
			want:    `{"range":{"age":{"gte":25}}}`,
		},
		{
			name: "in condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []int{25, 26},
			},
			wantErr: false,
			want:    `{"terms":{"age":[25,26]}}`,
		},
		{
			name: "not in condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotIn,
				Value:    []int{25, 26},
			},
			wantErr: false,
			want:    `{"bool":{"must_not":[{"terms":{"age":[25,26]}}]}}`,
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
			wantErr: false,
			want:    `{"bool":{"must":[{"term":{"name":{"value":"test"}}},{"bool":{"should":[{"term":{"status":{"value":"active"}}},{"range":{"score":{"lt":80}}}]}}]}}`,
		},
		{
			name: "Between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{20, 30},
			},
			wantErr: false,
			want:    `{"range":{"age":{"gte":20,"lte":30}}}`,
		},
		{
			name: "nil value Between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty value Between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{},
			},
			wantErr: true,
		},
		{
			name: "invalid value Between condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{nil, 1},
			},
			wantErr: false,
			want:    `{"range":{"age":{"gte":null,"lte":1}}}`,
		},
		{
			name: "nil value in condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty value in condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []any{},
			},
			wantErr: true,
		},
		{
			name: "nil element in condition",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorIn,
				Value:    []any{nil},
			},
			wantErr: false,
			want:    `{"terms":{"age":[null]}}`,
		},
	}

	c := &esConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := c.convertCondition(tt.condition)
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
			gotJSON, err := got.QueryCaster().MarshalJSON()
			if err != nil {
				t.Errorf("Failed to MarshalJSON got: %v", err)
				return
			}
			if !reflect.DeepEqual(string(gotJSON), tt.want) {
				t.Errorf("esConverter.convertCondition() = %v, want %v", string(gotJSON), tt.want)
			}
		})
	}
}

func Test_esConverter_convertNotEqual(t *testing.T) {
	tests := []struct {
		name      string
		condition *searchfilter.UniversalFilterCondition
		want      string
		wantErr   bool
	}{
		{
			name: "string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "test",
			},
			want:    `{"bool":{"must_not":[{"term":{"name":{"value":"test"}}}]}}`,
			wantErr: false,
		},
		{
			name: "numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorNotEqual,
				Value:    25,
			},
			want:    `{"bool":{"must_not":[{"term":{"age":{"value":25}}}]}}`,
			wantErr: false,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorNotEqual,
				Value:    true,
			},
			want:    `{"bool":{"must_not":[{"term":{"active":{"value":true}}}]}}`,
			wantErr: false,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorNotEqual,
				Value:    nil,
			},
			want:    `{"bool":{"must_not":[{"term":{"active":{"value":null}}}]}}`,
			wantErr: false,
		},
	}

	c := &esConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := c.convertNotEqual(tt.condition)
			if tt.wantErr {
				if err == nil {
					t.Errorf("convertNotEqual() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("convertNotEqual() unexpected error = %v", err)
				return
			}
			gotJSON, err := got.QueryCaster().MarshalJSON()
			if err != nil {
				t.Errorf("Failed to MarshalJSON got: %v", err)
				return
			}
			if !reflect.DeepEqual(string(gotJSON), tt.want) {
				t.Errorf("esConverter.convertNotEqual() = %v, want %v", string(gotJSON), tt.want)
			}
		})
	}
}

func Test_esConverter_convertEqual(t *testing.T) {
	tests := []struct {
		name      string
		condition *searchfilter.UniversalFilterCondition
		want      string
		wantErr   bool
	}{
		{
			name: "test1",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			want:    `{"term":{"name":{"value":"test"}}}`,
			wantErr: false,
		},
		{
			name: "test2",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			want:    `{"term":{"age":{"value":25}}}`,
			wantErr: false,
		},
		{
			name: "test3",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			want:    `{"term":{"active":{"value":true}}}`,
			wantErr: false,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    nil,
			},
			want:    `{"term":{"active":{"value":null}}}`,
			wantErr: false,
		},
	}
	c := &esConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := c.convertEqual(tt.condition)
			if tt.wantErr {
				if err == nil {
					t.Errorf("convertEqual() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("convertEqual() unexpected error = %v", err)
				return
			}
			gotJSON, err := got.QueryCaster().MarshalJSON()
			if err != nil {
				t.Errorf("Failed to MarshalJSON got: %v", err)
				return
			}
			if !reflect.DeepEqual(string(gotJSON), tt.want) {
				t.Errorf("esConverter.convertEqual() = %v, want %v", string(gotJSON), tt.want)
			}
		})
	}
}

func Test_esConverter_convertRange(t *testing.T) {
	tests := []struct {
		name      string
		condition *searchfilter.UniversalFilterCondition
		want      string
		wantErr   bool
	}{
		{
			name: "test1",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    25,
			},
			wantErr: false,
			want:    `{"range":{"age":{"gt":25}}}`,
		},
		{
			name: "test2",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    25,
			},
			wantErr: false,
			want:    `{"range":{"age":{"gte":25}}}`,
		},
		{
			name: "test3",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorLessThan,
				Value:    25,
			},
			wantErr: false,
			want:    `{"range":{"age":{"lt":25}}}`,
		},
		{
			name: "test4",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    25,
			},
			wantErr: false,
			want:    `{"range":{"age":{"lte":25}}}`,
		},
		{
			name: "test5",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "date",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    "2025-10-11 11:11:11",
			},
			wantErr: false,
			want:    `{"range":{"date":{"lte":"2025-10-11 11:11:11"}}}`,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "date",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    nil,
			},
			wantErr: false,
			want:    `{"range":{"date":{"lte":null}}}`,
		},
	}
	c := &esConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := c.convertRange(tt.condition)
			if tt.wantErr {
				if err == nil {
					t.Errorf("convertRange() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("convertRange() unexpected error = %v", err)
				return
			}
			gotJSON, err := got.QueryCaster().MarshalJSON()
			if err != nil {
				t.Errorf("Failed to MarshalJSON got: %v", err)
				return
			}
			if !reflect.DeepEqual(string(gotJSON), tt.want) {
				t.Errorf("esConverter.convertRange() = %v, want %v", string(gotJSON), tt.want)
			}
		})
	}
}

func Test_esConverter_buildComparisonCondition(t *testing.T) {
	tests := []struct {
		name      string
		condition *searchfilter.UniversalFilterCondition
		want      string
		wantErr   bool
	}{
		{
			name: "equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test",
			},
			wantErr: false,
			want:    `{"term":{"name":{"value":"test"}}}`,
		},
		{
			name: "equal operator with numeric value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "age",
				Operator: searchfilter.OperatorEqual,
				Value:    25,
			},
			wantErr: false,
			want:    `{"term":{"age":{"value":25}}}`,
		},
		{
			name: "not equal operator with string value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "status",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "active",
			},
			wantErr: false,
			want:    `{"bool":{"must_not":[{"term":{"status":{"value":"active"}}}]}}`,
		},
		{
			name: "greater than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    90,
			},
			wantErr: false,
			want:    `{"range":{"score":{"gt":90}}}`,
		},
		{
			name: "greater than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "score",
				Operator: searchfilter.OperatorGreaterThanOrEqual,
				Value:    80,
			},
			wantErr: false,
			want:    `{"range":{"score":{"gte":80}}}`,
		},
		{
			name: "less than operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThan,
				Value:    100,
			},
			wantErr: false,
			want:    `{"range":{"price":{"lt":100}}}`,
		},
		{
			name: "less than or equal operator",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "price",
				Operator: searchfilter.OperatorLessThanOrEqual,
				Value:    50,
			},
			wantErr: false,
			want:    `{"range":{"price":{"lte":50}}}`,
		},
		{
			name: "boolean value",
			condition: &searchfilter.UniversalFilterCondition{
				Field:    "active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr: false,
			want:    `{"term":{"active":{"value":true}}}`,
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
			name: "invalid filed",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			wantErr: true,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "active",
				Value:    nil,
			},
			wantErr: false,
			want:    `{"term":{"active":{"value":null}}}`,
		},
	}
	c := &esConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := c.buildComparisonCondition(tt.condition)
			if tt.wantErr {
				if err == nil {
					t.Errorf("buildComparisonCondition() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("buildComparisonCondition() unexpected error = %v", err)
				return
			}
			gotJSON, err := got.QueryCaster().MarshalJSON()
			if err != nil {
				t.Errorf("Failed to MarshalJSON got: %v", err)
				return
			}
			if !reflect.DeepEqual(string(gotJSON), tt.want) {
				t.Errorf("esConverter.buildComparisonCondition() = %v, want %v", string(gotJSON), tt.want)
			}
		})
	}
}
func Test_esConverter_buildLogicalCondition(t *testing.T) {
	tests := []struct {
		name      string
		condition *searchfilter.UniversalFilterCondition
		want      string
		wantErr   bool
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
			wantErr: false,
			want:    `{"bool":{"must":[{"term":{"name":{"value":"test"}}},{"range":{"age":{"gt":25}}}]}}`,
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
			wantErr: false,
			want:    `{"bool":{"should":[{"term":{"status":{"value":"active"}}},{"range":{"score":{"lt":80}}}]}}`,
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
			wantErr: false,
			want:    `{"bool":{"must":[{"term":{"name":{"value":"test"}}},{"bool":{"should":[{"term":{"status":{"value":"active"}}},{"range":{"score":{"lt":80}}}]}}]}}`,
		},
		{
			name: "nil value",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Field:    "status",
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty slice",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Field:    "status",
				Value:    []*searchfilter.UniversalFilterCondition{},
			},
			wantErr: true,
		},
		{
			name: "nil element slice",
			condition: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Field:    "status",
				Value:    []*searchfilter.UniversalFilterCondition{nil, nil},
			},
			wantErr: true,
		},
	}

	c := &esConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			got, err := c.buildLogicalCondition(tt.condition)
			if tt.wantErr {
				if err == nil {
					t.Errorf("buildLogicalCondition() expected error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("buildLogicalCondition() unexpected error = %v", err)
				return
			}
			gotJSON, err := got.QueryCaster().MarshalJSON()
			if err != nil {
				t.Errorf("Failed to MarshalJSON got: %v", err)
				return
			}
			if !reflect.DeepEqual(string(gotJSON), tt.want) {
				t.Errorf("esConverter.buildLogicalCondition() = %v, want %v", string(gotJSON), tt.want)
			}
		})
	}
}
