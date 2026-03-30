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
)

type plainContextTestRepo struct {
	summaries []Summary
	skills    map[string]*Skill
	paths     map[string]string
}

func (r *plainContextTestRepo) Summaries() []Summary {
	out := make([]Summary, len(r.summaries))
	copy(out, r.summaries)
	return out
}

func (r *plainContextTestRepo) Get(name string) (*Skill, error) {
	if sk, ok := r.skills[name]; ok {
		return sk, nil
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

func (r *plainContextTestRepo) Path(name string) (string, error) {
	if path, ok := r.paths[name]; ok {
		return path, nil
	}
	return "", fmt.Errorf("skill %q not found", name)
}

func TestContextRepositoryHelpers_HandleNilRepository(t *testing.T) {
	ctx := context.Background()

	require.False(t, IsContextAwareRepository(nil))
	require.Nil(t, SummariesForContext(ctx, nil))

	_, err := GetForContext(ctx, nil, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)

	_, err = PathForContext(ctx, nil, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
}

func TestFilteredRepository_NilReceiverAndNilBase(t *testing.T) {
	ctx := context.Background()

	var nilRepo *filteredRepository
	require.Nil(t, nilRepo.Summaries())
	require.Nil(t, nilRepo.SummariesForContext(ctx))

	_, err := nilRepo.Get("alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = nilRepo.Path("alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = nilRepo.GetForContext(ctx, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = nilRepo.PathForContext(ctx, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = nilRepo.SkillRunEnv(ctx, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)

	repo := &filteredRepository{}
	require.Nil(t, repo.Summaries())
	require.Nil(t, repo.SummariesForContext(ctx))

	_, err = repo.Get("alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = repo.Path("alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = repo.GetForContext(ctx, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = repo.PathForContext(ctx, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
	_, err = repo.SkillRunEnv(ctx, "alpha")
	require.EqualError(t, err, `skill "alpha" not found`)
}

func TestFilteredRepository_SkillRunEnv_NoProvider(t *testing.T) {
	repo := &filteredRepository{
		base: &plainContextTestRepo{
			summaries: []Summary{{Name: "alpha", Description: "A"}},
		},
		filter: func(context.Context, Summary) bool { return true },
	}

	env, err := repo.SkillRunEnv(context.Background(), "alpha")
	require.NoError(t, err)
	require.Nil(t, env)
}

func TestFilterSummaries_EdgeCases(t *testing.T) {
	ctx := context.Background()

	require.Nil(t, filterSummaries(ctx, nil, func(context.Context, Summary) bool {
		return true
	}))

	src := []Summary{{Name: "alpha", Description: "A"}}
	out := filterSummaries(ctx, src, nil)
	require.Equal(t, src, out)

	out[0].Name = "mutated"
	require.Equal(t, "alpha", src[0].Name)
}
