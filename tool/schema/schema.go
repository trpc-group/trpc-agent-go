package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// SchemaType represents JSON Schema types.
type SchemaType string

const (
	TypeString  SchemaType = "string"
	TypeNumber  SchemaType = "number"
	TypeInteger SchemaType = "integer"
	TypeBoolean SchemaType = "boolean"
	TypeArray   SchemaType = "array"
	TypeObject  SchemaType = "object"
	TypeNull    SchemaType = "null"
)

// Schema represents a JSON Schema for tool parameters.
type Schema struct {
	Type        SchemaType         `json:"type"`
	Description string             `json:"description,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Enum        []interface{}      `json:"enum,omitempty"`
	Default     interface{}        `json:"default,omitempty"`
	Format      string             `json:"format,omitempty"`
	MinLength   int                `json:"minLength,omitempty"`
	MaxLength   int                `json:"maxLength,omitempty"`
	Minimum     float64            `json:"minimum,omitempty"`
	Maximum     float64            `json:"maximum,omitempty"`
	Pattern     string             `json:"pattern,omitempty"`
}

// ParameterSchema represents a complete schema for tool parameters.
type ParameterSchema struct {
	Schema
}

// NewParameterSchema creates a new parameter schema for an object type.
func NewParameterSchema() *ParameterSchema {
	return &ParameterSchema{
		Schema: Schema{
			Type:       TypeObject,
			Properties: make(map[string]*Schema),
		},
	}
}

// AddProperty adds a property to the schema.
func (s *ParameterSchema) AddProperty(name string, propSchema *Schema, required bool) {
	s.Properties[name] = propSchema
	if required {
		s.Required = append(s.Required, name)
	}
}

// AddStringProperty adds a string property.
func (s *ParameterSchema) AddStringProperty(name, description string, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeString,
		Description: description,
	}, required)
}

// AddNumberProperty adds a number property.
func (s *ParameterSchema) AddNumberProperty(name, description string, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeNumber,
		Description: description,
	}, required)
}

// AddIntegerProperty adds an integer property.
func (s *ParameterSchema) AddIntegerProperty(name, description string, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeInteger,
		Description: description,
	}, required)
}

// AddBooleanProperty adds a boolean property.
func (s *ParameterSchema) AddBooleanProperty(name, description string, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeBoolean,
		Description: description,
	}, required)
}

// AddArrayProperty adds an array property.
func (s *ParameterSchema) AddArrayProperty(name, description string, itemSchema *Schema, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeArray,
		Description: description,
		Items:       itemSchema,
	}, required)
}

// AddObjectProperty adds an object property.
func (s *ParameterSchema) AddObjectProperty(name, description string, properties map[string]*Schema, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeObject,
		Description: description,
		Properties:  properties,
	}, required)
}

// AddEnumProperty adds an enum property.
func (s *ParameterSchema) AddEnumProperty(name, description string, values []interface{}, required bool) {
	s.AddProperty(name, &Schema{
		Type:        TypeString,
		Description: description,
		Enum:        values,
	}, required)
}

// Validate validates a parameter map against the schema.
func (s *ParameterSchema) Validate(params map[string]interface{}) error {
	// Check required parameters
	for _, reqName := range s.Required {
		if _, exists := params[reqName]; !exists {
			prop := s.Properties[reqName]
			desc := prop.Description
			if desc == "" {
				desc = fmt.Sprintf("Parameter of type %s", prop.Type)
			}
			return fmt.Errorf("missing required parameter '%s': %s", reqName, desc)
		}
	}

	// Validate each parameter
	for name, value := range params {
		propSchema, exists := s.Properties[name]
		if !exists {
			// Unknown parameter - could be lenient or strict
			continue
		}

		if err := validateValueAgainstSchema(name, value, propSchema); err != nil {
			return err
		}
	}

	return nil
}

// ValidateAndConvert validates and converts parameters to their correct types.
func (s *ParameterSchema) ValidateAndConvert(params map[string]interface{}) (map[string]interface{}, error) {
	// First attempt to convert each parameter that needs conversion
	convertedParams := make(map[string]interface{})
	conversionErrors := []string{}

	// First pass - attempt conversions
	for name, value := range params {
		propSchema, exists := s.Properties[name]
		if !exists {
			// Pass through unknown parameters
			convertedParams[name] = value
			continue
		}

		// Attempt to convert the value
		converted, err := convertValueToSchemaType(value, propSchema)
		if err != nil {
			conversionErrors = append(conversionErrors, fmt.Sprintf("parameter '%s': %v", name, err))
			// Keep the original value for validation
			convertedParams[name] = value
		} else {
			convertedParams[name] = converted
		}
	}

	// Validate the parameters (including any unconverted ones)
	if err := s.Validate(convertedParams); err != nil {
		return nil, err
	}

	// If we had conversion errors, return them now (after validation check)
	if len(conversionErrors) > 0 {
		return nil, fmt.Errorf("type conversion errors: %s", strings.Join(conversionErrors, "; "))
	}

	// Add defaults for missing optional parameters
	for name, propSchema := range s.Properties {
		if _, exists := convertedParams[name]; !exists && propSchema.Default != nil {
			convertedParams[name] = propSchema.Default
		}
	}

	return convertedParams, nil
}

// ToJSON converts the schema to a JSON string.
func (s *ParameterSchema) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// ToMap converts the schema to a map for use with OpenAI/Anthropic/etc.
func (s *ParameterSchema) ToMap() map[string]interface{} {
	var result map[string]interface{}
	bytes, _ := json.Marshal(s)
	json.Unmarshal(bytes, &result) // Ignoring error as we just marshaled it
	return result
}

// Helper functions for validation

func validateValueAgainstSchema(name string, value interface{}, schema *Schema) error {
	// Handle null/nil values
	if value == nil {
		// Check if the schema allows null
		return nil
	}

	switch schema.Type {
	case TypeString:
		strVal, ok := value.(string)
		if !ok {
			return fmt.Errorf("parameter '%s' must be a string, got %T", name, value)
		}
		if schema.MinLength > 0 && len(strVal) < schema.MinLength {
			return fmt.Errorf("parameter '%s' must be at least %d characters long", name, schema.MinLength)
		}
		if schema.MaxLength > 0 && len(strVal) > schema.MaxLength {
			return fmt.Errorf("parameter '%s' must be at most %d characters long", name, schema.MaxLength)
		}
		if len(schema.Enum) > 0 {
			valid := false
			for _, enumVal := range schema.Enum {
				if enumStr, ok := enumVal.(string); ok && enumStr == strVal {
					valid = true
					break
				}
			}
			if !valid {
				enumStrs := make([]string, 0, len(schema.Enum))
				for _, e := range schema.Enum {
					enumStrs = append(enumStrs, fmt.Sprintf("%v", e))
				}
				return fmt.Errorf("parameter '%s' must be one of: %s", name, strings.Join(enumStrs, ", "))
			}
		}

	case TypeNumber, TypeInteger:
		var numVal float64
		switch v := value.(type) {
		case float64:
			numVal = v
		case float32:
			numVal = float64(v)
		case int:
			numVal = float64(v)
		case int64:
			numVal = float64(v)
		case int32:
			numVal = float64(v)
		default:
			return fmt.Errorf("parameter '%s' must be a number, got %T", name, value)
		}

		if schema.Type == TypeInteger && float64(int(numVal)) != numVal {
			return fmt.Errorf("parameter '%s' must be an integer", name)
		}
		if schema.Minimum != 0 && numVal < schema.Minimum {
			return fmt.Errorf("parameter '%s' must be at least %v", name, schema.Minimum)
		}
		if schema.Maximum != 0 && numVal > schema.Maximum {
			return fmt.Errorf("parameter '%s' must be at most %v", name, schema.Maximum)
		}

	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("parameter '%s' must be a boolean, got %T", name, value)
		}

	case TypeArray:
		arr, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("parameter '%s' must be an array, got %T", name, value)
		}
		if schema.Items != nil {
			for i, item := range arr {
				itemName := fmt.Sprintf("%s[%d]", name, i)
				if err := validateValueAgainstSchema(itemName, item, schema.Items); err != nil {
					return err
				}
			}
		}

	case TypeObject:
		obj, ok := value.(map[string]interface{})
		if !ok {
			// Try to convert from a generic interface map
			if genericMap, isMap := value.(map[interface{}]interface{}); isMap {
				obj = make(map[string]interface{})
				for k, v := range genericMap {
					if keyStr, isStr := k.(string); isStr {
						obj[keyStr] = v
					}
				}
			} else {
				return fmt.Errorf("parameter '%s' must be an object, got %T", name, value)
			}
		}

		if schema.Properties != nil {
			// Check required properties
			if len(schema.Required) > 0 {
				for _, reqProp := range schema.Required {
					if _, exists := obj[reqProp]; !exists {
						propDesc := ""
						if propSchema, hasProp := schema.Properties[reqProp]; hasProp && propSchema.Description != "" {
							propDesc = ": " + propSchema.Description
						}
						return fmt.Errorf("parameter '%s.%s' is required%s", name, reqProp, propDesc)
					}
				}
			}

			// Validate properties that are present
			for propName, propSchema := range schema.Properties {
				propPath := fmt.Sprintf("%s.%s", name, propName)
				propVal, exists := obj[propName]

				// If property exists, validate it
				if exists {
					if err := validateValueAgainstSchema(propPath, propVal, propSchema); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// convertValueToSchemaType converts a value to the type specified in the schema.
func convertValueToSchemaType(value interface{}, schema *Schema) (interface{}, error) {
	if value == nil {
		return nil, nil
	}

	switch schema.Type {
	case TypeString:
		switch v := value.(type) {
		case string:
			return v, nil
		default:
			// Convert to string if possible
			return fmt.Sprintf("%v", v), nil
		}

	case TypeNumber:
		switch v := value.(type) {
		case float64:
			return v, nil
		case float32:
			return float64(v), nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case int32:
			return float64(v), nil
		case string:
			// Try to parse as float64
			var parsed float64
			if _, err := fmt.Sscanf(v, "%f", &parsed); err == nil {
				return parsed, nil
			}
			return nil, fmt.Errorf("cannot convert string '%s' to number", v)
		default:
			return nil, fmt.Errorf("cannot convert %T to number", value)
		}

	case TypeInteger:
		switch v := value.(type) {
		case int:
			return v, nil
		case int64:
			return int(v), nil
		case int32:
			return int(v), nil
		case float64:
			if float64(int(v)) == v {
				return int(v), nil
			}
			return nil, fmt.Errorf("cannot convert non-integer float %v to integer", v)
		case float32:
			if float32(int(v)) == v {
				return int(v), nil
			}
			return nil, fmt.Errorf("cannot convert non-integer float %v to integer", v)
		case string:
			// Try to parse as int
			var parsed int
			if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil {
				return parsed, nil
			}
			return nil, fmt.Errorf("cannot convert string '%s' to integer", v)
		default:
			return nil, fmt.Errorf("cannot convert %T to integer", value)
		}

	case TypeBoolean:
		switch v := value.(type) {
		case bool:
			return v, nil
		case string:
			switch strings.ToLower(v) {
			case "true", "yes", "1":
				return true, nil
			case "false", "no", "0":
				return false, nil
			default:
				return nil, fmt.Errorf("cannot convert string '%s' to boolean", v)
			}
		case float64:
			if v == 1.0 {
				return true, nil
			} else if v == 0.0 {
				return false, nil
			}
			return nil, fmt.Errorf("cannot convert number %v to boolean", v)
		case int:
			if v == 1 {
				return true, nil
			} else if v == 0 {
				return false, nil
			}
			return nil, fmt.Errorf("cannot convert number %v to boolean", v)
		default:
			return nil, fmt.Errorf("cannot convert %T to boolean", value)
		}

	case TypeArray:
		if arr, ok := value.([]interface{}); ok {
			if schema.Items != nil {
				result := make([]interface{}, len(arr))
				for i, item := range arr {
					converted, err := convertValueToSchemaType(item, schema.Items)
					if err != nil {
						return nil, fmt.Errorf("item %d: %w", i, err)
					}
					result[i] = converted
				}
				return result, nil
			}
			return arr, nil
		}

		// Try to convert a single value to an array
		if schema.Items != nil {
			converted, err := convertValueToSchemaType(value, schema.Items)
			if err != nil {
				return nil, err
			}
			return []interface{}{converted}, nil
		}

		return nil, fmt.Errorf("cannot convert %T to array", value)

	case TypeObject:
		switch v := value.(type) {
		case map[string]interface{}:
			if schema.Properties != nil {
				result := make(map[string]interface{})
				for propName, propSchema := range schema.Properties {
					if propVal, exists := v[propName]; exists {
						converted, err := convertValueToSchemaType(propVal, propSchema)
						if err != nil {
							return nil, fmt.Errorf("property '%s': %w", propName, err)
						}
						result[propName] = converted
					}
				}

				// Add any extra properties
				for propName, propVal := range v {
					if _, exists := schema.Properties[propName]; !exists {
						result[propName] = propVal
					}
				}

				return result, nil
			}
			return v, nil
		case map[interface{}]interface{}:
			// Convert to string keys
			result := make(map[string]interface{})
			for k, v := range v {
				if keyStr, ok := k.(string); ok {
					result[keyStr] = v
				} else {
					keyStr = fmt.Sprintf("%v", k)
					result[keyStr] = v
				}
			}

			// Now convert with the converted map
			return convertValueToSchemaType(result, schema)
		default:
			// If the value is a struct, try to convert it to map[string]interface{}
			if reflect.TypeOf(value).Kind() == reflect.Struct {
				data, err := json.Marshal(value)
				if err == nil {
					var result map[string]interface{}
					if err := json.Unmarshal(data, &result); err == nil {
						return convertValueToSchemaType(result, schema)
					}
				}
			}
			return nil, fmt.Errorf("cannot convert %T to object", value)
		}
	}

	return value, nil
}
