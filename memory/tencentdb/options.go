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

	defaultContextOffloadDataDir            = ".tdai-offload"
	defaultContextOffloadMinToolResultBytes = 8 << 10
	defaultContextOffloadMaxRefBytes        = 1 << 20
	defaultContextOffloadMaxEntries         = 20
	defaultContextOffloadMaxPairsPerBatch   = 20
	defaultContextOffloadL2NullThreshold    = 4
	defaultContextOffloadL2Timeout          = 5 * time.Minute
	defaultContextOffloadContextWindow      = 200000
	defaultContextOffloadMildRatio          = 0.50
	defaultContextOffloadAggressiveRatio    = 0.85
	defaultContextOffloadEmergencyRatio     = 0.95
	defaultContextOffloadEmergencyTarget    = 0.60
)

const (
	// ContextOffloadModeLocal runs L1/L1.5/L2 through the configured local
	// model, falling back to deterministic summaries when no model is set.
	ContextOffloadModeLocal = "local"
	// ContextOffloadModeBackend sends L1/L1.5/L2 to a TencentDB Agent Memory
	// offload backend.
	ContextOffloadModeBackend = "backend"
	// ContextOffloadModeCollect records refs and JSONL state without rewriting
	// model-facing messages.
	ContextOffloadModeCollect = "collect"
)

// SessionKeyFunc maps a framework session to the TencentDB Agent Memory
// session_key. The default avoids collisions across app/user/session IDs but
// does not provide strong multi-tenant isolation in a shared sidecar.
type SessionKeyFunc func(*session.Session) string

// ContextOffloadConfig configures the optional short-term context offload
// plugin. Zero values are filled from conservative defaults.
type ContextOffloadConfig struct {
	// Enabled controls whether ContextOffloadPlugin and its tools are active.
	Enabled bool
	// DataDir stores refs, JSONL indexes, Mermaid files, and state.
	DataDir string
	// Mode selects local, backend, or collect processing.
	Mode string
	// Model is used in local mode for L1/L1.5/L2 offload tasks.
	Model model.Model
	// MaxEntries limits recent entries rendered into injected task context.
	MaxEntries int
	// Backend configures backend-mode L1/L1.5/L2 calls.
	Backend ContextOffloadBackendConfig
	// L0 configures raw tool result externalization.
	L0 ContextOffloadL0Config
	// L1 configures tool pair summarization.
	L1 ContextOffloadL1Config
	// L2 configures Mermaid task map generation.
	L2 ContextOffloadL2Config
	// L3 configures token-pressure compression.
	L3 ContextOffloadL3Config
}

// ContextOffloadBackendConfig configures backend-mode offload calls.
type ContextOffloadBackendConfig struct {
	URL    string
	APIKey string
}

// ContextOffloadL0Config configures raw tool result externalization.
type ContextOffloadL0Config struct {
	MinToolResultBytes int
	MaxRefBytes        int64
}

// ContextOffloadL1Config configures tool pair summarization.
type ContextOffloadL1Config struct {
	MaxPairsPerBatch int
}

// ContextOffloadL2Config configures Mermaid task map generation.
type ContextOffloadL2Config struct {
	NullThreshold int
	Timeout       time.Duration
}

// ContextOffloadL3Config configures token-pressure compression.
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
	return ContextOffloadConfig{
		Mode:       ContextOffloadModeLocal,
		DataDir:    defaultContextOffloadDataDir,
		MaxEntries: defaultContextOffloadMaxEntries,
		L0: ContextOffloadL0Config{
			MinToolResultBytes: defaultContextOffloadMinToolResultBytes,
			MaxRefBytes:        defaultContextOffloadMaxRefBytes,
		},
		L1: ContextOffloadL1Config{
			MaxPairsPerBatch: defaultContextOffloadMaxPairsPerBatch,
		},
		L2: ContextOffloadL2Config{
			NullThreshold: defaultContextOffloadL2NullThreshold,
			Timeout:       defaultContextOffloadL2Timeout,
		},
		L3: ContextOffloadL3Config{
			ContextWindow:        defaultContextOffloadContextWindow,
			MildRatio:            defaultContextOffloadMildRatio,
			AggressiveRatio:      defaultContextOffloadAggressiveRatio,
			EmergencyRatio:       defaultContextOffloadEmergencyRatio,
			EmergencyTargetRatio: defaultContextOffloadEmergencyTarget,
		},
	}
}

func normalizeContextOffloadConfig(cfg ContextOffloadConfig) ContextOffloadConfig {
	def := defaultContextOffloadConfig()
	cfg.DataDir = strings.TrimSpace(cfg.DataDir)
	if cfg.DataDir == "" {
		cfg.DataDir = def.DataDir
	}
	switch strings.TrimSpace(cfg.Mode) {
	case ContextOffloadModeLocal, ContextOffloadModeBackend, ContextOffloadModeCollect:
		cfg.Mode = strings.TrimSpace(cfg.Mode)
	default:
		cfg.Mode = def.Mode
	}
	cfg.Backend.URL = strings.TrimRight(strings.TrimSpace(cfg.Backend.URL), "/")
	cfg.Backend.APIKey = strings.TrimSpace(cfg.Backend.APIKey)
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = def.MaxEntries
	}
	if cfg.L0.MinToolResultBytes <= 0 {
		cfg.L0.MinToolResultBytes = def.L0.MinToolResultBytes
	}
	if cfg.L0.MaxRefBytes <= 0 {
		cfg.L0.MaxRefBytes = def.L0.MaxRefBytes
	}
	if cfg.L1.MaxPairsPerBatch <= 0 {
		cfg.L1.MaxPairsPerBatch = def.L1.MaxPairsPerBatch
	}
	if cfg.L2.NullThreshold <= 0 {
		cfg.L2.NullThreshold = def.L2.NullThreshold
	}
	if cfg.L2.Timeout <= 0 {
		cfg.L2.Timeout = def.L2.Timeout
	}
	if cfg.L3.ContextWindow <= 0 {
		cfg.L3.ContextWindow = def.L3.ContextWindow
	}
	if cfg.L3.MildRatio <= 0 || cfg.L3.MildRatio >= 1 {
		cfg.L3.MildRatio = def.L3.MildRatio
	}
	if cfg.L3.AggressiveRatio <= 0 || cfg.L3.AggressiveRatio >= 1 {
		cfg.L3.AggressiveRatio = def.L3.AggressiveRatio
	}
	if cfg.L3.EmergencyRatio <= 0 || cfg.L3.EmergencyRatio >= 1 {
		cfg.L3.EmergencyRatio = def.L3.EmergencyRatio
	}
	if cfg.L3.EmergencyTargetRatio <= 0 || cfg.L3.EmergencyTargetRatio >= 1 {
		cfg.L3.EmergencyTargetRatio = def.L3.EmergencyTargetRatio
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

// WithContextOffload configures the explicit context offload plugin and its
// read-ref tool. It is disabled by default; set ContextOffloadConfig.Enabled
// to true before registering ContextOffloadPlugin.
func WithContextOffload(cfg ContextOffloadConfig) Option {
	return func(o *Options) {
		o.ContextOffload = normalizeContextOffloadConfig(cfg)
	}
}
