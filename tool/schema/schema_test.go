package schema

import (
	"testing"
)

func TestSchemaValidation(t *testing.T) {
	// Create a parameter schema
	schema := NewParameterSchema()
	schema.AddStringProperty("name", "The name parameter", true)
	schema.AddIntegerProperty("age", "The age parameter", true)
	schema.AddBooleanProperty("active", "Whether the user is active", false)
	schema.AddArrayProperty("tags", "List of tags", &Schema{Type: TypeString}, false)

	// Valid parameters
	validParams := map[string]interface{}{
		"name":   "John",
		"age":    30,
		"active": true,
		"tags":   []interface{}{"tag1", "tag2"},
	}

	// Validate the parameters
	err := schema.Validate(validParams)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Missing required parameter
	invalidParams := map[string]interface{}{
		"name": "John",
		// Missing age
	}

	err = schema.Validate(invalidParams)
	if err == nil {
		t.Error("Expected error for missing required parameter")
	}

	// Wrong type for parameter
	invalidParams = map[string]interface{}{
		"name":   "John",
		"age":    "not a number", // String instead of integer
		"active": true,
	}

	err = schema.Validate(invalidParams)
	if err == nil {
		t.Error("Expected error for wrong parameter type")
	}
}

func TestSchemaConversion(t *testing.T) {
	// Create a parameter schema
	schema := NewParameterSchema()
	schema.AddStringProperty("name", "The name parameter", true)
	schema.AddNumberProperty("height", "Height in meters", false)
	schema.AddIntegerProperty("age", "The age parameter", true)
	schema.AddBooleanProperty("active", "Whether the user is active", false)

	// Test converting parameters - use string representations that should convert correctly
	params := map[string]interface{}{
		"name":   "John",
		"height": "1.85", // String that should be converted to float
		"age":    30,     // Integer value that's already correct
		"active": "true", // String that should convert to boolean
	}

	converted, err := schema.ValidateAndConvert(params)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Check converted values
	if converted["name"] != "John" {
		t.Errorf("Expected name to be 'John', got %v", converted["name"])
	}

	// Check height was converted to float
	height, ok := converted["height"].(float64)
	if !ok {
		t.Errorf("Expected height to be float64, got %T", converted["height"])
	} else {
		// Allow a small margin for floating point conversion
		if height < 1.84 || height > 1.86 {
			t.Errorf("Expected height to be approximately 1.85, got %v", height)
		}
	}

	// Check age is preserved as int
	age, ok := converted["age"].(int)
	if !ok {
		t.Errorf("Expected age to be int, got %T", converted["age"])
	} else if age != 30 {
		t.Errorf("Expected age to be 30, got %v", age)
	}

	// Check active was converted to boolean
	active, ok := converted["active"].(bool)
	if !ok {
		t.Errorf("Expected active to be bool, got %T", converted["active"])
	} else if !active {
		t.Errorf("Expected active to be true, got %v", active)
	}
}

func TestNestedObjectValidation(t *testing.T) {
	// Create a nested schema
	addressProps := map[string]*Schema{
		"street": {Type: TypeString, Description: "Street name"},
		"city":   {Type: TypeString, Description: "City name"},
		"zip":    {Type: TypeString, Description: "Zip code"},
	}

	// Create the main schema
	schema := NewParameterSchema()
	schema.AddStringProperty("name", "The name parameter", true)
	schema.AddObjectProperty("address", "The address object", addressProps, true)
	
	// Add required fields at the object level
	schema.Properties["address"].Required = []string{"street", "city"}

	// Valid parameters
	validParams := map[string]interface{}{
		"name": "John",
		"address": map[string]interface{}{
			"street": "123 Main St",
			"city":   "New York",
			"zip":    "10001",
		},
	}

	// Validate the parameters
	err := schema.Validate(validParams)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Missing required nested parameter
	invalidParams := map[string]interface{}{
		"name": "John",
		"address": map[string]interface{}{
			"street": "123 Main St",
			// Missing city
		},
	}

	err = schema.Validate(invalidParams)
	if err == nil {
		t.Error("Expected error for missing required nested parameter")
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

func TestArrayValidation(t *testing.T) {
	// Create a schema with an array of objects
	itemSchema := &Schema{
		Type: TypeObject,
		Properties: map[string]*Schema{
			"id":   {Type: TypeString, Description: "Item ID"},
			"name": {Type: TypeString, Description: "Item name"},
		},
		Required: []string{"id"},
	}

	schema := NewParameterSchema()
	schema.AddArrayProperty("items", "List of items", itemSchema, true)

	// Valid parameters
	validParams := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{
				"id":   "item1",
				"name": "First Item",
			},
			map[string]interface{}{
				"id":   "item2",
				"name": "Second Item",
			},
		},
	}

	// Validate the parameters
	err := schema.Validate(validParams)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// Invalid item in array (missing required id)
	invalidParams := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{
				"id":   "item1",
				"name": "First Item",
			},
			map[string]interface{}{
				// Missing id
				"name": "Second Item",
			},
		},
	}

	err = schema.Validate(invalidParams)
	if err == nil {
		t.Error("Expected error for missing required field in array item")
	} else {
		t.Logf("Got expected error: %v", err)
	}
}
