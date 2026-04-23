//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// TestWithWorkspaceBootstrap_EmptySpecIsNoop pins the documented
// contract that an empty bootstrap spec does not wire a reconciler
// or a conversation-files provider. This keeps the zero-config
// workspace_exec path cheap and prevents a silent expansion of its
// behavior when callers construct an empty spec defensively.
func TestWithWorkspaceBootstrap_EmptySpecIsNoop(t *testing.T) {
	tl := NewExecTool(
		localexec.New(),
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{}),
	)
	require.Nil(t, tl.reconciler)
	require.Empty(t, tl.providers)
	require.False(t, tl.conversationFilesWired)
}

// TestWithWorkspaceBootstrap_WiresBootstrapAndConversationFiles proves
// that installing any non-empty bootstrap spec also auto-wires the
// conversation-files provider and a reconciler. addPreparer owns
// this coupling so that every tool built on workspaceprep gets a
// consistent default for message-attached file staging.
func TestWithWorkspaceBootstrap_WiresBootstrapAndConversationFiles(t *testing.T) {
	tl := NewExecTool(
		localexec.New(),
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target:  "work/seed.txt",
				Content: []byte("seed"),
			}},
			Commands: []codeexecutor.WorkspaceCommand{{
				Cmd:        "bash",
				Args:       []string{"-lc", "true"},
				MarkerPath: "work/.done",
			}},
		}),
	)
	require.NotNil(t, tl.reconciler)
	require.Len(t, tl.providers, 2,
		"bootstrap provider + conversation-files provider must "+
			"both be registered by a single WithWorkspaceBootstrap call")
	require.True(t, tl.conversationFilesWired)

	// A second call to addPreparer must not double-register the
	// conversation-files provider; only the new provider is added.
	tl2 := NewExecTool(
		localexec.New(),
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target:  "work/a.txt",
				Content: []byte("a"),
			}},
		}),
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target:  "work/b.txt",
				Content: []byte("b"),
			}},
		}),
	)
	require.Len(t, tl2.providers, 3,
		"two bootstrap providers + one conversation-files provider")
}

// TestWithWorkspaceBootstrap_InvalidSpecPanics guards the explicit
// "crash over silently ignore" contract. A bootstrap spec with a
// missing Target is always a configuration bug and should fail loud
// at agent construction time rather than at the first reconcile.
func TestWithWorkspaceBootstrap_InvalidSpecPanics(t *testing.T) {
	require.Panics(t, func() {
		_ = WithWorkspaceBootstrap(
			codeexecutor.WorkspaceBootstrapSpec{
				Files: []codeexecutor.WorkspaceFile{{
					Target: "",
				}},
			},
		)
	})
}

// TestWithWorkspaceBootstrap_EndToEnd verifies that a configured
// bootstrap spec actually materializes the declared artifacts before
// the tool runs its first command. This is the smallest full-loop
// test that exercises toInternalBootstrapSpec -> BootstrapProvider
// -> reconcileWorkspace -> runOne.
func TestWithWorkspaceBootstrap_EndToEnd(t *testing.T) {
	exec := localexec.New()
	tl := NewExecTool(
		exec,
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{{
				Target:  "work/seed.txt",
				Content: []byte("seed"),
				Mode:    0o644,
			}},
		}),
	)

	// The first Call drives reconcileWorkspace which materializes the
	// bootstrap file; the command itself then reads it back. If the
	// end-to-end wiring is broken the cat command fails because the
	// file was never staged.
	args := execInput{
		Command: "cat work/seed.txt",
		Timeout: timeoutSecSmall,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)
	out, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)

	eo, ok := out.(execOutput)
	require.True(t, ok)
	require.Equal(t, "exited", eo.Status)
	require.Equal(t, "seed", eo.Output)
}

func TestWithLoadedSkills_NilRepoIsNoop(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithLoadedSkills(nil))
	require.Nil(t, tl.reconciler)
	require.Empty(t, tl.providers)
	require.False(t, tl.conversationFilesWired)
}

func TestWithLoadedSkills_WiresProvider(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "echoer")
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	tl := NewExecTool(
		localexec.New(),
		WithLoadedSkills(repo),
	)
	require.NotNil(t, tl.reconciler)
	require.Len(t, tl.providers, 2,
		"loaded-skills provider + conversation-files provider")
	require.True(t, tl.conversationFilesWired)
}

// TestWriteStdinTool_Declaration pins the public schema contract for
// workspace_write_stdin. The schema is the only way the model learns
// how to follow up on a backgrounded session, so structural changes
// here are user-visible and should be intentional.
func TestWriteStdinTool_Declaration(t *testing.T) {
	decl := NewWriteStdinTool(NewExecTool(localexec.New())).Declaration()
	require.Equal(t, "workspace_write_stdin", decl.Name)
	require.NotEmpty(t, decl.Description)
	require.NotNil(t, decl.InputSchema)
	require.ElementsMatch(t,
		[]string{"session_id"}, decl.InputSchema.Required,
	)
	for _, k := range []string{
		"session_id", "sessionId", "chars",
		"yield_time_ms", "yieldMs",
		"append_newline", "submit",
	} {
		_, ok := decl.InputSchema.Properties[k]
		require.True(t, ok, "schema must expose %q", k)
	}
	require.NotNil(t, decl.OutputSchema)
}

func TestKillSessionTool_Declaration(t *testing.T) {
	decl := NewKillSessionTool(NewExecTool(localexec.New())).Declaration()
	require.Equal(t, "workspace_kill_session", decl.Name)
	require.NotEmpty(t, decl.Description)
	require.NotNil(t, decl.InputSchema)
	require.ElementsMatch(t,
		[]string{"session_id"}, decl.InputSchema.Required,
	)
	for _, k := range []string{"session_id", "sessionId"} {
		_, ok := decl.InputSchema.Properties[k]
		require.True(t, ok, "schema must expose %q", k)
	}
	require.NotNil(t, decl.OutputSchema)
	require.ElementsMatch(t,
		[]string{"ok", "session_id", "status"},
		decl.OutputSchema.Required,
	)
}
