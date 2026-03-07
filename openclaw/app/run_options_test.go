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

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
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
  ralph_loop:
    enabled: false
    max_iterations: 99
    completion_promise: "cfg promise"
    promise_tag_open: "<cfg>"
    promise_tag_close: "</cfg>"
    verify:
      command: "cfg cmd"
      work_dir: "/cfg/work"
      timeout: "10s"
      env: ["A=B"]
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
		"-agent-ralph-loop",
		"-agent-ralph-max-iterations", "7",
		"-agent-ralph-completion-promise", "flag promise",
		"-agent-ralph-promise-tag-open", "<flag>",
		"-agent-ralph-promise-tag-close", "</flag>",
		"-agent-ralph-verify-command", "flag cmd",
		"-agent-ralph-verify-workdir", "/tmp/flag",
		"-agent-ralph-verify-timeout", "30s",
		"-agent-ralph-verify-env", "X=1",
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
	require.True(t, opts.RalphLoopEnabled)
	require.Equal(t, 7, opts.RalphLoopMaxIterations)
	require.Equal(t, "flag promise", opts.RalphLoopCompletionPromise)
	require.Equal(t, "<flag>", opts.RalphLoopPromiseTagOpen)
	require.Equal(t, "</flag>", opts.RalphLoopPromiseTagClose)
	require.Equal(t, "flag cmd", opts.RalphLoopVerifyCommand)
	require.Equal(t, "/tmp/flag", opts.RalphLoopVerifyWorkDir)
	require.Equal(t, 30*time.Second, opts.RalphLoopVerifyTimeout)
	require.Equal(t, "X=1", opts.RalphLoopVerifyEnv)
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
memory:
  auto:
    enabled: true
    time_interval: "bad"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestParseRunOptions_RalphLoopInvalidDurationFails(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
agent:
  ralph_loop:
    enabled: true
    verify:
      command: "echo ok"
      timeout: "bad"
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
  ralph_loop:
    enabled: true
    max_iterations: 5
    completion_promise: "done"
    promise_tag_open: "<p>"
    promise_tag_close: "</p>"
    verify:
      command: "echo ok"
      work_dir: "/tmp"
      timeout: "90s"
      env: ["A=B"]

model:
  mode: "mock"
  name: "gpt-5"
  openai_variant: "openai"

gateway:
  allow_users: ["u1","u2"]
  require_mention: true
  mention_patterns: ["@bot"]

channels:
  - type: "telegram"
    config:
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
  allowBundled: ["gh-issues","notion"]
  entries:
    gh-issues:
      enabled: false
      apiKey: "k1"
      env:
        GH_TOKEN: "t1"
    notion:
      enabled: true
      api_key: "k2"
      env:
        NOTION_API_KEY: "t2"

tools:
  enable_local_exec: true
  enable_openclaw_tools: true
  enable_parallel_tools: true
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
	require.True(t, opts.RalphLoopEnabled)
	require.Equal(t, 5, opts.RalphLoopMaxIterations)
	require.Equal(t, "done", opts.RalphLoopCompletionPromise)
	require.Equal(t, "<p>", opts.RalphLoopPromiseTagOpen)
	require.Equal(t, "</p>", opts.RalphLoopPromiseTagClose)
	require.Equal(t, "echo ok", opts.RalphLoopVerifyCommand)
	require.Equal(t, "/tmp", opts.RalphLoopVerifyWorkDir)
	require.Equal(t, 90*time.Second, opts.RalphLoopVerifyTimeout)
	require.Equal(t, "A=B", opts.RalphLoopVerifyEnv)

	require.Equal(t, modeMock, opts.ModelMode)
	require.Equal(t, "gpt-5", opts.OpenAIModel)
	require.Equal(t, "openai", opts.OpenAIVariant)

	require.Equal(t, "u1,u2", opts.AllowUsers)
	require.True(t, opts.RequireMention)
	require.Equal(t, "@bot", opts.Mention)

	require.Len(t, opts.Channels, 1)
	require.Equal(t, telegramChannelType, opts.Channels[0].Type)
	require.Equal(t, "", opts.Channels[0].Name)
	require.NotNil(t, opts.Channels[0].Config)

	var tgCfg telegramChannelConfig
	require.NoError(t, registry.DecodeStrict(opts.Channels[0].Config, &tgCfg))
	require.Equal(t, "t", tgCfg.Token)
	require.NotNil(t, tgCfg.StartFromLatest)
	require.False(t, *tgCfg.StartFromLatest)
	require.Equal(t, "http://127.0.0.1:7890", tgCfg.Proxy)
	require.Equal(t, "60s", tgCfg.HTTPTimeout)
	require.NotNil(t, tgCfg.MaxRetries)
	require.Equal(t, 5, *tgCfg.MaxRetries)
	require.Equal(t, "block", tgCfg.Streaming)
	require.Equal(t, "open", tgCfg.DMPolicy)
	require.Equal(t, "allowlist", tgCfg.GroupPolicy)
	require.Equal(t, []string{"1", "2:topic:3"}, tgCfg.AllowThreads)
	require.Equal(t, "30m", tgCfg.PairingTTL)

	require.Equal(t, "/skills", opts.SkillsRoot)
	require.Equal(t, "/extra1,/extra2", opts.SkillsExtraDir)
	require.True(t, opts.SkillsDebug)
	require.Equal(t, "gh-issues,notion", opts.SkillsAllowBundled)

	require.Len(t, opts.SkillConfigs, 2)
	require.NotNil(t, opts.SkillConfigs["gh-issues"].Enabled)
	require.False(t, *opts.SkillConfigs["gh-issues"].Enabled)
	require.Equal(t, "k1", opts.SkillConfigs["gh-issues"].APIKey)
	require.Equal(t, "t1", opts.SkillConfigs["gh-issues"].Env["GH_TOKEN"])

	require.NotNil(t, opts.SkillConfigs["notion"].Enabled)
	require.True(t, *opts.SkillConfigs["notion"].Enabled)
	require.Equal(t, "k2", opts.SkillConfigs["notion"].APIKey)
	require.Equal(
		t,
		"t2",
		opts.SkillConfigs["notion"].Env["NOTION_API_KEY"],
	)

	require.True(t, opts.EnableLocalExec)
	require.True(t, opts.EnableOpenClawTools)
	require.True(t, opts.EnableParallelTools)
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

func TestConvertSkillConfigs_EmptyAndBlankKeys(t *testing.T) {
	require.Nil(t, convertSkillConfigs(nil))
	require.Nil(t, convertSkillConfigs(map[string]skillEntryConfig{}))

	got := convertSkillConfigs(map[string]skillEntryConfig{
		" ": {},
	})
	require.Nil(t, got)
}

func TestParseRunOptions_AllowBundledSnakeCase(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
skills:
  allow_bundled: ["a","b"]
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.Equal(t, "a,b", opts.SkillsAllowBundled)
}

func TestParseRunOptions_SkillsAllowBundledFlagOverridesConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
skills:
  allow_bundled: ["a"]
`)

	opts, err := parseRunOptions([]string{
		"-config", cfgPath,
		"-skills-allow-bundled", "b,c",
	})
	require.NoError(t, err)
	require.Equal(t, "b,c", opts.SkillsAllowBundled)
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}
