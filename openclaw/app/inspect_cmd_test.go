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
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRunInspect_DefaultIsPlugins(t *testing.T) {
	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect(nil))
	})

	require.Empty(t, stderr)
	require.Contains(t, stdout, "Model types")
	require.Contains(t, stdout, "mock")
	require.Contains(t, stdout, "telegram")
}

func TestRunInspect_UnknownCommand(t *testing.T) {
	_, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 2, runInspect([]string{"nope"}))
	})

	require.Contains(t, stderr, "unknown inspect command")
	require.Contains(t, stderr, "Usage:")
}

func TestRunInspect_ConfigKeys(t *testing.T) {
	stdout, stderr := captureInspectOutput(t, func() {
		require.Equal(t, 0, runInspect([]string{
			inspectCmdConfigKeys,
			"-telegram-token",
			"x",
			"-enable-openclaw-tools",
		}))
	})

	require.Empty(t, stderr)

	got := strings.Split(strings.TrimSpace(stdout), "\n")
	want := []string{
		"channels.telegram",
		"channels.telegram.token",
		"plugins.entries.telegram.config",
		"plugins.entries.telegram.config.token",
		"plugins.entries.telegram.enabled",
		"tools.bash",
		"tools.exec",
		"tools.process",
	}
	require.Equal(t, want, got)
}

func captureInspectOutput(
	t *testing.T,
	fn func(),
) (stdout string, stderr string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = outW
	os.Stderr = errW
	t.Cleanup(func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	})

	fn()

	require.NoError(t, outW.Close())
	require.NoError(t, errW.Close())

	out, err := io.ReadAll(outR)
	require.NoError(t, err)
	errOut, err := io.ReadAll(errR)
	require.NoError(t, err)

	return string(out), string(errOut)
}

func TestResolveSkillConfigKeys_IncludesPluginAndYAMLKeys(t *testing.T) {
	myChanConfig := mustYAMLNode(t, `
enabled: true
token: x
list:
  - 0
  - 1
emptymap: {}
emptyseq: []
"": true
`)
	providerConfig := mustYAMLNode(t, "x: 1\n")
	toolSetConfig := mustYAMLNode(t, "- true\n")

	opts := runOptions{
		TelegramToken:       "x",
		EnableOpenClawTools: true,
		EnableLocalExec:     true,
		Channels: []pluginSpec{
			{Type: "  MyChan ", Config: myChanConfig},
			{Type: "  ", Config: myChanConfig},
		},
		ToolProviders: []pluginSpec{
			{Type: "Provider", Config: providerConfig},
		},
		ToolSets: []pluginSpec{
			{Type: "ToolSet", Config: toolSetConfig},
		},
	}

	keys := resolveSkillConfigKeys(opts)

	require.Contains(t, keys, "channels.mychan")
	require.Contains(t, keys, "channels.mychan.enabled")
	require.Contains(t, keys, "channels.mychan.token")
	require.Contains(t, keys, "channels.mychan.list")
	require.NotContains(t, keys, "channels.mychan.emptymap")
	require.NotContains(t, keys, "channels.mychan.emptyseq")

	require.Contains(t, keys, "plugins.entries.mychan.enabled")
	require.Contains(t, keys, "plugins.entries.mychan.config")
	require.Contains(t, keys, "plugins.entries.mychan.config.enabled")
	require.Contains(t, keys, "plugins.entries.mychan.config.token")
	require.Contains(t, keys, "plugins.entries.mychan.config.list")

	require.Contains(t, keys, "tools.providers.provider")
	require.Contains(t, keys, "tools.providers.provider.x")
	require.Contains(t, keys, "plugins.entries.provider.enabled")
	require.Contains(t, keys, "plugins.entries.provider.config")

	require.Contains(t, keys, "tools.toolsets.toolset")
	require.Contains(t, keys, "plugins.entries.toolset.enabled")

	require.Contains(t, keys, "tools.exec")
	require.Contains(t, keys, "tools.local_exec")
}

func TestAddYAMLConfigKeys_RejectsFalseyMapping(t *testing.T) {
	set := map[string]struct{}{}
	node := mustYAMLNode(t, `
a: false
b: 0
c: 0.0
`)
	require.False(t, addYAMLConfigKeys(set, "root", node))
	require.Empty(t, set)
}

func TestAddYAMLConfigKeys_CoversEdgeCases(t *testing.T) {
	set := map[string]struct{}{}
	require.False(t, addYAMLConfigKeys(set, "", &yaml.Node{}))
	require.False(t, addYAMLConfigKeys(set, "root", nil))
	require.False(t,
		addYAMLConfigKeys(set, "root", &yaml.Node{Kind: yaml.DocumentNode}),
	)
	require.False(t,
		addYAMLConfigKeys(set, "root", &yaml.Node{Kind: 123}),
	)
}

func TestAddYAMLConfigKeys_AliasUsesTargetNode(t *testing.T) {
	set := map[string]struct{}{}
	aliasTarget := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!bool",
		Value: "true",
	}
	aliasNode := &yaml.Node{
		Kind:  yaml.AliasNode,
		Alias: aliasTarget,
	}

	require.True(t, addYAMLConfigKeys(set, "root", aliasNode))
	_, ok := set["root"]
	require.True(t, ok)
}

func TestAddConfigKey_IgnoresNilAndBlank(t *testing.T) {
	addConfigKey(nil, "x")

	set := map[string]struct{}{}
	addConfigKey(set, "  ")
	require.Empty(t, set)

	addConfigKey(set, "  a ")
	_, ok := set["a"]
	require.True(t, ok)
}

func TestIsTruthyScalar_RejectsNilAndInvalidValues(t *testing.T) {
	require.False(t, isTruthyScalar(nil))
	require.False(t, isTruthyScalar(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!bool",
		Value: "   ",
	}))
	require.False(t, isTruthyScalar(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!int",
		Value: "nope",
	}))
	require.False(t, isTruthyScalar(&yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!float",
		Value: "nope",
	}))
}

func mustYAMLNode(t *testing.T, raw string) *yaml.Node {
	t.Helper()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(raw), &node))
	return &node
}
