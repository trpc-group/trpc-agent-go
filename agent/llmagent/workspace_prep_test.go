//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspaceprep"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestWithWorkspaceBootstrap_SetsOptionsField exercises the public
// llmagent.WithWorkspaceBootstrap helper. The option should flow the
// caller-supplied spec through to Options.workspaceBootstrap
// verbatim; that value is later consumed by workspacePrepOptions.
func TestWithWorkspaceBootstrap_SetsOptionsField(t *testing.T) {
	spec := codeexecutor.WorkspaceBootstrapSpec{
		Files: []codeexecutor.WorkspaceFile{{
			Target:  "work/seed.txt",
			Content: []byte("seed"),
		}},
		Commands: []codeexecutor.WorkspaceCommand{{
			Cmd:        "bash",
			Args:       []string{"-lc", "true"},
			MarkerPath: "work/.done",
		}},
	}

	opts := &Options{}
	WithWorkspaceBootstrap(spec)(opts)
	require.Len(t, opts.workspaceBootstrap.Files, 1)
	require.Len(t, opts.workspaceBootstrap.Commands, 1)
	require.Equal(t,
		"work/seed.txt",
		opts.workspaceBootstrap.Files[0].Target,
	)
	require.Equal(t,
		"work/.done",
		opts.workspaceBootstrap.Commands[0].MarkerPath,
	)
}

// TestWithWorkspacePreparersDisabled_Toggles validates both branches
// of the disable switch. Keeping the API boolean-driven lets test
// fixtures opt back into the legacy path without resorting to
// unexported struct fields.
func TestWithWorkspacePreparersDisabled_Toggles(t *testing.T) {
	opts := &Options{}
	WithWorkspacePreparersDisabled(true)(opts)
	require.True(t, opts.disableWorkspacePreparers)
	WithWorkspacePreparersDisabled(false)(opts)
	require.False(t, opts.disableWorkspacePreparers)
}

// TestWorkspacePrepOptions_UsesInvocationRepoNotAgentDefault pins
// down the invocation-level alignment: workspace_exec's
// loaded-skills reconcile must use the repository the caller has
// already resolved for the current invocation (effectiveSkills),
// not opts.skillsRepository. Without this alignment a surface-patch
// repo override would be honored by the skill tools but silently
// ignored by the reconciler, and the model context could drift
// away from the materialized skill working copy.
func TestWorkspacePrepOptions_UsesInvocationRepoNotAgentDefault(t *testing.T) {
	agentDefaultRepo := &mockSkillRepository{
		summaries: []skill.Summary{{Name: "agent-default"}},
	}
	invocationRepo := &mockSkillRepository{
		summaries: []skill.Summary{{Name: "invocation-scoped"}},
	}

	// repo == nil: even with a non-nil agent-default repo, the
	// loaded-skills option must not be attached, because the
	// caller (typically a workspace_exec-only setup without skill
	// support) asked for no loaded-skills wiring.
	opts := &Options{skillsRepository: agentDefaultRepo}
	noneResolved := workspacePrepOptions(opts, nil)
	require.Empty(t, noneResolved,
		"nil invocation repo must suppress WithLoadedSkills "+
			"even when opts.skillsRepository is non-nil")

	// repo != nil: the option must be attached, and it is driven
	// by the resolved repo; the agent-default repo is irrelevant.
	withResolved := workspacePrepOptions(opts, invocationRepo)
	require.Len(t, withResolved, 1,
		"resolved invocation repo must yield one WithLoadedSkills option")

	// With a bootstrap spec, the bootstrap option is added
	// regardless of which repo drove the decision, and the repo
	// still controls whether WithLoadedSkills shows up.
	optsWithBootstrap := &Options{
		skillsRepository: agentDefaultRepo,
		workspaceBootstrap: codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target:  "work/seed.txt",
				Content: []byte("seed"),
			}},
		},
	}
	bootstrapOnly := workspacePrepOptions(optsWithBootstrap, nil)
	require.Len(t, bootstrapOnly, 1,
		"bootstrap must always be attached; loaded-skills must stay "+
			"suppressed when invocation repo is nil")
	bootstrapAndSkills := workspacePrepOptions(optsWithBootstrap, invocationRepo)
	require.Len(t, bootstrapAndSkills, 2,
		"bootstrap plus resolved invocation repo must yield two options")

	// The disable switch still wins over everything.
	disabledOpts := &Options{
		skillsRepository:          agentDefaultRepo,
		disableWorkspacePreparers: true,
	}
	require.Nil(
		t,
		workspacePrepOptions(disabledOpts, invocationRepo),
		"disableWorkspacePreparers must override invocation-scoped wiring",
	)
}

// TestInvocationToolSurface_WorkspaceExecUsesPatchedSkillRepo exercises
// the full wiring chain: agent construction with a default skill
// repository, invocation-scoped SurfacePatch that overrides to a
// different repository, and then InvocationToolSurface which
// ultimately passes the effective repository into workspace_exec's
// loaded-skills reconciler. The test asserts that the repo seen by
// workspace_exec matches the patched one, not the agent default, so
// that a future refactor cannot silently regress the alignment by
// changing just one intermediate hop.
func TestInvocationToolSurface_WorkspaceExecUsesPatchedSkillRepo(t *testing.T) {
	agentDefaultRepo := &mockSkillRepository{
		summaries: []skill.Summary{{Name: "agent-default"}},
	}
	patchedRepo := &mockSkillRepository{
		summaries: []skill.Summary{{Name: "invocation-scoped"}},
	}

	agt := New(
		"test-agent",
		WithModel(newDummyModel()),
		WithSkills(agentDefaultRepo),
		WithCodeExecutor(localexec.New()),
	)

	var patch agent.SurfacePatch
	patch.SetSkillRepository(patchedRepo)
	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("test-agent"),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithSurfacePatchForNode("test-agent", patch),
		)),
	)
	agt.setupInvocation(inv)

	tools, _ := agt.InvocationToolSurface(context.Background(), inv)
	var wsExec tool.Tool
	for _, tl := range tools {
		if tl == nil || tl.Declaration() == nil {
			continue
		}
		if tl.Declaration().Name == "workspace_exec" {
			wsExec = tl
			break
		}
	}
	require.NotNil(t, wsExec, "workspace_exec must be wired in InvocationToolSurface")

	gotRepo := loadedSkillsRepoFromExecTool(t, wsExec)
	require.NotNil(t, gotRepo,
		"loaded-skills provider must be installed when the resolved "+
			"invocation repo is non-nil")
	require.Same(t, skill.Repository(patchedRepo), gotRepo,
		"workspace_exec must reconcile loaded skills through the "+
			"surface-patched repo, not the agent default")
	require.NotSame(t, skill.Repository(agentDefaultRepo), gotRepo,
		"workspace_exec must not silently fall back to opts.skillsRepository "+
			"when the invocation has already resolved a different repo")
}

// loadedSkillsRepoFromExecTool peeks into a workspace_exec ExecTool
// to recover the skill.Repository that its loaded-skills provider
// was wired with. It relies on unexported fields in two packages
// (tool/workspaceexec and internal/workspaceprep) and is therefore
// strictly a test helper: production code must not do this. The
// helper returns nil when no loaded-skills provider is installed,
// which is itself a meaningful assertion for negative cases.
func loadedSkillsRepoFromExecTool(
	t *testing.T, tl tool.Tool,
) skill.Repository {
	t.Helper()
	root := reflect.ValueOf(tl)
	require.Equal(t, reflect.Ptr, root.Kind(),
		"workspace_exec tool is expected to be a pointer type")
	execVal := root.Elem()
	providersField := execVal.FieldByName("providers")
	require.True(t, providersField.IsValid(),
		"ExecTool.providers field must exist")
	providersVal := reflect.NewAt(
		providersField.Type(),
		unsafe.Pointer(providersField.UnsafeAddr()),
	).Elem()
	providers, ok := providersVal.Interface().([]workspaceprep.Provider)
	require.True(t, ok,
		"ExecTool.providers is not []workspaceprep.Provider")
	for _, p := range providers {
		if p == nil || p.Name() != "loaded_skills" {
			continue
		}
		pv := reflect.ValueOf(p)
		if pv.Kind() == reflect.Ptr {
			pv = pv.Elem()
		}
		repoField := pv.FieldByName("repo")
		if !repoField.IsValid() {
			continue
		}
		repoVal := reflect.NewAt(
			repoField.Type(),
			unsafe.Pointer(repoField.UnsafeAddr()),
		).Elem()
		repo, _ := repoVal.Interface().(skill.Repository)
		return repo
	}
	return nil
}
