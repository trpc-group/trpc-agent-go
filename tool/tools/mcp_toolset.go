// Package tools provides implementation of various tools.
package tools

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
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
func (ts *MCPToolset) GetTools(ctx context.Context) ([]tool.Tool, error) {
	// Create a client session
	client, err := ts.sessionMgr.CreateSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP session: %w", err)
	}

	// List available tools
	toolsResult, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list MCP tools: %w", err)
	}

	// Convert MCP tools to agent tools
	tools := make([]tool.Tool, 0, len(toolsResult.Tools))

	for _, mcpTool := range toolsResult.Tools {
		// Apply filter if one is set
		if ts.toolFilter != nil && !ts.toolFilter(mcpTool.Name) {
			continue
		}

		// Create an MCPTool wrapper
		tools = append(tools, NewMCPTool(mcpTool, client, ts.sessionMgr))
	}

	return tools, nil
}

// Close closes the MCP toolset and all its sessions.
func (ts *MCPToolset) Close() error {
	ts.sessionMgr.CloseAllSessions()
	return nil
}

// AddToToolSet adds all MCP tools to the given tool set.
func (ts *MCPToolset) AddToToolSet(ctx context.Context, toolSet *tool.ToolSet) error {
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
