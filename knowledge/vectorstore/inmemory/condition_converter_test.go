//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package inmemory provides an in-memory vector store implementation.
package inmemory

import (
	"reflect"
	"testing"

	"time"

	"runtime/debug"
	"strings"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
)

func Test_isValidField(t *testing.T) {
	type args struct {
		field string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{

		{
			name: "valid_comparison_field_id",
			args: args{field: "id"},
			want: true,
		},
		{
			name: "valid_comparison_field_name",
			args: args{field: "name"},
			want: true,
		},
		{
			name: "valid_comparison_field_content",
			args: args{field: "content"},
			want: true,
		},
		{
			name: "valid_comparison_field_metadata",
			args: args{field: "metadata"},
			want: true,
		},
		{
			name: "valid_comparison_field_created_at",
			args: args{field: "created_at"},
			want: true,
		},
		{
			name: "valid_comparison_field_updated_at",
			args: args{field: "updated_at"},
			want: true,
		},

		{
			name: "valid_metadata_prefix",
			args: args{field: "metadata.test_key"},
			want: true,
		},
		{
			name: "valid_metadata_nested_prefix",
			args: args{field: "metadata.nested.key"},
			want: true,
		},

		{
			name: "invalid_field_random_string",
			args: args{field: "invalid_field"},
			want: false,
		},
		{
			name: "invalid_partial_prefix",
			args: args{field: "meta."},
			want: false,
		},
		{
			name: "invalid_empty_string",
			args: args{field: ""},
			want: false,
		},
		{
			name: "invalid_case_sensitive",
			args: args{field: "Metadata.key"},
			want: false,
		},

		{
			name: "boundary_min_length",
			args: args{field: "md"},
			want: false,
		},
		{
			name: "boundary_special_chars",
			args: args{field: "metadata.!"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidField(tt.args.field); got != tt.want {
				t.Errorf("isValidField(%q) = %v, want %v", tt.args.field, got, tt.want)
			}
		})
	}
}

func Test_fieldValue(t *testing.T) {
	now := time.Now().UTC()
	testDoc := &document.Document{
		ID:        "test-id",
		Name:      "test-name",
		Content:   "test-content",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"exists": "value",
			"nested": map[string]any{
				"level": 2,
			},
		},
	}

	type args struct {
		doc   *document.Document
		field string
	}
	tests := []struct {
		name  string
		args  args
		want  any
		want1 bool
	}{

		{
			name:  "nil document",
			args:  args{doc: nil, field: idField},
			want:  nil,
			want1: false,
		},
		{
			name:  "empty field",
			args:  args{doc: testDoc, field: ""},
			want:  nil,
			want1: false,
		},

		{
			name:  "id field",
			args:  args{doc: testDoc, field: idField},
			want:  "test-id",
			want1: true,
		},
		{
			name:  "name field",
			args:  args{doc: testDoc, field: nameField},
			want:  "test-name",
			want1: true,
		},
		{
			name:  "content field",
			args:  args{doc: testDoc, field: contentField},
			want:  "test-content",
			want1: true,
		},
		{
			name:  "created_at field",
			args:  args{doc: testDoc, field: createdAtField},
			want:  now,
			want1: true,
		},
		{
			name:  "updated_at field",
			args:  args{doc: testDoc, field: updatedAtField},
			want:  now,
			want1: true,
		},

		{
			name:  "metadata exists",
			args:  args{doc: testDoc, field: "metadata.exists"},
			want:  "value",
			want1: true,
		},
		{
			name:  "nested metadata",
			args:  args{doc: testDoc, field: "metadata.nested"},
			want:  map[string]any{"level": 2},
			want1: true,
		},

		{
			name:  "metadata missing",
			args:  args{doc: testDoc, field: "metadata.missing"},
			want:  nil,
			want1: false,
		},
		{
			name:  "empty metadata",
			args:  args{doc: &document.Document{}, field: "metadata.key"},
			want:  nil,
			want1: false,
		},
		{
			name:  "invalid prefix",
			args:  args{doc: testDoc, field: "invalid_prefix.key"},
			want:  nil,
			want1: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := fieldValue(tt.args.doc, tt.args.field)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fieldValue() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("fieldValue() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_valueType(t *testing.T) {
	type args struct {
		value any
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "string type",
			args: args{value: "hello"},
			want: valueTypeString,
		},
		{
			name: "int type",
			args: args{value: 42},
			want: valueTypeNumber,
		},
		{
			name: "int64 type",
			args: args{value: int64(100)},
			want: valueTypeNumber,
		},
		{
			name: "float32 type",
			args: args{value: float32(3.14)},
			want: valueTypeNumber,
		},
		{
			name: "bool type",
			args: args{value: true},
			want: valueTypeBool,
		},
		{
			name: "time.Time type",
			args: args{value: time.Now()},
			want: valueTypeTime,
		},
		{
			name: "struct type",
			args: args{value: struct{}{}},
			want: "",
		},
		{
			name: "slice type",
			args: args{value: []int{1, 2, 3}},
			want: "",
		},
		{
			name: "nil value",
			args: args{value: nil},
			want: "",
		},
		{
			name: "map type",
			args: args{value: map[string]int{"key": 1}},
			want: "",
		},
		{
			name: "channel type",
			args: args{value: make(chan int)},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valueType(tt.args.value)
			require.Equal(t, tt.want, got, "valueType() = %v, want %v", got, tt.want)
		})
	}
}

func Test_valueType_EdgeCases(t *testing.T) {
	t.Run("pointer to time.Time", func(t *testing.T) {
		now := time.Now()
		require.Equal(t, "", valueType(&now))
	})

	t.Run("invalid kind with time.Time", func(t *testing.T) {
		type customTime time.Time
		require.Equal(t, "", valueType(customTime(time.Now())))
	})

	t.Run("zero value time.Time", func(t *testing.T) {
		require.Equal(t, valueTypeTime, valueType(time.Time{}))
	})

	t.Run("function type", func(t *testing.T) {
		require.Equal(t, "", valueType(func() {}))
	})
}

func Test_compareBool(t *testing.T) {
	type args struct {
		docValue  any
		condValue any
		operator  string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{

		{
			name: "both true with eq operator",
			args: args{docValue: true, condValue: true, operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "both false with eq operator",
			args: args{docValue: false, condValue: false, operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "true vs false with eq operator",
			args: args{docValue: true, condValue: false, operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "true vs false with ne operator",
			args: args{docValue: true, condValue: false, operator: searchfilter.OperatorNotEqual},
			want: true,
		},
		{
			name: "same true with ne operator",
			args: args{docValue: true, condValue: true, operator: searchfilter.OperatorNotEqual},
			want: false,
		},

		{
			name: "docValue is string",
			args: args{docValue: "true", condValue: true, operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "condValue is int",
			args: args{docValue: true, condValue: 1, operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "both non-bool types",
			args: args{docValue: "yes", condValue: 0, operator: searchfilter.OperatorEqual},
			want: false,
		},

		{
			name: "invalid operator",
			args: args{docValue: true, condValue: true, operator: "invalid_op"},
			want: false,
		},
		{
			name: "empty operator",
			args: args{docValue: true, condValue: true, operator: ""},
			want: false,
		},

		{
			name: "nil values with eq operator",
			args: args{docValue: nil, condValue: nil, operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "mixed nil values",
			args: args{docValue: nil, condValue: true, operator: searchfilter.OperatorEqual},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareBool(tt.args.docValue, tt.args.condValue, tt.args.operator); got != tt.want {
				t.Errorf("compareBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_toFloat64(t *testing.T) {
	type args struct {
		value any
	}
	tests := []struct {
		name  string
		args  args
		want  float64
		want1 bool
	}{

		{
			name:  "int",
			args:  args{value: 42},
			want:  42.0,
			want1: true,
		},
		{
			name:  "int8_min",
			args:  args{value: int8(-128)},
			want:  -128.0,
			want1: true,
		},
		{
			name:  "uint64_max",
			args:  args{value: uint64(18446744073709551615)},
			want:  1.8446744073709552e19,
			want1: true,
		},
		{
			name:  "float32",
			args:  args{value: float32(3.00)},
			want:  3.00,
			want1: true,
		},
		{
			name:  "float64_zero",
			args:  args{value: 0.0},
			want:  0.0,
			want1: true,
		},

		{
			name:  "string",
			args:  args{value: "invalid"},
			want:  0,
			want1: false,
		},
		{
			name:  "bool",
			args:  args{value: true},
			want:  0,
			want1: false,
		},
		{
			name:  "struct",
			args:  args{value: time.Now()},
			want:  0,
			want1: false,
		},

		{
			name:  "nil",
			args:  args{value: nil},
			want:  0,
			want1: false,
		},
		{
			name:  "max_int64",
			args:  args{value: int64(9223372036854775807)},
			want:  9.223372036854776e18,
			want1: true,
		},
		{
			name:  "min_uint",
			args:  args{value: uint(0)},
			want:  0.0,
			want1: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := toFloat64(tt.args.value)
			require.Equal(t, tt.want, got, "toFloat64() got = %v, want %v", got, tt.want)
			require.Equal(t, tt.want1, got1, "toFloat64() got1 = %v, want %v", got1, tt.want1)
		})
	}
}

func Test_inmemoryConverter_buildInCondition(t *testing.T) {
	now := time.Now()
	testDoc := &document.Document{
		ID:        "doc1",
		Name:      "Test Document",
		Content:   "Test content",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"category": "test",
			"tags":     "go",
		},
	}

	type args struct {
		cond *searchfilter.UniversalFilterCondition
	}
	tests := []struct {
		name     string
		args     args
		wantErr  bool
		testFunc func(comparisonFunc) bool
	}{
		{
			name: "invalid field",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "invalid_field",
					Operator: "in",
					Value:    []string{"value"},
				},
			},
			wantErr: true,
		},
		{
			name: "non-slice value",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "id",
					Operator: "in",
					Value:    "not_a_slice",
				},
			},
			wantErr: true,
		},
		{
			name: "empty slice",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "id",
					Operator: "in",
					Value:    []string{},
				},
			},
			wantErr: true,
		},
		{
			name: "valid id match",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "id",
					Operator: "in",
					Value:    []string{"doc1", "doc2"},
				},
			},
			testFunc: func(cf comparisonFunc) bool {
				return cf(testDoc) && !cf(&document.Document{ID: "doc3"})
			},
		},
		{
			name: "metadata field match",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "metadata.category",
					Operator: "in",
					Value:    []string{"test", "prod"},
				},
			},
			testFunc: func(cf comparisonFunc) bool {
				return cf(testDoc) && !cf(&document.Document{Metadata: map[string]any{"category": "dev"}})
			},
		},
		{
			name: "type mismatch with deep equal",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "created_at",
					Operator: "in",
					Value:    []time.Time{now},
				},
			},
			testFunc: func(cf comparisonFunc) bool {
				return cf(testDoc) && !cf(&document.Document{CreatedAt: now.Add(1 * time.Hour)})
			},
		},
		{
			name: "slice element order check",
			args: args{
				cond: &searchfilter.UniversalFilterCondition{
					Field:    "metadata.tags",
					Operator: "in",
					Value:    []string{"go", "python"},
				},
			},
			testFunc: func(cf comparisonFunc) bool {
				return cf(testDoc) && !cf(&document.Document{Metadata: map[string]any{"tags": []string{"python"}}})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &inmemoryConverter{}
			got, err := c.buildInCondition(tt.args.cond)

			if (err != nil) != tt.wantErr {
				t.Errorf("buildInCondition() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.testFunc != nil {
				if !tt.testFunc(got) {
					t.Error("comparison function does not behave as expected")
				}
			}
		})
	}
}

func Test_compareNumber(t *testing.T) {
	type args struct {
		docValue  any
		condValue any
		operator  string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{

		{
			name: "equal with same int",
			args: args{docValue: 5, condValue: 5, operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "equal with different numeric types",
			args: args{docValue: 5, condValue: 5.0, operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "not equal",
			args: args{docValue: 5, condValue: 6, operator: searchfilter.OperatorNotEqual},
			want: true,
		},
		{
			name: "greater than true",
			args: args{docValue: 10, condValue: 5, operator: searchfilter.OperatorGreaterThan},
			want: true,
		},
		{
			name: "greater than false",
			args: args{docValue: 5, condValue: 10, operator: searchfilter.OperatorGreaterThan},
			want: false,
		},
		{
			name: "greater or equal true with equal",
			args: args{docValue: 5, condValue: 5, operator: searchfilter.OperatorGreaterThanOrEqual},
			want: true,
		},
		{
			name: "greater or equal true with greater",
			args: args{docValue: 6, condValue: 5, operator: searchfilter.OperatorGreaterThanOrEqual},
			want: true,
		},
		{
			name: "less than true",
			args: args{docValue: 3, condValue: 5, operator: searchfilter.OperatorLessThan},
			want: true,
		},
		{
			name: "less than false",
			args: args{docValue: 5, condValue: 3, operator: searchfilter.OperatorLessThan},
			want: false,
		},
		{
			name: "less or equal true with equal",
			args: args{docValue: 5, condValue: 5, operator: searchfilter.OperatorLessThanOrEqual},
			want: true,
		},

		{
			name: "invalid operator",
			args: args{docValue: 5, condValue: 5, operator: "invalid_op"},
			want: false,
		},
		{
			name: "non-numeric docValue",
			args: args{docValue: "5", condValue: 5, operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "non-numeric condValue",
			args: args{docValue: 5, condValue: "5", operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "both non-numeric",
			args: args{docValue: "a", condValue: "b", operator: searchfilter.OperatorEqual},
			want: false,
		},

		{
			name: "zero values equality",
			args: args{docValue: 0, condValue: 0.0, operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "max int64 comparison",
			args: args{docValue: int64(1<<63 - 1), condValue: float64(1<<63 - 1), operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "float precision edge case",
			args: args{docValue: 0.1 + 0.2, condValue: 0.3, operator: searchfilter.OperatorEqual},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareNumber(tt.args.docValue, tt.args.condValue, tt.args.operator); got != tt.want {
				t.Errorf("compareNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_compareTime(t *testing.T) {

	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	earlierTime := baseTime.Add(-1 * time.Hour)
	laterTime := baseTime.Add(1 * time.Hour)

	type args struct {
		docValue  any
		condValue any
		operator  string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{

		{
			name: "gt - docTime after condTime",
			args: args{laterTime, baseTime, searchfilter.OperatorGreaterThan},
			want: true,
		},
		{
			name: "gt - docTime before condTime",
			args: args{earlierTime, baseTime, searchfilter.OperatorGreaterThan},
			want: false,
		},
		{
			name: "gte - times equal",
			args: args{baseTime, baseTime, searchfilter.OperatorGreaterThanOrEqual},
			want: true,
		},
		{
			name: "gte - docTime after condTime",
			args: args{laterTime, baseTime, searchfilter.OperatorGreaterThanOrEqual},
			want: true,
		},
		{
			name: "lt - docTime before condTime",
			args: args{earlierTime, baseTime, searchfilter.OperatorLessThan},
			want: true,
		},
		{
			name: "lt - docTime after condTime",
			args: args{laterTime, baseTime, searchfilter.OperatorLessThan},
			want: false,
		},
		{
			name: "lte - times equal",
			args: args{baseTime, baseTime, searchfilter.OperatorLessThanOrEqual},
			want: true,
		},
		{
			name: "lte - docTime before condTime",
			args: args{earlierTime, baseTime, searchfilter.OperatorLessThanOrEqual},
			want: true,
		},

		{
			name: "invalid docValue type",
			args: args{"not-a-time", baseTime, searchfilter.OperatorGreaterThan},
			want: false,
		},
		{
			name: "invalid condValue type",
			args: args{baseTime, "not-a-time", searchfilter.OperatorGreaterThan},
			want: false,
		},
		{
			name: "both values invalid type",
			args: args{123, 456, searchfilter.OperatorGreaterThan},
			want: false,
		},

		{
			name: "zero time comparison",
			args: args{time.Time{}, time.Time{}, searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "nil values",
			args: args{nil, nil, searchfilter.OperatorGreaterThan},
			want: false,
		},

		{
			name: "unsupported operator",
			args: args{baseTime, baseTime, "invalid-operator"},
			want: false,
		},
		{
			name: "empty operator",
			args: args{baseTime, baseTime, ""},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			defer func() {
				if r := recover(); r != nil {
					debug.PrintStack()
				}
			}()

			got := compareTime(tt.args.docValue, tt.args.condValue, tt.args.operator)
			require.Equal(t, tt.want, got, "compareTime() result mismatch")

			if strings.Contains(tt.name, "invalid") || strings.Contains(tt.name, "unsupported") {

			}
		})
	}
}

func Test_compareString(t *testing.T) {
	type args struct {
		docValue  any
		condValue any
		operator  string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{

		{
			name: "both values are not strings",
			args: args{docValue: 123, condValue: 456, operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "docValue is not string",
			args: args{docValue: 123, condValue: "test", operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "condValue is not string",
			args: args{docValue: "test", condValue: 123, operator: searchfilter.OperatorEqual},
			want: false,
		},

		{
			name: "eq operator with equal values",
			args: args{docValue: "apple", condValue: "apple", operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "eq operator with different values",
			args: args{docValue: "apple", condValue: "banana", operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "ne operator with different values",
			args: args{docValue: "apple", condValue: "banana", operator: searchfilter.OperatorNotEqual},
			want: true,
		},
		{
			name: "ne operator with equal values",
			args: args{docValue: "apple", condValue: "apple", operator: searchfilter.OperatorNotEqual},
			want: false,
		},

		{
			name: "gt operator true case",
			args: args{docValue: "delta", condValue: "alpha", operator: searchfilter.OperatorGreaterThan},
			want: true,
		},
		{
			name: "gt operator false case",
			args: args{docValue: "alpha", condValue: "delta", operator: searchfilter.OperatorGreaterThan},
			want: false,
		},
		{
			name: "gte operator equal case",
			args: args{docValue: "alpha", condValue: "alpha", operator: searchfilter.OperatorGreaterThanOrEqual},
			want: true,
		},
		{
			name: "gte operator greater case",
			args: args{docValue: "delta", condValue: "alpha", operator: searchfilter.OperatorGreaterThanOrEqual},
			want: true,
		},
		{
			name: "lt operator true case",
			args: args{docValue: "alpha", condValue: "delta", operator: searchfilter.OperatorLessThan},
			want: true,
		},
		{
			name: "lt operator false case",
			args: args{docValue: "delta", condValue: "alpha", operator: searchfilter.OperatorLessThan},
			want: false,
		},
		{
			name: "lte operator equal case",
			args: args{docValue: "alpha", condValue: "alpha", operator: searchfilter.OperatorLessThanOrEqual},
			want: true,
		},
		{
			name: "lte operator less case",
			args: args{docValue: "alpha", condValue: "delta", operator: searchfilter.OperatorLessThanOrEqual},
			want: true,
		},

		{
			name: "empty strings with eq",
			args: args{docValue: "", condValue: "", operator: searchfilter.OperatorEqual},
			want: true,
		},
		{
			name: "empty vs non-empty with eq",
			args: args{docValue: "", condValue: "a", operator: searchfilter.OperatorEqual},
			want: false,
		},
		{
			name: "case sensitivity check",
			args: args{docValue: "Apple", condValue: "apple", operator: searchfilter.OperatorEqual},
			want: false,
		},

		{
			name: "invalid operator",
			args: args{docValue: "a", condValue: "b", operator: "invalid_operator"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareString(tt.args.docValue, tt.args.condValue, tt.args.operator); got != tt.want {
				t.Errorf("compareString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_inmemoryConverter_buildComparisonCondition(t *testing.T) {
	now := time.Now()
	testTime := now.Add(1 * time.Hour)

	tests := []struct {
		name        string
		cond        *searchfilter.UniversalFilterCondition
		wantErr     bool
		testDoc     *document.Document
		expectMatch bool
	}{

		{
			name: "invalid field",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "invalid_field",
				Operator: searchfilter.OperatorEqual,
				Value:    "value",
			},
			wantErr: true,
		},

		{
			name: "string equal match",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    "test_doc",
			},
			testDoc: &document.Document{
				Name: "test_doc",
			},
			expectMatch: true,
		},
		{
			name: "string not equal",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "content",
				Operator: searchfilter.OperatorNotEqual,
				Value:    "wrong_content",
			},
			testDoc: &document.Document{
				Content: "correct_content",
			},
			expectMatch: true,
		},

		{
			name: "number greater than",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.count",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    5.0,
			},
			testDoc: &document.Document{
				Metadata: map[string]any{"count": 10},
			},
			expectMatch: true,
		},

		{
			name: "time after",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "created_at",
				Operator: searchfilter.OperatorGreaterThan,
				Value:    now,
			},
			testDoc: &document.Document{
				CreatedAt: testTime,
			},
			expectMatch: true,
		},

		{
			name: "bool equal",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.active",
				Operator: searchfilter.OperatorEqual,
				Value:    true,
			},
			testDoc: &document.Document{
				Metadata: map[string]any{"active": true},
			},
			expectMatch: true,
		},

		{
			name: "metadata nested field",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.tags.category",
				Operator: searchfilter.OperatorEqual,
				Value:    "tech",
			},
			testDoc: &document.Document{
				Metadata: map[string]any{
					"tags": map[string]any{"category": "tech"},
				},
			},
			expectMatch: false,
		},

		{
			name: "type mismatch string",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorEqual,
				Value:    123,
			},
			wantErr:     false,
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &inmemoryConverter{}
			condFunc, err := c.buildComparisonCondition(tt.cond)

			if (err != nil) != tt.wantErr {
				t.Errorf("buildComparisonCondition() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if condFunc == nil {
				t.Fatal("comparison function is nil")
			}

			result := condFunc(tt.testDoc)
			if result != tt.expectMatch {
				t.Errorf("comparison result = %v, want %v", result, tt.expectMatch)
			}
		})
	}
}

func Test_valueTypeDetection(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"string type", "test", valueTypeString},
		{"int type", 42, valueTypeNumber},
		{"float type", 3.14, valueTypeNumber},
		{"time type", time.Now(), valueTypeTime},
		{"bool type", true, valueTypeBool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := valueType(tt.input); got != tt.expected {
				t.Errorf("valueType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func Test_fieldValueRetrieval(t *testing.T) {
	now := time.Now()
	doc := &document.Document{
		ID:        "doc1",
		Name:      "Test Document",
		Content:   "Test content",
		CreatedAt: now,
		Metadata: map[string]any{
			"count": 5,
			"tags":  map[string]any{"category": "tech"},
		},
	}

	tests := []struct {
		name     string
		field    string
		expected any
	}{
		{"id field", "id", "doc1"},
		{"name field", "name", "Test Document"},
		{"content field", "content", "Test content"},
		{"created_at field", "created_at", now},
		{"metadata field", "metadata.count", 5},
		{"nested metadata", "metadata.tags.category", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, _ := fieldValue(doc, tt.field)
			if !reflect.DeepEqual(val, tt.expected) {
				t.Errorf("fieldValue() = %v, want %v", val, tt.expected)
			}
		})
	}
}

func Test_inmemoryConverter_convertCondition(t *testing.T) {
	now := time.Now()
	validTime := now.Add(-24 * time.Hour)

	tests := []struct {
		name    string
		cond    *searchfilter.UniversalFilterCondition
		wantErr bool
	}{

		{
			name: "AND operator with valid conditions",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{Operator: searchfilter.OperatorEqual, Field: "id", Value: "doc1"},
					{Operator: searchfilter.OperatorIn, Field: "metadata.tags", Value: []string{"tag1"}},
				},
			},
			wantErr: false,
		},
		{
			name: "EQ operator with valid field (id)",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "id",
				Value:    "doc1",
			},
			wantErr: false,
		},
		{
			name: "IN operator with valid slice",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "metadata.tags",
				Value:    []string{"tag1", "tag2"},
			},
			wantErr: false,
		},
		{
			name: "GT operator with time comparison",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorGreaterThan,
				Field:    "created_at",
				Value:    validTime,
			},
			wantErr: false,
		},

		{
			name: "AND operator with invalid value type",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value:    "invalid_value",
			},
			wantErr: true,
		},
		{
			name: "EQ operator with invalid field",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "invalid_field",
				Value:    "value",
			},
			wantErr: true,
		},
		{
			name: "IN operator with non-slice value",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "id",
				Value:    "not_a_slice",
			},
			wantErr: true,
		},
		{
			name: "IN operator with empty slice",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "id",
				Value:    []string{},
			},
			wantErr: true,
		},
		{
			name: "Unsupported operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: "unknown_operator",
			},
			wantErr: true,
		},
		{
			name: "nil value",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value:    nil,
			},
			wantErr: true,
		},
		{
			name: "empty slice",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value:    []*searchfilter.UniversalFilterCondition{},
			},
			wantErr: true,
		},
		{
			name: "nil element slice",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value:    []*searchfilter.UniversalFilterCondition{nil, nil},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &inmemoryConverter{}
			_, err := c.convertCondition(tt.cond)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertCondition() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestComparisonFuncBehavior(t *testing.T) {
	c := &inmemoryConverter{}
	now := time.Now()

	tests := []struct {
		name     string
		cond     *searchfilter.UniversalFilterCondition
		doc      *document.Document
		expected bool
	}{
		{
			name: "EQ operator matches ID",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "id",
				Value:    "doc1",
			},
			doc:      &document.Document{ID: "doc1"},
			expected: true,
		},
		{
			name: "GT operator with time comparison",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorGreaterThan,
				Field:    "created_at",
				Value:    now.Add(-24 * time.Hour),
			},
			doc: &document.Document{
				CreatedAt: now,
			},
			expected: true,
		},
		{
			name: "IN operator matches metadata",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "metadata.tags",
				Value:    []string{"tag1", "tag2"},
			},
			doc: &document.Document{
				Metadata: map[string]any{"tags": "tag1"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, err := c.convertCondition(tt.cond)
			if err != nil {
				t.Fatalf("Failed to create comparison function: %v", err)
			}

			if result := fn(tt.doc); result != tt.expected {
				t.Errorf("Comparison result = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func Test_inmemoryConverter_Convert(t *testing.T) {
	type docCheck struct {
		doc  *document.Document
		want bool
	}
	tests := []struct {
		name      string
		cond      *searchfilter.UniversalFilterCondition
		docChecks []docCheck
		wantErr   bool
		wantNil   bool
	}{
		{
			name:    "nil condition",
			wantErr: true,
			wantNil: true,
		},
		{
			name: "valid logical AND condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value: []*searchfilter.UniversalFilterCondition{
					{Operator: searchfilter.OperatorEqual, Field: "metadata.lang", Value: "en"},
					{Operator: searchfilter.OperatorGreaterThan, Field: "metadata.score", Value: 5},
				},
			},
			docChecks: []docCheck{
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 6}}, want: true},
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 4}}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
		{
			name: "valid logical OR condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorOr,
				Value: []*searchfilter.UniversalFilterCondition{
					{Operator: searchfilter.OperatorEqual, Field: "metadata.lang", Value: "en"},
					{Operator: searchfilter.OperatorGreaterThan, Field: "metadata.score", Value: 5},
				},
			},
			docChecks: []docCheck{
				{doc: &document.Document{Metadata: map[string]any{"lang": "ff", "score": 6}}, want: true},
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 4}}, want: true},
				{doc: &document.Document{Metadata: map[string]any{"lang": "ff", "score": 3}}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
		{
			name: "valid equal operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorEqual,
				Field:    "content",
				Value:    "Sample",
			},
			docChecks: []docCheck{
				{doc: &document.Document{Content: "Sample"}, want: true},
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 0.4}}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
		{
			name: "valid between operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "metadata.score",
				Value:    []any{3, 5},
			},
			docChecks: []docCheck{
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 4}}, want: true},
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 2}}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
		{
			name: "invalid between operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "metadata.score",
				Value:    nil,
			},
			docChecks: []docCheck{
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 4}}, want: false},
				{doc: &document.Document{Metadata: map[string]any{"lang": "en", "score": 2}}, want: false},
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "valid in operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "content",
				Value:    []any{"Sample", "fr"},
			},
			docChecks: []docCheck{
				{doc: &document.Document{Content: "Sample"}, want: true},
				{doc: &document.Document{Content: "Test"}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
		{
			name: "unsupported operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: "invalid",
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "invalid value type for AND operator",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorAnd,
				Value:    "invalid",
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "nil value in condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "metadata.score",
				Value:    nil,
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "empty value in condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "metadata.score",
				Value:    []any{},
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "nil element value in condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorIn,
				Field:    "content",
				Value:    []any{nil, nil},
			},
			docChecks: []docCheck{
				{doc: &document.Document{Content: "Sample"}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
		{
			name: "nil value between condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "metadata.score",
				Value:    nil,
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "empty value between condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "metadata.score",
				Value:    []any{},
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "nil element value between condition",
			cond: &searchfilter.UniversalFilterCondition{
				Operator: searchfilter.OperatorBetween,
				Field:    "content",
				Value:    []any{nil, nil},
			},
			docChecks: []docCheck{
				{doc: &document.Document{Content: "Sample"}, want: false},
			},
			wantErr: false,
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &inmemoryConverter{}
			cf, err := c.Convert(tt.cond)
			if (err != nil) != tt.wantErr {
				t.Errorf("Convert() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if (cf == nil) != tt.wantNil {
				t.Errorf("Convert() got nil = %v, wantNil %v", cf == nil, tt.wantNil)
				return
			}
			if tt.wantNil {
				return
			}

			for _, dc := range tt.docChecks {
				condResult := cf(dc.doc)
				if condResult != dc.want {
					t.Errorf("comparison function for doc %+v = %v, want %v", dc.doc, condResult, dc.want)
				}
			}
		})
	}
}

func TestInmemoryConverter_buildBetweenCondition(t *testing.T) {
	tests := []struct {
		name       string
		cond       *searchfilter.UniversalFilterCondition
		doc        *document.Document
		wantErr    bool
		wantErrMsg string
		wantResult bool
	}{
		{
			name: "between 10 and 20",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{10, 20},
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    false,
			wantResult: true,
		},
		{
			name: "invalid field",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "invalid_field",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{1, 2},
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    true,
			wantErrMsg: `field name only be in`,
		},
		{
			name: "not a slice",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    123,
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    true,
			wantErrMsg: "between operator value must be a slice with two elements",
		},
		{
			name: "one element in slice",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    []int{1},
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    true,
			wantErrMsg: "between operator value must be a slice with two elements",
		},
		{
			name: "invalid type in slice",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    []string{"a", "b"},
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    false,
			wantResult: false,
		},
		{
			name: "nil value",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    nil,
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    true,
			wantResult: false,
			wantErrMsg: "between operator value must be a slice with two elements",
		},
		{
			name: "empty slice value",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{},
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    true,
			wantResult: false,
			wantErrMsg: "between operator value must be a slice with two elements",
		},
		{
			name: "nil element slice value",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorBetween,
				Value:    []any{nil, nil},
			},
			doc:        &document.Document{Metadata: map[string]any{"id": 15}},
			wantErr:    false,
			wantResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := &inmemoryConverter{}

			gotFunc, err := mc.buildBetweenCondition(tt.cond)

			if (err != nil) != tt.wantErr {
				t.Errorf("buildBetweenCondition() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if err == nil {
					t.Error("expect error, but is nil")
				} else if tt.wantErrMsg != "" && !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("buildBetweenCondition() err = %v, want contains %v", err.Error(), tt.wantErrMsg)
				}
				return
			}

			if gotFunc(tt.doc) != tt.wantResult {
				t.Errorf("buildBetweenCondition() result = %v, want %v", gotFunc(tt.doc), tt.wantResult)
			}
		})
	}
}

func TestLikePatternToRegex(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"test", "^test$"},
		{"test%", "^test.*$"},
		{"%test", "^.*test$"},
		{"%test%", "^.*test.*$"},
		{"test_", "^test.$"},
		{"_test", "^.test$"},
		{"100%", "^100.*$"},
		{"100%_complete", "^100.*.complete$"},
		{"special.chars", "^special\\.chars$"},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := likePatternToRegex(tt.pattern)
			if got != tt.want {
				t.Errorf("likePatternToRegex(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestBuildLikeCondition(t *testing.T) {
	converter := &inmemoryConverter{}

	tests := []struct {
		name        string
		cond        *searchfilter.UniversalFilterCondition
		doc         *document.Document
		wantErr     bool
		errContains string
		expected    bool
	}{
		{
			name: "invalid field",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "invalid_field",
				Operator: searchfilter.OperatorLike,
				Value:    "test%",
			},
			wantErr:     true,
			errContains: "field name only be in",
		},
		{
			name: "value is not string",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    123,
			},
			wantErr:     true,
			errContains: "like operator requires a string pattern",
		},
		{
			name: "like pattern is just %",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    "John%",
			},
			doc: &document.Document{
				Name: "John Doe",
			},
			wantErr:  false,
			expected: true,
		},
		{
			name: "like pattern is just % in middle",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    "J%n%",
			},
			doc: &document.Document{
				Name: "John Doe",
			},
			wantErr:  false,
			expected: true,
		},
		{
			name: "like pattern",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    "J_hn",
			},
			doc: &document.Document{
				Name: "John",
			},
			wantErr:  false,
			expected: true,
		},
		{
			name: "like not pattern",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorLike,
				Value:    "Jane%",
			},
			doc: &document.Document{
				Name: "John Doe",
			},
			wantErr:  false,
			expected: false,
		},
		{
			name: "like empty string",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "content",
				Operator: searchfilter.OperatorLike,
				Value:    "",
			},
			doc: &document.Document{
				Content: "",
			},
			wantErr:  false,
			expected: true,
		},

		{
			name: "not like pattern",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorNotLike,
				Value:    "John%",
			},
			doc: &document.Document{
				Name: "John Doe",
			},
			wantErr:  false,
			expected: false,
		},
		{
			name: "not like pattern matches",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "name",
				Operator: searchfilter.OperatorNotLike,
				Value:    "Jane%",
			},
			doc: &document.Document{
				Name: "John Doe",
			},
			wantErr:  false,
			expected: true,
		},
		{
			name: "field not exist",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "nonexistent_field",
				Operator: searchfilter.OperatorLike,
				Value:    "test",
			},
			doc: &document.Document{
				Name: "test",
			},
			wantErr:     true,
			errContains: "field name only be in",
		},
		{
			name: "like pattern with integer field value",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "metadata.id",
				Operator: searchfilter.OperatorLike,
				Value:    "123%",
			},
			doc: &document.Document{
				Metadata: map[string]any{
					"id": 123,
				},
			},
			wantErr:  false,
			expected: false,
		},
		{
			name: "like pattern with string field value containing special regex chars",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "content",
				Operator: searchfilter.OperatorLike,
				Value:    "100%_complete",
			},
			doc: &document.Document{
				Content: "100%_complete",
			},
			wantErr:  false,
			expected: true,
		},
		{
			name: "nil value",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "content",
				Operator: searchfilter.OperatorLike,
				Value:    nil,
			},
			doc: &document.Document{
				Content: "100%_complete",
			},
			wantErr: true,
		},
		{
			name: "empty value",
			cond: &searchfilter.UniversalFilterCondition{
				Field:    "content",
				Operator: searchfilter.OperatorLike,
				Value:    "",
			},
			doc: &document.Document{
				Content: "100%_complete",
			},
			wantErr:  false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			condFunc, err := converter.buildLikeCondition(tt.cond)
			if tt.wantErr {
				if err == nil {
					t.Errorf("buildLikeCondition() expect error, but is nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("buildLikeCondition() err = %v, want contains = %v", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("buildLikeCondition() err = %v", err)
				return
			}

			if condFunc == nil {
				t.Error("buildLikeCondition() condFunc is nil")
				return
			}

			result := condFunc(tt.doc)
			if result != tt.expected {
				t.Errorf("result = %v, expect = %v, test data: field=%v, operator=%v, cond value=%v, doc=%v",
					result, tt.expected, tt.cond.Field, tt.cond.Operator, tt.cond.Value, tt.doc)
			}
		})
	}
}
