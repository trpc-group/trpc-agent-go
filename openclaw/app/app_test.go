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

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func TestRun_ParseErrorExitCode(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"-unknown-flag"})
	require.Error(t, err)

	var exitErr *exitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 2, exitErr.Code)
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
	mdl, err := newModel(modeMock, "ignored", openAIVariantAuto)
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
	mdl, err := newModel(modeOpenAI, "gpt-5", openAIVariantAuto)
	require.NoError(t, err)
	require.Equal(t, "gpt-5", mdl.Info().Name)
}

func TestNewModel_UnsupportedMode(t *testing.T) {
	_, err := newModel("x", "gpt-5", openAIVariantAuto)
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
