// Package tools provides implementation of various tools.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// MCPTool is a tool that wraps an MCP tool.
type MCPTool struct {
	tool.BaseTool
	mcpTool    mcp.Tool
	mcpClient  *mcp.Client
	sessionMgr *MCPSessionManager
	schema     map[string]interface{}
	executor   tool.Executor
}

// NewMCPTool creates a new MCP tool.
func NewMCPTool(mcpTool mcp.Tool, mcpClient *mcp.Client, sessionMgr *MCPSessionManager, schema map[string]interface{}, executor tool.Executor) *MCPTool {
	// Convert MCP tool schema to JSON Schema for the tool parameters
	params := convertSchemaToParameters(mcpTool.InputSchema)

	return &MCPTool{
		BaseTool:   *tool.NewBaseTool(mcpTool.Name, mcpTool.Description, params),
		mcpTool:    mcpTool,
		mcpClient:  mcpClient,
		sessionMgr: sessionMgr,
		schema:     schema,
		executor:   executor,
	}
}

// Execute executes the MCP tool.
func (t *MCPTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Log the tool call
	log.Infof("Executing MCP tool %s with arguments: %v\n", t.Name(), args)

	// Normalize parameters to a standard format regardless of input method
	normalizedParams := make(map[string]interface{})

	// STEP 1: Extract nested parameters from tool_input if present
	if toolInput, hasToolInput := args["tool_input"]; hasToolInput {
		// Case 1a: tool_input is a map
		if inputMap, isMap := toolInput.(map[string]interface{}); isMap {
			log.Infof("Found nested tool_input object, extracting parameters")
			for k, v := range inputMap {
				normalizedParams[k] = v
			}
			// Case 1b: tool_input is a JSON string
		} else if inputStr, isStr := toolInput.(string); isStr && inputStr != "" {
			var jsonMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputStr), &jsonMap); err == nil {
				log.Infof("Parsed tool_input JSON string into parameters")
				for k, v := range jsonMap {
					normalizedParams[k] = v
				}
			} else {
				// Case 1c: tool_input is a direct string value
				inferredParam := inferParameterName(t.mcpTool.Name, t.Parameters())
				if inferredParam != "" {
					normalizedParams[inferredParam] = inputStr
					log.Infof("Mapped direct string tool_input to parameter %s", inferredParam)
				} else {
					log.Warnf("Couldn't infer parameter name for tool_input string, using default 'input'")
					normalizedParams["input"] = inputStr
				}
			}
		} else if toolInput == nil {
			// Handle cases where tool_input is null but present
			log.Infof("tool_input is present but null, searching for parameters at top level")
			// We'll handle this in STEP 2 by gathering top-level params
		} else {
			log.Warnf("Unexpected type for tool_input: %T", toolInput)
		}
	}

	// STEP 2: Process direct arguments not in tool_input
	for k, v := range args {
		// Skip special keys that aren't actual parameters
		if k == "tool_name" || k == "tool_input" {
			continue
		}

		// Only add if not already set from tool_input
		if _, exists := normalizedParams[k]; !exists {
			normalizedParams[k] = v
		}
	}

	// STEP 3: Handle case where there's a single direct string parameter
	// (e.g., {"location": "Beijing"} or just direct "Beijing")
	if len(normalizedParams) == 0 && len(args) == 1 {
		for k, v := range args {
			if k != "tool_name" && k != "tool_input" {
				normalizedParams[k] = v
				log.Infof("Used direct parameter %s: %v", k, v)
				break
			} else if strValue, isStr := v.(string); isStr && k != "tool_name" {
				inferredParam := inferParameterName(t.mcpTool.Name, t.Parameters())
				if inferredParam != "" {
					normalizedParams[inferredParam] = strValue
					log.Infof("Mapped direct string value to parameter %s", inferredParam)
				}
			}
		}
	}

	// STEP 4: Apply parameter name normalization (e.g., city→location)
	normalizedParams = mapParameterNames(normalizedParams, t.mcpTool.Name)

	// STEP 5: Validate parameters against the schema
	if err := validateAgainstSchema(normalizedParams, t.Parameters()); err != nil {
		log.Warnf("Parameter validation failed: %v", err)

		// Try one more time with inference if missing required params
		if hasEmptyRequiredParams(normalizedParams, t.Parameters()) {
			// Try to infer missing parameters from context
			contextParams := inferParametersFromContext(ctx, t.mcpTool.Name, t.Parameters())

			// Only add parameters that are missing
			for param, value := range contextParams {
				if _, exists := normalizedParams[param]; !exists {
					normalizedParams[param] = value
					log.Infof("Inferred missing parameter '%s' from context: %v", param, value)
				}
			}

			// One more attempt: extract relevant values from the original query if present in the context
			if hasEmptyRequiredParams(normalizedParams, t.Parameters()) {
				query := extractQueryFromContext(ctx)
				if query != "" {
					// Try to extract entities from the query based on parameter types
					if props, ok := t.Parameters()["properties"].(map[string]interface{}); ok {
						for paramName, propDef := range props {
							if normalizedParams[paramName] != nil {
								continue // Skip if already has a value
							}

							if propMap, ok := propDef.(map[string]interface{}); ok {
								propType, _ := propMap["type"].(string)
								if propType == "string" {
									// For parameters that might represent locations
									if stringsContainFragment(paramName, []string{"location", "place", "city"}) {
										locations := extractLocationsFromQuery(query)
										if len(locations) > 0 {
											normalizedParams[paramName] = locations[0]
											log.Infof("Extracted location '%s' from query for parameter '%s'",
												locations[0], paramName)
											continue
										}
									}
								}
							}
						}
					}
				}
			}

			// Validate again after inference
			if err := validateAgainstSchema(normalizedParams, t.Parameters()); err != nil {
				return nil, fmt.Errorf("parameter validation failed: %w", err)
			}
		} else {
			return nil, fmt.Errorf("parameter validation failed: %w", err)
		}
	}

	// Check if we have a valid client
	if t.mcpClient == nil {
		if t.sessionMgr != nil {
			// Try to reinitialize the session
			var err error
			t.mcpClient, err = t.sessionMgr.CreateSession(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to create MCP session: %w", err)
			}
		} else {
			return nil, fmt.Errorf("MCP client not available")
		}
	}

	// Log the final parameters being sent to the tool
	log.Infof("Calling MCP tool %s with normalized parameters: %v", t.Name(), normalizedParams)

	// Call the MCP tool
	result, err := t.mcpClient.CallTool(ctx, t.Name(), normalizedParams)
	if err != nil {
		log.Infof("MCP tool %s execution failed: %v\n", t.Name(), err)
		return nil, fmt.Errorf("MCP tool execution failed: %w", err)
	}

	if result.IsError {
		// Enhance error messages with more detail about the failure
		errorMsg := fmt.Sprintf("MCP tool returned error: %v", result.Content)
		log.Infof("%s\n", errorMsg)

		// Extract parameter information from the result if available
		paramInfo := extractParameterInfoFromError(result.Content)
		if paramInfo != "" {
			return nil, fmt.Errorf("%s\nParameter information: %s", errorMsg, paramInfo)
		}

		return nil, fmt.Errorf("%s", errorMsg)
	}

	// Process the result content
	output := extractResultContent(result.Content)

	log.Infof("MCP tool %s execution successful: %v\n", t.Name(), output)

	// Return the result
	return tool.NewJSONResult(output), nil
}

// validateAgainstSchema validates arguments against the tool's schema
func validateAgainstSchema(args map[string]interface{}, schema map[string]interface{}) error {
	// Check for required parameters
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		if required, ok := schema["required"].([]interface{}); ok {
			for _, reqField := range required {
				if reqFieldStr, ok := reqField.(string); ok {
					if _, exists := args[reqFieldStr]; !exists {
						if propInfo, hasProp := properties[reqFieldStr].(map[string]interface{}); hasProp {
							description := ""
							if desc, hasDesc := propInfo["description"].(string); hasDesc {
								description = desc
							}
							return fmt.Errorf("missing required parameter '%s': %s", reqFieldStr, description)
						}
						return fmt.Errorf("missing required parameter '%s'", reqFieldStr)
					}
				}
			}
		} else if required, ok := schema["required"].([]string); ok {
			for _, reqField := range required {
				if _, exists := args[reqField]; !exists {
					if propInfo, hasProp := properties[reqField].(map[string]interface{}); hasProp {
						description := ""
						if desc, hasDesc := propInfo["description"].(string); hasDesc {
							description = desc
						}
						return fmt.Errorf("missing required parameter '%s': %s", reqField, description)
					}
					return fmt.Errorf("missing required parameter '%s'", reqField)
				}
			}
		}

		// Validate parameter types
		for paramName, paramValue := range args {
			if propInfo, exists := properties[paramName].(map[string]interface{}); exists {
				if expectedType, hasType := propInfo["type"].(string); hasType {
					if err := validateParameterType(paramName, paramValue, expectedType); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// validateParameterType validates that a parameter value matches its expected type
func validateParameterType(paramName string, paramValue interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := paramValue.(string); !ok {
			return fmt.Errorf("parameter '%s' must be a string", paramName)
		}
	case "integer":
		// For JSON unmarshaled from LLM, numbers often come as float64
		if num, ok := paramValue.(float64); ok {
			// Check if it's an integer value
			if num != float64(int(num)) {
				return fmt.Errorf("parameter '%s' must be an integer", paramName)
			}
		} else if _, ok := paramValue.(int); !ok {
			return fmt.Errorf("parameter '%s' must be an integer", paramName)
		}
	case "number":
		if _, ok := paramValue.(float64); !ok {
			if _, ok := paramValue.(int); !ok {
				return fmt.Errorf("parameter '%s' must be a number", paramName)
			}
		}
	case "boolean":
		if _, ok := paramValue.(bool); !ok {
			return fmt.Errorf("parameter '%s' must be a boolean", paramName)
		}
	case "array":
		if _, ok := paramValue.([]interface{}); !ok {
			return fmt.Errorf("parameter '%s' must be an array", paramName)
		}
	case "object":
		if _, ok := paramValue.(map[string]interface{}); !ok {
			return fmt.Errorf("parameter '%s' must be an object", paramName)
		}
	}

	return nil
}

// extractParameterInfoFromError tries to extract parameter information from an error response
func extractParameterInfoFromError(contents []mcp.Content) string {
	if len(contents) == 0 {
		return ""
	}

	// Look for parameter-related error messages in the content
	for _, content := range contents {
		if textContent, ok := content.(mcp.TextContent); ok {
			text := textContent.Text

			// Look for common parameter error indicators
			if strings.Contains(text, "parameter") ||
				strings.Contains(text, "required") ||
				strings.Contains(text, "missing") ||
				strings.Contains(text, "invalid") {
				return text
			}
		}
	}

	return ""
}

// convertSchemaToParameters converts a schema to a JSON Schema for tool parameters.
func convertSchemaToParameters(schema interface{}) map[string]interface{} {
	// Create a basic JSON Schema structure
	params := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}

	if schemaMap, ok := schema.(map[string]interface{}); ok {
		// Process properties
		if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
			properties := params["properties"].(map[string]interface{})

			// Enhanced property mapping with better type handling
			for propName, propValue := range props {
				if propObj, ok := propValue.(map[string]interface{}); ok {
					// Create a cleaned property definition
					cleanProp := map[string]interface{}{}

					// Copy relevant fields
					if propType, ok := propObj["type"]; ok {
						cleanProp["type"] = propType
					}
					if description, ok := propObj["description"]; ok {
						cleanProp["description"] = description
					}
					if format, ok := propObj["format"]; ok {
						cleanProp["format"] = format
					}
					if enum, ok := propObj["enum"]; ok {
						cleanProp["enum"] = enum
					}
					if defaultValue, ok := propObj["default"]; ok {
						cleanProp["default"] = defaultValue
					}

					properties[propName] = cleanProp
				} else {
					// If not an object, just include it as-is
					properties[propName] = propValue
				}
			}
		}

		// Process required fields
		if required, ok := schemaMap["required"].([]interface{}); ok {
			requiredParams := make([]string, 0, len(required))
			for _, req := range required {
				if reqStr, ok := req.(string); ok {
					requiredParams = append(requiredParams, reqStr)
				}
			}
			params["required"] = requiredParams
		} else if required, ok := schemaMap["required"].([]string); ok {
			params["required"] = required
		}
	}

	return params
}

// extractResultContent extracts content from the MCP tool result.
func extractResultContent(contents []mcp.Content) interface{} {
	if len(contents) == 0 {
		return nil
	}

	// Combine all text content
	var textResults []string

	for _, content := range contents {
		if textContent, ok := content.(mcp.TextContent); ok {
			textResults = append(textResults, textContent.Text)
		} else {
			// For non-text content, add a placeholder
			textResults = append(textResults, fmt.Sprintf("[Unsupported content type: %T]", content))
		}
	}

	// If there's only one result, return it directly
	if len(textResults) == 1 {
		return textResults[0]
	}

	// Otherwise return the array
	return textResults
}

// inferParameterName attempts to infer the primary parameter name for a tool based on its schema
func inferParameterName(toolName string, schema map[string]interface{}) string {
	// First try to find parameter from schema
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		// 1. Check required parameters first
		if required, ok := schema["required"].([]string); ok && len(required) > 0 {
			// Return the first required parameter
			return required[0]
		} else if required, ok := schema["required"].([]interface{}); ok && len(required) > 0 {
			// Handle case where required is []interface{} instead of []string
			if reqStr, ok := required[0].(string); ok {
				return reqStr
			}
		}

		// 2. Try to find a parameter with a descriptive name that matches common parameter roles
		commonParams := []string{"query", "input", "location", "text", "value"}
		for _, paramName := range commonParams {
			if _, exists := props[paramName]; exists {
				return paramName
			}
		}

		// 3. If no obvious matches, prefer string parameters
		for name, prop := range props {
			if propMap, ok := prop.(map[string]interface{}); ok {
				if propType, ok := propMap["type"].(string); ok && propType == "string" {
					return name
				}
			}
		}

		// 4. Last resort - just return any parameter
		for name := range props {
			return name
		}
	}

	// Default fallback - just return a generic parameter name
	return "input" // Generic default for all tools
}

// mapParameterNames applies common parameter name mappings based on parameter semantics
func mapParameterNames(args map[string]interface{}, toolName string) map[string]interface{} {
	// Clone the args to avoid modifying the original
	result := make(map[string]interface{}, len(args))
	for k, v := range args {
		result[k] = v
	}

	// Define canonical parameter names and their common aliases
	// This approach focuses on parameter semantics rather than tool categories
	paramAliases := map[string][]string{
		// Location-related parameters
		"location": {"city", "city_name", "place", "location_name", "address", "destination"},

		// Query-related parameters
		"query": {"term", "keyword", "search", "find", "text", "question", "content", "search_term"},

		// Value-related parameters
		"value": {"amount", "number", "quantity", "data", "input"},
	}

	// Apply mappings based on parameter semantics
	for canonicalName, aliases := range paramAliases {
		// Check if the canonical parameter is already present
		if _, hasCanonical := result[canonicalName]; !hasCanonical {
			// Look for aliases in the arguments
			for _, alias := range aliases {
				if aliasValue, hasAlias := args[alias]; hasAlias {
					result[canonicalName] = aliasValue
					delete(result, alias)
					log.Infof("Mapped parameter '%s' to canonical name '%s'", alias, canonicalName)
					break // Stop after finding the first match for this canonical name
				}
			}
		}
	}

	return result
}

// hasEmptyRequiredParams checks if any required parameters are missing
func hasEmptyRequiredParams(args map[string]interface{}, schema map[string]interface{}) bool {
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		// Check required parameters
		if required, ok := schema["required"].([]interface{}); ok {
			for _, reqField := range required {
				if reqFieldStr, ok := reqField.(string); ok {
					if _, exists := args[reqFieldStr]; !exists {
						// Check if this required field has default value in properties
						if propInfo, hasProp := properties[reqFieldStr]; hasProp {
							if propMap, isPropMap := propInfo.(map[string]interface{}); isPropMap {
								if _, hasDefault := propMap["default"]; hasDefault {
									// Skip if the property has a default value
									continue
								}
							}
						}
						return true
					}
				}
			}
		} else if required, ok := schema["required"].([]string); ok {
			for _, reqField := range required {
				if _, exists := args[reqField]; !exists {
					// Check if this required field has default value in properties
					if propInfo, hasProp := properties[reqField]; hasProp {
						if propMap, isPropMap := propInfo.(map[string]interface{}); isPropMap {
							if _, hasDefault := propMap["default"]; hasDefault {
								// Skip if the property has a default value
								continue
							}
						}
					}
					return true
				}
			}
		}
	}
	return false
}

// inferParametersFromContext extracts relevant parameters from context
func inferParametersFromContext(ctx context.Context, toolName string, schema map[string]interface{}) map[string]interface{} {
	params := make(map[string]interface{})

	// Extract query from context if available
	query := extractQueryFromContext(ctx)
	if query == "" {
		return params
	}

	// Get required parameters from schema
	requiredParams := getRequiredParamsFromSchema(schema)
	if len(requiredParams) == 0 {
		return params
	}

	// Try to extract potential entities from query based on schema's parameter types
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		// Check each required parameter to see if we can infer its value
		for _, paramName := range requiredParams {
			if prop, ok := props[paramName]; ok {
				if propMap, ok := prop.(map[string]interface{}); ok {
					// Handle string parameters
					if propType, ok := propMap["type"].(string); ok && propType == "string" {
						// Check if parameter name suggests it might be a location
						if stringsContainFragment(paramName, []string{"location", "place", "city", "where"}) {
							locations := extractLocationsFromQuery(query)
							if len(locations) > 0 {
								params[paramName] = locations[0]
								continue
							}
						}
					}
				}
			}
		}
	}

	return params
}

// stringsContainFragment checks if any string in the list contains the fragment
func stringsContainFragment(fragment string, list []string) bool {
	lowerFragment := strings.ToLower(fragment)
	for _, item := range list {
		if strings.Contains(lowerFragment, strings.ToLower(item)) {
			return true
		}
	}
	return false
}

// extractQueryFromContext gets the user query from context
func extractQueryFromContext(ctx context.Context) string {
	// Try to extract the query from context values
	if queryVal := ctx.Value("user_query"); queryVal != nil {
		if query, ok := queryVal.(string); ok {
			return query
		}
	}

	// If not available through context values, check any stored messages
	if messagesVal := ctx.Value("messages"); messagesVal != nil {
		if messages, ok := messagesVal.([]interface{}); ok && len(messages) > 0 {
			// Try to find the most recent user message
			for i := len(messages) - 1; i >= 0; i-- {
				if msg, ok := messages[i].(map[string]interface{}); ok {
					if role, hasRole := msg["role"].(string); hasRole && role == "user" {
						if content, hasContent := msg["content"].(string); hasContent {
							return content
						}
					}
				}
			}
		}
	}

	return ""
}

// extractLocationsFromQuery extracts potential location names from a query
func extractLocationsFromQuery(query string) []string {
	query = strings.ToLower(query)
	words := strings.Fields(query)

	// Common location-related words that might precede a location
	locationPrefixes := []string{"in", "at", "near", "from", "to"}

	var locations []string

	// Look for words after location indicators
	for i, word := range words {
		for _, prefix := range locationPrefixes {
			if word == prefix && i < len(words)-1 {
				location := words[i+1]
				// Remove any punctuation
				location = strings.Trim(location, ",.?!:;()")
				locations = append(locations, location)
			}
		}
	}

	return locations
}

// findLocationParameterName finds the parameter that likely represents a location
func findLocationParameterName(schema map[string]interface{}) string {
	locationParams := []string{"location", "city", "place", "address", "destination"}

	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		// First check for exact matches
		for _, name := range locationParams {
			if _, exists := properties[name]; exists {
				return name
			}
		}

		// Then check for partial matches
		for paramName := range properties {
			paramLower := strings.ToLower(paramName)
			for _, locParam := range locationParams {
				if strings.Contains(paramLower, locParam) {
					return paramName
				}
			}
		}

		// If no matches, return the first string parameter
		for paramName, paramSchema := range properties {
			if paramDef, ok := paramSchema.(map[string]interface{}); ok {
				if paramType, ok := paramDef["type"].(string); ok && paramType == "string" {
					return paramName
				}
			}
		}
	}

	// Fallback to "location" as a default
	return "location"
}

// getRequiredParamsFromSchema extracts required parameters from a schema
func getRequiredParamsFromSchema(schema map[string]interface{}) []string {
	var required []string

	if req, ok := schema["required"].([]interface{}); ok {
		for _, r := range req {
			if str, ok := r.(string); ok {
				required = append(required, str)
			}
		}
	} else if req, ok := schema["required"].([]string); ok {
		required = req
	}

	return required
}
