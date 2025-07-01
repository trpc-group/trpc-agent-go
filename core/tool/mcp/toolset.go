// Package mcp provides MCP tool set implementation.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// MCPToolSet implements the ToolSet interface for MCP tools.
type MCPToolSet struct {
	config         mcpToolSetConfig
	sessionManager *mcpSessionManager
	tools          []tool.Tool
	mu             sync.RWMutex
	lastRefresh    time.Time
	refreshTicker  *time.Ticker
	stopCh         chan struct{}
}

// NewMCPToolSet creates a new MCP tool set with the given configuration.
func NewMCPToolSet(config MCPConnectionConfig, opts ...MCPToolSetOption) *MCPToolSet {
	// Apply default configuration.
	cfg := mcpToolSetConfig{
		connectionConfig: config,
		retryConfig:      defaultRetryConfig,
	}

	// Apply user options.
	for _, opt := range opts {
		opt(&cfg)
	}

	// Merge connection config settings into cfg if they exist
	if config.Retry != nil {
		cfg.retryConfig = config.Retry
	}
	if config.Auth != nil {
		cfg.authConfig = config.Auth
	}

	// Set default client info if not provided
	if cfg.connectionConfig.ClientInfo.Name == "" {
		cfg.connectionConfig.ClientInfo = defaultClientInfo
	}

	// Create session manager
	sessionManager := newMCPSessionManager(cfg.connectionConfig)

	toolSet := &MCPToolSet{
		config:         cfg,
		sessionManager: sessionManager,
		tools:          make([]tool.Tool, 0),
		stopCh:         make(chan struct{}),
	}

	// Set up auto-refresh if configured
	if cfg.autoRefresh > 0 {
		toolSet.startAutoRefresh()
	}

	return toolSet
}

// Tools implements the ToolSet interface.
func (ts *MCPToolSet) Tools(ctx context.Context) []tool.Tool {
	ts.mu.RLock()
	shouldRefresh := len(ts.tools) == 0 ||
		(ts.config.autoRefresh > 0 && time.Since(ts.lastRefresh) > ts.config.autoRefresh)
	ts.mu.RUnlock()

	if shouldRefresh {
		if err := ts.refreshTools(ctx); err != nil {
			log.Error("Failed to refresh tools", err)
			// Return cached tools if refresh fails
		}
	}

	ts.mu.RLock()
	defer ts.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make([]tool.Tool, len(ts.tools))
	copy(result, ts.tools)
	return result
}

// Close implements the ToolSet interface.
func (ts *MCPToolSet) Close() error {
	ts.mu.Lock()

	// Stop auto-refresh first
	if ts.refreshTicker != nil {
		ts.refreshTicker.Stop()
		ts.refreshTicker = nil
	}

	ts.mu.Unlock()

	// Signal stop to auto-refresh goroutine (without holding lock)
	select {
	case ts.stopCh <- struct{}{}:
	default:
		// Channel might be closed or blocked, that's okay
	}

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

// refreshTools connects to the MCP server and refreshes the tool list.
func (ts *MCPToolSet) refreshTools(ctx context.Context) error {
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
		tool := newMCPTool(mcpTool, ts.sessionManager, ts.config.retryConfig)
		tools = append(tools, tool)
	}

	// Apply tool filter if configured.
	if ts.config.toolFilter != nil {
		toolInfos := make([]MCPToolInfo, len(tools))
		for i, tool := range tools {
			decl := tool.Declaration()
			toolInfos[i] = MCPToolInfo{
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
	ts.lastRefresh = time.Now()
	ts.mu.Unlock()

	log.Debug("Successfully refreshed MCP tools", "count", len(tools))
	return nil
}

// startAutoRefresh starts the auto-refresh goroutine.
func (ts *MCPToolSet) startAutoRefresh() {
	if ts.config.autoRefresh <= 0 {
		return
	}

	ts.refreshTicker = time.NewTicker(ts.config.autoRefresh)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("Auto-refresh goroutine panicked", "panic", r)
			}
		}()

		log.Debug("Starting auto-refresh", "interval", ts.config.autoRefresh)

		for {
			// Get ticker channel safely
			ts.mu.RLock()
			ticker := ts.refreshTicker
			ts.mu.RUnlock()

			if ticker == nil {
				log.Debug("Auto-refresh ticker is nil, stopping")
				return
			}

			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := ts.refreshTools(ctx); err != nil {
					log.Error("Auto-refresh failed", err)
				}
				cancel()

			case <-ts.stopCh:
				log.Debug("Auto-refresh stopped")
				return
			}
		}
	}()
}

// GetToolByName returns a tool by its name, or nil if not found.
func (ts *MCPToolSet) GetToolByName(ctx context.Context, name string) tool.Tool {
	tools := ts.Tools(ctx)
	for _, tool := range tools {
		if tool.Declaration().Name == name {
			return tool
		}
	}
	return nil
}

// IsConnected returns whether the MCP session is connected and initialized.
func (ts *MCPToolSet) IsConnected() bool {
	return ts.sessionManager.isConnected()
}

// Reconnect explicitly reconnects to the MCP server.
func (ts *MCPToolSet) Reconnect(ctx context.Context) error {
	log.Info("Reconnecting to MCP server")

	// Close existing connection.
	if err := ts.sessionManager.close(); err != nil {
		log.Warn("Failed to close existing connection during reconnect", err)
	}

	// Reconnect and refresh tools.
	if err := ts.refreshTools(ctx); err != nil {
		return fmt.Errorf("failed to reconnect and refresh tools: %w", err)
	}

	log.Info("Successfully reconnected to MCP server")
	return nil
}

// GetToolNames returns a list of available tool names.
// This is useful for error diagnostics and debugging.
func (ts *MCPToolSet) GetToolNames(ctx context.Context) []string {
	tools := ts.Tools(ctx)
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Declaration().Name
	}
	return names
}

// mcpSessionManager manages the MCP client connection and session.
type mcpSessionManager struct {
	config      MCPConnectionConfig
	client      mcp.Connector
	authHandler authHandler
	diagnostics *errorDiagnostic
	mu          sync.RWMutex
	connected   bool
	initialized bool
}

// authHandler handles authentication for MCP connections.
type authHandler interface {
	ApplyAuth(headers http.Header) error
}

// newMCPSessionManager creates a new MCP session manager.
func newMCPSessionManager(config MCPConnectionConfig) *mcpSessionManager {
	manager := &mcpSessionManager{
		config:      config,
		diagnostics: newErrorDiagnostic(),
	}

	// Set up authentication handler if needed.
	if config.Auth != nil {
		manager.authHandler = newAuthHandler(config.Auth)
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
			log.Error("Failed to close client after initialization failure", "error", closeErr)
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
func (m *mcpSessionManager) callTool(ctx context.Context, name string, arguments map[string]interface{}) ([]mcp.Content, error) {
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

	// Check if the result contains an error.
	if callResp.IsError {
		errorMessage := m.extractErrorFromContent(callResp.Content)
		log.Error("Tool returned error", "name", name, "error", errorMessage)
		return nil, fmt.Errorf("tool %s returned error: %s", name, errorMessage)
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

// bearerAuthHandler implements authentication using bearer token.
type bearerAuthHandler struct {
	token string
}

// noopAuthHandler implements no authentication.
type noopAuthHandler struct{}

// newAuthHandler creates an appropriate auth handler based on config.
func newAuthHandler(config *AuthConfig) authHandler {
	// Validate and convert auth type string to internal type
	authType, err := validateAuthType(config.Type)
	if err != nil {
		// For invalid auth types, fall back to no-op
		return &noopAuthHandler{}
	}

	switch authType {
	case authTypeBearer:
		token, _ := config.Credentials["token"].(string)
		return &bearerAuthHandler{token: token}

	default:
		return &noopAuthHandler{}
	}
}

// ApplyAuth applies bearer token authentication to headers.
func (h *bearerAuthHandler) ApplyAuth(headers http.Header) error {
	if h.token == "" {
		return fmt.Errorf("bearer token is required")
	}
	headers.Set("Authorization", fmt.Sprintf("Bearer %s", h.token))
	return nil
}

// ApplyAuth does nothing for no-op authentication.
func (h *noopAuthHandler) ApplyAuth(headers http.Header) error {
	return nil
}

// getAvailableToolNames returns a list of available tool names.
// This is used for error diagnostics.
func (m *mcpSessionManager) getAvailableToolNames(ctx context.Context) []string {
	tools, err := m.listTools(ctx)
	if err != nil {
		return nil
	}

	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}
