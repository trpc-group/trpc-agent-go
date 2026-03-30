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
	require.Equal(t, "custom", entry.Source)
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
