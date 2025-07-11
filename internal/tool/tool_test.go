package tool_test

import (
	"reflect"
	"testing"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGenerateJSONSchema_PrimitiveTypes(t *testing.T) {
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

		if result.Type != "string,null" {
			t.Errorf("expected string,null type, got %s", result.Type)
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

		if result.Properties["optional"] == nil || result.Properties["optional"].Type != "string,null" {
			t.Errorf("expected optional property of type string,null")
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

func TestGenerateJSONSchema_NestedStructs(t *testing.T) {
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
