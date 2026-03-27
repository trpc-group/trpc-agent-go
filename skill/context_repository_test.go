//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

type visibilityTestRepo struct {
	summaries []rootskill.Summary
	skills    map[string]*rootskill.Skill
	paths     map[string]string
	env       map[string]map[string]string
}

func (r *visibilityTestRepo) Summaries() []rootskill.Summary {
	out := make([]rootskill.Summary, len(r.summaries))
	copy(out, r.summaries)
	return out
}

func (r *visibilityTestRepo) Get(name string) (*rootskill.Skill, error) {
	if sk, ok := r.skills[name]; ok {
		return sk, nil
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

func (r *visibilityTestRepo) Path(name string) (string, error) {
	if p, ok := r.paths[name]; ok {
		return p, nil
	}
	return "", fmt.Errorf("skill %q not found", name)
}

func (r *visibilityTestRepo) SkillRunEnv(
	_ context.Context,
	skillName string,
) (map[string]string, error) {
	if env, ok := r.env[skillName]; ok {
		return env, nil
	}
	return nil, fmt.Errorf("skill %q not found", skillName)
}

func testRuntimeStateContext(userID string) context.Context {
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
		RuntimeState: map[string]any{"user_id": userID},
	}))
	return agent.NewInvocationContext(context.Background(), inv)
}

func visibleSkillNames(summaries []rootskill.Summary) []string {
	out := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, summary.Name)
	}
	return out
}

func TestFilteredRepository_FiltersByContext(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []rootskill.Summary{
			{Name: "alpha", Description: "A"},
			{Name: "beta", Description: "B"},
		},
		skills: map[string]*rootskill.Skill{
			"alpha": {Summary: rootskill.Summary{Name: "alpha"}, Body: "alpha body"},
			"beta":  {Summary: rootskill.Summary{Name: "beta"}, Body: "beta body"},
		},
		paths: map[string]string{
			"alpha": "/skills/alpha",
			"beta":  "/skills/beta",
		},
		env: map[string]map[string]string{
			"alpha": {"ALPHA": "1"},
			"beta":  {"BETA": "1"},
		},
	}
	repo := rootskill.NewFilteredRepository(base, func(ctx context.Context, summary rootskill.Summary) bool {
		userID, _ := agent.GetRuntimeStateValueFromContext[string](ctx, "user_id")
		if userID == "user-a" {
			return summary.Name == "alpha"
		}
		return summary.Name == "beta"
	})

	ctxA := testRuntimeStateContext("user-a")
	ctxB := testRuntimeStateContext("user-b")

	require.True(t, rootskill.IsContextAwareRepository(repo))
	require.Equal(t, []string{"alpha"}, visibleSkillNames(rootskill.SummariesForContext(ctxA, repo)))
	require.Equal(t, []string{"beta"}, visibleSkillNames(rootskill.SummariesForContext(ctxB, repo)))

	sk, err := rootskill.GetForContext(ctxA, repo, "alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha body", sk.Body)

	path, err := rootskill.PathForContext(ctxB, repo, "beta")
	require.NoError(t, err)
	require.Equal(t, "/skills/beta", path)

	_, err = rootskill.GetForContext(ctxA, repo, "beta")
	require.EqualError(t, err, `skill "beta" not found`)
	_, err = rootskill.PathForContext(ctxA, repo, "beta")
	require.EqualError(t, err, `skill "beta" not found`)

	envRepo, ok := any(repo).(interface {
		SkillRunEnv(context.Context, string) (map[string]string, error)
	})
	require.True(t, ok)
	env, err := envRepo.SkillRunEnv(ctxA, "alpha")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"ALPHA": "1"}, env)

	_, err = envRepo.SkillRunEnv(ctxA, "beta")
	require.EqualError(t, err, `skill "beta" not found`)
}

func TestContextRepositoryHelpers_FallbackToPlainRepository(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []rootskill.Summary{{Name: "alpha", Description: "A"}},
		skills: map[string]*rootskill.Skill{
			"alpha": {Summary: rootskill.Summary{Name: "alpha"}, Body: "alpha body"},
		},
		paths: map[string]string{"alpha": "/skills/alpha"},
	}

	ctx := context.Background()

	require.False(t, rootskill.IsContextAwareRepository(base))
	require.Equal(t, []string{"alpha"}, visibleSkillNames(rootskill.SummariesForContext(ctx, base)))

	sk, err := rootskill.GetForContext(ctx, base, "alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha body", sk.Body)

	path, err := rootskill.PathForContext(ctx, base, "alpha")
	require.NoError(t, err)
	require.Equal(t, "/skills/alpha", path)
}

func TestNewFilteredRepository_NilFilterKeepsPlainRepoNonContextAware(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []rootskill.Summary{{Name: "alpha", Description: "A"}},
		skills: map[string]*rootskill.Skill{
			"alpha": {Summary: rootskill.Summary{Name: "alpha"}, Body: "alpha body"},
		},
		paths: map[string]string{"alpha": "/skills/alpha"},
	}

	repo := rootskill.NewFilteredRepository(base, nil)
	require.False(t, rootskill.IsContextAwareRepository(repo))
	require.Equal(
		t,
		[]string{"alpha"},
		visibleSkillNames(rootskill.SummariesForContext(context.Background(), repo)),
	)
}

func TestNewFilteredRepository_NilFilterPreservesContextAwareBase(t *testing.T) {
	base := rootskill.NewFilteredRepository(
		&visibilityTestRepo{
			summaries: []rootskill.Summary{{Name: "alpha", Description: "A"}},
			skills: map[string]*rootskill.Skill{
				"alpha": {Summary: rootskill.Summary{Name: "alpha"}, Body: "alpha body"},
			},
			paths: map[string]string{"alpha": "/skills/alpha"},
		},
		func(context.Context, rootskill.Summary) bool { return true },
	)

	repo := rootskill.NewFilteredRepository(base, nil)
	require.True(t, rootskill.IsContextAwareRepository(repo))
}

func TestFilteredRepository_DelegatesPlainRepositoryMethods(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []rootskill.Summary{{Name: "alpha", Description: "A"}},
		skills: map[string]*rootskill.Skill{
			"alpha": {Summary: rootskill.Summary{Name: "alpha"}, Body: "alpha body"},
		},
		paths: map[string]string{"alpha": "/skills/alpha"},
	}

	repo := rootskill.NewFilteredRepository(base, func(context.Context, rootskill.Summary) bool { return true })
	require.Equal(t, []string{"alpha"}, visibleSkillNames(repo.Summaries()))

	sk, err := repo.Get("alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha body", sk.Body)

	path, err := repo.Path("alpha")
	require.NoError(t, err)
	require.Equal(t, "/skills/alpha", path)
}

func TestNewFilteredRepository_NilBaseReturnsNil(t *testing.T) {
	require.Nil(t, rootskill.NewFilteredRepository(nil, func(context.Context, rootskill.Summary) bool { return true }))
}
