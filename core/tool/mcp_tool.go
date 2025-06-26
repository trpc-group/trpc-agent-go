package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// mcpTool implements the Tool interface for MCP tools.
type mcpTool struct {
	mcpToolRef     *mcp.Tool
	inputSchema    *Schema
	sessionManager *mcpSessionManager
	retryConfig    *RetryConfig
	diagnostics    *ErrorDiagnostic
}

// newMCPTool creates a new MCP tool wrapper.
func newMCPTool(mcpToolData mcp.Tool, sessionManager *mcpSessionManager, retryConfig *RetryConfig) *mcpTool {
	tool := &mcpTool{
		mcpToolRef:     &mcpToolData,
		sessionManager: sessionManager,
		retryConfig:    retryConfig,
		diagnostics:    NewErrorDiagnostic(),
	}

	// Convert MCP input schema to inner Schema.
	if mcpToolData.InputSchema != nil {
		tool.inputSchema = convertMCPSchemaToSchema(mcpToolData.InputSchema)
	}

	return tool
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
		diagInfo := DiagnosticInfo{
			ToolName:       t.mcpToolRef.Name,
			Operation:      "parameter_processing",
			ProvidedArgs:   rawArguments,
			ExpectedSchema: t.mcpToolRef.InputSchema,
		}
		if toolCtx, ok := GetToolContext(ctx); ok {
			diagInfo.SessionContext = toolCtx
		}

		mcpErr := t.diagnostics.AnalyzeError(err, diagInfo)
		t.diagnostics.LogError(mcpErr)
		return nil, mcpErr
	}

	// Validate parameters against schema.
	if err := t.validateParameters(normalizedParams); err != nil {
		// Enhanced error diagnosis for parameter validation.
		diagInfo := DiagnosticInfo{
			ToolName:       t.mcpToolRef.Name,
			Operation:      "parameter_validation",
			ProvidedArgs:   normalizedParams,
			ExpectedSchema: t.mcpToolRef.InputSchema,
		}
		if toolCtx, ok := GetToolContext(ctx); ok {
			diagInfo.SessionContext = toolCtx
		}

		mcpErr := t.diagnostics.AnalyzeError(err, diagInfo)
		t.diagnostics.LogError(mcpErr)
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
		diagInfo := DiagnosticInfo{
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
		mcpErr := t.diagnostics.AnalyzeError(err, diagInfo)
		t.diagnostics.LogError(mcpErr)

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
func (t *mcpTool) Declaration() *Declaration {
	return &Declaration{
		Name:        t.mcpToolRef.Name,
		Description: t.mcpToolRef.Description,
		InputSchema: t.inputSchema,
	}
}

// ErrorDiagnostic provides intelligent error analysis and suggestions.
type ErrorDiagnostic struct{}

// NewErrorDiagnostic creates a new error diagnostic instance.
func NewErrorDiagnostic() *ErrorDiagnostic {
	return &ErrorDiagnostic{}
}

// AnalyzeError analyzes an error and returns an enhanced MCPError with suggestions.
func (d *ErrorDiagnostic) AnalyzeError(err error, info DiagnosticInfo) *MCPError {
	if err == nil {
		return nil
	}

	mcpErr := &MCPError{
		Code:        d.classifyError(err, info),
		Message:     err.Error(),
		OriginalErr: err,
		Context:     d.buildContext(info),
		Details:     d.buildErrorDetails(err, info),
	}

	mcpErr.Suggestions = d.generateSuggestions(mcpErr.Code, info)

	log.Debug("Error analyzed",
		"code", mcpErr.Code,
		"operation", info.Operation,
		"tool", info.ToolName)

	return mcpErr
}

// classifyError determines the error type based on error content and context.
func (d *ErrorDiagnostic) classifyError(err error, info DiagnosticInfo) MCPErrorCode {
	errMsg := strings.ToLower(err.Error())

	// Connection-related errors
	if strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "dial") {
		return MCPErrorConnectionFailed
	}

	// Authentication errors
	if strings.Contains(errMsg, "auth") || strings.Contains(errMsg, "unauthorized") ||
		strings.Contains(errMsg, "403") || strings.Contains(errMsg, "401") {
		return MCPErrorAuthenticationFailed
	}

	// Tool not found errors
	if strings.Contains(errMsg, "tool not found") || strings.Contains(errMsg, "unknown tool") ||
		(info.Operation == "tool_call" && strings.Contains(errMsg, "not found")) {
		return MCPErrorToolNotFound
	}

	// Parameter-related errors
	if strings.Contains(errMsg, "parameter") || strings.Contains(errMsg, "argument") {
		if strings.Contains(errMsg, "missing") || strings.Contains(errMsg, "required") {
			return MCPErrorMissingParameters
		}
		if strings.Contains(errMsg, "invalid") || strings.Contains(errMsg, "type") {
			return MCPErrorInvalidParameters
		}
		if strings.Contains(errMsg, "validation") {
			return MCPErrorTypeValidation
		}
	}

	// Permission errors
	if strings.Contains(errMsg, "permission") || strings.Contains(errMsg, "forbidden") {
		return MCPErrorPermissionDenied
	}

	// Timeout errors
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		return MCPErrorTimeout
	}

	// Server errors
	if strings.Contains(errMsg, "server") || strings.Contains(errMsg, "500") ||
		strings.Contains(errMsg, "internal error") {
		return MCPErrorServerError
	}

	// Response parsing errors
	if strings.Contains(errMsg, "json") || strings.Contains(errMsg, "parse") ||
		strings.Contains(errMsg, "unmarshal") || strings.Contains(errMsg, "decode") {
		return MCPErrorInvalidResponse
	}

	return MCPErrorUnknown
}

// buildContext creates contextual information for the error.
func (d *ErrorDiagnostic) buildContext(info DiagnosticInfo) map[string]interface{} {
	context := make(map[string]interface{})

	if info.ToolName != "" {
		context["tool_name"] = info.ToolName
	}
	if info.Operation != "" {
		context["operation"] = info.Operation
	}
	if len(info.ProvidedArgs) > 0 {
		context["provided_arguments"] = info.ProvidedArgs
	}
	if len(info.AvailableTools) > 0 {
		context["available_tools"] = info.AvailableTools
	}
	if info.SessionContext != nil {
		context["session_id"] = info.SessionContext.SessionID
		context["user_id"] = info.SessionContext.UserID
	}

	context["timestamp"] = time.Now().Format(time.RFC3339)

	return context
}

// buildErrorDetails creates detailed error information.
func (d *ErrorDiagnostic) buildErrorDetails(err error, info DiagnosticInfo) *ErrorDetails {
	details := &ErrorDetails{
		TechnicalDetails:   err.Error(),
		ProvidedParameters: info.ProvidedArgs,
	}

	// Generate user-friendly message.
	details.UserFriendlyMessage = d.generateUserFriendlyMessage(err, info)

	// Extract expected parameters if available.
	if info.ExpectedSchema != nil {
		details.ExpectedParameters = d.extractParameterInfo(info.ExpectedSchema)
	}

	// Include available tools for tool not found errors.
	if len(info.AvailableTools) > 0 {
		details.AvailableTools = info.AvailableTools
	}

	return details
}

// generateUserFriendlyMessage creates a user-friendly error message.
func (d *ErrorDiagnostic) generateUserFriendlyMessage(err error, info DiagnosticInfo) string {
	errMsg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errMsg, "connection"):
		return fmt.Sprintf("Unable to connect to the MCP server. Please check if the server is running and accessible.")

	case strings.Contains(errMsg, "auth"):
		return fmt.Sprintf("Authentication failed. Please check your credentials and try again.")

	case strings.Contains(errMsg, "tool not found") ||
		(info.Operation == "tool_call" && strings.Contains(errMsg, "not found")):
		if info.ToolName != "" {
			return fmt.Sprintf("Tool '%s' was not found. Please check the tool name or refresh the available tools list.", info.ToolName)
		}
		return "The requested tool was not found. Please check the tool name and try again."

	case strings.Contains(errMsg, "parameter") && strings.Contains(errMsg, "missing"):
		return "Some required parameters are missing. Please provide all necessary parameters and try again."

	case strings.Contains(errMsg, "parameter") && (strings.Contains(errMsg, "invalid") ||
		strings.Contains(errMsg, "type")):
		return "One or more parameters have invalid values or types. Please check the parameter format and try again."

	case strings.Contains(errMsg, "timeout"):
		return "The operation timed out. The server may be busy or the request is taking too long to process."

	case strings.Contains(errMsg, "permission"):
		return "You don't have permission to perform this operation. Please check your access rights."

	default:
		return fmt.Sprintf("An error occurred: %s", err.Error())
	}
}

// extractParameterInfo extracts parameter information from schema.
func (d *ErrorDiagnostic) extractParameterInfo(schema interface{}) []ParameterInfo {
	var params []ParameterInfo

	// Try to parse as openapi3.Schema or generic map.
	switch s := schema.(type) {
	case map[string]interface{}:
		if properties, ok := s["properties"].(map[string]interface{}); ok {
			required := make(map[string]bool)
			if reqSlice, ok := s["required"].([]interface{}); ok {
				for _, req := range reqSlice {
					if reqStr, ok := req.(string); ok {
						required[reqStr] = true
					}
				}
			}

			for name, prop := range properties {
				if propMap, ok := prop.(map[string]interface{}); ok {
					param := ParameterInfo{
						Name:     name,
						Required: required[name],
					}

					if typeVal, ok := propMap["type"].(string); ok {
						param.Type = typeVal
					}
					if desc, ok := propMap["description"].(string); ok {
						param.Description = desc
					}
					if example, ok := propMap["example"]; ok {
						param.Example = example
					}

					params = append(params, param)
				}
			}
		}
	}

	return params
}

// generateSuggestions creates actionable suggestions based on error type.
func (d *ErrorDiagnostic) generateSuggestions(code MCPErrorCode, info DiagnosticInfo) []string {
	var suggestions []string

	switch code {
	case MCPErrorConnectionFailed:
		suggestions = append(suggestions, "Check if the MCP server is running")
		suggestions = append(suggestions, "Verify the server URL or command path")
		suggestions = append(suggestions, "Check network connectivity")
		if len(info.ConnectionInfo) > 0 {
			if url, ok := info.ConnectionInfo["server_url"].(string); ok && url != "" {
				suggestions = append(suggestions, fmt.Sprintf("Try accessing %s manually", url))
			}
		}

	case MCPErrorAuthenticationFailed:
		suggestions = append(suggestions, "Verify your authentication credentials")
		suggestions = append(suggestions, "Check if the authentication token/key is still valid")
		suggestions = append(suggestions, "Ensure you have the correct permissions")

	case MCPErrorToolNotFound:
		suggestions = append(suggestions, "Check the tool name spelling")
		suggestions = append(suggestions, "Refresh the tools list to get updated available tools")
		if len(info.AvailableTools) > 0 {
			suggestions = append(suggestions, fmt.Sprintf("Available tools: %s", strings.Join(info.AvailableTools, ", ")))
		}

	case MCPErrorMissingParameters:
		suggestions = append(suggestions, "Check the required parameters for this tool")
		suggestions = append(suggestions, "Ensure all mandatory parameters are provided")
		if info.ExpectedSchema != nil {
			suggestions = append(suggestions, "Use the parameter information to provide the correct arguments")
		}

	case MCPErrorInvalidParameters:
		suggestions = append(suggestions, "Verify parameter types and formats")
		suggestions = append(suggestions, "Check parameter value constraints")
		suggestions = append(suggestions, "Ensure JSON format is correct if using structured arguments")

	case MCPErrorTypeValidation:
		suggestions = append(suggestions, "Check parameter data types (string, number, boolean, etc.)")
		suggestions = append(suggestions, "Verify numeric values are within valid ranges")
		suggestions = append(suggestions, "Ensure required fields are not empty")

	case MCPErrorTimeout:
		suggestions = append(suggestions, "Try again after a short delay")
		suggestions = append(suggestions, "Check if the server is overloaded")
		suggestions = append(suggestions, "Consider increasing timeout settings")

	case MCPErrorServerError:
		suggestions = append(suggestions, "Check server logs for more details")
		suggestions = append(suggestions, "Try again later if this is a temporary issue")
		suggestions = append(suggestions, "Contact the server administrator if the problem persists")

	case MCPErrorInvalidResponse:
		suggestions = append(suggestions, "Check if the server response format is valid")
		suggestions = append(suggestions, "Verify server compatibility with MCP protocol version")
		suggestions = append(suggestions, "Try refreshing the connection")

	default:
		suggestions = append(suggestions, "Check the error message for more specific information")
		suggestions = append(suggestions, "Try the operation again")
		suggestions = append(suggestions, "Contact support if the problem persists")
	}

	return suggestions
}

// FormatError formats an MCPError for display to users.
func (d *ErrorDiagnostic) FormatError(mcpErr *MCPError) string {
	var parts []string

	// Add user-friendly message
	if mcpErr.Details != nil && mcpErr.Details.UserFriendlyMessage != "" {
		parts = append(parts, mcpErr.Details.UserFriendlyMessage)
	} else {
		parts = append(parts, mcpErr.Message)
	}

	// Add suggestions if available
	if len(mcpErr.Suggestions) > 0 {
		parts = append(parts, "\nSuggestions:")
		for i, suggestion := range mcpErr.Suggestions {
			parts = append(parts, fmt.Sprintf("  %d. %s", i+1, suggestion))
		}
	}

	// Add context information if helpful
	if len(mcpErr.Context) > 0 {
		if toolName, ok := mcpErr.Context["tool_name"].(string); ok && toolName != "" {
			parts = append(parts, fmt.Sprintf("\nTool: %s", toolName))
		}
		if operation, ok := mcpErr.Context["operation"].(string); ok && operation != "" {
			parts = append(parts, fmt.Sprintf("Operation: %s", operation))
		}
	}

	return strings.Join(parts, "\n")
}

// LogError logs an error with appropriate detail level.
func (d *ErrorDiagnostic) LogError(mcpErr *MCPError) {
	fields := []interface{}{
		"error_code", mcpErr.Code,
		"message", mcpErr.Message,
	}

	// Add context fields.
	for key, value := range mcpErr.Context {
		fields = append(fields, key, value)
	}

	log.Error("MCP operation failed", fields)

	// Log technical details at debug level.
	if mcpErr.Details != nil && mcpErr.Details.TechnicalDetails != "" {
		log.Debug("Technical error details", "details", mcpErr.Details.TechnicalDetails)
	}
}

// convertMCPSchemaToSchema converts MCP's JSON schema to our Schema format.
func convertMCPSchemaToSchema(mcpSchema interface{}) *Schema {
	schemaBytes, err := json.Marshal(mcpSchema)
	if err != nil {
		return &Schema{
			Type: "object",
		}
	}

	var schemaMap map[string]interface{}
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		return &Schema{
			Type: "object",
		}
	}

	schema := &Schema{}
	if typeVal, ok := schemaMap["type"].(string); ok {
		schema.Type = typeVal
	}
	if descVal, ok := schemaMap["description"].(string); ok {
		schema.Description = descVal
	}
	if propsVal, ok := schemaMap["properties"].(map[string]interface{}); ok {
		schema.Properties = convertProperties(propsVal)
	}
	if reqVal, ok := schemaMap["required"].([]interface{}); ok {
		required := make([]string, len(reqVal))
		for i, req := range reqVal {
			if reqStr, ok := req.(string); ok {
				required[i] = reqStr
			}
		}
		schema.Required = required
	}

	return schema
}

// convertProperties converts property definitions from map[string]interface{} to map[string]*Schema.
func convertProperties(props map[string]interface{}) map[string]*Schema {
	if props == nil {
		return nil
	}

	result := make(map[string]*Schema)
	for name, prop := range props {
		if propMap, ok := prop.(map[string]interface{}); ok {
			propSchema := &Schema{}
			if typeVal, ok := propMap["type"].(string); ok {
				propSchema.Type = typeVal
			}
			if descVal, ok := propMap["description"].(string); ok {
				propSchema.Description = descVal
			}
			result[name] = propSchema
		}
	}
	return result
}

// convertMCPContentToResult converts MCP content to a suitable return format.
func convertMCPContentToResult(content []mcp.Content) interface{} {
	if len(content) == 0 {
		return nil
	}

	if len(content) == 1 {
		return convertSingleMCPContent(content[0])
	}

	// Multiple content items - return as array.
	results := make([]interface{}, len(content))
	for i, item := range content {
		results[i] = convertSingleMCPContent(item)
	}
	return results
}

// convertSingleMCPContent converts a single MCP content item to standard format.
func convertSingleMCPContent(content mcp.Content) interface{} {
	switch c := content.(type) {
	case mcp.TextContent:
		return map[string]interface{}{
			"type": "text",
			"text": c.Text,
		}
	case mcp.ImageContent:
		return map[string]interface{}{
			"type":     "image",
			"data":     c.Data,
			"mimetype": c.MimeType,
		}
	case mcp.AudioContent:
		return map[string]interface{}{
			"type":     "audio",
			"data":     c.Data,
			"mimetype": c.MimeType,
		}
	case mcp.EmbeddedResource:
		resourceData := map[string]interface{}{
			"type": "resource",
		}

		// Handle different types of resource contents
		switch res := c.Resource.(type) {
		case mcp.TextResourceContents:
			resourceData["uri"] = res.URI
			resourceData["text"] = res.Text
			resourceData["mimetype"] = res.MIMEType
		case mcp.BlobResourceContents:
			resourceData["uri"] = res.URI
			resourceData["blob"] = res.Blob
			resourceData["mimetype"] = res.MIMEType
		default:
			resourceData["error"] = "unknown resource type"
		}

		return resourceData
	default:
		// Fallback: try to marshal the content as-is.
		contentBytes, err := json.Marshal(content)
		if err != nil {
			return map[string]interface{}{
				"type":  "unknown",
				"error": err.Error(),
			}
		}

		var result interface{}
		if err := json.Unmarshal(contentBytes, &result); err != nil {
			return map[string]interface{}{
				"type":  "unknown",
				"error": err.Error(),
			}
		}
		return result
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
