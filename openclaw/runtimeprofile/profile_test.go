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
		},
		Knowledge: KnowledgePolicy{
			Filter: sourceFilter,
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
