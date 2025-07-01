package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// diagnosticInfo contains context information for error diagnosis.
type diagnosticInfo struct {
	// ToolName is the name of the tool being diagnosed.
	ToolName string `json:"tool_name"`
	// Operation is the operation that was being performed.
	Operation string `json:"operation"`
	// ProvidedArgs contains the arguments that were provided.
	ProvidedArgs map[string]interface{} `json:"provided_args"`
	// ExpectedSchema contains the expected parameter schema.
	ExpectedSchema interface{} `json:"expected_schema"`
	// AvailableTools lists the tools that are available.
	AvailableTools []string `json:"available_tools"`
	// ConnectionInfo contains information about the connection.
	ConnectionInfo map[string]interface{} `json:"connection_info"`
	// SessionContext contains the session context.
	SessionContext *ToolContext `json:"session_context"`
	// ServerCapabilities lists the capabilities of the server.
	ServerCapabilities []string `json:"server_capabilities"`
}

// errorDiagnostic provides intelligent error analysis and suggestions.
type errorDiagnostic struct{}

// newErrorDiagnostic creates a new error diagnostic instance.
func newErrorDiagnostic() *errorDiagnostic {
	return &errorDiagnostic{}
}

// AnalyzeError analyzes an error and returns an enhanced MCPError with suggestions.
func (d *errorDiagnostic) analyzeError(err error, info diagnosticInfo) *MCPError {
	if err == nil {
		return nil
	}

	errorCode := d.classifyError(err, info)
	mcpErr := &MCPError{
		Code:        string(errorCode),
		Message:     err.Error(),
		OriginalErr: err,
		Context:     d.buildContext(info),
		Details:     d.buildErrorDetails(err, info),
	}

	mcpErr.Suggestions = d.generateSuggestions(errorCode, info)

	log.Debug("Error analyzed",
		"code", mcpErr.Code,
		"operation", info.Operation,
		"tool", info.ToolName)

	return mcpErr
}

// classifyError determines the error type based on error content and context.
func (d *errorDiagnostic) classifyError(err error, info diagnosticInfo) mcpErrorCode {
	errMsg := strings.ToLower(err.Error())

	// Connection-related errors
	if strings.Contains(errMsg, "connection") || strings.Contains(errMsg, "dial") {
		return mcpErrorConnectionFailed
	}

	// Authentication errors
	if strings.Contains(errMsg, "auth") || strings.Contains(errMsg, "unauthorized") ||
		strings.Contains(errMsg, "403") || strings.Contains(errMsg, "401") {
		return mcpErrorAuthenticationFailed
	}

	// Tool not found errors
	if strings.Contains(errMsg, "tool not found") || strings.Contains(errMsg, "unknown tool") ||
		(info.Operation == "tool_call" && strings.Contains(errMsg, "not found")) {
		return mcpErrorToolNotFound
	}

	// Parameter-related errors
	if strings.Contains(errMsg, "parameter") || strings.Contains(errMsg, "argument") {
		if strings.Contains(errMsg, "missing") || strings.Contains(errMsg, "required") {
			return mcpErrorMissingParameters
		}
		if strings.Contains(errMsg, "invalid") || strings.Contains(errMsg, "type") {
			return mcpErrorInvalidParameters
		}
		if strings.Contains(errMsg, "validation") {
			return mcpErrorTypeValidation
		}
	}

	// Permission errors
	if strings.Contains(errMsg, "permission") || strings.Contains(errMsg, "forbidden") {
		return mcpErrorPermissionDenied
	}

	// Timeout errors
	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		return mcpErrorTimeout
	}

	// Server errors
	if strings.Contains(errMsg, "server") || strings.Contains(errMsg, "500") ||
		strings.Contains(errMsg, "internal error") {
		return mcpErrorServerError
	}

	// Response parsing errors
	if strings.Contains(errMsg, "json") || strings.Contains(errMsg, "parse") ||
		strings.Contains(errMsg, "unmarshal") || strings.Contains(errMsg, "decode") {
		return mcpErrorInvalidResponse
	}

	return mcpErrorUnknown
}

// buildContext creates contextual information for the error.
func (d *errorDiagnostic) buildContext(info diagnosticInfo) map[string]interface{} {
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
func (d *errorDiagnostic) buildErrorDetails(err error, info diagnosticInfo) *ErrorDetails {
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
func (d *errorDiagnostic) generateUserFriendlyMessage(err error, info diagnosticInfo) string {
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
func (d *errorDiagnostic) extractParameterInfo(schema interface{}) []ParameterInfo {
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
func (d *errorDiagnostic) generateSuggestions(code mcpErrorCode, info diagnosticInfo) []string {
	var suggestions []string

	switch code {
	case mcpErrorConnectionFailed:
		suggestions = append(suggestions, "Check if the MCP server is running")
		suggestions = append(suggestions, "Verify the server URL or command path")
		suggestions = append(suggestions, "Check network connectivity")
		if len(info.ConnectionInfo) > 0 {
			if url, ok := info.ConnectionInfo["server_url"].(string); ok && url != "" {
				suggestions = append(suggestions, fmt.Sprintf("Try accessing %s manually", url))
			}
		}

	case mcpErrorAuthenticationFailed:
		suggestions = append(suggestions, "Verify your authentication credentials")
		suggestions = append(suggestions, "Check if the authentication token/key is still valid")
		suggestions = append(suggestions, "Ensure you have the correct permissions")

	case mcpErrorToolNotFound:
		suggestions = append(suggestions, "Check the tool name spelling")
		suggestions = append(suggestions, "Refresh the tools list to get updated available tools")
		if len(info.AvailableTools) > 0 {
			suggestions = append(suggestions, fmt.Sprintf("Available tools: %s", strings.Join(info.AvailableTools, ", ")))
		}

	case mcpErrorMissingParameters:
		suggestions = append(suggestions, "Check the required parameters for this tool")
		suggestions = append(suggestions, "Ensure all mandatory parameters are provided")
		if info.ExpectedSchema != nil {
			suggestions = append(suggestions, "Use the parameter information to provide the correct arguments")
		}

	case mcpErrorInvalidParameters:
		suggestions = append(suggestions, "Verify parameter types and formats")
		suggestions = append(suggestions, "Check parameter value constraints")
		suggestions = append(suggestions, "Ensure JSON format is correct if using structured arguments")

	case mcpErrorTypeValidation:
		suggestions = append(suggestions, "Check parameter data types (string, number, boolean, etc.)")
		suggestions = append(suggestions, "Verify numeric values are within valid ranges")
		suggestions = append(suggestions, "Ensure required fields are not empty")

	case mcpErrorTimeout:
		suggestions = append(suggestions, "Try again after a short delay")
		suggestions = append(suggestions, "Check if the server is overloaded")
		suggestions = append(suggestions, "Consider increasing timeout settings")

	case mcpErrorServerError:
		suggestions = append(suggestions, "Check server logs for more details")
		suggestions = append(suggestions, "Try again later if this is a temporary issue")
		suggestions = append(suggestions, "Contact the server administrator if the problem persists")

	case mcpErrorInvalidResponse:
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
func (d *errorDiagnostic) FormatError(mcpErr *MCPError) string {
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
func (d *errorDiagnostic) logError(mcpErr *MCPError) {
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
func convertMCPSchemaToSchema(mcpSchema interface{}) *tool.Schema {
	schemaBytes, err := json.Marshal(mcpSchema)
	if err != nil {
		return &tool.Schema{
			Type: "object",
		}
	}

	var schemaMap map[string]interface{}
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		return &tool.Schema{
			Type: "object",
		}
	}

	schema := &tool.Schema{}
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
func convertProperties(props map[string]interface{}) map[string]*tool.Schema {
	if props == nil {
		return nil
	}

	result := make(map[string]*tool.Schema)
	for name, prop := range props {
		if propMap, ok := prop.(map[string]interface{}); ok {
			propSchema := &tool.Schema{}
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
