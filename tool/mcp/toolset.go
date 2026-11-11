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
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolretry"
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

const processAlreadyFinishedText = "process already finished"

func isBenignMCPClientCloseError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) ||
		strings.Contains(err.Error(), processAlreadyFinishedText)
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
// The default ToolSet name is "mcp". When attached to an LLMAgent, tools are
// exposed to the model as {name}_{remoteToolName}; pass WithName with a unique
// value per MCP server to avoid prefix collisions across multiple ToolSets.
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
		log.ErrorContext(ctx, "Failed to refresh tools", err)
		// Return cached tools if refresh fails.
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
	log.DebugContext(ctx, "Refreshing MCP tools")

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

	log.DebugContext(ctx, "Retrieved tools from MCP server", "count", len(mcpTools))

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

	log.DebugContext(ctx, "Successfully refreshed MCP tools", "count", len(tools))
	return nil
}

// mcpSessionManager manages the MCP client connection and session.
type mcpSessionManager struct {
	config                 ConnectionConfig
	mcpOptions             []mcp.ClientOption      // MCP client options
	sessionReconnectConfig *SessionReconnectConfig // Session reconnection configuration
	client                 mcp.Connector
	mu                     sessionLock
	connected              bool
	initialized            bool
	tasksEnabled           atomic.Bool

	reconnectMu      sync.Mutex
	reconnect        *sessionReconnect
	reconnectVersion uint64

	taskMu       sync.Mutex
	taskCalls    map[uint64]context.CancelFunc
	nextTaskCall uint64
	closingTasks int
}

// newMCPSessionManager creates a new MCP session manager.
func newMCPSessionManager(config ConnectionConfig, mcpOptions []mcp.ClientOption, sessionReconnectConfig *SessionReconnectConfig) *mcpSessionManager {
	config.Headers = maps.Clone(config.Headers)
	manager := &mcpSessionManager{
		config:                 config,
		mcpOptions:             mcpOptions,
		sessionReconnectConfig: sessionReconnectConfig,
	}

	return manager
}

// connect establishes connection to the MCP server.
func (m *mcpSessionManager) connect(ctx context.Context) error {
	if err := m.mu.lock(ctx); err != nil {
		return err
	}
	defer m.mu.Unlock()

	if m.connected {
		return nil
	}

	log.DebugContext(ctx, "Connecting to MCP server", "transport", m.config.Transport)

	client, err := m.createClient()
	if err != nil {
		return fmt.Errorf("failed to create MCP client: %w", err)
	}

	m.setClient(client)
	m.connected = true

	// Initialize the session.
	if err := m.initialize(ctx); err != nil {
		m.connected = false
		if closeErr := client.Close(); closeErr != nil &&
			!isBenignMCPClientCloseError(closeErr) {
			log.ErrorContext(ctx, "Failed to close client after initialization failure",
				"client_close_error", closeErr, "error", err)
		}
		return fmt.Errorf("failed to initialize MCP session: %w", err)
	}

	log.DebugContext(ctx, "Successfully connected to MCP server")
	return nil
}

// setClient installs a client under mu. Keep its task policy when a
// failed reconnect clears the client, so the next call still gets one deadline.
func (m *mcpSessionManager) setClient(client mcp.Connector) {
	m.client = client
	capability, ok := client.(toolTaskClient)
	m.tasksEnabled.Store(ok && capability.TasksEnabled())
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
		options := m.buildHTTPOptions()
		return mcp.NewSSEClient(m.config.ServerURL, clientInfo, options...)

	case transportStreamable:
		options := m.buildHTTPOptions()
		return mcp.NewClient(m.config.ServerURL, clientInfo, options...)

	default:
		return nil, fmt.Errorf("unsupported transport: %s", m.config.Transport)
	}
}

// buildHTTPOptions builds MCP client options for HTTP-based transports (SSE, Streamable).
//
// Static headers from ConnectionConfig.Headers are applied first via
// WithHTTPHeaders, followed by any user-provided options from WithMCPOptions
// in registration order. For per-request dynamic headers (e.g. user-specific
// auth tokens), pass mcp.WithHTTPBeforeRequest to WithMCPOptions and read the
// required values from context inside the hook. See the package-level
// documentation for an example.
func (m *mcpSessionManager) buildHTTPOptions() []mcp.ClientOption {
	var options []mcp.ClientOption

	if len(m.config.Headers) > 0 {
		headers := http.Header{}
		for k, v := range m.config.Headers {
			headers.Set(k, v)
		}
		options = append(options, mcp.WithHTTPHeaders(headers))
	}

	if len(m.mcpOptions) > 0 {
		options = append(options, m.mcpOptions...)
	}

	return options
}

// createTimeoutContext creates a context with timeout if configured and no existing deadline.
// Returns the context and a cancel function. The caller should defer the cancel function.
func (m *mcpSessionManager) createTimeoutContext(ctx context.Context, operation string) (context.Context, context.CancelFunc) {
	if m.config.Timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			timeoutCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
			log.DebugContext(ctx, "Applied MCP timeout", "timeout", m.config.Timeout, "operation", operation)
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

	log.DebugContext(ctx, "Initializing MCP session")

	initCtx, cancel := m.createTimeoutContext(ctx, "initialize")
	defer cancel()
	initReq := &mcp.InitializeRequest{}
	initResp, err := m.client.Initialize(initCtx, initReq)
	if err != nil {
		return fmt.Errorf("failed to initialize MCP session: %w", err)
	}

	log.DebugContext(ctx, "MCP session initialized",
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
		if err := m.mu.rlock(ctx); err != nil {
			return err
		}
		defer m.mu.RUnlock()

		if m.client == nil {
			return fmt.Errorf("transport is closed")
		}

		log.DebugContext(ctx, "Listing tools from MCP server")

		listCtx, cancel := m.createTimeoutContext(ctx, "listTools")
		defer cancel()
		listReq := &mcp.ListToolsRequest{}
		listResp, listErr := m.client.ListTools(listCtx, listReq)
		if listErr != nil {
			return fmt.Errorf("failed to list tools: %w", listErr)
		}

		log.DebugContext(ctx, "Listed tools from MCP server", "count", len(listResp.Tools))
		result = listResp.Tools
		return nil
	})

	return result, operationErr
}

// callTool executes a tool call on the MCP server.
// Returns the full CallToolResult including Meta field for metadata access.
func (m *mcpSessionManager) callTool(ctx context.Context, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	var result *mcp.CallToolResult
	var attempt toolCallAttempt
	if m.tasksEnabled.Load() {
		// Task recovery shares one deadline; ordinary clients keep their existing
		// per-attempt timeout behavior.
		var cancel context.CancelFunc
		ctx, cancel = m.createTimeoutContext(ctx, "callTool")
		defer cancel()
		ctx, cancel = m.taskCallContext(ctx)
		defer cancel()
	}

	// Execute with session reconnection support
	operationErr := m.executeWithSessionReconnect(ctx, func() error {
		if err := m.mu.rlock(ctx); err != nil {
			return err
		}
		defer m.mu.RUnlock()

		if m.client == nil {
			return fmt.Errorf("transport is closed")
		}

		log.DebugContext(ctx, "Calling tool", "name", name, "arguments", arguments)

		callReq := &mcp.CallToolRequest{}
		callReq.Params.Name = name
		callReq.Params.Arguments = arguments

		callCtx, cancel := m.createTimeoutContext(ctx, "callTool")
		defer cancel()
		callResp, callErr := attempt.call(callCtx, m.client, callReq)
		if callErr != nil {
			// Enhanced error with parameter information.
			enhancedErr := fmt.Errorf("failed to call tool %s: %w", name, callErr)
			log.ErrorfContext(ctx, "Tool call failed (name=%s, error=%v)", name, callErr)
			return enhancedErr
		}

		log.DebugContext(ctx, "Tool call completed", "name", name, "content_count", len(callResp.Content))
		// Handle structured content if present
		if len(callResp.Content) == 0 && callResp.StructuredContent != nil {
			structuredBytes, err := json.Marshal(callResp.StructuredContent)
			if err != nil {
				return fmt.Errorf("marshal structured content: %w", err)
			}
			// Create a copy with the marshaled content
			result = &mcp.CallToolResult{
				Result:            callResp.Result,
				Content:           append([]mcp.Content(nil), mcp.NewTextContent(string(structuredBytes))),
				StructuredContent: callResp.StructuredContent,
				IsError:           callResp.IsError,
			}
		} else {
			result = callResp
		}
		return nil
	})

	// A session failure can replace the task error before the SDK resumes it.
	if operationErr != nil {
		if attempt.unsafeErr != nil && !toolretry.IsReplayUnsafe(operationErr) {
			return nil, errors.Join(operationErr, attempt.unsafeErr)
		}
		return nil, operationErr
	}
	return result, nil
}

// taskCallContext binds a logical call and its reconnect attempts to Close.
func (m *mcpSessionManager) taskCallContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	m.taskMu.Lock()
	if m.closingTasks != 0 {
		m.taskMu.Unlock()
		cancel()
		return ctx, cancel
	}
	if m.taskCalls == nil {
		m.taskCalls = make(map[uint64]context.CancelFunc)
	}
	m.nextTaskCall++
	id := m.nextTaskCall
	m.taskCalls[id] = cancel
	m.taskMu.Unlock()
	return ctx, func() {
		cancel()
		m.taskMu.Lock()
		delete(m.taskCalls, id)
		m.taskMu.Unlock()
	}
}

// close closes the MCP session and client connection.
func (m *mcpSessionManager) close() error {
	// Polling holds a read lock until the SDK returns. Signal it before
	// waiting for the write lock, including when no call deadline is set.
	m.taskMu.Lock()
	m.closingTasks++
	for _, cancel := range m.taskCalls {
		cancel()
	}
	m.taskMu.Unlock()
	defer func() {
		m.taskMu.Lock()
		m.closingTasks--
		m.taskMu.Unlock()
	}()

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
		if isBenignMCPClientCloseError(err) {
			log.Debug("MCP client already finished during close", "error", err)
			return nil
		}
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
		// Check if context is already cancelled or timed out.
		if ctx.Err() != nil {
			log.DebugfContext(ctx, "Context cancelled or timed out, stopping reconnection attempts "+
				"(attempt=%d, error=%v)", attempt, ctx.Err())
			return fmt.Errorf("reconnection aborted: %w", errors.Join(ctx.Err(), err))
		}

		log.DebugfContext(ctx,
			"Session expired error detected, attempting session reconnection (attempt=%d/%d)",
			attempt, maxAttempts)

		// Attempt session reconnection.
		if reconnectErr := m.recreateSession(ctx); reconnectErr != nil {
			log.ErrorfContext(ctx, "Session reconnection failed (attempt=%d/%d, reconnect_error=%v, "+
				"original_error=%v)", attempt, maxAttempts, reconnectErr, err)

			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("reconnection aborted: %w", errors.Join(ctxErr, err, reconnectErr))
			}
			// If this was the last attempt, return the original error.
			if attempt >= maxAttempts {
				log.WarnfContext(ctx, "Max session reconnect attempts reached for this operation, "+
					"giving up (attempts=%d/%d)", attempt, maxAttempts)
				return err
			}

			// Continue to next attempt.
			continue
		}

		log.DebugfContext(ctx, "Session reconnection successful, retrying operation (attempt=%d)",
			attempt)

		// Retry the operation after successful reconnection.
		operationErr := operation()
		if operationErr == nil {
			log.DebugfContext(ctx, "Operation succeeded after session reconnection (attempt=%d)",
				attempt)
			return nil
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("reconnection aborted: %w", errors.Join(ctxErr, operationErr, err))
		}
		err = operationErr

		// If operation still fails, check if we should retry reconnection.
		if !m.shouldAttemptSessionReconnect(err) {
			// Different error type, don't retry.
			return err
		}

		// If we have more attempts, continue the loop.
		if attempt < maxAttempts {
			log.DebugfContext(ctx, "Operation failed after reconnection, will retry (attempt=%d/%d, error=%v)",
				attempt, maxAttempts, err)
		}
	}

	// All attempts exhausted.
	log.WarnfContext(ctx, "All reconnection attempts exhausted for this operation (max_attempts=%d)", maxAttempts)
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

	var sdkTaskErr *mcp.TaskError
	var rpcErr *mcp.TaskRPCError
	if errors.As(err, &sdkTaskErr) && (sdkTaskErr.Status != "" || errors.As(sdkTaskErr, &rpcErr)) {
		// Task status and JSON-RPC errors come from the server. Their message
		// text does not indicate transport failure.
		return false
	}

	var taskErr toolTaskFailure
	if errors.As(err, &taskErr) && !taskErr.ToolCallReplaySafe() {
		if taskErr.TaskID() == "" {
			return false
		}
		// The SDK's HTTP transport reports expired sessions with this wording.
		// Only task polling may use this fallback; never replay tools/call.
		if strings.Contains(err.Error(), "status code 404") {
			return true
		}
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

type sessionReconnect struct {
	done          chan struct{}
	err           error
	ownerCanceled bool
}

// recreateSession shares an in-flight reconnect. Waiters can leave on their
// own context without cancelling the caller that owns the reconnect.
func (m *mcpSessionManager) recreateSession(ctx context.Context) error {
	m.reconnectMu.Lock()
	version := m.reconnectVersion
	m.reconnectMu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.reconnectMu.Lock()
		if m.reconnectVersion != version {
			m.reconnectMu.Unlock()
			return nil
		}
		attempt := m.reconnect
		if attempt == nil {
			attempt = &sessionReconnect{
				done: make(chan struct{}),
				err:  errors.New("mcp session recreation did not complete"),
			}
			m.reconnect = attempt
			m.reconnectMu.Unlock()
			return m.runReconnect(ctx, attempt)
		}
		m.reconnectMu.Unlock()
		select {
		case <-attempt.done:
			if !attempt.ownerCanceled {
				return attempt.err
			}
			// The owner ran out of time. A live waiter may take over
			// without charging this aborted attempt to its budget.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *mcpSessionManager) runReconnect(ctx context.Context, attempt *sessionReconnect) error {
	defer func() {
		attempt.ownerCanceled = attempt.err != nil && ctx.Err() != nil
		m.reconnectMu.Lock()
		if attempt.err == nil {
			m.reconnectVersion++
		}
		m.reconnect = nil
		close(attempt.done)
		m.reconnectMu.Unlock()
	}()
	attempt.err = m.doRecreateSession(ctx)
	return attempt.err
}

// doRecreateSession closes the old client and initializes its replacement.
func (m *mcpSessionManager) doRecreateSession(ctx context.Context) error {
	if err := m.mu.lock(ctx); err != nil {
		return err
	}
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	log.DebugContext(ctx, "Recreating MCP session")

	// Close existing client if any.
	if m.client != nil {
		if closeErr := m.client.Close(); closeErr != nil &&
			!isBenignMCPClientCloseError(closeErr) {
			log.WarnContext(
				ctx,
				"Failed to close old client during session recreation",
				"error",
				closeErr,
			)
		}
		m.client = nil
	}

	// Reset connection state (will be set to true on success).
	m.connected = false
	m.initialized = false
	if err := ctx.Err(); err != nil {
		return err
	}

	// Create new client.
	client, err := m.createClient()
	if err != nil {
		return fmt.Errorf("failed to create new MCP client during session recreation: %w", err)
	}

	m.setClient(client)
	m.connected = true

	// Re-initialize the session.
	if err := m.initialize(ctx); err != nil {
		m.connected = false
		if closeErr := client.Close(); closeErr != nil &&
			!isBenignMCPClientCloseError(closeErr) {
			log.ErrorContext(ctx, "Failed to close client after re-initialization failure",
				"close_error", closeErr, "init_error", err)
		}
		m.client = nil
		return fmt.Errorf("failed to re-initialize MCP session: %w", err)
	}

	log.DebugContext(ctx, "MCP session recreation completed successfully")
	return nil
}
