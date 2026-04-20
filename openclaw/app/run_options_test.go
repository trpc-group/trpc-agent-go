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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "openclaw-test-home-*")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Unsetenv(openClawConfigEnvName)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestParseRunOptions_UsesEnvConfig(t *testing.T) {
	cfgPath := writeTempConfig(t, `
app_name: demo
http:
  addr: ":9999"
a2a:
  enabled: true
  host: "http://127.0.0.1:8080/a2a"
  streaming: false
gateway:
  allow_users: ["u1","u2"]
`)
	t.Setenv(openClawConfigEnvName, cfgPath)

	opts, err := parseRunOptions(nil)
	require.NoError(t, err)
	require.Equal(t, "demo", opts.AppName)
	require.Equal(t, ":9999", opts.HTTPAddr)
	require.True(t, opts.A2AEnabled)
	require.Equal(t, "http://127.0.0.1:8080/a2a", opts.A2AHost)
	require.False(t, opts.A2AStreaming)
	require.Equal(t, "u1,u2", opts.AllowUsers)
	require.Equal(t, cfgPath, opts.ConfigPath)
}

func TestParseRunOptions_A2AConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
a2a:
  enabled: true
  host: " 127.0.0.1:8080/a2a/ "
  user_id_header: " X-Caller-User "
  streaming: false
  advertise_tools: true
  name: " openclaw-sandbox "
  description: " sandbox subagent "
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.True(t, opts.A2AEnabled)
	require.Equal(t, "http://127.0.0.1:8080/a2a/", opts.A2AHost)
	require.Equal(t, "X-Caller-User", opts.A2AUserIDHeader)
	require.False(t, opts.A2AStreaming)
	require.True(t, opts.A2AAdvertiseTools)
	require.Equal(t, "openclaw-sandbox", opts.A2AName)
	require.Equal(t, "sandbox subagent", opts.A2ADescription)
}

func TestParseRunOptions_MemoryBackendFileFromConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(
		t,
		"memory:\n"+
			"  backend: file\n"+
			"  auto:\n"+
			"    enabled: true\n",
	)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.Equal(t, memoryBackendFile, opts.MemoryBackend)
	require.True(t, opts.MemoryAutoEnabled)
}

func TestParseRunOptions_A2AFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseRunOptions([]string{
		"-a2a",
		"-a2a-host", " 127.0.0.1:8080/a2a/ ",
		"-a2a-user-id-header", " X-Caller-User ",
		"-a2a-streaming=false",
		"-a2a-advertise-tools=true",
		"-a2a-name", " openclaw-sandbox ",
		"-a2a-description", " sandbox subagent ",
	})
	require.NoError(t, err)
	require.True(t, opts.A2AEnabled)
	require.Equal(t, "http://127.0.0.1:8080/a2a/", opts.A2AHost)
	require.Equal(t, "X-Caller-User", opts.A2AUserIDHeader)
	require.False(t, opts.A2AStreaming)
	require.True(t, opts.A2AAdvertiseTools)
	require.Equal(t, "openclaw-sandbox", opts.A2AName)
	require.Equal(t, "sandbox subagent", opts.A2ADescription)
}

func TestParseRunOptions_A2AFlagsOverrideConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
a2a:
  enabled: true
  host: "http://127.0.0.1:8080/a2a"
  user_id_header: "X-Config-User"
  streaming: false
  advertise_tools: true
  name: "config-name"
  description: "config-description"
`)

	opts, err := parseRunOptions([]string{
		"-config", cfgPath,
		"-a2a=false",
		"-a2a-host", "http://127.0.0.1:9090/subagent",
		"-a2a-user-id-header", "X-Flag-User",
		"-a2a-streaming=true",
		"-a2a-advertise-tools=false",
		"-a2a-name", "flag-name",
		"-a2a-description", "flag-description",
	})
	require.NoError(t, err)
	require.False(t, opts.A2AEnabled)
	require.Equal(t, "http://127.0.0.1:9090/subagent", opts.A2AHost)
	require.Equal(t, "X-Flag-User", opts.A2AUserIDHeader)
	require.True(t, opts.A2AStreaming)
	require.False(t, opts.A2AAdvertiseTools)
	require.Equal(t, "flag-name", opts.A2AName)
	require.Equal(t, "flag-description", opts.A2ADescription)
}

func TestParseRunOptions_LangfuseConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
observability:
  langfuse:
    enabled: true
    required: true
    ui_base_url: " http://127.0.0.1:3000/ "
    trace_url_template: " http://127.0.0.1:3000/project/local-dev/traces/{{trace_id}} "
    observation_leaf_value_max_bytes: 4096
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.True(t, opts.LangfuseEnabled)
	require.True(t, opts.LangfuseRequired)
	require.Equal(t, "http://127.0.0.1:3000", opts.LangfuseUIBaseURL)
	require.Equal(
		t,
		"http://127.0.0.1:3000/project/local-dev/traces/{{trace_id}}",
		opts.LangfuseTraceURLTemplate,
	)
	require.NotNil(t, opts.LangfuseObservationLeafValueMaxBytes)
	require.Equal(
		t,
		4096,
		*opts.LangfuseObservationLeafValueMaxBytes,
	)
}

func TestParseRunOptions_UsesDefaultConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfgPath := filepath.Join(
		home,
		defaultConfigRootDir,
		defaultConfigAppDir,
		defaultConfigFile,
	)
	err := os.MkdirAll(filepath.Dir(cfgPath), 0o755)
	require.NoError(t, err)
	err = os.WriteFile(cfgPath, []byte("app_name: demo\n"), 0o644)
	require.NoError(t, err)

	opts, err := parseRunOptions(nil)
	require.NoError(t, err)
	require.Equal(t, "demo", opts.AppName)
	require.Equal(t, cfgPath, opts.ConfigPath)
}

func TestApplyOpenClawToolDefaults_LLMEnablesTools(t *testing.T) {
	t.Parallel()

	opts := runOptions{}
	applyOpenClawToolDefaults(agentTypeLLM, &opts)
	require.True(t, opts.EnableOpenClawTools)
}

func TestApplyOpenClawToolDefaults_RespectsExplicitFalse(t *testing.T) {
	t.Parallel()

	opts := runOptions{
		enableOpenClawToolsExplicit: true,
	}
	applyOpenClawToolDefaults(agentTypeLLM, &opts)
	require.False(t, opts.EnableOpenClawTools)
}

func TestApplyOpenClawToolDefaults_ClaudeCodeStaysOff(t *testing.T) {
	t.Parallel()

	opts := runOptions{}
	applyOpenClawToolDefaults(agentTypeClaudeCode, &opts)
	require.False(t, opts.EnableOpenClawTools)
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
  enable_context_compaction: false
  context_compaction_oversized_tool_result_max_tokens: 2048
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
		"-enable-context-compaction",
		"-context-compaction-oversized-tool-result-max-tokens", "256",
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
	require.True(t, opts.EnableContextCompaction)
	require.Equal(t, 256, opts.ContextCompactionOversizedToolResultMaxTokens)
	require.Equal(t, 9, opts.MaxHistoryRuns)
	require.Equal(t, -1, opts.PreloadMemory)
}

func TestParseRunOptions_ModelGenerationConfig_DefaultsStreamTrue(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
model:
  mode: "mock"
  generation_config:
    max_tokens: 256
    temperature: 0.1
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.NotNil(t, opts.GenerationConfig)
	require.True(t, opts.GenerationConfig.Stream)
	require.Equal(t, intPtrValue(256), opts.GenerationConfig.MaxTokens)
	require.Equal(
		t,
		float64PtrValue(0.1),
		opts.GenerationConfig.Temperature,
	)
}

func TestParseRunOptions_ModelGenerationConfig_PreservesFalseStream(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
model:
  mode: "mock"
  generation_config:
    stream: false
    stop:
      - "DONE"
    reasoning_effort: " medium "
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.NotNil(t, opts.GenerationConfig)
	require.False(t, opts.GenerationConfig.Stream)
	require.Equal(t, []string{"DONE"}, opts.GenerationConfig.Stop)
	require.Equal(
		t,
		stringPtrValue("medium"),
		opts.GenerationConfig.ReasoningEffort,
	)
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

func TestLoadConfigFile_ExpandsEnvPlaceholders(t *testing.T) {
	t.Setenv("TEST_OPENCLAW_APP", "demo")

	cfgPath := writeTempConfig(t, `
app_name: ${TEST_OPENCLAW_APP}
`)

	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.AppName)
	require.Equal(t, "demo", *cfg.AppName)
}

func TestLoadConfigFile_MissingEnvPlaceholderFails(t *testing.T) {
	cfgPath := writeTempConfig(t, `
app_name: ${TEST_OPENCLAW_MISSING}
`)

	_, err := loadConfigFile(cfgPath)
	require.Error(t, err)
	require.Contains(t, err.Error(), "TEST_OPENCLAW_MISSING")
}

func TestExpandEnvPlaceholders_TrimsName(t *testing.T) {
	t.Setenv("OPENCLAW_ENV_TEST", "demo")

	in := []byte("app_name: ${ OPENCLAW_ENV_TEST }\n")
	out, err := expandEnvPlaceholders(in)
	require.NoError(t, err)
	require.Equal(t, "app_name: demo\n", string(out))
}

func TestExpandEnvPlaceholders_InvalidNameIsPreserved(t *testing.T) {
	in := []byte("app_name: ${1BAD}\n")
	out, err := expandEnvPlaceholders(in)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestExpandEnvPlaceholders_MissingBraceIsPreserved(t *testing.T) {
	in := []byte("app_name: ${OPENCLAW_ENV_TEST\n")
	out, err := expandEnvPlaceholders(in)
	require.NoError(t, err)
	require.Equal(t, in, out)
}

func TestResolveGenerationConfigYAML_DefaultsStreamTrue(t *testing.T) {
	t.Parallel()

	got := resolveGenerationConfigYAML(&generationConfigYAML{})
	require.NotNil(t, got)
	require.True(t, got.Stream)
}

func TestResolveGenerationConfigYAML_ExplicitFalseWins(t *testing.T) {
	t.Parallel()

	stream := false
	got := resolveGenerationConfigYAML(&generationConfigYAML{
		Stream: &stream,
	})
	require.NotNil(t, got)
	require.False(t, got.Stream)
}

func intPtrValue(v int) *int {
	return &v
}

func float64PtrValue(v float64) *float64 {
	return &v
}

func stringPtrValue(v string) *string {
	return &v
}

func TestExpandEnvPlaceholders_ReplacesMultiple(t *testing.T) {
	t.Setenv("OPENCLAW_ENV_A", "A")
	t.Setenv("OPENCLAW_ENV_B", "B")

	in := []byte("a: ${OPENCLAW_ENV_A} b: ${OPENCLAW_ENV_B}\n")
	out, err := expandEnvPlaceholders(in)
	require.NoError(t, err)
	require.Equal(t, "a: A b: B\n", string(out))
}

func TestIsValidEnvName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{in: "", want: false},
		{in: "A", want: true},
		{in: "_A", want: true},
		{in: "A1", want: true},
		{in: "1A", want: false},
		{in: "A-B", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isValidEnvName(tc.in))
		})
	}
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
  enable_context_compaction: true
  context_compaction_oversized_tool_result_max_tokens: 4096
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
  watch: false
  watch_bundled: true
  watch_debounce_ms: 125
  tool_profile: "knowledge_only"
  load_mode: "session"
  max_loaded_skills: 3
  loaded_content_in_tool_results: false
  skip_fallback_on_session_summary: false
  tooling_guidance: "Prefer runtime help over stale docs."
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
  openclaw_tooling_guidance: ""
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
    mode: "auto"
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
	require.True(t, opts.EnableContextCompaction)
	require.Equal(t, 4096, opts.ContextCompactionOversizedToolResultMaxTokens)
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
	require.False(t, opts.SkillsWatch)
	require.True(t, opts.SkillsWatchBundled)
	require.Equal(t, 125*time.Millisecond, opts.SkillsWatchDebounce)
	require.Equal(
		t,
		skillprofile.KnowledgeOnly,
		opts.SkillsToolProfile,
	)
	require.Equal(t, "session", opts.SkillsLoadMode)
	require.Equal(t, 3, opts.SkillsMaxLoaded)
	require.False(t, opts.SkillsToolResults)
	require.False(t, opts.SkillsSkipFallback)
	require.NotNil(t, opts.SkillsToolingGuide)
	require.Equal(
		t,
		"Prefer runtime help over stale docs.",
		*opts.SkillsToolingGuide,
	)

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
	require.NotNil(t, opts.OpenClawToolingGuide)
	require.Equal(t, "", *opts.OpenClawToolingGuide)
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
	require.Equal(t, "auto", opts.SessionSummaryMode)
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

func TestParseRunOptions_KnowledgesEntriesConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
knowledges:
  entries:
    - name: "docs"
      embedder:
        type: "openai"
        model: "text-embedding-3-small"
      vector_store:
        type: "inmemory"
        max_results: 5
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.Len(t, opts.KnowledgesConfig, 1)
	require.Contains(t, opts.KnowledgesConfig, "docs")
	require.NotNil(t, opts.KnowledgesConfig["docs"])
}

func TestParseRunOptions_KnowledgesEntriesRequireName(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
knowledges:
  entries:
    - vector_store:
        type: "inmemory"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)
	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.Contains(t, err.Error(), "knowledges.entries[0].name is empty")
}

func TestParseRunOptions_KnowledgesEntriesRejectDuplicateNames(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
knowledges:
  entries:
    - name: "docs"
      vector_store:
        type: "inmemory"
    - name: "docs"
      vector_store:
        type: "inmemory"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)
	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.Contains(t, err.Error(), "duplicate knowledge name: docs")
}

func TestConvertKnowledgeConfigs_SkipsEntriesWithoutComponents(t *testing.T) {
	t.Parallel()

	configs, err := convertKnowledgeConfigs([]knowledgeEntryConfig{
		{Name: "empty"},
		{
			Name: "docs",
			VectorStore: &rawYAMLNode{Node: yamlNode(t, `
type: inmemory
max_results: 5
`)},
		},
	})
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.NotContains(t, configs, "empty")
	require.Contains(t, configs, "docs")
}

func TestConvertKnowledgeConfigs_EmptyEntriesReturnNil(t *testing.T) {
	t.Parallel()

	configs, err := convertKnowledgeConfigs(nil)
	require.NoError(t, err)
	require.Nil(t, configs)
}

func TestConvertKnowledgeConfigs_ClonesNestedNodes(t *testing.T) {
	t.Parallel()

	embedderNode := yamlNode(t, `
type: openai
model: text-embedding-3-small
`)
	vectorStoreNode := yamlNode(t, `
type: inmemory
max_results: 5
`)

	configs, err := convertKnowledgeConfigs([]knowledgeEntryConfig{{
		Name:        "docs",
		Embedder:    &rawYAMLNode{Node: embedderNode},
		VectorStore: &rawYAMLNode{Node: vectorStoreNode},
	}})
	require.NoError(t, err)

	mappingValue(embedderNode, "model").Value = "mutated-model"
	mappingValue(vectorStoreNode, "max_results").Value = "99"

	gotEmbedder := mappingValue(configs["docs"], "embedder")
	gotVectorStore := mappingValue(configs["docs"], "vector_store")
	require.Equal(t, "text-embedding-3-small", mappingValue(gotEmbedder, "model").Value)
	require.Equal(t, "5", mappingValue(gotVectorStore, "max_results").Value)
}

func TestCloneYAMLNode_NilReturnsNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, cloneYAMLNode(nil))
}

func TestParseRunOptions_DebugRecorder_ConfigApplied(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	cfgPath := writeTempConfig(t, fmt.Sprintf(`
debug_recorder:
  enabled: true
  dir: %q
  mode: safe
`, outDir))

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.True(t, opts.DebugRecorderEnabled)
	require.Equal(t, outDir, opts.DebugRecorderDir)
	require.Equal(t, "safe", opts.DebugRecorderMode)
}

func TestParseRunOptions_SkillsDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseRunOptions(nil)
	require.NoError(t, err)
	require.True(t, opts.SkillsWatch)
	require.False(t, opts.SkillsWatchBundled)
	require.Equal(
		t,
		defaultSkillsWatchDebounce,
		opts.SkillsWatchDebounce,
	)
	require.Equal(
		t,
		defaultSkillsToolProfile,
		opts.SkillsToolProfile,
	)
	require.Equal(t, defaultSkillsLoadMode, opts.SkillsLoadMode)
	require.True(t, opts.SkillsToolResults)
	require.True(t, opts.SkillsSkipFallback)
	require.Zero(t, opts.SkillsMaxLoaded)
	require.Nil(t, opts.SkillsToolingGuide)
}

func TestParseRunOptions_SkillsLoadMode_InvalidFails(t *testing.T) {
	t.Parallel()

	_, err := parseRunOptions([]string{
		"-skills-load-mode", "bad",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 2, exitErr.Code)
}

func TestParseRunOptions_SkillsToolProfile_InvalidFails(
	t *testing.T,
) {
	t.Parallel()

	_, err := parseRunOptions([]string{
		"-skills-tool-profile", "bad",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 2, exitErr.Code)
}

func TestParseRunOptions_SkillsToolProfile_FullAccepted(
	t *testing.T,
) {
	t.Parallel()

	opts, err := parseRunOptions([]string{
		"-skills-tool-profile", "FULL",
	})
	require.NoError(t, err)
	require.Equal(t, skillprofile.Full, opts.SkillsToolProfile)
}

func TestParseRunOptions_SkillsToolProfile_ConfigInvalidFails(
	t *testing.T,
) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
skills:
  tool_profile: "bad"
`)

	_, err := parseRunOptions([]string{"-config", cfgPath})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestSkillsOptionExitCode(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		2,
		skillsOptionExitCode(map[string]struct{}{
			flagSkillsToolProfile: {},
		}),
	)
	require.Equal(
		t,
		2,
		skillsOptionExitCode(map[string]struct{}{
			flagSkillsLoadMode: {},
		}),
	)
	require.Equal(t, 1, skillsOptionExitCode(map[string]struct{}{}))
}

func TestParseRunOptions_AdminDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseRunOptions(nil)
	require.NoError(t, err)
	require.True(t, opts.AdminEnabled)
	require.Equal(t, defaultAdminAddr, opts.AdminAddr)
	require.True(t, opts.AdminAutoPort)
}

func TestParseRunOptions_AdminConfigApplied(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
admin:
  enabled: true
  addr: "127.0.0.1:21000"
  auto_port: false
`)

	opts, err := parseRunOptions([]string{"-config", cfgPath})
	require.NoError(t, err)
	require.True(t, opts.AdminEnabled)
	require.Equal(t, "127.0.0.1:21000", opts.AdminAddr)
	require.False(t, opts.AdminAutoPort)
}

func TestParseRunOptions_AdminFlagOverridesConfig(t *testing.T) {
	t.Parallel()

	cfgPath := writeTempConfig(t, `
admin:
  enabled: true
  addr: "127.0.0.1:21000"
  auto_port: false
`)

	opts, err := parseRunOptions([]string{
		"-config", cfgPath,
		"-admin-addr", "127.0.0.1:22000",
		"-admin-auto-port=true",
	})
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:22000", opts.AdminAddr)
	require.True(t, opts.AdminAutoPort)
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func TestFinalizeRunOptions_ApproxRunesPerToken(t *testing.T) {
	t.Parallel()

	t.Run("zero is valid", func(t *testing.T) {
		opts := &runOptions{}
		require.NoError(t, finalizeRunOptions(opts))
	})

	t.Run("positive is valid", func(t *testing.T) {
		opts := &runOptions{SessionSummaryApproxRunesPerToken: 2.0}
		require.NoError(t, finalizeRunOptions(opts))
	})

	t.Run("negative is rejected", func(t *testing.T) {
		opts := &runOptions{SessionSummaryApproxRunesPerToken: -1.0}
		require.Error(t, finalizeRunOptions(opts))
	})

	t.Run("NaN is rejected", func(t *testing.T) {
		opts := &runOptions{
			SessionSummaryApproxRunesPerToken: math.NaN(),
		}
		require.Error(t, finalizeRunOptions(opts))
	})

	t.Run("Inf is rejected", func(t *testing.T) {
		opts := &runOptions{
			SessionSummaryApproxRunesPerToken: math.Inf(1),
		}
		require.Error(t, finalizeRunOptions(opts))
	})
}

func TestFinalizeRunOptions_SkillsWatchDebounce(t *testing.T) {
	t.Parallel()

	opts := &runOptions{
		SkillsWatchDebounce: -time.Millisecond,
	}

	err := finalizeRunOptions(opts)
	require.EqualError(
		t,
		err,
		"invalid skills watch debounce: -1ms",
	)
}
