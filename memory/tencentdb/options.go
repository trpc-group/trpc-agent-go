//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tencentdb integrates with TencentDB Agent Memory through its
// local gateway sidecar.
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
)

const (
	// ContextOffloadModeLocal is retained for source compatibility with the
	// unreleased context offload API. The Go adapter now delegates all offload
	// processing to the TencentDB Agent Memory gateway regardless of mode.
	//
	// Deprecated: configure the TencentDB Agent Memory gateway instead.
	ContextOffloadModeLocal = "local"
	// ContextOffloadModeBackend is retained for source compatibility with the
	// unreleased context offload API.
	//
	// Deprecated: use ContextOffloadConfig.GatewayURL and APIKey.
	ContextOffloadModeBackend = "backend"
	// ContextOffloadModeCollect is retained for source compatibility with the
	// unreleased context offload API.
	//
	// Deprecated: collection behavior is owned by the TencentDB Agent Memory
	// gateway.
	ContextOffloadModeCollect = "collect"
)

// SessionKeyFunc maps a framework session to the TencentDB Agent Memory
// session_key. The default avoids collisions across app/user/session IDs but
// does not provide strong multi-tenant isolation in a shared sidecar.
type SessionKeyFunc func(*session.Session) string

// ContextOffloadConfig configures the optional TencentDB Agent Memory context
// offload gateway integration. Zero values reuse the Service gateway settings.
type ContextOffloadConfig struct {
	// Enabled controls whether ContextOffloadPlugin and companion tools are
	// active.
	Enabled bool

	// GatewayURL optionally overrides Service.GatewayURL for context offload
	// hook and drill-down tool calls. Empty reuses Service.GatewayURL.
	GatewayURL string

	// APIKey optionally overrides Service.APIKey for context offload calls.
	// Empty reuses Service.APIKey.
	APIKey string

	// DataDir is retained for source compatibility with the unreleased local
	// offload API.
	//
	// Deprecated: storage is owned by the TencentDB Agent Memory gateway and
	// this field is ignored.
	DataDir string
	// Mode is retained for source compatibility with the unreleased local
	// offload API.
	//
	// Deprecated: all modes delegate to the TencentDB Agent Memory gateway.
	Mode string
	// Model is retained for source compatibility with the unreleased local
	// offload API.
	//
	// Deprecated: offload model calls are owned by the TencentDB Agent Memory
	// gateway and this field is ignored.
	Model model.Model
	// MaxEntries is retained for source compatibility with the unreleased local
	// offload API.
	//
	// Deprecated: context assembly is owned by the TencentDB Agent Memory
	// gateway and this field is ignored.
	MaxEntries int
	// Backend is retained for source compatibility with the unreleased backend
	// offload API. URL and APIKey are treated as fallbacks for GatewayURL and
	// APIKey when those new fields are empty.
	//
	// Deprecated: use GatewayURL and APIKey directly.
	Backend ContextOffloadBackendConfig
	// L0 is retained for source compatibility with the unreleased local offload
	// API.
	//
	// Deprecated: L0 policy is owned by the TencentDB Agent Memory gateway and
	// this field is ignored.
	L0 ContextOffloadL0Config
	// L1 is retained for source compatibility with the unreleased local offload
	// API.
	//
	// Deprecated: L1 policy is owned by the TencentDB Agent Memory gateway and
	// this field is ignored.
	L1 ContextOffloadL1Config
	// L2 is retained for source compatibility with the unreleased local offload
	// API.
	//
	// Deprecated: L2 policy is owned by the TencentDB Agent Memory gateway and
	// this field is ignored.
	L2 ContextOffloadL2Config
	// L3 is retained for source compatibility with the unreleased local offload
	// API.
	//
	// Deprecated: L3 policy is owned by the TencentDB Agent Memory gateway and
	// this field is ignored.
	L3 ContextOffloadL3Config
}

// ContextOffloadBackendConfig is retained for source compatibility with the
// unreleased backend offload API.
//
// Deprecated: use ContextOffloadConfig.GatewayURL and APIKey.
type ContextOffloadBackendConfig struct {
	URL    string
	APIKey string
}

// ContextOffloadL0Config is retained for source compatibility with the
// unreleased local offload API.
//
// Deprecated: L0 policy is owned by the TencentDB Agent Memory gateway.
type ContextOffloadL0Config struct {
	MinToolResultBytes int
	MaxRefBytes        int64
}

// ContextOffloadL1Config is retained for source compatibility with the
// unreleased local offload API.
//
// Deprecated: L1 policy is owned by the TencentDB Agent Memory gateway.
type ContextOffloadL1Config struct {
	MaxPairsPerBatch int
}

// ContextOffloadL2Config is retained for source compatibility with the
// unreleased local offload API.
//
// Deprecated: L2 policy is owned by the TencentDB Agent Memory gateway.
type ContextOffloadL2Config struct {
	NullThreshold int
	Timeout       time.Duration
}

// ContextOffloadL3Config is retained for source compatibility with the
// unreleased local offload API.
//
// Deprecated: L3 policy is owned by the TencentDB Agent Memory gateway.
type ContextOffloadL3Config struct {
	ContextWindow        int
	MildRatio            float64
	AggressiveRatio      float64
	EmergencyRatio       float64
	EmergencyTargetRatio float64
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
}

// Option configures Service.
type Option func(*Options)

// defaultOptions returns conservative defaults. Cross-session/user reads are
// opt-in: automatic recall and the long-term memory_search tool stay disabled
// because the shared gateway sidecar does not currently enforce user/session
// scoping on those paths. Only session-scoped surfaces (capture and
// conversation search) are enabled by default.
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
	return ContextOffloadConfig{}
}

func normalizeContextOffloadConfig(cfg ContextOffloadConfig) ContextOffloadConfig {
	cfg.GatewayURL = strings.TrimRight(strings.TrimSpace(cfg.GatewayURL), "/")
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	hasExplicitGatewayURL := cfg.GatewayURL != ""
	cfg.Backend.URL = strings.TrimRight(strings.TrimSpace(cfg.Backend.URL), "/")
	cfg.Backend.APIKey = strings.TrimSpace(cfg.Backend.APIKey)
	if !hasExplicitGatewayURL {
		cfg.GatewayURL = cfg.Backend.URL
	}
	if cfg.APIKey == "" && !hasExplicitGatewayURL {
		cfg.APIKey = cfg.Backend.APIKey
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

// WithSessionKeyFunc overrides the session_key mapping.
func WithSessionKeyFunc(fn SessionKeyFunc) Option {
	return func(o *Options) {
		if fn != nil {
			o.SessionKeyFunc = fn
		}
	}
}

// WithRecallEnabled controls whether Plugin performs automatic recall.
//
// It is off by default: the gateway currently recalls from a shared long-term
// store without enforcing the request's user/session scope, so on a shared
// sidecar automatic recall can surface another user's or session's memories.
// Only enable it when the gateway guarantees per-tenant isolation.
func WithRecallEnabled(enabled bool) Option {
	return func(o *Options) {
		o.RecallEnabled = enabled
	}
}

// WithMemorySearchTool controls whether the long-term memory_search tool
// (tdai_memory_search, plus the standard alias when enabled) is exposed.
//
// It is off by default for the same reason as recall: the gateway memory search
// does not enforce user/session scoping, so the tool can read a shared
// long-term store. Only enable it when the gateway guarantees per-tenant
// isolation. The session-scoped conversation search tool stays available.
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
// yields tdai_memory_search and tdai_conversation_search.
func WithToolPrefix(prefix string) Option {
	return func(o *Options) {
		if prefix != "" {
			o.ToolPrefix = prefix
		}
	}
}

// WithContextOffload configures the explicit TencentDB Agent Memory context
// offload gateway integration and companion drill-down tools. It is disabled
// by default; set ContextOffloadConfig.Enabled to true before registering
// ContextOffloadPlugin.
func WithContextOffload(cfg ContextOffloadConfig) Option {
	return func(o *Options) {
		o.ContextOffload = normalizeContextOffloadConfig(cfg)
	}
}
