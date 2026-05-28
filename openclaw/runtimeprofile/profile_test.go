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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
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

type declarationOnlyTool struct {
	name string
}

func (t declarationOnlyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

type testTool struct {
	name               string
	toolSetName        string
	knowledgeIndexName string
}

func (t testTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t testTool) ToolSetName() string {
	return t.toolSetName
}

func (t testTool) KnowledgeIndexName() string {
	return t.knowledgeIndexName
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

func TestMapResolverCatalogEdgeCases(t *testing.T) {
	t.Parallel()

	var nilResolver *MapResolver
	ids, err := nilResolver.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids)

	appNames, err := nilResolver.AppNames(context.Background())
	require.NoError(t, err)
	require.Empty(t, appNames)

	resolver := NewMapResolver(Config{
		Profiles: map[string]Profile{
			"empty": {},
			testProfileRetail: {
				AppName: "retail-app",
			},
			"retail-alias": {
				ID:      "retail-alias",
				AppName: "retail-app",
			},
			"isolated": {
				Isolation: IsolationPolicy{
					Mode: IsolationModeProfileCache,
				},
			},
		},
	})

	ids, err = resolver.ProfileIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{
		"empty",
		"isolated",
		testProfileRetail,
		"retail-alias",
	}, ids)

	appNames, err = resolver.AppNames(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"isolated", "retail-app"}, appNames)
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
		testTool{name: testToolAllowed, toolSetName: "mcp-retail"},
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

func TestRunOptionsFiltersProfileToolSets(t *testing.T) {
	t.Parallel()

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		Tools: ToolPolicy{
			ToolSets: []string{"crm"},
		},
	})...)

	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: "direct"},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		declarationOnlyTool{name: "direct"},
	))
	require.True(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: "crm_search", toolSetName: "crm"},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: "erp_search", toolSetName: "erp"},
	))

	concreteOpts := agent.NewRunOptions(RunOptions(Profile{
		Tools: ToolPolicy{
			Include:  []string{"crm_search"},
			ToolSets: []string{"crm"},
		},
	})...)
	require.True(t, concreteOpts.ToolFilter(
		context.Background(),
		testTool{name: "crm_search", toolSetName: "crm"},
	))
	require.False(t, concreteOpts.ToolFilter(
		context.Background(),
		testTool{name: "crm_update", toolSetName: "crm"},
	))
}

func TestRunOptionsFiltersProfileKnowledgeIndexes(t *testing.T) {
	t.Parallel()

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		Knowledge: KnowledgePolicy{
			Indexes: []string{"docs"},
		},
	})...)

	require.True(t, runOpts.ToolFilter(
		context.Background(),
		declarationOnlyTool{name: "direct"},
	))
	require.True(t, runOpts.ToolFilter(
		context.Background(),
		testTool{
			name:               "knowledge_search",
			knowledgeIndexName: "docs",
		},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{
			name:               "faq_knowledge_search",
			knowledgeIndexName: "faq",
		},
	))
}

func TestRunOptionsFiltersProfileCredentialRefs(t *testing.T) {
	t.Parallel()

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		Tools: ToolPolicy{
			CredentialRefs: map[string]string{
				"crm":          "secret://tenant/crm",
				"crm_delete":   "secret://tenant/admin",
				"erp_search":   "secret://tenant/erp",
				"empty_search": " ",
			},
		},
		Credentials: CredentialPolicy{
			AllowedRefs: []string{"secret://tenant/crm"},
		},
	})...)

	require.True(t, runOpts.ToolFilter(
		context.Background(),
		declarationOnlyTool{name: "direct"},
	))
	require.True(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: "crm_search", toolSetName: "crm"},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: "crm_delete", toolSetName: "crm"},
	))
	require.False(t, runOpts.ToolFilter(
		context.Background(),
		testTool{name: "erp_search"},
	))
	require.Equal(
		t,
		map[string]string{"crm": "secret://tenant/crm"},
		runOpts.RuntimeState[RuntimeStateToolCredentialRefs],
	)
}

func TestRunOptionsDropsReservedUserRuntimeState(t *testing.T) {
	t.Parallel()

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		ID: testProfileRetail,
		State: map[string]any{
			"plan":                         "vip",
			RuntimeStateProfileID:          "spoofed",
			RuntimeStateToolCredentialRefs: "secret://tenant/admin",
		},
		Tools: ToolPolicy{
			CredentialRefs: map[string]string{
				"crm_delete": "secret://tenant/admin",
			},
		},
		Credentials: CredentialPolicy{
			AllowedRefs: []string{"secret://tenant/crm"},
		},
	})...)

	require.Equal(t, "vip", runOpts.RuntimeState["plan"])
	require.Equal(
		t,
		testProfileRetail,
		runOpts.RuntimeState[RuntimeStateProfileID],
	)
	require.NotContains(
		t,
		runOpts.RuntimeState,
		RuntimeStateToolCredentialRefs,
	)
}

func TestRunOptionsUsesProfileIDForRuntimeIsolation(t *testing.T) {
	t.Parallel()

	runOpts := agent.NewRunOptions(RunOptions(Profile{
		ID: testProfileRetail,
		Isolation: IsolationPolicy{
			Mode: IsolationModeProfileCache,
		},
	})...)
	require.Equal(t, testProfileRetail, runOpts.AppName)

	runOpts = agent.NewRunOptions(RunOptions(Profile{
		ID:      testProfileRetail,
		AppName: "retail-app",
		Isolation: IsolationPolicy{
			Mode: IsolationModeProfileCache,
		},
	})...)
	require.Equal(t, "retail-app", runOpts.AppName)
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

func TestHasProfileContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile Profile
	}{
		{name: "id", profile: Profile{ID: testProfileRetail}},
		{name: "version", profile: Profile{Version: "v1"}},
		{name: "app", profile: Profile{AppName: "retail-app"}},
		{name: "agent", profile: Profile{AgentName: "assistant"}},
		{name: "model", profile: Profile{ModelName: "gpt-retail"}},
		{name: "instruction", profile: Profile{
			Prompt: Prompt{Instruction: "help"},
		}},
		{name: "system", profile: Profile{
			Prompt: Prompt{SystemPrompt: "system"},
		}},
		{name: "tool include", profile: Profile{
			Tools: ToolPolicy{Include: []string{testToolAllowed}},
		}},
		{name: "tool exclude", profile: Profile{
			Tools: ToolPolicy{Exclude: []string{testToolBlocked}},
		}},
		{name: "tool execution include", profile: Profile{
			Tools: ToolPolicy{ExecutionInclude: []string{testToolExecute}},
		}},
		{name: "tool execution exclude", profile: Profile{
			Tools: ToolPolicy{ExecutionExclude: []string{testToolBlocked}},
		}},
		{name: "toolsets", profile: Profile{
			Tools: ToolPolicy{ToolSets: []string{"crm"}},
		}},
		{name: "tool credentials", profile: Profile{
			Tools: ToolPolicy{
				CredentialRefs: map[string]string{
					"crm": "secret://retail/crm",
				},
			},
		}},
		{name: "knowledge indexes", profile: Profile{
			Knowledge: KnowledgePolicy{Indexes: []string{"retail"}},
		}},
		{name: "knowledge filter", profile: Profile{
			Knowledge: KnowledgePolicy{
				Filter: map[string]any{"tenant": testProfileRetail},
			},
		}},
		{name: "workspace workdir", profile: Profile{
			Workspace: WorkspacePolicy{Workdir: "/workspace/retail"},
		}},
		{name: "workspace roots", profile: Profile{
			Workspace: WorkspacePolicy{
				AllowedRoots: []string{"/workspace/retail"},
			},
		}},
		{name: "credentials", profile: Profile{
			Credentials: CredentialPolicy{
				AllowedRefs: []string{"secret://retail/crm"},
			},
		}},
		{name: "skill include", profile: Profile{
			Skills: SkillPolicy{Include: []string{"crm"}},
		}},
		{name: "skill exclude", profile: Profile{
			Skills: SkillPolicy{Exclude: []string{"draft"}},
		}},
		{name: "skill roots", profile: Profile{
			Skills: SkillPolicy{Roots: []string{"/skills/retail"}},
		}},
		{name: "isolation mode", profile: Profile{
			Isolation: IsolationPolicy{Mode: IsolationModeService},
		}},
		{name: "isolation agent cache", profile: Profile{
			Isolation: IsolationPolicy{AgentCache: true},
		}},
		{name: "isolation toolset cache", profile: Profile{
			Isolation: IsolationPolicy{ToolSetCache: true},
		}},
		{name: "isolation service", profile: Profile{
			Isolation: IsolationPolicy{ServiceMode: "sidecar"},
		}},
		{name: "state", profile: Profile{
			State: map[string]any{"plan": "vip"},
		}},
		{name: "extra model", profile: Profile{
			ExtraModel: map[string]any{"reasoning_effort": "medium"},
		}},
	}

	require.False(t, HasProfile(Profile{}))
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.True(t, HasProfile(tt.profile))
		})
	}
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

	ctx = WithProfile(context.Background(), Profile{
		ID: testProfileRetail,
		Isolation: IsolationPolicy{
			Mode: IsolationModeProfileCache,
		},
	})
	require.Equal(t, testProfileRetail, AppNameFromContext(ctx, "fallback"))

	require.Equal(t, "fallback", AppNameFromContext(
		context.Background(),
		" fallback ",
	))
}

func TestContextRequest(t *testing.T) {
	t.Parallel()

	require.Nil(t, WithRequest(nil, Request{}))

	_, ok := RequestFromContext(nil)
	require.False(t, ok)
	_, ok = RequestFromContext(context.Background())
	require.False(t, ok)

	raw := json.RawMessage(`{"profile_id":"retail"}`)
	ctx := WithRequest(context.Background(), Request{
		Channel:   " wecom ",
		ProfileID: " retail ",
		TenantID:  " tenant-a ",
		UserID:    " user-a ",
		SessionID: " session-a ",
		RequestID: " request-a ",
		Extensions: map[string]json.RawMessage{
			ExtensionKey: raw,
		},
	})

	req, ok := RequestFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "wecom", req.Channel)
	require.Equal(t, testProfileRetail, req.ProfileID)
	require.Equal(t, "tenant-a", req.TenantID)
	require.Equal(t, "user-a", req.UserID)
	require.Equal(t, "session-a", req.SessionID)
	require.Equal(t, "request-a", req.RequestID)
	require.Equal(t, raw, req.Extensions[ExtensionKey])

	req.Extensions[ExtensionKey][0] = '['
	req, ok = RequestFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, raw, req.Extensions[ExtensionKey])
}

func TestTraceFields(t *testing.T) {
	t.Parallel()

	require.Nil(t, TraceFields(Profile{}))

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
		Skills: SkillPolicy{
			Roots: []string{"/workspace/retail/skills"},
		},
		Isolation: IsolationPolicy{
			Mode:         IsolationModeService,
			ServiceMode:  "sidecar",
			AgentCache:   true,
			ToolSetCache: true,
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
	require.Equal(t, "sidecar", fields["service_mode"])
	require.Equal(t, true, fields["agent_cache"])
	require.Equal(t, true, fields["toolset_cache"])
	require.Equal(t, 1, fields["skill_root_count"])
	require.NotContains(t, fields, "workspace_workdir")
	require.NotContains(t, fields, "workspace_allowed_roots")

	fields = TraceFields(Profile{
		ID: testProfileRetail,
		Isolation: IsolationPolicy{
			Mode: IsolationModeProfileCache,
		},
	})
	require.Equal(t, testProfileRetail, fields["profile_app_name"])
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
