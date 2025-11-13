//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"reflect"
	"testing"
	"time"
)

type testStruct struct {
	Name  string
	Value int
	Data  []string
}

type cyclicStruct struct {
	Self     *cyclicStruct
	Name     string
	Time     time.Time
	TimePtr  *time.Time
	DTimePtr *DataTime
	DTime    DataTime
}

type DataTime time.Time

func newDataTime() DataTime {
	t := time.Now()
	return (DataTime)(t)
}

func TestDeepCopyAny(t *testing.T) {
	now := time.Now()
	dTime := newDataTime()
	cyclic1 := &cyclicStruct{
		Name:     "A",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic1.Self = cyclic1

	cyclic2 := cyclicStruct{
		Name:     "B",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic3 := &cyclicStruct{
		Name:     "C",
		Time:     now,
		TimePtr:  &now,
		DTime:    dTime,
		DTimePtr: &dTime,
	}
	cyclic2.Self = cyclic3
	cyclic3.Self = &cyclic2

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{
			name:  "nil value",
			input: nil,
			want:  nil,
		},
		{
			name:  "string",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "int",
			input: 42,
			want:  42,
		},
		{
			name:  "float64",
			input: 3.14,
			want:  3.14,
		},
		{
			name:  "bool",
			input: true,
			want:  true,
		},

		{
			name:  "[]string",
			input: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "[]int",
			input: []int{1, 2, 3},
			want:  []int{1, 2, 3},
		},
		{
			name:  "[]any with mixed types",
			input: []any{"a", 1, 3.14, true},
			want:  []any{"a", 1, 3.14, true},
		},

		{
			name: "map[string]any",
			input: map[string]any{
				"name": "test",
				"age":  25,
				"tags": []string{"a", "b"},
			},
			want: map[string]any{
				"name": "test",
				"age":  25,
				"tags": []string{"a", "b"},
			},
		},

		{
			name: "testStruct",
			input: testStruct{
				Name:  "test",
				Value: 100,
				Data:  []string{"x", "y", "z"},
			},
			want: testStruct{
				Name:  "test",
				Value: 100,
				Data:  []string{"x", "y", "z"},
			},
		},

		{
			name: "*testStruct",
			input: &testStruct{
				Name:  "pointer_test",
				Value: 200,
				Data:  []string{"p", "q", "r"},
			},
			want: &testStruct{
				Name:  "pointer_test",
				Value: 200,
				Data:  []string{"p", "q", "r"},
			},
		},

		{
			name:  "time.Time",
			input: now,
			want:  now,
		},
		{
			name:  "*time.Time",
			input: &now,
			want:  &now,
		},

		{
			name:  "cyclic self-reference",
			input: cyclic1,
			want:  cyclic1,
		},
		{
			name:  "cyclic cross-reference",
			input: cyclic2,
			want:  cyclic2,
		},

		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "empty map",
			input: map[string]int{},
			want:  map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copied := deepCopyAny(tt.input)
			if !reflect.DeepEqual(copied, tt.want) {
				t.Errorf("deepCopyAny(%v) = %v, want %v", tt.input, copied, tt.want)
			}
		})
	}
}

func TestDeepCopyUnexportedFields(t *testing.T) {
	type privateStruct struct {
		PublicField  string
		privateField string
	}

	original := privateStruct{
		PublicField:  "public",
		privateField: "private",
	}

	rv := reflect.ValueOf(&original).Elem()
	privateField := rv.FieldByName("privateField")
	if privateField.IsValid() && privateField.CanSet() {
		privateField.SetString("private")
	}

	copied := deepCopyAny(original)

	originalPublic := reflect.ValueOf(original).FieldByName("PublicField").String()
	copiedPublic := reflect.ValueOf(copied).FieldByName("PublicField").String()

	if originalPublic != copiedPublic {
		t.Errorf("Public field not copied correctly: original %s, copied %s",
			originalPublic, copiedPublic)
	}

	copiedPrivate := reflect.ValueOf(copied).FieldByName("privateField")
	if copiedPrivate.String() != "" {
		t.Errorf("Private field should not be copied, but got: %s", copiedPrivate.String())
	}
}

func BenchmarkDeepCopyAny(b *testing.B) {
	complexData := map[string]any{
		"users": []map[string]any{
			{
				"name": "Alice",
				"age":  30,
				"tags": []string{"admin", "user"},
			},
			{
				"name": "Bob",
				"age":  25,
				"tags": []string{"user"},
			},
		},
		"metadata": map[string]any{
			"version": "1.0",
			"config":  []int{1, 2, 3, 4, 5},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deepCopyAny(complexData)
	}
}
