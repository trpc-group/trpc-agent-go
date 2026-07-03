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
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestScopedEvolutionCLIPath(t *testing.T) {
	// No app/user → root unchanged.
	got, err := scopedEvolutionCLIPath("/rev", "", "")
	require.NoError(t, err)
	require.Equal(t, "/rev", got)

	// App only → apps/<app>.
	got, err = scopedEvolutionCLIPath("/rev", "weather", "")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/rev", "apps", "weather"), got)

	// App + user → users/<app>/<user>.
	got, err = scopedEvolutionCLIPath("/rev", "weather", "u-1")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/rev", "users", "weather", "u-1"), got)

	// User without app is invalid in user mode.
	_, err = scopedEvolutionCLIPath("/rev", "", "u-1")
	require.Error(t, err)
}

func TestScopedManagedSkillsDir(t *testing.T) {
	got, err := scopedManagedSkillsDir("/state", skill.SkillScopeApp, skill.SkillScope{AppName: "app"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/state", defaultSkillsDir, "evolution", "apps", "app"), got)

	got, err = scopedManagedSkillsDir("/state", skill.SkillScopeUser, skill.SkillScope{AppName: "app", UserID: "u"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/state", defaultSkillsDir, "evolution", "users", "app", "u"), got)

	_, err = scopedManagedSkillsDir("/state", skill.SkillScopeUser, skill.SkillScope{AppName: "app"})
	require.Error(t, err)
}

func TestScopedSkillKey(t *testing.T) {
	got, err := scopedSkillKey(skill.SkillScopeApp, skill.SkillScope{AppName: "app"})
	require.NoError(t, err)
	require.Equal(t, "apps/app", got)

	got, err = scopedSkillKey(skill.SkillScopeUser, skill.SkillScope{AppName: "app", UserID: "u"})
	require.NoError(t, err)
	require.Equal(t, "users/app/u", got)

	_, err = scopedSkillKey(skill.SkillScopeUser, skill.SkillScope{})
	require.Error(t, err)
}

func TestResolveSkillRootsForScope_ReplacesManagedRoot(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	cfg := agentConfig{
		StateDir:                stateDir,
		EvolutionSkillScopeMode: skill.SkillScopeUser,
	}
	roots, err := resolveSkillRootsForScope(cwd, cfg, skill.SkillScope{AppName: "app", UserID: "u"})
	require.NoError(t, err)

	managedRoot := filepath.Join(stateDir, defaultSkillsDir)
	scopedManaged, err := scopedManagedSkillsDir(stateDir, skill.SkillScopeUser, skill.SkillScope{AppName: "app", UserID: "u"})
	require.NoError(t, err)

	// The unscoped managed root must be replaced by the scoped one.
	require.NotContains(t, roots, managedRoot)
	require.Contains(t, roots, scopedManaged)
}

// TestScopedSkillRepositoryProvider_IsolatesUsers is the multi-tenant
// end-to-end check: distinct users must resolve to distinct repositories
// backed by isolated managed-skill directories, while the same user reuses
// a cached repository.
func TestScopedSkillRepositoryProvider_IsolatesUsers(t *testing.T) {
	stateDir := t.TempDir()
	cwd := t.TempDir()
	cfg := agentConfig{
		StateDir:                stateDir,
		EvolutionSkillScopeMode: skill.SkillScopeUser,
	}
	provider := newScopedSkillRepositoryProvider(cwd, cfg)
	ctx := context.Background()

	repoAlice1, err := provider.Repository(ctx, skill.SkillScope{AppName: "app", UserID: "alice"})
	require.NoError(t, err)
	require.NotNil(t, repoAlice1)

	repoAlice2, err := provider.Repository(ctx, skill.SkillScope{AppName: "app", UserID: "alice"})
	require.NoError(t, err)
	require.Same(t, repoAlice1, repoAlice2, "same scope must return cached repository")

	repoBob, err := provider.Repository(ctx, skill.SkillScope{AppName: "app", UserID: "bob"})
	require.NoError(t, err)
	require.NotSame(t, repoAlice1, repoBob, "different users must be isolated")

	// An invalid scope (user mode missing user id) surfaces an error.
	_, err = provider.Repository(ctx, skill.SkillScope{AppName: "app"})
	require.Error(t, err)
}
