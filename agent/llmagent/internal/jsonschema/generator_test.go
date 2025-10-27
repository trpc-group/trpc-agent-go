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
}

type Mixed struct {
	OptStr *string        `json:"opt_str,omitempty"`
	Arr    []int          `json:"arr"`
	MapStr map[string]int `json:"map_str"`
	AnyMap map[string]any `json:"any_map"`
}

func TestGenerator_MixedKinds(t *testing.T) {
	gen := New()
	schema := gen.Generate(reflect.TypeOf(Mixed{}))
	if schema["type"] != "object" {
		t.Fatalf("expected object root")
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
		{"", false},
		{"field_name", false},
		{"field_name,omitempty", true},
		{"field_name,omitempty,string", true},
		{",omitempty", true},
		{"omitempty", false}, // Only tag name without comma.
	}

	for _, tt := range tests {
		result := isOmitEmpty(tt.tag)
		if result != tt.expected {
			t.Errorf("isOmitEmpty(%q) = %v, expected %v", tt.tag, result, tt.expected)
		}
	}
}
