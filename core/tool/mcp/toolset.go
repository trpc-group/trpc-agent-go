// Package mcp provides MCP tool set implementation.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// ToolSet implements the ToolSet interface for MCP tools.
type ToolSet struct {
	config         toolSetConfig
	sessionManager *mcpSessionManager
	tools          []tool.Tool
	mu             sync.RWMutex
}

// NewMCPToolSet creates a new MCP tool set with the given configuration.
func NewMCPToolSet(config ConnectionConfig, opts ...ToolSetOption) *ToolSet {
	// Apply default configuration.
	cfg := toolSetConfig{
		connectionConfig: config,
	}

	// Apply user options.
	for _, opt := range opts {
		opt(&cfg)
	}

	// Set default client info if not provided
	if cfg.connectionConfig.ClientInfo.Name == "" {
		cfg.connectionConfig.ClientInfo = defaultClientInfo
	}

	// Create session manager
	sessionManager := newMCPSessionManager(cfg.connectionConfig)

	toolSet := &ToolSet{
		config:         cfg,
		sessionManager: sessionManager,
		tools:          nil,
	}

	return toolSet
}

// Tools implements the ToolSet interface.
func (ts *ToolSet) Tools(ctx context.Context) []tool.CallableTool {
	if err := ts.listTools(ctx); err != nil {
		log.Error("Failed to refresh tools", err)
		// Return cached tools if refresh fails
	}

	ts.mu.RLock()
	defer ts.mu.RUnlock()

	// Since we control the creation of mcpTool instances and they all implement CallableTool,
	// we can safely do the type conversion. Using a more explicit approach for better readability.
	result := make([]tool.CallableTool, 0, len(ts.tools))
	for _, t := range ts.tools {
		// All tools created by newMCPTool implement CallableTool, so this should always succeed
		result = append(result, t.(tool.CallableTool))
	}
	return result
}

// Close implements the ToolSet interface.
func (ts *ToolSet) Close() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Close session manager
	if ts.sessionManager != nil {
		if err := ts.sessionManager.close(); err != nil {
			log.Error("Failed to close session manager", err)
			return fmt.Errorf("failed to close MCP session: %w", err)
		}
	}

	log.Info("MCP tool set closed successfully")
	return nil
}

// listTools connects to the MCP server and refreshes the tool list.
func (ts *ToolSet) listTools(ctx context.Context) error {
	log.Debug("Refreshing MCP tools")

	// Ensure connection.
	if !ts.sessionManager.isConnected() {
		if err := ts.sessionManager.connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to MCP server: %w", err)
		}
	}

	// List tools from MCP server.
	mcpTools, err := ts.sessionManager.listTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tools from MCP server: %w", err)
	}

	log.Debug("Retrieved tools from MCP server", "count", len(mcpTools))

	// Convert MCP tools to standard tool format.
	tools := make([]tool.Tool, 0, len(mcpTools))
	for _, mcpTool := range mcpTools {
		tool := newMCPTool(mcpTool, ts.sessionManager)
		tools = append(tools, tool)
	}

	// Apply tool filter if configured.
	if ts.config.toolFilter != nil {
		toolInfos := make([]ToolInfo, len(tools))
		for i, tool := range tools {
			decl := tool.Declaration()
			toolInfos[i] = ToolInfo{
				Name:        decl.Name,
				Description: decl.Description,
			}
		}

		filteredInfos := ts.config.toolFilter.Filter(ctx, toolInfos)
		filteredTools := make([]tool.Tool, 0, len(filteredInfos))

		// Build a map for quick lookup.
		filteredNames := make(map[string]bool)
		for _, info := range filteredInfos {
			filteredNames[info.Name] = true
		}

		// Keep only filtered tools.
		for _, tool := range tools {
			if filteredNames[tool.Declaration().Name] {
				filteredTools = append(filteredTools, tool)
			}
		}

		tools = filteredTools
	}

	// Update tools atomically.
	ts.mu.Lock()
	ts.tools = tools
	ts.mu.Unlock()

	log.Debug("Successfully refreshed MCP tools", "count", len(tools))
	return nil
}

// mcpSessionManager manages the MCP client connection and session.
type mcpSessionManager struct {
	config      ConnectionConfig
	client      mcp.Connector
	mu          sync.RWMutex
	connected   bool
	initialized bool
}

// newMCPSessionManager creates a new MCP session manager.
func newMCPSessionManager(config ConnectionConfig) *mcpSessionManager {
	manager := &mcpSessionManager{
		config: config,
	}

	return manager
}

// connect establishes connection to the MCP server.
func (m *mcpSessionManager) connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connected {
		return nil
	}

	log.Info("Connecting to MCP server", "transport", m.config.Transport)

	client, err := m.createClient()
	if err != nil {
		return fmt.Errorf("failed to create MCP client: %w", err)
	}

	m.client = client
	m.connected = true

	// Initialize the session.
	if err := m.initialize(ctx); err != nil {
		m.connected = false
		if closeErr := client.Close(); closeErr != nil {
			log.Error("Failed to close client after initialization failure", "client_close_error", closeErr, "error", err)
		}
		return fmt.Errorf("failed to initialize MCP session: %w", err)
	}

	log.Info("Successfully connected to MCP server")
	return nil
}

// createClient creates the appropriate MCP client based on transport configuration.
func (m *mcpSessionManager) createClient() (mcp.Connector, error) {
	clientInfo := m.config.ClientInfo
	if clientInfo.Name == "" {
		clientInfo = defaultClientInfo
	}

	// Validate and convert transport string to internal type
	transportType, err := validateTransport(m.config.Transport)
	if err != nil {
		return nil, err
	}

	switch transportType {
	case transportStdio:
		config := mcp.StdioTransportConfig{
			ServerParams: mcp.StdioServerParameters{
				Command: m.config.Command,
				Args:    m.config.Args,
			},
			Timeout: m.config.Timeout,
		}
		return mcp.NewStdioClient(config, clientInfo)

	case transportStreamable:
		options := []mcp.ClientOption{
			mcp.WithClientLogger(mcp.GetDefaultLogger()),
		}

		if len(m.config.Headers) > 0 {
			headers := http.Header{}
			for k, v := range m.config.Headers {
				headers.Set(k, v)
			}
			options = append(options, mcp.WithHTTPHeaders(headers))
		}

		return mcp.NewClient(m.config.ServerURL, clientInfo, options...)

	default:
		return nil, fmt.Errorf("unsupported transport: %s", m.config.Transport)
	}
}

// initialize initializes the MCP session.
func (m *mcpSessionManager) initialize(ctx context.Context) error {
	if m.initialized {
		return nil
	}

	log.Debug("Initializing MCP session")

	initReq := &mcp.InitializeRequest{}
	initResp, err := m.client.Initialize(ctx, initReq)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP session: %w", err)
	}

	log.Info("MCP session initialized",
		"server_name", initResp.ServerInfo.Name,
		"server_version", initResp.ServerInfo.Version,
		"protocol_version", initResp.ProtocolVersion)

	m.initialized = true
	return nil
}

// listTools retrieves the list of available tools from the MCP server.
func (m *mcpSessionManager) listTools(ctx context.Context) ([]mcp.Tool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.connected || !m.initialized {
		return nil, fmt.Errorf("MCP session not connected or initialized")
	}

	log.Debug("Listing tools from MCP server")

	listReq := &mcp.ListToolsRequest{}
	listResp, err := m.client.ListTools(ctx, listReq)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	log.Debug("Listed tools from MCP server", "count", len(listResp.Tools))
	return listResp.Tools, nil
}

// callTool executes a tool call on the MCP server.
func (m *mcpSessionManager) callTool(ctx context.Context, name string, arguments map[string]any) ([]mcp.Content, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.connected || !m.initialized {
		return nil, fmt.Errorf("MCP session not connected or initialized")
	}

	log.Debug("Calling tool", "name", name, "arguments", arguments)

	callReq := &mcp.CallToolRequest{}
	callReq.Params.Name = name
	callReq.Params.Arguments = arguments

	callResp, err := m.client.CallTool(ctx, callReq)
	if err != nil {
		// Enhanced error with parameter information.
		enhancedErr := fmt.Errorf("failed to call tool %s: %w", name, err)
		log.Error("Tool call failed", "name", name, "error", err)
		return nil, enhancedErr
	}

	log.Debug("Tool call completed", "name", name, "content_count", len(callResp.Content))
	return callResp.Content, nil
}

// extractErrorFromContent extracts error information from MCP content.
func (m *mcpSessionManager) extractErrorFromContent(contents []mcp.Content) string {
	if len(contents) == 0 {
		return "unknown error"
	}

	var errorMessages []string
	for _, content := range contents {
		if textContent, ok := content.(mcp.TextContent); ok {
			errorMessages = append(errorMessages, textContent.Text)
		}
	}

	if len(errorMessages) == 0 {
		return "error content not readable"
	}

	if len(errorMessages) == 1 {
		return errorMessages[0]
	}

	// Join multiple error messages.
	return fmt.Sprintf("%s", errorMessages)
}

// close closes the MCP session and client connection.
func (m *mcpSessionManager) close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected || m.client == nil {
		return nil
	}

	log.Info("Closing MCP session")

	err := m.client.Close()
	m.connected = false
	m.initialized = false
	m.client = nil

	if err != nil {
		log.Error("Failed to close MCP client", "error", err)
		return fmt.Errorf("failed to close MCP client: %w", err)
	}

	log.Info("MCP session closed successfully")
	return nil
}

// isConnected returns whether the session is connected and initialized.
func (m *mcpSessionManager) isConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected && m.initialized
}
