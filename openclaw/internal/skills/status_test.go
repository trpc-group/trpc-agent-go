//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skills

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildStatus_ReportsMissingRequirements(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "weather-probe", `---
name: weather-probe
description: "Probe weather prerequisites"
metadata:
  openclaw:
    skillKey: "weather-api"
    primaryEnv: "OPENAI_API_KEY"
    emoji: "⛅"
    homepage: "https://example.com/weather"
    requires:
      bins: ["definitely-missing-bin"]
      anyBins: ["definitely-missing-a", "definitely-missing-b"]
      env: ["OPENAI_API_KEY"]
      config: ["channels.telegram.token"]
---

# weather-probe
`)

	report, err := BuildStatus([]string{root})
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)

	entry := report.Skills[0]
	require.Equal(t, "weather-probe", entry.Name)
	require.Equal(t, "weather-api", entry.SkillKey)
	require.False(t, entry.Eligible)
	require.False(t, entry.Disabled)
	require.Equal(t, []string{"definitely-missing-bin"}, entry.Missing.Bins)
	require.Equal(
		t,
		[]string{"definitely-missing-a", "definitely-missing-b"},
		entry.Missing.AnyBins,
	)
	require.Equal(t, []string{"OPENAI_API_KEY"}, entry.Missing.Env)
	require.Equal(t, []string{"channels.telegram.token"}, entry.Missing.Config)
	require.Equal(t, "OPENAI_API_KEY", entry.PrimaryEnv)
	require.Equal(t, "https://example.com/weather", entry.Homepage)
}

func TestBuildStatus_HonorsDisabledConfigAndPrimaryAPIKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSkill(t, root, "needkey", `---
name: needkey
description: "Needs key"
metadata:
  openclaw:
    skillKey: "needkey"
    primaryEnv: "OPENAI_API_KEY"
    requires:
      env: ["OPENAI_API_KEY"]
---

# needkey
`)

	disabled := false
	report, err := BuildStatus(
		[]string{root},
		WithSkillConfigs(map[string]SkillConfig{
			"needkey": {
				Enabled: &disabled,
				APIKey:  "secret",
			},
		}),
	)
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)

	entry := report.Skills[0]
	require.True(t, entry.Disabled)
	require.False(t, entry.Eligible)
	require.Empty(t, entry.Missing.Env)
}

func TestStatusHelpers_MissingRequirementsAndInstallers(t *testing.T) {
	t.Parallel()

	require.Nil(t, missingOS([]string{runtime.GOOS}))
	require.Equal(
		t,
		[]string{"definitely-not-this-os"},
		missingOS([]string{"definitely-not-this-os"}),
	)
	require.Equal(
		t,
		[]string{"definitely-missing-bin"},
		missingBins([]string{"", " definitely-missing-bin "}),
	)
	require.Nil(t, missingAnyBins([]string{"go", "definitely-missing"}))
	require.Equal(
		t,
		[]string{"definitely-missing-a", "definitely-missing-b"},
		missingAnyBins([]string{
			"",
			" definitely-missing-a ",
			" definitely-missing-b ",
		}),
	)

	cfg := SkillConfig{
		APIKey: "secret",
		Env: map[string]string{
			"CUSTOM_ENV": "configured",
		},
	}
	require.Equal(
		t,
		[]string{envOpenSSLConf, "MISSING_ENV"},
		missingEnv(
			[]string{"PATH", envOpenSSLConf, "CUSTOM_ENV", "PRIMARY_ENV", "MISSING_ENV"},
			"PRIMARY_ENV",
			cfg,
		),
	)
	require.Equal(
		t,
		[]string{"channels.discord.token"},
		missingConfig(
			[]string{" channels.telegram.token ", "channels.discord.token"},
			map[string]struct{}{"channels.telegram.token": {}},
		),
	)

	meta := &openClawMetadata{
		PrimaryEnv: "PRIMARY_ENV",
		OS:         []string{"definitely-not-this-os"},
		Requires: openClawRequires{
			Bins:    []string{"definitely-missing-bin"},
			AnyBins: []string{"definitely-missing-a", "definitely-missing-b"},
			Env:     []string{"PRIMARY_ENV", "MISSING_ENV"},
			Config:  []string{" channels.discord.token "},
		},
	}
	require.Equal(
		t,
		StatusRequirements{
			OS:      []string{"definitely-not-this-os"},
			Bins:    []string{"definitely-missing-bin"},
			AnyBins: []string{"definitely-missing-a", "definitely-missing-b"},
			Env:     []string{"PRIMARY_ENV", "MISSING_ENV"},
			Config:  []string{"channels.discord.token"},
		},
		requiredStatus(meta),
	)
	require.Equal(
		t,
		StatusRequirements{
			OS:      []string{"definitely-not-this-os"},
			Bins:    []string{"definitely-missing-bin"},
			AnyBins: []string{"definitely-missing-a", "definitely-missing-b"},
			Env:     []string{"MISSING_ENV"},
			Config:  []string{"channels.discord.token"},
		},
		missingStatus(meta, nil, cfg),
	)
	require.Equal(
		t,
		[]string{"channels.telegram.token", "channels.discord.token"},
		normalizeRequirementConfig(
			[]string{
				" channels.telegram.token ",
				"",
				"channels.discord.token",
			},
		),
	)
	require.Empty(t, missingStatus(&openClawMetadata{Always: true}, nil, cfg))

	options := normalizeStatusInstall([]openClawInstallEntry{
		{Kind: "brew", Formula: "jq"},
		{Kind: "go", Module: "example.com/cmd@latest"},
		{Kind: "uv", Package: "ruff"},
		{Kind: "node", Packages: []string{"ccusage", "git-wrapped"}},
		{Kind: "download", URL: "https://example.com/tool.tar.gz"},
		{Kind: "custom", Label: "Custom label", Bins: []string{" tool "}},
		{Kind: "custom"},
		{Kind: " ", Label: "skip"},
	})
	require.Equal(
		t,
		[]string{
			"brew install jq",
			"go install example.com/cmd@latest",
			"uv tool install ruff",
			"npm install -g ccusage git-wrapped",
			"download tool.tar.gz",
			"Custom label",
			"run installer",
		},
		[]string{
			options[0].Label,
			options[1].Label,
			options[2].Label,
			options[3].Label,
			options[4].Label,
			options[5].Label,
			options[6].Label,
		},
	)
	require.Equal(t, []string{"tool"}, options[5].Bins)
	require.Nil(t, normalizeStatusInstall([]openClawInstallEntry{{Kind: ""}}))
}

func TestStatusHelpers_SourceAndConfigResolution(t *testing.T) {
	t.Parallel()

	require.Equal(t, "weather-api", statusConfigKey(nil, " weather-api ", "weather"))
	require.Equal(
		t,
		"weather",
		statusConfigKey(
			&Repository{skillConfigs: map[string]SkillConfig{"weather": {}}},
			"",
			" weather ",
		),
	)
	require.Equal(
		t,
		"weather-api",
		statusConfigKey(
			&Repository{skillConfigs: map[string]SkillConfig{"weather-api": {}}},
			" weather-api ",
			"weather",
		),
	)

	root := t.TempDir()
	require.Equal(t, "bundled", resolveStatusSource("/tmp/skill", true, nil))
	require.Equal(t, "unknown", resolveStatusSource("", false, nil))
	require.Equal(
		t,
		"codex",
		resolveStatusSource(
			filepath.Join(root, ".codex", "skills", "demo"),
			false,
			nil,
		),
	)
	require.Equal(
		t,
		"local",
		resolveStatusSource(
			filepath.Join(root, "skills", "local", "demo"),
			false,
			nil,
		),
	)
	require.Equal(
		t,
		"project",
		resolveStatusSource(
			filepath.Join(root, ".agents", "skills", "demo"),
			false,
			nil,
		),
	)

	workspaceRoot := filepath.Join(root, "workspace", "skills")
	require.Equal(
		t,
		"workspace",
		resolveStatusSource(
			filepath.Join(workspaceRoot, "demo"),
			false,
			[]string{workspaceRoot},
		),
	)
	extraRoot := filepath.Join(root, "extra-root")
	require.Equal(
		t,
		"extra",
		resolveStatusSource(
			filepath.Join(extraRoot, "demo"),
			false,
			[]string{extraRoot},
		),
	)
	require.Equal(
		t,
		"custom",
		resolveStatusSource(
			filepath.Join(root, "custom", "demo"),
			false,
			[]string{filepath.Join(root, "other")},
		),
	)
}

func TestStatus_NilAndZeroValueRepository(t *testing.T) {
	t.Parallel()

	var nilRepo *Repository
	require.Empty(t, nilRepo.Status().Skills)

	var zero Repository
	require.Empty(t, zero.Status().Skills)
}
