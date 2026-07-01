//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	"trpc.group/trpc-go/trpc-agent-go/model"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	openClawConfigEnvName = "OPENCLAW_CONFIG"

	defaultConfigRootDir = ".trpc-agent-go-github"
	defaultConfigAppDir  = "openclaw"
	defaultConfigFile    = "openclaw.yaml"
	defaultAdminAddr     = "127.0.0.1:19789"
	defaultAdminAutoPort = true

	sessionBackendInMemory   = "inmemory"
	sessionBackendRedis      = "redis"
	sessionBackendSQLite     = "sqlite"
	sessionBackendMySQL      = "mysql"
	sessionBackendPostgres   = "postgres"
	sessionBackendClickHouse = "clickhouse"

	memoryBackendFile      = "file"
	memoryBackendInMemory  = "inmemory"
	memoryBackendRedis     = "redis"
	memoryBackendSQLite    = "sqlite"
	memoryBackendSQLiteVec = "sqlitevec"
	memoryBackendMySQL     = "mysql"
	memoryBackendPostgres  = "postgres"
	memoryBackendPGVector  = "pgvector"

	codeExecutorTypeSandbox = "sandbox"

	sandboxBackendAuto            = "auto"
	sandboxBackendLinuxBubblewrap = "linux-bubblewrap"

	sandboxProfileWorkspaceWrite = "workspace_write"
	sandboxProfileReadOnly       = "read_only"
	sandboxProfileDisabled       = "disabled"

	sandboxNetworkRestricted = "restricted"
	sandboxNetworkEnabled    = "enabled"

	sandboxShellEnvInheritAll  = "all"
	sandboxShellEnvInheritCore = "core"
	sandboxShellEnvInheritNone = "none"

	defaultSandboxCodeExecutorTimeout        = 30 * time.Second
	defaultSandboxCodeExecutorOutputMaxBytes = 1 << 20

	summaryPolicyAny = "any"
	summaryPolicyAll = "all"

	summaryModeAuto   = "auto"
	summaryModeManual = "manual"

	defaultSessionSummaryEventThreshold = 20
	defaultSkillsLoadMode               = "turn"
	defaultSkillsToolProfile            = skillprofile.KnowledgeOnly
	defaultSkillsWatchDebounce          = 250 * time.Millisecond

	flagAddSessionSummary                             = "add-session-summary"
	flagEnableContextCompaction                       = "enable-context-compaction"
	flagContextCompactionOversizedToolResultMaxTokens = "context-compaction-oversized-tool-result-max-tokens"
	flagMaxHistoryRuns                                = "max-history-runs"
	flagMaxLLMCalls                                   = "max-llm-calls"
	flagMaxToolIterations                             = "max-tool-iterations"
	flagPreloadMemory                                 = "preload-memory"

	flagAgentInstruction       = "agent-instruction"
	flagAgentInstructionFiles  = "agent-instruction-files"
	flagAgentInstructionDir    = "agent-instruction-dir"
	flagAgentSystemPrompt      = "agent-system-prompt"
	flagAgentSystemPromptFiles = "agent-system-prompt-files"
	flagAgentSystemPromptDir   = "agent-system-prompt-dir"

	flagAgentRalphLoopEnabled           = "agent-ralph-loop"
	flagAgentRalphLoopMaxIterations     = "agent-ralph-max-iterations"
	flagAgentRalphLoopCompletionPromise = "agent-ralph-completion-promise"
	flagAgentRalphLoopPromiseTagOpen    = "agent-ralph-promise-tag-open"
	flagAgentRalphLoopPromiseTagClose   = "agent-ralph-promise-tag-close"
	flagAgentRalphLoopVerifyCommand     = "agent-ralph-verify-command"
	flagAgentRalphLoopVerifyWorkDir     = "agent-ralph-verify-workdir"
	flagAgentRalphLoopVerifyTimeout     = "agent-ralph-verify-timeout"
	flagAgentRalphLoopVerifyEnv         = "agent-ralph-verify-env"

	flagEnableParallelTools = "enable-parallel-tools"

	flagSkillsAllowBundled    = "skills-allow-bundled"
	flagSkillsWatch           = "skills-watch"
	flagSkillsWatchBundled    = "skills-watch-bundled"
	flagSkillsWatchDebounce   = "skills-watch-debounce"
	flagSkillsSummaryCacheTTL = "skills-summary-cache-ttl"
	flagSkillsOverviewLimit   = "skills-overview-limit"
	flagSkillsOverviewPinned  = "skills-overview-pinned"
	flagSkillsToolProfile     = "skills-tool-profile"
	flagSkillsLoadMode        = "skills-load-mode"
	flagSkillsMaxLoaded       = "skills-max-loaded"
	flagSkillsToolResults     = "skills-loaded-content-in-tool-results"
	flagSkillsSkipFallback    = "skills-skip-fallback-on-session-summary"

	flagDebugRecorder     = "debug-recorder"
	flagDebugRecorderDir  = "debug-recorder-dir"
	flagDebugRecorderMode = "debug-recorder-mode"

	flagLatencyDiagnostics                 = "latency-diagnostics"
	flagLatencyDiagnosticsEvents           = "latency-diagnostics-events"
	flagDeferToolSurface                   = "defer-tools-to-dynamic-agent"
	flagDeferToolSurfaceMode               = "defer-tools-to-dynamic-agent-mode"
	flagDeferToolSurfaceChars              = "defer-tools-to-dynamic-agent-threshold-chars"
	flagDeferToolSurfaceDefaultDirectTools = "defer-tools-to-dynamic-agent-default-direct-tools"
	flagDeferToolSurfaceDirect             = "defer-tools-to-dynamic-agent-direct-tools"
	flagDynamicAgentTimeout                = "dynamic-agent-timeout"
	flagHostExecDefaultTimeout             = "host-exec-default-timeout"

	flagAdminEnabled  = "admin-enabled"
	flagAdminAddr     = "admin-addr"
	flagAdminAutoPort = "admin-auto-port"

	flagA2AEnabled        = "a2a"
	flagA2AHost           = "a2a-host"
	flagA2AUserIDHeader   = "a2a-user-id-header"
	flagA2AStreaming      = "a2a-streaming"
	flagA2AAdvertiseTools = "a2a-advertise-tools"
	flagA2AName           = "a2a-name"
	flagA2ADescription    = "a2a-description"
)

type runOptions struct {
	ConfigPath string

	AppName  string
	HTTPAddr string

	AdminEnabled  bool
	AdminAddr     string
	AdminAutoPort bool

	LangfuseEnabled                      bool
	LangfuseRequired                     bool
	LangfuseUIBaseURL                    string
	LangfuseTraceURLTemplate             string
	LangfuseObservationLeafValueMaxBytes *int
	LatencyDiagnosticsEnabled            bool
	LatencyDiagnosticsEvents             bool

	A2AEnabled        bool
	A2AHost           string
	A2AUserIDHeader   string
	A2AStreaming      bool
	A2AAdvertiseTools bool
	A2AName           string
	A2ADescription    string

	AddSessionSummary                             bool
	EnableContextCompaction                       bool
	ContextCompactionOversizedToolResultMaxTokens int
	MaxHistoryRuns                                int
	MaxLLMCalls                                   int
	MaxToolIterations                             int
	PreloadMemory                                 int
	PostToolPromptEnabled                         *bool

	AgentInstruction       string
	AgentInstructionFiles  string
	AgentInstructionDir    string
	AgentSystemPrompt      string
	AgentSystemPromptFiles string
	AgentSystemPromptDir   string

	AgentType string

	RalphLoopEnabled           bool
	RalphLoopMaxIterations     int
	RalphLoopCompletionPromise string
	RalphLoopPromiseTagOpen    string
	RalphLoopPromiseTagClose   string
	RalphLoopVerifyCommand     string
	RalphLoopVerifyWorkDir     string
	RalphLoopVerifyTimeout     time.Duration
	RalphLoopVerifyEnv         string

	ClaudeBin          string
	ClaudeOutputFormat string
	ClaudeExtraArgs    string
	ClaudeEnv          string
	ClaudeWorkDir      string

	ModelMode             string
	OpenAIModel           string
	OpenAIVariant         string
	OpenAIBaseURL         string
	OpenAIHeaders         map[string]string
	GenerationConfig      *model.GenerationConfig
	ModelConfig           *yaml.Node
	KnowledgesConfig      []knowledgeEntry
	SkillsRoot            string
	SkillsExtraDir        string
	SkillsDebug           bool
	SkillsAllowBundled    string
	SkillConfigs          map[string]ocskills.SkillConfig
	SkillsWatch           bool
	SkillsWatchBundled    bool
	SkillsWatchDebounce   time.Duration
	SkillsSummaryCacheTTL time.Duration
	SkillsOverviewLimit   int
	SkillsOverviewPinned  string
	SkillsToolProfile     string
	SkillsLoadMode        string
	SkillsMaxLoaded       int
	SkillsToolResults     bool
	SkillsSkipFallback    bool
	SkillsToolingGuide    *string
	StateDir              string

	EvolutionEnabled        bool
	EvolutionHumanGate      string
	EvolutionSkillScopeMode skill.SkillScopeMode

	DebugRecorderEnabled bool
	DebugRecorderDir     string
	DebugRecorderMode    string

	AllowUsers      string
	RequireMention  bool
	Mention         string
	RuntimeProfiles *runtimeprofile.Config

	Channels []pluginSpec

	SessionBackend       string
	SessionRedisURL      string
	SessionRedisInstance string
	SessionRedisKeyPref  string
	SessionConfig        *yaml.Node

	MemoryBackend       string
	MemoryRedisURL      string
	MemoryRedisInstance string
	MemoryRedisKeyPref  string
	MemoryLimit         int
	MemoryConfig        *yaml.Node

	MemoryAutoEnabled          bool
	MemoryAutoPolicy           string
	MemoryAutoMessageThreshold int
	MemoryAutoTimeInterval     time.Duration

	SessionSummaryEnabled             bool
	SessionSummaryMode                string
	SessionSummaryPolicy              string
	SessionSummaryEventCount          int
	SessionSummaryTokenCount          int
	SessionSummaryIdleThreshold       time.Duration
	SessionSummaryMaxWords            int
	SessionSummaryApproxRunesPerToken float64

	EnableLocalExec                    bool
	CodeExecutor                       codeExecutorOptions
	EnableOpenClawTools                bool
	OpenClawToolingGuide               *string
	EnableParallelTools                bool
	DeferToolSurface                   bool
	DeferToolSurfaceMode               string
	DeferToolSurfaceChars              int
	DeferToolSurfaceDefaultDirectTools bool
	DeferToolSurfaceDirect             string
	DynamicAgentTimeout                time.Duration
	HostExecDefaultTimeout             time.Duration

	enableOpenClawToolsExplicit  bool
	deferToolSurfaceModeExplicit bool

	ToolProviders []pluginSpec
	ToolSets      []pluginSpec

	RefreshToolSetsOnRun bool
}

func parseRunOptions(args []string) (runOptions, error) {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := runOptions{
		AppName:       appName,
		HTTPAddr:      defaultHTTPAddr,
		AdminEnabled:  true,
		AdminAddr:     defaultAdminAddr,
		AdminAutoPort: defaultAdminAutoPort,
		A2AStreaming:  true,

		AgentType: agentTypeLLM,

		ModelMode:     modeOpenAI,
		OpenAIModel:   defaultOpenAIModelName(),
		OpenAIVariant: defaultOpenAIVariant,

		SkillsWatch:         true,
		SkillsWatchDebounce: defaultSkillsWatchDebounce,
		SkillsToolProfile:   defaultSkillsToolProfile,
		SkillsLoadMode:      defaultSkillsLoadMode,
		SkillsToolResults:   true,
		SkillsSkipFallback:  true,

		EvolutionSkillScopeMode: skill.SkillScopeApp,

		SessionBackend: sessionBackendInMemory,
		MemoryBackend:  memoryBackendInMemory,

		SessionSummaryPolicy: summaryPolicyAny,

		MemoryAutoPolicy: summaryPolicyAny,

		DeferToolSurfaceMode:               deferToolSurfaceModeAuto,
		DeferToolSurfaceDefaultDirectTools: true,
	}

	fs.StringVar(
		&opts.ConfigPath,
		"config",
		"",
		"Path to YAML config file; can also be set via $"+openClawConfigEnvName,
	)
	fs.StringVar(
		&opts.AppName,
		"app-name",
		appName,
		"App name for session/memory isolation",
	)
	fs.StringVar(
		&opts.HTTPAddr,
		"http-addr",
		defaultHTTPAddr,
		"HTTP listen address for gateway endpoints",
	)
	fs.BoolVar(
		&opts.AdminEnabled,
		flagAdminEnabled,
		true,
		"Enable the local OpenClaw admin UI",
	)
	fs.StringVar(
		&opts.AdminAddr,
		flagAdminAddr,
		defaultAdminAddr,
		"HTTP listen address for the local OpenClaw admin UI",
	)
	fs.BoolVar(
		&opts.AdminAutoPort,
		flagAdminAutoPort,
		defaultAdminAutoPort,
		"Auto-pick a nearby free admin port when the preferred one is busy",
	)
	fs.BoolVar(
		&opts.A2AEnabled,
		flagA2AEnabled,
		false,
		"Enable the OpenClaw A2A surface",
	)
	fs.StringVar(
		&opts.A2AHost,
		flagA2AHost,
		"",
		"Public A2A base URL; must include a non-root path",
	)
	fs.StringVar(
		&opts.A2AUserIDHeader,
		flagA2AUserIDHeader,
		"",
		"HTTP header name used to read user IDs on the A2A surface",
	)
	fs.BoolVar(
		&opts.A2AStreaming,
		flagA2AStreaming,
		true,
		"Enable streaming responses on the A2A surface",
	)
	fs.BoolVar(
		&opts.A2AAdvertiseTools,
		flagA2AAdvertiseTools,
		false,
		"Publish individual tools in the OpenClaw A2A agent card",
	)
	fs.StringVar(
		&opts.A2AName,
		flagA2AName,
		"",
		"Override the advertised A2A agent name",
	)
	fs.StringVar(
		&opts.A2ADescription,
		flagA2ADescription,
		"",
		"Override the advertised A2A agent description",
	)
	fs.StringVar(
		&opts.AgentType,
		"agent-type",
		agentTypeLLM,
		"Agent type: llm|claude-code",
	)
	fs.BoolVar(
		&opts.AddSessionSummary,
		flagAddSessionSummary,
		false,
		"Prepend session summary to the model context (optional)",
	)
	fs.BoolVar(
		&opts.EnableContextCompaction,
		flagEnableContextCompaction,
		false,
		"Enable prompt-side context compaction to control context window growth",
	)
	fs.IntVar(
		&opts.ContextCompactionOversizedToolResultMaxTokens,
		flagContextCompactionOversizedToolResultMaxTokens,
		0,
		"Truncate oversized tool results with head+tail preservation when "+
			"context compaction is enabled (0=disable; recommended opt-in "+
			"value is 8192). Requires --enable-context-compaction.",
	)
	fs.IntVar(
		&opts.MaxHistoryRuns,
		flagMaxHistoryRuns,
		0,
		"Max history messages when add-session-summary=false (0=unlimited)",
	)
	fs.IntVar(
		&opts.MaxLLMCalls,
		flagMaxLLMCalls,
		0,
		"Max LLM calls per invocation (0=unlimited)",
	)
	fs.IntVar(
		&opts.MaxToolIterations,
		flagMaxToolIterations,
		0,
		"Max tool-call iterations per invocation (0=unlimited)",
	)
	fs.IntVar(
		&opts.PreloadMemory,
		flagPreloadMemory,
		0,
		"Preload memories into system prompt (0=off, -1=all, N>0=adaptive budget)",
	)
	fs.StringVar(
		&opts.AgentInstruction,
		flagAgentInstruction,
		"",
		"Agent instruction (system-level guidance)",
	)
	fs.StringVar(
		&opts.AgentInstructionFiles,
		flagAgentInstructionFiles,
		"",
		"Comma-separated files merged into agent instruction",
	)
	fs.StringVar(
		&opts.AgentInstructionDir,
		flagAgentInstructionDir,
		"",
		"Dir of .md files merged into agent instruction",
	)
	fs.StringVar(
		&opts.AgentSystemPrompt,
		flagAgentSystemPrompt,
		"",
		"Agent system prompt (prepended to instruction)",
	)
	fs.StringVar(
		&opts.AgentSystemPromptFiles,
		flagAgentSystemPromptFiles,
		"",
		"Comma-separated files merged into system prompt",
	)
	fs.StringVar(
		&opts.AgentSystemPromptDir,
		flagAgentSystemPromptDir,
		"",
		"Dir of .md files merged into system prompt",
	)
	fs.BoolVar(
		&opts.RalphLoopEnabled,
		flagAgentRalphLoopEnabled,
		false,
		"Enable Ralph Loop outer verification loop (unsafe)",
	)
	fs.IntVar(
		&opts.RalphLoopMaxIterations,
		flagAgentRalphLoopMaxIterations,
		0,
		"Ralph Loop max iterations (0 uses default)",
	)
	fs.StringVar(
		&opts.RalphLoopCompletionPromise,
		flagAgentRalphLoopCompletionPromise,
		"",
		"Ralph Loop completion promise (optional)",
	)
	fs.StringVar(
		&opts.RalphLoopPromiseTagOpen,
		flagAgentRalphLoopPromiseTagOpen,
		"",
		"Ralph Loop promise open tag (optional)",
	)
	fs.StringVar(
		&opts.RalphLoopPromiseTagClose,
		flagAgentRalphLoopPromiseTagClose,
		"",
		"Ralph Loop promise close tag (optional)",
	)
	fs.StringVar(
		&opts.RalphLoopVerifyCommand,
		flagAgentRalphLoopVerifyCommand,
		"",
		"Ralph Loop verify command (optional, host shell)",
	)
	fs.StringVar(
		&opts.RalphLoopVerifyWorkDir,
		flagAgentRalphLoopVerifyWorkDir,
		"",
		"Ralph Loop verify command working dir (optional)",
	)
	fs.DurationVar(
		&opts.RalphLoopVerifyTimeout,
		flagAgentRalphLoopVerifyTimeout,
		0,
		"Ralph Loop verify command timeout (0 disables)",
	)
	fs.StringVar(
		&opts.RalphLoopVerifyEnv,
		flagAgentRalphLoopVerifyEnv,
		"",
		"Ralph Loop verify env overrides (KEY=VALUE, comma-separated)",
	)
	fs.StringVar(
		&opts.ClaudeBin,
		"claude-bin",
		"",
		"Claude Code CLI executable (agent-type=claude-code)",
	)
	fs.StringVar(
		&opts.ClaudeOutputFormat,
		"claude-output-format",
		"",
		"Claude Code output format: json|stream-json",
	)
	fs.StringVar(
		&opts.ClaudeExtraArgs,
		"claude-extra-args",
		"",
		"Extra Claude args (comma-separated)",
	)
	fs.StringVar(
		&opts.ClaudeEnv,
		"claude-env",
		"",
		"Extra Claude env (comma-separated KEY=VALUE)",
	)
	fs.StringVar(
		&opts.ClaudeWorkDir,
		"claude-workdir",
		"",
		"Claude Code working dir (optional)",
	)
	fs.StringVar(
		&opts.ModelMode,
		"mode",
		modeOpenAI,
		"Model mode: mock or openai",
	)
	fs.StringVar(
		&opts.OpenAIModel,
		"model",
		defaultOpenAIModelName(),
		"OpenAI model name (mode=openai)",
	)
	fs.StringVar(
		&opts.OpenAIVariant,
		"openai-variant",
		defaultOpenAIVariant,
		"OpenAI variant: auto, openai, deepseek, qwen, hunyuan (auto uses configured base URL host)",
	)
	fs.StringVar(
		&opts.OpenAIBaseURL,
		"openai-base-url",
		"",
		"OpenAI base URL override (mode=openai, optional)",
	)
	fs.StringVar(
		&opts.AllowUsers,
		"allow-users",
		"",
		"Comma-separated allowlist; empty allows all",
	)
	fs.BoolVar(
		&opts.RequireMention,
		"require-mention",
		false,
		"Require mention in thread/group messages",
	)
	fs.StringVar(
		&opts.Mention,
		"mention",
		"",
		"Comma-separated mention patterns",
	)
	fs.StringVar(
		&opts.SkillsRoot,
		"skills-root",
		"",
		"Skills root directory (default: ./skills)",
	)
	fs.StringVar(
		&opts.SkillsExtraDir,
		"skills-extra-dirs",
		"",
		"Extra skills roots (comma-separated, lowest precedence)",
	)
	fs.BoolVar(
		&opts.SkillsDebug,
		"skills-debug",
		false,
		"Log skill gating decisions",
	)
	fs.StringVar(
		&opts.SkillsAllowBundled,
		flagSkillsAllowBundled,
		"",
		"Comma-separated allowlist of bundled skills",
	)
	fs.BoolVar(
		&opts.SkillsWatch,
		flagSkillsWatch,
		true,
		"Watch local skill roots and refresh automatically",
	)
	fs.DurationVar(
		&opts.SkillsWatchDebounce,
		flagSkillsWatchDebounce,
		defaultSkillsWatchDebounce,
		"Debounce for automatic skill refreshes",
	)
	fs.BoolVar(
		&opts.SkillsWatchBundled,
		flagSkillsWatchBundled,
		false,
		"Also watch bundled skills roots for local changes",
	)
	fs.DurationVar(
		&opts.SkillsSummaryCacheTTL,
		flagSkillsSummaryCacheTTL,
		0,
		"How long to reuse the skills summary cache before "+
			"checking for changes (0 uses the default)",
	)
	fs.IntVar(
		&opts.SkillsOverviewLimit,
		flagSkillsOverviewLimit,
		0,
		"Show at most N skills in the skills overview "+
			"(0 shows all)",
	)
	fs.StringVar(
		&opts.SkillsOverviewPinned,
		flagSkillsOverviewPinned,
		"",
		"Comma-separated skill names to show first when "+
			"skills-overview-limit is set",
	)
	fs.StringVar(
		&opts.SkillsToolProfile,
		flagSkillsToolProfile,
		defaultSkillsToolProfile,
		"Built-in skill tool profile: full|knowledge_only",
	)
	fs.StringVar(
		&opts.SkillsLoadMode,
		flagSkillsLoadMode,
		defaultSkillsLoadMode,
		"Skill context lifetime: once|turn|session",
	)
	fs.IntVar(
		&opts.SkillsMaxLoaded,
		flagSkillsMaxLoaded,
		0,
		"Keep at most N loaded skills (0 disables the cap)",
	)
	fs.BoolVar(
		&opts.SkillsToolResults,
		flagSkillsToolResults,
		true,
		"Materialize loaded skill content into tool results",
	)
	fs.BoolVar(
		&opts.SkillsSkipFallback,
		flagSkillsSkipFallback,
		true,
		"Skip skill fallback system message when session "+
			"summary exists",
	)
	fs.StringVar(
		&opts.StateDir,
		"state-dir",
		"",
		"State dir for offsets and managed skills",
	)
	fs.BoolVar(
		&opts.DebugRecorderEnabled,
		flagDebugRecorder,
		false,
		"Enable file-based debug recorder",
	)
	fs.StringVar(
		&opts.DebugRecorderDir,
		flagDebugRecorderDir,
		"",
		"Debug recorder output dir (default: <state_dir>/debug)",
	)
	fs.StringVar(
		&opts.DebugRecorderMode,
		flagDebugRecorderMode,
		"",
		"Debug recorder mode: full|safe (default: full)",
	)
	fs.BoolVar(
		&opts.LatencyDiagnosticsEnabled,
		flagLatencyDiagnostics,
		false,
		"Enable per-request latency diagnostic spans",
	)
	fs.BoolVar(
		&opts.LatencyDiagnosticsEvents,
		flagLatencyDiagnosticsEvents,
		false,
		"Emit latency diagnostic runner events",
	)
	fs.StringVar(
		&opts.SessionBackend,
		"session-backend",
		sessionBackendInMemory,
		"Session backend: inmemory|redis|sqlite|mysql|postgres|clickhouse",
	)
	fs.StringVar(
		&opts.SessionRedisURL,
		"session-redis-url",
		"",
		"Redis URL for session backend=redis",
	)
	fs.StringVar(
		&opts.SessionRedisInstance,
		"session-redis-instance",
		"",
		"Redis instance name for session backend=redis",
	)
	fs.StringVar(
		&opts.SessionRedisKeyPref,
		"session-redis-key-prefix",
		"",
		"Redis key prefix for session backend=redis",
	)
	fs.StringVar(
		&opts.MemoryBackend,
		"memory-backend",
		memoryBackendInMemory,
		"Memory backend: file|inmemory|redis|sqlite|"+
			"sqlitevec(requires openclaw_sqlitevec build tag)|"+
			"mysql|postgres|pgvector",
	)
	fs.StringVar(
		&opts.MemoryRedisURL,
		"memory-redis-url",
		"",
		"Redis URL for memory backend=redis",
	)
	fs.StringVar(
		&opts.MemoryRedisInstance,
		"memory-redis-instance",
		"",
		"Redis instance name for memory backend=redis",
	)
	fs.StringVar(
		&opts.MemoryRedisKeyPref,
		"memory-redis-key-prefix",
		"",
		"Redis key prefix for memory backend=redis",
	)
	fs.IntVar(
		&opts.MemoryLimit,
		"memory-limit",
		0,
		"Memory entries limit per user (optional)",
	)
	fs.BoolVar(
		&opts.MemoryAutoEnabled,
		"memory-auto",
		false,
		"Enable auto memory extraction (optional)",
	)
	fs.StringVar(
		&opts.MemoryAutoPolicy,
		"memory-auto-policy",
		summaryPolicyAny,
		"Auto memory gating policy: any|all",
	)
	fs.IntVar(
		&opts.MemoryAutoMessageThreshold,
		"memory-auto-messages",
		0,
		"Extract when messages exceed N (0 disables threshold check)",
	)
	fs.DurationVar(
		&opts.MemoryAutoTimeInterval,
		"memory-auto-interval",
		0,
		"Extract when time since last extract exceeds duration",
	)
	fs.BoolVar(
		&opts.SessionSummaryEnabled,
		"session-summary",
		false,
		"Enable session summarization (optional)",
	)
	fs.StringVar(
		&opts.SessionSummaryMode,
		"session-summary-mode",
		"",
		"Summary trigger mode: auto (context-window aware) or manual (explicit thresholds)",
	)
	fs.StringVar(
		&opts.SessionSummaryPolicy,
		"session-summary-policy",
		summaryPolicyAny,
		"Session summary gating policy: any|all (only used in manual mode)",
	)
	fs.IntVar(
		&opts.SessionSummaryEventCount,
		"session-summary-events",
		0,
		"Summarize when delta events exceed N (0 disables)",
	)
	fs.IntVar(
		&opts.SessionSummaryTokenCount,
		"session-summary-tokens",
		0,
		"Summarize when delta tokens exceed N (0 disables)",
	)
	fs.DurationVar(
		&opts.SessionSummaryIdleThreshold,
		"session-summary-idle",
		0,
		"Summarize on summary checks when the checked session's last event is older than duration (0 disables)",
	)
	fs.IntVar(
		&opts.SessionSummaryMaxWords,
		"session-summary-max-words",
		0,
		"Max summary words (0 means no limit)",
	)
	fs.Float64Var(
		&opts.SessionSummaryApproxRunesPerToken,
		"session-summary-approx-runes-per-token",
		0,
		"Approximate runes per token for summary token estimation "+
			"(0 uses framework default 4.0; set ~2.0 for Chinese-heavy content)",
	)
	fs.BoolVar(
		&opts.EnableLocalExec,
		"enable-local-exec",
		false,
		"Enable local code execution tool (unsafe)",
	)
	fs.BoolVar(
		&opts.EnableOpenClawTools,
		"enable-openclaw-tools",
		false,
		"Enable OpenClaw host tools (exec_command, message, "+
			"cron) (unsafe, enabled by default for llm agents)",
	)
	fs.BoolVar(
		&opts.EnableParallelTools,
		flagEnableParallelTools,
		false,
		"Enable parallel tool calls (not supported by claude-code)",
	)
	fs.BoolVar(
		&opts.RefreshToolSetsOnRun,
		"refresh-toolsets-on-run",
		false,
		"Refresh ToolSets tool list on each run (optional)",
	)
	fs.BoolVar(
		&opts.DeferToolSurface,
		flagDeferToolSurface,
		false,
		"Expose configured tools through dynamic_agent instead of "+
			"the main agent tool surface",
	)
	fs.StringVar(
		&opts.DeferToolSurfaceMode,
		flagDeferToolSurfaceMode,
		deferToolSurfaceModeAuto,
		"Deferred tool surface mode: off, on, auto",
	)
	fs.IntVar(
		&opts.DeferToolSurfaceChars,
		flagDeferToolSurfaceChars,
		0,
		"Auto-defer when direct tool declarations exceed this "+
			"many characters (0 uses default)",
	)
	fs.BoolVar(
		&opts.DeferToolSurfaceDefaultDirectTools,
		flagDeferToolSurfaceDefaultDirectTools,
		true,
		"Keep default direct tools on the parent agent when "+
			"deferred tool surface mode is active",
	)
	fs.StringVar(
		&opts.DeferToolSurfaceDirect,
		flagDeferToolSurfaceDirect,
		"",
		"Comma-separated additional tool names to keep directly on "+
			"the parent agent when deferred mode is active",
	)
	fs.DurationVar(
		&opts.DynamicAgentTimeout,
		flagDynamicAgentTimeout,
		0,
		"Maximum duration for one dynamic_agent child call (0 disables)",
	)
	fs.DurationVar(
		&opts.HostExecDefaultTimeout,
		flagHostExecDefaultTimeout,
		0,
		"Default timeout for OpenClaw host exec commands when timeout_sec "+
			"is omitted (0 keeps the built-in default)",
	)

	if err := fs.Parse(args); err != nil {
		return runOptions{}, &exitError{Code: 2, Err: err}
	}
	if len(fs.Args()) > 0 {
		return runOptions{}, &exitError{
			Code: 2,
			Err:  unexpectedArgsError(fs.Args()),
		}
	}

	setFlags := make(map[string]struct{})
	fs.Visit(func(f *flag.Flag) {
		setFlags[f.Name] = struct{}{}
	})
	opts.enableOpenClawToolsExplicit = flagWasSet(
		setFlags,
		"enable-openclaw-tools",
	)
	opts.deferToolSurfaceModeExplicit = flagWasSet(
		setFlags,
		flagDeferToolSurfaceMode,
	)
	if flagWasSet(setFlags, flagDeferToolSurface) &&
		!opts.DeferToolSurface &&
		!flagWasSet(setFlags, flagDeferToolSurfaceMode) {
		opts.DeferToolSurfaceMode = deferToolSurfaceModeOff
	}

	cfgPath := resolveConfigPath(opts.ConfigPath)
	if cfgPath == "" {
		if err := finalizeRunOptions(&opts); err != nil {
			return runOptions{}, &exitError{Code: 2, Err: err}
		}
		return opts, nil
	}
	opts.ConfigPath = cfgPath

	cfg, err := loadConfigFile(cfgPath)
	if err != nil {
		return runOptions{}, &exitError{
			Code: 1,
			Err:  fmt.Errorf("load config failed: %w", err),
		}
	}
	if cfg == nil {
		if err := finalizeRunOptions(&opts); err != nil {
			return runOptions{}, &exitError{Code: 2, Err: err}
		}
		return opts, nil
	}
	if err := cfg.apply(&opts, setFlags); err != nil {
		return runOptions{}, &exitError{
			Code: 1,
			Err:  fmt.Errorf("apply config failed: %w", err),
		}
	}

	if err := finalizeRunOptions(&opts); err != nil {
		return runOptions{}, &exitError{
			Code: skillsOptionExitCode(setFlags),
			Err:  err,
		}
	}

	return opts, nil
}

func unexpectedArgsError(args []string) error {
	if len(args) == 0 {
		return errors.New("unexpected args")
	}
	joined := strings.Join(args, " ")
	hint := ""
	switch args[0] {
	case subcmdPairing:
		hint = "did you mean: openclaw pairing ...?"
	case subcmdDoctor:
		hint = "did you mean: openclaw doctor ...?"
	}
	if hint == "" {
		return fmt.Errorf("unexpected args: %s", joined)
	}
	return fmt.Errorf("unexpected args: %s (%s)", joined, hint)
}

func resolveConfigPath(raw string) string {
	path := strings.TrimSpace(raw)
	if path != "" {
		return path
	}
	if v := strings.TrimSpace(os.Getenv(openClawConfigEnvName)); v != "" {
		return v
	}
	return defaultConfigPathIfExists()
}

func defaultConfigPathIfExists() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if strings.TrimSpace(home) == "" {
		return ""
	}
	cfgPath := filepath.Join(
		home,
		defaultConfigRootDir,
		defaultConfigAppDir,
		defaultConfigFile,
	)
	st, err := os.Stat(cfgPath)
	if err != nil || st == nil || st.IsDir() {
		return ""
	}
	return cfgPath
}

type fileConfig struct {
	AppName  *string `yaml:"app_name,omitempty"`
	StateDir *string `yaml:"state_dir,omitempty"`

	DebugRecorder *debugRecorderConfig `yaml:"debug_recorder,omitempty"`

	HTTP            *httpConfig            `yaml:"http,omitempty"`
	Admin           *adminConfig           `yaml:"admin,omitempty"`
	Observability   *observabilityConfig   `yaml:"observability,omitempty"`
	A2A             *a2aConfig             `yaml:"a2a,omitempty"`
	Agent           *agentRunConfig        `yaml:"agent,omitempty"`
	Model           *modelConfig           `yaml:"model,omitempty"`
	Knowledges      *knowledgesConfig      `yaml:"knowledges,omitempty"`
	Gateway         *gatewayConfig         `yaml:"gateway,omitempty"`
	RuntimeProfiles *runtimeprofile.Config `yaml:"runtime_profiles,omitempty"`
	Channels        []filePluginSpec       `yaml:"channels,omitempty"`
	Skills          *skillsConfig          `yaml:"skills,omitempty"`
	Tools           *toolsConfig           `yaml:"tools,omitempty"`

	Session *sessionConfig `yaml:"session,omitempty"`
	Memory  *memoryConfig  `yaml:"memory,omitempty"`

	Evolution *evolutionConfig `yaml:"evolution,omitempty"`
}

type httpConfig struct {
	Addr *string `yaml:"addr,omitempty"`
}

type adminConfig struct {
	Enabled  *bool   `yaml:"enabled,omitempty"`
	Addr     *string `yaml:"addr,omitempty"`
	AutoPort *bool   `yaml:"auto_port,omitempty"`
}

type observabilityConfig struct {
	Langfuse    *langfuseConfig           `yaml:"langfuse,omitempty"`
	LatencyDiag *latencyDiagnosticsConfig `yaml:"latency_diagnostics,omitempty"`
}

type langfuseConfig struct {
	Enabled                      *bool   `yaml:"enabled,omitempty"`
	Required                     *bool   `yaml:"required,omitempty"`
	UIBaseURL                    *string `yaml:"ui_base_url,omitempty"`
	TraceURLTemplate             *string `yaml:"trace_url_template,omitempty"`
	ObservationLeafValueMaxBytes *int    `yaml:"observation_leaf_value_max_bytes,omitempty"`
}

type latencyDiagnosticsConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
	Events  *bool `yaml:"events,omitempty"`
}

type a2aConfig struct {
	Enabled        *bool   `yaml:"enabled,omitempty"`
	Host           *string `yaml:"host,omitempty"`
	UserIDHeader   *string `yaml:"user_id_header,omitempty"`
	Streaming      *bool   `yaml:"streaming,omitempty"`
	AdvertiseTools *bool   `yaml:"advertise_tools,omitempty"`
	Name           *string `yaml:"name,omitempty"`
	Description    *string `yaml:"description,omitempty"`
}

type debugRecorderConfig struct {
	Enabled *bool   `yaml:"enabled,omitempty"`
	Dir     *string `yaml:"dir,omitempty"`
	Mode    *string `yaml:"mode,omitempty"`
}

type agentRunConfig struct {
	Type *string `yaml:"type,omitempty"`

	AddSessionSummary                             *bool `yaml:"add_session_summary,omitempty"`
	EnableContextCompaction                       *bool `yaml:"enable_context_compaction,omitempty"`
	ContextCompactionOversizedToolResultMaxTokens *int  `yaml:"context_compaction_oversized_tool_result_max_tokens,omitempty"`
	MaxHistoryRuns                                *int  `yaml:"max_history_runs,omitempty"`
	MaxLLMCalls                                   *int  `yaml:"max_llm_calls,omitempty"`
	MaxToolIterations                             *int  `yaml:"max_tool_iterations,omitempty"`
	PreloadMemory                                 *int  `yaml:"preload_memory,omitempty"`
	DisablePostToolPrompt                         *bool `yaml:"disable_post_tool_prompt,omitempty"`
	DisablePostToolPromptCamel                    *bool `yaml:"disablePostToolPrompt,omitempty"`

	Instruction      *string  `yaml:"instruction,omitempty"`
	InstructionFiles []string `yaml:"instruction_files,omitempty"`
	InstructionDir   *string  `yaml:"instruction_dir,omitempty"`

	SystemPrompt      *string  `yaml:"system_prompt,omitempty"`
	SystemPromptFiles []string `yaml:"system_prompt_files,omitempty"`
	SystemPromptDir   *string  `yaml:"system_prompt_dir,omitempty"`

	RalphLoop *ralphLoopConfig `yaml:"ralph_loop,omitempty"`

	ClaudeBin          *string  `yaml:"claude_bin,omitempty"`
	ClaudeOutputFormat *string  `yaml:"claude_output_format,omitempty"`
	ClaudeExtraArgs    []string `yaml:"claude_extra_args,omitempty"`
	ClaudeEnv          []string `yaml:"claude_env,omitempty"`
	ClaudeWorkDir      *string  `yaml:"claude_work_dir,omitempty"`
}

type ralphLoopConfig struct {
	Enabled           *bool   `yaml:"enabled,omitempty"`
	MaxIterations     *int    `yaml:"max_iterations,omitempty"`
	CompletionPromise *string `yaml:"completion_promise,omitempty"`
	PromiseTagOpen    *string `yaml:"promise_tag_open,omitempty"`
	PromiseTagClose   *string `yaml:"promise_tag_close,omitempty"`

	Verify *ralphLoopVerifyConfig `yaml:"verify,omitempty"`
}

type ralphLoopVerifyConfig struct {
	Command *string  `yaml:"command,omitempty"`
	WorkDir *string  `yaml:"work_dir,omitempty"`
	Timeout *string  `yaml:"timeout,omitempty"`
	Env     []string `yaml:"env,omitempty"`
}

type modelConfig struct {
	Mode             *string               `yaml:"mode,omitempty"`
	Name             *string               `yaml:"name,omitempty"`
	BaseURL          *string               `yaml:"base_url,omitempty"`
	OpenAIVariant    *string               `yaml:"openai_variant,omitempty"`
	Headers          map[string]string     `yaml:"headers,omitempty"`
	GenerationConfig *generationConfigYAML `yaml:"generation_config,omitempty"`
	Config           *rawYAMLNode          `yaml:"config,omitempty"`
}

type generationConfigYAML struct {
	MaxTokens        *int     `yaml:"max_tokens,omitempty"`
	Temperature      *float64 `yaml:"temperature,omitempty"`
	TopP             *float64 `yaml:"top_p,omitempty"`
	Stream           *bool    `yaml:"stream,omitempty"`
	Stop             []string `yaml:"stop,omitempty"`
	PresencePenalty  *float64 `yaml:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `yaml:"frequency_penalty,omitempty"`
	ReasoningEffort  *string  `yaml:"reasoning_effort,omitempty"`
	ThinkingEnabled  *bool    `yaml:"thinking_enabled,omitempty"`
	ThinkingTokens   *int     `yaml:"thinking_tokens,omitempty"`
}

type gatewayConfig struct {
	AllowUsers      []string `yaml:"allow_users,omitempty"`
	RequireMention  *bool    `yaml:"require_mention,omitempty"`
	MentionPatterns []string `yaml:"mention_patterns,omitempty"`
}

type skillsConfig struct {
	Root      *string  `yaml:"root,omitempty"`
	ExtraDirs []string `yaml:"extra_dirs,omitempty"`
	Debug     *bool    `yaml:"debug,omitempty"`

	AllowBundled        []string `yaml:"allow_bundled,omitempty"`
	AllowBundledCamel   []string `yaml:"allowBundled,omitempty"`
	Watch               *bool    `yaml:"watch,omitempty"`
	WatchBundled        *bool    `yaml:"watch_bundled,omitempty"`
	WatchBundledCamel   *bool    `yaml:"watchBundled,omitempty"`
	WatchDebounceMS     *int     `yaml:"watch_debounce_ms,omitempty"`
	WatchDebounceCamel  *int     `yaml:"watchDebounceMs,omitempty"`
	SummaryCacheTTLMS   *int     `yaml:"summary_cache_ttl_ms,omitempty"`
	SummaryCacheCamel   *int     `yaml:"summaryCacheTtlMs,omitempty"`
	OverviewLimit       *int     `yaml:"overview_limit,omitempty"`
	OverviewLimitCamel  *int     `yaml:"overviewLimit,omitempty"`
	OverviewPinned      []string `yaml:"overview_pinned,omitempty"`
	OverviewPinnedCamel []string `yaml:"overviewPinned,omitempty"`
	ToolProfile         *string  `yaml:"tool_profile,omitempty"`
	ToolProfileCamel    *string  `yaml:"toolProfile,omitempty"`
	LoadMode            *string  `yaml:"load_mode,omitempty"`
	LoadModeCamel       *string  `yaml:"loadMode,omitempty"`
	MaxLoadedSkills     *int     `yaml:"max_loaded_skills,omitempty"`
	MaxLoadedCamel      *int     `yaml:"maxLoadedSkills,omitempty"`

	ToolResults          *bool   `yaml:"loaded_content_in_tool_results,omitempty"`
	ToolResultsCamel     *bool   `yaml:"loadedContentInToolResults,omitempty"`
	SkipSummaryFallback  *bool   `yaml:"skip_fallback_on_session_summary,omitempty"`
	SkipFallbackCamel    *bool   `yaml:"skipFallbackOnSessionSummary,omitempty"`
	ToolingGuidance      *string `yaml:"tooling_guidance,omitempty"`
	ToolingGuidanceCamel *string `yaml:"toolingGuidance,omitempty"`

	Entries map[string]skillEntryConfig `yaml:"entries,omitempty"`
}

type skillEntryConfig struct {
	Enabled     *bool             `yaml:"enabled,omitempty"`
	APIKey      string            `yaml:"api_key,omitempty"`
	APIKeyCamel string            `yaml:"apiKey,omitempty"`
	Env         map[string]string `yaml:"env,omitempty"`
}

type toolsConfig struct {
	EnableLocalExec               *bool               `yaml:"enable_local_exec,omitempty"`
	CodeExecutor                  *codeExecutorConfig `yaml:"code_executor,omitempty"`
	EnableOpenClawTools           *bool               `yaml:"enable_openclaw_tools,omitempty"`
	OpenClawToolingGuide          *string             `yaml:"openclaw_tooling_guidance,omitempty"`
	OpenClawToolingGuideCamel     *string             `yaml:"openClawToolingGuidance,omitempty"`
	EnableParallelTools           *bool               `yaml:"enable_parallel_tools,omitempty"`
	RefreshToolSetsOnRun          *bool               `yaml:"refresh_toolsets_on_run,omitempty"`
	DeferToDynamicAgent           *bool               `yaml:"defer_to_dynamic_agent,omitempty"`
	DeferToDynamicAgentCamel      *bool               `yaml:"deferToDynamicAgent,omitempty"`
	DeferToDynamicAgentMode       *string             `yaml:"defer_to_dynamic_agent_mode,omitempty"`
	DeferToDynamicAgentModeCamel  *string             `yaml:"deferToDynamicAgentMode,omitempty"`
	DeferToDynamicAgentChars      *int                `yaml:"defer_to_dynamic_agent_threshold_chars,omitempty"`
	DeferToDynamicAgentCharsCamel *int                `yaml:"deferToDynamicAgentThresholdChars,omitempty"`
	DeferDefaultDirectTools       *bool               `yaml:"defer_default_direct_tools,omitempty"`
	DeferDefaultDirectToolsCamel  *bool               `yaml:"deferDefaultDirectTools,omitempty"`
	DeferDirectTools              yamlStringList      `yaml:"defer_direct_tools,omitempty"`
	DeferDirectToolsCamel         yamlStringList      `yaml:"deferDirectTools,omitempty"`
	DynamicAgentTimeout           *string             `yaml:"dynamic_agent_timeout,omitempty"`
	DynamicAgentTimeoutCamel      *string             `yaml:"dynamicAgentTimeout,omitempty"`
	HostExecDefaultTimeout        *string             `yaml:"host_exec_default_timeout,omitempty"`
	HostExecDefaultTimeoutCamel   *string             `yaml:"hostExecDefaultTimeout,omitempty"`

	Providers []filePluginSpec `yaml:"providers,omitempty"`
	ToolSets  []filePluginSpec `yaml:"toolsets,omitempty"`
}

type codeExecutorConfig struct {
	Type                  string                     `yaml:"type,omitempty"`
	AutoExecuteCodeBlocks *bool                      `yaml:"auto_execute_code_blocks,omitempty"`
	Sandbox               *sandboxCodeExecutorConfig `yaml:"sandbox,omitempty"`
}

type sandboxCodeExecutorConfig struct {
	WorkspaceRoot  string                 `yaml:"workspace_root,omitempty"`
	Backend        string                 `yaml:"backend,omitempty"`
	Profile        string                 `yaml:"profile,omitempty"`
	Network        string                 `yaml:"network,omitempty"`
	DefaultTimeout string                 `yaml:"default_timeout,omitempty"`
	OutputMaxBytes *int                   `yaml:"output_max_bytes,omitempty"`
	ShellEnv       *sandboxShellEnvConfig `yaml:"shell_env,omitempty"`
}

type sandboxShellEnvConfig struct {
	Inherit              string            `yaml:"inherit,omitempty"`
	ApplyDefaultExcludes *bool             `yaml:"apply_default_excludes,omitempty"`
	Exclude              []string          `yaml:"exclude,omitempty"`
	IncludeOnly          []string          `yaml:"include_only,omitempty"`
	Set                  map[string]string `yaml:"set,omitempty"`
}

type codeExecutorOptions struct {
	Type                  string
	AutoExecuteCodeBlocks *bool
	Sandbox               sandboxCodeExecutorOptions
}

type sandboxCodeExecutorOptions struct {
	WorkspaceRoot  string
	Backend        string
	Profile        string
	Network        string
	DefaultTimeout time.Duration
	OutputMaxBytes int
	ShellEnv       sandboxShellEnvOptions
}

type sandboxShellEnvOptions struct {
	Inherit              string
	ApplyDefaultExcludes bool
	Exclude              []string
	IncludeOnly          []string
	Set                  map[string]string
}

type sessionConfig struct {
	Backend *string        `yaml:"backend,omitempty"`
	Redis   *redisConfig   `yaml:"redis,omitempty"`
	Summary *summaryConfig `yaml:"summary,omitempty"`
	Config  *rawYAMLNode   `yaml:"config,omitempty"`
}

type memoryConfig struct {
	Backend *string      `yaml:"backend,omitempty"`
	Redis   *redisConfig `yaml:"redis,omitempty"`
	Limit   *int         `yaml:"limit,omitempty"`
	Auto    *memoryAuto  `yaml:"auto,omitempty"`
	Config  *rawYAMLNode `yaml:"config,omitempty"`
}

type evolutionConfig struct {
	// Enabled explicitly opts the runtime into the async evolution service.
	Enabled *bool `yaml:"enabled,omitempty"`

	// HumanGate controls the human approval gate for skill revisions.
	// Values: "always" (hold all), "create" (hold new skills only), "" (disabled).
	HumanGate  *string                    `yaml:"human_gate,omitempty"`
	SkillScope *evolutionSkillScopeConfig `yaml:"skill_scope,omitempty"`
}

type evolutionSkillScopeConfig struct {
	Mode *string `yaml:"mode,omitempty"`
}

type knowledgesConfig struct {
	Providers []knowledgeProviderConfig `yaml:"providers,omitempty"`

	// Entries is the deprecated field name (pre-v0.0.4). Kept here so
	// that KnownFields(true) does not reject it with a confusing
	// "field entries not found" error; instead we return a clear
	// migration message in fileConfig.apply.
	Entries []rawYAMLNode `yaml:"entries,omitempty"`
}

type knowledgeProviderConfig struct {
	Type        string       `yaml:"type,omitempty"`
	Name        string       `yaml:"name,omitempty"`
	Description string       `yaml:"description,omitempty"`
	MaxResults  *int         `yaml:"max_results,omitempty"`
	Config      *rawYAMLNode `yaml:"config,omitempty"`
}

type pluginSpec struct {
	Type   string     `yaml:"type,omitempty"`
	Name   string     `yaml:"name,omitempty"`
	Config *yaml.Node `yaml:"config,omitempty"`
}

type rawYAMLNode struct {
	Node *yaml.Node
}

func (r *rawYAMLNode) UnmarshalYAML(node *yaml.Node) error {
	r.Node = node
	return nil
}

type yamlStringList []string

func (l *yamlStringList) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.SequenceNode:
		values := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			values = append(values, item.Value)
		}
		*l = yamlStringList(values)
		return nil
	case yaml.ScalarNode:
		*l = yamlStringList(splitCSV(node.Value))
		return nil
	default:
		return fmt.Errorf("expected string or list, got %v", node.Kind)
	}
}

type filePluginSpec struct {
	Type   string       `yaml:"type,omitempty"`
	Name   string       `yaml:"name,omitempty"`
	Config *rawYAMLNode `yaml:"config,omitempty"`
}

type redisConfig struct {
	URL      *string `yaml:"url,omitempty"`
	Instance *string `yaml:"instance,omitempty"`
	KeyPref  *string `yaml:"key_prefix,omitempty"`
}

type summaryConfig struct {
	Enabled             *bool    `yaml:"enabled,omitempty"`
	Mode                *string  `yaml:"mode,omitempty"`
	Policy              *string  `yaml:"policy,omitempty"`
	EventThreshold      *int     `yaml:"event_threshold,omitempty"`
	TokenThreshold      *int     `yaml:"token_threshold,omitempty"`
	IdleThreshold       *string  `yaml:"idle_threshold,omitempty"`
	MaxWords            *int     `yaml:"max_words,omitempty"`
	ApproxRunesPerToken *float64 `yaml:"approx_runes_per_token,omitempty"`
}

type memoryAuto struct {
	Enabled          *bool   `yaml:"enabled,omitempty"`
	Policy           *string `yaml:"policy,omitempty"`
	MessageThreshold *int    `yaml:"message_threshold,omitempty"`
	TimeInterval     *string `yaml:"time_interval,omitempty"`
}

func loadConfigFile(path string) (*fileConfig, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, nil
	}

	data, err := os.ReadFile(trimmed)
	if err != nil {
		return nil, err
	}
	data, err = expandEnvPlaceholders(data)
	if err != nil {
		return nil, err
	}

	var cfg fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil && extra != nil {
		return nil, errors.New("multiple YAML documents are not supported")
	}
	return &cfg, nil
}

func expandEnvPlaceholders(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	const prefix = "${"
	if !bytes.Contains(data, []byte(prefix)) {
		return data, nil
	}

	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != '$' || i+1 >= len(data) || data[i+1] != '{' {
			out = append(out, data[i])
			i++
			continue
		}

		end := bytes.IndexByte(data[i+2:], '}')
		if end < 0 {
			out = append(out, data[i])
			i++
			continue
		}
		rawName := strings.TrimSpace(string(data[i+2 : i+2+end]))
		if !isValidEnvName(rawName) {
			out = append(out, data[i:i+2+end+1]...)
			i += 2 + end + 1
			continue
		}

		val, ok := os.LookupEnv(rawName)
		if !ok {
			return nil, fmt.Errorf(
				"config: env var %s is not set",
				rawName,
			)
		}
		out = append(out, []byte(val)...)
		i += 2 + end + 1
	}
	return out, nil
}

func isValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if i == 0 {
			if isAlpha || r == '_' {
				continue
			}
			return false
		}
		if isAlpha || isDigit || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (cfg *fileConfig) apply(
	opts *runOptions,
	set map[string]struct{},
) error {
	if cfg == nil || opts == nil {
		return nil
	}

	if cfg.AppName != nil && !flagWasSet(set, "app-name") {
		opts.AppName = strings.TrimSpace(*cfg.AppName)
	}
	if cfg.StateDir != nil && !flagWasSet(set, "state-dir") {
		opts.StateDir = strings.TrimSpace(*cfg.StateDir)
	}
	if cfg.DebugRecorder != nil {
		if cfg.DebugRecorder.Enabled != nil &&
			!flagWasSet(set, flagDebugRecorder) {
			opts.DebugRecorderEnabled = *cfg.DebugRecorder.Enabled
		}
		if cfg.DebugRecorder.Dir != nil &&
			!flagWasSet(set, flagDebugRecorderDir) {
			opts.DebugRecorderDir = strings.TrimSpace(*cfg.DebugRecorder.Dir)
		}
		if cfg.DebugRecorder.Mode != nil &&
			!flagWasSet(set, flagDebugRecorderMode) {
			opts.DebugRecorderMode = strings.TrimSpace(*cfg.DebugRecorder.Mode)
		}
	}

	if cfg.HTTP != nil && cfg.HTTP.Addr != nil && !flagWasSet(set, "http-addr") {
		opts.HTTPAddr = strings.TrimSpace(*cfg.HTTP.Addr)
	}
	if cfg.Admin != nil {
		if cfg.Admin.Enabled != nil &&
			!flagWasSet(set, flagAdminEnabled) {
			opts.AdminEnabled = *cfg.Admin.Enabled
		}
		if cfg.Admin.Addr != nil &&
			!flagWasSet(set, flagAdminAddr) {
			opts.AdminAddr = strings.TrimSpace(*cfg.Admin.Addr)
		}
		if cfg.Admin.AutoPort != nil &&
			!flagWasSet(set, flagAdminAutoPort) {
			opts.AdminAutoPort = *cfg.Admin.AutoPort
		}
	}
	if cfg.Observability != nil {
		if cfg.Observability.Langfuse != nil {
			applyLangfuseConfig(
				cfg.Observability.Langfuse,
				opts,
			)
		}
		if cfg.Observability.LatencyDiag != nil {
			applyLatencyDiagnosticsConfig(
				cfg.Observability.LatencyDiag,
				opts,
				set,
			)
		}
	}
	if cfg.A2A != nil {
		if cfg.A2A.Enabled != nil &&
			!flagWasSet(set, flagA2AEnabled) {
			opts.A2AEnabled = *cfg.A2A.Enabled
		}
		if cfg.A2A.Host != nil &&
			!flagWasSet(set, flagA2AHost) {
			opts.A2AHost = *cfg.A2A.Host
		}
		if cfg.A2A.UserIDHeader != nil &&
			!flagWasSet(set, flagA2AUserIDHeader) {
			opts.A2AUserIDHeader = *cfg.A2A.UserIDHeader
		}
		if cfg.A2A.Streaming != nil &&
			!flagWasSet(set, flagA2AStreaming) {
			opts.A2AStreaming = *cfg.A2A.Streaming
		}
		if cfg.A2A.AdvertiseTools != nil &&
			!flagWasSet(set, flagA2AAdvertiseTools) {
			opts.A2AAdvertiseTools = *cfg.A2A.AdvertiseTools
		}
		if cfg.A2A.Name != nil &&
			!flagWasSet(set, flagA2AName) {
			opts.A2AName = *cfg.A2A.Name
		}
		if cfg.A2A.Description != nil &&
			!flagWasSet(set, flagA2ADescription) {
			opts.A2ADescription = *cfg.A2A.Description
		}
		normalizeA2AOptions(opts)
	}

	if cfg.Agent != nil {
		if cfg.Agent.Type != nil && !flagWasSet(set, "agent-type") {
			opts.AgentType = strings.TrimSpace(*cfg.Agent.Type)
		}
		if cfg.Agent.AddSessionSummary != nil &&
			!flagWasSet(set, flagAddSessionSummary) {
			opts.AddSessionSummary = *cfg.Agent.AddSessionSummary
		}
		if cfg.Agent.EnableContextCompaction != nil &&
			!flagWasSet(set, flagEnableContextCompaction) {
			opts.EnableContextCompaction = *cfg.Agent.EnableContextCompaction
		}
		if cfg.Agent.ContextCompactionOversizedToolResultMaxTokens != nil &&
			!flagWasSet(set, flagContextCompactionOversizedToolResultMaxTokens) {
			opts.ContextCompactionOversizedToolResultMaxTokens = *cfg.Agent.ContextCompactionOversizedToolResultMaxTokens
		}
		if cfg.Agent.MaxHistoryRuns != nil &&
			!flagWasSet(set, flagMaxHistoryRuns) {
			opts.MaxHistoryRuns = *cfg.Agent.MaxHistoryRuns
		}
		if cfg.Agent.MaxLLMCalls != nil &&
			!flagWasSet(set, flagMaxLLMCalls) {
			opts.MaxLLMCalls = *cfg.Agent.MaxLLMCalls
		}
		if cfg.Agent.MaxToolIterations != nil &&
			!flagWasSet(set, flagMaxToolIterations) {
			opts.MaxToolIterations = *cfg.Agent.MaxToolIterations
		}
		if cfg.Agent.PreloadMemory != nil &&
			!flagWasSet(set, flagPreloadMemory) {
			opts.PreloadMemory = *cfg.Agent.PreloadMemory
		}
		disablePostToolPrompt := firstBoolPtr(
			cfg.Agent.DisablePostToolPrompt,
			cfg.Agent.DisablePostToolPromptCamel,
		)
		if disablePostToolPrompt != nil {
			enabled := !*disablePostToolPrompt
			opts.PostToolPromptEnabled = &enabled
		}
		if cfg.Agent.Instruction != nil &&
			!flagWasSet(set, flagAgentInstruction) {
			opts.AgentInstruction = strings.TrimSpace(
				*cfg.Agent.Instruction,
			)
		}
		if len(cfg.Agent.InstructionFiles) > 0 &&
			!flagWasSet(set, flagAgentInstructionFiles) {
			opts.AgentInstructionFiles = strings.Join(
				cfg.Agent.InstructionFiles,
				csvDelimiter,
			)
		}
		if cfg.Agent.InstructionDir != nil &&
			!flagWasSet(set, flagAgentInstructionDir) {
			opts.AgentInstructionDir = strings.TrimSpace(
				*cfg.Agent.InstructionDir,
			)
		}
		if cfg.Agent.SystemPrompt != nil &&
			!flagWasSet(set, flagAgentSystemPrompt) {
			opts.AgentSystemPrompt = strings.TrimSpace(
				*cfg.Agent.SystemPrompt,
			)
		}
		if len(cfg.Agent.SystemPromptFiles) > 0 &&
			!flagWasSet(set, flagAgentSystemPromptFiles) {
			opts.AgentSystemPromptFiles = strings.Join(
				cfg.Agent.SystemPromptFiles,
				csvDelimiter,
			)
		}
		if cfg.Agent.SystemPromptDir != nil &&
			!flagWasSet(set, flagAgentSystemPromptDir) {
			opts.AgentSystemPromptDir = strings.TrimSpace(
				*cfg.Agent.SystemPromptDir,
			)
		}
		if cfg.Agent.RalphLoop != nil {
			if err := applyRalphLoopConfig(
				cfg.Agent.RalphLoop,
				opts,
				set,
			); err != nil {
				return err
			}
		}
		if cfg.Agent.ClaudeBin != nil &&
			!flagWasSet(set, "claude-bin") {
			opts.ClaudeBin = strings.TrimSpace(*cfg.Agent.ClaudeBin)
		}
		if cfg.Agent.ClaudeOutputFormat != nil &&
			!flagWasSet(set, "claude-output-format") {
			opts.ClaudeOutputFormat = strings.TrimSpace(
				*cfg.Agent.ClaudeOutputFormat,
			)
		}
		if len(cfg.Agent.ClaudeExtraArgs) > 0 &&
			!flagWasSet(set, "claude-extra-args") {
			opts.ClaudeExtraArgs = strings.Join(
				cfg.Agent.ClaudeExtraArgs,
				csvDelimiter,
			)
		}
		if len(cfg.Agent.ClaudeEnv) > 0 &&
			!flagWasSet(set, "claude-env") {
			opts.ClaudeEnv = strings.Join(
				cfg.Agent.ClaudeEnv,
				csvDelimiter,
			)
		}
		if cfg.Agent.ClaudeWorkDir != nil &&
			!flagWasSet(set, "claude-workdir") {
			opts.ClaudeWorkDir = strings.TrimSpace(
				*cfg.Agent.ClaudeWorkDir,
			)
		}
	}

	if cfg.Model != nil {
		if cfg.Model.Mode != nil && !flagWasSet(set, "mode") {
			opts.ModelMode = strings.TrimSpace(*cfg.Model.Mode)
		}
		if cfg.Model.Name != nil && !flagWasSet(set, "model") {
			opts.OpenAIModel = strings.TrimSpace(*cfg.Model.Name)
		}
		if cfg.Model.BaseURL != nil &&
			!flagWasSet(set, "openai-base-url") {
			opts.OpenAIBaseURL = strings.TrimSpace(*cfg.Model.BaseURL)
		}
		if cfg.Model.OpenAIVariant != nil &&
			!flagWasSet(set, "openai-variant") {
			opts.OpenAIVariant = strings.TrimSpace(
				*cfg.Model.OpenAIVariant,
			)
		}
		if len(cfg.Model.Headers) > 0 {
			opts.OpenAIHeaders = cleanHeaderMap(cfg.Model.Headers)
		}
		if cfg.Model.Config != nil {
			opts.ModelConfig = cfg.Model.Config.Node
		}
		if cfg.Model.GenerationConfig != nil {
			opts.GenerationConfig = resolveGenerationConfigYAML(
				cfg.Model.GenerationConfig,
			)
		}
	}
	if cfg.Knowledges != nil {
		if len(cfg.Knowledges.Entries) > 0 {
			return fmt.Errorf(
				"knowledges.entries is no longer supported; " +
					"rename it to knowledges.providers and wrap " +
					"embedder/vector_store under a 'config' key " +
					"(see README for the new format)",
			)
		}
		knowledges, err := convertKnowledgeConfigs(cfg.Knowledges.Providers)
		if err != nil {
			return err
		}
		opts.KnowledgesConfig = knowledges
	}

	if cfg.Gateway != nil {
		if len(cfg.Gateway.AllowUsers) > 0 &&
			!flagWasSet(set, "allow-users") {
			opts.AllowUsers = strings.Join(
				cfg.Gateway.AllowUsers,
				csvDelimiter,
			)
		}
		if cfg.Gateway.RequireMention != nil &&
			!flagWasSet(set, "require-mention") {
			opts.RequireMention = *cfg.Gateway.RequireMention
		}
		if len(cfg.Gateway.MentionPatterns) > 0 &&
			!flagWasSet(set, "mention") {
			opts.Mention = strings.Join(
				cfg.Gateway.MentionPatterns,
				csvDelimiter,
			)
		}
	}
	if cfg.RuntimeProfiles != nil {
		if err := validateRuntimeProfiles(cfg.RuntimeProfiles); err != nil {
			return err
		}
		opts.RuntimeProfiles = cfg.RuntimeProfiles
	}

	if len(cfg.Channels) > 0 {
		opts.Channels = convertPluginSpecs(cfg.Channels)
	}

	if cfg.Skills != nil {
		if cfg.Skills.Root != nil && !flagWasSet(set, "skills-root") {
			opts.SkillsRoot = strings.TrimSpace(*cfg.Skills.Root)
		}
		if len(cfg.Skills.ExtraDirs) > 0 &&
			!flagWasSet(set, "skills-extra-dirs") {
			opts.SkillsExtraDir = strings.Join(
				cfg.Skills.ExtraDirs,
				csvDelimiter,
			)
		}
		if cfg.Skills.Debug != nil && !flagWasSet(set, "skills-debug") {
			opts.SkillsDebug = *cfg.Skills.Debug
		}
		allowBundled := cfg.Skills.AllowBundled
		if len(allowBundled) == 0 {
			allowBundled = cfg.Skills.AllowBundledCamel
		}
		if len(allowBundled) > 0 &&
			!flagWasSet(set, flagSkillsAllowBundled) {
			opts.SkillsAllowBundled = strings.Join(
				allowBundled,
				csvDelimiter,
			)
		}
		if cfg.Skills.Watch != nil &&
			!flagWasSet(set, flagSkillsWatch) {
			opts.SkillsWatch = *cfg.Skills.Watch
		}
		watchBundled := firstBoolPtr(
			cfg.Skills.WatchBundled,
			cfg.Skills.WatchBundledCamel,
		)
		if watchBundled != nil &&
			!flagWasSet(set, flagSkillsWatchBundled) {
			opts.SkillsWatchBundled = *watchBundled
		}
		watchDebounceMS := firstIntPtr(
			cfg.Skills.WatchDebounceMS,
			cfg.Skills.WatchDebounceCamel,
		)
		if watchDebounceMS != nil &&
			!flagWasSet(set, flagSkillsWatchDebounce) {
			opts.SkillsWatchDebounce = time.Duration(
				*watchDebounceMS,
			) * time.Millisecond
		}
		summaryCacheTTLMS := firstIntPtr(
			cfg.Skills.SummaryCacheTTLMS,
			cfg.Skills.SummaryCacheCamel,
		)
		if summaryCacheTTLMS != nil &&
			!flagWasSet(set, flagSkillsSummaryCacheTTL) {
			opts.SkillsSummaryCacheTTL = time.Duration(
				*summaryCacheTTLMS,
			) * time.Millisecond
		}
		overviewLimit := firstIntPtr(
			cfg.Skills.OverviewLimit,
			cfg.Skills.OverviewLimitCamel,
		)
		if overviewLimit != nil &&
			!flagWasSet(set, flagSkillsOverviewLimit) {
			opts.SkillsOverviewLimit = *overviewLimit
		}
		overviewPinned := cfg.Skills.OverviewPinned
		if len(overviewPinned) == 0 {
			overviewPinned = cfg.Skills.OverviewPinnedCamel
		}
		if len(overviewPinned) > 0 &&
			!flagWasSet(set, flagSkillsOverviewPinned) {
			opts.SkillsOverviewPinned = strings.Join(
				overviewPinned,
				csvDelimiter,
			)
		}
		if len(cfg.Skills.Entries) > 0 {
			opts.SkillConfigs = convertSkillConfigs(
				cfg.Skills.Entries,
			)
		}
		toolProfile := firstStringPtr(
			cfg.Skills.ToolProfile,
			cfg.Skills.ToolProfileCamel,
		)
		if toolProfile != nil &&
			!flagWasSet(set, flagSkillsToolProfile) {
			opts.SkillsToolProfile = strings.TrimSpace(*toolProfile)
		}
		loadMode := firstStringPtr(
			cfg.Skills.LoadMode,
			cfg.Skills.LoadModeCamel,
		)
		if loadMode != nil && !flagWasSet(set, flagSkillsLoadMode) {
			opts.SkillsLoadMode = strings.TrimSpace(*loadMode)
		}
		maxLoaded := firstIntPtr(
			cfg.Skills.MaxLoadedSkills,
			cfg.Skills.MaxLoadedCamel,
		)
		if maxLoaded != nil && !flagWasSet(set, flagSkillsMaxLoaded) {
			opts.SkillsMaxLoaded = *maxLoaded
		}
		toolResults := firstBoolPtr(
			cfg.Skills.ToolResults,
			cfg.Skills.ToolResultsCamel,
		)
		if toolResults != nil && !flagWasSet(set, flagSkillsToolResults) {
			opts.SkillsToolResults = *toolResults
		}
		skipFallback := firstBoolPtr(
			cfg.Skills.SkipSummaryFallback,
			cfg.Skills.SkipFallbackCamel,
		)
		if skipFallback != nil &&
			!flagWasSet(set, flagSkillsSkipFallback) {
			opts.SkillsSkipFallback = *skipFallback
		}
		opts.SkillsToolingGuide = firstStringPtr(
			cfg.Skills.ToolingGuidance,
			cfg.Skills.ToolingGuidanceCamel,
		)
	}

	if cfg.Tools != nil {
		if cfg.Tools.EnableLocalExec != nil &&
			!flagWasSet(set, "enable-local-exec") {
			opts.EnableLocalExec = *cfg.Tools.EnableLocalExec
		}
		if cfg.Tools.CodeExecutor != nil {
			codeExecutor, err := convertCodeExecutorConfig(
				cfg.Tools.CodeExecutor,
			)
			if err != nil {
				return fmt.Errorf("tools.code_executor: %w", err)
			}
			opts.CodeExecutor = codeExecutor
		}
		if cfg.Tools.EnableOpenClawTools != nil {
			opts.enableOpenClawToolsExplicit = true
			if !flagWasSet(set, "enable-openclaw-tools") {
				opts.EnableOpenClawTools =
					*cfg.Tools.EnableOpenClawTools
			}
		}
		opts.OpenClawToolingGuide = firstStringPtr(
			cfg.Tools.OpenClawToolingGuide,
			cfg.Tools.OpenClawToolingGuideCamel,
		)
		if cfg.Tools.EnableParallelTools != nil &&
			!flagWasSet(set, flagEnableParallelTools) {
			opts.EnableParallelTools = *cfg.Tools.EnableParallelTools
		}
		if cfg.Tools.RefreshToolSetsOnRun != nil &&
			!flagWasSet(set, "refresh-toolsets-on-run") {
			opts.RefreshToolSetsOnRun = *cfg.Tools.RefreshToolSetsOnRun
		}
		if !flagWasSet(set, flagDeferToolSurface) {
			deferConfigured := false
			if cfg.Tools.DeferToDynamicAgent != nil {
				deferConfigured = true
				opts.DeferToolSurface = *cfg.Tools.DeferToDynamicAgent
			}
			if cfg.Tools.DeferToDynamicAgentCamel != nil {
				deferConfigured = true
				opts.DeferToolSurface =
					*cfg.Tools.DeferToDynamicAgentCamel
			}
			if deferConfigured &&
				!opts.DeferToolSurface &&
				!flagWasSet(set, flagDeferToolSurfaceMode) {
				opts.DeferToolSurfaceMode = deferToolSurfaceModeOff
			}
		}
		deferMode := firstStringPtr(
			cfg.Tools.DeferToDynamicAgentMode,
			cfg.Tools.DeferToDynamicAgentModeCamel,
		)
		if deferMode != nil &&
			!flagWasSet(set, flagDeferToolSurface) &&
			!flagWasSet(set, flagDeferToolSurfaceMode) {
			opts.DeferToolSurface = false
			opts.DeferToolSurfaceMode = *deferMode
			opts.deferToolSurfaceModeExplicit = true
		}
		deferChars := firstIntPtr(
			cfg.Tools.DeferToDynamicAgentChars,
			cfg.Tools.DeferToDynamicAgentCharsCamel,
		)
		if deferChars != nil &&
			!flagWasSet(set, flagDeferToolSurfaceChars) {
			opts.DeferToolSurfaceChars = *deferChars
		}
		deferDefaults := firstBoolPtr(
			cfg.Tools.DeferDefaultDirectTools,
			cfg.Tools.DeferDefaultDirectToolsCamel,
		)
		if deferDefaults != nil &&
			!flagWasSet(set, flagDeferToolSurfaceDefaultDirectTools) {
			opts.DeferToolSurfaceDefaultDirectTools = *deferDefaults
		}
		if !flagWasSet(set, flagDeferToolSurfaceDirect) {
			if len(cfg.Tools.DeferDirectTools) > 0 {
				opts.DeferToolSurfaceDirect = strings.Join(
					[]string(cfg.Tools.DeferDirectTools),
					",",
				)
			}
			if len(cfg.Tools.DeferDirectToolsCamel) > 0 {
				opts.DeferToolSurfaceDirect = strings.Join(
					[]string(cfg.Tools.DeferDirectToolsCamel),
					",",
				)
			}
		}
		dynamicTimeout := firstStringPtr(
			cfg.Tools.DynamicAgentTimeout,
			cfg.Tools.DynamicAgentTimeoutCamel,
		)
		if dynamicTimeout != nil &&
			!flagWasSet(set, flagDynamicAgentTimeout) {
			dur, err := parseDuration(*dynamicTimeout)
			if err != nil {
				return fmt.Errorf("tools.dynamic_agent_timeout: %w", err)
			}
			opts.DynamicAgentTimeout = dur
		}
		hostExecTimeout := firstStringPtr(
			cfg.Tools.HostExecDefaultTimeout,
			cfg.Tools.HostExecDefaultTimeoutCamel,
		)
		if hostExecTimeout != nil &&
			!flagWasSet(set, flagHostExecDefaultTimeout) {
			dur, err := parseDuration(*hostExecTimeout)
			if err != nil {
				return fmt.Errorf(
					"tools.host_exec_default_timeout: %w",
					err,
				)
			}
			opts.HostExecDefaultTimeout = dur
		}
		if len(cfg.Tools.Providers) > 0 {
			opts.ToolProviders = convertPluginSpecs(cfg.Tools.Providers)
		}
		if len(cfg.Tools.ToolSets) > 0 {
			opts.ToolSets = convertPluginSpecs(cfg.Tools.ToolSets)
		}
	}

	if cfg.Session != nil {
		if cfg.Session.Backend != nil &&
			!flagWasSet(set, "session-backend") {
			opts.SessionBackend = strings.TrimSpace(
				*cfg.Session.Backend,
			)
		}
		if cfg.Session.Redis != nil {
			if cfg.Session.Redis.URL != nil &&
				!flagWasSet(set, "session-redis-url") {
				opts.SessionRedisURL = strings.TrimSpace(
					*cfg.Session.Redis.URL,
				)
			}
			if cfg.Session.Redis.Instance != nil &&
				!flagWasSet(set, "session-redis-instance") {
				opts.SessionRedisInstance = strings.TrimSpace(
					*cfg.Session.Redis.Instance,
				)
			}
			if cfg.Session.Redis.KeyPref != nil &&
				!flagWasSet(set, "session-redis-key-prefix") {
				opts.SessionRedisKeyPref = strings.TrimSpace(
					*cfg.Session.Redis.KeyPref,
				)
			}
		}
		if cfg.Session.Summary != nil {
			if err := applySessionSummary(
				cfg.Session.Summary,
				opts,
				set,
			); err != nil {
				return err
			}
		}
		if cfg.Session.Config != nil {
			opts.SessionConfig = cfg.Session.Config.Node
		}
	}

	if cfg.Memory != nil {
		if cfg.Memory.Backend != nil &&
			!flagWasSet(set, "memory-backend") {
			opts.MemoryBackend = strings.TrimSpace(*cfg.Memory.Backend)
		}
		if cfg.Memory.Redis != nil {
			if cfg.Memory.Redis.URL != nil &&
				!flagWasSet(set, "memory-redis-url") {
				opts.MemoryRedisURL = strings.TrimSpace(
					*cfg.Memory.Redis.URL,
				)
			}
			if cfg.Memory.Redis.Instance != nil &&
				!flagWasSet(set, "memory-redis-instance") {
				opts.MemoryRedisInstance = strings.TrimSpace(
					*cfg.Memory.Redis.Instance,
				)
			}
			if cfg.Memory.Redis.KeyPref != nil &&
				!flagWasSet(set, "memory-redis-key-prefix") {
				opts.MemoryRedisKeyPref = strings.TrimSpace(
					*cfg.Memory.Redis.KeyPref,
				)
			}
		}
		if cfg.Memory.Limit != nil && !flagWasSet(set, "memory-limit") {
			opts.MemoryLimit = *cfg.Memory.Limit
		}
		if cfg.Memory.Auto != nil {
			if err := applyMemoryAuto(
				cfg.Memory.Auto,
				opts,
				set,
			); err != nil {
				return err
			}
		}
		if cfg.Memory.Config != nil {
			opts.MemoryConfig = cfg.Memory.Config.Node
		}
	}

	if cfg.Evolution != nil {
		if cfg.Evolution.Enabled != nil {
			opts.EvolutionEnabled = *cfg.Evolution.Enabled
		}
		if cfg.Evolution.HumanGate != nil {
			opts.EvolutionHumanGate = strings.TrimSpace(*cfg.Evolution.HumanGate)
		}
		if cfg.Evolution.SkillScope != nil &&
			cfg.Evolution.SkillScope.Mode != nil {
			mode, err := parseEvolutionSkillScopeMode(
				*cfg.Evolution.SkillScope.Mode,
			)
			if err != nil {
				return fmt.Errorf("evolution.skill_scope.mode: %w", err)
			}
			opts.EvolutionSkillScopeMode = mode
		}
	}

	return nil
}

func applyLangfuseConfig(
	cfg *langfuseConfig,
	opts *runOptions,
) {
	if cfg == nil || opts == nil {
		return
	}

	if cfg.Enabled != nil {
		opts.LangfuseEnabled = *cfg.Enabled
	}
	if cfg.Required != nil {
		opts.LangfuseRequired = *cfg.Required
	}
	if cfg.UIBaseURL != nil {
		opts.LangfuseUIBaseURL = strings.TrimSpace(*cfg.UIBaseURL)
	}
	if cfg.TraceURLTemplate != nil {
		opts.LangfuseTraceURLTemplate = strings.TrimSpace(
			*cfg.TraceURLTemplate,
		)
	}
	if cfg.ObservationLeafValueMaxBytes != nil {
		value := *cfg.ObservationLeafValueMaxBytes
		opts.LangfuseObservationLeafValueMaxBytes = &value
	}
}

func applyLatencyDiagnosticsConfig(
	cfg *latencyDiagnosticsConfig,
	opts *runOptions,
	set map[string]struct{},
) {
	if cfg == nil || opts == nil {
		return
	}
	if cfg.Enabled != nil && !flagWasSet(set, flagLatencyDiagnostics) {
		opts.LatencyDiagnosticsEnabled = *cfg.Enabled
	}
	if cfg.Events != nil && !flagWasSet(set, flagLatencyDiagnosticsEvents) {
		opts.LatencyDiagnosticsEvents = *cfg.Events
	}
}

func applyRalphLoopConfig(
	cfg *ralphLoopConfig,
	opts *runOptions,
	set map[string]struct{},
) error {
	if cfg == nil || opts == nil {
		return nil
	}

	if cfg.Enabled != nil &&
		!flagWasSet(set, flagAgentRalphLoopEnabled) {
		opts.RalphLoopEnabled = *cfg.Enabled
	}
	if cfg.MaxIterations != nil &&
		!flagWasSet(set, flagAgentRalphLoopMaxIterations) {
		opts.RalphLoopMaxIterations = *cfg.MaxIterations
	}
	if cfg.CompletionPromise != nil &&
		!flagWasSet(set, flagAgentRalphLoopCompletionPromise) {
		opts.RalphLoopCompletionPromise = strings.TrimSpace(
			*cfg.CompletionPromise,
		)
	}
	if cfg.PromiseTagOpen != nil &&
		!flagWasSet(set, flagAgentRalphLoopPromiseTagOpen) {
		opts.RalphLoopPromiseTagOpen = strings.TrimSpace(
			*cfg.PromiseTagOpen,
		)
	}
	if cfg.PromiseTagClose != nil &&
		!flagWasSet(set, flagAgentRalphLoopPromiseTagClose) {
		opts.RalphLoopPromiseTagClose = strings.TrimSpace(
			*cfg.PromiseTagClose,
		)
	}

	if cfg.Verify == nil {
		return nil
	}

	if cfg.Verify.Command != nil &&
		!flagWasSet(set, flagAgentRalphLoopVerifyCommand) {
		opts.RalphLoopVerifyCommand = strings.TrimSpace(
			*cfg.Verify.Command,
		)
	}
	if cfg.Verify.WorkDir != nil &&
		!flagWasSet(set, flagAgentRalphLoopVerifyWorkDir) {
		opts.RalphLoopVerifyWorkDir = strings.TrimSpace(
			*cfg.Verify.WorkDir,
		)
	}
	if cfg.Verify.Timeout != nil &&
		!flagWasSet(set, flagAgentRalphLoopVerifyTimeout) {
		dur, err := parseDuration(*cfg.Verify.Timeout)
		if err != nil {
			return fmt.Errorf(
				"agent.ralph_loop.verify.timeout: %w",
				err,
			)
		}
		opts.RalphLoopVerifyTimeout = dur
	}
	if len(cfg.Verify.Env) > 0 &&
		!flagWasSet(set, flagAgentRalphLoopVerifyEnv) {
		opts.RalphLoopVerifyEnv = strings.Join(
			cfg.Verify.Env,
			csvDelimiter,
		)
	}

	return nil
}

func convertPluginSpecs(specs []filePluginSpec) []pluginSpec {
	out := make([]pluginSpec, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		var cfg *yaml.Node
		if spec.Config != nil {
			cfg = spec.Config.Node
		}
		out = append(out, pluginSpec{
			Type:   spec.Type,
			Name:   spec.Name,
			Config: cfg,
		})
	}
	return out
}

func convertSkillConfigs(
	entries map[string]skillEntryConfig,
) map[string]ocskills.SkillConfig {
	if len(entries) == 0 {
		return nil
	}

	out := make(map[string]ocskills.SkillConfig, len(entries))
	for rawKey, rawCfg := range entries {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			continue
		}

		apiKey := strings.TrimSpace(rawCfg.APIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(rawCfg.APIKeyCamel)
		}
		out[key] = ocskills.SkillConfig{
			Enabled: rawCfg.Enabled,
			APIKey:  apiKey,
			Env:     rawCfg.Env,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func convertKnowledgeConfigs(
	providers []knowledgeProviderConfig,
) ([]knowledgeEntry, error) {
	if len(providers) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool, len(providers))
	out := make([]knowledgeEntry, 0, len(providers))

	for i, p := range providers {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return nil, fmt.Errorf(
				"knowledges.providers[%d].name is empty", i,
			)
		}
		if seen[name] {
			return nil, fmt.Errorf(
				"duplicate knowledge name: %s", name,
			)
		}
		seen[name] = true

		typeName := strings.ToLower(strings.TrimSpace(p.Type))
		if typeName == "" {
			typeName = "builtin"
		}

		var maxResults int
		if p.MaxResults != nil && *p.MaxResults > 0 {
			maxResults = *p.MaxResults
		}

		var config *yaml.Node
		if p.Config != nil {
			config = p.Config.Node
		}

		out = append(out, knowledgeEntry{
			Type:        typeName,
			Name:        name,
			Description: strings.TrimSpace(p.Description),
			MaxResults:  maxResults,
			Config:      config,
		})
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func convertCodeExecutorConfig(
	cfg *codeExecutorConfig,
) (codeExecutorOptions, error) {
	if cfg == nil {
		return codeExecutorOptions{}, nil
	}
	typeName := strings.ToLower(strings.TrimSpace(cfg.Type))
	out := codeExecutorOptions{
		Type:                  typeName,
		AutoExecuteCodeBlocks: cfg.AutoExecuteCodeBlocks,
	}
	switch typeName {
	case "":
		if cfg.Sandbox != nil {
			return codeExecutorOptions{}, fmt.Errorf(
				"sandbox config requires type %q",
				codeExecutorTypeSandbox,
			)
		}
		return out, nil
	case codeExecutorTypeSandbox:
		sandboxCfg, err := convertSandboxCodeExecutorConfig(cfg.Sandbox)
		if err != nil {
			return codeExecutorOptions{}, err
		}
		out.Sandbox = sandboxCfg
		return out, nil
	default:
		return codeExecutorOptions{}, fmt.Errorf(
			"invalid type %q: want sandbox or empty",
			cfg.Type,
		)
	}
}

func convertSandboxCodeExecutorConfig(
	cfg *sandboxCodeExecutorConfig,
) (sandboxCodeExecutorOptions, error) {
	out := sandboxCodeExecutorOptions{
		Backend:        sandboxBackendAuto,
		Profile:        sandboxProfileWorkspaceWrite,
		Network:        sandboxNetworkRestricted,
		DefaultTimeout: defaultSandboxCodeExecutorTimeout,
		OutputMaxBytes: defaultSandboxCodeExecutorOutputMaxBytes,
		ShellEnv: sandboxShellEnvOptions{
			Inherit:              sandboxShellEnvInheritCore,
			ApplyDefaultExcludes: true,
		},
	}
	if cfg == nil {
		return out, nil
	}
	out.WorkspaceRoot = strings.TrimSpace(cfg.WorkspaceRoot)
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend != "" {
		switch backend {
		case sandboxBackendAuto, sandboxBackendLinuxBubblewrap:
			out.Backend = backend
		default:
			return sandboxCodeExecutorOptions{}, fmt.Errorf(
				"sandbox.backend %q: want auto|linux-bubblewrap",
				cfg.Backend,
			)
		}
	}
	profile := strings.ToLower(strings.TrimSpace(cfg.Profile))
	if profile != "" {
		switch profile {
		case sandboxProfileWorkspaceWrite,
			sandboxProfileReadOnly,
			sandboxProfileDisabled:
			out.Profile = profile
		default:
			return sandboxCodeExecutorOptions{}, fmt.Errorf(
				"sandbox.profile %q: want workspace_write|read_only|disabled",
				cfg.Profile,
			)
		}
	}
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network != "" {
		switch network {
		case sandboxNetworkRestricted, sandboxNetworkEnabled:
			out.Network = network
		default:
			return sandboxCodeExecutorOptions{}, fmt.Errorf(
				"sandbox.network %q: want restricted|enabled",
				cfg.Network,
			)
		}
	}
	timeout := strings.TrimSpace(cfg.DefaultTimeout)
	if timeout != "" {
		dur, err := parseDuration(timeout)
		if err != nil {
			return sandboxCodeExecutorOptions{}, fmt.Errorf(
				"sandbox.default_timeout: %w",
				err,
			)
		}
		if dur <= 0 {
			return sandboxCodeExecutorOptions{}, fmt.Errorf(
				"sandbox.default_timeout must be positive",
			)
		}
		out.DefaultTimeout = dur
	}
	if cfg.OutputMaxBytes != nil {
		if *cfg.OutputMaxBytes <= 0 {
			return sandboxCodeExecutorOptions{}, fmt.Errorf(
				"sandbox.output_max_bytes must be positive",
			)
		}
		out.OutputMaxBytes = *cfg.OutputMaxBytes
	}
	if cfg.ShellEnv != nil {
		shellEnv, err := convertSandboxShellEnvConfig(cfg.ShellEnv)
		if err != nil {
			return sandboxCodeExecutorOptions{}, err
		}
		out.ShellEnv = shellEnv
	}
	return out, nil
}

func convertSandboxShellEnvConfig(
	cfg *sandboxShellEnvConfig,
) (sandboxShellEnvOptions, error) {
	out := sandboxShellEnvOptions{
		Inherit:              sandboxShellEnvInheritCore,
		ApplyDefaultExcludes: true,
	}
	if cfg == nil {
		return out, nil
	}
	inherit := strings.ToLower(strings.TrimSpace(cfg.Inherit))
	if inherit != "" {
		switch inherit {
		case sandboxShellEnvInheritAll,
			sandboxShellEnvInheritCore,
			sandboxShellEnvInheritNone:
			out.Inherit = inherit
		default:
			return sandboxShellEnvOptions{}, fmt.Errorf(
				"sandbox.shell_env.inherit %q: want all|core|none",
				cfg.Inherit,
			)
		}
	}
	if cfg.ApplyDefaultExcludes != nil {
		out.ApplyDefaultExcludes = *cfg.ApplyDefaultExcludes
	}
	out.Exclude = trimStringSlice(cfg.Exclude)
	out.IncludeOnly = trimStringSlice(cfg.IncludeOnly)
	if len(cfg.Set) > 0 {
		out.Set = make(map[string]string, len(cfg.Set))
		for key, value := range cfg.Set {
			name := strings.TrimSpace(key)
			if name == "" {
				return sandboxShellEnvOptions{}, fmt.Errorf(
					"sandbox.shell_env.set contains empty key",
				)
			}
			out.Set[name] = value
		}
	}
	return out, nil
}

func trimStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func applySessionSummary(
	cfg *summaryConfig,
	opts *runOptions,
	set map[string]struct{},
) error {
	if cfg == nil || opts == nil {
		return nil
	}

	if cfg.Enabled != nil && !flagWasSet(set, "session-summary") {
		opts.SessionSummaryEnabled = *cfg.Enabled
	}
	if cfg.Mode != nil && !flagWasSet(set, "session-summary-mode") {
		opts.SessionSummaryMode = strings.TrimSpace(*cfg.Mode)
	}
	if cfg.Policy != nil && !flagWasSet(set, "session-summary-policy") {
		opts.SessionSummaryPolicy = strings.TrimSpace(*cfg.Policy)
	}
	if cfg.EventThreshold != nil &&
		!flagWasSet(set, "session-summary-events") {
		opts.SessionSummaryEventCount = *cfg.EventThreshold
	}
	if cfg.TokenThreshold != nil &&
		!flagWasSet(set, "session-summary-tokens") {
		opts.SessionSummaryTokenCount = *cfg.TokenThreshold
	}
	if cfg.IdleThreshold != nil && !flagWasSet(set, "session-summary-idle") {
		dur, err := parseDuration(*cfg.IdleThreshold)
		if err != nil {
			return fmt.Errorf("session.summary.idle_threshold: %w", err)
		}
		opts.SessionSummaryIdleThreshold = dur
	}
	if cfg.MaxWords != nil && !flagWasSet(set, "session-summary-max-words") {
		opts.SessionSummaryMaxWords = *cfg.MaxWords
	}
	if cfg.ApproxRunesPerToken != nil &&
		!flagWasSet(set, "session-summary-approx-runes-per-token") {
		opts.SessionSummaryApproxRunesPerToken = *cfg.ApproxRunesPerToken
	}
	return nil
}

func applyMemoryAuto(
	cfg *memoryAuto,
	opts *runOptions,
	set map[string]struct{},
) error {
	if cfg == nil || opts == nil {
		return nil
	}

	if cfg.Enabled != nil && !flagWasSet(set, "memory-auto") {
		opts.MemoryAutoEnabled = *cfg.Enabled
	}
	if cfg.Policy != nil && !flagWasSet(set, "memory-auto-policy") {
		opts.MemoryAutoPolicy = strings.TrimSpace(*cfg.Policy)
	}
	if cfg.MessageThreshold != nil &&
		!flagWasSet(set, "memory-auto-messages") {
		opts.MemoryAutoMessageThreshold = *cfg.MessageThreshold
	}
	if cfg.TimeInterval != nil &&
		!flagWasSet(set, "memory-auto-interval") {
		dur, err := parseDuration(*cfg.TimeInterval)
		if err != nil {
			return fmt.Errorf("memory.auto.time_interval: %w", err)
		}
		opts.MemoryAutoTimeInterval = dur
	}
	return nil
}

func parseDuration(raw string) (time.Duration, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, nil
	}
	return time.ParseDuration(v)
}

func parseEvolutionSkillScopeMode(raw string) (skill.SkillScopeMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return skill.SkillScopeNone, nil
	case string(skill.SkillScopeApp):
		return skill.SkillScopeApp, nil
	case string(skill.SkillScopeUser):
		return skill.SkillScopeUser, nil
	default:
		return skill.SkillScopeNone, fmt.Errorf("unsupported mode %q", raw)
	}
}

func flagWasSet(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}

func skillsOptionExitCode(set map[string]struct{}) int {
	if flagWasSet(set, flagSkillsToolProfile) ||
		flagWasSet(set, flagSkillsLoadMode) {
		return 2
	}
	return 1
}

func finalizeRunOptions(opts *runOptions) error {
	if opts == nil {
		return nil
	}
	profile, err := normalizeSkillsToolProfile(opts.SkillsToolProfile)
	if err != nil {
		return err
	}
	opts.SkillsToolProfile = profile
	mode, err := normalizeSkillsLoadMode(opts.SkillsLoadMode)
	if err != nil {
		return err
	}
	opts.SkillsLoadMode = mode
	if opts.SkillsWatchDebounce < 0 {
		return fmt.Errorf(
			"invalid skills watch debounce: %v",
			opts.SkillsWatchDebounce,
		)
	}
	if opts.SkillsSummaryCacheTTL < 0 {
		return fmt.Errorf(
			"invalid skills summary cache ttl: %v",
			opts.SkillsSummaryCacheTTL,
		)
	}
	if opts.SkillsOverviewLimit < 0 {
		return fmt.Errorf(
			"invalid skills overview limit: %d",
			opts.SkillsOverviewLimit,
		)
	}
	if opts.MaxToolIterations < 0 {
		return fmt.Errorf(
			"invalid max tool iterations: %d",
			opts.MaxToolIterations,
		)
	}
	if opts.MaxLLMCalls < 0 {
		return fmt.Errorf(
			"invalid max LLM calls: %d",
			opts.MaxLLMCalls,
		)
	}
	opts.EvolutionSkillScopeMode = skill.NormalizeSkillScopeMode(
		opts.EvolutionSkillScopeMode,
	)
	opts.MemoryBackend = resolveMemoryBackendType(opts.MemoryBackend)
	opts.AdminAddr = strings.TrimSpace(opts.AdminAddr)
	if opts.AdminEnabled && opts.AdminAddr == "" {
		opts.AdminAddr = defaultAdminAddr
	}
	opts.LangfuseUIBaseURL = strings.TrimRight(
		strings.TrimSpace(opts.LangfuseUIBaseURL),
		"/",
	)
	opts.LangfuseTraceURLTemplate = strings.TrimSpace(
		opts.LangfuseTraceURLTemplate,
	)
	if v := opts.SessionSummaryApproxRunesPerToken; math.IsNaN(v) ||
		math.IsInf(v, 0) || v < 0 {
		return fmt.Errorf(
			"invalid session-summary-approx-runes-per-token: %v", v,
		)
	}
	if opts.DeferToolSurface {
		opts.DeferToolSurfaceMode = deferToolSurfaceModeOn
	} else {
		mode, err := normalizeDeferToolSurfaceMode(
			opts.DeferToolSurfaceMode,
		)
		if err != nil {
			return err
		}
		opts.DeferToolSurfaceMode = mode
	}
	if opts.DeferToolSurfaceChars < 0 {
		return fmt.Errorf(
			"invalid defer tool surface threshold chars: %d",
			opts.DeferToolSurfaceChars,
		)
	}
	if opts.DynamicAgentTimeout < 0 {
		return fmt.Errorf(
			"invalid dynamic agent timeout: %s",
			opts.DynamicAgentTimeout,
		)
	}
	if opts.HostExecDefaultTimeout < 0 {
		return fmt.Errorf(
			"invalid host exec default timeout: %s",
			opts.HostExecDefaultTimeout,
		)
	}
	opts.DeferToolSurfaceDirect = strings.Join(
		normalizeStringList(splitCSV(opts.DeferToolSurfaceDirect)),
		",",
	)
	normalizeA2AOptions(opts)
	return nil
}

func normalizeA2AOptions(opts *runOptions) {
	if opts == nil {
		return
	}
	opts.A2AHost = normalizeA2AHost(opts.A2AHost)
	opts.A2AUserIDHeader = strings.TrimSpace(opts.A2AUserIDHeader)
	opts.A2AName = strings.TrimSpace(opts.A2AName)
	opts.A2ADescription = strings.TrimSpace(opts.A2ADescription)
}

func normalizeA2AHost(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return ""
	}
	return ia2a.NormalizeURL(host)
}

func normalizeSkillsToolProfile(raw string) (string, error) {
	profile := strings.ToLower(strings.TrimSpace(raw))
	if profile == "" {
		return defaultSkillsToolProfile, nil
	}
	switch profile {
	case skillprofile.Full, skillprofile.KnowledgeOnly:
		return profile, nil
	default:
		return "", fmt.Errorf(
			"invalid skills tool profile %q: "+
				"want full|knowledge_only",
			raw,
		)
	}
}

func normalizeSkillsLoadMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return defaultSkillsLoadMode, nil
	}
	switch mode {
	case "once", "turn", "session":
		return mode, nil
	default:
		return "", fmt.Errorf(
			"invalid skills load mode %q: want once|turn|session",
			raw,
		)
	}
}

func firstBoolPtr(primary, fallback *bool) *bool {
	if primary != nil {
		return primary
	}
	return fallback
}

func boolPtr(value bool) *bool {
	return &value
}

func firstIntPtr(primary, fallback *int) *int {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstStringPtr(primary, fallback *string) *string {
	if primary != nil {
		return primary
	}
	return fallback
}

func resolveGenerationConfigYAML(
	cfg *generationConfigYAML,
) *model.GenerationConfig {
	if cfg == nil {
		return nil
	}
	out := &model.GenerationConfig{Stream: true}
	out.MaxTokens = cfg.MaxTokens
	out.Temperature = cfg.Temperature
	out.TopP = cfg.TopP
	if cfg.Stream != nil {
		out.Stream = *cfg.Stream
	}
	if cfg.Stop != nil {
		out.Stop = append([]string(nil), cfg.Stop...)
	}
	out.PresencePenalty = cfg.PresencePenalty
	out.FrequencyPenalty = cfg.FrequencyPenalty
	out.ReasoningEffort = trimStringPtr(cfg.ReasoningEffort)
	out.ThinkingEnabled = cfg.ThinkingEnabled
	out.ThinkingTokens = cfg.ThinkingTokens
	return out
}

func trimStringPtr(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	return &trimmed
}
