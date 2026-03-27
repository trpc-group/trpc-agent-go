//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type visibilityTestRepo struct {
	summaries []Summary
	skills    map[string]*Skill
	paths     map[string]string
	env       map[string]map[string]string
}

func (r *visibilityTestRepo) Summaries() []Summary {
	out := make([]Summary, len(r.summaries))
	copy(out, r.summaries)
	return out
}

func (r *visibilityTestRepo) Get(name string) (*Skill, error) {
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

func visibleSkillNames(summaries []Summary) []string {
	out := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, summary.Name)
	}
	return out
}

func TestFilteredRepository_FiltersByContext(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []Summary{
			{Name: "alpha", Description: "A"},
			{Name: "beta", Description: "B"},
		},
		skills: map[string]*Skill{
			"alpha": {Summary: Summary{Name: "alpha"}, Body: "alpha body"},
			"beta":  {Summary: Summary{Name: "beta"}, Body: "beta body"},
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
	repo := NewFilteredRepository(base, func(ctx context.Context, summary Summary) bool {
		userID, _ := agent.GetRuntimeStateValueFromContext[string](ctx, "user_id")
		if userID == "user-a" {
			return summary.Name == "alpha"
		}
		return summary.Name == "beta"
	})

	ctxA := testRuntimeStateContext("user-a")
	ctxB := testRuntimeStateContext("user-b")

	require.True(t, IsContextAwareRepository(repo))
	require.Equal(t, []string{"alpha"}, visibleSkillNames(SummariesForContext(ctxA, repo)))
	require.Equal(t, []string{"beta"}, visibleSkillNames(SummariesForContext(ctxB, repo)))

	sk, err := GetForContext(ctxA, repo, "alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha body", sk.Body)

	path, err := PathForContext(ctxB, repo, "beta")
	require.NoError(t, err)
	require.Equal(t, "/skills/beta", path)

	_, err = GetForContext(ctxA, repo, "beta")
	require.EqualError(t, err, `skill "beta" not found`)
	_, err = PathForContext(ctxA, repo, "beta")
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
		summaries: []Summary{{Name: "alpha", Description: "A"}},
		skills: map[string]*Skill{
			"alpha": {Summary: Summary{Name: "alpha"}, Body: "alpha body"},
		},
		paths: map[string]string{"alpha": "/skills/alpha"},
	}

	ctx := context.Background()

	require.False(t, IsContextAwareRepository(base))
	require.Equal(t, []string{"alpha"}, visibleSkillNames(SummariesForContext(ctx, base)))

	sk, err := GetForContext(ctx, base, "alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha body", sk.Body)

	path, err := PathForContext(ctx, base, "alpha")
	require.NoError(t, err)
	require.Equal(t, "/skills/alpha", path)
}

func TestNewFilteredRepository_NilFilterKeepsPlainRepoNonContextAware(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []Summary{{Name: "alpha", Description: "A"}},
		skills: map[string]*Skill{
			"alpha": {Summary: Summary{Name: "alpha"}, Body: "alpha body"},
		},
		paths: map[string]string{"alpha": "/skills/alpha"},
	}

	repo := NewFilteredRepository(base, nil)
	require.False(t, IsContextAwareRepository(repo))
	require.Equal(
		t,
		[]string{"alpha"},
		visibleSkillNames(SummariesForContext(context.Background(), repo)),
	)
}

func TestNewFilteredRepository_NilFilterPreservesContextAwareBase(t *testing.T) {
	base := NewFilteredRepository(
		&visibilityTestRepo{
			summaries: []Summary{{Name: "alpha", Description: "A"}},
			skills: map[string]*Skill{
				"alpha": {Summary: Summary{Name: "alpha"}, Body: "alpha body"},
			},
			paths: map[string]string{"alpha": "/skills/alpha"},
		},
		func(context.Context, Summary) bool { return true },
	)

	repo := NewFilteredRepository(base, nil)
	require.True(t, IsContextAwareRepository(repo))
}

func TestFilteredRepository_DelegatesPlainRepositoryMethods(t *testing.T) {
	base := &visibilityTestRepo{
		summaries: []Summary{{Name: "alpha", Description: "A"}},
		skills: map[string]*Skill{
			"alpha": {Summary: Summary{Name: "alpha"}, Body: "alpha body"},
		},
		paths: map[string]string{"alpha": "/skills/alpha"},
	}

	repo := NewFilteredRepository(base, func(context.Context, Summary) bool { return true })
	require.Equal(t, []string{"alpha"}, visibleSkillNames(repo.Summaries()))

	sk, err := repo.Get("alpha")
	require.NoError(t, err)
	require.Equal(t, "alpha body", sk.Body)

	path, err := repo.Path("alpha")
	require.NoError(t, err)
	require.Equal(t, "/skills/alpha", path)
}

func TestNewFilteredRepository_NilBaseReturnsNil(t *testing.T) {
	require.Nil(t, NewFilteredRepository(nil, func(context.Context, Summary) bool { return true }))
}
