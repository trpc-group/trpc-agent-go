//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool_test

import (
	"context"
	"reflect"
	"testing"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// --- NamedToolSet and NamedTool tests (migrated) ---

// fakeTool implements tool.CallableTool and tool.StreamableTool for testing.
type fakeTool struct {
	decl       *tool.Declaration
	callResult any
	callErr    error
	stream     *tool.Stream
}

func (f *fakeTool) Declaration() *tool.Declaration                { return f.decl }
func (f *fakeTool) Call(_ context.Context, _ []byte) (any, error) { return f.callResult, f.callErr }
func (f *fakeTool) StreamableCall(_ context.Context, _ []byte) (*tool.StreamReader, error) {
	if f.stream == nil {
		f.stream = tool.NewStream(1)
	}
	return f.stream.Reader, nil
}

// simpleTool implements only tool.Tool (not callable/streamable) for negative paths.
type simpleTool struct{ name, desc string }

func (s *simpleTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name, Description: s.desc}
}

// skipperTool implements tool.Tool and exposes SkipSummarization preference
// to validate NamedTool delegation behavior in tests.
type skipperTool struct {
	name string
	skip bool
}

func (s *skipperTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: s.name}
}

func (s *skipperTool) SkipSummarization() bool { return s.skip }

// fakeToolSet implements tool.ToolSet.
type fakeToolSet struct {
	name   string
	tools  []tool.Tool
	closed bool
}

func (f *fakeToolSet) Tools(context.Context) []tool.Tool { return f.tools }
func (f *fakeToolSet) Close() error                      { f.closed = true; return nil }
func (f *fakeToolSet) Name() string                      { return f.name }

func TestNamedToolSet_Idempotent(t *testing.T) {
	ts := &fakeToolSet{name: "fs"}
	nts := itool.NewNamedToolSet(ts)
	// Calling again with an already wrapped toolset should return the same instance.
	nts2 := itool.NewNamedToolSet(nts)
	if nts != nts2 {
		t.Fatalf("expected idempotent wrapping to return same instance")
	}
}

func TestNamedToolSet_Tools_PrefixingAndPassthrough(t *testing.T) {
	// With a name, tool names should be prefixed.
	base := &fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&simpleTool{name: "read", desc: "read file"}},
	}
	nts := itool.NewNamedToolSet(base)
	got := nts.Tools(context.Background())
	if len(got) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(got))
	}
	if got[0].Declaration().Name != "fs_read" {
		t.Fatalf("expected prefixed name 'fs_read', got %q", got[0].Declaration().Name)
	}

	// Without a name, names should be unchanged.
	base2 := &fakeToolSet{name: "", tools: []tool.Tool{&simpleTool{name: "write", desc: "write file"}}}
	nts2 := itool.NewNamedToolSet(base2)
	got2 := nts2.Tools(context.Background())
	if got2[0].Declaration().Name != "write" {
		t.Fatalf("expected unmodified name 'write', got %q", got2[0].Declaration().Name)
	}
}

func TestNamedTool_OriginalAndCloseAndName(t *testing.T) {
	base := &fakeToolSet{name: "fs"}
	nts := itool.NewNamedToolSet(base)
	// Wrap a single tool.
	t1 := &simpleTool{name: "copy", desc: "copy file"}
	base.tools = []tool.Tool{t1}
	got := nts.Tools(context.Background())
	nt, ok := got[0].(*itool.NamedTool)
	if !ok {
		t.Fatalf("expected NamedTool, got %T", got[0])
	}
	if nt.Original() != t1 {
		t.Fatalf("Original() mismatch")
	}
	if nts.Name() != "fs" {
		t.Fatalf("Name() forward mismatch: %q", nts.Name())
	}
	if err := nts.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if !base.closed {
		t.Fatalf("underlying Close() not called")
	}
}

func TestNamedTool_CallAndStreamableCall(t *testing.T) {
	// Positive path via NamedToolSet wrapper.
	f := &fakeTool{decl: &tool.Declaration{Name: "sum"}, callResult: 42}
	nts := itool.NewNamedToolSet(&fakeToolSet{name: "math", tools: []tool.Tool{f}})
	ts := nts.Tools(context.Background())
	nt, ok := ts[0].(*itool.NamedTool)
	if !ok {
		t.Fatalf("expected NamedTool, got %T", ts[0])
	}
	v, err := nt.Call(context.Background(), nil)
	if err != nil {
		t.Fatalf("Call() unexpected error: %v", err)
	}
	if v != 42 {
		t.Fatalf("Call() result = %v, want 42", v)
	}

	r, err := nt.StreamableCall(context.Background(), nil)
	if err != nil {
		t.Fatalf("StreamableCall() unexpected error: %v", err)
	}
	if f.stream == nil {
		t.Fatalf("stream should be initialized")
	}
	f.stream.Writer.Send(tool.StreamChunk{Content: "ok"}, nil)
	chunk, recvErr := r.Recv()
	if recvErr != nil {
		t.Fatalf("Recv() unexpected error: %v", recvErr)
	}
	if chunk.Content != "ok" {
		t.Fatalf("Recv() content = %v, want ok", chunk.Content)
	}
	f.stream.Writer.Close()
}

func TestNamedTool_CallFailures(t *testing.T) {
	// Negative path through wrapper (not callable or streamable).
	nts := itool.NewNamedToolSet(&fakeToolSet{name: "fs", tools: []tool.Tool{&simpleTool{name: "noop"}}})
	nt := nts.Tools(context.Background())[0].(*itool.NamedTool)
	if _, err := nt.Call(context.Background(), nil); err == nil || err.Error() != "tool is not callable" {
		t.Fatalf("Call() expected not callable error, got %v", err)
	}
	if _, err := nt.StreamableCall(context.Background(), nil); err == nil || err.Error() != "tool is not streamable" {
		t.Fatalf("StreamableCall() expected not streamable error, got %v", err)
	}
}

func TestNamedTool_SkipSummarizationDelegation(t *testing.T) {
	// Wrap with NamedToolSet so we can obtain a *NamedTool instance.
	nts := itool.NewNamedToolSet(&fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&skipperTool{name: "raw", skip: true}},
	})
	t1 := nts.Tools(context.Background())[0].(*itool.NamedTool)
	if !t1.SkipSummarization() {
		t.Fatalf("expected delegated SkipSummarization=true")
	}

	nts2 := itool.NewNamedToolSet(&fakeToolSet{
		name:  "fs",
		tools: []tool.Tool{&skipperTool{name: "raw", skip: false}},
	})
	t2 := nts2.Tools(context.Background())[0].(*itool.NamedTool)
	if t2.SkipSummarization() {
		t.Fatalf("expected delegated SkipSummarization=false")
	}
}

func TestGenerateJSONSchema_Primitives(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected *tool.Schema
	}{
		{
			name:     "string type",
			input:    "",
			expected: &tool.Schema{Type: "string"},
		},
		{
			name:     "integer type",
			input:    int(0),
			expected: &tool.Schema{Type: "integer"},
		},
		{
			name:     "float type",
			input:    float64(0),
			expected: &tool.Schema{Type: "number"},
		},
		{
			name:     "boolean type",
			input:    false,
			expected: &tool.Schema{Type: "boolean"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := itool.GenerateJSONSchema(reflect.TypeOf(tc.input))
			if result.Type != tc.expected.Type {
				t.Errorf("expected type %s, got %s", tc.expected.Type, result.Type)
			}
		})
	}
}

func TestGenerateJSONSchema_ComplexTypes(t *testing.T) {
	t.Run("array type", func(t *testing.T) {
		input := []string{}
		result := itool.GenerateJSONSchema(reflect.TypeOf(input))

		if result.Type != "array" {
			t.Errorf("expected array type, got %s", result.Type)
		}
		if result.Items == nil {
			t.Fatal("expected items schema, got nil")
		}
		if result.Items.Type != "string" {
			t.Errorf("expected items type string, got %s", result.Items.Type)
		}
	})

	t.Run("map type", func(t *testing.T) {
		input := map[string]int{}
		result := itool.GenerateJSONSchema(reflect.TypeOf(input))

		if result.Type != "object" {
			t.Errorf("expected object type, got %s", result.Type)
		}
		if result.AdditionalProperties == nil {
			t.Fatal("expected additionalProperties, got nil")
		}
		propSchema, ok := result.AdditionalProperties.(*tool.Schema)
		if !ok {
			t.Fatalf("expected additionalProperties to be *tool.Schema, got %T", result.AdditionalProperties)
		}
		if propSchema.Type != "integer" {
			t.Errorf("expected additionalProperties type integer, got %s", propSchema.Type)
		}
	})

	t.Run("pointer type", func(t *testing.T) {
		var input *string
		result := itool.GenerateJSONSchema(reflect.TypeOf(input))

		if result.Type != "string" {
			t.Errorf("expected string type, got %s", result.Type)
		}
	})
}

func TestGenerateJSONSchema_StructTypes(t *testing.T) {
	type TestStruct struct {
		Name       string  `json:"name"`
		Age        int     `json:"age"`
		Optional   *string `json:"optional,omitempty"`
		Ignored    string  `json:"-"`
		unexported string
	}

	t.Run("struct with fields", func(t *testing.T) {
		result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

		if result.Type != "object" {
			t.Errorf("expected object type for struct, got %s", result.Type)
		}

		if len(result.Properties) != 3 {
			t.Errorf("expected 3 properties, got %d", len(result.Properties))
		}

		if result.Properties["name"] == nil || result.Properties["name"].Type != "string" {
			t.Errorf("expected name property of type string")
		}

		if result.Properties["age"] == nil || result.Properties["age"].Type != "integer" {
			t.Errorf("expected age property of type integer")
		}

		if result.Properties["optional"] == nil || result.Properties["optional"].Type != "string" {
			t.Errorf("expected optional property of type string")
		}

		// Check required fields
		requiredFound := false
		for _, req := range result.Required {
			if req == "name" || req == "age" {
				requiredFound = true
			}
			if req == "optional" {
				t.Errorf("optional field should not be in required list")
			}
		}
		if !requiredFound {
			t.Errorf("name and age should be in required list")
		}

		// Make sure ignored and unexported fields are not included
		if result.Properties["Ignored"] != nil {
			t.Errorf("ignored field should not be included")
		}
		if result.Properties["unexported"] != nil {
			t.Errorf("unexported field should not be included")
		}
	})
}

func TestGenerateJSONSchema_Nested(t *testing.T) {
	type Address struct {
		Street string `json:"street"`
		City   string `json:"city"`
	}

	type Person struct {
		Name    string   `json:"name"`
		Address Address  `json:"address"`
		Tags    []string `json:"tags"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(Person{}))

	if result.Properties["address"] == nil {
		t.Fatal("expected address property")
	}

	addressProps := result.Properties["address"].Properties
	if addressProps == nil {
		t.Fatal("expected address to have properties")
	}

	if addressProps["street"] == nil || addressProps["street"].Type != "string" {
		t.Errorf("expected street property of type string")
	}

	if result.Properties["tags"] == nil || result.Properties["tags"].Type != "array" {
		t.Errorf("expected tags property of type array")
	}

	if result.Properties["tags"].Items == nil || result.Properties["tags"].Items.Type != "string" {
		t.Errorf("expected tags items to be of type string")
	}
}

func TestGenerateJSONSchema_PointerTypeFix(t *testing.T) {
	// Test that pointer types now generate standard schema format
	// instead of the problematic "object,null" format

	type TestRequest struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	var input *TestRequest
	result := itool.GenerateJSONSchema(reflect.TypeOf(input))

	// Should generate "object" instead of "object,null"
	if result.Type != "object" {
		t.Errorf("expected object type for pointer to struct, got %s", result.Type)
	}

	// Should have properties
	if result.Properties == nil {
		t.Errorf("expected properties for struct schema")
	}

	// Check that properties are correctly generated
	if result.Properties["name"] == nil || result.Properties["name"].Type != "string" {
		t.Errorf("expected name property of type string")
	}

	if result.Properties["age"] == nil || result.Properties["age"].Type != "integer" {
		t.Errorf("expected age property of type integer")
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_Description(t *testing.T) {
	type TestStruct struct {
		Name string `json:"name" jsonschema:"description=User's full name"`
		Age  int    `json:"age" jsonschema:"description=User's age in years"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check description for name field
	if result.Properties["name"].Description != "User's full name" {
		t.Errorf("expected description 'User's full name', got '%s'", result.Properties["name"].Description)
	}

	// Check description for age field
	if result.Properties["age"].Description != "User's age in years" {
		t.Errorf("expected description 'User's age in years', got '%s'", result.Properties["age"].Description)
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_StringEnum(t *testing.T) {
	type TestStruct struct {
		Status string `json:"status" jsonschema:"enum=active,enum=inactive,enum=pending"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	statusSchema := result.Properties["status"]
	if len(statusSchema.Enum) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(statusSchema.Enum))
	}

	expectedEnums := []string{"active", "inactive", "pending"}
	for i, expected := range expectedEnums {
		if statusSchema.Enum[i] != expected {
			t.Errorf("expected enum[%d] to be '%s', got '%v'", i, expected, statusSchema.Enum[i])
		}
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_IntEnum(t *testing.T) {
	type TestStruct struct {
		Priority int `json:"priority" jsonschema:"enum=1,enum=2,enum=3"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	prioritySchema := result.Properties["priority"]
	if len(prioritySchema.Enum) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(prioritySchema.Enum))
	}

	expectedEnums := []int64{1, 2, 3}
	for i, expected := range expectedEnums {
		if prioritySchema.Enum[i] != expected {
			t.Errorf("expected enum[%d] to be %d, got %v", i, expected, prioritySchema.Enum[i])
		}
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_FloatEnum(t *testing.T) {
	type TestStruct struct {
		Rate float64 `json:"rate" jsonschema:"enum=1.5,enum=2.0,enum=3.5"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	rateSchema := result.Properties["rate"]
	if len(rateSchema.Enum) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(rateSchema.Enum))
	}

	expectedEnums := []float64{1.5, 2.0, 3.5}
	for i, expected := range expectedEnums {
		if rateSchema.Enum[i] != expected {
			t.Errorf("expected enum[%d] to be %f, got %v", i, expected, rateSchema.Enum[i])
		}
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_BoolEnum(t *testing.T) {
	type TestStruct struct {
		Enabled bool `json:"enabled" jsonschema:"enum=true,enum=false"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	enabledSchema := result.Properties["enabled"]
	if len(enabledSchema.Enum) != 2 {
		t.Errorf("expected 2 enum values, got %d", len(enabledSchema.Enum))
	}

	expectedEnums := []bool{true, false}
	for i, expected := range expectedEnums {
		if enabledSchema.Enum[i] != expected {
			t.Errorf("expected enum[%d] to be %t, got %v", i, expected, enabledSchema.Enum[i])
		}
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_Required(t *testing.T) {
	type TestStruct struct {
		RequiredField    string `json:"required_field" jsonschema:"required"`
		OptionalField    string `json:"optional_field,omitempty"`
		NonOptionalField string `json:"non_optional_field"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check required fields
	expectedRequired := []string{"required_field", "non_optional_field"}
	if len(result.Required) != len(expectedRequired) {
		t.Errorf("expected %d required fields, got %d", len(expectedRequired), len(result.Required))
	}

	for _, expected := range expectedRequired {
		found := false
		for _, required := range result.Required {
			if required == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected field '%s' to be required", expected)
		}
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_Combined(t *testing.T) {
	type TestStruct struct {
		Status string `json:"status" jsonschema:"description=Current status,enum=active,enum=inactive,required"`
		Count  int    `json:"count,omitempty" jsonschema:"description=Item count,enum=10,enum=20,enum=30"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check status field
	statusSchema := result.Properties["status"]
	if statusSchema.Description != "Current status" {
		t.Errorf("expected status description 'Current status', got '%s'", statusSchema.Description)
	}
	if len(statusSchema.Enum) != 2 {
		t.Errorf("expected 2 status enum values, got %d", len(statusSchema.Enum))
	}

	// Check count field
	countSchema := result.Properties["count"]
	if countSchema.Description != "Item count" {
		t.Errorf("expected count description 'Item count', got '%s'", countSchema.Description)
	}
	if len(countSchema.Enum) != 3 {
		t.Errorf("expected 3 count enum values, got %d", len(countSchema.Enum))
	}

	// Check required fields (only status should be required)
	if len(result.Required) != 1 || result.Required[0] != "status" {
		t.Errorf("expected only 'status' to be required, got %v", result.Required)
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_InvalidEnum(t *testing.T) {
	type TestStruct struct {
		InvalidInt string `json:"invalid_int" jsonschema:"enum=not_a_number"`
	}

	// This should continue processing despite the invalid enum error
	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Should return a struct schema with properties despite the error
	if result.Type != "object" {
		t.Errorf("expected object type, got %s", result.Type)
	}

	// Should have the field property even with invalid enum
	if result.Properties["invalid_int"] == nil {
		t.Errorf("expected invalid_int property to be present")
	}

	if result.Properties["invalid_int"].Type != "string" {
		t.Errorf("expected invalid_int to be string type, got %s", result.Properties["invalid_int"].Type)
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_EdgeCases(t *testing.T) {
	type TestStruct struct {
		EmptyTag    string `json:"empty_tag" jsonschema:""`
		OnlyCommas  string `json:"only_commas" jsonschema:",,,"`
		SimpleTag   string `json:"simple" jsonschema:"description=Test Description,required"`
		SingleValue string `json:"single" jsonschema:"required"`
		NoEquals    string `json:"no_equals" jsonschema:"description"`
	}

	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Check that description is set correctly without trimming
	if result.Properties["simple"].Description != "Test Description" {
		t.Errorf("expected description 'Test Description', got '%s'", result.Properties["simple"].Description)
	}

	// Check required fields
	expectedRequired := []string{"simple", "single", "empty_tag", "only_commas", "no_equals"}
	if len(result.Required) != len(expectedRequired) {
		t.Errorf("expected %d required fields, got %d", len(expectedRequired), len(result.Required))
	}
}

func TestGenerateJSONSchema_JSONSchemaTag_UnsupportedEnumType(t *testing.T) {
	type CustomType struct {
		Value string
	}

	type TestStruct struct {
		Custom CustomType `json:"custom" jsonschema:"enum=value1,enum=value2"`
	}

	// This should continue processing despite the unsupported enum type error
	result := itool.GenerateJSONSchema(reflect.TypeOf(TestStruct{}))

	// Should return a struct schema with properties despite the error
	if result.Type != "object" {
		t.Errorf("expected object type, got %s", result.Type)
	}

	// Should have the field property even with unsupported enum type
	if result.Properties["custom"] == nil {
		t.Errorf("expected custom property to be present")
	}

	if result.Properties["custom"].Type != "object" {
		t.Errorf("expected custom to be object type, got %s", result.Properties["custom"].Type)
	}
}
