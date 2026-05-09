//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runtimeprofile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type testSkillPathResolver map[string]string

func (r testSkillPathResolver) Path(name string) (string, error) {
	path, ok := r[name]
	if !ok {
		return "", errors.New("missing skill")
	}
	return path, nil
}

func TestProfilePolicyHelpers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowed := filepath.Join(root, "tenant")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	denied := filepath.Join(root, "other")
	require.NoError(t, os.MkdirAll(denied, 0o755))

	ctx := WithProfile(context.Background(), Profile{
		Workspace: WorkspacePolicy{
			Workdir:      allowed,
			AllowedRoots: []string{allowed},
		},
		Credentials: CredentialPolicy{
			AllowedRefs: []string{"secret://retail/crm"},
		},
		Skills: SkillPolicy{
			Include: []string{"crm"},
			Exclude: []string{"draft"},
		},
	})

	workspace, ok := WorkspaceFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, allowed, workspace.Workdir)

	workdir, err := ResolveWorkdir(ctx, "")
	require.NoError(t, err)
	require.Equal(t, allowed, workdir)

	workdir, err = ResolveWorkdir(ctx, allowed)
	require.NoError(t, err)
	require.Equal(t, allowed, workdir)

	child := filepath.Join(allowed, "child")
	workdir, err = ResolveWorkdir(ctx, "child")
	require.NoError(t, err)
	require.Equal(t, child, workdir)

	_, err = ResolveWorkdir(ctx, denied)
	require.ErrorIs(t, err, ErrWorkspaceDenied)

	noBaseCtx := WithProfile(context.Background(), Profile{
		Workspace: WorkspacePolicy{
			AllowedRoots: []string{allowed},
		},
	})
	_, err = ResolveWorkdir(noBaseCtx, "relative")
	require.ErrorIs(t, err, ErrWorkspaceDenied)
	_, err = ResolveWorkdir(noBaseCtx, "")
	require.ErrorIs(t, err, ErrWorkspaceDenied)

	policy, ok := CredentialPolicyFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, []string{"secret://retail/crm"}, policy.AllowedRefs)

	require.NoError(t, CheckCredentialRef(ctx, "secret://retail/crm"))
	err = CheckCredentialRef(ctx, "secret://other/crm")
	require.ErrorIs(t, err, ErrCredentialDenied)

	require.True(t, SkillVisibilityFilter(ctx, skill.Summary{Name: "crm"}))
	require.False(t, SkillVisibilityFilter(
		ctx,
		skill.Summary{Name: "draft"},
	))
	require.False(t, SkillVisibilityFilter(
		ctx,
		skill.Summary{Name: "other"},
	))
	require.True(t, SkillVisibilityFilter(
		context.Background(),
		skill.Summary{Name: "other"},
	))
}

func TestProfilePolicyHelpersWithoutProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	workspace, ok := WorkspaceFromContext(ctx)
	require.False(t, ok)
	require.Empty(t, workspace)

	policy, ok := CredentialPolicyFromContext(ctx)
	require.False(t, ok)
	require.Empty(t, policy)

	workdir, err := ResolveWorkdir(ctx, " /tmp/work ")
	require.NoError(t, err)
	require.Equal(t, filepath.Clean("/tmp/work"), filepath.Clean(workdir))

	require.NoError(t, CheckCredentialRef(ctx, "secret://retail/crm"))
	require.NoError(t, CheckCredentialRef(ctx, " "))
}

func TestSkillVisibilityFilterRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	retailRoot := filepath.Join(root, "retail")
	otherRoot := filepath.Join(root, "other")
	require.NoError(t, os.MkdirAll(retailRoot, 0o755))
	require.NoError(t, os.MkdirAll(otherRoot, 0o755))

	resolver := testSkillPathResolver{
		"crm":   filepath.Join(retailRoot, "crm", "SKILL.md"),
		"other": filepath.Join(otherRoot, "other", "SKILL.md"),
	}
	filter := SkillVisibilityFilterForRepository(resolver)
	ctx := WithProfile(context.Background(), Profile{
		Skills: SkillPolicy{Roots: []string{retailRoot}},
	})

	require.True(t, filter(ctx, skill.Summary{Name: "crm"}))
	require.False(t, filter(ctx, skill.Summary{Name: "other"}))
	require.False(t, filter(ctx, skill.Summary{Name: "missing"}))
	require.False(t, filter(ctx, skill.Summary{}))
	require.True(t, filter(context.Background(), skill.Summary{Name: "other"}))

	filter = SkillVisibilityFilterForRepository(nil)
	require.False(t, filter(ctx, skill.Summary{Name: "crm"}))
}
