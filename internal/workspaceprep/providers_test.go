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

func TestConversationFilesProvider_NoSessionIsNoop(t *testing.T) {
	ctx := context.Background()
	provider := NewConversationFilesProvider()
	reqs, err := provider.Requirements(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, reqs)
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
