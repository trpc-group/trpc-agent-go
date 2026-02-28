//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseRunOptions_UsesEnvConfig(t *testing.T) {
	cfgPath := writeTempConfig(t, `
app_name: demo
http:
  addr: ":9999"
gateway:
  allow_users: ["u1","u2"]
`)
	t.Setenv(openClawConfigEnvName, cfgPath)

	opts, err := parseRunOptions(nil)
	require.NoError(t, err)
	require.Equal(t, "demo", opts.AppName)
	require.Equal(t, ":9999", opts.HTTPAddr)
	require.Equal(t, "u1,u2", opts.AllowUsers)
}

func TestParseRunOptions_FlagOverridesConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
http:
  addr: ":9999"

agent:
  type: "claude-code"
  instruction: "cfg instruction"
  instruction_files: ["cfg_1.md","cfg_2.md"]
  instruction_dir: "cfg_instr_dir"
  system_prompt: "cfg system"
  system_prompt_files: ["cfg_sys_1.md","cfg_sys_2.md"]
  system_prompt_dir: "cfg_sys_dir"
  claude_bin: "/bin/claude"
  claude_output_format: "stream-json"
  claude_extra_args: ["--permission-mode","bypassPermissions"]
  claude_env: ["A=B"]
  claude_work_dir: "/tmp/work"
  add_session_summary: false
  max_history_runs: 123
  preload_memory: 2
`)

	opts, err := parseRunOptions([]string{
		"-config", cfgPath,
		"-http-addr", ":7777",
		"-agent-type", agentTypeLLM,
		"-agent-instruction", "flag instruction",
		"-agent-system-prompt", "flag system",
		"-claude-bin", "/tmp/claude",
		"-add-session-summary",
		"-max-history-runs", "9",
		"-preload-memory", "-1",
	})
	require.NoError(t, err)
	require.Equal(t, ":7777", opts.HTTPAddr)
	require.Equal(t, agentTypeLLM, opts.AgentType)
	require.Equal(t, "flag instruction", opts.AgentInstruction)
	require.Equal(t, "flag system", opts.AgentSystemPrompt)
	require.Equal(t, "/tmp/claude", opts.ClaudeBin)
	require.True(t, opts.AddSessionSummary)
	require.Equal(t, 9, opts.MaxHistoryRuns)
	require.Equal(t, -1, opts.PreloadMemory)
}

func TestParseRunOptions_UnknownFieldFails(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
unknown: 1
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestParseRunOptions_InvalidDurationFails(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
telegram:
  http_timeout: "bad"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestParseRunOptions_ConfigAppliesAllSections(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
app_name: "demo"
state_dir: "/tmp/state"

http:
  addr: ":9000"

agent:
  type: "claude-code"
  claude_bin: "/bin/claude"
  claude_output_format: "stream-json"
  claude_extra_args: ["--permission-mode","bypassPermissions"]
  claude_env: ["FOO=bar","X=1"]
  claude_work_dir: "/tmp/work"
  add_session_summary: true
  max_history_runs: 50
  preload_memory: 10
  instruction: "instruction"
  instruction_files: ["i1.md","i2.md"]
  instruction_dir: "/instruction_dir"
  system_prompt: "system prompt"
  system_prompt_files: ["s1.md","s2.md"]
  system_prompt_dir: "/system_prompt_dir"

model:
  mode: "mock"
  name: "gpt-5"
  openai_variant: "openai"

gateway:
  allow_users: ["u1","u2"]
  require_mention: true
  mention_patterns: ["@bot"]

telegram:
  token: "t"
  start_from_latest: false
  proxy: "http://127.0.0.1:7890"
  http_timeout: "60s"
  max_retries: 5
  streaming: "block"
  dm_policy: "open"
  group_policy: "allowlist"
  allow_threads: ["1","2:topic:3"]
  pairing_ttl: "30m"

skills:
  root: "/skills"
  extra_dirs: ["/extra1","/extra2"]
  debug: true

tools:
  enable_local_exec: true
  enable_openclaw_tools: true
  refresh_toolsets_on_run: true
  providers:
    - type: "duckduckgo"
      name: "ddg"
      config:
        base_url: "https://api.duckduckgo.com"
  toolsets:
    - type: "mcp"
      name: "test_mcp"
      config:
        transport: "stdio"
        command: "echo"
        args: ["hello"]

session:
  backend: "redis"
  redis:
    url: "redis://127.0.0.1:6379/0"
    instance: "r1"
    key_prefix: "sp"
  summary:
    enabled: true
    policy: "all"
    event_threshold: 10
    token_threshold: 100
    idle_threshold: "5m"
    max_words: 200
  config:
    dsn: "mysql://example"

memory:
  backend: "redis"
  redis:
    url: "redis://127.0.0.1:6379/0"
    instance: "r2"
    key_prefix: "mp"
  limit: 123
  auto:
    enabled: true
    policy: "all"
    message_threshold: 7
    time_interval: "10m"
  config:
    dsn: "postgres://example"
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)

	require.Equal(t, "demo", opts.AppName)
	require.Equal(t, "/tmp/state", opts.StateDir)
	require.Equal(t, ":9000", opts.HTTPAddr)

	require.Equal(t, agentTypeClaudeCode, opts.AgentType)
	require.Equal(t, "/bin/claude", opts.ClaudeBin)
	require.Equal(t, "stream-json", opts.ClaudeOutputFormat)
	require.Equal(
		t,
		"--permission-mode,bypassPermissions",
		opts.ClaudeExtraArgs,
	)
	require.Equal(t, "FOO=bar,X=1", opts.ClaudeEnv)
	require.Equal(t, "/tmp/work", opts.ClaudeWorkDir)

	require.True(t, opts.AddSessionSummary)
	require.Equal(t, 50, opts.MaxHistoryRuns)
	require.Equal(t, 10, opts.PreloadMemory)
	require.Equal(t, "instruction", opts.AgentInstruction)
	require.Equal(t, "i1.md,i2.md", opts.AgentInstructionFiles)
	require.Equal(t, "/instruction_dir", opts.AgentInstructionDir)
	require.Equal(t, "system prompt", opts.AgentSystemPrompt)
	require.Equal(t, "s1.md,s2.md", opts.AgentSystemPromptFiles)
	require.Equal(t, "/system_prompt_dir", opts.AgentSystemPromptDir)

	require.Equal(t, modeMock, opts.ModelMode)
	require.Equal(t, "gpt-5", opts.OpenAIModel)
	require.Equal(t, "openai", opts.OpenAIVariant)

	require.Equal(t, "u1,u2", opts.AllowUsers)
	require.True(t, opts.RequireMention)
	require.Equal(t, "@bot", opts.Mention)

	require.Equal(t, "t", opts.TelegramToken)
	require.False(t, opts.TelegramStartFromLatest)
	require.Equal(t, "http://127.0.0.1:7890", opts.TelegramProxy)
	require.Equal(t, 60*time.Second, opts.TelegramHTTPTimeout)
	require.Equal(t, 5, opts.TelegramMaxRetries)
	require.Equal(t, "block", opts.TelegramStreaming)
	require.Equal(t, "open", opts.TelegramDMPolicy)
	require.Equal(t, "allowlist", opts.TelegramGroupPolicy)
	require.Equal(t, "1,2:topic:3", opts.TelegramAllowThreads)
	require.Equal(t, 30*time.Minute, opts.TelegramPairingTTL)

	require.Equal(t, "/skills", opts.SkillsRoot)
	require.Equal(t, "/extra1,/extra2", opts.SkillsExtraDir)
	require.True(t, opts.SkillsDebug)

	require.True(t, opts.EnableLocalExec)
	require.True(t, opts.EnableOpenClawTools)
	require.True(t, opts.RefreshToolSetsOnRun)

	require.Len(t, opts.ToolProviders, 1)
	require.Equal(t, "duckduckgo", opts.ToolProviders[0].Type)
	require.Equal(t, "ddg", opts.ToolProviders[0].Name)
	require.NotNil(t, opts.ToolProviders[0].Config)

	require.Len(t, opts.ToolSets, 1)
	require.Equal(t, "mcp", opts.ToolSets[0].Type)
	require.Equal(t, "test_mcp", opts.ToolSets[0].Name)
	require.NotNil(t, opts.ToolSets[0].Config)

	require.Equal(t, "redis", opts.SessionBackend)
	require.Equal(t, "redis://127.0.0.1:6379/0", opts.SessionRedisURL)
	require.Equal(t, "r1", opts.SessionRedisInstance)
	require.Equal(t, "sp", opts.SessionRedisKeyPref)
	require.NotNil(t, opts.SessionConfig)

	require.True(t, opts.SessionSummaryEnabled)
	require.Equal(t, "all", opts.SessionSummaryPolicy)
	require.Equal(t, 10, opts.SessionSummaryEventCount)
	require.Equal(t, 100, opts.SessionSummaryTokenCount)
	require.Equal(t, 5*time.Minute, opts.SessionSummaryIdleThreshold)
	require.Equal(t, 200, opts.SessionSummaryMaxWords)

	require.Equal(t, "redis", opts.MemoryBackend)
	require.Equal(t, "redis://127.0.0.1:6379/0", opts.MemoryRedisURL)
	require.Equal(t, "r2", opts.MemoryRedisInstance)
	require.Equal(t, "mp", opts.MemoryRedisKeyPref)
	require.Equal(t, 123, opts.MemoryLimit)
	require.NotNil(t, opts.MemoryConfig)

	require.True(t, opts.MemoryAutoEnabled)
	require.Equal(t, "all", opts.MemoryAutoPolicy)
	require.Equal(t, 7, opts.MemoryAutoMessageThreshold)
	require.Equal(t, 10*time.Minute, opts.MemoryAutoTimeInterval)
}

func TestParseRunOptions_UnexpectedArgsFails(t *testing.T) {
	t.Parallel()

	_, err := parseRunOptions([]string{"pairing", "list"})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 2, exitErr.Code)
}

func TestParseRunOptions_MultipleYAMLDocsFails(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
app_name: "demo"
---
app_name: "second"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestParseRunOptions_SummaryIdleThresholdInvalidFails(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
session:
  summary:
    enabled: true
    idle_threshold: "bad"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}
