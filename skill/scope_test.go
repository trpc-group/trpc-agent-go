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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSkillScope_AppMode(t *testing.T) {
	scope, err := NewSkillScope(SkillScopeApp, "app", "user")
	require.NoError(t, err)
	require.Equal(t, SkillScope{AppName: "app"}, scope)
}

func TestNewSkillScope_UserMode(t *testing.T) {
	scope, err := NewSkillScope(SkillScopeUser, "app", "user")
	require.NoError(t, err)
	require.Equal(t, SkillScope{AppName: "app", UserID: "user"}, scope)
}

func TestScopePathParts(t *testing.T) {
	parts, err := ScopePathParts(SkillScopeApp, SkillScope{AppName: "weather-bot"})
	require.NoError(t, err)
	require.Equal(t, []string{"apps", "weather-bot"}, parts)

	parts, err = ScopePathParts(SkillScopeUser, SkillScope{AppName: "weather-bot", UserID: "u-1"})
	require.NoError(t, err)
	require.Equal(t, []string{"users", "weather-bot", "u-1"}, parts)
}

func TestScopePathParts_HashesUnsafePII(t *testing.T) {
	parts, err := ScopePathParts(SkillScopeUser, SkillScope{
		AppName: "org/workspace",
		UserID:  "user@example.com",
	})
	require.NoError(t, err)
	require.Len(t, parts, 3)
	require.Equal(t, "users", parts[0])
	require.NotContains(t, parts[1], "/")
	require.NotContains(t, parts[2], "@")
	require.Contains(t, parts[1], "id-")
	require.Contains(t, parts[2], "id-")
}

func TestRepositoryProviderFunc(t *testing.T) {
	repo := &FSRepository{}
	provider := RepositoryProviderFunc(func(context.Context, SkillScope) (Repository, error) {
		return repo, nil
	})
	got, err := provider.Repository(context.Background(), SkillScope{})
	require.NoError(t, err)
	require.Same(t, repo, got)
}
