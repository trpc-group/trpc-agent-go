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

func TestCopyTimeType(t *testing.T) {
	now := time.Now()
	dTime := newDataTime()

	tests := []struct {
		name     string
		input    reflect.Value
		validate func(t *testing.T, result any)
	}{
		{
			name:  "time.Time value",
			input: reflect.ValueOf(now),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				if !ok {
					t.Errorf("Expected time.Time, got %T", result)
					return
				}
				if !resultTime.Equal(now) {
					t.Errorf("Times should be equal: original %v, result %v", now, resultTime)
				}
				modified := now.Add(time.Hour)
				if resultTime.Equal(modified) {
					t.Error("Modifying original time affected the result")
				}
			},
		},
		{
			name:  "custom time type (convertible to time.Time)",
			input: reflect.ValueOf(dTime),
			validate: func(t *testing.T, result any) {
				_, ok := result.(DataTime)
				if !ok {
					t.Errorf("Expected MyTime, got %T", result)
					return
				}
				resultTime := time.Time(result.(DataTime))
				if !resultTime.Equal(time.Time(dTime)) {
					t.Errorf("Times should be equal: original %v, result %v", dTime, resultTime)
				}
			},
		},
		{
			name:  "non-time type (string)",
			input: reflect.ValueOf("not a time"),
			validate: func(t *testing.T, result any) {
				resultStr, ok := result.(string)
				if !ok {
					t.Errorf("Expected string, got %T", result)
					return
				}
				if resultStr != "not a time" {
					t.Errorf("Expected 'not a time', got '%s'", resultStr)
				}
			},
		},
		{
			name:  "non-time type (int)",
			input: reflect.ValueOf(42),
			validate: func(t *testing.T, result any) {
				resultInt, ok := result.(int)
				if !ok {
					t.Errorf("Expected int, got %T", result)
					return
				}
				if resultInt != 42 {
					t.Errorf("Expected 42, got %d", resultInt)
				}
			},
		},
		{
			name:  "zero time",
			input: reflect.ValueOf(time.Time{}),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				if !ok {
					t.Errorf("Expected time.Time, got %T", result)
					return
				}
				if !resultTime.IsZero() {
					t.Error("Expected zero time")
				}
			},
		},
		{
			name:  "time with location",
			input: reflect.ValueOf(time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC)),
			validate: func(t *testing.T, result any) {
				resultTime, ok := result.(time.Time)
				if !ok {
					t.Errorf("Expected time.Time, got %T", result)
					return
				}
				expected := time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC)
				if !resultTime.Equal(expected) {
					t.Errorf("Times should be equal: expected %v, result %v", expected, resultTime)
				}
				if resultTime.Location() != time.UTC {
					t.Error("Location should be preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := copyTime(tt.input)
			tt.validate(t, result)
		})
	}
}

type deepCopoerStruct struct {
	Name string
	age  int
}

func (d deepCopoerStruct) DeepCopy() any {
	return deepCopoerStruct{
		Name: d.Name,
		age:  d.age,
	}
}

func TestDeepCopyAny_DeepCopier(t *testing.T) {
	original := &deepCopoerStruct{
		Name: "Alice",
		age:  30,
	}

	copied := deepCopyAny(original).(deepCopoerStruct)

	if !reflect.DeepEqual(copied, *original) {
		t.Errorf("Copied value should be equal to original. Got %v, expected %v", copied, *original)
	}

	tests := map[string]any{
		"simple": deepCopoerStruct{
			Name: "Bob",
			age:  25,
		},
		"list": []deepCopoerStruct{
			{
				Name: "Alice",
				age:  30,
			},
			{
				Name: "Bob",
				age:  25,
			},
		},
	}
	copyMap := deepCopyAny(tests)
	if !reflect.DeepEqual(copyMap, tests) {
		t.Errorf("Copied value should be equal to original")
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
