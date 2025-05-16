// Package tool provides interfaces and implementations for tools that agents can use.
package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParameterInfo holds information about a tool parameter
type ParameterInfo struct {
	Name        string
	Type        string
	Description string
	Required    bool
	Default     interface{}
	Enum        []interface{}
}

// SchemaProcessor provides utilities for extracting parameter information from tool schemas
type SchemaProcessor struct {
	tools []Tool
}

// NewSchemaProcessor creates a new SchemaProcessor with the given tools
func NewSchemaProcessor(tools []Tool) *SchemaProcessor {
	return &SchemaProcessor{
		tools: tools,
	}
}

// GetParametersForTool extracts parameter information for a tool by name
func (sp *SchemaProcessor) GetParametersForTool(toolName string) ([]ParameterInfo, error) {
	for _, t := range sp.tools {
		if t.Name() == toolName {
			return sp.ExtractParameterInfo(t.Parameters())
		}
	}
	return nil, fmt.Errorf("tool not found: %s", toolName)
}

// ExtractParameterInfo extracts parameter information from a JSON Schema
func (sp *SchemaProcessor) ExtractParameterInfo(schema map[string]interface{}) ([]ParameterInfo, error) {
	var result []ParameterInfo

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid schema: properties field missing or not an object")
	}

	// Extract required fields
	var requiredFields []string
	if required, ok := schema["required"].([]interface{}); ok {
		for _, r := range required {
			if str, ok := r.(string); ok {
				requiredFields = append(requiredFields, str)
			}
		}
	} else if required, ok := schema["required"].([]string); ok {
		requiredFields = required
	}

	// Create a set of required fields for faster lookup
	requiredSet := make(map[string]bool)
	for _, r := range requiredFields {
		requiredSet[r] = true
	}

	// Process each property
	for name, propInterface := range properties {
		prop, ok := propInterface.(map[string]interface{})
		if !ok {
			continue
		}

		info := ParameterInfo{
			Name:     name,
			Required: requiredSet[name],
		}

		// Extract type
		if typeVal, ok := prop["type"].(string); ok {
			info.Type = typeVal
		}

		// Extract description
		if descVal, ok := prop["description"].(string); ok {
			info.Description = descVal
		}

		// Extract default value
		if defaultVal, ok := prop["default"]; ok {
			info.Default = defaultVal
		}

		// Extract enum values
		if enumVal, ok := prop["enum"].([]interface{}); ok {
			info.Enum = enumVal
		}

		result = append(result, info)
	}

	return result, nil
}

// FindPrimaryParameterName identifies the primary parameter for a tool based on its schema
func (sp *SchemaProcessor) FindPrimaryParameterName(toolName string) (string, error) {
	params, err := sp.GetParametersForTool(toolName)
	if err != nil {
		return "", err
	}

	if len(params) == 0 {
		return "", fmt.Errorf("no parameters found for tool: %s", toolName)
	}

	// Strategy 1: If there's only one parameter, that's the primary one
	if len(params) == 1 {
		return params[0].Name, nil
	}

	// Strategy 2: Look for required parameters first
	var requiredParams []ParameterInfo
	for _, p := range params {
		if p.Required {
			requiredParams = append(requiredParams, p)
		}
	}

	if len(requiredParams) == 1 {
		return requiredParams[0].Name, nil
	}

	// Strategy 3: Look for parameters with names or descriptions that suggest they're primary
	primaryIndicators := []string{"query", "input", "text", "content", "location", "city", "search", "prompt"}

	// First check names
	for _, indicator := range primaryIndicators {
		for _, p := range params {
			if strings.Contains(strings.ToLower(p.Name), indicator) {
				return p.Name, nil
			}
		}
	}

	// Then check descriptions
	for _, indicator := range primaryIndicators {
		for _, p := range params {
			if strings.Contains(strings.ToLower(p.Description), indicator) {
				return p.Name, nil
			}
		}
	}

	// Strategy 4: If there are required parameters, use the first one
	if len(requiredParams) > 0 {
		return requiredParams[0].Name, nil
	}

	// Strategy 5: Fall back to the first parameter
	return params[0].Name, nil
}

// ParseStructuredArgument attempts to parse a structured argument from text
func ParseStructuredArgument(text string) (map[string]interface{}, error) {
	// Check if it might be JSON
	if strings.HasPrefix(strings.TrimSpace(text), "{") && strings.HasSuffix(strings.TrimSpace(text), "}") {
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(text), &result); err == nil {
			return result, nil
		}
	}

	// Try to parse as key-value pairs
	// Try different separators and formats
	formats := []struct {
		lineSep     string
		keyValueSep string
		valQuoted   bool
	}{
		{",", "=", false},  // key=value, key2=value2
		{",", ":", false},  // key:value, key2:value2
		{"\n", "=", false}, // key=value\nkey2=value2
		{"\n", ":", false}, // key:value\nkey2:value2
		{";", "=", false},  // key=value; key2=value2
		{";", ":", false},  // key:value; key2:value2
	}

	for _, format := range formats {
		lines := strings.Split(text, format.lineSep)
		if len(lines) < 2 && !strings.Contains(text, format.keyValueSep) {
			continue
		}

		tempResult := make(map[string]interface{})
		success := true

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			parts := strings.SplitN(line, format.keyValueSep, 2)
			if len(parts) != 2 {
				success = false
				break
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// Remove quotes if present
			if format.valQuoted && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
				value = value[1 : len(value)-1]
			}

			tempResult[key] = value
		}

		if success && len(tempResult) > 0 {
			return tempResult, nil
		}
	}

	// If we can't parse structured data, return an error
	return nil, fmt.Errorf("could not parse structured argument from text")
}

// ConvertArgumentsToCorrectTypes attempts to convert argument values to their expected types
// based on the parameter schema
func ConvertArgumentsToCorrectTypes(args map[string]interface{}, schema map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// Check for nested structure with tool_name and tool_input pattern
	if toolInput, hasToolInput := args["tool_input"].(map[string]interface{}); hasToolInput {
		// If we have a nested structure, use the inner map
		args = toolInput
	}

	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return args, nil
	}

	// Track conversion errors for better error reporting
	var conversionErrors []string

	for key, value := range args {
		prop, ok := properties[key].(map[string]interface{})
		if !ok {
			// If the property isn't defined in the schema, pass it through unchanged
			result[key] = value
			continue
		}

		// Get the expected type
		typeStr, ok := prop["type"].(string)
		if !ok {
			// If no type is specified, pass it through unchanged
			result[key] = value
			continue
		}

		// Try to convert the value to the expected type
		convertedValue, err := convertToType(value, typeStr)
		if err != nil {
			// Collect conversion errors but continue processing
			conversionErrors = append(conversionErrors,
				fmt.Sprintf("parameter '%s': %v", key, err))
			// Preserve the original value
			result[key] = value
		} else {
			result[key] = convertedValue
		}
	}

	// If we had conversion errors, include them in the return error
	if len(conversionErrors) > 0 {
		return result, fmt.Errorf("type conversion errors: %s", strings.Join(conversionErrors, "; "))
	}

	return result, nil
}

// convertToType converts a value to the specified type
func convertToType(value interface{}, targetType string) (interface{}, error) {
	// Handle string values (most common from LLM responses)
	if strValue, ok := value.(string); ok {
		switch targetType {
		case "string":
			return strValue, nil
		case "number", "integer":
			// First, try direct parsing with fmt.Sscanf
			var numValue float64
			if _, err := fmt.Sscanf(strValue, "%f", &numValue); err == nil {
				if targetType == "integer" {
					return int(numValue), nil
				}
				return numValue, nil
			}

			// Next, try handling currency symbols and common patterns
			cleanedValue := cleanNumberString(strValue)
			if _, err := fmt.Sscanf(cleanedValue, "%f", &numValue); err == nil {
				if targetType == "integer" {
					return int(numValue), nil
				}
				return numValue, nil
			}

			return nil, fmt.Errorf("cannot convert %q to %s", strValue, targetType)
		case "boolean":
			lower := strings.ToLower(strValue)
			switch lower {
			case "true", "yes", "y", "1", "on", "enable", "enabled":
				return true, nil
			case "false", "no", "n", "0", "off", "disable", "disabled":
				return false, nil
			default:
				return nil, fmt.Errorf("cannot convert %q to boolean", strValue)
			}
		case "array":
			// Try to parse as JSON array first
			if strings.HasPrefix(strValue, "[") && strings.HasSuffix(strValue, "]") {
				var arrayValue []interface{}
				if err := json.Unmarshal([]byte(strValue), &arrayValue); err == nil {
					return arrayValue, nil
				}
			}

			// Then try comma-separated values
			items := strings.Split(strValue, ",")
			result := make([]interface{}, len(items))
			for i, item := range items {
				result[i] = strings.TrimSpace(item)
			}
			return result, nil
		case "object":
			// Try to parse as JSON
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(strValue), &result); err == nil {
				return result, nil
			}

			// If it's not valid JSON, try to parse key-value pairs
			// First as comma-separated pairs
			if !strings.Contains(strValue, "{") && strings.Contains(strValue, ":") {
				result = make(map[string]interface{})
				pairs := strings.Split(strValue, ",")
				for _, pair := range pairs {
					parts := strings.SplitN(pair, ":", 2)
					if len(parts) == 2 {
						key := strings.TrimSpace(parts[0])
						value := strings.TrimSpace(parts[1])
						result[key] = value
					}
				}
				if len(result) > 0 {
					return result, nil
				}
			}

			return nil, fmt.Errorf("cannot convert %q to object", strValue)
		}
	} else if numValue, ok := value.(float64); ok {
		// Convert number types appropriately
		switch targetType {
		case "integer":
			return int(numValue), nil
		case "number":
			return numValue, nil
		case "string":
			return fmt.Sprintf("%g", numValue), nil
		case "boolean":
			// 0 is false, anything else is true
			return numValue != 0, nil
		}
	} else if intValue, ok := value.(int); ok {
		// Convert integer types
		switch targetType {
		case "integer":
			return intValue, nil
		case "number":
			return float64(intValue), nil
		case "string":
			return fmt.Sprintf("%d", intValue), nil
		case "boolean":
			return intValue != 0, nil
		}
	} else if boolValue, ok := value.(bool); ok {
		// Convert boolean types
		switch targetType {
		case "boolean":
			return boolValue, nil
		case "string":
			return fmt.Sprintf("%t", boolValue), nil
		case "integer":
			if boolValue {
				return 1, nil
			}
			return 0, nil
		case "number":
			if boolValue {
				return 1.0, nil
			}
			return 0.0, nil
		}
	} else if arrayValue, ok := value.([]interface{}); ok {
		// Handle array values
		switch targetType {
		case "array":
			return arrayValue, nil
		case "string":
			// Convert array to JSON string
			bytes, err := json.Marshal(arrayValue)
			if err != nil {
				return nil, fmt.Errorf("failed to convert array to string: %v", err)
			}
			return string(bytes), nil
		}
	} else if mapValue, ok := value.(map[string]interface{}); ok {
		// Handle object/map values
		switch targetType {
		case "object":
			return mapValue, nil
		case "string":
			// Convert map to JSON string
			bytes, err := json.Marshal(mapValue)
			if err != nil {
				return nil, fmt.Errorf("failed to convert object to string: %v", err)
			}
			return string(bytes), nil
		}
	}

	// If no specific conversion path, just return the original value and log a warning
	return value, fmt.Errorf("no conversion available from %T to %s", value, targetType)
}

// cleanNumberString cleans a string representing a number by removing currency symbols and formatting
func cleanNumberString(s string) string {
	// Remove common currency symbols
	currencySymbols := []string{"$", "€", "£", "¥", "₹", "₽", "₩", "₿", "฿"}
	for _, symbol := range currencySymbols {
		s = strings.ReplaceAll(s, symbol, "")
	}

	// Remove commas used as thousand separators and spaces
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")

	// Handle percentage values
	if strings.HasSuffix(s, "%") {
		// Convert percentage to decimal (e.g., "50%" -> "0.5")
		s = strings.TrimSuffix(s, "%")
		if val, err := strconv.ParseFloat(s, 64); err == nil {
			return fmt.Sprintf("%f", val/100)
		}
	}

	return s
}
