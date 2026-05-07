//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package identity

import (
	"context"
	"maps"
)

// Identity represents a resolved user identity with credentials and metadata
// that can be injected into tool calls.
//
// Identity values are treated as immutable after Provider.Resolve returns.
// Plugin.beforeAgent stores the returned *Identity in invocation state and
// beforeTool may read Headers and EnvVars concurrently during parallel tool
// calls. Providers must not mutate the returned Identity or any of its maps
// (Headers, EnvVars, Extra) after returning. Helpers such as HeadersFromContext
// and EnvVarsFromContext clone the top-level map on read, but they do not
// deep-copy values, so any reference-typed entry placed in Extra must itself
// be immutable (or the caller is responsible for its concurrency safety).
type Identity struct {
	// UserID is the authenticated user identifier.
	UserID string

	// Token is an opaque bearer token (e.g., OAuth access token).
	Token string

	// Signature is a business-level request signature for custom auth schemes.
	Signature string

	// Headers are key-value pairs to inject into HTTP-based tool calls
	// (e.g., MCP SSE/Streamable HTTP, webhook invocations).
	Headers map[string]string

	// EnvVars are key-value pairs to expose through context for
	// command-execution tools (e.g., workspace_exec, skill_run).
	EnvVars map[string]string

	// Extra holds arbitrary extension data that business code may need.
	//
	// Reference-typed values (slice, map, pointer) stored here are NOT
	// deep-cloned by EnvVarsFromContext / HeadersFromContext. If any value
	// may be mutated after Provider.Resolve returns, callers must either
	// store an immutable copy or guard the value with their own
	// synchronization.
	Extra map[string]any
}

// Provider resolves the current user identity from the execution context.
// Implementations typically extract identity from HTTP request headers, JWT
// claims, session stores, or business-specific signing services.
//
// The returned Identity must be treated as immutable after Resolve returns.
// If an implementation needs to keep mutating its own source data, it should
// deep-clone before returning.
type Provider interface {
	Resolve(ctx context.Context, userID string, sessionID string) (*Identity, error)
}

// ProviderFunc is a convenience adapter to allow the use of ordinary functions
// as Provider implementations.
type ProviderFunc func(ctx context.Context, userID, sessionID string) (*Identity, error)

// Resolve implements Provider.
func (f ProviderFunc) Resolve(ctx context.Context, userID, sessionID string) (*Identity, error) {
	return f(ctx, userID, sessionID)
}

// ---- context helpers ----

type identityCtxKey struct{}

// NewContext returns a copy of ctx that carries the given Identity.
func NewContext(ctx context.Context, id *Identity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// FromContext extracts the Identity stored in ctx by NewContext.
// Returns nil and false if no identity is present.
//
// A typed-nil value stored via NewContext(ctx, nil) is normalized here to
// (nil, false), so callers branching on ok always see a consistent notion of
// "identity absent".
func FromContext(ctx context.Context) (*Identity, bool) {
	if ctx == nil {
		return nil, false
	}
	id, ok := ctx.Value(identityCtxKey{}).(*Identity)
	if !ok || id == nil {
		return nil, false
	}
	return id, true
}

// HeadersFromContext returns a copy of identity headers stored in ctx.
//
// Typical usage is inside an mcp.WithHTTPBeforeRequest hook passed through
// tool/mcp.WithMCPOptions, so every outgoing MCP HTTP request picks up the
// current user's headers:
//
//	toolmcp.WithMCPOptions(tmcp.WithHTTPBeforeRequest(
//	    func(ctx context.Context, req *http.Request) error {
//	        headers, err := identity.HeadersFromContext(ctx)
//	        if err != nil {
//	            return err
//	        }
//	        for k, v := range headers {
//	            req.Header.Set(k, v)
//	        }
//	        return nil
//	    },
//	))
//
// The two-return-value signature (headers, error) intentionally mirrors a
// hook that may fail when resolving identity; in practice HeadersFromContext
// itself never returns a non-nil error today, so callers can ignore it if
// preferred.
func HeadersFromContext(ctx context.Context) (map[string]string, error) {
	id, ok := FromContext(ctx)
	if !ok || id == nil || len(id.Headers) == 0 {
		return nil, nil
	}
	return maps.Clone(id.Headers), nil
}

// EnvVarsFromContext returns a copy of identity environment variables stored
// in ctx. It returns nil when no identity env is available.
//
// This function intentionally matches the codeexecutor.RunEnvProvider
// signature (func(context.Context) map[string]string), so a command-executing
// code executor can be wired directly without an adapter:
//
//	exec = codeexecutor.NewEnvInjectingCodeExecutor(exec, identity.EnvVarsFromContext)
//
// After that wrapping, any tool that routes through the executor (skill_run,
// workspace_exec, interactive sessions, ...) automatically receives the
// current user's EnvVars in exec.Cmd.Env. Without the wrap, Identity.EnvVars
// is set on context by the Plugin but no one consumes it.
func EnvVarsFromContext(ctx context.Context) map[string]string {
	id, ok := FromContext(ctx)
	if !ok || id == nil || len(id.EnvVars) == 0 {
		return nil
	}
	return maps.Clone(id.EnvVars)
}
