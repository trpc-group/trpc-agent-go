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
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestRun_ParseErrorExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"-unknown-flag"})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 2, exitErr.Code)
}

func TestMain_HelpReturnsUsageCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, Main([]string{"-h"}))
}

func TestMain_InspectDispatches(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, Main([]string{subcmdInspect}))
}

func TestRun_TelegramProxyErrorExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-telegram-token", "x",
		"-telegram-proxy", "://bad",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_CreateModelFailsExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-mode", "nope",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_CreateAgentFailsExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", "http://[::1",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_ClaudeCode_Smoke(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-agent-type", agentTypeClaudeCode,
		"-http-addr", "127.0.0.1:0",
		"-state-dir", dir,
	})
	require.NoError(t, err)
}

func TestRun_ClaudeCode_WithSessionSummary_Smoke(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-agent-type", agentTypeClaudeCode,
		"-mode", modeMock,
		"-session-summary",
		"-http-addr", "127.0.0.1:0",
		"-state-dir", dir,
	})
	require.NoError(t, err)
}

func TestRun_ClaudeCode_UnsupportedOptionsExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-agent-type", agentTypeClaudeCode,
		"-enable-openclaw-tools",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_ClaudeCode_InvalidOutputFormatExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-agent-type", agentTypeClaudeCode,
		"-claude-output-format", "nope",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_ClaudeCode_UnsupportedPromptOptionsExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-agent-type", agentTypeClaudeCode,
		"-agent-instruction", "x",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_ClaudeCode_UnsupportedSystemPromptOptionsExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-agent-type", agentTypeClaudeCode,
		"-agent-system-prompt", "x",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestRun_PromptDirWithoutMarkdownExitCode(t *testing.T) {
	t.Parallel()

	promptDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(promptDir, "note.txt"),
		[]byte("ignored"),
		0o600,
	))

	err := run(context.Background(), []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-agent-system-prompt-dir", promptDir,
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestNewAgent_EmptyInstructionUsesDefault(t *testing.T) {
	t.Parallel()

	agt, err := newAgent(&echoModel{name: "mock"}, agentConfig{
		AppName:      "demo",
		SkillsRoot:   t.TempDir(),
		StateDir:     t.TempDir(),
		Instruction:  "",
		SystemPrompt: "sys",
	}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, agt)
}

func TestRun_HTTPListenErrorPath(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:-1",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
	})
	require.NoError(t, err)
}

func TestRun_Smoke(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", dir,
		"-skills-root", t.TempDir(),
		"-skills-extra-dirs", t.TempDir() + "," + t.TempDir(),
		"-skills-debug",
		"-allow-users", "u1,u2",
		"-require-mention",
		"-mention", "@bot",
		"-enable-local-exec",
		"-enable-openclaw-tools",
	})
	require.NoError(t, err)
}

func TestRun_WithTelegram_BaseURLOverride(t *testing.T) {
	dir := t.TempDir()
	token := "token"

	srv := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		switch r.URL.Path {
		case "/bot" + token + "/getMe":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w,
				`{"ok":true,"result":{"id":1,"username":"bot"}}`,
			)
		case "/bot" + token + "/getUpdates":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv(telegramBaseURLEnvName, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", dir,
		"-skills-root", t.TempDir(),
		"-telegram-token", token,
		"-require-mention",
	})
	require.NoError(t, err)
}

func TestSplitCSV(t *testing.T) {
	require.Nil(t, splitCSV(""))
	require.Nil(t, splitCSV("  "))
	require.Equal(t, []string{"a", "b"}, splitCSV(" a , b "))
	require.Equal(t, []string{"a"}, splitCSV("a,, ,"))
}

func TestNormalizeAgentType(t *testing.T) {
	t.Parallel()

	typ, err := normalizeAgentType("")
	require.NoError(t, err)
	require.Equal(t, agentTypeLLM, typ)

	typ, err = normalizeAgentType(" LLM ")
	require.NoError(t, err)
	require.Equal(t, agentTypeLLM, typ)

	typ, err = normalizeAgentType("claude-code")
	require.NoError(t, err)
	require.Equal(t, agentTypeClaudeCode, typ)

	typ, err = normalizeAgentType("ClaudeCode")
	require.NoError(t, err)
	require.Equal(t, agentTypeClaudeCode, typ)

	_, err = normalizeAgentType("nope")
	require.Error(t, err)
}

func TestValidateAgentRunOptions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		agentType string
		opts      runOptions
		wantErr   bool
	}{
		{
			name:      "llm ok",
			agentType: agentTypeLLM,
		},
		{
			name:      "claude ok",
			agentType: agentTypeClaudeCode,
		},
		{
			name:      "unknown agent",
			agentType: "x",
			wantErr:   true,
		},
		{
			name:      "add-session-summary",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				AddSessionSummary: true,
			},
			wantErr: true,
		},
		{
			name:      "max-history-runs",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				MaxHistoryRuns: 1,
			},
			wantErr: true,
		},
		{
			name:      "preload-memory",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				PreloadMemory: 1,
			},
			wantErr: true,
		},
		{
			name:      "enable-local-exec",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				EnableLocalExec: true,
			},
			wantErr: true,
		},
		{
			name:      "enable-openclaw-tools",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				EnableOpenClawTools: true,
			},
			wantErr: true,
		},
		{
			name:      "tools.providers",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				ToolProviders: []pluginSpec{{Type: "x"}},
			},
			wantErr: true,
		},
		{
			name:      "tools.toolsets",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				ToolSets: []pluginSpec{{Type: "x"}},
			},
			wantErr: true,
		},
		{
			name:      "refresh-toolsets-on-run",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				RefreshToolSetsOnRun: true,
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateAgentRunOptions(tc.agentType, tc.opts)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestParseClaudeOutputFormat(t *testing.T) {
	t.Parallel()

	format, err := parseClaudeOutputFormat("json")
	require.NoError(t, err)
	require.Equal(t, claudecode.OutputFormatJSON, format)

	format, err = parseClaudeOutputFormat(" stream-json ")
	require.NoError(t, err)
	require.Equal(t, claudecode.OutputFormatStreamJSON, format)

	_, err = parseClaudeOutputFormat("")
	require.Error(t, err)

	_, err = parseClaudeOutputFormat("nope")
	require.Error(t, err)
}

func TestNewClaudeCodeAgent(t *testing.T) {
	t.Parallel()

	ag, err := newClaudeCodeAgent(runOptions{
		ClaudeBin:          "claude",
		ClaudeOutputFormat: "stream-json",
		ClaudeExtraArgs:    "--help",
		ClaudeEnv:          "A=B",
		ClaudeWorkDir:      "/tmp",
	})
	require.NoError(t, err)
	require.Equal(t, defaultAgentName, ag.Info().Name)
}

func TestResolveStateDir_Custom(t *testing.T) {
	got, err := resolveStateDir("  /tmp/state ")
	require.NoError(t, err)
	require.Equal(t, "/tmp/state", got)
}

func TestResolveStateDir_DefaultHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := resolveStateDir("")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".trpc-agent-go", appName), got)
}

func TestConfigFingerprint_Deterministic(t *testing.T) {
	require.Equal(t, configFingerprint("a"), configFingerprint("a"))
	require.NotEqual(t, configFingerprint("a"), configFingerprint("b"))
}

func TestParseOpenAIVariant_Explicit(t *testing.T) {
	v, err := parseOpenAIVariant(string(openai.VariantOpenAI), "gpt-5")
	require.NoError(t, err)
	require.Equal(t, openai.VariantOpenAI, v)
}

func TestParseOpenAIVariant_Auto(t *testing.T) {
	v, err := parseOpenAIVariant(openAIVariantAuto, "deepseek-chat")
	require.NoError(t, err)
	require.Equal(t, openai.VariantDeepSeek, v)
}

func TestParseOpenAIVariant_Unknown(t *testing.T) {
	_, err := parseOpenAIVariant("nope", "gpt-5")
	require.Error(t, err)
}

func TestInferOpenAIVariant(t *testing.T) {
	require.Equal(
		t,
		openai.VariantDeepSeek,
		inferOpenAIVariant("deepseek-r1"),
	)
	require.Equal(t, openai.VariantQwen, inferOpenAIVariant("qwen2.5"))
	require.Equal(
		t,
		openai.VariantHunyuan,
		inferOpenAIVariant("hunyuan-t1"),
	)
	require.Equal(t, openai.VariantOpenAI, inferOpenAIVariant("gpt-5"))
}

func TestNewModel_Mock(t *testing.T) {
	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)
	require.Equal(t, "mock-echo", mdl.Info().Name)
}

func TestDefaultOpenAIModelName(t *testing.T) {
	t.Setenv(openAIModelEnvName, "")
	require.Equal(t, defaultOpenAIModel, defaultOpenAIModelName())

	t.Setenv(openAIModelEnvName, " gpt-5 ")
	require.Equal(t, "gpt-5", defaultOpenAIModelName())
}

func TestExitError_Error(t *testing.T) {
	t.Parallel()

	var e *exitError
	require.Equal(t, "", e.Error())
	require.Equal(t, "", (&exitError{}).Error())
	require.Equal(t, "x", (&exitError{Err: errors.New("x")}).Error())
}

func TestNewModel_OpenAI(t *testing.T) {
	mdl, err := modelFromOptions(runOptions{
		ModelMode:     modeOpenAI,
		OpenAIModel:   "gpt-5",
		OpenAIVariant: openAIVariantAuto,
	})
	require.NoError(t, err)
	require.Equal(t, "gpt-5", mdl.Info().Name)
}

func TestNewModel_UnsupportedMode(t *testing.T) {
	_, err := modelFromOptions(runOptions{ModelMode: "x"})
	require.Error(t, err)
}

func TestEchoModel_GenerateContent_NilContext(t *testing.T) {
	m := &echoModel{name: "x"}
	_, err := m.GenerateContent(nil, &model.Request{})
	require.Error(t, err)
}

func TestEchoModel_GenerateContent_EchoesLastUserMessage(t *testing.T) {
	m := &echoModel{name: "x"}
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("first"),
			model.NewAssistantMessage("skip"),
			model.NewUserMessage("last"),
		},
	})
	require.NoError(t, err)

	rsp := <-ch
	require.True(t, rsp.Done)
	require.Equal(t, "x", rsp.Model)
	require.Len(t, rsp.Choices, 1)
	require.Contains(t, rsp.Choices[0].Message.Content, "Echo: last")
}

func TestLastUserText(t *testing.T) {
	require.Equal(t, "", lastUserText(nil))
	require.Equal(
		t,
		"b",
		lastUserText(&model.Request{
			Messages: []model.Message{
				model.NewAssistantMessage("x"),
				model.NewUserMessage("a"),
				model.NewUserMessage("b"),
			},
		}),
	)
}

func TestDirExists(t *testing.T) {
	require.False(t, dirExists(filepath.Join(t.TempDir(), "missing")))

	dir := filepath.Join(t.TempDir(), "x")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.True(t, dirExists(dir))
}

func TestResolveWorkspaceSkillsRoot_Custom(t *testing.T) {
	require.Equal(
		t,
		"/x",
		resolveWorkspaceSkillsRoot("/cwd", "/x"),
	)
}

func TestResolveWorkspaceSkillsRoot_CwdSkills(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(cwd, defaultSkillsDir),
		0o700,
	))

	got := resolveWorkspaceSkillsRoot(cwd, "")
	require.Equal(t, filepath.Join(cwd, defaultSkillsDir), got)
}

func TestResolveWorkspaceSkillsRoot_RepoBundled(t *testing.T) {
	cwd := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(cwd, appName, defaultSkillsDir),
		0o700,
	))

	got := resolveWorkspaceSkillsRoot(cwd, "")
	require.Equal(t, filepath.Join(cwd, appName, defaultSkillsDir), got)
}

func TestResolveSkillRoots_IncludesExpectedRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := t.TempDir()
	require.NoError(t, os.MkdirAll(
		filepath.Join(cwd, defaultSkillsDir),
		0o700,
	))

	stateDir := t.TempDir()
	cfg := agentConfig{
		StateDir: stateDir,
		SkillsExtraDirs: []string{
			"extra1",
			"extra2",
		},
	}

	roots := resolveSkillRoots(cwd, cfg)
	require.Contains(t, roots, filepath.Join(cwd, defaultSkillsDir))
	require.Contains(
		t,
		roots,
		filepath.Join(cwd, defaultAgentsDir, defaultSkillsDir),
	)
	require.Contains(
		t,
		roots,
		filepath.Join(home, defaultAgentsDir, defaultSkillsDir),
	)
	require.Contains(t, roots, filepath.Join(stateDir, defaultSkillsDir))
	require.Contains(
		t,
		roots,
		filepath.Join(cwd, appName, defaultSkillsDir),
	)
	require.Contains(t, roots, "extra1")
	require.Contains(t, roots, "extra2")
}

type stubGateway struct{}

func (stubGateway) SendMessage(
	_ context.Context,
	_ gwclient.MessageRequest,
) (gwclient.MessageResponse, error) {
	return gwclient.MessageResponse{}, nil
}

func (stubGateway) Cancel(_ context.Context, _ string) (bool, error) {
	return false, nil
}

type channelPluginCfg struct {
	Greeting string `yaml:"greeting"`
}

type stubChannel struct {
	id       string
	greeting string
	deps     registry.ChannelDeps
}

func (c *stubChannel) ID() string { return c.id }

func (c *stubChannel) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func TestChannelsFromRegistry(t *testing.T) {
	const typeName = "test_channel"
	require.NoError(t, registry.RegisterChannel(
		typeName,
		func(
			deps registry.ChannelDeps,
			spec registry.PluginSpec,
		) (occhannel.Channel, error) {
			var cfg channelPluginCfg
			if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
				return nil, err
			}

			id := typeName
			if spec.Name != "" {
				id = spec.Name
			}
			return &stubChannel{
				id:       id,
				greeting: cfg.Greeting,
				deps:     deps,
			}, nil
		},
	))

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("greeting: hi"), &node))

	channels, err := channelsFromRegistry(
		stubGateway{},
		"demo",
		"/state",
		[]pluginSpec{{
			Type:   typeName,
			Name:   "c1",
			Config: &node,
		}},
	)
	require.NoError(t, err)
	require.Len(t, channels, 1)

	got, ok := channels[0].(*stubChannel)
	require.True(t, ok)
	require.Equal(t, "c1", got.ID())
	require.Equal(t, "hi", got.greeting)
	require.Equal(t, "demo", got.deps.AppName)
	require.Equal(t, "/state", got.deps.StateDir)
}

type toolProviderCfg struct {
	ToolName string `yaml:"tool_name"`
}

type stubTool struct {
	name string
}

func (t stubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        t.name,
		Description: "stub tool",
	}
}

func TestToolsFromProviders(t *testing.T) {
	const typeName = "test_tool_provider"
	require.NoError(t, registry.RegisterToolProvider(
		typeName,
		func(
			_ registry.ToolProviderDeps,
			spec registry.PluginSpec,
		) ([]tool.Tool, error) {
			var cfg toolProviderCfg
			if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
				return nil, err
			}
			return []tool.Tool{stubTool{name: cfg.ToolName}}, nil
		},
	))

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("tool_name: t1"), &node))

	tools, err := toolsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{
			Type:   typeName,
			Name:   "p1",
			Config: &node,
		}},
	)
	require.NoError(t, err)
	require.Len(t, tools, 1)
	require.Equal(t, "t1", tools[0].Declaration().Name)
}

func TestToolsFromProviders_EmptyTypeFails(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = toolsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{Type: " "}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tools.providers[0].type is empty")
}

func TestToolsFromProviders_UnsupportedTypeFails(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = toolsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{Type: "nope"}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported tool provider")
}

func TestToolsFromProviders_ProviderErrorWrapped(t *testing.T) {
	t.Parallel()

	const typeName = "test_tool_provider_error"
	require.NoError(t, registry.RegisterToolProvider(
		typeName,
		func(
			_ registry.ToolProviderDeps,
			_ registry.PluginSpec,
		) ([]tool.Tool, error) {
			return nil, errors.New("boom")
		},
	))

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = toolsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{Type: typeName}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool provider")
	require.Contains(t, err.Error(), "boom")
}

func TestToolSetsFromProviders_EmptySpecsReturnsNil(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	sets, err := toolSetsFromProviders(mdl, "demo", "/state", nil)
	require.NoError(t, err)
	require.Nil(t, sets)
}

func TestToolSetsFromProviders_EmptyTypeFails(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = toolSetsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{Type: " "}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tools.toolsets[0].type is empty")
}

func TestToolSetsFromProviders_UnsupportedTypeFails(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = toolSetsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{Type: "nope"}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported toolset provider")
}

func TestToolSetsFromProviders_ProviderErrorWrapped(t *testing.T) {
	t.Parallel()

	const typeName = "test_toolset_provider_error"
	require.NoError(t, registry.RegisterToolSetProvider(
		typeName,
		func(
			_ registry.ToolSetProviderDeps,
			_ registry.PluginSpec,
		) (tool.ToolSet, error) {
			return nil, errors.New("boom")
		},
	))

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = toolSetsFromProviders(
		mdl,
		"demo",
		"/state",
		[]pluginSpec{{Type: typeName}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "toolset provider")
	require.Contains(t, err.Error(), "boom")
}

func TestChannelsFromRegistry_EmptyTypeFails(t *testing.T) {
	t.Parallel()

	_, err := channelsFromRegistry(
		stubGateway{},
		"demo",
		"/state",
		[]pluginSpec{{Type: " "}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "channels[0].type is empty")
}

func TestChannelsFromRegistry_UnsupportedTypeFails(t *testing.T) {
	t.Parallel()

	_, err := channelsFromRegistry(
		stubGateway{},
		"demo",
		"/state",
		[]pluginSpec{{Type: "nope"}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported channel type")
}

func TestChannelsFromRegistry_ChannelErrorWrapped(t *testing.T) {
	t.Parallel()

	const typeName = "test_channel_error"
	require.NoError(t, registry.RegisterChannel(
		typeName,
		func(
			_ registry.ChannelDeps,
			_ registry.PluginSpec,
		) (occhannel.Channel, error) {
			return nil, errors.New("boom")
		},
	))

	_, err := channelsFromRegistry(
		stubGateway{},
		"demo",
		"/state",
		[]pluginSpec{{Type: typeName}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel")
	require.Contains(t, err.Error(), "boom")
}
