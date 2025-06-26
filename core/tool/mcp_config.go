package tool

import (
	"context"
	"regexp"
	"time"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

type Transport string

const (
	TransportStdio      Transport = "stdio"
	TransportSSE        Transport = "sse"
	TransportStreamable Transport = "streamable"
)

// MCPConnectionConfig defines the configuration for connecting to an MCP server.
type MCPConnectionConfig struct {
	// Transport specifies the transport method: "stdio", "sse", "streamable_http".
	Transport Transport `json:"transport"`

	// HTTP/SSE configuration.
	ServerURL string            `json:"server_url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`

	// STDIO configuration.
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`

	// Common configuration.
	Timeout time.Duration `json:"timeout,omitempty"`

	// Advanced configuration (optional).
	ClientInfo mcp.Implementation     `json:"client_info,omitempty"`
	Auth       *AuthConfig            `json:"auth,omitempty"`
	Retry      *RetryConfig           `json:"retry,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// mcpToolSetConfig holds internal configuration for MCPToolSet.
type mcpToolSetConfig struct {
	connectionConfig MCPConnectionConfig
	retryConfig      *RetryConfig
	authConfig       *AuthConfig
	toolFilter       ToolFilter
	autoRefresh      time.Duration
}

// MCPToolSetOption is a function type for configuring MCPToolSet.
type MCPToolSetOption func(*mcpToolSetConfig)

// contextKey is a custom type for context keys to avoid collisions.
type contextKey string

const (
	toolContextKey contextKey = "tool_context"
)

// WithToolContext adds a ToolContext to the given context.
func WithToolContext(ctx context.Context, toolCtx *ToolContext) context.Context {
	return context.WithValue(ctx, toolContextKey, toolCtx)
}

// GetToolContext retrieves the ToolContext from the given context.
// Returns the ToolContext and true if found, nil and false otherwise.
func GetToolContext(ctx context.Context) (*ToolContext, bool) {
	toolCtx, ok := ctx.Value(toolContextKey).(*ToolContext)
	return toolCtx, ok
}

// RetryConfig configures retry behavior for MCP operations.
type RetryConfig struct {
	Enabled       bool          `json:"enabled"`
	MaxAttempts   int           `json:"max_attempts"`
	InitialDelay  time.Duration `json:"initial_delay"`
	BackoffFactor float64       `json:"backoff_factor"`
	MaxDelay      time.Duration `json:"max_delay"`
}

// AuthConfig configures authentication for MCP connections.
type AuthConfig struct {
	Type        AuthType               `json:"type"`
	Credentials map[string]interface{} `json:"credentials"`
	Options     map[string]interface{} `json:"options"`
}

// AuthType defines the authentication method.
type AuthType string

const (
	AuthTypeNone   AuthType = "none"
	AuthTypeBearer AuthType = "bearer"
	AuthTypeBasic  AuthType = "basic"
	AuthTypeOAuth2 AuthType = "oauth2"
)

// ToolContext contains context information for tool execution.
type ToolContext struct {
	AgentID     string                 `json:"agent_id"`
	SessionID   string                 `json:"session_id"`
	UserID      string                 `json:"user_id"`
	RequestID   string                 `json:"request_id"`
	Permissions []string               `json:"permissions"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// ToolFilter defines the interface for filtering tools.
type ToolFilter interface {
	Filter(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo
}

// ToolFilterFunc is a function type that implements ToolFilter interface.
type ToolFilterFunc func(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo

// Filter implements the ToolFilter interface.
func (f ToolFilterFunc) Filter(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo {
	return f(ctx, tools)
}

// ToolNameFilter filters tools by a list of allowed tool names.
type ToolNameFilter struct {
	AllowedNames []string
	Mode         FilterMode
}

// FilterMode defines how the filter should behave.
type FilterMode string

const (
	FilterModeInclude FilterMode = "include" // Only include listed tools
	FilterModeExclude FilterMode = "exclude" // Exclude listed tools
)

// Filter implements the ToolFilter interface.
func (f *ToolNameFilter) Filter(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo {
	if len(f.AllowedNames) == 0 {
		return tools
	}

	nameSet := make(map[string]bool)
	for _, name := range f.AllowedNames {
		nameSet[name] = true
	}

	var filtered []MCPToolInfo
	for _, tool := range tools {
		inSet := nameSet[tool.Name]

		switch f.Mode {
		case FilterModeInclude:
			if inSet {
				filtered = append(filtered, tool)
			}
		case FilterModeExclude:
			if !inSet {
				filtered = append(filtered, tool)
			}
		default:
			// Default to include mode
			if inSet {
				filtered = append(filtered, tool)
			}
		}
	}

	return filtered
}

// CompositeFilter combines multiple filters using AND logic.
type CompositeFilter struct {
	Filters []ToolFilter
}

// Filter implements the ToolFilter interface.
func (f *CompositeFilter) Filter(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo {
	result := tools
	for _, filter := range f.Filters {
		result = filter.Filter(ctx, result)
	}
	return result
}

// PatternFilter filters tools using pattern matching on names and descriptions.
type PatternFilter struct {
	NamePatterns        []string
	DescriptionPatterns []string
	Mode                FilterMode
}

// Filter implements the ToolFilter interface.
func (f *PatternFilter) Filter(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo {
	if len(f.NamePatterns) == 0 && len(f.DescriptionPatterns) == 0 {
		return tools
	}

	var filtered []MCPToolInfo
	for _, tool := range tools {
		matches := f.matchesTool(tool)

		switch f.Mode {
		case FilterModeInclude:
			if matches {
				filtered = append(filtered, tool)
			}
		case FilterModeExclude:
			if !matches {
				filtered = append(filtered, tool)
			}
		default:
			// Default to include mode.
			if matches {
				filtered = append(filtered, tool)
			}
		}
	}

	return filtered
}

// matchesTool checks if a tool matches any of the patterns.
func (f *PatternFilter) matchesTool(tool MCPToolInfo) bool {
	// Check name patterns.
	for _, pattern := range f.NamePatterns {
		if matched, _ := regexp.MatchString(pattern, tool.Name); matched {
			return true
		}
	}

	// Check description patterns.
	for _, pattern := range f.DescriptionPatterns {
		if matched, _ := regexp.MatchString(pattern, tool.Description); matched {
			return true
		}
	}

	return false
}

// MCPToolInfo contains metadata about an MCP tool.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// WithRetry configures retry behavior for MCP operations.
func WithRetry(config RetryConfig) MCPToolSetOption {
	return func(c *mcpToolSetConfig) {
		c.retryConfig = &config
	}
}

// WithAuth configures authentication for MCP connections.
func WithAuth(config AuthConfig) MCPToolSetOption {
	return func(c *mcpToolSetConfig) {
		c.authConfig = &config
	}
}

// WithToolFilter configures tool filtering.
func WithToolFilter(filter ToolFilter) MCPToolSetOption {
	return func(c *mcpToolSetConfig) {
		c.toolFilter = filter
	}
}

// WithAutoRefresh configures automatic tool list refresh.
func WithAutoRefresh(interval time.Duration) MCPToolSetOption {
	return func(c *mcpToolSetConfig) {
		c.autoRefresh = interval
	}
}

// Default configurations
var (
	defaultRetryConfig = &RetryConfig{
		Enabled:       true,
		MaxAttempts:   3,
		InitialDelay:  100 * time.Millisecond,
		BackoffFactor: 2.0,
		MaxDelay:      5 * time.Second,
	}

	defaultClientInfo = mcp.Implementation{
		Name:    "trpc-agent-go",
		Version: "1.0.0",
	}
)

// Convenience functions for creating tool filters

// NewIncludeFilter creates a filter that only includes specified tool names.
func NewIncludeFilter(toolNames ...string) ToolFilter {
	return &ToolNameFilter{
		AllowedNames: toolNames,
		Mode:         FilterModeInclude,
	}
}

// NewExcludeFilter creates a filter that excludes specified tool names.
func NewExcludeFilter(toolNames ...string) ToolFilter {
	return &ToolNameFilter{
		AllowedNames: toolNames,
		Mode:         FilterModeExclude,
	}
}

// NewPatternIncludeFilter creates a filter that includes tools matching name patterns.
func NewPatternIncludeFilter(namePatterns ...string) ToolFilter {
	return &PatternFilter{
		NamePatterns: namePatterns,
		Mode:         FilterModeInclude,
	}
}

// NewPatternExcludeFilter creates a filter that excludes tools matching name patterns.
func NewPatternExcludeFilter(namePatterns ...string) ToolFilter {
	return &PatternFilter{
		NamePatterns: namePatterns,
		Mode:         FilterModeExclude,
	}
}

// NewDescriptionFilter creates a filter that matches tools by description patterns.
func NewDescriptionFilter(descPatterns ...string) ToolFilter {
	return &PatternFilter{
		DescriptionPatterns: descPatterns,
		Mode:                FilterModeInclude,
	}
}

// NewCompositeFilter creates a composite filter that applies multiple filters.
func NewCompositeFilter(filters ...ToolFilter) ToolFilter {
	return &CompositeFilter{
		Filters: filters,
	}
}

// NewFuncFilter creates a filter from a function.
func NewFuncFilter(filterFunc func(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo) ToolFilter {
	return ToolFilterFunc(filterFunc)
}

// NoFilter returns all tools without filtering.
var NoFilter ToolFilter = ToolFilterFunc(func(ctx context.Context, tools []MCPToolInfo) []MCPToolInfo {
	return tools
})

// MCPErrorCode represents specific MCP error types for better diagnosis.
type MCPErrorCode string

const (
	MCPErrorUnknown              MCPErrorCode = "unknown"
	MCPErrorConnectionFailed     MCPErrorCode = "connection_failed"
	MCPErrorAuthenticationFailed MCPErrorCode = "authentication_failed"
	MCPErrorToolNotFound         MCPErrorCode = "tool_not_found"
	MCPErrorInvalidParameters    MCPErrorCode = "invalid_parameters"
	MCPErrorMissingParameters    MCPErrorCode = "missing_parameters"
	MCPErrorTypeValidation       MCPErrorCode = "type_validation"
	MCPErrorPermissionDenied     MCPErrorCode = "permission_denied"
	MCPErrorServerError          MCPErrorCode = "server_error"
	MCPErrorTimeout              MCPErrorCode = "timeout"
	MCPErrorInvalidResponse      MCPErrorCode = "invalid_response"
)

// MCPError represents an enhanced error with diagnostic information.
type MCPError struct {
	Code        MCPErrorCode           `json:"code"`
	Message     string                 `json:"message"`
	OriginalErr error                  `json:"-"`
	Context     map[string]interface{} `json:"context,omitempty"`
	Suggestions []string               `json:"suggestions,omitempty"`
	Details     *ErrorDetails          `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *MCPError) Error() string {
	if e.Details != nil && e.Details.UserFriendlyMessage != "" {
		return e.Details.UserFriendlyMessage
	}
	return e.Message
}

// Unwrap returns the original error for error unwrapping.
func (e *MCPError) Unwrap() error {
	return e.OriginalErr
}

// ErrorDetails contains detailed diagnostic information.
type ErrorDetails struct {
	UserFriendlyMessage string                 `json:"user_friendly_message"`
	TechnicalDetails    string                 `json:"technical_details"`
	ExpectedParameters  []ParameterInfo        `json:"expected_parameters,omitempty"`
	ProvidedParameters  map[string]interface{} `json:"provided_parameters,omitempty"`
	AvailableTools      []string               `json:"available_tools,omitempty"`
	ServerResponse      interface{}            `json:"server_response,omitempty"`
}

// ParameterInfo describes expected parameter information.
type ParameterInfo struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    bool        `json:"required"`
	Description string      `json:"description"`
	Example     interface{} `json:"example,omitempty"`
}

// DiagnosticInfo contains context information for error diagnosis.
type DiagnosticInfo struct {
	ToolName           string                 `json:"tool_name"`
	Operation          string                 `json:"operation"`
	ProvidedArgs       map[string]interface{} `json:"provided_args"`
	ExpectedSchema     interface{}            `json:"expected_schema"`
	AvailableTools     []string               `json:"available_tools"`
	ConnectionInfo     map[string]interface{} `json:"connection_info"`
	SessionContext     *ToolContext           `json:"session_context"`
	ServerCapabilities []string               `json:"server_capabilities"`
}
