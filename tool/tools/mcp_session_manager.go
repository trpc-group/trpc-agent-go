// Package tools provides implementation of various tools.
package tools

import (
	"context"
	"fmt"
	"sync"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// ConnectionType defines the type of MCP connection.
type ConnectionType string

const (
	// ConnectionTypeStdio represents a stdio-based MCP server connection.
	ConnectionTypeStdio ConnectionType = "stdio"

	// ConnectionTypeHTTP represents an HTTP-based MCP server connection.
	ConnectionTypeHTTP ConnectionType = "http"
)

// MCPServerParams contains parameters for the MCP server connection.
type MCPServerParams struct {
	// Connection type (stdio or http)
	Type ConnectionType

	// Command to run for stdio connections
	Command string

	// Arguments for the command
	Args []string

	// URL for HTTP connections
	URL string
}

// MCPSessionManager manages MCP client sessions.
type MCPSessionManager struct {
	// Server parameters
	params MCPServerParams

	// Active sessions
	sessions []*mcp.Client

	// Session mutex
	mutex sync.Mutex
}

// NewMCPSessionManager creates a new MCP session manager.
func NewMCPSessionManager(params MCPServerParams) *MCPSessionManager {
	return &MCPSessionManager{
		params:   params,
		sessions: make([]*mcp.Client, 0),
	}
}

// CreateSession creates a new MCP client session.
func (m *MCPSessionManager) CreateSession(ctx context.Context) (*mcp.Client, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Create a client based on the connection type
	var client *mcp.Client
	var err error

	clientInfo := mcp.Implementation{
		Name:    "trpc-agent-go",
		Version: "1.0.0",
	}

	switch m.params.Type {
	case ConnectionTypeHTTP:
		// Create HTTP client
		if m.params.URL == "" {
			return nil, fmt.Errorf("URL is required for HTTP connection")
		}

		client, err = mcp.NewClient(
			m.params.URL,
			clientInfo,
			mcp.WithClientLogger(mcp.GetDefaultLogger()),
		)

	case ConnectionTypeStdio:
		// Create Stdio client - not implemented yet
		return nil, fmt.Errorf("stdio connection not implemented")

	default:
		return nil, fmt.Errorf("unsupported connection type: %s", m.params.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create MCP client: %w", err)
	}

	// Initialize the client
	_, err = client.Initialize(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MCP client: %w", err)
	}

	// Add to the sessions list
	m.sessions = append(m.sessions, client)

	return client, nil
}

// CloseAllSessions closes all active MCP sessions.
func (m *MCPSessionManager) CloseAllSessions() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, session := range m.sessions {
		_ = session.Close() // Ignore errors
	}

	m.sessions = make([]*mcp.Client, 0)
}

// GetActiveSessionCount returns the number of active sessions.
func (m *MCPSessionManager) GetActiveSessionCount() int {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	return len(m.sessions)
}
