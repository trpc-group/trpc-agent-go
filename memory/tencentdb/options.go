//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tencentdb integrates with TencentDB Agent Memory through its gateway
// API.
package tencentdb

import (
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultGatewayURL       = "http://127.0.0.1:8420"
	defaultTimeout          = 5 * time.Second
	defaultMaxBodyBytes     = 10 << 20
	defaultIngestWorkers    = 1
	defaultIngestQueueSize  = 10
	defaultIngestJobTimeout = 30 * time.Second
	defaultCompactionRatio  = 0.5
)

// SessionKeyFunc maps a framework session to the TencentDB Agent Memory
// session_key. The default avoids collisions across app/user/session IDs but
// does not provide strong multi-tenant isolation in a shared sidecar.
type SessionKeyFunc func(*session.Session) string

// ContextOffloadConfig configures the optional TencentDB Agent Memory context
// offload v2 integration. GatewayURL and APIKey reuse the Service settings when
// empty; ServiceID is required when Enabled is true.
type ContextOffloadConfig struct {
	// Enabled controls whether ContextOffloadPlugin and its companion tool are
	// active.
	Enabled bool

	// GatewayURL optionally overrides Service.GatewayURL for context offload
	// API calls. Empty reuses Service.GatewayURL.
	GatewayURL string

	// APIKey optionally overrides Service.APIKey for context offload calls.
	// Empty reuses Service.APIKey.
	APIKey string

	// ServiceID identifies the TencentDB Agent Memory service instance used by
	// the v2 offload API. It is required when Enabled is true.
	ServiceID string

	// CompactionRatio is the context-window utilization at which the plugin
	// asks the gateway to compact model messages. Zero selects the default 0.5;
	// non-zero values must be in (0, 2].
	CompactionRatio float64

	// TokenCounter estimates per-message tokens for the local CompactionRatio
	// trigger and for gateway request metadata. Nil uses
	// model.NewSimpleTokenCounter. If counting fails or returns a negative
	// value, the plugin retries token counting with the simple counter for that
	// model call.
	TokenCounter model.TokenCounter
}

// Options configures Service.
type Options struct {
	GatewayURL       string
	Timeout          time.Duration
	HTTPClient       *http.Client
	MaxBodyBytes     int64
	IngestWorkers    int
	IngestQueueSize  int
	IngestJobTimeout time.Duration

	// APIKey is sent as an "Authorization: Bearer <key>" header on gateway
	// requests. It is required when the gateway is started with
	// TDAI_GATEWAY_API_KEY.
	APIKey string

	SessionKeyFunc SessionKeyFunc

	RecallEnabled                bool
	EnableMemorySearchTool       bool
	EnableConversationSearchTool bool
	EnableStandardAliases        bool
	ToolPrefix                   string

	ContextOffload ContextOffloadConfig

	identity *serviceIdentity
}

// Option configures Service.
type Option func(*Options)

// defaultOptions returns conservative defaults. Cross-session/user reads are
// opt-in so existing legacy gateways do not begin reading from a shared store
// unexpectedly. Identity-scoped gateways enforce service/team/agent/user
// isolation but retain the same opt-in defaults for compatibility.
func defaultOptions() Options {
	return Options{
		GatewayURL:                   defaultGatewayURL,
		Timeout:                      defaultTimeout,
		MaxBodyBytes:                 defaultMaxBodyBytes,
		IngestWorkers:                defaultIngestWorkers,
		IngestQueueSize:              defaultIngestQueueSize,
		IngestJobTimeout:             defaultIngestJobTimeout,
		SessionKeyFunc:               defaultSessionKey,
		RecallEnabled:                false,
		EnableMemorySearchTool:       false,
		EnableConversationSearchTool: true,
		ToolPrefix:                   "tdai",
		ContextOffload:               defaultContextOffloadConfig(),
	}
}

func defaultContextOffloadConfig() ContextOffloadConfig {
	return ContextOffloadConfig{
		CompactionRatio: defaultCompactionRatio,
	}
}

func normalizeContextOffloadConfig(cfg ContextOffloadConfig) ContextOffloadConfig {
	cfg.GatewayURL = strings.TrimRight(strings.TrimSpace(cfg.GatewayURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.ServiceID = strings.TrimSpace(cfg.ServiceID)
	if cfg.CompactionRatio == 0 {
		cfg.CompactionRatio = defaultCompactionRatio
	}
	return cfg
}

// WithGatewayURL sets the TencentDB Agent Memory gateway URL.
func WithGatewayURL(url string) Option {
	return func(o *Options) {
		if url != "" {
			o.GatewayURL = url
		}
	}
}

// WithTimeout sets the request timeout used by the gateway client.
func WithTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.Timeout = timeout
		}
	}
}

// WithHTTPClient injects a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(o *Options) {
		if client != nil {
			o.HTTPClient = client
		}
	}
}

// WithMaxBodyBytes limits gateway response bodies.
func WithMaxBodyBytes(max int64) Option {
	return func(o *Options) {
		if max > 0 {
			o.MaxBodyBytes = max
		}
	}
}

// WithAPIKey sets the gateway API key. When the gateway is started with
// TDAI_GATEWAY_API_KEY it requires an "Authorization: Bearer <key>" header on
// every non-health route, so capture, recall, search, and session-end requests
// return 401 without it even though the health check still passes.
func WithAPIKey(key string) Option {
	return func(o *Options) {
		if k := strings.TrimSpace(key); k != "" {
			o.APIKey = k
		}
	}
}

// WithServiceIdentity configures the service, team, and agent identity used by
// TencentDB Agent Memory's V3 API. All three IDs are required. Use WithAPIKey
// when the gateway requires Bearer authentication; self-hosted gateways that
// keep authentication disabled can omit it. Omitting this option preserves the
// legacy gateway API.
// User and session IDs come from the framework session. The optional TencentDB
// task ID is not sent by this adapter.
// The same option applies to cloud and self-hosted deployments; use
// WithGatewayURL and WithAPIKey to configure the deployment endpoint.
func WithServiceIdentity(serviceID, teamID, agentID string) Option {
	return func(o *Options) {
		o.identity = &serviceIdentity{
			serviceID: strings.TrimSpace(serviceID),
			teamID:    strings.TrimSpace(teamID),
			agentID:   strings.TrimSpace(agentID),
		}
	}
}

// WithIngestWorkers sets the number of async capture workers.
func WithIngestWorkers(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.IngestWorkers = n
		}
	}
}

// WithIngestQueueSize sets the per-worker capture queue size.
func WithIngestQueueSize(size int) Option {
	return func(o *Options) {
		if size > 0 {
			o.IngestQueueSize = size
		}
	}
}

// WithIngestJobTimeout sets the timeout applied to queued capture jobs.
func WithIngestJobTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.IngestJobTimeout = timeout
		}
	}
}

// WithSessionKeyFunc overrides the Legacy session_key mapping and the local
// key used to serialize capture jobs. V3 still sends Session.ID as session_id;
// this option does not add an application isolation field to the V3 protocol.
func WithSessionKeyFunc(fn SessionKeyFunc) Option {
	return func(o *Options) {
		if fn != nil {
			o.SessionKeyFunc = fn
		}
	}
}

// WithRecallEnabled controls whether Plugin performs automatic recall.
//
// It is off by default. Legacy gateways may recall from a shared long-term
// store without enforcing the request's user/session scope. The
// identity-scoped data plane selected by WithServiceIdentity scopes L0/L1 by
// service/team/agent/user and L2/L3 by service/team/agent. Recall remains
// opt-in to preserve existing defaults.
func WithRecallEnabled(enabled bool) Option {
	return func(o *Options) {
		o.RecallEnabled = enabled
	}
}

// WithMemorySearchTool controls whether the long-term memory_search tool
// (tdai_memory_search, plus the standard alias when enabled) is exposed.
//
// It is off by default to preserve existing behavior. Legacy gateway memory
// search can read a shared long-term store without user/session scoping; the V3
// data plane scopes L1 search by service/team/agent/user. The session-scoped
// conversation search tool stays available.
func WithMemorySearchTool(enabled bool) Option {
	return func(o *Options) {
		o.EnableMemorySearchTool = enabled
	}
}

// WithConversationSearchTool controls whether tdai_conversation_search is exposed.
func WithConversationSearchTool(enabled bool) Option {
	return func(o *Options) {
		o.EnableConversationSearchTool = enabled
	}
}

// WithStandardAliases exposes memory_search as an additional alias.
//
// This can conflict with the framework's built-in memory tools, so it is off by
// default. TencentDB-native tdai_* names are always preferred. The alias is
// only exposed when the memory search tool is enabled via WithMemorySearchTool.
func WithStandardAliases(enabled bool) Option {
	return func(o *Options) {
		o.EnableStandardAliases = enabled
	}
}

// WithToolPrefix changes the native tool name prefix. The default prefix "tdai"
// yields names such as tdai_memory_search and tdai_conversation_search.
func WithToolPrefix(prefix string) Option {
	return func(o *Options) {
		if prefix != "" {
			o.ToolPrefix = prefix
		}
	}
}

// WithContextOffload configures the explicit TencentDB Agent Memory context
// offload v2 integration and its result-reference reader tool. It is disabled
// by default. When enabling it, configure ContextOffloadConfig.ServiceID and
// an API key before registering ContextOffloadPlugin.
func WithContextOffload(cfg ContextOffloadConfig) Option {
	return func(o *Options) {
		o.ContextOffload = normalizeContextOffloadConfig(cfg)
	}
}
