//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

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
