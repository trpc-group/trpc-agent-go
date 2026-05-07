//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mcpbroker provides opt-in MCP discovery/call broker tools.
package mcpbroker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

// ErrClientOptionsProviderPanicked is the sentinel returned (wrapped via fmt.Errorf %w) when a
// ClientOptionsProvider panics. The broker recovers the panic so it never unwinds through
// trpc-mcp-go internals; callers can detect this case with errors.Is for observability or to
// fall back to default options. The recovered panic value is included in the wrapped error
// message via %v.
var ErrClientOptionsProviderPanicked = errors.New("ClientOptionsProvider panicked")

const (
	listServersToolName  = "mcp_list_servers"
	listToolsToolName    = "mcp_list_tools"
	inspectToolsToolName = "mcp_inspect_tools"
	callToolToolName     = "mcp_call"
)

const (
	originCode = "code"
)

const (
	targetTypeStdio = "stdio"
	targetTypeHTTP  = "http"
)

// Exported target type strings match ClientOptionsRequest.TargetType after resolution.
const (
	// TargetTypeHTTP indicates SSE or streamable HTTP MCP transport.
	TargetTypeHTTP = targetTypeHTTP
	// TargetTypeStdio indicates stdio MCP transport.
	TargetTypeStdio = targetTypeStdio
)

// Origin values for ClientOptionsRequest.Origin.
const (
	// OriginCode indicates a named server from WithServers.
	OriginCode = originCode
	// OriginAdhoc indicates an ad-hoc URL selector (requires WithAllowAdHocHTTP).
	OriginAdhoc = "adhoc"
)

var defaultSensitiveHeaderDenylist = []string{
	"authorization",
	"proxy-authorization",
	"cookie",
	"set-cookie",
	"x-api-key",
}

// Broker provides a small set of MCP management tools for agents.
type Broker struct {
	options brokerOptions
}

// Option configures a Broker.
type Option func(*brokerOptions)

type brokerOptions struct {
	servers                     map[string]mcpcfg.ConnectionConfig
	allowAdHocHTTP              bool
	adhocSensitiveHeaderDenyset map[string]struct{}
	httpHeaderInjector          HTTPHeaderInjector
	clientOptionsProvider       ClientOptionsProvider
	errorInterceptor            ErrorInterceptor
}

// ClientOptionsProvider supplies extra trpc-mcp-go client options after the broker has resolved
// the target and merged injected HTTP headers, but before the underlying MCP client is created.
// Returning (nil, nil) applies no extra options.
//
// # Trust boundary
//
// The provider is host-trusted code registered via WithClientOptionsProvider; it is not
// model-controlled or end-user-controlled input. The broker therefore does NOT subject the
// returned options to the ad-hoc header denylist or any other sanitisation. By design, the
// provider can override Authorization or any other header previously set by
// WithHTTPHeaderInjector - hosts rely on this for service-account auth, request signing, and
// audit hooks. The flip side is that a provider that accidentally shadows the injector's
// Authorization is a host bug, not a framework vulnerability.
//
// # Failure modes
//
//   - If the provider returns an error, the broker aborts before establishing the MCP connection.
//   - If the provider panics, the broker recovers and surfaces an error wrapping
//     ErrClientOptionsProviderPanicked (use errors.Is to detect this case) instead of unwinding
//     through trpc-mcp-go internals.
//   - nil entries inside the returned ClientOptions.HTTP / .Stdio slices are silently filtered out
//     so a conditional option builder that yields nil does not crash trpc-mcp-go.
//
// # Caveats
//
// HTTP-layer retries (e.g. tmcp.WithRetry) configured here are not aware of MCP-level side
// effects. Retrying tools/call automatically can cause duplicated side effects; hosts that need
// retry semantics for tool calls should implement them above the broker, gated by idempotency.
type ClientOptionsProvider func(context.Context, *ClientOptionsRequest) (*ClientOptions, error)

// ClientOptionsRequest is the broker-side context passed to ClientOptionsProvider before creating
// the underlying MCP client. Config is a defensive clone of the final ConnectionConfig for this
// call (see cloneConnectionConfig); mutations to Config (including its Headers map and Args slice)
// do not propagate back to the broker or the actual request.
type ClientOptionsRequest struct {
	Selector   string
	ServerName string
	Origin     string // OriginCode or OriginAdhoc
	TargetType string // TargetTypeHTTP or TargetTypeStdio

	Transport string
	BaseURL   string
	ToolName  string
	Phase     string // PhaseListTools | PhaseInspectTools | PhaseCallTool

	Config mcpcfg.ConnectionConfig
}

// ClientOptions carries optional HTTP and stdio client options. HTTP applies to SSE and streamable
// transports; Stdio applies to stdio transports.
//
// Default broker headers are applied first; options here follow and may override or extend
// behavior intentionally (see trpc-mcp-go ClientOption / StdioClientOption). For example, a host
// can use tmcp.WithHTTPBeforeRequest to rewrite an Authorization header set earlier by
// WithHTTPHeaderInjector. nil entries in HTTP / Stdio are filtered out before being applied.
type ClientOptions struct {
	HTTP  []tmcp.ClientOption
	Stdio []tmcp.StdioClientOption
}

// HTTPHeaderInjector resolves per-run HTTP headers for MCP requests.
type HTTPHeaderInjector func(context.Context, *HeaderInjectRequest) (map[string]string, error)

// HeaderInjectRequest describes an MCP HTTP request before execution.
type HeaderInjectRequest struct {
	Selector  string
	BaseURL   string
	ToolName  string
	Phase     string
	Transport string
	IsAdHoc   bool
}

// ErrorInterceptor lets business code classify and transform MCP HTTP errors.
type ErrorInterceptor func(context.Context, *BrokerErrorRequest) (*BrokerErrorDecision, error)

// BrokerErrorRequest describes an MCP HTTP operation error.
type BrokerErrorRequest struct {
	Selector  string
	BaseURL   string
	ToolName  string
	Phase     string
	Transport string
	IsAdHoc   bool
	Err       error
}

// BrokerErrorDecision controls how an intercepted error is surfaced.
type BrokerErrorDecision struct {
	WrapError error
	Handled   bool
}

// New creates a new MCP broker.
func New(opts ...Option) *Broker {
	options := brokerOptions{
		servers: make(map[string]mcpcfg.ConnectionConfig),
		adhocSensitiveHeaderDenyset: func() map[string]struct{} {
			result := make(map[string]struct{}, len(defaultSensitiveHeaderDenylist))
			for _, name := range defaultSensitiveHeaderDenylist {
				result[name] = struct{}{}
			}
			return result
		}(),
	}

	for _, opt := range opts {
		opt(&options)
	}

	return &Broker{options: options}
}

// WithServers adds named MCP server configurations provided by code.
func WithServers(servers map[string]mcpcfg.ConnectionConfig) Option {
	return func(opts *brokerOptions) {
		if len(servers) == 0 {
			return
		}
		if opts.servers == nil {
			opts.servers = make(map[string]mcpcfg.ConnectionConfig, len(servers))
		}
		for name, cfg := range servers {
			opts.servers[name] = cloneConnectionConfig(cfg)
		}
	}
}

// WithAllowAdHocHTTP controls whether ad-hoc HTTP MCP targets are allowed.
func WithAllowAdHocHTTP(enabled bool) Option {
	return func(opts *brokerOptions) {
		opts.allowAdHocHTTP = enabled
	}
}

// WithAdHocSensitiveHeaderDenylist adds case-insensitive denylist header names
// for ad-hoc HTTP MCP calls.
func WithAdHocSensitiveHeaderDenylist(headers []string) Option {
	return func(opts *brokerOptions) {
		if len(headers) == 0 {
			return
		}
		if opts.adhocSensitiveHeaderDenyset == nil {
			opts.adhocSensitiveHeaderDenyset = make(map[string]struct{})
		}
		for _, header := range headers {
			header = strings.ToLower(strings.TrimSpace(header))
			if header == "" {
				continue
			}
			opts.adhocSensitiveHeaderDenyset[header] = struct{}{}
		}
	}
}

// WithHTTPHeaderInjector injects per-run HTTP headers derived from context and target metadata.
func WithHTTPHeaderInjector(fn HTTPHeaderInjector) Option {
	return func(opts *brokerOptions) {
		opts.httpHeaderInjector = fn
	}
}

// WithClientOptionsProvider registers a hook that returns extra trpc-mcp-go client options after
// resolveTarget and WithHTTPHeaderInjector. Use this for URL policy, auditing, custom HTTP
// clients, or stdio options without adding parallel WithX hooks for each concern. See
// ClientOptionsProvider for the trust boundary, failure modes, and the HTTP-retry caveat.
func WithClientOptionsProvider(fn ClientOptionsProvider) Option {
	return func(opts *brokerOptions) {
		opts.clientOptionsProvider = fn
	}
}

// WithErrorInterceptor intercepts HTTP MCP execution errors and may translate them for model consumption.
func WithErrorInterceptor(fn ErrorInterceptor) Option {
	return func(opts *brokerOptions) {
		opts.errorInterceptor = fn
	}
}

// Tools returns the broker's MCP management tools.
func (b *Broker) Tools() []tool.Tool {
	return newBrokerTools(b)
}

type namedServer struct {
	Name       string
	Origin     string
	TargetType string
	Config     mcpcfg.ConnectionConfig
}

func (b *Broker) resolveNamedServers() ([]namedServer, map[string]namedServer, error) {
	merged := make(map[string]namedServer, len(b.options.servers))
	for name, cfg := range b.options.servers {
		server, serverErr := normalizeNamedServer(name, cfg, originCode)
		if serverErr != nil {
			return nil, nil, serverErr
		}
		if _, exists := merged[server.Name]; exists {
			return nil, nil, fmt.Errorf("duplicate MCP server name after normalization: %s", server.Name)
		}
		merged[server.Name] = server
	}

	list := make([]namedServer, 0, len(merged))
	for _, server := range merged {
		list = append(list, server)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	return list, merged, nil
}

func (b *Broker) resolveTarget(input targetInput) (resolvedTarget, error) {
	serverName := strings.TrimSpace(input.ServerName)
	urlValue := strings.TrimSpace(input.URL)

	if serverName == "" && urlValue == "" {
		return resolvedTarget{}, fmt.Errorf("exactly one of server_name or url is required")
	}

	if serverName != "" {
		_, merged, err := b.resolveNamedServers()
		if err != nil {
			return resolvedTarget{}, err
		}
		server, ok := merged[serverName]
		if !ok {
			return resolvedTarget{}, fmt.Errorf("unknown MCP server: %s", serverName)
		}
		return resolvedTarget{
			Name:       server.Name,
			Origin:     server.Origin,
			TargetType: server.TargetType,
			Config:     server.Config,
		}, nil
	}

	if !b.options.allowAdHocHTTP {
		return resolvedTarget{}, fmt.Errorf("ad-hoc HTTP MCP is disabled")
	}

	config, targetType, err := b.buildAdHocConfig(input)
	if err != nil {
		return resolvedTarget{}, err
	}
	return resolvedTarget{
		Origin:     OriginAdhoc,
		TargetType: targetType,
		Config:     config,
	}, nil
}

func (b *Broker) listServers(ctx context.Context, _ listServersInput) (listServersOutput, error) {
	servers, _, err := b.resolveNamedServers()
	if err != nil {
		return listServersOutput{}, err
	}

	output := listServersOutput{Servers: make([]listServersServer, 0, len(servers))}
	for _, server := range servers {
		output.Servers = append(output.Servers, listServersServer{
			Name:        server.Name,
			Transport:   server.Config.Transport,
			Description: server.Config.Description,
		})
	}
	return output, nil
}

func (b *Broker) resolveListSelector(
	selector string,
	transport string,
	headers map[string]string,
) (resolvedTarget, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return resolvedTarget{}, "", fmt.Errorf("selector is required")
	}

	if looksLikeHTTPSelector(selector) {
		target, err := b.resolveTarget(targetInput{
			URL:       selector,
			Transport: transport,
			Headers:   headers,
		})
		if err != nil {
			return resolvedTarget{}, "", err
		}
		return target, selector, nil
	}

	target, err := b.resolveTarget(targetInput{
		ServerName: selector,
		Transport:  transport,
		Headers:    headers,
	})
	if err != nil {
		return resolvedTarget{}, "", err
	}
	return target, selector, nil
}

func (b *Broker) resolveCallSelector(
	selector string,
	transport string,
	headers map[string]string,
) (resolvedTarget, string, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return resolvedTarget{}, "", "", fmt.Errorf("selector is required")
	}

	if looksLikeHTTPSelector(selector) {
		baseURL, toolName, err := splitHTTPToolSelector(selector)
		if err != nil {
			return resolvedTarget{}, "", "", err
		}
		target, targetErr := b.resolveTarget(targetInput{
			URL:       baseURL,
			Transport: transport,
			Headers:   headers,
		})
		if targetErr != nil {
			return resolvedTarget{}, "", "", targetErr
		}
		return target, baseURL, toolName, nil
	}

	serverName, toolName, err := b.splitNamedToolSelector(selector)
	if err != nil {
		return resolvedTarget{}, "", "", err
	}
	target, targetErr := b.resolveTarget(targetInput{
		ServerName: serverName,
		Transport:  transport,
		Headers:    headers,
	})
	if targetErr != nil {
		return resolvedTarget{}, "", "", targetErr
	}
	return target, serverName, toolName, nil
}

func (b *Broker) splitNamedToolSelector(selector string) (string, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", "", fmt.Errorf("selector is required")
	}

	_, merged, err := b.resolveNamedServers()
	if err != nil {
		return "", "", err
	}

	bestName := ""
	for name := range merged {
		prefix := name + "."
		if strings.HasPrefix(selector, prefix) && len(name) > len(bestName) {
			bestName = name
		}
	}
	if bestName != "" {
		toolName := strings.TrimSpace(strings.TrimPrefix(selector, bestName+"."))
		if toolName == "" {
			return "", "", fmt.Errorf("call selector must be <server>.<tool>, <url>.<tool>, or <url>#tool=<tool>")
		}
		return bestName, toolName, nil
	}

	lastDot := strings.LastIndex(selector, ".")
	if lastDot <= 0 || lastDot == len(selector)-1 {
		return "", "", fmt.Errorf("call selector must be <server>.<tool>, <url>.<tool>, or <url>#tool=<tool>")
	}

	serverName := strings.TrimSpace(selector[:lastDot])
	if _, ok := merged[serverName]; !ok {
		return "", "", fmt.Errorf("unknown MCP server: %s", serverName)
	}
	return serverName, strings.TrimSpace(selector[lastDot+1:]), nil
}

func splitHTTPToolSelector(selector string) (string, string, error) {
	selector = strings.TrimSpace(selector)
	if fragmentIndex := strings.Index(selector, "#tool="); fragmentIndex >= 0 {
		baseURL := strings.TrimSpace(selector[:fragmentIndex])
		toolName := strings.TrimSpace(selector[fragmentIndex+len("#tool="):])
		if baseURL == "" || toolName == "" {
			return "", "", fmt.Errorf("call selector must be <server>.<tool>, <url>.<tool>, or <url>#tool=<tool>")
		}
		return baseURL, toolName, nil
	}

	parsedURL, err := url.Parse(selector)
	if err != nil {
		return "", "", fmt.Errorf("invalid HTTP selector %q: %w", selector, err)
	}
	pathValue := parsedURL.EscapedPath()
	lastSlash := strings.LastIndex(pathValue, "/")
	segment := pathValue[lastSlash+1:]
	firstDot := strings.Index(segment, ".")
	if firstDot <= 0 || firstDot == len(segment)-1 {
		return "", "", fmt.Errorf("call selector must be <server>.<tool>, <url>.<tool>, or <url>#tool=<tool>")
	}

	toolName := strings.TrimSpace(segment[firstDot+1:])
	parsedURL.Path = strings.TrimSpace(pathValue[:lastSlash+1] + segment[:firstDot])
	parsedURL.RawPath = parsedURL.Path
	baseURL := strings.TrimSpace(parsedURL.String())
	if baseURL == "" || toolName == "" {
		return "", "", fmt.Errorf("call selector must be <server>.<tool>, <url>.<tool>, or <url>#tool=<tool>")
	}
	return baseURL, toolName, nil
}

func shouldUseFragmentHTTPToolSelector(selector string) bool {
	selector = strings.TrimSpace(selector)
	parsedURL, err := url.Parse(selector)
	if err != nil {
		return true
	}
	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return true
	}
	pathValue := parsedURL.EscapedPath()
	if pathValue == "" {
		return false
	}
	lastSlash := strings.LastIndex(pathValue, "/")
	segment := pathValue[lastSlash+1:]
	return strings.Contains(segment, ".")
}

func looksLikeHTTPSelector(selector string) bool {
	selector = strings.ToLower(strings.TrimSpace(selector))
	return strings.HasPrefix(selector, "http://") || strings.HasPrefix(selector, "https://")
}
