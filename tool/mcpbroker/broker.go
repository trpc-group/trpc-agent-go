//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package mcpbroker provides opt-in MCP discovery/call broker tools
// (mcp_list_servers, mcp_list_tools, mcp_inspect_tools, mcp_call) that an LLM
// agent can use to discover and invoke remote MCP servers at runtime.
//
// # Untrusted remote content
//
// The broker is a passthrough: it forwards remote MCP server output - tool names
// and descriptions, tool result Content, error messages - verbatim to the model.
// Remote content is therefore untrusted from the host's perspective and may be
// crafted to attempt prompt injection, tool-name spoofing, or data exfiltration
// via the model's tool-use loop. The broker intentionally does not sanitise this
// content because removing it would also remove the information the agent
// legitimately needs to reason about the remote server.
//
// Hosts that connect to MCP servers outside their trust domain - especially when
// ad-hoc HTTP is enabled via WithAllowAdHocHTTP - should:
//
//   - Constrain which servers or URLs the agent can reach via WithServers,
//     WithAllowAdHocHTTP, and WithClientOptionsProvider.
//   - Treat remote tool output Content as untrusted input when composing
//     downstream prompts and tool chains.
//   - Use WithErrorInterceptor to redact internal-topology details from MCP
//     server errors that would otherwise reach the model.
package mcpbroker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

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
	adhocHTTPTimeout            time.Duration
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

// HTTPHeaderInjector resolves per-run HTTP headers for MCP requests. The broker
// calls it after target resolution and before WithClientOptionsProvider, and
// merges the returned headers into the outgoing request.
//
// # Trust BaseURL with care for ad-hoc targets
//
// When HeaderInjectRequest.IsAdHoc is true, BaseURL originates from a
// model-supplied URL and may have been chosen by an attacker via prompt
// injection. Returning a host secret (OAuth token, session cookie, vendor API
// key) here without first cross-checking that BaseURL.Host belongs to a trusted
// destination can exfiltrate that secret to whatever endpoint the model picks.
// The recommended pattern is:
//
//   - For named servers (IsAdHoc == false): inject the secret bound to that
//     server name.
//   - For ad-hoc targets (IsAdHoc == true): either skip injection, or only
//     inject when the parsed BaseURL.Host is on a host-defined allowlist.
type HTTPHeaderInjector func(context.Context, *HeaderInjectRequest) (map[string]string, error)

// HeaderInjectRequest describes an MCP HTTP request before execution. IsAdHoc
// distinguishes trusted named-server targets from model-supplied ad-hoc URLs;
// see HTTPHeaderInjector for the implications when injecting secrets.
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
//
// When enabled, mcp_list_tools / mcp_inspect_tools / mcp_call accept model-supplied
// URLs (selectors that look like an http:// or https:// URL) instead of named
// servers from WithServers. This is a powerful surface and a risky one: the broker
// only enforces the http/https scheme and the ad-hoc header denylist (see
// WithAdHocSensitiveHeaderDenylist); it does NOT validate destination identity,
// host policy, or reachability.
//
// # SSRF risk
//
// Without further controls, a sufficiently-instructed model can ask the broker to
// connect to internal addresses (localhost, link-local, cloud metadata endpoints,
// intranet services), exfiltrate data to attacker-controlled hosts, or probe
// internal networks. URL allow/deny policy is the host's responsibility.
//
// # Recommended pattern
//
// Use WithClientOptionsProvider together with tmcp.WithHTTPBeforeRequest to enforce
// a per-deployment URL policy. The provider receives
// ClientOptionsRequest.Origin == OriginAdhoc for model-supplied URLs, so a host can
// reject or rewrite them before the connection is established.
//
// # Hang risk and bounding
//
// trpc-mcp-go's underlying http.Client has no built-in request timeout, so an
// ad-hoc MCP call against a stalled or hostile remote can hang for as long as the
// caller-supplied context permits. When that context has no deadline (common in
// long-running agent runs or background jobs), the call can hang indefinitely and
// occupy a goroutine plus a TCP connection until the OS gives up. Hosts that
// enable ad-hoc HTTP should bound it explicitly via WithAdHocHTTPTimeout, or via
// WithClientOptionsProvider for finer-grained per-call policy.
func WithAllowAdHocHTTP(enabled bool) Option {
	return func(opts *brokerOptions) {
		opts.allowAdHocHTTP = enabled
	}
}

// WithAdHocHTTPTimeout sets the upper bound on the total wall-clock time the
// broker spends on a single ad-hoc HTTP MCP operation (mcp_list_tools,
// mcp_inspect_tools, or mcp_call). The bound covers Initialize and every MCP
// RPC the operation issues; the broker wraps the entire operation in
// context.WithTimeout once, taking whichever deadline is tighter between this
// value and the caller-supplied context. An ad-hoc target is one resolved
// from an http/https URL selector rather than a name configured via
// WithServers.
//
// # Why this option is opt-in
//
// trpc-mcp-go's underlying http.Client has no built-in request timeout, so an
// ad-hoc MCP call against a stalled or hostile remote can hang indefinitely
// when the caller-supplied context has no deadline. Because the appropriate
// bound is workload-specific (short tool calls might warrant a few seconds;
// analytical tools may need minutes), the broker exposes this option instead
// of imposing a framework-wide default that would be wrong for many
// deployments.
//
// # Values
//
//   - d > 0: apply d as the per-operation deadline upper bound for ad-hoc
//     HTTP calls. The clock starts before Initialize and stops after the
//     last MCP RPC needed for the operation.
//   - d <= 0: no broker-level deadline (default). The operation then relies
//     entirely on the caller-supplied context deadline; without one, a
//     stalled remote can hang the operation indefinitely. Only choose this
//     when an upstream deadline source is guaranteed.
//
// # Scope
//
// This option only affects ad-hoc HTTP operations. Named servers configured
// via WithServers continue to use their own ConnectionConfig.Timeout, which
// is not modified by this option.
func WithAdHocHTTPTimeout(d time.Duration) Option {
	return func(opts *brokerOptions) {
		if d < 0 {
			d = 0
		}
		opts.adhocHTTPTimeout = d
	}
}

// WithAdHocSensitiveHeaderDenylist adds case-insensitive header names that the
// broker must reject when the model supplies them via the "headers" parameter of
// mcp_list_tools / mcp_inspect_tools / mcp_call for an ad-hoc HTTP MCP target.
// Comparison is performed against the lowercase name; entries here extend (never
// shrink) the built-in default.
//
// # Default coverage
//
// The built-in default covers the protocol-standard auth headers - Authorization,
// Proxy-Authorization, Cookie, Set-Cookie - plus X-API-Key, a de-facto industry
// standard across cloud providers and API gateways. These are intentionally
// narrow: every host will agree the model should not be able to spell them.
//
// # Vendor-specific headers are the host's responsibility
//
// Many ecosystems use non-standard token-bearing headers - OpenStack uses
// X-Auth-Token, AWS uses X-Amz-Security-Token, GitLab uses Private-Token, and
// many internal systems invent their own. The broker deliberately does NOT add
// these to the default list. Doing so would silently reject legitimate model
// traffic in deployments that do not use those vendors, and would create a false
// sense of safety in deployments that do (an attacker can always rename the
// exfiltration header). Hosts whose MCP servers - or whose neighbouring services
// reachable on the same network - rely on such headers should pass them in via
// this option.
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
