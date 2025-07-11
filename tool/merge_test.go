//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package tool

import (
	"reflect"
	"testing"
)

// Test structs for struct concatenation tests
type TestPerson struct {
	Name   string
	Age    int
	Height float64
	Active bool
	Tags   []string
	Scores []int
	Meta   map[string]string
}

type TestAddress struct {
	Street string
	Number int
}

type TestPersonWithAddress struct {
	Name    string
	Age     int
	Address TestAddress
	Tags    []string
}

// Custom type implementing Mergeable interface
type MergeableString struct {
	Value string
}

func (ms MergeableString) Merge(other any) any {
	if otherMS, ok := other.(MergeableString); ok {
		return MergeableString{Value: ms.Value + "|" + otherMS.Value}
	}
	return ms
}

func TestMerge_EmptySlice(t *testing.T) {
	// Test empty slice returns zero value
	result := Merge([]string{})
	if result != "" {
		t.Errorf("Expected empty string, got %v", result)
	}

	result2 := Merge([]int{})
	if result2 != 0 {
		t.Errorf("Expected 0, got %v", result2)
	}
}

func TestMerge_SingleElement(t *testing.T) {
	// Test single element returns the element itself
	result := Merge([]string{"hello"})
	if result != "hello" {
		t.Errorf("Expected 'hello', got %v", result)
	}

	result2 := Merge([]int{42})
	if result2 != 42 {
		t.Errorf("Expected 42, got %v", result2)
	}
}

func TestMerge_Strings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "simple concatenation",
			input:    []string{"hello", " ", "world"},
			expected: "hello world",
		},
		{
			name:     "empty strings",
			input:    []string{"", "test", ""},
			expected: "test",
		},
		{
			name:     "all empty",
			input:    []string{"", "", ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Merge(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestMerge_Integers(t *testing.T) {
	tests := []struct {
		name     string
		input    []int
		expected int
	}{
		{
			name:     "positive numbers",
			input:    []int{1, 2, 3, 4},
			expected: 10,
		},
		{
			name:     "with zero",
			input:    []int{5, 0, 3},
			expected: 8,
		},
		{
			name:     "negative numbers",
			input:    []int{-1, -2, 3},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Merge(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func TestMerge_IntegerTypes(t *testing.T) {
	// Test different integer types
	result8 := Merge([]int8{1, 2, 3})
	if result8 != 6 {
		t.Errorf("int8: Expected 6, got %d", result8)
	}

	result16 := Merge([]int16{10, 20, 30})
	if result16 != 60 {
		t.Errorf("int16: Expected 60, got %d", result16)
	}

	result32 := Merge([]int32{100, 200})
	if result32 != 300 {
		t.Errorf("int32: Expected 300, got %d", result32)
	}

	result64 := Merge([]int64{1000, 2000})
	if result64 != 3000 {
		t.Errorf("int64: Expected 3000, got %d", result64)
	}
}

func TestMerge_UnsignedIntegers(t *testing.T) {
	resultUint := Merge([]uint{1, 2, 3})
	if resultUint != 6 {
		t.Errorf("uint: Expected 6, got %d", resultUint)
	}

	resultUint8 := Merge([]uint8{10, 20})
	if resultUint8 != 30 {
		t.Errorf("uint8: Expected 30, got %d", resultUint8)
	}

	resultUint16 := Merge([]uint16{100, 200})
	if resultUint16 != 300 {
		t.Errorf("uint16: Expected 300, got %d", resultUint16)
	}

	resultUint32 := Merge([]uint32{1000, 2000})
	if resultUint32 != 3000 {
		t.Errorf("uint32: Expected 3000, got %d", resultUint32)
	}

	resultUint64 := Merge([]uint64{10000, 20000})
	if resultUint64 != 30000 {
		t.Errorf("uint64: Expected 30000, got %d", resultUint64)
	}
}

func TestMerge_Floats(t *testing.T) {
	result32 := Merge([]float32{1.5, 2.5, 3.0})
	expected32 := float32(7.0)
	if result32 != expected32 {
		t.Errorf("float32: Expected %f, got %f", expected32, result32)
	}

	result64 := Merge([]float64{1.1, 2.2, 3.3})
	expected64 := 6.6
	if abs64(result64-expected64) > 1e-10 {
		t.Errorf("float64: Expected %f, got %f", expected64, result64)
	}
}

func TestMerge_Slices(t *testing.T) {
	// String slices
	stringSlices := [][]string{
		{"a", "b"},
		{"c", "d"},
		{"e"},
	}
	resultStrings := Merge(stringSlices)
	expectedStrings := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(resultStrings, expectedStrings) {
		t.Errorf("String slices: Expected %v, got %v", expectedStrings, resultStrings)
	}

	// Integer slices
	intSlices := [][]int{
		{1, 2},
		{3, 4, 5},
		{6},
	}
	resultInts := Merge(intSlices)
	expectedInts := []int{1, 2, 3, 4, 5, 6}
	if !reflect.DeepEqual(resultInts, expectedInts) {
		t.Errorf("Int slices: Expected %v, got %v", expectedInts, resultInts)
	}

	// Empty slices
	emptySlices := [][]string{
		{},
		{"test"},
		{},
	}
	resultEmpty := Merge(emptySlices)
	expectedEmpty := []string{"test"}
	if !reflect.DeepEqual(resultEmpty, expectedEmpty) {
		t.Errorf("Empty slices: Expected %v, got %v", expectedEmpty, resultEmpty)
	}
}

func TestMerge_ByteSlices(t *testing.T) {
	byteSlices := [][]byte{
		{1, 2, 3},
		{4, 5},
		{6, 7, 8, 9},
	}
	result := Merge(byteSlices)
	expected := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Byte slices: Expected %v, got %v", expected, result)
	}
}

func TestMerge_Arrays(t *testing.T) {
	arrays := [][3]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
	}
	result := Merge(arrays)
	expected := [3]int{1, 2, 3}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Arrays: Expected %v, got %v", expected, result)
	}
}

func TestMerge_Maps(t *testing.T) {
	maps := []map[string]int{
		{"a": 1, "b": 2},
		{"c": 3, "b": 20}, // "b" should be overwritten
		{"d": 4},
	}
	result := Merge(maps)
	expected := map[string]int{
		"a": 1,
		"b": 20, // Last value wins
		"c": 3,
		"d": 4,
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("Maps: Expected %v, got %v", expected, result)
	}
}

func TestMerge_Structs_Simple(t *testing.T) {
	persons := []TestPerson{
		{
			Name:   "Alice",
			Age:    25,
			Height: 5.5,
			Active: true,
			Tags:   []string{"dev", "go"},
			Scores: []int{90, 85},
			Meta:   map[string]string{"dept": "eng", "level": "senior"},
		},
		{
			Name:   " Bob",
			Age:    30,
			Height: 1.5,
			Active: false,
			Tags:   []string{"mgr", "python"},
			Scores: []int{95, 88},
			Meta:   map[string]string{"dept": "product", "exp": "5years"},
		},
	}

	result := Merge(persons)

	// Check concatenated/summed fields
	if result.Name != "Alice Bob" {
		t.Errorf("Name: Expected 'Alice Bob', got %q", result.Name)
	}
	if result.Age != 55 {
		t.Errorf("Age: Expected 55, got %d", result.Age)
	}
	if abs64(result.Height-7.0) > 1e-10 {
		t.Errorf("Height: Expected 7.0, got %f", result.Height)
	}

	expectedTags := []string{"dev", "go", "mgr", "python"}
	if !reflect.DeepEqual(result.Tags, expectedTags) {
		t.Errorf("Tags: Expected %v, got %v", expectedTags, result.Tags)
	}

	expectedScores := []int{90, 85, 95, 88}
	if !reflect.DeepEqual(result.Scores, expectedScores) {
		t.Errorf("Scores: Expected %v, got %v", expectedScores, result.Scores)
	}

	expectedMeta := map[string]string{
		"dept":  "product", // Last value wins
		"level": "senior",
		"exp":   "5years",
	}
	if !reflect.DeepEqual(result.Meta, expectedMeta) {
		t.Errorf("Meta: Expected %v, got %v", expectedMeta, result.Meta)
	}
}

func TestMerge_Structs_Nested(t *testing.T) {
	persons := []TestPersonWithAddress{
		{
			Name: "John",
			Age:  28,
			Address: TestAddress{
				Street: "Main",
				Number: 123,
			},
			Tags: []string{"frontend"},
		},
		{
			Name: " Jane",
			Age:  32,
			Address: TestAddress{
				Street: " St",
				Number: 456,
			},
			Tags: []string{"backend"},
		},
	}

	result := Merge(persons)

	if result.Name != "John Jane" {
		t.Errorf("Name: Expected 'John Jane', got %q", result.Name)
	}
	if result.Age != 60 {
		t.Errorf("Age: Expected 60, got %d", result.Age)
	}
	if result.Address.Street != "Main St" {
		t.Errorf("Address.Street: Expected 'Main St', got %q", result.Address.Street)
	}
	if result.Address.Number != 579 {
		t.Errorf("Address.Number: Expected 579, got %d", result.Address.Number)
	}

	expectedTags := []string{"frontend", "backend"}
	if !reflect.DeepEqual(result.Tags, expectedTags) {
		t.Errorf("Tags: Expected %v, got %v", expectedTags, result.Tags)
	}
}

func TestMerge_MergeableInterface(t *testing.T) {
	mergeables := []MergeableString{
		{Value: "first"},
		{Value: "second"},
		{Value: "third"},
	}

	result := Merge(mergeables)
	expected := MergeableString{Value: "first|second|third"}

	if result.Value != expected.Value {
		t.Errorf("Mergeable: Expected %q, got %q", expected.Value, result.Value)
	}
}

func TestMerge_UnsupportedType(t *testing.T) {
	// Test with unsupported type (should return first element)
	type UnsupportedType struct {
		Data complex64 // Make it exported to avoid reflection issues
	}

	unsupported := []UnsupportedType{
		{Data: complex64(complex(1, 2))},
		{Data: complex64(complex(3, 4))},
	}

	result := Merge(unsupported)
	// For struct fields with unsupported types, the struct merging algorithm
	// will recursively call Merge on the field values
	// Since complex64 is an unsupported type, Merge returns the first element
	expected := complex64(complex(1, 2)) // First element for unsupported types

	if result.Data != expected {
		t.Errorf("Unsupported type: Expected %v, got %v", expected, result.Data)
	}
}

func TestMerge_BooleanFields(t *testing.T) {
	// Test boolean fields in structs (should use fallback behavior)
	type TestBool struct {
		Flag bool
	}

	bools := []TestBool{
		{Flag: true},
		{Flag: false},
	}

	result := Merge(bools)
	// For unsupported types like bool in struct fields, Merge returns the first value
	if result.Flag != true {
		t.Errorf("Boolean field: Expected true (first value for unsupported type), got %v", result.Flag)
	}
}

func TestMerge_NilHandling(t *testing.T) {
	// Test with nil slices in struct fields
	type TestWithNilSlice struct {
		Items []string
	}

	structs := []TestWithNilSlice{
		{Items: []string{"a", "b"}},
		{Items: nil},
		{Items: []string{"c"}},
	}

	result := Merge(structs)
	expected := []string{"a", "b", "c"}
	if !reflect.DeepEqual(result.Items, expected) {
		t.Errorf("Nil slice handling: Expected %v, got %v", expected, result.Items)
	}
}

// Helper function for floating point comparison
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// Benchmark tests
func BenchmarkMerge_Strings(b *testing.B) {
	strings := []string{"hello", " ", "world", " ", "benchmark"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Merge(strings)
	}
}

func BenchmarkMerge_Integers(b *testing.B) {
	ints := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Merge(ints)
	}
}

func BenchmarkMerge_Slices(b *testing.B) {
	slices := [][]int{
		{1, 2, 3},
		{4, 5, 6},
		{7, 8, 9},
		{10, 11, 12},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Merge(slices)
	}
}

func BenchmarkMerge_Structs(b *testing.B) {
	structs := []TestPerson{
		{Name: "Alice", Age: 25, Height: 5.5, Tags: []string{"dev"}},
		{Name: " Bob", Age: 30, Height: 6.0, Tags: []string{"mgr"}},
		{Name: " Charlie", Age: 35, Height: 5.8, Tags: []string{"arch"}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Merge(structs)
	}
}
