package tool

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestGenerateJSONSchema_PrimitiveTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected *Schema
	}{
		{
			name:     "string type",
			input:    "",
			expected: &Schema{Type: "string"},
		},
		{
			name:     "integer type",
			input:    int(0),
			expected: &Schema{Type: "integer"},
		},
		{
			name:     "float type",
			input:    float64(0),
			expected: &Schema{Type: "number"},
		},
		{
			name:     "boolean type",
			input:    false,
			expected: &Schema{Type: "boolean"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := generateJSONSchema(reflect.TypeOf(tc.input))
			if result.Type != tc.expected.Type {
				t.Errorf("expected type %s, got %s", tc.expected.Type, result.Type)
			}
		})
	}
}

func TestGenerateJSONSchema_ComplexTypes(t *testing.T) {
	t.Run("array type", func(t *testing.T) {
		input := []string{}
		result := generateJSONSchema(reflect.TypeOf(input))

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
		result := generateJSONSchema(reflect.TypeOf(input))

		if result.Type != "object" {
			t.Errorf("expected object type, got %s", result.Type)
		}
		if result.AdditionalProperties == nil {
			t.Fatal("expected additionalProperties, got nil")
		}
		propSchema, ok := result.AdditionalProperties.(*Schema)
		if !ok {
			t.Fatalf("expected additionalProperties to be *Schema, got %T", result.AdditionalProperties)
		}
		if propSchema.Type != "integer" {
			t.Errorf("expected additionalProperties type integer, got %s", propSchema.Type)
		}
	})

	t.Run("pointer type", func(t *testing.T) {
		var input *string
		result := generateJSONSchema(reflect.TypeOf(input))

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
		result := generateJSONSchema(reflect.TypeOf(TestStruct{}))

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

	result := generateJSONSchema(reflect.TypeOf(Person{}))

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

func TestStreamableTool_Interface(t *testing.T) {
	// Compile-time check
	var _ StreamableTool = (*testStreamableTool)(nil)
}

type testStreamableTool struct{}

func (d *testStreamableTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*StreamReader, error) {
	s := NewStream(1)
	go func() {
		defer s.Writer.Close()
		s.Writer.Send(StreamChunk{Content: "test", Metadata: Metadata{CreatedAt: time.Now()}}, nil)
		s.Writer.Send(StreamChunk{Content: "more data"}, nil)
		s.Writer.Send(StreamChunk{Content: "final chunk"}, nil)

	}()
	return s.Reader, nil
}
func (d *testStreamableTool) Declaration() *Declaration {
	return &Declaration{
		Name:        "TestStreamableTool",
		Description: "A test tool for streaming data.",
		InputSchema: &Schema{
			Type:        "object",
			Properties:  map[string]*Schema{"input": {Type: "string"}},
			Required:    []string{"input"},
			Description: "Input for the test streamable tool.",
		},
	}
}
