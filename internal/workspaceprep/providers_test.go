//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceprep

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	sessionpkg "trpc.group/trpc-go/trpc-agent-go/session"
	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestBootstrapProvider_FilesAndCommands(t *testing.T) {
	ctx := context.Background()
	eng, ws := newTestEngine(t)

	provider, err := NewBootstrapProvider(BootstrapSpec{
		Files: []FileSpec{{
			Target:  "work/seed.txt",
			Content: []byte("seed"),
		}},
		Commands: []CommandSpec{{
			Cmd:        "bash",
			Args:       []string{"-lc", "mkdir -p work && echo init > work/init.log"},
			MarkerPath: "work/.init",
		}},
	})
	require.NoError(t, err)

	reqs, err := provider.Requirements(ctx, nil)
	require.NoError(t, err)
	require.Len(t, reqs, 2)

	rec := NewReconciler()
	_, err = rec.Reconcile(ctx, eng, ws, reqs)
	require.NoError(t, err)

	seed, err := os.ReadFile(filepath.Join(ws.Path, "work/seed.txt"))
	require.NoError(t, err)
	require.Equal(t, "seed", string(seed))

	initLog, err := os.ReadFile(filepath.Join(ws.Path, "work/init.log"))
	require.NoError(t, err)
	require.Equal(t, "init\n", string(initLog))

	_, err = os.Stat(filepath.Join(ws.Path, "work/.init"))
	require.NoError(t, err)
}

func TestBootstrapProvider_RejectsInvalidSpecs(t *testing.T) {
	_, err := NewBootstrapProvider(BootstrapSpec{
		Files: []FileSpec{{Target: ""}},
	})
	require.Error(t, err)

	_, err = NewBootstrapProvider(BootstrapSpec{
		Commands: []CommandSpec{{Cmd: ""}},
	})
	require.Error(t, err)
}

func TestConversationFilesProvider_NoInvocationIsNoop(t *testing.T) {
	ctx := context.Background()
	provider := NewConversationFilesProvider()
	reqs, err := provider.Requirements(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, reqs)
}

// TestConversationFilesProvider_NoSessionStillStagesMessageFiles locks in
// the contract that a user message carrying a file part triggers
// conversation-file staging even when no session is attached to the
// invocation. The legacy StageConversationFiles helper supported this
// case, and gating the provider on inv.Session != nil silently regressed
// it. The reconciler is responsible for re-checking fingerprints and
// sentinels, so emitting a requirement here is the minimum we owe the
// current turn.
func TestConversationFilesProvider_NoSessionStillStagesMessageFiles(
	t *testing.T,
) {
	ctx := context.Background()
	provider := NewConversationFilesProvider()

	inv := &agent.Invocation{
		Message: model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{
					FileID: "upload-1",
					Name:   "note.txt",
					Data:   []byte("hello"),
				},
			}},
		},
	}

	reqs, err := provider.Requirements(ctx, inv)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, KindConversationFile, reqs[0].Kind())
}

// TestLoadedSkillsFromInvocation_ScansScopedAndLegacyPrefixes locks in
// the contract that the loaded-skills provider honors both the
// agent-scoped state keys written by modern skill_load calls and the
// legacy unscoped prefix still present in older sessions. It also
// guards against regressing to a raw map read by exercising a real
// Session, whose SnapshotState copies entries behind a read lock.
func TestLoadedSkillsFromInvocation_ScansScopedAndLegacyPrefixes(t *testing.T) {
	sess := sessionpkg.NewSession("app", "user", "sid")
	// Agent-scoped entry for the active agent.
	sess.SetState(rootskill.LoadedKey("agent-a", "scoped_skill"), []byte{1})
	// Legacy unscoped entry; should also surface.
	sess.SetState(rootskill.StateKeyLoadedPrefix+"legacy_skill", []byte{1})
	// Unrelated key must be ignored.
	sess.SetState("temp:other:noise", []byte{1})
	// Entry scoped to a different agent must not leak into this one.
	sess.SetState(rootskill.LoadedKey("agent-b", "other_agent_skill"), []byte{1})

	inv := &agent.Invocation{
		AgentName: "agent-a",
		Session:   sess,
	}

	got := loadedSkillsFromInvocation(inv)
	require.Equal(t, []string{"legacy_skill", "scoped_skill"}, got)
}

// TestAllConversationFiles_FiltersByRoleAndIncludesCurrentMessage
// pins down two behaviors that previously lived in
// workspaceinput.StageConversationFiles:
//
//  1. Session events are considered only when the message role is
//     model.RoleUser (assistant/tool file parts are dropped).
//  2. The current invocation message always contributes its file
//     parts because it represents the active user turn that has not
//     yet been appended to the event log.
func TestAllConversationFiles_FiltersByRoleAndIncludesCurrentMessage(t *testing.T) {
	sess := sessionpkg.NewSession("app", "user", "sid")
	sess.Events = append(sess.Events,
		event.Event{Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleUser,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeFile,
					File: &model.File{FileID: "user-past"},
				}},
			},
		}}}},
		// Assistant-authored file must be dropped.
		event.Event{Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeFile,
					File: &model.File{FileID: "assistant-output"},
				}},
			},
		}}}},
		// Tool-authored file must also be dropped.
		event.Event{Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleTool,
				ContentParts: []model.ContentPart{{
					Type: model.ContentTypeFile,
					File: &model.File{FileID: "tool-output"},
				}},
			},
		}}}},
	)

	inv := &agent.Invocation{
		Session: sess,
		Message: model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{FileID: "user-current"},
			}},
		},
	}

	got := allConversationFiles(inv)
	require.Equal(t, []string{"id:user-current", "id:user-past"}, got)
}

// TestStaticProvider_Name pins the public Name() contract for the
// bootstrap provider. Reconciler error messages include the provider
// name to help operators root-cause a failing requirement, so the
// label must remain stable.
func TestStaticProvider_Name(t *testing.T) {
	p, err := NewBootstrapProvider(BootstrapSpec{
		Files: []FileSpec{{Target: "work/x", Content: []byte("y")}},
	})
	require.NoError(t, err)
	require.Equal(t, "bootstrap", p.Name())
}

func TestLoadedSkillsProvider_RequiresRepository(t *testing.T) {
	_, err := NewLoadedSkillsProvider(nil)
	require.Error(t, err)
}

func TestLoadedSkillsProvider_NameAndRequirements(t *testing.T) {
	ctx := context.Background()

	repo, _ := newFSSkillRepo(t, "echoer", "body")
	provider, err := NewLoadedSkillsProvider(repo)
	require.NoError(t, err)
	require.Equal(t, "loaded_skills", provider.Name())

	// No invocation -> no requirements.
	reqs, err := provider.Requirements(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, reqs)

	// Session without any loaded-skill markers -> no requirements.
	sess := sessionpkg.NewSession("app", "user", "sid")
	reqs, err = provider.Requirements(ctx, &agent.Invocation{
		AgentName: "agent-a",
		Session:   sess,
	})
	require.NoError(t, err)
	require.Empty(t, reqs)

	// With a loaded-skill marker the provider emits one requirement
	// per loaded skill, keyed by "skill:<name>".
	sess.SetState(
		rootskill.LoadedKey("agent-a", "echoer"), []byte{1},
	)
	reqs, err = provider.Requirements(ctx, &agent.Invocation{
		AgentName: "agent-a",
		Session:   sess,
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	require.Equal(t, "skill:echoer", reqs[0].Key())
	require.Equal(t, KindSkill, reqs[0].Kind())
}

func TestConversationFilesProvider_Name(t *testing.T) {
	p := NewConversationFilesProvider()
	require.Equal(t, "conversation_files", p.Name())
}

// TestConversationFilesRequirement_Metadata locks in the stable tool
// metadata exposed to the reconciler. These values are observed by
// the dedup/phase pipeline and by warning messages, so changes here
// should be deliberate.
func TestConversationFilesRequirement_Metadata(t *testing.T) {
	r := &conversationFilesRequirement{}
	require.Equal(t, "conversation-files", r.Key())
	require.Equal(t, KindConversationFile, r.Kind())
	require.Equal(t, PhaseFile, r.Phase())
	require.False(t, r.Required(),
		"conversation files must stay optional so a partial stage "+
			"degrades to a warning instead of aborting the turn")
	require.Equal(t,
		codeexecutor.DirWork+"/inputs",
		r.Target(),
	)
}

// TestConversationFilesRequirement_FingerprintReflectsFiles covers the
// two properties the reconciler relies on: Fingerprint is stable
// across calls for the same inputs, and changing the file set
// changes the digest so a second reconcile actually re-stages.
func TestConversationFilesRequirement_FingerprintReflectsFiles(t *testing.T) {
	ctx := context.Background()
	r := &conversationFilesRequirement{}

	// No invocation -> empty fingerprint, but no error.
	fp, err := r.Fingerprint(ctx, ApplyContext{})
	require.NoError(t, err)
	require.Empty(t, fp)

	inv := &agent.Invocation{
		Message: model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{FileID: "upload-1"},
			}},
		},
	}
	fp1, err := r.Fingerprint(ctx, ApplyContext{Invocation: inv})
	require.NoError(t, err)
	require.NotEmpty(t, fp1)

	// Adding a second file part changes the digest.
	inv.Message.ContentParts = append(inv.Message.ContentParts,
		model.ContentPart{
			Type: model.ContentTypeFile,
			File: &model.File{FileID: "upload-2"},
		},
	)
	fp2, err := r.Fingerprint(ctx, ApplyContext{Invocation: inv})
	require.NoError(t, err)
	require.NotEqual(t, fp1, fp2)
}

// TestConversationFilesRequirement_SentinelIsTrue documents that the
// sentinel is intentionally a constant-true: StageConversationFiles
// self-verifies through workspace metadata, so the reconciler relies
// on the fingerprint alone for skip decisions.
func TestConversationFilesRequirement_SentinelIsTrue(t *testing.T) {
	r := &conversationFilesRequirement{}
	ok, err := r.SentinelExists(context.Background(), ApplyContext{})
	require.NoError(t, err)
	require.True(t, ok)
}

// TestConversationFilesRequirement_ApplyStagesInlineBytes exercises
// the Apply path end-to-end against a live local-runtime engine. This
// guards the contract that message-attached inline bytes land under
// work/inputs/<name> when no session is present, which is the
// scenario that regressed when inv.Session==nil was gated.
func TestConversationFilesRequirement_ApplyStagesInlineBytes(t *testing.T) {
	eng, ws := newTestEngine(t)

	inv := &agent.Invocation{
		Message: model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{
					Name: "note.txt",
					Data: []byte("hello"),
				},
			}},
		},
	}
	// StageConversationFiles reads the invocation from the context,
	// so the requirement's Apply must be invoked with a ctx that has
	// the invocation attached. That mirrors how the reconciler drives
	// it in production via agent.NewInvocationContext.
	ctx := agent.NewInvocationContext(context.Background(), inv)

	r := &conversationFilesRequirement{}
	require.NoError(t, r.Apply(ctx, ApplyContext{
		Engine:     eng,
		Workspace:  ws,
		Invocation: inv,
	}))

	got, err := os.ReadFile(filepath.Join(
		ws.Path, "work", "inputs", "note.txt",
	))
	require.NoError(t, err)
	require.Equal(t, "hello", string(got))
}

// TestFileDigest_ReturnsStableKeys pins the digest format the
// provider's fingerprint depends on: explicit IDs win over bytes,
// empty inputs produce an empty key (filtered out by the caller),
// and inline bytes fall back to a "sha:" prefix.
func TestFileDigest_ReturnsStableKeys(t *testing.T) {
	require.Equal(t, "id:abc", fileDigest("abc", nil))
	require.Equal(t, "id:abc", fileDigest("  abc  ", []byte("ignored")))
	require.Equal(t, "", fileDigest("", nil))
	require.Equal(t, "", fileDigest("   ", nil))
	d := fileDigest("", []byte("hello"))
	require.NotEmpty(t, d)
	require.Contains(t, d, "sha:")
	// Same bytes must produce the same digest.
	require.Equal(t, d, fileDigest("", []byte("hello")))
}
