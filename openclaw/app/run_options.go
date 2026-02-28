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
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	openClawConfigEnvName = "OPENCLAW_CONFIG"

	sessionBackendInMemory   = "inmemory"
	sessionBackendRedis      = "redis"
	sessionBackendMySQL      = "mysql"
	sessionBackendPostgres   = "postgres"
	sessionBackendClickHouse = "clickhouse"

	memoryBackendInMemory = "inmemory"
	memoryBackendRedis    = "redis"
	memoryBackendMySQL    = "mysql"
	memoryBackendPostgres = "postgres"
	memoryBackendPGVector = "pgvector"

	summaryPolicyAny = "any"
	summaryPolicyAll = "all"

	defaultSessionSummaryEventThreshold = 20
	defaultMemoryAutoMessageThreshold   = 20

	flagAddSessionSummary = "add-session-summary"
	flagMaxHistoryRuns    = "max-history-runs"
	flagPreloadMemory     = "preload-memory"

	flagAgentInstruction       = "agent-instruction"
	flagAgentInstructionFiles  = "agent-instruction-files"
	flagAgentInstructionDir    = "agent-instruction-dir"
	flagAgentSystemPrompt      = "agent-system-prompt"
	flagAgentSystemPromptFiles = "agent-system-prompt-files"
	flagAgentSystemPromptDir   = "agent-system-prompt-dir"
)

type runOptions struct {
	ConfigPath string

	AppName  string
	HTTPAddr string

	AddSessionSummary bool
	MaxHistoryRuns    int
	PreloadMemory     int

	AgentInstruction       string
	AgentInstructionFiles  string
	AgentInstructionDir    string
	AgentSystemPrompt      string
	AgentSystemPromptFiles string
	AgentSystemPromptDir   string

	AgentType string

	ClaudeBin          string
	ClaudeOutputFormat string
	ClaudeExtraArgs    string
	ClaudeEnv          string
	ClaudeWorkDir      string

	ModelMode      string
	OpenAIModel    string
	OpenAIVariant  string
	OpenAIBaseURL  string
	ModelConfig    *yaml.Node
	TelegramToken  string
	SkillsRoot     string
	SkillsExtraDir string
	SkillsDebug    bool
	StateDir       string

	AllowUsers     string
	RequireMention bool
	Mention        string

	TelegramStartFromLatest bool
	TelegramProxy           string
	TelegramHTTPTimeout     time.Duration
	TelegramMaxRetries      int
	TelegramStreaming       string
	TelegramDMPolicy        string
	TelegramGroupPolicy     string
	TelegramAllowThreads    string
	TelegramPairingTTL      time.Duration

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

	SessionSummaryEnabled       bool
	SessionSummaryPolicy        string
	SessionSummaryEventCount    int
	SessionSummaryTokenCount    int
	SessionSummaryIdleThreshold time.Duration
	SessionSummaryMaxWords      int

	EnableLocalExec     bool
	EnableOpenClawTools bool

	ToolProviders []pluginSpec
	ToolSets      []pluginSpec

	RefreshToolSetsOnRun bool
}

func parseRunOptions(args []string) (runOptions, error) {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := runOptions{
		AppName:  appName,
		HTTPAddr: defaultHTTPAddr,

		AgentType: agentTypeLLM,

		ModelMode:     modeOpenAI,
		OpenAIModel:   defaultOpenAIModelName(),
		OpenAIVariant: defaultOpenAIVariant,

		TelegramStartFromLatest: true,
		TelegramMaxRetries:      defaultTelegramMaxRetries,
		TelegramStreaming:       defaultTelegramStreaming,
		TelegramPairingTTL:      time.Hour,

		SessionBackend: sessionBackendInMemory,
		MemoryBackend:  memoryBackendInMemory,

		SessionSummaryPolicy: summaryPolicyAny,

		MemoryAutoPolicy: summaryPolicyAny,
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
	fs.IntVar(
		&opts.MaxHistoryRuns,
		flagMaxHistoryRuns,
		0,
		"Max history messages when add-session-summary=false (0=unlimited)",
	)
	fs.IntVar(
		&opts.PreloadMemory,
		flagPreloadMemory,
		0,
		"Preload N memories into system prompt (0=off, -1=all)",
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
		"OpenAI variant: auto, openai, deepseek, qwen, hunyuan",
	)
	fs.StringVar(
		&opts.OpenAIBaseURL,
		"openai-base-url",
		"",
		"OpenAI base URL override (mode=openai, optional)",
	)
	fs.StringVar(
		&opts.TelegramToken,
		"telegram-token",
		"",
		"Telegram bot token; empty disables Telegram",
	)
	fs.BoolVar(
		&opts.TelegramStartFromLatest,
		"telegram-start-from-latest",
		true,
		"Drain pending updates on first start (no offset)",
	)
	fs.StringVar(
		&opts.TelegramProxy,
		"telegram-proxy",
		"",
		"HTTP proxy URL for Telegram API calls (optional)",
	)
	fs.DurationVar(
		&opts.TelegramHTTPTimeout,
		"telegram-http-timeout",
		0,
		"HTTP client timeout for Telegram API calls (optional)",
	)
	fs.IntVar(
		&opts.TelegramMaxRetries,
		"telegram-max-retries",
		defaultTelegramMaxRetries,
		"Max retries for Telegram API calls (429/5xx/transport errors)",
	)
	fs.StringVar(
		&opts.TelegramStreaming,
		"telegram-streaming",
		defaultTelegramStreaming,
		"Telegram reply streaming: off|block|progress",
	)
	fs.StringVar(
		&opts.TelegramDMPolicy,
		"telegram-dm-policy",
		"",
		"Telegram DM policy: disabled|open|allowlist|pairing",
	)
	fs.StringVar(
		&opts.TelegramGroupPolicy,
		"telegram-group-policy",
		"",
		"Telegram group policy: disabled|open|allowlist",
	)
	fs.StringVar(
		&opts.TelegramAllowThreads,
		"telegram-allow-threads",
		"",
		"Comma-separated allowlist of chat/topic threads",
	)
	fs.DurationVar(
		&opts.TelegramPairingTTL,
		"telegram-pairing-ttl",
		time.Hour,
		"How long pairing codes stay valid",
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
		&opts.StateDir,
		"state-dir",
		"",
		"State dir for offsets and managed skills",
	)
	fs.StringVar(
		&opts.SessionBackend,
		"session-backend",
		sessionBackendInMemory,
		"Session backend: inmemory|redis|mysql|postgres|clickhouse",
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
		"Memory backend: inmemory|redis|mysql|postgres|pgvector",
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
		"Extract when messages exceed N (0 uses default)",
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
		&opts.SessionSummaryPolicy,
		"session-summary-policy",
		summaryPolicyAny,
		"Session summary gating policy: any|all",
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
		"Summarize when time since last event exceeds duration (0 disables)",
	)
	fs.IntVar(
		&opts.SessionSummaryMaxWords,
		"session-summary-max-words",
		0,
		"Max summary words (0 means no limit)",
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
		"Enable OpenClaw-compatible exec/process tools (unsafe)",
	)
	fs.BoolVar(
		&opts.RefreshToolSetsOnRun,
		"refresh-toolsets-on-run",
		false,
		"Refresh ToolSets tool list on each run (optional)",
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

	cfgPath := resolveConfigPath(opts.ConfigPath)
	if cfgPath == "" {
		return opts, nil
	}

	cfg, err := loadConfigFile(cfgPath)
	if err != nil {
		return runOptions{}, &exitError{
			Code: 1,
			Err:  fmt.Errorf("load config failed: %w", err),
		}
	}
	if cfg == nil {
		return opts, nil
	}
	if err := cfg.apply(&opts, setFlags); err != nil {
		return runOptions{}, &exitError{
			Code: 1,
			Err:  fmt.Errorf("apply config failed: %w", err),
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
	return strings.TrimSpace(os.Getenv(openClawConfigEnvName))
}

type fileConfig struct {
	AppName  *string `yaml:"app_name,omitempty"`
	StateDir *string `yaml:"state_dir,omitempty"`

	HTTP     *httpConfig      `yaml:"http,omitempty"`
	Agent    *agentRunConfig  `yaml:"agent,omitempty"`
	Model    *modelConfig     `yaml:"model,omitempty"`
	Gateway  *gatewayConfig   `yaml:"gateway,omitempty"`
	Telegram *telegramConfig  `yaml:"telegram,omitempty"`
	Channels []filePluginSpec `yaml:"channels,omitempty"`
	Skills   *skillsConfig    `yaml:"skills,omitempty"`
	Tools    *toolsConfig     `yaml:"tools,omitempty"`

	Session *sessionConfig `yaml:"session,omitempty"`
	Memory  *memoryConfig  `yaml:"memory,omitempty"`
}

type httpConfig struct {
	Addr *string `yaml:"addr,omitempty"`
}

type agentRunConfig struct {
	Type *string `yaml:"type,omitempty"`

	AddSessionSummary *bool `yaml:"add_session_summary,omitempty"`
	MaxHistoryRuns    *int  `yaml:"max_history_runs,omitempty"`
	PreloadMemory     *int  `yaml:"preload_memory,omitempty"`

	Instruction      *string  `yaml:"instruction,omitempty"`
	InstructionFiles []string `yaml:"instruction_files,omitempty"`
	InstructionDir   *string  `yaml:"instruction_dir,omitempty"`

	SystemPrompt      *string  `yaml:"system_prompt,omitempty"`
	SystemPromptFiles []string `yaml:"system_prompt_files,omitempty"`
	SystemPromptDir   *string  `yaml:"system_prompt_dir,omitempty"`

	ClaudeBin          *string  `yaml:"claude_bin,omitempty"`
	ClaudeOutputFormat *string  `yaml:"claude_output_format,omitempty"`
	ClaudeExtraArgs    []string `yaml:"claude_extra_args,omitempty"`
	ClaudeEnv          []string `yaml:"claude_env,omitempty"`
	ClaudeWorkDir      *string  `yaml:"claude_work_dir,omitempty"`
}

type modelConfig struct {
	Mode          *string      `yaml:"mode,omitempty"`
	Name          *string      `yaml:"name,omitempty"`
	BaseURL       *string      `yaml:"base_url,omitempty"`
	OpenAIVariant *string      `yaml:"openai_variant,omitempty"`
	Config        *rawYAMLNode `yaml:"config,omitempty"`
}

type gatewayConfig struct {
	AllowUsers      []string `yaml:"allow_users,omitempty"`
	RequireMention  *bool    `yaml:"require_mention,omitempty"`
	MentionPatterns []string `yaml:"mention_patterns,omitempty"`
}

type telegramConfig struct {
	Token           *string  `yaml:"token,omitempty"`
	StartFromLatest *bool    `yaml:"start_from_latest,omitempty"`
	Proxy           *string  `yaml:"proxy,omitempty"`
	HTTPTimeout     *string  `yaml:"http_timeout,omitempty"`
	MaxRetries      *int     `yaml:"max_retries,omitempty"`
	Streaming       *string  `yaml:"streaming,omitempty"`
	DMPolicy        *string  `yaml:"dm_policy,omitempty"`
	GroupPolicy     *string  `yaml:"group_policy,omitempty"`
	AllowThreads    []string `yaml:"allow_threads,omitempty"`
	PairingTTL      *string  `yaml:"pairing_ttl,omitempty"`
}

type skillsConfig struct {
	Root      *string  `yaml:"root,omitempty"`
	ExtraDirs []string `yaml:"extra_dirs,omitempty"`
	Debug     *bool    `yaml:"debug,omitempty"`
}

type toolsConfig struct {
	EnableLocalExec      *bool `yaml:"enable_local_exec,omitempty"`
	EnableOpenClawTools  *bool `yaml:"enable_openclaw_tools,omitempty"`
	RefreshToolSetsOnRun *bool `yaml:"refresh_toolsets_on_run,omitempty"`

	Providers []filePluginSpec `yaml:"providers,omitempty"`
	ToolSets  []filePluginSpec `yaml:"toolsets,omitempty"`
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
	Enabled        *bool   `yaml:"enabled,omitempty"`
	Policy         *string `yaml:"policy,omitempty"`
	EventThreshold *int    `yaml:"event_threshold,omitempty"`
	TokenThreshold *int    `yaml:"token_threshold,omitempty"`
	IdleThreshold  *string `yaml:"idle_threshold,omitempty"`
	MaxWords       *int    `yaml:"max_words,omitempty"`
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

	if cfg.HTTP != nil && cfg.HTTP.Addr != nil && !flagWasSet(set, "http-addr") {
		opts.HTTPAddr = strings.TrimSpace(*cfg.HTTP.Addr)
	}

	if cfg.Agent != nil {
		if cfg.Agent.Type != nil && !flagWasSet(set, "agent-type") {
			opts.AgentType = strings.TrimSpace(*cfg.Agent.Type)
		}
		if cfg.Agent.AddSessionSummary != nil &&
			!flagWasSet(set, flagAddSessionSummary) {
			opts.AddSessionSummary = *cfg.Agent.AddSessionSummary
		}
		if cfg.Agent.MaxHistoryRuns != nil &&
			!flagWasSet(set, flagMaxHistoryRuns) {
			opts.MaxHistoryRuns = *cfg.Agent.MaxHistoryRuns
		}
		if cfg.Agent.PreloadMemory != nil &&
			!flagWasSet(set, flagPreloadMemory) {
			opts.PreloadMemory = *cfg.Agent.PreloadMemory
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
		if cfg.Model.Config != nil {
			opts.ModelConfig = cfg.Model.Config.Node
		}
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

	if cfg.Telegram != nil {
		if cfg.Telegram.Token != nil &&
			!flagWasSet(set, "telegram-token") {
			opts.TelegramToken = strings.TrimSpace(
				*cfg.Telegram.Token,
			)
		}
		if cfg.Telegram.StartFromLatest != nil &&
			!flagWasSet(set, "telegram-start-from-latest") {
			opts.TelegramStartFromLatest = *cfg.Telegram.StartFromLatest
		}
		if cfg.Telegram.Proxy != nil &&
			!flagWasSet(set, "telegram-proxy") {
			opts.TelegramProxy = strings.TrimSpace(
				*cfg.Telegram.Proxy,
			)
		}
		if cfg.Telegram.HTTPTimeout != nil &&
			!flagWasSet(set, "telegram-http-timeout") {
			dur, err := parseDuration(*cfg.Telegram.HTTPTimeout)
			if err != nil {
				return fmt.Errorf("telegram.http_timeout: %w", err)
			}
			opts.TelegramHTTPTimeout = dur
		}
		if cfg.Telegram.MaxRetries != nil &&
			!flagWasSet(set, "telegram-max-retries") {
			opts.TelegramMaxRetries = *cfg.Telegram.MaxRetries
		}
		if cfg.Telegram.Streaming != nil &&
			!flagWasSet(set, "telegram-streaming") {
			opts.TelegramStreaming = strings.TrimSpace(
				*cfg.Telegram.Streaming,
			)
		}
		if cfg.Telegram.DMPolicy != nil &&
			!flagWasSet(set, "telegram-dm-policy") {
			opts.TelegramDMPolicy = strings.TrimSpace(
				*cfg.Telegram.DMPolicy,
			)
		}
		if cfg.Telegram.GroupPolicy != nil &&
			!flagWasSet(set, "telegram-group-policy") {
			opts.TelegramGroupPolicy = strings.TrimSpace(
				*cfg.Telegram.GroupPolicy,
			)
		}
		if len(cfg.Telegram.AllowThreads) > 0 &&
			!flagWasSet(set, "telegram-allow-threads") {
			opts.TelegramAllowThreads = strings.Join(
				cfg.Telegram.AllowThreads,
				csvDelimiter,
			)
		}
		if cfg.Telegram.PairingTTL != nil &&
			!flagWasSet(set, "telegram-pairing-ttl") {
			dur, err := parseDuration(*cfg.Telegram.PairingTTL)
			if err != nil {
				return fmt.Errorf("telegram.pairing_ttl: %w", err)
			}
			opts.TelegramPairingTTL = dur
		}
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
	}

	if cfg.Tools != nil {
		if cfg.Tools.EnableLocalExec != nil &&
			!flagWasSet(set, "enable-local-exec") {
			opts.EnableLocalExec = *cfg.Tools.EnableLocalExec
		}
		if cfg.Tools.EnableOpenClawTools != nil &&
			!flagWasSet(set, "enable-openclaw-tools") {
			opts.EnableOpenClawTools = *cfg.Tools.EnableOpenClawTools
		}
		if cfg.Tools.RefreshToolSetsOnRun != nil &&
			!flagWasSet(set, "refresh-toolsets-on-run") {
			opts.RefreshToolSetsOnRun = *cfg.Tools.RefreshToolSetsOnRun
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

func flagWasSet(set map[string]struct{}, name string) bool {
	_, ok := set[name]
	return ok
}
