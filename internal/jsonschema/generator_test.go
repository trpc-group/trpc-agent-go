//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonschema

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type Address struct {
	Street string `json:"street"`
	City   string `json:"city"`
	Zip    string `json:"zip"`
}

type User struct {
	ID        int            `json:"id"`
	Name      string         `json:"name" description:"user full name"`
	Email     string         `json:"email,omitempty"`
	Active    bool           `json:"active"`
	Score     float64        `json:"score"`
	CreatedAt time.Time      `json:"created_at"`
	Tags      []string       `json:"tags"`
	Meta      map[string]int `json:"meta"`
	Address   Address        `json:"address"`
}

func TestGenerator_SimpleStruct(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(User{}))
	if schema["type"] != "object" {
		t.Fatalf("expected root type object, got %v", schema["type"])
	}
	// For non-recursive structs, we inline schemas and omit $defs.
	if _, ok := schema["$defs"]; ok {
		t.Fatalf("did not expect $defs for simple non-recursive struct")
	}
}

func TestGenerator_JSONMarshalling(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(&User{}))
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema error: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal schema error: %v", err)
	}
}

type RecursiveNode struct {
	Name string         `json:"name"`
	Next *RecursiveNode `json:"next,omitempty"`
}

func TestGenerator_Recursive(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(&RecursiveNode{}))
	if _, ok := schema["$defs"]; !ok {
		t.Fatalf("expected $defs for recursive type")
	}
	props := schema["properties"].(map[string]any)
	nextSchema := props["next"].(map[string]any)
	if nextSchema["$ref"] == nil {
		t.Fatalf("expected recursive field to keep $ref, got %v", nextSchema)
	}
}

func TestGenerator_StrictRecursive(t *testing.T) {
	gen := New(WithStrict())
	schema := gen.Generate(reflect.TypeOf(&RecursiveNode{}))
	if _, ok := schema["$defs"]; !ok {
		t.Fatalf("expected $defs for recursive type")
	}
	props := schema["properties"].(map[string]any)
	nextSchema := props["next"].(map[string]any)
	nextAnyOf := nextSchema["anyOf"].([]any)
	if len(nextAnyOf) != 2 {
		t.Fatalf("expected recursive field to be nullable, got %v", nextSchema)
	}
	refSchema := nextAnyOf[0].(map[string]any)
	if refSchema["$ref"] == nil {
		t.Fatalf("expected recursive field to keep $ref, got %v", refSchema)
	}
}

type Mixed struct {
	OptStr *string        `json:"opt_str,omitempty"`
	Arr    []int          `json:"arr"`
	MapStr map[string]int `json:"map_str"`
	AnyMap map[string]any `json:"any_map"`
}

type structuredOutputDetail struct {
	City  string `json:"city"`
	Codes []int  `json:"codes"`
}

type structuredOutputPayload struct {
	Answer   string                  `json:"answer"`
	Detail   *structuredOutputDetail `json:"detail"`
	Tags     []string                `json:"tags"`
	Scores   [2]int                  `json:"scores"`
	Optional string                  `json:"optional,omitempty"`
}

type structuredOutputMapPayload struct {
	Answer string            `json:"answer"`
	Labels map[string]string `json:"labels"`
}

func TestGenerator_MixedKinds(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(Mixed{}))
	if schema["type"] != "object" {
		t.Fatalf("expected object root")
	}
}

func TestGenerator_DefaultStructuredOutputPreservesOptionalFields(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(structuredOutputPayload{}))
	props := schema["properties"].(map[string]any)

	required := schema["required"].([]string)
	if len(required) != 2 {
		t.Fatalf("expected only non-optional properties to be required, got %v", required)
	}
	if required[0] != "answer" || required[1] != "scores" {
		t.Fatalf("expected required fields [answer scores], got %v", required)
	}

	detailSchema := props["detail"].(map[string]any)
	if detailSchema["type"] != "object" {
		t.Fatalf("expected detail object schema, got %v", detailSchema["type"])
	}
	detailRequired := detailSchema["required"].([]string)
	if len(detailRequired) != 1 || detailRequired[0] != "city" {
		t.Fatalf("expected nested required fields [city], got %v", detailRequired)
	}

	tagsSchema := props["tags"].(map[string]any)
	if tagsSchema["type"] != "array" {
		t.Fatalf("expected slice field tags to remain array, got %v", tagsSchema)
	}

	optionalSchema := props["optional"].(map[string]any)
	if optionalSchema["type"] != "string" {
		t.Fatalf("expected omitempty field optional to remain string, got %v", optionalSchema)
	}

	scoresSchema := props["scores"].(map[string]any)
	if scoresSchema["type"] != "array" {
		t.Fatalf("expected fixed array schema to remain array, got %v", scoresSchema["type"])
	}
}

func TestGenerator_StrictStructuredOutputCompatibility(t *testing.T) {
	gen := New(WithStrict())
	schema := gen.Generate(reflect.TypeOf(structuredOutputPayload{}))
	props := schema["properties"].(map[string]any)

	required := schema["required"].([]string)
	if len(required) != len(props) {
		t.Fatalf("expected all properties to be required, got %v for %v", required, props)
	}

	detailSchema := props["detail"].(map[string]any)
	detailAnyOf := detailSchema["anyOf"].([]any)
	if len(detailAnyOf) != 2 {
		t.Fatalf("expected nullable anyOf for detail, got %v", detailSchema)
	}
	detailObject := detailAnyOf[0].(map[string]any)
	if detailObject["type"] != "object" {
		t.Fatalf("expected detail object schema, got %v", detailObject["type"])
	}
	detailRequired := detailObject["required"].([]string)
	if len(detailRequired) != 2 || detailRequired[0] != "city" || detailRequired[1] != "codes" {
		t.Fatalf("expected nested required fields [city codes], got %v", detailRequired)
	}

	tagsSchema := props["tags"].(map[string]any)
	if _, ok := tagsSchema["anyOf"].([]any); !ok {
		t.Fatalf("expected nullable anyOf for slice field tags, got %v", tagsSchema)
	}

	optionalSchema := props["optional"].(map[string]any)
	if _, ok := optionalSchema["anyOf"].([]any); !ok {
		t.Fatalf("expected nullable anyOf for omitempty field optional, got %v", optionalSchema)
	}

	scoresSchema := props["scores"].(map[string]any)
	if scoresSchema["type"] != "array" {
		t.Fatalf("expected fixed array schema to remain array, got %v", scoresSchema["type"])
	}
}

func TestGenerator_StructuredOutputMapCompatibility(t *testing.T) {
	gen := New(WithStrict())
	schema := gen.Generate(reflect.TypeOf(structuredOutputMapPayload{}))
	props := schema["properties"].(map[string]any)

	required := schema["required"].([]string)
	if len(required) != len(props) {
		t.Fatalf("expected all properties to be required, got %v for %v", required, props)
	}

	labelsSchema := props["labels"].(map[string]any)
	labelsAnyOf := labelsSchema["anyOf"].([]any)
	if len(labelsAnyOf) != 2 {
		t.Fatalf("expected nullable anyOf for labels, got %v", labelsSchema)
	}
	labelsObject := labelsAnyOf[0].(map[string]any)
	if labelsObject["type"] != "object" {
		t.Fatalf("expected labels object schema, got %v", labelsObject["type"])
	}
	additionalProperties := labelsObject["additionalProperties"].(map[string]any)
	if additionalProperties["type"] != "string" {
		t.Fatalf("expected map value schema to remain string, got %v", additionalProperties["type"])
	}
}

// TestGenerator_MapWithNonStringKey tests map with non-string key fallback.
func TestGenerator_MapWithNonStringKey(t *testing.T) {
	type NonStringKeyMap struct {
		IntMap map[int]string `json:"int_map"`
	}
	gen := New()
	schema := gen.Generate(reflect.TypeOf(NonStringKeyMap{}))
	props := schema["properties"].(map[string]any)
	intMapSchema := props["int_map"].(map[string]any)
	// Should fallback to array representation.
	if intMapSchema["type"] != "array" {
		t.Fatalf("expected array type for non-string key map, got %v", intMapSchema["type"])
	}
}

func TestGenerator_StrictMapWithNonStringKey(t *testing.T) {
	type NonStringKeyMap struct {
		IntMap map[int]string `json:"int_map"`
	}
	gen := New(WithStrict())
	schema := gen.Generate(reflect.TypeOf(NonStringKeyMap{}))
	props := schema["properties"].(map[string]any)
	intMapSchema := props["int_map"].(map[string]any)
	intMapAnyOf := intMapSchema["anyOf"].([]any)
	intMapSchema = intMapAnyOf[0].(map[string]any)
	if intMapSchema["type"] != "array" {
		t.Fatalf("expected array type for non-string key map, got %v", intMapSchema["type"])
	}
}

// TestGenerator_FieldTags tests field tags (description, enum).
func TestGenerator_FieldTags(t *testing.T) {
	type TaggedStruct struct {
		Status      string `json:"status" description:"status of the user" enum:"active,inactive,pending"`
		Category    string `json:"category" description:"user category"`
		NoEnumField string `json:"no_enum"`
	}
	gen := New()
	schema := gen.Generate(reflect.TypeOf(TaggedStruct{}))
	props := schema["properties"].(map[string]any)

	// Check Status field has description and enum.
	statusSchema := props["status"].(map[string]any)
	if statusSchema["description"] != "status of the user" {
		t.Errorf("expected description for status field")
	}
	if statusSchema["enum"] == nil {
		t.Errorf("expected enum for status field")
	}
	enumValues := statusSchema["enum"].([]any)
	if len(enumValues) != 3 {
		t.Errorf("expected 3 enum values, got %d", len(enumValues))
	}

	// Check Category field has description only.
	categorySchema := props["category"].(map[string]any)
	if categorySchema["description"] != "user category" {
		t.Errorf("expected description for category field")
	}
	if categorySchema["enum"] != nil {
		t.Errorf("did not expect enum for category field")
	}

	// Check NoEnumField has neither description nor enum.
	noEnumSchema := props["no_enum"].(map[string]any)
	if noEnumSchema["description"] != nil {
		t.Errorf("did not expect description for no_enum field")
	}
	if noEnumSchema["enum"] != nil {
		t.Errorf("did not expect enum for no_enum field")
	}
}

// TestGenerator_DefinitionName tests definitionName for various types.
func TestGenerator_DefinitionName(t *testing.T) {
	// Named struct with package path.
	type NamedStruct struct {
		Field string
	}

	gen := New()
	// Test with named type.
	namedType := reflect.TypeOf(NamedStruct{})
	defName := gen.definitionName(namedType)
	if defName == "" {
		t.Errorf("expected non-empty definition name for named struct")
	}

	builtinType := reflect.TypeOf(int(0))
	defNameBuiltin := gen.definitionName(builtinType)
	if defNameBuiltin != "int" {
		t.Errorf("expected builtin definition name int, got %s", defNameBuiltin)
	}

	// Test with anonymous struct (no name, no package path).
	anonType := reflect.TypeOf(struct{ Field string }{})
	defName2 := gen.definitionName(anonType)
	if defName2 == "" {
		t.Errorf("expected generated definition name for anonymous struct")
	}

	// Test that sequential calls generate unique names.
	defName3 := gen.definitionName(anonType)
	if defName2 == defName3 {
		t.Errorf("expected unique definition names for multiple anonymous structs")
	}
}

func TestMakeNullable(t *testing.T) {
	nilSchema := makeNullable(nil)
	if nilSchema["type"] != "null" {
		t.Fatalf("expected nil schema to become null, got %v", nilSchema)
	}

	baseSchema := map[string]any{"type": "string"}
	nullableSchema := makeNullable(baseSchema)
	anyOf, ok := nullableSchema["anyOf"].([]any)
	if !ok || len(anyOf) != 2 {
		t.Fatalf("expected anyOf nullable schema, got %v", nullableSchema)
	}

	alreadyNullable := map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "null"},
		},
	}
	if !reflect.DeepEqual(makeNullable(alreadyNullable), alreadyNullable) {
		t.Fatalf("expected already nullable schema to be returned as-is")
	}
}

func TestHasNullableAnyOf(t *testing.T) {
	if hasNullableAnyOf(map[string]any{"type": "string"}) {
		t.Fatalf("expected schema without anyOf to be non-nullable")
	}

	if hasNullableAnyOf(map[string]any{
		"anyOf": []any{
			"invalid",
			map[string]any{"type": "string"},
		},
	}) {
		t.Fatalf("expected anyOf without null branch to be non-nullable")
	}

	if !hasNullableAnyOf(map[string]any{
		"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "null"},
		},
	}) {
		t.Fatalf("expected anyOf with null branch to be nullable")
	}
}

// TestGenerator_FieldJSONName tests fieldJSONName with various json tags.
func TestGenerator_FieldJSONName(t *testing.T) {
	type TestStruct struct {
		NoTag            string `json:""`
		WithTag          string `json:"custom_name"`
		WithOmit         string `json:"omit_field,omitempty"`
		EmptyBeforeComma string `json:",omitempty"`
	}

	typ := reflect.TypeOf(TestStruct{})

	// NoTag - should use field name.
	field1, _ := typ.FieldByName("NoTag")
	name1 := fieldJSONName(field1)
	if name1 != "NoTag" {
		t.Errorf("expected 'NoTag', got %s", name1)
	}

	// WithTag - should use custom name.
	field2, _ := typ.FieldByName("WithTag")
	name2 := fieldJSONName(field2)
	if name2 != "custom_name" {
		t.Errorf("expected 'custom_name', got %s", name2)
	}

	// WithOmit - should use name before comma.
	field3, _ := typ.FieldByName("WithOmit")
	name3 := fieldJSONName(field3)
	if name3 != "omit_field" {
		t.Errorf("expected 'omit_field', got %s", name3)
	}

	// EmptyBeforeComma - should use field name.
	field4, _ := typ.FieldByName("EmptyBeforeComma")
	name4 := fieldJSONName(field4)
	if name4 != "EmptyBeforeComma" {
		t.Errorf("expected 'EmptyBeforeComma', got %s", name4)
	}
}

// TestGenerator_IsOmitEmpty tests isOmitEmpty function.
func TestGenerator_IsOmitEmpty(t *testing.T) {
	tests := []struct {
		tag      string
		expected bool
	}{
		{tag: "", expected: false},
		{tag: "field", expected: false},
		{tag: "field,omitempty", expected: true},
		{tag: "field,string", expected: false},
		{tag: "field,string,omitempty", expected: true},
	}

	for _, tt := range tests {
		if got := isOmitEmpty(tt.tag); got != tt.expected {
			t.Errorf("isOmitEmpty(%q) = %v, want %v", tt.tag, got, tt.expected)
		}
	}
}

// TestGenerator_IsPointerLike tests isPointerLike function.
func TestGenerator_IsPointerLike(t *testing.T) {
	stringType := reflect.TypeOf("")
	ptrType := reflect.TypeOf((*string)(nil))
	sliceType := reflect.TypeOf([]string(nil))
	mapType := reflect.TypeOf(map[string]string(nil))

	if isPointerLike(stringType) {
		t.Errorf("expected string type to not be pointer-like")
	}
	if !isPointerLike(ptrType) {
		t.Errorf("expected pointer type to be pointer-like")
	}
	if !isPointerLike(sliceType) {
		t.Errorf("expected slice type to be pointer-like")
	}
	if !isPointerLike(mapType) {
		t.Errorf("expected map type to be pointer-like")
	}
}
