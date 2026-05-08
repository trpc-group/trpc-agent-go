//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package identity propagates trusted user identity through agent tool calls.
//
// It bridges a caller-side identity (already known at the HTTP entry or from
// the session store) into every tool invocation — MCP HTTP requests, command
// executions, function tools — without requiring the model to supply the
// credentials or the user to go through an OAuth flow.
//
// # Quick start
//
// See the Plugin doc comment for a copy-paste setup that wires a Provider,
// a Runner plugin registration, an env-injecting code executor and an MCP
// before-request hook.
//
// # Integrating with a custom identity backend
//
// Most services already have an in-process way to turn a userID into
// credentials: a cache-backed user-info service, a signing helper, a secrets
// store, a JWT minter, or any combination of them. The recommended pattern
// is to consolidate those lookups inside a single Provider implementation
// and let the rest of the service consume the result through this package's
// context helpers.
//
// What a Provider should return:
//
//   - UserID: the stable authenticated identifier the rest of the service
//     already uses. Leave blank only for anonymous flows.
//   - Token: an opaque bearer token when downstream services expect
//     Authorization: Bearer <token>. Prefer setting Token once per
//     invocation; avoid per-request token minting inside the Provider,
//     which defeats caching.
//   - Signature: a business-level request signature when the scheme is
//     static for the whole invocation (e.g. an HMAC of UserID +
//     application). Schemes that depend on the target URL or wall-clock
//     time MUST be computed on the consumer side instead; see below.
//   - Headers: key/value pairs that should be set verbatim on outgoing
//     HTTP requests for HTTP-based tools. Keys are canonicalised by
//     http.Header.Set on the consumer side.
//   - EnvVars: key/value pairs that should be exported to any command
//     executed on the user's behalf (skill_run, workspace_exec, custom
//     bin-tools). Never model-visible.
//   - Extra: anything else your downstream consumers need but that does
//     not fit the categories above — signing-secret IDs, issuer strings,
//     feature flags, per-user workspace paths, etc.
//
// What belongs on the consumer side, NOT in the Provider:
//
//   - Signatures that depend on per-request context (target URL, current
//     unix time, request body). Ship the raw ingredients in Identity
//     (Token, Signature-seed, Extra["issuer"], ...) and compute the final
//     signature inside the HTTP before-request hook or the tool's Call
//     method, where the full request is available.
//   - Long-lived handles (database connections, http.Client) — keep those
//     in the Provider as struct fields and close them on shutdown;
//     Identity itself should be cheap to allocate.
//
// What the Plugin guarantees on top of Provider:
//
//   - Provider.Resolve is invoked at most once per invocation
//     (BeforeAgent). Sub-agents and AgentTool inherit the resolved
//     Identity through the invocation clone; BeforeAgent skips re-resolve
//     when an Identity is already present.
//   - Headers, EnvVars and Extra are attached to the per-tool context via
//     BeforeTool, so any tool implementation (including 3rd-party
//     toolsets) can read them with FromContext / HeadersFromContext /
//     EnvVarsFromContext regardless of whether the tool knows about this
//     plugin.
//
// # Typical mapping from an existing service
//
// If your service currently resolves identity inline inside each tool
// (e.g. tool A reads X-User-ID from ctx, tool B looks up per-user secrets
// from a KV store, tool C signs outbound HTTP with an HMAC helper),
// migrating usually looks like:
//
//  1. Move the "lookup once per user" parts of each site into a single
//     Provider.Resolve. Populate UserID, Token, Headers (the ones whose
//     value is known without seeing the outgoing request) and EnvVars.
//  2. Keep the "per-request computation" parts where they are — inside the
//     HTTP before-request hook or the command-building code — but switch
//     them to read from identity.FromContext(ctx) / HeadersFromContext /
//     EnvVarsFromContext instead of re-deriving from raw HTTP headers or
//     session state.
//  3. For command-executing tools, wrap the code executor once with
//     codeexecutor.NewEnvInjectingCodeExecutor(exec,
//     identity.EnvVarsFromContext). After that, each tool's own
//     buildEnv(userID) helper can usually be deleted.
//  4. For MCP HTTP toolsets, install a single WithHTTPBeforeRequest hook
//     that writes HeadersFromContext onto the outgoing request. Replaces
//     any per-toolset copy of the "set auth header" logic.
//
// # What this package intentionally does NOT do
//
//   - Capture arbitrary inbound HTTP headers and forward them verbatim.
//     That is a transport-level concern (proxying), not an identity
//     concern; keep the forwarded-header map as a separate context value
//     if your gateway needs it.
//   - Resolve identity from the context automatically. The Provider is
//     called with UserID/SessionID extracted from agent.Invocation.Session
//     at the top of BeforeAgent; if your userID lives elsewhere (custom
//     header, claim, cookie), put that extraction in the Runner-level
//     middleware that populates Session, or read ctx inside your
//     Provider.Resolve.
//   - Re-authenticate on every tool call. Providers are expected to be
//     idempotent per invocation. If you need per-tool-call freshness
//     (e.g. rotating short-lived tokens), implement that inside the
//     consumer hook (compute-on-use) rather than reissuing identity.
package identity
