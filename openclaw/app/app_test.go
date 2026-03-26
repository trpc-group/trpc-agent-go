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
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
	langfuseobs "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

type captureRequestModel struct {
	got *model.Request
}

func (m *captureRequestModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.got = req
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "ok",
			},
		}},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func (m *captureRequestModel) Info() model.Info {
	return model.Info{Name: "capture"}
}

func createAppTestSkill(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "echoer")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data := "---\nname: echoer\n" +
		"description: simple echo skill\n---\nbody\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte(data),
		0o600,
	))
	return root
}

func runAgentAndCapture(
	t *testing.T,
	agt agent.Agent,
	mdl *captureRequestModel,
	sess *session.Session,
) *model.Request {
	t.Helper()

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(sess),
	)
	ch, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for evt := range ch {
		if evt == nil || !evt.RequiresCompletion {
			continue
		}
		key := agent.GetAppendEventNoticeKey(evt.ID)
		require.NotNil(
			t,
			inv.AddNoticeChannel(context.Background(), key),
		)
		require.NoError(t, inv.NotifyCompletion(context.Background(), key))
	}
	require.NotNil(t, mdl.got)
	return mdl.got
}

func joinSystemMessages(req *model.Request) string {
	if req == nil {
		return ""
	}
	var parts []string
	for _, msg := range req.Messages {
		if msg.Role != model.RoleSystem {
			continue
		}
		parts = append(parts, msg.Content)
	}
	return strings.Join(parts, "\n\n")
}

func findToolDeclaration(
	tools []tool.Tool,
	name string,
) *tool.Declaration {
	for _, item := range tools {
		decl := item.Declaration()
		if decl == nil || decl.Name != name {
			continue
		}
		return decl
	}
	return nil
}

func TestRun_ParseErrorExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"-unknown-flag"})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 2, exitErr.Code)
}

func TestNewRuntime_BuildsGatewayHandler(t *testing.T) {
	t.Parallel()

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", "mock",
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})

	require.NotNil(t, rt.Gateway.Handler)
	require.NotEmpty(t, rt.Gateway.HealthPath)
	require.NotEmpty(t, rt.Gateway.MessagesPath)
	require.NotEmpty(t, rt.Gateway.StatusPath)
	require.NotEmpty(t, rt.Gateway.CancelPath)
	require.Empty(t, rt.Channels)
}

func TestNewRuntime_MemoryBackendFile_DisablesStructuredMemory(
	t *testing.T,
) {
	t.Parallel()

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-memory-backend", memoryBackendFile,
		"-memory-auto",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})
	require.Nil(t, rt.memorySvc)
	require.Nil(t, memoryServiceTools(nil))
}

func TestFileMemoryStoreForBackend_FileOnly(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	require.Same(
		t,
		store,
		fileMemoryStoreForBackend(memoryBackendFile, store),
	)
	require.Nil(
		t,
		fileMemoryStoreForBackend(memoryBackendInMemory, store),
	)
	require.Nil(
		t,
		fileMemoryStoreForBackend(memoryBackendSQLite, store),
	)
}

func TestBuildOpenClawTools_HidesMemoryFileEnvWithoutFileBackend(t *testing.T) {
	t.Parallel()

	bundle := buildOpenClawTools(true, t.TempDir(), nil, nil)
	decl := findToolDeclaration(bundle.tools, "exec_command")
	require.NotNil(t, decl)
	require.NotContains(t, decl.Description, "OPENCLAW_MEMORY_FILE")
}

func TestBuildOpenClawTools_ExposesMemoryFileEnvForFileBackend(t *testing.T) {
	t.Parallel()

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)

	bundle := buildOpenClawTools(true, t.TempDir(), nil, store)
	decl := findToolDeclaration(bundle.tools, "exec_command")
	require.NotNil(t, decl)
	require.Contains(t, decl.Description, "OPENCLAW_MEMORY_FILE")
}

func TestBuildOpenClawTools_IncludesConversationHistoryTool(
	t *testing.T,
) {
	t.Parallel()

	bundle := buildOpenClawTools(true, t.TempDir(), nil, nil)
	decl := findToolDeclaration(bundle.tools, "conversation_history")
	require.NotNil(t, decl)
	require.Contains(
		t,
		decl.Description,
		"current conversation session",
	)
}

func TestNewRuntimeStores_CreatesAllStores(t *testing.T) {
	t.Parallel()

	stores, err := newRuntimeStores(t.TempDir())
	require.NoError(t, err)
	require.NotNil(t, stores.uploads)
	require.NotNil(t, stores.personas)
	require.NotNil(t, stores.memoryFiles)
}

func TestNewRuntimeStores_EmptyStateDirReturnsError(t *testing.T) {
	t.Parallel()

	_, err := newRuntimeStores(" ")
	require.Error(t, err)
}

func TestNewRuntime_RuntimeStoresErrorExitCode(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state-file")
	require.NoError(t, os.WriteFile(stateFile, []byte("x"), 0o600))

	_, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", stateFile,
		"-skills-root", t.TempDir(),
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "create runtime stores failed")
}

func TestRun_HTTPListenErrorPath_WithFileMemoryBackend(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:-1",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-memory-backend", memoryBackendFile,
		"-enable-openclaw-tools",
	})
	require.NoError(t, err)
}

func TestRun_RuntimeStoresErrorExitCode(t *testing.T) {
	t.Parallel()

	stateFile := filepath.Join(t.TempDir(), "state-file")
	require.NoError(t, os.WriteFile(stateFile, []byte("x"), 0o600))

	err := run(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", stateFile,
		"-skills-root", t.TempDir(),
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "create runtime stores failed")
}

func TestNewRuntime_A2AConfigErrorExitCode(t *testing.T) {
	t.Parallel()

	_, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-a2a",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "create a2a failed")
}

func TestNewRuntime_DebugRecorderEnabled_Smoke(t *testing.T) {
	t.Parallel()

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-debug-recorder",
		"-debug-recorder-mode", "safe",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})
	require.NotNil(t, rt.Gateway.Handler)
}

func TestNewRuntime_DebugRecorderModeInvalidExitCode(t *testing.T) {
	t.Parallel()

	_, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-debug-recorder",
		"-debug-recorder-mode", "nope",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestNewRuntime_WithRalphLoop_Smoke(t *testing.T) {
	t.Parallel()

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-agent-ralph-loop",
		"-agent-ralph-completion-promise", "done",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})
	require.NotNil(t, rt.Gateway.Handler)
}

func TestNewRuntime_ClaudeCode_Smoke(t *testing.T) {
	t.Parallel()

	rt, err := NewRuntime(context.Background(), []string{
		"-agent-type", agentTypeClaudeCode,
		"-state-dir", t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})
	require.NotNil(t, rt.Gateway.Handler)
}

func TestNewRuntime_ClaudeCode_WithSessionSummary_Smoke(t *testing.T) {
	dir := t.TempDir()

	rt, err := NewRuntime(context.Background(), []string{
		"-agent-type", agentTypeClaudeCode,
		"-mode", modeMock,
		"-session-summary",
		"-state-dir", dir,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})
	require.NotNil(t, rt.Gateway.Handler)
}

func TestNewRuntime_WithTelegram_BuildsChannel(t *testing.T) {
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
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	cfgData, err := yaml.Marshal(map[string]any{
		"channels": []any{
			map[string]any{
				"type": telegramChannelType,
				"config": map[string]any{
					"token": token,
				},
			},
		},
	})
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", dir,
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
		"-require-mention",
		"-mention", "@bot",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})

	require.NotNil(t, rt.Gateway.Handler)
	require.Len(t, rt.Channels, 1)
	require.Equal(t, telegramChannelType, rt.Channels[0].ID())
}

func TestNewRuntime_TelegramProxyErrorExitCode(t *testing.T) {
	cfgData, err := yaml.Marshal(map[string]any{
		"channels": []any{
			map[string]any{
				"type": telegramChannelType,
				"config": map[string]any{
					"token": "x",
					"proxy": "://bad",
				},
			},
		},
	})
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-config", cfgPath,
		"-skills-root", t.TempDir(),
	})
	require.Nil(t, rt)
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
}

func TestNewRuntime_ClosesResourcesOnError(t *testing.T) {
	const toolSetType = "test_runtime_toolset_cleanup"
	const badChannelType = "test_runtime_channel_missing"

	toolSetClosed := false
	toolSetCloseErr := errors.New("close toolset boom")
	require.NoError(t, registry.RegisterToolSetProvider(
		toolSetType,
		func(
			_ registry.ToolSetProviderDeps,
			_ registry.PluginSpec,
		) (tool.ToolSet, error) {
			return &stubToolSet{
				name:     toolSetType,
				closeErr: toolSetCloseErr,
				closed:   &toolSetClosed,
			}, nil
		},
	))

	stateDir := t.TempDir()
	cfg := map[string]any{
		"state_dir": stateDir,
		"tools": map[string]any{
			"toolsets": []any{
				map[string]any{"type": toolSetType},
			},
		},
		"channels": []any{
			map[string]any{"type": badChannelType},
		},
	}

	cfgData, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-config", cfgPath,
	})
	require.Nil(t, rt)
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.True(t, toolSetClosed)
}

func TestNewRuntime_WithExtraChannels(t *testing.T) {
	const typeName = "test_runtime_channel"
	const channelName = "c1"

	require.NoError(t, registry.RegisterChannel(
		typeName,
		func(
			deps registry.ChannelDeps,
			spec registry.PluginSpec,
		) (occhannel.Channel, error) {
			id := typeName
			if spec.Name != "" {
				id = spec.Name
			}
			return &stubChannel{id: id, deps: deps}, nil
		},
	))

	stateDir := t.TempDir()
	cfg := map[string]any{
		"state_dir": stateDir,
		"channels": []any{
			map[string]any{
				"type": typeName,
				"name": channelName,
			},
		},
	}

	cfgData, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-config", cfgPath,
		"-skills-root", t.TempDir(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Close()
	})

	require.Len(t, rt.Channels, 1)
	ch, ok := rt.Channels[0].(*stubChannel)
	require.True(t, ok)
	require.Equal(t, channelName, ch.ID())
	require.Equal(t, stateDir, ch.deps.StateDir)
	require.Equal(t, appName, ch.deps.AppName)
}

func TestNewRuntime_ErrorPathsExitCode(t *testing.T) {
	const token = "token"

	makeBotServer := func(t *testing.T, ok bool) string {
		t.Helper()

		srv := httptest.NewServer(http.HandlerFunc(func(
			w http.ResponseWriter,
			r *http.Request,
		) {
			switch r.URL.Path {
			case "/bot" + token + "/getMe":
				if !ok {
					http.Error(w, "boom", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w,
					`{"ok":true,"result":{"id":1,"username":"bot"}}`,
				)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}

	cases := []struct {
		name     string
		args     func(t *testing.T) []string
		wantCode int
	}{
		{
			name: "parse error",
			args: func(*testing.T) []string {
				return []string{"-unknown-flag"}
			},
			wantCode: 2,
		},
		{
			name: "unsupported agent type",
			args: func(*testing.T) []string {
				return []string{"-agent-type", "nope"}
			},
			wantCode: 1,
		},
		{
			name: "unsupported model mode",
			args: func(t *testing.T) []string {
				return []string{
					"-mode", "nope",
					"-state-dir", t.TempDir(),
				}
			},
			wantCode: 1,
		},
		{
			name: "unsupported memory backend",
			args: func(t *testing.T) []string {
				return []string{
					"-mode", modeMock,
					"-state-dir", t.TempDir(),
					"-skills-root", t.TempDir(),
					"-memory-backend", "nope",
				}
			},
			wantCode: 1,
		},
		{
			name: "prompt dir without markdown",
			args: func(t *testing.T) []string {
				promptDir := t.TempDir()
				require.NoError(t, os.WriteFile(
					filepath.Join(promptDir, "note.txt"),
					[]byte("ignored"),
					0o600,
				))
				return []string{
					"-mode", modeMock,
					"-state-dir", t.TempDir(),
					"-skills-root", t.TempDir(),
					"-agent-system-prompt-dir", promptDir,
				}
			},
			wantCode: 1,
		},
		{
			name: "unsupported toolset provider",
			args: func(t *testing.T) []string {
				stateDir := t.TempDir()
				cfg := map[string]any{
					"state_dir": stateDir,
					"tools": map[string]any{
						"toolsets": []any{
							map[string]any{
								"type": "missing_toolset",
							},
						},
					},
				}

				cfgData, err := yaml.Marshal(cfg)
				require.NoError(t, err)
				cfgPath := filepath.Join(
					t.TempDir(),
					"config.yaml",
				)
				require.NoError(t, os.WriteFile(
					cfgPath,
					cfgData,
					0o600,
				))
				return []string{
					"-mode", modeMock,
					"-config", cfgPath,
					"-skills-root", t.TempDir(),
				}
			},
			wantCode: 1,
		},
		{
			name: "probe telegram bot fails",
			args: func(t *testing.T) []string {
				t.Setenv(
					tgapi.BaseURLEnvName,
					makeBotServer(t, false),
				)

				cfgData, err := yaml.Marshal(map[string]any{
					"channels": []any{
						map[string]any{
							"type": telegramChannelType,
							"config": map[string]any{
								"token": token,
							},
						},
					},
				})
				require.NoError(t, err)
				cfgPath := filepath.Join(
					t.TempDir(),
					"config.yaml",
				)
				require.NoError(t, os.WriteFile(
					cfgPath,
					cfgData,
					0o600,
				))
				return []string{
					"-mode", modeMock,
					"-state-dir", t.TempDir(),
					"-skills-root", t.TempDir(),
					"-config", cfgPath,
				}
			},
			wantCode: 1,
		},
		{
			name: "create telegram channel fails",
			args: func(t *testing.T) []string {
				t.Setenv(
					tgapi.BaseURLEnvName,
					makeBotServer(t, true),
				)

				cfgData, err := yaml.Marshal(map[string]any{
					"channels": []any{
						map[string]any{
							"type": telegramChannelType,
							"config": map[string]any{
								"token":       token,
								"pairing_ttl": "0s",
							},
						},
					},
				})
				require.NoError(t, err)
				cfgPath := filepath.Join(
					t.TempDir(),
					"config.yaml",
				)
				require.NoError(t, os.WriteFile(
					cfgPath,
					cfgData,
					0o600,
				))
				return []string{
					"-mode", modeMock,
					"-state-dir", t.TempDir(),
					"-skills-root", t.TempDir(),
					"-config", cfgPath,
				}
			},
			wantCode: 1,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			rt, err := NewRuntime(context.Background(), tc.args(t))
			require.Nil(t, rt)
			require.Error(t, err)

			var exitErr *exitError
			require.True(t, errors.As(err, &exitErr))
			require.Equal(t, tc.wantCode, exitErr.Code)
		})
	}
}

func TestMain_HelpReturnsUsageCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, Main([]string{"-h"}))
}

func TestMain_HelpSkipsErrorLog(t *testing.T) {
	t.Parallel()

	require.False(t, shouldLogExitError(flag.ErrHelp))
	require.True(t, shouldLogExitError(errors.New("boom")))
	require.False(t, shouldLogExitError(nil))
}

func TestMain_InspectDispatches(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, Main([]string{subcmdInspect}))
}

func TestMain_BootstrapDispatches(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, Main([]string{subcmdBootstrap}))
}

func TestRun_TelegramProxyErrorExitCode(t *testing.T) {
	t.Parallel()

	cfgData, err := yaml.Marshal(map[string]any{
		"channels": []any{
			map[string]any{
				"type": telegramChannelType,
				"config": map[string]any{
					"token": "x",
					"proxy": "://bad",
				},
			},
		},
	})
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	err = run(context.Background(), []string{
		"-config", cfgPath,
		"-mode", modeMock,
		"-skills-root", t.TempDir(),
		"-state-dir", t.TempDir(),
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

func TestNewAgent_SkillsToolingGuidance_ConfigApplied(t *testing.T) {
	t.Parallel()

	root := createAppTestSkill(t)
	mdl := &captureRequestModel{}
	guide := ""
	agt, err := newAgent(mdl, agentConfig{
		AppName:            "demo",
		SkillsRoot:         root,
		StateDir:           t.TempDir(),
		SkillsToolingGuide: &guide,
	}, nil, nil)
	require.NoError(t, err)

	req := runAgentAndCapture(
		t,
		agt,
		mdl,
		&session.Session{},
	)
	sys := joinSystemMessages(req)
	require.Contains(t, sys, "Available skills:")
	require.NotContains(
		t,
		sys,
		"Tooling and workspace guidance:",
	)
}

func TestNewAgent_SkillsLoadModeTurnClearsLoadedState(t *testing.T) {
	t.Parallel()

	root := createAppTestSkill(t)
	mdl := &captureRequestModel{}
	agt, err := newAgent(mdl, agentConfig{
		AppName:        "demo",
		SkillsRoot:     root,
		StateDir:       t.TempDir(),
		SkillsLoadMode: "turn",
	}, nil, nil)
	require.NoError(t, err)

	sess := &session.Session{
		State: session.StateMap{
			skill.StateKeyLoadedPrefix + "echoer": []byte("1"),
		},
	}
	req := runAgentAndCapture(t, agt, mdl, sess)
	sys := joinSystemMessages(req)
	require.NotContains(t, sys, "[Loaded] echoer")
	require.Nil(t, sess.State[skill.StateKeyLoadedPrefix+"echoer"])
}

func TestNewAgent_SkillsToolResults_ConfigApplied(t *testing.T) {
	t.Parallel()

	root := createAppTestSkill(t)
	mdl := &captureRequestModel{}
	agt, err := newAgent(mdl, agentConfig{
		AppName:           "demo",
		SkillsRoot:        root,
		StateDir:          t.TempDir(),
		SkillsLoadMode:    "session",
		SkillsToolResults: true,
	}, nil, nil)
	require.NoError(t, err)

	sess := &session.Session{
		State: session.StateMap{
			skill.StateKeyLoadedPrefix + "echoer": []byte("1"),
		},
	}
	req := runAgentAndCapture(t, agt, mdl, sess)
	sys := joinSystemMessages(req)
	require.Contains(t, sys, "Loaded skill context:")
	require.Contains(t, sys, "[Loaded] echoer")
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
		"-agent-ralph-loop",
		"-agent-ralph-completion-promise", "done",
		"-allow-users", "u1,u2",
		"-require-mention",
		"-mention", "@bot",
		"-enable-local-exec",
		"-enable-openclaw-tools",
	})
	require.NoError(t, err)
}

func TestRun_WithA2A_Smoke(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", dir,
		"-skills-root", t.TempDir(),
		"-a2a",
		"-a2a-host", "http://127.0.0.1:18080/a2a",
		"-a2a-user-id-header", "X-Caller-User",
	})
	require.NoError(t, err)
}

func TestRun_A2AConfigErrorExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-a2a",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "create a2a failed")
}

func TestRun_DebugRecorderEnabled_Smoke(t *testing.T) {
	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", dir,
		"-skills-root", t.TempDir(),
		"-debug-recorder",
		"-debug-recorder-mode", "safe",
	})
	require.NoError(t, err)
}

func TestRun_DebugRecorderModeInvalidExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-debug-recorder",
		"-debug-recorder-mode", "nope",
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
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

	t.Setenv(tgapi.BaseURLEnvName, srv.URL)

	cfgData, err := yaml.Marshal(map[string]any{
		"channels": []any{
			map[string]any{
				"type": telegramChannelType,
				"config": map[string]any{
					"token": token,
				},
			},
		},
	})
	require.NoError(t, err)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, cfgData, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	runErr := run(ctx, []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", dir,
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
		"-require-mention",
		"-mention", "@bot",
	})
	require.NoError(t, runErr)
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
			name:      "llm ralph loop missing stop condition",
			agentType: agentTypeLLM,
			opts: runOptions{
				RalphLoopEnabled: true,
			},
			wantErr: true,
		},
		{
			name:      "llm ralph loop invalid env",
			agentType: agentTypeLLM,
			opts: runOptions{
				RalphLoopEnabled:       true,
				RalphLoopVerifyCommand: "echo ok",
				RalphLoopVerifyEnv:     "A",
			},
			wantErr: true,
		},
		{
			name:      "claude ok",
			agentType: agentTypeClaudeCode,
		},
		{
			name:      "claude ralph loop",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				RalphLoopEnabled:       true,
				RalphLoopVerifyCommand: "echo ok",
			},
			wantErr: true,
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
			name:      "enable-parallel-tools",
			agentType: agentTypeClaudeCode,
			opts: runOptions{
				EnableParallelTools: true,
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

func TestRalphLoopConfigFromRunOptions_NoStopConditionFails(t *testing.T) {
	t.Parallel()

	_, err := ralphLoopConfigFromRunOptions(runOptions{
		RalphLoopEnabled: true,
	})
	require.Error(t, err)
}

func TestRalphLoopConfigFromRunOptions_NegativeIterationsFails(t *testing.T) {
	t.Parallel()

	_, err := ralphLoopConfigFromRunOptions(runOptions{
		RalphLoopEnabled:       true,
		RalphLoopMaxIterations: -1,
		RalphLoopVerifyCommand: "echo ok",
	})
	require.Error(t, err)
}

func TestRalphLoopConfigFromRunOptions_ParsesVerifyEnv(t *testing.T) {
	t.Parallel()

	cfg, err := ralphLoopConfigFromRunOptions(runOptions{
		RalphLoopEnabled:       true,
		RalphLoopMaxIterations: 7,
		RalphLoopVerifyCommand: "echo ok",
		RalphLoopVerifyEnv:     "A=B,X=1",
	})
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, 7, cfg.MaxIterations)
	require.Equal(t, "echo ok", cfg.VerifyCommand)
	require.Equal(t, map[string]string{
		"A": "B",
		"X": "1",
	}, cfg.VerifyEnv)
}

func TestRalphLoopConfigFromRunOptions_NegativeTimeoutFails(t *testing.T) {
	t.Parallel()

	_, err := ralphLoopConfigFromRunOptions(runOptions{
		RalphLoopEnabled:       true,
		RalphLoopVerifyCommand: "echo ok",
		RalphLoopVerifyTimeout: -1 * time.Second,
	})
	require.Error(t, err)
}

func TestParseKVOverrides_EmptyKeyFails(t *testing.T) {
	t.Parallel()

	_, err := parseKVOverrides([]string{"=B"})
	require.Error(t, err)
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
	require.Equal(
		t,
		filepath.Join(home, ".trpc-agent-go-github", appName),
		got,
	)
}

func TestMaybeEnableDebugRecorder_Disabled(t *testing.T) {
	t.Parallel()

	ctx, rec, err := maybeEnableDebugRecorder(nil, runOptions{})
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Nil(t, rec)
	require.Nil(t, debugrecorder.RecorderFromContext(ctx))
}

func TestMaybeEnableDebugRecorder_Enabled_Defaults(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	ctx, rec, err := maybeEnableDebugRecorder(
		context.Background(),
		runOptions{
			StateDir:             stateDir,
			DebugRecorderEnabled: true,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, rec)
	require.Equal(
		t,
		filepath.Join(stateDir, defaultDebugRecorderDir),
		rec.Dir(),
	)
	require.Equal(t, rec, debugrecorder.RecorderFromContext(ctx))

	_, err = os.Stat(rec.Dir())
	require.NoError(t, err)
}

func TestMaybeEnableDebugRecorder_InvalidModeFails(t *testing.T) {
	t.Parallel()

	_, rec, err := maybeEnableDebugRecorder(
		context.Background(),
		runOptions{
			StateDir:             t.TempDir(),
			DebugRecorderEnabled: true,
			DebugRecorderMode:    "nope",
		},
	)
	require.Error(t, err)
	require.Nil(t, rec)
}

func TestMaybeEnableDebugRecorder_BadDirFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	debugFile := filepath.Join(dir, "debug.txt")
	require.NoError(t, os.WriteFile(debugFile, []byte("x"), 0o600))

	_, rec, err := maybeEnableDebugRecorder(
		context.Background(),
		runOptions{
			StateDir:             t.TempDir(),
			DebugRecorderEnabled: true,
			DebugRecorderDir:     debugFile,
		},
	)
	require.Error(t, err)
	require.Nil(t, rec)
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

func TestExitError_ExitCode(t *testing.T) {
	t.Parallel()

	var e *exitError
	require.Equal(t, 1, e.ExitCode())

	require.Equal(t, 1, (&exitError{}).ExitCode())
	require.Equal(t, 1, (&exitError{Code: 0}).ExitCode())
	require.Equal(t, 2, (&exitError{Code: 2}).ExitCode())
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
		filepath.Join(stateDir, defaultBundledSkillsDir),
	)
	require.Contains(t, roots, "extra1")
	require.Contains(t, roots, "extra2")
}

func TestResolveSkillRoots_UsesInstalledBundledSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd := t.TempDir()
	stateDir := t.TempDir()
	installedBundled := filepath.Join(
		stateDir,
		defaultBundledSkillsDir,
	)
	require.NoError(t, os.MkdirAll(installedBundled, 0o700))

	roots := resolveSkillRoots(cwd, agentConfig{StateDir: stateDir})
	require.Contains(t, roots, installedBundled)
	require.NotContains(
		t,
		roots,
		filepath.Join(cwd, appName, defaultSkillsDir),
	)
}

func TestResolveBundledSkillsRoot_RepoFallback(t *testing.T) {
	cwd := t.TempDir()
	repoBundled := filepath.Join(cwd, appName, defaultSkillsDir)
	require.NoError(t, os.MkdirAll(repoBundled, 0o700))

	require.Equal(
		t,
		repoBundled,
		resolveBundledSkillsRoot(cwd, t.TempDir()),
	)
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
		context.Background(),
		stubGateway{},
		"demo",
		"/state",
		nil,
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

func TestRuntimeAdminHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", listenURL(""))
	require.Equal(
		t,
		"http://127.0.0.1:18789",
		listenURL(":18789"),
	)
	require.Equal(
		t,
		"http://127.0.0.1:8080",
		listenURL("0.0.0.0:8080"),
	)
	require.Equal(
		t,
		"http://127.0.0.1:9090",
		listenURL("127.0.0.1:9090"),
	)

	ids := channelIDs([]occhannel.Channel{
		&stubChannel{id: "telegram"},
		nil,
		&stubChannel{id: "  "},
		&stubChannel{id: "discord"},
	})
	require.Equal(t, []string{"telegram", "discord"}, ids)

	instanceID := runtimeInstanceID(
		agentTypeLLM,
		runOptions{
			ModelMode:   modeOpenAI,
			OpenAIModel: "gpt-5",
		},
		true,
		"/tmp/state",
	)
	require.NotEmpty(t, instanceID)
}

func TestGatewayStartupLines(t *testing.T) {
	t.Parallel()

	gwSrv, err := gateway.New(&stubRunner{})
	require.NoError(t, err)

	require.Equal(t,
		[]startupLogLine{
			{text: "Gateway listening on 127.0.0.1:18080"},
			{text: "Health:   GET  /healthz"},
			{text: "Messages: POST /v1/gateway/messages"},
			{text: "Stream:   POST /v1/gateway/messages:stream"},
			{text: "Status:   GET  /v1/gateway/status?request_id=..."},
			{text: "Cancel:   POST /v1/gateway/cancel"},
		},
		gatewayStartupLines("127.0.0.1:18080", gwSrv),
	)
}

func TestAdminStartupLines(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]startupLogLine{
			{text: "Admin UI listening on 127.0.0.1:19789"},
			{text: "Admin UI: http://127.0.0.1:19789"},
		},
		adminStartupLines("127.0.0.1:19789", &adminBinding{
			addr: "127.0.0.1:19789",
			url:  "http://127.0.0.1:19789",
		}),
	)

	require.Equal(t,
		[]startupLogLine{
			{
				warn: true,
				text: "Admin UI preferred address 127.0.0.1:19789 " +
					"was busy; using 127.0.0.1:19790 instead",
			},
			{text: "Admin UI listening on 127.0.0.1:19790"},
			{text: "Admin UI: http://127.0.0.1:19790"},
		},
		adminStartupLines("127.0.0.1:19789", &adminBinding{
			addr:      "127.0.0.1:19790",
			url:       "http://127.0.0.1:19790",
			relocated: true,
		}),
	)

	require.Nil(t, adminStartupLines("127.0.0.1:19789", nil))
}

func TestRuntimeStartupLines(t *testing.T) {
	t.Parallel()

	lines := runtimeStartupLines(
		runOptions{
			AppName:        "openclaw-stdin",
			ConfigPath:     "openclaw.yaml",
			ModelMode:      modeMock,
			SessionBackend: sessionBackendInMemory,
			MemoryBackend:  memoryBackendInMemory,
		},
		"/tmp/state",
		[]occhannel.Channel{
			&stubChannel{id: "stdin"},
			&stubChannel{id: "telegram"},
		},
		true,
	)
	require.Equal(t,
		[]startupLogLine{
			{text: "App name: openclaw-stdin"},
			{text: "Config: " + filepath.Join(
				mustGetwd(t),
				"openclaw.yaml",
			)},
			{text: "State dir: /tmp/state"},
			{text: "Channels: stdin, telegram"},
			{text: "Model: mock"},
			{text: "Storage: session=inmemory memory=inmemory"},
		},
		lines,
	)
}

func TestRuntimeStartupLinesWithoutConfigFile(t *testing.T) {
	t.Parallel()

	lines := runtimeStartupLines(
		runOptions{
			AppName:        "openclaw",
			ModelMode:      modeOpenAI,
			OpenAIModel:    "gpt-5",
			SessionBackend: sessionBackendSQLite,
			MemoryBackend:  memoryBackendSQLite,
		},
		"/tmp/state",
		nil,
		false,
	)
	require.Equal(t,
		[]startupLogLine{
			{text: "App name: openclaw"},
			{text: "Config: built-in defaults and CLI flags"},
			{text: "State dir: /tmp/state"},
			{text: "Channels: none"},
			{text: "Model: disabled"},
			{text: "Storage: session=sqlite memory=sqlite"},
		},
		lines,
	)
}

func mustGetwd(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
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

func TestCloseSessionService_CloseErrorDoesNotPanic(t *testing.T) {
	t.Parallel()

	closeSessionService(stubCloser{closeErr: errors.New("boom")})
}

func TestCloseMemoryService_CloseErrorDoesNotPanic(t *testing.T) {
	t.Parallel()

	closeMemoryService(stubCloser{closeErr: errors.New("boom")})
}

func TestCloseToolSets_CloseErrorDoesNotPanic(t *testing.T) {
	t.Parallel()

	toolSetClosed := false
	closeToolSets([]tool.ToolSet{
		nil,
		&stubToolSet{
			name:     "stub",
			closeErr: errors.New("boom"),
			closed:   &toolSetClosed,
		},
	})
	require.True(t, toolSetClosed)
}

func TestRuntime_Close_ReturnsRunnerCloseError(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("runner close boom")
	runnerClosed := false

	rt := &Runtime{
		runner: &stubRunner{
			closeErr: closeErr,
			closed:   &runnerClosed,
		},
	}

	require.ErrorIs(t, rt.Close(), closeErr)
	require.True(t, runnerClosed)
}

func TestRuntime_Close_JoinsTelemetryShutdownError(t *testing.T) {
	t.Parallel()

	runnerErr := errors.New("runner close boom")
	telemetryErr := errors.New("telemetry shutdown boom")
	runnerClosed := false
	telemetryClosed := false

	rt := &Runtime{
		runner: &stubRunner{
			closeErr: runnerErr,
			closed:   &runnerClosed,
		},
		telemetryShutdown: func(context.Context) error {
			telemetryClosed = true
			return telemetryErr
		},
	}

	err := rt.Close()
	require.ErrorIs(t, err, runnerErr)
	require.ErrorIs(t, err, telemetryErr)
	require.True(t, runnerClosed)
	require.True(t, telemetryClosed)
}

func TestRuntime_Close_NilIsNoop(t *testing.T) {
	t.Parallel()

	var rt *Runtime
	require.NoError(t, rt.Close())
}

func TestRuntime_Close_NilRunnerIsNoop(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	require.NoError(t, rt.Close())
}

func TestShutdownTelemetryWithContext_NilContextUsesBackground(
	t *testing.T,
) {
	t.Parallel()

	called := false
	err := shutdownTelemetryWithContext(
		nil,
		func(ctx context.Context) error {
			called = true
			require.NotNil(t, ctx)
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, called)
}

func TestNewRuntime_LangfuseRequiredErrorExitCode(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return nil, errors.New("langfuse boom")
	}

	cfgPath := writeTempConfig(t, `
observability:
  langfuse:
    enabled: true
    required: true
`)

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
	})
	require.Nil(t, rt)
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "langfuse config failed")
}

func TestNewRuntime_WithLangfuse_Smoke(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})

	startCalls := 0
	shutdownCalls := 0
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error {
			shutdownCalls++
			return nil
		}, nil
	}

	cfgPath := writeTempConfig(t, `
observability:
  langfuse:
    enabled: true
`)

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
	})
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.NotNil(t, rt.telemetryShutdown)
	require.Equal(t, 1, startCalls)
	require.NoError(t, rt.Close())
	require.Equal(t, 1, shutdownCalls)
}

func TestNewRuntime_ClosesLangfuseOnLaterError(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})

	shutdownCalls := 0
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return func(context.Context) error {
			shutdownCalls++
			return nil
		}, nil
	}

	cfgPath := writeTempConfig(t, `
channels:
  - type: missing_channel
observability:
  langfuse:
    enabled: true
`)

	rt, err := NewRuntime(context.Background(), []string{
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
	})
	require.Nil(t, rt)
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "create channels failed")
	require.Equal(t, 1, shutdownCalls)
}

func TestRun_LangfuseRequiredErrorExitCode(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return nil, errors.New("langfuse boom")
	}

	cfgPath := writeTempConfig(t, `
observability:
  langfuse:
    enabled: true
    required: true
`)

	err := run(context.Background(), []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "langfuse config failed")
}

func TestRun_WithLangfuse_Smoke(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})

	startCalls := 0
	shutdownCalls := 0
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		startCalls++
		return func(context.Context) error {
			shutdownCalls++
			return nil
		}, nil
	}

	cfgPath := writeTempConfig(t, `
observability:
  langfuse:
    enabled: true
`)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	time.AfterFunc(50*time.Millisecond, cancel)

	err := run(ctx, []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
	})
	require.NoError(t, err)
	require.Equal(t, 1, startCalls)
	require.Equal(t, 1, shutdownCalls)
}

func TestRun_ClosesLangfuseOnLaterError(t *testing.T) {
	restore := langfuseStart
	t.Cleanup(func() {
		langfuseStart = restore
	})

	shutdownCalls := 0
	langfuseStart = func(
		context.Context,
		...langfuseobs.Option,
	) (func(context.Context) error, error) {
		return func(context.Context) error {
			shutdownCalls++
			return nil
		}, nil
	}

	cfgPath := writeTempConfig(t, `
channels:
  - type: missing_channel
observability:
  langfuse:
    enabled: true
`)

	err := run(context.Background(), []string{
		"-http-addr", "127.0.0.1:0",
		"-mode", modeMock,
		"-state-dir", t.TempDir(),
		"-skills-root", t.TempDir(),
		"-config", cfgPath,
	})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 1, exitErr.Code)
	require.ErrorContains(t, exitErr.Err, "create channels failed")
	require.Equal(t, 1, shutdownCalls)
}

func TestInProcGatewayClient_SendMessage_OK(t *testing.T) {
	t.Parallel()

	const (
		testReply     = "ok"
		testRequestID = "req-1"
	)

	srv, err := gateway.New(&inProcGWTestRunner{
		reply:     testReply,
		requestID: testRequestID,
	})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")

	rsp, err := c.SendMessage(context.Background(), gwclient.MessageRequest{
		From: "u1",
		Text: "hi",
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rsp.StatusCode)
	require.Equal(t, testReply, rsp.Reply)
	require.Equal(t, testRequestID, rsp.RequestID)
	require.NotEmpty(t, rsp.SessionID)
}

func TestInProcGatewayClient_SendMessage_StatusError(t *testing.T) {
	t.Parallel()

	const (
		wantErr = "gwclient: status 400: invalid_request: " +
			"missing user_id or from"
	)

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")

	_, err = c.SendMessage(context.Background(), gwclient.MessageRequest{
		Text: "hi",
	})
	require.Error(t, err)
	require.Equal(t, wantErr, err.Error())
}

func TestInProcGatewayClient_StreamMessage_OK(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{
		reply:     "ok",
		requestID: "req-1",
	})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	stream, err := c.StreamMessage(context.Background(), gwclient.MessageRequest{
		From: "u1",
		Text: "hi",
	})
	require.NoError(t, err)

	var events []gwclient.StreamEvent
	for evt := range stream {
		events = append(events, evt)
	}
	require.Len(t, events, 5)
	require.Equal(t, gwproto.StreamEventTypeRunStarted, events[0].Type)
	require.Equal(t, gwproto.StreamEventTypeRunProgress, events[1].Type)
	require.Equal(t, gwproto.StreamEventTypeMessageDelta, events[2].Type)
	require.Equal(t, gwproto.StreamEventTypeMessageCompleted, events[3].Type)
	require.Equal(t, gwproto.StreamEventTypeRunCompleted, events[4].Type)
}

func TestInProcGatewayClient_StreamMessage_StatusError(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	_, err = c.StreamMessage(context.Background(), gwclient.MessageRequest{
		Text: "hi",
	})
	require.Error(t, err)
	require.Equal(
		t,
		"gwclient: status 400: invalid_request: missing user_id or from",
		err.Error(),
	)
}

func TestInProcGatewayClient_NilServerFails(t *testing.T) {
	t.Parallel()

	var c *inProcGatewayClient

	_, err := c.SendMessage(context.Background(), gwclient.MessageRequest{
		From: "u1",
		Text: "hi",
	})
	require.Error(t, err)
	require.Equal(t, errNilGatewayServer, err.Error())

	_, err = c.Cancel(context.Background(), "req-1")
	require.Error(t, err)
	require.Equal(t, errNilGatewayServer, err.Error())

	_, err = c.StreamMessage(context.Background(), gwclient.MessageRequest{
		From: "u1",
		Text: "hi",
	})
	require.Error(t, err)
	require.Equal(t, errNilGatewayServer, err.Error())
}

func TestInProcGatewayClient_Cancel_OK(t *testing.T) {
	t.Parallel()

	const testRequestID = "req-1"

	r := &inProcGWTestManagedRunner{cancelOK: testRequestID}
	srv, err := gateway.New(r)
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")

	canceled, err := c.Cancel(context.Background(), testRequestID)
	require.NoError(t, err)
	require.True(t, canceled)
}

func TestInProcGatewayClient_Cancel_Unsupported(t *testing.T) {
	t.Parallel()

	const wantErr = "gwclient: status 501: unsupported: " +
		"runner does not support cancel"

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")

	_, err = c.Cancel(context.Background(), "req-1")
	require.Error(t, err)
	require.Equal(t, wantErr, err.Error())
}

func TestInProcGatewayClient_ForgetUser_DeletesState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	sessSvc := sessioninmemory.NewSessionService()
	memSvc := meminmemory.NewMemoryService()

	const (
		channelName = "telegram"
		userID      = "u1"
	)

	_, err := sessSvc.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: "s1",
	}, nil)
	require.NoError(t, err)
	_, err = sessSvc.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: "s2",
	}, nil)
	require.NoError(t, err)

	err = memSvc.AddMemory(
		ctx,
		memory.UserKey{AppName: appName, UserID: userID},
		"remember",
		nil,
	)
	require.NoError(t, err)

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	debugDir := t.TempDir()
	rec, err := debugrecorder.New(debugDir, mode)
	require.NoError(t, err)

	trace, err := rec.Start(debugrecorder.TraceStart{
		AppName:   appName,
		Channel:   channelName,
		UserID:    userID,
		SessionID: "sid",
	})
	require.NoError(t, err)
	require.NotNil(t, trace)
	traceDir := trace.Dir()
	require.NotEmpty(t, traceDir)
	require.NoError(t, trace.Close(debugrecorder.TraceEnd{Status: "ok"}))

	otherTrace, err := rec.Start(debugrecorder.TraceStart{
		AppName:   appName,
		Channel:   channelName,
		UserID:    "u2",
		SessionID: "sid2",
	})
	require.NoError(t, err)
	require.NotNil(t, otherTrace)
	otherTraceDir := otherTrace.Dir()
	require.NotEmpty(t, otherTraceDir)
	require.NoError(
		t,
		otherTrace.Close(debugrecorder.TraceEnd{Status: "ok"}),
	)

	uploadStore, err := uploads.NewStore(debugDir)
	require.NoError(t, err)
	saved, err := uploadStore.Save(
		ctx,
		uploads.Scope{
			Channel:   channelName,
			UserID:    userID,
			SessionID: "sid",
		},
		"report.pdf",
		[]byte("pdf"),
	)
	require.NoError(t, err)

	personaPath, err := persona.DefaultStorePath(debugDir)
	require.NoError(t, err)
	personaStore, err := persona.NewStore(personaPath)
	require.NoError(t, err)
	_, err = personaStore.Set(
		ctx,
		persona.DMScopeKey(channelName, userID),
		persona.PresetCoach,
	)
	require.NoError(t, err)

	memoryRoot, err := memoryfile.DefaultRoot(debugDir)
	require.NoError(t, err)
	memoryStore, err := memoryfile.NewStore(memoryRoot)
	require.NoError(t, err)
	memoryPath, err := memoryStore.EnsureMemory(
		ctx,
		appName,
		userID,
	)
	require.NoError(t, err)
	otherUserMemoryPath, err := memoryStore.EnsureMemory(
		ctx,
		appName,
		"u2",
	)
	require.NoError(t, err)
	otherAppMemoryPath, err := memoryStore.EnsureMemory(
		ctx,
		"other-app",
		userID,
	)
	require.NoError(t, err)

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(
		srv,
		appName,
		sessSvc,
		memSvc,
		debugDir,
		uploadStore,
	)
	c.SetPersonaStore(personaStore)
	c.SetMemoryFileStore(memoryStore)

	require.NoError(t, c.ForgetUser(ctx, channelName, userID))

	sessions, err := sessSvc.ListSessions(ctx, session.UserKey{
		AppName: appName,
		UserID:  userID,
	})
	require.NoError(t, err)
	require.Empty(t, sessions)

	memories, err := memSvc.ReadMemories(
		ctx,
		memory.UserKey{AppName: appName, UserID: userID},
		10,
	)
	require.NoError(t, err)
	require.Empty(t, memories)

	_, err = os.Stat(traceDir)
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	_, err = os.Stat(saved.Path)
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))

	_, err = os.Stat(otherTraceDir)
	require.NoError(t, err)

	currentPreset, err := personaStore.Get(
		persona.DMScopeKey(channelName, userID),
	)
	require.NoError(t, err)
	require.Equal(t, persona.PresetDefault, currentPreset.ID)

	_, err = os.Stat(memoryPath)
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = os.Stat(otherUserMemoryPath)
	require.NoError(t, err)

	_, err = os.Stat(otherAppMemoryPath)
	require.NoError(t, err)
}

func TestInProcGatewayClient_ForgetUser_ClearsCronJobsOnlyOnce(
	t *testing.T,
) {
	t.Parallel()

	ctx := context.Background()
	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	sessSvc := sessioninmemory.NewSessionService()
	memSvc := meminmemory.NewMemoryService()
	const userID = "u1"

	_, err = sessSvc.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: "dm:1",
	}, nil)
	require.NoError(t, err)
	_, err = sessSvc.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: "cron:job-1:1",
	}, nil)
	require.NoError(t, err)

	cronSvc, err := cron.NewService(
		t.TempDir(),
		&inProcGWTestRunner{},
		outbound.NewRouter(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	_, err = cronSvc.Add(&cron.Job{
		Name:    "report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  userID,
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, sessSvc, memSvc, "")
	c.SetCronService(cronSvc)

	require.NoError(t, c.ForgetUser(ctx, "telegram", userID))

	sessions, err := sessSvc.ListSessions(ctx, session.UserKey{
		AppName: appName,
		UserID:  userID,
	})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, "cron:job-1:1", sessions[0].ID)
	require.Empty(
		t,
		cronSvc.ListForUser(
			userID,
			outbound.DeliveryTarget{Channel: "telegram"},
		),
	)
}

func TestInProcGatewayClient_ForgetUser_ValidationErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")

	require.NoError(t, c.ForgetUser(nil, "telegram", "u1"))

	err = c.ForgetUser(ctx, " ", "u1")
	require.Error(t, err)
	require.Equal(t, errEmptyForgetChannel, err.Error())

	err = c.ForgetUser(ctx, "telegram", " ")
	require.Error(t, err)
	require.Equal(t, errEmptyForgetUserID, err.Error())

	c2 := newInProcGatewayClient(srv, " ", nil, nil, "")
	err = c2.ForgetUser(ctx, "telegram", "u1")
	require.ErrorIs(t, err, session.ErrAppNameRequired)

	c3 := newInProcGatewayClient(nil, appName, nil, nil, "")
	err = c3.ForgetUser(ctx, "telegram", "u1")
	require.Error(t, err)
	require.Equal(t, errNilGatewayServer, err.Error())

	debugFile := filepath.Join(t.TempDir(), "debug.txt")
	require.NoError(t, os.WriteFile(debugFile, []byte("x"), 0o600))

	c4 := newInProcGatewayClient(srv, appName, nil, nil, debugFile)
	require.NoError(t, c4.ForgetUser(ctx, "telegram", "u1"))
}

func TestInProcGatewayClient_SetMemoryFileStore_NilReceiverIsNoop(t *testing.T) {
	t.Parallel()

	var client *inProcGatewayClient
	client.SetMemoryFileStore(nil)
}

func TestInProcGatewayClient_ForgetUser_DeleteMemoryFilesError(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	root, err := memoryfile.DefaultRoot(t.TempDir())
	require.NoError(t, err)
	store, err := memoryfile.NewStore(root)
	require.NoError(t, err)
	_, err = store.EnsureMemory(context.Background(), appName, "u1")
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	c.SetMemoryFileStore(store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = c.ForgetUser(ctx, "telegram", "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "forget: delete user memory files")
}

func TestInProcGatewayClient_ScheduledJobs(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	router := outbound.NewRouter()
	now := time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC)
	cronSvc, err := cron.NewService(
		t.TempDir(),
		&inProcGWTestRunner{},
		router,
		cron.WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	job, err := cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  "u1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
		LastStatus: cron.StatusSucceeded,
	})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	c.SetCronService(cronSvc)

	jobs, err := c.ListScheduledJobs(
		context.Background(),
		"telegram",
		"u1",
		"100",
	)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, job.ID, jobs[0].ID)
	require.Equal(t, "cpu report", jobs[0].Name)
	require.Equal(t, "every 1m", jobs[0].Schedule)
	require.Equal(t, cron.StatusSucceeded, jobs[0].LastStatus)
	require.WithinDuration(t, now.Add(time.Minute), *jobs[0].NextRunAt, 0)

	removed, err := c.ClearScheduledJobs(
		context.Background(),
		"telegram",
		"u1",
		"100",
	)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.Len(
		t,
		cronSvc.ListForUser("u1", outbound.DeliveryTarget{}),
		0,
	)
}

func TestInProcGatewayClient_ScheduledJobs_RequireCronService(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")

	_, err = c.ListScheduledJobs(
		context.Background(),
		"telegram",
		"u1",
		"100",
	)
	require.Error(t, err)
	require.Equal(t, errNilCronService, err.Error())

	_, err = c.ClearScheduledJobs(
		context.Background(),
		"telegram",
		"u1",
		"100",
	)
	require.Error(t, err)
	require.Equal(t, errNilCronService, err.Error())

	_, err = c.SetScheduledJobEnabled(
		context.Background(),
		"telegram",
		"u1",
		"100",
		"job-1",
		false,
	)
	require.Error(t, err)
	require.Equal(t, errNilCronService, err.Error())

	_, err = c.RemoveScheduledJob(
		context.Background(),
		"telegram",
		"u1",
		"100",
		"job-1",
	)
	require.Error(t, err)
	require.Equal(t, errNilCronService, err.Error())
}

func TestInProcGatewayClient_ManageScheduledJob(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC)
	cronSvc, err := cron.NewService(
		t.TempDir(),
		&inProcGWTestRunner{},
		nil,
		cron.WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	job, err := cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  "u1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
		LastStatus: cron.StatusSucceeded,
		LastOutput: "ok",
	})
	require.NoError(t, err)

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	c.SetCronService(cronSvc)

	updated, err := c.SetScheduledJobEnabled(
		context.Background(),
		"telegram",
		"u1",
		"100",
		job.ID,
		false,
	)
	require.NoError(t, err)
	require.False(t, updated.Enabled)
	require.Equal(t, "collect cpu", updated.Message)
	require.Equal(t, "ok", updated.LastOutput)
	require.Equal(t, "telegram", updated.DeliveryChannel)
	require.Equal(t, "100", updated.DeliveryTarget)

	removed, err := c.RemoveScheduledJob(
		context.Background(),
		"telegram",
		"u1",
		"100",
		job.ID,
	)
	require.NoError(t, err)
	require.True(t, removed)
	require.Nil(t, cronSvc.Get(job.ID))
}

func TestInProcGatewayClient_ScheduledJobScopeErrors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 6, 16, 0, 0, 0, time.UTC)
	cronSvc, err := cron.NewService(
		t.TempDir(),
		&inProcGWTestRunner{},
		nil,
		cron.WithClock(func() time.Time { return now }),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cronSvc.Close())
	})

	_, err = cronSvc.Add(&cron.Job{
		Name:    "cpu report",
		Enabled: true,
		Schedule: cron.Schedule{
			Kind:  cron.ScheduleKindEvery,
			Every: "1m",
		},
		Message: "collect cpu",
		UserID:  "u1",
		Delivery: outbound.DeliveryTarget{
			Channel: "telegram",
			Target:  "100",
		},
	})
	require.NoError(t, err)

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	c.SetCronService(cronSvc)

	_, err = c.SetScheduledJobEnabled(
		context.Background(),
		"telegram",
		"u1",
		"999",
		"",
		false,
	)
	require.ErrorContains(t, err, errUnknownJob)

	removed, err := c.RemoveScheduledJob(
		context.Background(),
		"telegram",
		"u1",
		"999",
		"job-1",
	)
	require.ErrorContains(t, err, errUnknownJob)
	require.False(t, removed)
}

func TestInProcGatewayClient_PresetPersona(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	personaPath, err := persona.DefaultStorePath(t.TempDir())
	require.NoError(t, err)
	personaStore, err := persona.NewStore(personaPath)
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, "")
	c.SetPersonaStore(personaStore)

	scopeKey := persona.DMScopeKey("telegram", "u1")
	preset, err := c.SetPresetPersona(
		context.Background(),
		scopeKey,
		persona.PresetGirlfriend,
	)
	require.NoError(t, err)
	require.Equal(t, persona.PresetGirlfriend, preset.ID)

	got, err := c.GetPresetPersona(context.Background(), scopeKey)
	require.NoError(t, err)
	require.Equal(t, persona.PresetGirlfriend, got.ID)

	require.NotEmpty(t, c.ListPresetPersonas())
}

type errSessionService struct {
	session.Service

	listErr   error
	deleteErr error
}

func (s errSessionService) ListSessions(
	ctx context.Context,
	userKey session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.Service.ListSessions(ctx, userKey, options...)
}

func (s errSessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.Service.DeleteSession(ctx, key, options...)
}

type errMemoryService struct {
	memory.Service

	clearErr error
}

func (m errMemoryService) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	if m.clearErr != nil {
		return m.clearErr
	}
	return m.Service.ClearMemories(ctx, userKey)
}

func TestInProcGatewayClient_ForgetUser_ListSessionsError(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	sessSvc := errSessionService{
		Service:   sessioninmemory.NewSessionService(),
		listErr:   errors.New("list boom"),
		deleteErr: nil,
	}
	c := newInProcGatewayClient(srv, appName, sessSvc, nil, "")

	err = c.ForgetUser(context.Background(), "telegram", "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "forget: list sessions")
	require.Contains(t, err.Error(), "list boom")
}

func TestInProcGatewayClient_ForgetUser_DeleteSessionError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	base := sessioninmemory.NewSessionService()
	_, err = base.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    "u1",
		SessionID: "s1",
	}, nil)
	require.NoError(t, err)

	sessSvc := errSessionService{
		Service:   base,
		deleteErr: errors.New("delete boom"),
	}
	c := newInProcGatewayClient(srv, appName, sessSvc, nil, "")

	err = c.ForgetUser(ctx, "telegram", "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "forget: delete session")
	require.Contains(t, err.Error(), "delete boom")
}

func TestInProcGatewayClient_ForgetUser_ClearMemoriesError(t *testing.T) {
	t.Parallel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	base := meminmemory.NewMemoryService()
	memSvc := errMemoryService{
		Service:  base,
		clearErr: errors.New("clear boom"),
	}
	c := newInProcGatewayClient(srv, appName, nil, memSvc, "")

	err = c.ForgetUser(context.Background(), "telegram", "u1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "forget: clear memories")
	require.Contains(t, err.Error(), "clear boom")
}

func TestInProcGatewayClient_ForgetUser_DeleteDebugTracesError(t *testing.T) {
	t.Parallel()

	mode, err := debugrecorder.ParseMode("safe")
	require.NoError(t, err)

	dir := t.TempDir()
	rec, err := debugrecorder.New(dir, mode)
	require.NoError(t, err)

	const (
		channelName = "telegram"
		userID      = "u1"
	)

	trace, err := rec.Start(debugrecorder.TraceStart{
		AppName:   appName,
		Channel:   channelName,
		UserID:    userID,
		SessionID: "s1",
	})
	require.NoError(t, err)
	require.NotNil(t, trace)
	traceDir := trace.Dir()
	require.NoError(t, trace.Close(debugrecorder.TraceEnd{Status: "ok"}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	srv, err := gateway.New(&inProcGWTestRunner{})
	require.NoError(t, err)

	c := newInProcGatewayClient(srv, appName, nil, nil, rec.Dir())
	err = c.ForgetUser(ctx, channelName, userID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "forget: delete debug traces")
	require.Contains(t, err.Error(), "context canceled")

	_, err = os.Stat(traceDir)
	require.NoError(t, err)
}

func TestDeleteDebugTraces_MissingDirNoError(t *testing.T) {
	t.Parallel()

	debugDir := filepath.Join(t.TempDir(), "missing")
	err := deleteDebugTraces(
		context.Background(),
		debugDir,
		"telegram",
		appName,
		"u1",
	)
	require.NoError(t, err)
}

func TestDeleteDebugTraces_IgnoresBadMeta(t *testing.T) {
	t.Parallel()

	debugDir := t.TempDir()

	require.NoError(t, os.WriteFile(
		filepath.Join(debugDir, "notes.txt"),
		[]byte("ignored"),
		0o600,
	))

	rootMeta := `{"start":{"app_name":"openclaw","channel":"telegram",
"user_id":"u1"}}`
	require.NoError(t, os.WriteFile(
		filepath.Join(debugDir, debugTraceMetaFile),
		[]byte(rootMeta),
		0o600,
	))

	badReadDir := filepath.Join(debugDir, "badread")
	require.NoError(t, os.MkdirAll(badReadDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(badReadDir, debugTraceMetaFile),
		[]byte("secret"),
		0,
	))

	badJSONDir := filepath.Join(debugDir, "badjson")
	require.NoError(t, os.MkdirAll(badJSONDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(badJSONDir, debugTraceMetaFile),
		[]byte("{"),
		0o600,
	))

	mismatchDir := filepath.Join(debugDir, "mismatch")
	require.NoError(t, os.MkdirAll(mismatchDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(mismatchDir, debugTraceMetaFile),
		[]byte(`{"start":{"app_name":"other"}}`),
		0o600,
	))

	err := deleteDebugTraces(
		context.Background(),
		debugDir,
		"telegram",
		appName,
		"u1",
	)
	require.NoError(t, err)
}

func TestCompactStrings_Deduplicates(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		[]string{"a", "b"},
		compactStrings([]string{"a", "a", "b", "b"}),
	)
}

func TestErrorForGWStatus_NilAPIError(t *testing.T) {
	t.Parallel()

	err := errorForGWStatus(http.StatusInternalServerError, nil)
	require.Error(t, err)
	require.Equal(t, "gwclient: status 500", err.Error())
}

type stubCloser struct {
	closeErr error
}

func (s stubCloser) Close() error { return s.closeErr }

type stubToolSet struct {
	name     string
	closeErr error
	closed   *bool
}

func (s *stubToolSet) Tools(context.Context) []tool.Tool { return nil }

func (s *stubToolSet) Close() error {
	if s.closed != nil {
		*s.closed = true
	}
	return s.closeErr
}

func (s *stubToolSet) Name() string { return s.name }

type stubRunner struct {
	closeErr error
	closed   *bool
}

func (s *stubRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	return nil, nil
}

func (s *stubRunner) Close() error {
	if s.closed != nil {
		*s.closed = true
	}
	return s.closeErr
}

type inProcGWTestRunner struct {
	reply     string
	requestID string
}

func (r *inProcGWTestRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	reply := r.reply
	if reply == "" {
		reply = "ok"
	}
	requestID := r.requestID
	if requestID == "" {
		requestID = "req-1"
	}

	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{
				{Message: model.NewAssistantMessage(reply)},
			},
			Done: true,
		},
		RequestID: requestID,
	}
	close(ch)
	return ch, nil
}

func (r *inProcGWTestRunner) Close() error { return nil }

type inProcGWTestManagedRunner struct {
	inProcGWTestRunner
	cancelOK string
}

func (r *inProcGWTestManagedRunner) Cancel(requestID string) bool {
	return requestID == r.cancelOK
}

func (r *inProcGWTestManagedRunner) RunStatus(
	string,
) (runner.RunStatus, bool) {
	return runner.RunStatus{}, false
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
		context.Background(),
		stubGateway{},
		"demo",
		"/state",
		nil,
		[]pluginSpec{{Type: " "}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "channels[0].type is empty")
}

func TestChannelsFromRegistry_UnsupportedTypeFails(t *testing.T) {
	t.Parallel()

	_, err := channelsFromRegistry(
		context.Background(),
		stubGateway{},
		"demo",
		"/state",
		nil,
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
		context.Background(),
		stubGateway{},
		"demo",
		"/state",
		nil,
		[]pluginSpec{{Type: typeName}},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "channel")
	require.Contains(t, err.Error(), "boom")
}
