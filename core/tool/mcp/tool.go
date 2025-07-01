package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// mcpTool implements the Tool interface for MCP tools.
type mcpTool struct {
	mcpToolRef     *mcp.Tool
	inputSchema    *tool.Schema
	sessionManager *mcpSessionManager
	retryConfig    *RetryConfig
	diagnostics    *errorDiagnostic
}

// newMCPTool creates a new MCP tool wrapper.
func newMCPTool(mcpToolData mcp.Tool, sessionManager *mcpSessionManager, retryConfig *RetryConfig) *mcpTool {
	mcpTool := &mcpTool{
		mcpToolRef:     &mcpToolData,
		sessionManager: sessionManager,
		retryConfig:    retryConfig,
		diagnostics:    newErrorDiagnostic(),
	}

	// Convert MCP input schema to inner Schema.
	if mcpToolData.InputSchema != nil {
		mcpTool.inputSchema = convertMCPSchemaToSchema(mcpToolData.InputSchema)
	}

	return mcpTool
}

// Call implements the Tool interface.
func (t *mcpTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	log.Debug("Calling MCP tool", "name", t.mcpToolRef.Name)

	// Parse raw arguments.
	var rawArguments map[string]interface{}
	if len(jsonArgs) > 0 {
		if err := json.Unmarshal(jsonArgs, &rawArguments); err != nil {
			return nil, fmt.Errorf("failed to parse tool arguments: %w", err)
		}
	} else {
		rawArguments = make(map[string]interface{})
	}

	// Apply intelligent parameter processing.
	normalizedParams, err := t.normalizeParameters(ctx, rawArguments)
	if err != nil {
		// Enhanced error diagnosis for parameter processing
		diagInfo := diagnosticInfo{
			ToolName:       t.mcpToolRef.Name,
			Operation:      "parameter_processing",
			ProvidedArgs:   rawArguments,
			ExpectedSchema: t.mcpToolRef.InputSchema,
		}
		if toolCtx, ok := GetToolContext(ctx); ok {
			diagInfo.SessionContext = toolCtx
		}

		mcpErr := t.diagnostics.analyzeError(err, diagInfo)
		t.diagnostics.logError(mcpErr)
		return nil, mcpErr
	}

	// Validate parameters against schema.
	if err := t.validateParameters(normalizedParams); err != nil {
		// Enhanced error diagnosis for parameter validation.
		diagInfo := diagnosticInfo{
			ToolName:       t.mcpToolRef.Name,
			Operation:      "parameter_validation",
			ProvidedArgs:   normalizedParams,
			ExpectedSchema: t.mcpToolRef.InputSchema,
		}
		if toolCtx, ok := GetToolContext(ctx); ok {
			diagInfo.SessionContext = toolCtx
		}

		mcpErr := t.diagnostics.analyzeError(err, diagInfo)
		t.diagnostics.logError(mcpErr)
		return nil, mcpErr
	}

	// Add tool context if available.
	if toolCtx, ok := GetToolContext(ctx); ok {
		log.Debug("Adding tool context to MCP call",
			"agent_id", toolCtx.AgentID,
			"session_id", toolCtx.SessionID,
			"request_id", toolCtx.RequestID)

		// Add context metadata to arguments if not already present.
		if normalizedParams["_context"] == nil {
			normalizedParams["_context"] = map[string]interface{}{
				"agent_id":    toolCtx.AgentID,
				"session_id":  toolCtx.SessionID,
				"user_id":     toolCtx.UserID,
				"request_id":  toolCtx.RequestID,
				"permissions": toolCtx.Permissions,
				"metadata":    toolCtx.Metadata,
			}
		}
	}

	log.Debug("Calling MCP tool with normalized parameters", "name",
		t.mcpToolRef.Name, "params", normalizedParams)

	// Call the tool with retry logic.
	if t.retryConfig != nil && t.retryConfig.Enabled {
		return t.callWithRetry(ctx, normalizedParams)
	}
	return t.callOnce(ctx, normalizedParams)
}

// callOnce performs a single call to the MCP tool.
func (t *mcpTool) callOnce(ctx context.Context, arguments map[string]interface{}) (any, error) {
	content, err := t.sessionManager.callTool(ctx, t.mcpToolRef.Name, arguments)
	if err != nil {
		// Enhanced error diagnosis.
		diagInfo := diagnosticInfo{
			ToolName:       t.mcpToolRef.Name,
			Operation:      "tool_call",
			ProvidedArgs:   arguments,
			ExpectedSchema: t.mcpToolRef.InputSchema,
			AvailableTools: t.sessionManager.getAvailableToolNames(ctx),
		}

		// Get tool context if available.
		if toolCtx, ok := GetToolContext(ctx); ok {
			diagInfo.SessionContext = toolCtx
		}

		// Add connection info for diagnostics.
		diagInfo.ConnectionInfo = map[string]interface{}{
			"transport":  t.sessionManager.config.Transport,
			"server_url": t.sessionManager.config.ServerURL,
			"command":    t.sessionManager.config.Command,
			"connected":  t.sessionManager.isConnected(),
		}

		// Analyze and enhance the error.
		mcpErr := t.diagnostics.analyzeError(err, diagInfo)
		t.diagnostics.logError(mcpErr) // TODO: implement logError

		return nil, mcpErr
	}

	// Convert MCP content to our return format.
	return convertMCPContentToResult(content), nil
}

// callWithRetry performs a call to the MCP tool with retry logic.
func (t *mcpTool) callWithRetry(ctx context.Context, arguments map[string]interface{}) (any, error) {
	var lastError error
	delay := t.retryConfig.InitialDelay

	for attempt := 1; attempt <= t.retryConfig.MaxAttempts; attempt++ {
		log.Debug("Attempting MCP tool call", "name", t.mcpToolRef.Name, "attempt", attempt)

		result, err := t.callOnce(ctx, arguments)
		if err == nil {
			if attempt > 1 {
				log.Info("MCP tool call succeeded after retry", "name", t.mcpToolRef.Name, "attempt", attempt)
			}
			return result, nil
		}

		lastError = err
		log.Warn("MCP tool call failed", "name", t.mcpToolRef.Name, "attempt", attempt, "error", err)

		// Don't sleep after the last attempt.
		if attempt < t.retryConfig.MaxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// Calculate next delay with exponential backoff.
			delay = time.Duration(float64(delay) * t.retryConfig.BackoffFactor)
			if delay > t.retryConfig.MaxDelay {
				delay = t.retryConfig.MaxDelay
			}
		}
	}

	return nil, fmt.Errorf("MCP tool call failed after %d attempts: %w", t.retryConfig.MaxAttempts, lastError)
}

// Declaration implements the Tool interface.
func (t *mcpTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.mcpToolRef.Name,
		Description: t.mcpToolRef.Description,
		InputSchema: t.inputSchema,
	}
}

// normalizeParameters applies intelligent parameter processing.
func (t *mcpTool) normalizeParameters(ctx context.Context, rawArgs map[string]interface{}) (map[string]interface{}, error) {
	normalizedParams := make(map[string]interface{})

	// STEP 1: Extract nested parameters from tool_input if present.
	if toolInput, hasToolInput := rawArgs["tool_input"]; hasToolInput {
		// Case 1a: tool_input is a map
		if inputMap, isMap := toolInput.(map[string]interface{}); isMap {
			log.Debug("Found nested tool_input object, extracting parameters")
			for k, v := range inputMap {
				normalizedParams[k] = v
			}
		} else if inputStr, isStr := toolInput.(string); isStr && inputStr != "" {
			// Case 1b: tool_input is a JSON string.
			var jsonMap map[string]interface{}
			if err := json.Unmarshal([]byte(inputStr), &jsonMap); err == nil {
				log.Debug("Parsed tool_input JSON string into parameters")
				for k, v := range jsonMap {
					normalizedParams[k] = v
				}
			} else {
				// Case 1c: tool_input is a direct string value.
				inferredParam := t.inferPrimaryParameter()
				if inferredParam != "" {
					normalizedParams[inferredParam] = inputStr
					log.Debug("Mapped direct string tool_input to parameter", "param", inferredParam)
				} else {
					log.Info("Couldn't infer parameter name for tool_input string, using default 'input'")
					normalizedParams["input"] = inputStr
				}
			}
		} else if toolInput == nil {
			// Handle cases where tool_input is null but present.
			log.Debug("tool_input is present but null, searching for parameters at top level")
		} else {
			log.Warn("Unexpected type for tool_input", "type", fmt.Sprintf("%T", toolInput))
		}
	}

	// STEP 2: Process direct arguments not in tool_input.
	for k, v := range rawArgs {
		// Skip special keys that aren't actual parameters.
		if k == "tool_name" || k == "tool_input" {
			continue
		}

		// Only add if not already set from tool_input.
		if _, exists := normalizedParams[k]; !exists {
			normalizedParams[k] = v
		}
	}

	// STEP 3: Handle case where there's a single direct string parameter.
	if len(normalizedParams) == 0 && len(rawArgs) == 1 {
		for k, v := range rawArgs {
			if k != "tool_name" && k != "tool_input" {
				normalizedParams[k] = v
				log.Debug("Used direct parameter", "param", k, "value", v)
				break
			} else if strValue, isStr := v.(string); isStr && k != "tool_name" {
				inferredParam := t.inferPrimaryParameter()
				if inferredParam != "" {
					normalizedParams[inferredParam] = strValue
					log.Debug("Mapped direct string value to parameter", "param", inferredParam)
				}
			}
		}
	}

	// STEP 4: Try to infer missing required parameters from context.
	if t.hasMissingRequiredParams(normalizedParams) {
		contextParams := t.inferParametersFromContext(ctx)
		for param, value := range contextParams {
			if _, exists := normalizedParams[param]; !exists {
				normalizedParams[param] = value
				log.Debug("Inferred missing parameter from context", "param", param, "value", value)
			}
		}
	}

	return normalizedParams, nil
}

// validateParameters validates parameters against the tool's schema.
func (t *mcpTool) validateParameters(args map[string]interface{}) error {
	if t.inputSchema == nil {
		return nil // No schema to validate against
	}

	// Check for required parameters
	for _, reqField := range t.inputSchema.Required {
		if _, exists := args[reqField]; !exists {
			// Get description if available
			description := ""
			if t.inputSchema.Properties != nil && t.inputSchema.Properties[reqField] != nil {
				description = t.inputSchema.Properties[reqField].Description
			}

			if description != "" {
				return fmt.Errorf("missing required parameter '%s': %s", reqField, description)
			}
			return fmt.Errorf("missing required parameter '%s'", reqField)
		}
	}

	// Validate parameter types.
	if t.inputSchema.Properties != nil {
		for paramName, paramValue := range args {
			if propSchema, exists := t.inputSchema.Properties[paramName]; exists {
				if err := t.validateParameterType(paramName, paramValue, propSchema.Type); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// validateParameterType validates that a parameter value matches its expected type.
func (t *mcpTool) validateParameterType(paramName string, paramValue interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := paramValue.(string); !ok {
			return fmt.Errorf("parameter '%s' must be a string", paramName)
		}
	case "integer":
		// For JSON unmarshalled from LLM, numbers often come as float64.
		if num, ok := paramValue.(float64); ok {
			// Check if it's an integer value.
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

// inferPrimaryParameter attempts to infer the primary parameter name for a tool based on its schema.
func (t *mcpTool) inferPrimaryParameter() string {
	if t.inputSchema == nil || t.inputSchema.Properties == nil {
		return "input" // Default fallback
	}

	// 1. Check required parameters first.
	if len(t.inputSchema.Required) > 0 {
		return t.inputSchema.Required[0]
	}

	// 2. Try to find a parameter with a descriptive name that matches common parameter roles.
	commonParams := []string{"query", "input", "location", "text", "value", "message", "content"}
	for _, paramName := range commonParams {
		if _, exists := t.inputSchema.Properties[paramName]; exists {
			return paramName
		}
	}

	// 3. If no obvious matches, prefer string parameters.
	for name, prop := range t.inputSchema.Properties {
		if prop.Type == "string" {
			return name
		}
	}

	// 4. Last resort - just return any parameter.
	for name := range t.inputSchema.Properties {
		return name
	}

	// Final fallback.
	return "input"
}

// hasMissingRequiredParams checks if any required parameters are missing.
func (t *mcpTool) hasMissingRequiredParams(args map[string]interface{}) bool {
	if t.inputSchema == nil {
		return false
	}

	for _, reqField := range t.inputSchema.Required {
		if _, exists := args[reqField]; !exists {
			// Check if this required field has default value in properties.
			if t.inputSchema.Properties != nil && t.inputSchema.Properties[reqField] != nil {
				// For now, we assume no default values in our schema structure.
				// This could be extended to support default values.
			}
			return true
		}
	}
	return false
}

// inferParametersFromContext extracts relevant parameters from context.
func (t *mcpTool) inferParametersFromContext(ctx context.Context) map[string]interface{} {
	params := make(map[string]interface{})

	// Extract query from context if available.
	query := t.extractQueryFromContext(ctx)
	if query == "" {
		return params
	}

	// Get required parameters from schema.
	if t.inputSchema == nil {
		return params
	}

	// Try to infer parameters based on tool name and schema.
	for _, reqParam := range t.inputSchema.Required {
		if _, exists := params[reqParam]; !exists {
			// Try to infer common parameters.
			switch reqParam {
			case "location", "place":
				if locations := t.extractLocationsFromQuery(query); len(locations) > 0 {
					params[reqParam] = locations[0]
				}
			case "query", "q", "search", "text", "input", "message":
				params[reqParam] = query
			}
		}
	}

	return params
}

// extractQueryFromContext gets the user query from context.
func (t *mcpTool) extractQueryFromContext(ctx context.Context) string {
	// Try to extract the query from context values
	if queryVal := ctx.Value("user_query"); queryVal != nil {
		if query, ok := queryVal.(string); ok {
			return query
		}
	}

	// If not available through context values, check any stored messages.
	if messagesVal := ctx.Value("messages"); messagesVal != nil {
		if messages, ok := messagesVal.([]interface{}); ok && len(messages) > 0 {
			// Try to find the most recent user message.
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

// extractLocationsFromQuery extracts potential location names from a query.
func (t *mcpTool) extractLocationsFromQuery(query string) []string {
	words := strings.Fields(query)

	// Common location-related words that might precede a location
	locationPrefixes := []string{"in", "at", "near", "from", "to", "for", "about"}

	var locations []string

	// Priority 1: Look for words after location indicators (more reliable).
	for i, word := range words {
		for _, prefix := range locationPrefixes {
			if strings.ToLower(word) == prefix && i < len(words)-1 {
				location := words[i+1]
				// Remove any punctuation
				location = strings.Trim(location, ",.?!:;()")
				if location != "" && !t.isCommonWord(location) {
					locations = append(locations, location)
				}
			}
		}
	}

	// Priority 2: If no prefix-based locations found, look for capitalized words.
	if len(locations) == 0 {
		for _, word := range words {
			if len(word) > 2 && word[0] >= 'A' && word[0] <= 'Z' {
				// Remove punctuation
				cleanWord := strings.Trim(word, ",.?!:;()")
				if cleanWord != "" && !t.isCommonWord(cleanWord) {
					locations = append(locations, cleanWord)
				}
			}
		}
	}

	return locations
}

// isCommonWord checks if a word is a common English word (not likely a location).
func (t *mcpTool) isCommonWord(word string) bool {
	commonWords := []string{
		"The", "And", "But", "For", "Are", "You", "Can", "How", "What", "When", "Where", "Why",
		"Will", "Would", "Could", "Should", "Want", "Know", "Get", "Tell", "Weather", "Conditions",
		"Today", "Tomorrow", "Information", "Time", "Like", "Have", "This", "That", "With", "About",
	}
	wordLower := strings.ToLower(word)
	for _, common := range commonWords {
		if strings.ToLower(common) == wordLower {
			return true
		}
	}
	return false
}
