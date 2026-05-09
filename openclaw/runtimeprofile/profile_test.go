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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	testProfileDefault = "default"
	testProfileRetail  = "retail"
	testToolAllowed    = "allowed"
	testToolBlocked    = "blocked"
	testToolOther      = "other"
	testToolExecute    = "execute"
)

type nilDeclarationTool struct{}

func (nilDeclarationTool) Declaration() *tool.Declaration {
	return nil
}

type testTool struct {
	name string
}

func (t testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func TestMapResolver(t *testing.T) {
	t.Parallel()

	resolver := NewMapResolver(Config{
		Default: testProfileDefault,
		Profiles: map[string]Profile{
			testProfileDefault: {
				AppName: "default-app",
			},
			testProfileRetail: {
				ID:      testProfileRetail,
				AppName: "retail-app",
				Tools: ToolPolicy{
					Include: []string{testToolAllowed},
				},
			},
		},
	})

	profile, err := resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, testProfileDefault, profile.ID)
	require.Equal(t, "default-app", profile.AppName)

	profile, err = resolver.Resolve(context.Background(), Request{
		ProfileID: testProfileRetail,
	})
	require.NoError(t, err)
	require.Equal(t, testProfileRetail, profile.ID)
	require.Equal(t, "retail-app", profile.AppName)
	require.Equal(t, []string{testToolAllowed}, profile.Tools.Include)

	_, err = resolver.Resolve(context.Background(), Request{
		ProfileID: "missing",
	})
	require.ErrorIs(t, err, ErrProfileNotFound)
}

func TestMapResolverUsesMapKeyAndExplicitID(t *testing.T) {
	t.Parallel()

	const profileAlias = "retail-alias"
	resolver := NewMapResolver(Config{
		Default: testProfileRetail,
		Profiles: map[string]Profile{
			testProfileRetail: {
				ID:      profileAlias,
				AppName: "retail-app",
			},
		},
	})

	profile, err := resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Equal(t, profileAlias, profile.ID)

	profile, err = resolver.Resolve(context.Background(), Request{
		ProfileID: profileAlias,
	})
	require.NoError(t, err)
	require.Equal(t, profileAlias, profile.ID)
}

func TestMapResolverFallbackToDefault(t *testing.T) {
	t.Parallel()

	resolver := NewMapResolver(Config{
		Default:           testProfileDefault,
		FallbackToDefault: true,
		Profiles: map[string]Profile{
			testProfileDefault: {
				AppName: "default-app",
			},
		},
	})

	profile, err := resolver.Resolve(context.Background(), Request{
		ProfileID: "missing",
	})
	require.NoError(t, err)
	require.Equal(t, testProfileDefault, profile.ID)
	require.Equal(t, "default-app", profile.AppName)
}

func TestValidateConfig(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateConfig(Config{}))
	require.NoError(t, ValidateConfig(Config{
		Default: testProfileDefault,
		Profiles: map[string]Profile{
			testProfileDefault: {},
		},
	}))
	require.NoError(t, ValidateConfig(Config{
		Default: testProfileRetail,
		Profiles: map[string]Profile{
			"retail-key": {
				ID: testProfileRetail,
			},
		},
	}))

	err := ValidateConfig(Config{Required: true})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "required profiles are empty")

	err = ValidateConfig(Config{
		Default: "missing",
		Profiles: map[string]Profile{
			testProfileDefault: {},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "default profile")

	err = ValidateConfig(Config{
		FallbackToDefault: true,
		Profiles: map[string]Profile{
			testProfileDefault: {},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "fallback needs default")

	err = ValidateConfig(Config{
		Profiles: map[string]Profile{
			"retail-a": {ID: testProfileRetail},
			"retail-b": {ID: testProfileRetail},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "duplicate profile id")

	err = ValidateConfig(Config{
		Profiles: map[string]Profile{
			testProfileRetail: {
				Isolation: IsolationPolicy{Mode: "bad"},
			},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "unsupported isolation mode")

	err = ValidateConfig(Config{
		Profiles: map[string]Profile{
			testProfileRetail: {
				Isolation: IsolationPolicy{
					Mode:       IsolationModeShared,
					AgentCache: true,
				},
			},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "shared isolation")

	err = ValidateConfig(Config{
		Profiles: map[string]Profile{
			testProfileRetail: {
				Isolation: IsolationPolicy{
					Mode:        IsolationModeProfileCache,
					ServiceMode: "sidecar",
				},
			},
		},
	})
	require.ErrorIs(t, err, ErrConfigInvalid)
	require.Contains(t, err.Error(), "profile_cache isolation")
}

func TestMapResolverEdgeCases(t *testing.T) {
	t.Parallel()

	require.Nil(t, NewMapResolver(Config{}))
	require.Nil(t, NewMapResolver(Config{
		Profiles: map[string]Profile{
			" ": {
				AppName: "ignored",
			},
		},
	}))

	var nilResolver *MapResolver
	profile, err := nilResolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Empty(t, profile)

	resolver := NewMapResolver(Config{
		Profiles: map[string]Profile{
			testProfileRetail: {
				AppName: "retail-app",
			},
		},
	})
	profile, err = resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Empty(t, profile)
}

func TestRunOptionsAppliesProfile(t *testing.T) {
	t.Parallel()

	sourceFilter := map[string]any{
		"tenant": testProfileRetail,
	}
	sourceState := map[string]any{
		"plan": "vip",
	}
	sourceExtraModel := map[string]any{
		"reasoning_effort": "medium",
	}

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		ID:        testProfileRetail,
		Version:   "2026-05-09",
		AppName:   "retail-app",
		AgentName: "sales-agent",
		ModelName: "gpt-retail",
		Prompt: Prompt{
			Instruction:  "help retail customers",
			SystemPrompt: "stay concise",
		},
		Tools: ToolPolicy{
			Include:          []string{testToolAllowed, testToolBlocked},
			Exclude:          []string{testToolBlocked},
			ExecutionInclude: []string{testToolExecute},
			ExecutionExclude: []string{testToolBlocked},
			ToolSets:         []string{"mcp-retail"},
			CredentialRefs: map[string]string{
				"crm": "secret://retail/crm",
			},
		},
		Knowledge: KnowledgePolicy{
			Indexes: []string{"retail-index"},
			Filter:  sourceFilter,
		},
		Workspace: WorkspacePolicy{
			Workdir:      "/workspace/retail",
			AllowedRoots: []string{"/workspace/retail"},
		},
		Credentials: CredentialPolicy{
			AllowedRefs: []string{"secret://retail/crm"},
		},
		Skills: SkillPolicy{
			Include: []string{"crm"},
			Exclude: []string{"internal"},
			Roots:   []string{"/skills/retail"},
		},
		Isolation: IsolationPolicy{
			Mode:         IsolationModeService,
			AgentCache:   true,
			ToolSetCache: true,
			ServiceMode:  "sidecar",
		},
		State:      sourceState,
		ExtraModel: sourceExtraModel,
	})...)

	require.Equal(t, "retail-app", runOpts.AppName)
	require.Equal(t, "sales-agent", runOpts.AgentByName)
	require.Equal(t, "gpt-retail", runOpts.ModelName)
	require.Equal(t, "help retail customers", runOpts.Instruction)
	require.Equal(t, "stay concise", runOpts.GlobalInstruction)
	require.Equal(t, testProfileRetail, runOpts.KnowledgeFilter["tenant"])
	require.Equal(
		t,
		"medium",
		runOpts.ModelRequestExtraFields["reasoning_effort"],
	)
	require.Equal(t, "vip", runOpts.RuntimeState["plan"])
	require.Equal(
		t,
		testProfileRetail,
		runOpts.RuntimeState[RuntimeStateProfileID],
	)
	require.Equal(
		t,
		"2026-05-09",
		runOpts.RuntimeState[RuntimeStateProfileVersion],
	)
	require.Equal(
		t,
		"/workspace/retail",
		runOpts.RuntimeState[RuntimeStateWorkspaceWorkdir],
	)
	require.Equal(
		t,
		[]string{"/workspace/retail"},
		runOpts.RuntimeState[RuntimeStateWorkspaceAllowedRoots],
	)
	require.Equal(
		t,
		[]string{"secret://retail/crm"},
		runOpts.RuntimeState[RuntimeStateCredentialAllowedRefs],
	)
	require.Equal(
		t,
		[]string{"crm"},
		runOpts.RuntimeState[RuntimeStateSkillInclude],
	)
	require.Equal(
		t,
		[]string{"internal"},
		runOpts.RuntimeState[RuntimeStateSkillExclude],
	)
	require.Equal(
		t,
		[]string{"/skills/retail"},
		runOpts.RuntimeState[RuntimeStateSkillRoots],
	)
	require.Equal(
		t,
		[]string{"retail-index"},
		runOpts.RuntimeState[RuntimeStateKnowledgeIndexes],
	)
	require.Equal(
		t,
		[]string{"mcp-retail"},
		runOpts.RuntimeState[RuntimeStateToolSets],
	)
	require.Equal(
		t,
		map[string]string{"crm": "secret://retail/crm"},
		runOpts.RuntimeState[RuntimeStateToolCredentialRefs],
	)
	require.Equal(
		t,
		string(IsolationModeService),
		runOpts.RuntimeState[RuntimeStateIsolationMode],
	)
	require.Equal(
		t,
		true,
		runOpts.RuntimeState[RuntimeStateIsolationAgentCache],
	)
	require.Equal(
		t,
		true,
		runOpts.RuntimeState[RuntimeStateIsolationToolSetCache],
	)
	require.Equal(
		t,
		"sidecar",
		runOpts.RuntimeState[RuntimeStateIsolationServiceMode],
	)

	sourceFilter["tenant"] = "changed"
	sourceState["plan"] = "changed"
	sourceExtraModel["reasoning_effort"] = "changed"
	require.Equal(t, testProfileRetail, runOpts.KnowledgeFilter["tenant"])
	require.Equal(t, "vip", runOpts.RuntimeState["plan"])
	require.Equal(
		t,
		"medium",
		runOpts.ModelRequestExtraFields["reasoning_effort"],
	)

	require.True(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: testToolAllowed},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: testToolBlocked},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: testToolOther},
	))
	require.True(t, runOpts.ToolExecutionFilter(
		context.Background(),
		testTool{name: testToolExecute},
	))
	require.False(t, runOpts.ToolExecutionFilter(
		context.Background(),
		testTool{name: testToolOther},
	))
}

func TestRunOptionsEdgeCases(t *testing.T) {
	t.Parallel()

	require.Empty(t, RunOptions(Profile{}))

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		ID: testProfileRetail,
	})...)
	require.Equal(
		t,
		testProfileRetail,
		runOpts.RuntimeState[RuntimeStateProfileID],
	)

	runOpts = agent.NewRunOptions(RunOptions(Profile{
		Version: "2026-05-09",
		Tools: ToolPolicy{
			Exclude: []string{" ", testToolBlocked},
		},
	})...)
	require.Equal(
		t,
		"2026-05-09",
		runOpts.RuntimeState[RuntimeStateProfileVersion],
	)
	require.True(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: testToolAllowed},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: testToolBlocked},
	))
	require.False(t, runOpts.ToolFilter(context.Background(), nil))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		nilDeclarationTool{},
	))
}

func TestExtensionFromRequestExtensions(t *testing.T) {
	t.Parallel()

	_, ok, err := ExtensionFromRequestExtensions(nil)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = ExtensionFromRequestExtensions(
		map[string]json.RawMessage{
			"other": json.RawMessage(`{}`),
		},
	)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = ExtensionFromRequestExtensions(
		map[string]json.RawMessage{
			ExtensionKey: nil,
		},
	)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = ExtensionFromRequestExtensions(
		map[string]json.RawMessage{
			ExtensionKey: json.RawMessage("null"),
		},
	)
	require.NoError(t, err)
	require.False(t, ok)

	raw, err := json.Marshal(Extension{
		ProfileID: " retail ",
		TenantID:  " tenant-a ",
	})
	require.NoError(t, err)

	ext, ok, err := ExtensionFromRequestExtensions(
		map[string]json.RawMessage{
			ExtensionKey: raw,
		},
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, testProfileRetail, ext.ProfileID)
	require.Equal(t, "tenant-a", ext.TenantID)

	_, _, err = ExtensionFromRequestExtensions(
		map[string]json.RawMessage{
			ExtensionKey: json.RawMessage("{"),
		},
	)
	require.Error(t, err)
}

func TestContextProfile(t *testing.T) {
	t.Parallel()

	require.Nil(t, WithProfile(nil, Profile{}))

	_, ok := ProfileFromContext(nil)
	require.False(t, ok)
	_, ok = ProfileFromContext(context.Background())
	require.False(t, ok)

	ctx := WithProfile(context.Background(), Profile{
		ID:      testProfileRetail,
		AppName: "retail-app",
		State: map[string]any{
			"plan": "vip",
		},
	})

	profile, ok := ProfileFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, testProfileRetail, profile.ID)
	require.Equal(t, "retail-app", AppNameFromContext(ctx, "fallback"))

	profile.State["plan"] = "changed"
	profile, ok = ProfileFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "vip", profile.State["plan"])
	require.Equal(t, "fallback", AppNameFromContext(
		context.Background(),
		" fallback ",
	))
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

	_, err = ResolveWorkdir(ctx, denied)
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

func TestTraceFields(t *testing.T) {
	t.Parallel()

	fields := TraceFields(Profile{
		ID:      testProfileRetail,
		Version: "v1",
		AppName: "retail-app",
		Workspace: WorkspacePolicy{
			Workdir:      "/workspace/retail",
			AllowedRoots: []string{"/workspace/retail"},
		},
		Credentials: CredentialPolicy{
			AllowedRefs: []string{"secret://retail/crm"},
		},
		Tools: ToolPolicy{
			ToolSets: []string{"crm"},
			CredentialRefs: map[string]string{
				"crm": "secret://retail/crm",
			},
		},
		Knowledge: KnowledgePolicy{
			Indexes: []string{"retail-index"},
		},
		Isolation: IsolationPolicy{
			Mode:       IsolationModeService,
			AgentCache: true,
		},
	})

	require.Equal(t, testProfileRetail, fields["profile_id"])
	require.Equal(t, "v1", fields["profile_version"])
	require.Equal(t, "retail-app", fields["profile_app_name"])
	require.Equal(t, 1, fields["credential_ref_count"])
	require.Equal(t, 1, fields["tool_credential_ref_count"])
	require.Equal(t, 1, fields["workspace_allowed_root_count"])
	require.Equal(t, true, fields["has_workspace_workdir"])
	require.Equal(t, string(IsolationModeService), fields["isolation_mode"])
	require.Equal(t, true, fields["agent_cache"])
	require.NotContains(t, fields, "workspace_workdir")
	require.NotContains(t, fields, "workspace_allowed_roots")
}

func TestResolverFuncNil(t *testing.T) {
	t.Parallel()

	var resolver ResolverFunc
	profile, err := resolver.Resolve(context.Background(), Request{})
	require.NoError(t, err)
	require.Empty(t, profile)
}

func TestResolverFuncCallsFunction(t *testing.T) {
	t.Parallel()

	resolver := ResolverFunc(func(
		ctx context.Context,
		req Request,
	) (Profile, error) {
		require.NotNil(t, ctx)
		require.Equal(t, testProfileRetail, req.ProfileID)
		return Profile{ID: req.ProfileID}, nil
	})

	profile, err := resolver.Resolve(context.Background(), Request{
		ProfileID: testProfileRetail,
	})
	require.NoError(t, err)
	require.Equal(t, testProfileRetail, profile.ID)
}

func TestMapResolverUnknownProfile(t *testing.T) {
	t.Parallel()

	resolver := NewMapResolver(Config{
		Profiles: map[string]Profile{
			testProfileRetail: {},
		},
	})
	_, err := resolver.Resolve(context.Background(), Request{
		ProfileID: testProfileDefault,
	})
	require.True(t, errors.Is(err, ErrProfileNotFound))
}
