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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
)

func TestSetSkillEnabledInConfig_CreatesSkillsEntries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte("app_name: demo\n"), 0o600))

	require.NoError(t, setSkillEnabledInConfig(path, "weather-api", false))

	cfg, err := loadConfigFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Skills)
	require.Contains(t, cfg.Skills.Entries, "weather-api")
	require.NotNil(t, cfg.Skills.Entries["weather-api"].Enabled)
	require.False(t, *cfg.Skills.Entries["weather-api"].Enabled)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "skills:")
	require.Contains(t, string(body), "entries:")
	require.Contains(t, string(body), "weather-api:")
	require.Contains(t, string(body), "enabled: false")
}

func TestSetSkillEnabledInConfig_PreservesExistingSkillEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
skills:
  entries:
    weather-api:
      api_key: demo
      enabled: false
`), 0o600))

	require.NoError(t, setSkillEnabledInConfig(path, "weather-api", true))

	cfg, err := loadConfigFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Skills)
	require.Contains(t, cfg.Skills.Entries, "weather-api")
	require.Equal(t, "demo", cfg.Skills.Entries["weather-api"].APIKey)
	require.NotNil(t, cfg.Skills.Entries["weather-api"].Enabled)
	require.True(t, *cfg.Skills.Entries["weather-api"].Enabled)
}

func TestDecodeConfigDocument_RejectsMultipleDocuments(t *testing.T) {
	t.Parallel()

	_, err := decodeConfigDocument([]byte("app_name: demo\n---\nextra: true\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple YAML documents")
}

func TestDecodeConfigDocument_RejectsMalformedTrailingDocument(t *testing.T) {
	t.Parallel()

	_, err := decodeConfigDocument([]byte("app_name: demo\n---\n: bad\n"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "multiple YAML documents")
}

func TestAdminSkillsProviderSetSkillEnabled_UpdatesMemory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte("app_name: demo\n"), 0o600))

	provider := &adminSkillsProvider{
		configPath: path,
		skillConfigs: map[string]ocskills.SkillConfig{
			"weather-api": {},
		},
	}

	require.NoError(t, provider.SetSkillEnabled("weather-api", false))
	require.NotNil(t, provider.skillConfigs["weather-api"].Enabled)
	require.False(t, *provider.skillConfigs["weather-api"].Enabled)
}

func TestAdminSkillsProviderSetSkillEnabled_UpdatesLiveRepo(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "openclaw.yaml")
	require.NoError(t, os.WriteFile(path, []byte("app_name: demo\n"), 0o600))

	root := t.TempDir()
	writeTestSkill(t, root, "weather-probe", `---
name: weather-probe
description: "Probe weather prerequisites"
metadata:
  openclaw:
    skillKey: "weather-api"
---

# weather-probe
`)

	repo, err := ocskills.NewRepository([]string{root})
	require.NoError(t, err)

	provider := &adminSkillsProvider{
		configPath: path,
		repo:       repo,
	}

	require.NoError(t, provider.SetSkillEnabled("weather-api", false))

	report, err := provider.SkillsStatus()
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)
	require.True(t, report.Skills[0].Disabled)
	require.False(t, report.Skills[0].Eligible)
	require.Equal(t, "disabled by config", report.Skills[0].Reason)
}

func TestAdminSkillsProviderRefreshSkills_UpdatesLiveRepo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo, err := ocskills.NewRepository([]string{root})
	require.NoError(t, err)

	provider := &adminSkillsProvider{
		repo: repo,
	}

	report, err := provider.SkillsStatus()
	require.NoError(t, err)
	require.Empty(t, report.Skills)

	writeTestSkill(t, root, "weather-probe", `---
name: weather-probe
description: "Probe weather prerequisites"
---

# weather-probe
`)

	require.NoError(t, provider.RefreshSkills())

	report, err = provider.SkillsStatus()
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)
	require.Equal(t, "weather-probe", report.Skills[0].Name)
}

func TestAdminSkillsProviderSkillsStatus_AttachesWatchStatus(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestSkill(t, root, "weather-probe", `---
name: weather-probe
description: "Probe weather prerequisites"
---

# weather-probe
`)

	repo, err := ocskills.NewRepository([]string{root})
	require.NoError(t, err)

	watch := ocskills.NewWatchService(
		repo,
		[]string{root},
		ocskills.WatchConfig{Enabled: false},
	)
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	provider := &adminSkillsProvider{
		roots: []string{root},
		watch: watch,
	}

	report, err := provider.SkillsStatus()
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)
	require.NotNil(t, report.Watch)
	require.False(t, report.Watch.Enabled)
}

func TestAdminSkillsProviderSkillsStatus_BuildError(t *testing.T) {
	t.Parallel()

	provider := &adminSkillsProvider{
		roots: []string{"cos://bucket/skills.zip"},
	}

	_, err := provider.SkillsStatus()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported skills root URL")
}

func TestAdminSkillsProviderRefreshSkills_UsesWatchRefresh(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repo, err := ocskills.NewRepository([]string{root})
	require.NoError(t, err)

	watch := ocskills.NewWatchService(
		repo,
		[]string{root},
		ocskills.WatchConfig{Enabled: false},
	)
	require.NotNil(t, watch)
	t.Cleanup(func() {
		require.NoError(t, watch.Close())
	})

	provider := &adminSkillsProvider{
		repo:  repo,
		watch: watch,
	}

	writeTestSkill(t, root, "weather-probe", `---
name: weather-probe
description: "Probe weather prerequisites"
---

# weather-probe
`)

	require.NoError(t, provider.RefreshSkills())

	report, err := provider.SkillsStatus()
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)
	require.NotNil(t, report.Watch)
	require.Equal(t, "manual", report.Watch.LastRefreshReason)
}

func TestAdminSkillsProviderHelpersAndFallbackStatus(t *testing.T) {
	t.Parallel()

	var nilProvider *adminSkillsProvider
	report, err := nilProvider.SkillsStatus()
	require.NoError(t, err)
	require.Empty(t, report.Skills)
	require.Empty(t, nilProvider.SkillsConfigPath())
	require.False(t, nilProvider.SkillsRefreshable())
	require.EqualError(t, nilProvider.RefreshSkills(), "skills config is not available")

	root := t.TempDir()
	writeTestSkill(t, root, "weather-probe", `---
name: weather-probe
description: "Probe weather prerequisites"
---

# weather-probe
`)

	provider := &adminSkillsProvider{
		configPath: "  /tmp/openclaw.yaml ",
		roots:      []string{root},
		skillConfigs: map[string]ocskills.SkillConfig{
			"weather-probe": {},
		},
	}
	report, err = provider.SkillsStatus()
	require.NoError(t, err)
	require.Len(t, report.Skills, 1)
	require.Equal(t, "/tmp/openclaw.yaml", provider.SkillsConfigPath())
	require.False(t, provider.SkillsRefreshable())
}

func TestAdminSkillsProviderSetSkillEnabled_ErrorPaths(t *testing.T) {
	t.Parallel()

	var nilProvider *adminSkillsProvider
	require.EqualError(
		t,
		nilProvider.SetSkillEnabled("weather-api", true),
		"skills config is not available",
	)

	provider := &adminSkillsProvider{}
	require.EqualError(
		t,
		provider.SetSkillEnabled(" ", true),
		"skill config key is required",
	)
	require.EqualError(
		t,
		provider.SetSkillEnabled("weather-api", true),
		"skill toggles require a config-backed runtime",
	)
}

func TestCloneAdminSkillConfigs_ClonesAndTrims(t *testing.T) {
	t.Parallel()

	enabled := true
	src := map[string]ocskills.SkillConfig{
		"weather-api": {
			Enabled: &enabled,
			APIKey:  " secret ",
			Env: map[string]string{
				" TOKEN ": " value ",
				"":        "ignored",
				"DROP":    " ",
			},
		},
	}

	got := cloneAdminSkillConfigs(src)
	require.Contains(t, got, "weather-api")
	require.NotNil(t, got["weather-api"].Enabled)
	require.True(t, *got["weather-api"].Enabled)
	require.Equal(t, "secret", got["weather-api"].APIKey)
	require.Equal(
		t,
		map[string]string{"TOKEN": "value"},
		got["weather-api"].Env,
	)

	enabled = false
	src["weather-api"] = ocskills.SkillConfig{
		Enabled: &enabled,
		APIKey:  "changed",
		Env: map[string]string{
			"TOKEN": "changed",
		},
	}
	require.True(t, *got["weather-api"].Enabled)
	require.Equal(t, "secret", got["weather-api"].APIKey)
	require.Equal(t, "value", got["weather-api"].Env["TOKEN"])
}

func TestConfigDocumentHelpers_ErrorPaths(t *testing.T) {
	t.Parallel()

	_, err := ensureDocumentMapping(nil)
	require.Error(t, err)

	_, err = ensureDocumentMapping(&yaml.Node{Kind: yaml.ScalarNode})
	require.Error(t, err)

	var doc yaml.Node
	root, err := ensureDocumentMapping(&doc)
	require.NoError(t, err)
	require.Equal(t, yaml.MappingNode, root.Kind)

	doc = yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{nil},
	}
	root, err = ensureDocumentMapping(&doc)
	require.NoError(t, err)
	require.Equal(t, yaml.MappingNode, root.Kind)

	_, err = ensureMappingChild(nil, "skills")
	require.Error(t, err)
	_, err = ensureMappingChild(&yaml.Node{Kind: yaml.SequenceNode}, "skills")
	require.Error(t, err)
	_, err = ensureMappingChild(&yaml.Node{Kind: yaml.MappingNode}, " ")
	require.Error(t, err)

	parent := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "skills"},
			nil,
		},
	}
	child, err := ensureMappingChild(parent, "skills")
	require.NoError(t, err)
	require.Equal(t, yaml.MappingNode, child.Kind)

	_, err = ensureMappingChild(&yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "skills"},
			{Kind: yaml.ScalarNode, Value: "oops"},
		},
	}, "skills")
	require.Error(t, err)

	require.EqualError(
		t,
		setMappingBool(nil, "enabled", true),
		"mapping node is required",
	)
	require.EqualError(
		t,
		setMappingBool(&yaml.Node{Kind: yaml.SequenceNode}, "enabled", true),
		"expected mapping node",
	)
	require.EqualError(
		t,
		setMappingBool(&yaml.Node{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "enabled"},
				{Kind: yaml.MappingNode},
			},
		}, "enabled", true),
		"expected scalar node",
	)
}

func TestSetSkillEnabledInConfig_ErrorPathsAndWriteConfigDocument(t *testing.T) {
	t.Parallel()

	require.EqualError(
		t,
		setSkillEnabledInConfig("", "weather-api", true),
		"skills config path is empty",
	)

	path := filepath.Join(t.TempDir(), "openclaw.yaml")
	require.EqualError(
		t,
		setSkillEnabledInConfig(path, "", true),
		"skill config key is required",
	)

	err := setSkillEnabledInConfig(t.TempDir(), "weather-api", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read config")

	path = filepath.Join(t.TempDir(), "nested", "openclaw.yaml")
	require.NoError(t, setSkillEnabledInConfig(path, "weather-api", true))
	cfg, err := loadConfigFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Skills)
	require.Contains(t, cfg.Skills.Entries, "weather-api")
	require.NotNil(t, cfg.Skills.Entries["weather-api"].Enabled)
	require.True(t, *cfg.Skills.Entries["weather-api"].Enabled)

	require.NoError(t, os.WriteFile(path, []byte("[]\n"), 0o644))
	err = setSkillEnabledInConfig(path, "weather-api", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "config root")

	require.NoError(t, os.WriteFile(path, []byte("skills: []\n"), 0o644))
	err = setSkillEnabledInConfig(path, "weather-api", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "config skills")

	path = filepath.Join(t.TempDir(), "empty.yaml")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	require.NoError(t, setSkillEnabledInConfig(path, "weather-api", true))
	cfg, err = loadConfigFile(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Skills)
	require.Contains(t, cfg.Skills.Entries, "weather-api")
	require.NotNil(t, cfg.Skills.Entries["weather-api"].Enabled)
	require.True(t, *cfg.Skills.Entries["weather-api"].Enabled)

	doc := yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{{
			Kind: yaml.MappingNode,
			Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "app_name"},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "demo"},
			},
		}},
	}
	path = filepath.Join(t.TempDir(), "write.yaml")
	require.NoError(t, os.WriteFile(path, []byte("app_name: old\n"), 0o644))
	require.NoError(t, writeConfigDocument(path, &doc))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "app_name: demo")

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	err = writeConfigDocument(
		filepath.Join(t.TempDir(), "missing", "write.yaml"),
		&doc,
	)
	require.Error(t, err)
}

func writeTestSkill(t *testing.T, root, name, body string) {
	t.Helper()

	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "SKILL.md"),
		[]byte(body),
		0o644,
	))
}
