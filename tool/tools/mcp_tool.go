// Package tools provides implementation of various tools.
package tools

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// MCPTool is a tool that wraps an MCP tool.
type MCPTool struct {
	tool.BaseTool
	mcpTool    mcp.Tool
	mcpClient  *mcp.Client
	sessionMgr *MCPSessionManager
}

// NewMCPTool creates a new MCP tool.
func NewMCPTool(mcpTool mcp.Tool, mcpClient *mcp.Client, sessionMgr *MCPSessionManager) *MCPTool {
	// Convert MCP tool schema to JSON Schema for the tool parameters
	params := convertSchemaToParameters(mcpTool.InputSchema)

	return &MCPTool{
		BaseTool:   *tool.NewBaseTool(mcpTool.Name, mcpTool.Description, params),
		mcpTool:    mcpTool,
		mcpClient:  mcpClient,
		sessionMgr: sessionMgr,
	}
}

// Execute executes the MCP tool.
func (t *MCPTool) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Log the tool call
	fmt.Printf("Executing MCP tool %s with arguments: %v\n", t.Name(), args)

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

	// Call the MCP tool
	result, err := t.mcpClient.CallTool(ctx, t.Name(), args)
	if err != nil {
		fmt.Printf("MCP tool %s execution failed: %v\n", t.Name(), err)
		return nil, fmt.Errorf("MCP tool execution failed: %w", err)
	}

	if result.IsError {
		return nil, fmt.Errorf("MCP tool returned error: %v", result.Content)
	}

	// Process the result content
	output := extractResultContent(result.Content)

	fmt.Printf("MCP tool %s execution successful: %v\n", t.Name(), output)

	// Return the result
	return tool.NewJSONResult(output), nil
}

// convertSchemaToParameters converts a schema to a JSON Schema for tool parameters.
func convertSchemaToParameters(schema interface{}) map[string]interface{} {
	// Create a basic JSON Schema structure
	params := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}

	// In a real implementation, we would convert the OpenAPI schema
	// For now, we're just passing through the schema as-is
	if schemaMap, ok := schema.(map[string]interface{}); ok {
		if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
			params["properties"] = props
		}
		if required, ok := schemaMap["required"].([]string); ok {
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
