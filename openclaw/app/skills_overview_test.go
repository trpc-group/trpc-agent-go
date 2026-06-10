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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestRenderCompactSkillsOverviewPinnedAndOmitted(t *testing.T) {
	t.Parallel()

	summaries := []skill.Summary{
		{Name: "alpha", Description: "Alpha skill"},
		{Name: "beta", Description: "Beta skill"},
		{Name: "gamma", Description: "Gamma skill"},
	}

	got := renderCompactSkillsOverview(
		summaries,
		2,
		[]string{"gamma", "missing"},
	)

	require.True(t, strings.HasPrefix(
		got,
		"Available skills:\n- gamma: Gamma skill\n- alpha: Alpha skill\n",
	))
	require.Contains(t, got, "1 more skills are available")
	require.Contains(t, got, "skill_list")
	require.NotContains(t, got, "- beta: Beta skill")
}

func TestNewSkillsOverviewRendererDisabled(t *testing.T) {
	t.Parallel()

	require.Nil(t, newSkillsOverviewRenderer(0, []string{"alpha"}))
}

func TestBuildSkillsOverviewRunOptionResolver(t *testing.T) {
	t.Parallel()

	resolver := buildSkillsOverviewRunOptionResolver(1, []string{"beta"})
	require.NotNil(t, resolver)

	_, runOpts, err := resolver(
		context.Background(),
		gateway.RunOptionInput{},
	)
	require.NoError(t, err)

	opts := agent.NewRunOptions(runOpts...)
	require.NotNil(t, opts.AvailableSkillsRenderer)

	got := opts.AvailableSkillsRenderer(
		context.Background(),
		agent.AvailableSkillsRenderRequest{
			Summaries: []skill.Summary{
				{Name: "alpha", Description: "Alpha skill"},
				{Name: "beta", Description: "Beta skill"},
			},
		},
	)
	require.Contains(t, got, "- beta: Beta skill")
	require.NotContains(t, got, "- alpha: Alpha skill")
}
