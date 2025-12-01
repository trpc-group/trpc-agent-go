//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides internal utilities
// management in the trpc-agent-go framework.
package util

import (
	"math"
	"testing"
)

type Person struct {
	Name string
	Age  int
}

func TestGetMapValue(t *testing.T) {
	tests := []struct {
		name        string
		m           map[string]any
		key         string
		wantType    string
		wantVal     any
		wantOk      bool
		description string
	}{
		{
			name:        "string value exists",
			m:           map[string]any{"name": "Alice", "age": 30},
			key:         "name",
			wantType:    "string",
			wantVal:     "Alice",
			wantOk:      true,
			description: "string value exists",
		},
		{
			name:        "int value exists",
			m:           map[string]any{"name": "Alice", "age": 30},
			key:         "age",
			wantType:    "int",
			wantVal:     30,
			wantOk:      true,
			description: "int value exists",
		},
		{
			name:        "bool value exists",
			m:           map[string]any{"active": true, "name": "Alice"},
			key:         "active",
			wantType:    "bool",
			wantVal:     true,
			wantOk:      true,
			description: "bool value exists",
		},
		{
			name:        "float value exists",
			m:           map[string]any{"score": 95.5, "name": "Alice"},
			key:         "score",
			wantType:    "float64",
			wantVal:     95.5,
			wantOk:      true,
			description: "float value exists",
		},
		{
			name:        "slice value exists",
			m:           map[string]any{"tags": []string{"a", "b"}, "name": "Alice"},
			key:         "tags",
			wantType:    "[]string",
			wantVal:     []string{"a", "b"},
			wantOk:      true,
			description: "slice value exists",
		},
		{
			name:        "map value exists",
			m:           map[string]any{"metadata": map[string]any{"role": "admin"}, "name": "Alice"},
			key:         "metadata",
			wantType:    "map[string]any",
			wantVal:     map[string]any{"role": "admin"},
			wantOk:      true,
			description: "map value exists",
		},
		{
			name:        "struct value exists",
			m:           map[string]any{"person": Person{Name: "Alice", Age: 30}},
			key:         "person",
			wantType:    "Person",
			wantVal:     Person{Name: "Alice", Age: 30},
			wantOk:      true,
			description: "struct value exists",
		},

		{
			name:        "key does not exist",
			m:           map[string]any{"name": "Alice", "age": 30},
			key:         "nonexistent",
			wantType:    "string",
			wantVal:     "",
			wantOk:      false,
			description: "key does not exist",
		},
		{
			name:        "nil map",
			m:           nil,
			key:         "anykey",
			wantType:    "string",
			wantVal:     "",
			wantOk:      false,
			description: "nil map",
		},
		{
			name:        "empty map",
			m:           map[string]any{},
			key:         "anykey",
			wantType:    "string",
			wantVal:     "",
			wantOk:      false,
			description: "empty map",
		},
		{
			name:        "nil value",
			m:           map[string]any{"nilval": nil, "name": "Alice"},
			key:         "nilval",
			wantType:    "string",
			wantVal:     "",
			wantOk:      false,
			description: "nil值",
		},

		{
			name:        "type mismatch: string to int",
			m:           map[string]any{"age": "30"},
			key:         "age",
			wantType:    "int",
			wantVal:     0,
			wantOk:      false,
			description: "type mismatch: string to int",
		},
		{
			name:        "type mismatch: int to string",
			m:           map[string]any{"name": 123},
			key:         "name",
			wantType:    "string",
			wantVal:     "",
			wantOk:      false,
			description: "type mismatch: int to string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("测试: %s", tt.description)

			switch tt.wantType {
			case "string":
				gotVal, gotOk := GetMapValue[string, string](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk && gotVal != tt.wantVal {
					t.Errorf("GetMapValue() val = %v, want %v", gotVal, tt.wantVal)
				}

			case "int":
				gotVal, gotOk := GetMapValue[string, int](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk && gotVal != tt.wantVal {
					t.Errorf("GetMapValue() val = %v, want %v", gotVal, tt.wantVal)
				}

			case "bool":
				gotVal, gotOk := GetMapValue[string, bool](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk && gotVal != tt.wantVal {
					t.Errorf("GetMapValue() val = %v, want %v", gotVal, tt.wantVal)
				}

			case "float64":
				gotVal, gotOk := GetMapValue[string, float64](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk {
					wantVal := tt.wantVal.(float64)
					if math.Abs(gotVal-wantVal) > 1e-9 {
						t.Errorf("GetMapValue() val = %v, want %v", gotVal, wantVal)
					}
				}

			case "[]string":
				gotVal, gotOk := GetMapValue[string, []string](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk {
					wantSlice := tt.wantVal.([]string)
					if len(gotVal) != len(wantSlice) {
						t.Errorf("GetMapValue() slice length = %v, want %v", len(gotVal), len(wantSlice))
					} else {
						for i, v := range gotVal {
							if v != wantSlice[i] {
								t.Errorf("GetMapValue() slice[%d] = %v, want %v", i, v, wantSlice[i])
							}
						}
					}
				}

			case "map[string]any":
				gotVal, gotOk := GetMapValue[string, map[string]any](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk {
					wantMap := tt.wantVal.(map[string]any)
					if len(gotVal) != len(wantMap) {
						t.Errorf("GetMapValue() map length = %v, want %v", len(gotVal), len(wantMap))
					} else {
						for k, v := range gotVal {
							if wantV, exists := wantMap[k]; !exists || v != wantV {
								t.Errorf("GetMapValue() map[%s] = %v, want %v", k, v, wantV)
							}
						}
					}
				}

			case "Person":
				gotVal, gotOk := GetMapValue[string, Person](tt.m, tt.key)
				if gotOk != tt.wantOk {
					t.Errorf("GetMapValue() ok = %v, want %v", gotOk, tt.wantOk)
				}
				if tt.wantOk {
					wantPerson := tt.wantVal.(Person)
					if gotVal != wantPerson {
						t.Errorf("GetMapValue() val = %+v, want %+v", gotVal, wantPerson)
					}
				}
			}
		})
	}
}

func TestGetMapValue_IntKey(t *testing.T) {
	m := map[int]any{1: "one", 2: "two", 3: 3}

	val, ok := GetMapValue[int, string](m, 1)
	if !ok || val != "one" {
		t.Errorf("GetMapValue() with int key failed: got (%v, %v), want (one, true)", val, ok)
	}

	val2, ok2 := GetMapValue[int, string](m, 3)
	if ok2 {
		t.Errorf("GetMapValue() should fail with type mismatch: got (%v, %v), want (, false)", val2, ok2)
	}

	val3, ok3 := GetMapValue[int, string](m, 99)
	if ok3 {
		t.Errorf("GetMapValue() with non-existent key should fail: got (%v, %v), want (, false)", val3, ok3)
	}
}

func TestGetMapValue_NilHandling(t *testing.T) {
	m := map[string]any{"nil_val": nil, "str_val": "hello"}

	val, ok := GetMapValue[string, string](m, "nil_val")
	if ok || val != "" {
		t.Errorf("GetMapValue() with nil value should return (zero, false): got (%v, %v)", val, ok)
	}

	val2, ok2 := GetMapValue[string, string](m, "str_val")
	if !ok2 || val2 != "hello" {
		t.Errorf("GetMapValue() with normal value failed: got (%v, %v), want (hello, true)", val2, ok2)
	}
}
