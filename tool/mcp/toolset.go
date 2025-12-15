//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mcp provides MCP tool set implementation.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// sessionReconnectErrorPatterns defines error patterns that trigger session reconnection.
// Conservative approach: only reconnect for clear connection/session failures.
// Configuration errors (DNS) and potential performance issues (timeout) are excluded.
var sessionReconnectErrorPatterns = []string{
	"session_expired:",       // Explicit session expiration from transport layer
	"transport is closed",    // Transport layer closed
	"client not initialized", // MCP client not initialized
	"not initialized",        // Generic initialization error
	"connection refused",     // Server not reachable (possibly restarting)
	"connection reset",       // Connection reset by peer
	"EOF",                    // End of file (stream closed)
	"broken pipe",            // Broken connection
	"HTTP 404",               // Session not found on server
	"session not found",      // Explicit session not found error
}

// ToolSet implements the ToolSet interface for MCP tools.
type ToolSet struct {
	config         toolSetConfig
	sessionManager *mcpSessionManager
	tools          []tool.Tool
	mu             sync.RWMutex
	name           string
}

// NewMCPToolSet creates a new MCP tool set with the given configuration.
// Use WithName option to set a custom name for the toolset to avoid name conflicts
// for tools with the same name under different tool sets when using multiple MCP toolsets.
// Example: NewMCPToolSet(config, WithName("your-mcp-toolset"))
func NewMCPToolSet(config ConnectionConfig, opts ...ToolSetOption) *ToolSet {
	// Apply default configuration.
	cfg := toolSetConfig{
		connectionConfig: config,
		mcpOptions:       []mcp.ClientOption{}, // Initialize mcpOptions
		name:             "mcp",
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
	sessionManager := newMCPSessionManager(cfg.connectionConfig, cfg.mcpOptions, cfg.sessionReconnectConfig)

	toolSet := &ToolSet{
		config:         cfg,
		sessionManager: sessionManager,
		tools:          nil,
		name:           cfg.name,
	}

	return toolSet
}

// Init establishes the MCP session and preloads tools by calling
// Initialize and ListTools once. It returns any initialization error so
// callers can fail fast during startup.
func (ts *ToolSet) Init(ctx context.Context) error {
	if err := ts.listTools(ctx); err != nil {
		return fmt.Errorf("failed to initialize MCP tool set %q: %w", ts.name, err)
	}
	return nil
}

// Tools implements the ToolSet interface.
func (ts *ToolSet) Tools(ctx context.Context) []tool.Tool {
	if err := ts.listTools(ctx); err != nil {
		log.Error("Failed to refresh tools", err)
		// Return cached tools if refresh fails
	}

	ts.mu.RLock()
	defer ts.mu.RUnlock()

	// Since we control the creation of mcpTool instances and they all implement CallableTool,
	// we can safely do the type conversion. Using a more explicit approach for better readability.
	result := make([]tool.Tool, 0, len(ts.tools))
	for _, t := range ts.tools {
		// All tools created by newMCPTool implement CallableTool, so this should always succeed
		result = append(result, t)
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

	log.Debug("MCP tool set closed successfully")
	return nil
}

// Name implements the ToolSet interface.
func (ts *ToolSet) Name() string {
	return ts.name
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
	if ts.config.toolFilterFunc != nil {
		tools = tool.FilterTools(ctx, tools, ts.config.toolFilterFunc)
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
	config                 ConnectionConfig
	mcpOptions             []mcp.ClientOption      // MCP client options
	sessionReconnectConfig *SessionReconnectConfig // Session reconnection configuration
	client                 mcp.Connector
	mu                     sync.RWMutex
	connected              bool
	initialized            bool
	reconnectGroup         singleflight.Group // Ensures only one reconnection happens at a time
}

// newMCPSessionManager creates a new MCP session manager.
func newMCPSessionManager(config ConnectionConfig, mcpOptions []mcp.ClientOption, sessionReconnectConfig *SessionReconnectConfig) *mcpSessionManager {
	manager := &mcpSessionManager{
		config:                 config,
		mcpOptions:             mcpOptions,
		sessionReconnectConfig: sessionReconnectConfig,
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

	log.Debug("Connecting to MCP server", "transport", m.config.Transport)

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

	log.Debug("Successfully connected to MCP server")
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

	case transportSSE:
		var options []mcp.ClientOption

		if len(m.config.Headers) > 0 {
			headers := http.Header{}
			for k, v := range m.config.Headers {
				headers.Set(k, v)
			}
			options = append(options, mcp.WithHTTPHeaders(headers))
		}

		// Add MCP options if configured.
		if len(m.mcpOptions) > 0 {
			options = append(options, m.mcpOptions...)
		}

		return mcp.NewSSEClient(m.config.ServerURL, clientInfo, options...)

	case transportStreamable:
		var options []mcp.ClientOption

		if len(m.config.Headers) > 0 {
			headers := http.Header{}
			for k, v := range m.config.Headers {
				headers.Set(k, v)
			}
			options = append(options, mcp.WithHTTPHeaders(headers))
		}

		// Add MCP options if configured.
		if len(m.mcpOptions) > 0 {
			options = append(options, m.mcpOptions...)
		}

		return mcp.NewClient(m.config.ServerURL, clientInfo, options...)

	default:
		return nil, fmt.Errorf("unsupported transport: %s", m.config.Transport)
	}
}

// createTimeoutContext creates a context with timeout if configured and no existing deadline.
// Returns the context and a cancel function. The caller should defer the cancel function.
func (m *mcpSessionManager) createTimeoutContext(ctx context.Context, operation string) (context.Context, context.CancelFunc) {
	if m.config.Timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			timeoutCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
			log.Debug("Applied MCP timeout", "timeout", m.config.Timeout, "operation", operation)
			return timeoutCtx, cancel
		}
	}
	return ctx, func() {} // Return no-op cancel function for consistency.
}

// initialize initializes the MCP session.
func (m *mcpSessionManager) initialize(ctx context.Context) error {
	if m.initialized {
		return nil
	}

	log.Debug("Initializing MCP session")

	initCtx, cancel := m.createTimeoutContext(ctx, "initialize")
	defer cancel()
	initReq := &mcp.InitializeRequest{}
	initResp, err := m.client.Initialize(initCtx, initReq)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP session: %w", err)
	}

	log.Debug("MCP session initialized",
		"server_name", initResp.ServerInfo.Name,
		"server_version", initResp.ServerInfo.Version,
		"protocol_version", initResp.ProtocolVersion)

	m.initialized = true
	return nil
}

// listTools retrieves the list of available tools from the MCP server.
func (m *mcpSessionManager) listTools(ctx context.Context) ([]mcp.Tool, error) {
	var result []mcp.Tool

	// Execute with session reconnection support
	operationErr := m.executeWithSessionReconnect(ctx, func() error {
		m.mu.RLock()
		defer m.mu.RUnlock()

		if m.client == nil {
			return fmt.Errorf("transport is closed")
		}

		log.Debug("Listing tools from MCP server")

		listCtx, cancel := m.createTimeoutContext(ctx, "listTools")
		defer cancel()
		listReq := &mcp.ListToolsRequest{}
		listResp, listErr := m.client.ListTools(listCtx, listReq)
		if listErr != nil {
			return fmt.Errorf("failed to list tools: %w", listErr)
		}

		log.Debug("Listed tools from MCP server", "count", len(listResp.Tools))
		result = listResp.Tools
		return nil
	})

	return result, operationErr
}

// callTool executes a tool call on the MCP server.
func (m *mcpSessionManager) callTool(ctx context.Context, name string, arguments map[string]any) ([]mcp.Content, error) {
	var result []mcp.Content

	// Execute with session reconnection support
	operationErr := m.executeWithSessionReconnect(ctx, func() error {
		m.mu.RLock()
		defer m.mu.RUnlock()

		if m.client == nil {
			return fmt.Errorf("transport is closed")
		}

		log.Debug("Calling tool", "name", name, "arguments", arguments)

		toolCtx, cancel := m.createTimeoutContext(ctx, "callTool")
		defer cancel()
		callReq := &mcp.CallToolRequest{}
		callReq.Params.Name = name
		callReq.Params.Arguments = arguments

		callResp, callErr := m.client.CallTool(toolCtx, callReq)
		if callErr != nil {
			// Enhanced error with parameter information.
			enhancedErr := fmt.Errorf("failed to call tool %s: %w", name, callErr)
			log.Errorf("Tool call failed (name=%s, error=%v)", name, callErr)
			return enhancedErr
		}

		log.Debug("Tool call completed", "name", name, "content_count", len(callResp.Content))
		result = callResp.Content
		if callResp.StructuredContent != nil {
			structuredBytes, err := json.Marshal(callResp.StructuredContent)
			if err != nil {
				return fmt.Errorf("marshal structured content: %w", err)
			}
			result = append(result, mcp.NewTextContent(string(structuredBytes)))
		}
		return nil
	})

	return result, operationErr
}

// close closes the MCP session and client connection.
func (m *mcpSessionManager) close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected || m.client == nil {
		return nil
	}

	log.Debug("Closing MCP session")

	err := m.client.Close()
	m.connected = false
	m.initialized = false
	m.client = nil

	if err != nil {
		log.Error("Failed to close MCP client", "error", err)
		return fmt.Errorf("failed to close MCP client: %w", err)
	}

	log.Debug("MCP session closed successfully")
	return nil
}

// isConnected returns whether the session is connected and initialized.
func (m *mcpSessionManager) isConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected && m.initialized
}

// executeWithSessionReconnect executes an operation with automatic session reconnection support.
// Uses per-operation retry strategy: each operation gets independent reconnection attempts.
// If the operation fails with a session-expired error and session reconnection is enabled,
// it will attempt to recreate the session and retry the operation up to maxAttempts times.
func (m *mcpSessionManager) executeWithSessionReconnect(ctx context.Context, operation func() error) error {
	// Execute the operation first
	err := operation()
	if err == nil {
		return nil
	}

	// Check if session reconnection should be attempted
	if !m.shouldAttemptSessionReconnect(err) {
		return err
	}

	// Get max attempts from config
	maxAttempts := m.sessionReconnectConfig.MaxReconnectAttempts

	// Per-operation reconnection attempts
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Check if context is already cancelled or timed out
		if ctx.Err() != nil {
			log.Debugf("Context cancelled or timed out, stopping reconnection attempts (attempt=%d, error=%v)", attempt, ctx.Err())
			return fmt.Errorf("reconnection aborted: %w", ctx.Err())
		}

		log.Debugf("Session expired error detected, attempting session reconnection (attempt=%d/%d)", attempt, maxAttempts)

		// Attempt session reconnection
		if reconnectErr := m.recreateSession(ctx); reconnectErr != nil {
			log.Errorf("Session reconnection failed (attempt=%d/%d, reconnect_error=%v, original_error=%v)",
				attempt, maxAttempts, reconnectErr, err)

			// If this was the last attempt, return the original error
			if attempt >= maxAttempts {
				log.Warnf("Max session reconnect attempts reached for this operation, giving up (attempts=%d/%d)",
					attempt, maxAttempts)
				return err
			}

			// Continue to next attempt
			continue
		}

		log.Debugf("Session reconnection successful, retrying operation (attempt=%d)", attempt)

		// Retry the operation after successful reconnection
		err = operation()
		if err == nil {
			log.Debugf("Operation succeeded after session reconnection (attempt=%d)", attempt)
			return nil
		}

		// If operation still fails, check if we should retry reconnection
		if !m.shouldAttemptSessionReconnect(err) {
			// Different error type, don't retry
			return err
		}

		// If we have more attempts, continue the loop
		if attempt < maxAttempts {
			log.Debugf("Operation failed after reconnection, will retry (attempt=%d/%d, error=%v)",
				attempt, maxAttempts, err)
		}
	}

	// All attempts exhausted
	log.Warnf("All reconnection attempts exhausted for this operation (max_attempts=%d)", maxAttempts)
	return err
}

// shouldAttemptSessionReconnect determines if session reconnection should be attempted
// based on the error type and configuration.
func (m *mcpSessionManager) shouldAttemptSessionReconnect(err error) bool {
	// Check if session reconnection is enabled
	if m.sessionReconnectConfig == nil || !m.sessionReconnectConfig.EnableAutoReconnect {
		return false
	}

	if err == nil {
		return false
	}

	errStr := err.Error()

	// Check for connection/session errors that indicate reconnection should be attempted.
	for _, pattern := range sessionReconnectErrorPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// recreateSession recreates the MCP session by closing the old connection,
// creating a new client, and re-initializing the session.
// Uses singleflight to ensure only one reconnection happens at a time across all goroutines.
func (m *mcpSessionManager) recreateSession(ctx context.Context) error {
	// Use singleflight to ensure only one reconnection happens at a time
	// If multiple goroutines call this simultaneously, only one will execute,
	// and others will wait for the result
	_, err, _ := m.reconnectGroup.Do("reconnect", func() (any, error) {
		return nil, m.doRecreateSession(ctx)
	})
	return err
}

// doRecreateSession performs the actual session recreation logic.
func (m *mcpSessionManager) doRecreateSession(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Debug("Recreating MCP session")

	// Close existing client if any
	if m.client != nil {
		if closeErr := m.client.Close(); closeErr != nil {
			log.Warn("Failed to close old client during session recreation", "error", closeErr)
		}
		m.client = nil
	}

	// Reset connection state (will be set to true on success)
	m.connected = false
	m.initialized = false

	// Create new client
	client, err := m.createClient()
	if err != nil {
		return fmt.Errorf("failed to create new MCP client during session recreation: %w", err)
	}

	m.client = client
	m.connected = true

	// Re-initialize the session
	if err := m.initialize(ctx); err != nil {
		m.connected = false
		if closeErr := client.Close(); closeErr != nil {
			log.Error("Failed to close client after re-initialization failure", "close_error", closeErr, "init_error", err)
		}
		m.client = nil
		return fmt.Errorf("failed to re-initialize MCP session: %w", err)
	}

	log.Debug("MCP session recreation completed successfully")
	return nil
}
