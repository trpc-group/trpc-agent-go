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

func TestRepositoryProviderFunc_Nil(t *testing.T) {
	var provider RepositoryProviderFunc
	got, err := provider.Repository(context.Background(), SkillScope{})
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestNormalizeSkillScopeMode(t *testing.T) {
	require.Equal(t, SkillScopeNone, NormalizeSkillScopeMode(SkillScopeNone))
	require.Equal(t, SkillScopeApp, NormalizeSkillScopeMode(SkillScopeApp))
	require.Equal(t, SkillScopeUser, NormalizeSkillScopeMode(SkillScopeUser))
	// Case-insensitive and trimmed.
	require.Equal(t, SkillScopeUser, NormalizeSkillScopeMode("  USER "))
	require.Equal(t, SkillScopeApp, NormalizeSkillScopeMode("APP"))
	// Unknown modes fall back to app-level sharing.
	require.Equal(t, SkillScopeApp, NormalizeSkillScopeMode("tenant"))
}

func TestNewSkillScope_Errors(t *testing.T) {
	_, err := NewSkillScope(SkillScopeApp, "  ", "user")
	require.Error(t, err)

	_, err = NewSkillScope(SkillScopeUser, "", "user")
	require.Error(t, err)

	_, err = NewSkillScope(SkillScopeUser, "app", "   ")
	require.Error(t, err)
}

func TestNewSkillScope_TrimsAndDropsUserInAppMode(t *testing.T) {
	scope, err := NewSkillScope(SkillScopeApp, "  app ", " user ")
	require.NoError(t, err)
	require.Equal(t, SkillScope{AppName: "app"}, scope)
}

func TestNewSkillScope_NoneModeIsUnscoped(t *testing.T) {
	scope, err := NewSkillScope(SkillScopeNone, "app", "user")
	require.NoError(t, err)
	require.True(t, scope.IsZero())
}

func TestSkillScope_IsZero(t *testing.T) {
	require.True(t, SkillScope{}.IsZero())
	require.True(t, SkillScope{AppName: "  "}.IsZero())
	require.False(t, SkillScope{AppName: "app"}.IsZero())
	require.False(t, SkillScope{UserID: "u"}.IsZero())
}

func TestScopePathParts_Errors(t *testing.T) {
	// App mode without an app name is invalid.
	_, err := ScopePathParts(SkillScopeApp, SkillScope{})
	require.Error(t, err)

	// User mode without a user id is invalid.
	_, err = ScopePathParts(SkillScopeUser, SkillScope{AppName: "app"})
	require.Error(t, err)
}

func TestScopePathParts_NoneModeIsUnsupported(t *testing.T) {
	_, err := ScopePathParts(SkillScopeNone, SkillScope{AppName: "app"})
	require.Error(t, err)
}
