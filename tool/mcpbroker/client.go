//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpcfg "trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

var defaultClientInfo = tmcp.Implementation{
	Name:    "trpc-agent-go",
	Version: "1.0.0",
}

type resolvedTarget struct {
	Name       string
	Origin     string
	TargetType string
	Config     mcpcfg.ConnectionConfig
}

type targetInput struct {
	ServerName string            `json:"server_name,omitempty"`
	URL        string            `json:"url,omitempty"`
	Transport  string            `json:"transport,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
}

const (
	phaseListTools    = "list_tools"
	phaseInspectTools = "inspect_tools"
	phaseCallTool     = "call_tool"
)

type operationMetadata struct {
	Selector string
	BaseURL  string
	ToolName string
	Phase    string
}

func (b *Broker) buildAdHocConfig(input targetInput) (mcpcfg.ConnectionConfig, string, error) {
	headers, err := b.sanitizeAdHocHeaders(input.Headers)
	if err != nil {
		return mcpcfg.ConnectionConfig{}, "", err
	}

	cfg, kind, err := normalizeConnectionConfig(mcpcfg.ConnectionConfig{
		Transport: strings.TrimSpace(input.Transport),
		ServerURL: strings.TrimSpace(input.URL),
		Headers:   headers,
	}, true)
	if err != nil {
		return mcpcfg.ConnectionConfig{}, "", err
	}

	targetType := targetTypeHTTP
	if kind == transportStdio {
		targetType = targetTypeStdio
	}
	return cfg, targetType, nil
}

func (b *Broker) sanitizeAdHocHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}

	result := make(map[string]string, len(headers))
	for key, value := range headers {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			return nil, fmt.Errorf("ad-hoc header name cannot be empty")
		}
		if _, denied := b.options.adhocSensitiveHeaderDenyset[normalized]; denied {
			return nil, fmt.Errorf("ad-hoc header %q is not allowed", key)
		}
		result[http.CanonicalHeaderKey(strings.TrimSpace(key))] = value
	}
	return result, nil
}

func (b *Broker) withPreparedHTTPHeaders(
	ctx context.Context,
	target resolvedTarget,
	meta operationMetadata,
) (mcpcfg.ConnectionConfig, error) {
	cfg := cloneConnectionConfig(target.Config)
	if target.TargetType != targetTypeHTTP {
		return cfg, nil
	}

	if b.options.httpHeaderInjector == nil {
		return cfg, nil
	}
	isAdHoc := target.Origin == "adhoc"
	injected, err := b.options.httpHeaderInjector(ctx, &HeaderInjectRequest{
		Selector:  meta.Selector,
		BaseURL:   meta.BaseURL,
		ToolName:  meta.ToolName,
		Phase:     meta.Phase,
		Transport: cfg.Transport,
		IsAdHoc:   isAdHoc,
	})
	if err != nil {
		return mcpcfg.ConnectionConfig{}, err
	}
	if len(injected) == 0 {
		return cfg, nil
	}

	cfg.Headers = mergeHeaders(cfg.Headers, injected)
	return cfg, nil
}

func mergeHeaders(base map[string]string, extra map[string]string) map[string]string {
	switch {
	case len(base) == 0 && len(extra) == 0:
		return nil
	case len(base) == 0:
		return canonicalizeHeaders(extra)
	case len(extra) == 0:
		return canonicalizeHeaders(base)
	}

	result := canonicalizeHeaders(base)
	for key, value := range extra {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		result[http.CanonicalHeaderKey(trimmed)] = value
	}
	return result
}

func canonicalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		result[http.CanonicalHeaderKey(trimmed)] = value
	}
	return result
}

func interceptHTTPOperationError(
	ctx context.Context,
	b *Broker,
	target resolvedTarget,
	meta operationMetadata,
	err error,
) (bool, error) {

	if err == nil || target.TargetType != targetTypeHTTP || b.options.errorInterceptor == nil {
		return false, err
	}

	decision, interceptErr := b.options.errorInterceptor(ctx, &BrokerErrorRequest{
		Selector:  meta.Selector,
		BaseURL:   meta.BaseURL,
		ToolName:  meta.ToolName,
		Phase:     meta.Phase,
		Transport: target.Config.Transport,
		IsAdHoc:   target.Origin == "adhoc",
		Err:       err,
	})
	if interceptErr != nil {
		return true, interceptErr
	}
	if decision == nil || !decision.Handled {
		return false, err
	}
	if decision.WrapError != nil {
		return true, decision.WrapError
	}
	return true, fmt.Errorf("broker error interceptor handled the error but returned no wrapped error")
}

func createClient(cfg mcpcfg.ConnectionConfig) (tmcp.Connector, error) {
	clientInfo := cfg.ClientInfo
	if clientInfo.Name == "" {
		clientInfo = defaultClientInfo
	}

	_, kind, err := normalizeConnectionConfig(cfg, false)
	if err != nil {
		return nil, err
	}

	switch kind {
	case transportStdio:
		return tmcp.NewStdioClient(tmcp.StdioTransportConfig{
			ServerParams: tmcp.StdioServerParameters{
				Command: cfg.Command,
				Args:    cfg.Args,
			},
			Timeout: cfg.Timeout,
		}, clientInfo)
	case transportSSE:
		return tmcp.NewSSEClient(cfg.ServerURL, clientInfo, httpHeaderOptions(cfg.Headers)...)
	case transportStreamable:
		return tmcp.NewClient(cfg.ServerURL, clientInfo, httpHeaderOptions(cfg.Headers)...)
	default:
		return nil, fmt.Errorf("unsupported transport: %s", cfg.Transport)
	}
}

func httpHeaderOptions(headers map[string]string) []tmcp.ClientOption {
	if len(headers) == 0 {
		return nil
	}

	httpHeaders := http.Header{}
	for key, value := range headers {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		httpHeaders.Set(http.CanonicalHeaderKey(trimmed), value)
	}
	return []tmcp.ClientOption{tmcp.WithHTTPHeaders(httpHeaders)}
}

func withTimeoutContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	timeoutDeadline := time.Now().Add(timeout)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline && deadline.Before(timeoutDeadline) {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func withOneShotClient[T any](ctx context.Context, cfg mcpcfg.ConnectionConfig, fn func(context.Context, tmcp.Connector) (T, error)) (T, error) {
	var zero T

	client, err := createClient(cfg)
	if err != nil {
		return zero, err
	}
	defer client.Close()

	initCtx, cancel := withTimeoutContext(ctx, cfg.Timeout)
	defer cancel()

	if _, err := client.Initialize(initCtx, &tmcp.InitializeRequest{}); err != nil {
		return zero, fmt.Errorf("initialize MCP client: %w", err)
	}

	opCtx, opCancel := withTimeoutContext(ctx, cfg.Timeout)
	defer opCancel()

	return fn(opCtx, client)
}
