// Package tools provides implementation of various tools.
package tool

import (
	"context"
	"fmt"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// MCPToolset is a toolset that provides MCP tools.
type MCPToolset struct {
	// Session manager for MCP connections
	sessionMgr *MCPSessionManager

	// Tool filter function
	toolFilter func(string) bool

	// Reference to client info (used to ensure mcp import is used)
	clientInfo mcp.Implementation
}

// MCPToolsetOption represents an option for MCP toolset configuration.
type MCPToolsetOption func(*MCPToolset)

// NewMCPToolset creates a new MCP toolset.
func NewMCPToolset(params MCPServerParams, opts ...MCPToolsetOption) *MCPToolset {
	toolset := &MCPToolset{
		sessionMgr: NewMCPSessionManager(params),
		toolFilter: nil, // No filter by default
		clientInfo: mcp.Implementation{
			Name:    "trpc-agent-go",
			Version: "1.0.0",
		},
	}

	// Apply options
	for _, opt := range opts {
		opt(toolset)
	}

	return toolset
}

// WithToolFilter adds a filter function for selecting tools.
func WithToolFilter(filterFunc func(string) bool) MCPToolsetOption {
	return func(ts *MCPToolset) {
		ts.toolFilter = filterFunc
	}
}

// WithToolNames sets specific tool names to include.
func WithToolNames(names ...string) MCPToolsetOption {
	nameMap := make(map[string]bool)
	for _, name := range names {
		nameMap[name] = true
	}

	return func(ts *MCPToolset) {
		ts.toolFilter = func(name string) bool {
			return nameMap[name]
		}
	}
}

// GetTools gets all available MCP tools.
func (ts *MCPToolset) GetTools(ctx context.Context) ([]Tool, error) {
	// Create a client session
	client, err := ts.sessionMgr.CreateSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP session: %w", err)
	}

	// Use the createMCPTools function to get tools
	tools, err := createMCPTools(ctx, client, ts.sessionMgr)
	if err != nil {
		return nil, err
	}

	// Apply filter if one is set
	if ts.toolFilter != nil {
		filteredTools := make([]Tool, 0)
		for _, t := range tools {
			if ts.toolFilter(t.Name()) {
				filteredTools = append(filteredTools, t)
			}
		}
		return filteredTools, nil
	}

	return tools, nil
}

// Close closes the MCP toolset and all its sessions.
func (ts *MCPToolset) Close() error {
	ts.sessionMgr.CloseAllSessions()
	return nil
}

// AddToToolSet adds all MCP tools to the given tool set.
func (ts *MCPToolset) AddToToolSet(ctx context.Context, toolSet *ToolSet) error {
	tools, err := ts.GetTools(ctx)
	if err != nil {
		return err
	}

	for _, t := range tools {
		if err := toolSet.Add(t); err != nil {
			return fmt.Errorf("failed to add MCP tool %s: %w", t.Name(), err)
		}
	}

	return nil
}

// Create MCPTool for each MCP tool
func createMCPTools(ctx context.Context, mcpClient *mcp.Client, sessionMgr *MCPSessionManager) ([]Tool, error) {
	// List available tools from MCP client
	toolsResult, err := mcpClient.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP tools: %w", err)
	}

	// Create MCPTool wrappers
	var tools []Tool
	for _, mcpTool := range toolsResult.Tools {
		// Create executor function
		executor := NewFunctionExecutor(func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			result, err := mcpClient.CallTool(ctx, mcpTool.Name, args)
			if err != nil {
				return nil, err
			}
			if result.IsError {
				return nil, fmt.Errorf("MCP tool returned error: %v", result.Content)
			}
			return extractResultContent(result.Content), nil
		})

		// Create the MCPTool
		tools = append(tools, NewMCPTool(mcpTool, mcpClient, sessionMgr, executor))
	}

	return tools, nil
}
