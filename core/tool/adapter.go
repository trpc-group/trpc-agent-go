package tool

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/tool/schema"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ConvertDefinitionToSchema converts a ToolDefinition to a schema.ParameterSchema.
func ConvertDefinitionToSchema(def *ToolDefinition) (*schema.ParameterSchema, error) {
	if def == nil {
		return nil, fmt.Errorf("tool definition cannot be nil")
	}

	paramSchema := schema.NewParameterSchema()

	// Add properties from the definition
	for name, prop := range def.Parameters {
		var propSchema *schema.Schema
		switch prop.Type {
		case "string":
			propSchema = &schema.Schema{
				Type:        schema.TypeString,
				Description: prop.Description,
				Default:     prop.Default,
			}
		case "number":
			propSchema = &schema.Schema{
				Type:        schema.TypeNumber,
				Description: prop.Description,
				Default:     prop.Default,
			}
		case "integer":
			propSchema = &schema.Schema{
				Type:        schema.TypeInteger,
				Description: prop.Description,
				Default:     prop.Default,
			}
		case "boolean":
			propSchema = &schema.Schema{
				Type:        schema.TypeBoolean,
				Description: prop.Description,
				Default:     prop.Default,
			}
		case "array":
			items := &schema.Schema{Type: schema.TypeString} // Default to string array
			if prop.Items != nil {
				items = &schema.Schema{
					Type:        schema.SchemaType(prop.Items.Type),
					Description: prop.Items.Description,
				}
			}
			propSchema = &schema.Schema{
				Type:        schema.TypeArray,
				Description: prop.Description,
				Items:       items,
				Default:     prop.Default,
			}
		case "object":
			properties := make(map[string]*schema.Schema)
			if prop.Properties != nil {
				for subName, subProp := range prop.Properties {
					properties[subName] = &schema.Schema{
						Type:        schema.SchemaType(subProp.Type),
						Description: subProp.Description,
						Default:     subProp.Default,
					}
				}
			}
			propSchema = &schema.Schema{
				Type:        schema.TypeObject,
				Description: prop.Description,
				Properties:  properties,
				Default:     prop.Default,
			}
		default:
			log.Debugf("Unknown property type '%s' for parameter '%s', defaulting to string",
				prop.Type, name)
			propSchema = &schema.Schema{
				Type:        schema.TypeString, // Default to string
				Description: prop.Description,
				Default:     prop.Default,
			}
		}

		if len(prop.Enum) > 0 {
			propSchema.Enum = prop.Enum
		}

		paramSchema.AddProperty(name, propSchema, prop.Required)
	}

	return paramSchema, nil
}

// ValidateParameters validates the input parameters against the tool's parameter schema.
// This function provides better error messages and parameter validation than the default
// ConvertArgumentsToCorrectTypes function.
func ValidateParameters(input map[string]interface{}, tool Tool) (map[string]interface{}, error) {
	if tool == nil {
		return nil, fmt.Errorf("tool cannot be nil")
	}

	// Check for nested structure with tool_name and tool_input pattern
	if toolInput, hasToolInput := input["tool_input"].(map[string]interface{}); hasToolInput {
		// If we have a nested structure, use the inner map
		log.Debugf("Found nested tool_input structure, using inner parameters")
		input = toolInput
	}

	def := tool.GetDefinition()
	if def == nil {
		// Fall back to simpler parameter validation
		return ConvertArgumentsToCorrectTypes(input, tool.Parameters())
	}

	// Convert the definition to a schema
	paramSchema, err := ConvertDefinitionToSchema(def)
	if err != nil {
		log.Warnf("Failed to convert tool definition to schema: %v", err)
		// Fall back to simpler parameter validation
		return ConvertArgumentsToCorrectTypes(input, tool.Parameters())
	}

	// Validate and convert the parameters
	validatedInput, err := paramSchema.ValidateAndConvert(input)
	if err != nil {
		// Add more context to the error
		return nil, fmt.Errorf("parameter validation for tool '%s' failed: %w", tool.Name(), err)
	}

	log.Debugf("Successfully validated and converted parameters for tool '%s'", tool.Name())
	return validatedInput, nil
}
