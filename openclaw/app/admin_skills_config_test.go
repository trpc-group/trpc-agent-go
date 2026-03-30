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
