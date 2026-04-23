//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skillprofile

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"":                   KnowledgeOnly,
		"full":               Full,
		" FULL ":             Full,
		"knowledge_only":     KnowledgeOnly,
		" KNOWLEDGE_ONLY \n": KnowledgeOnly,
		"unknown":            KnowledgeOnly,
	}
	for in, want := range tests {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveFlags(t *testing.T) {
	full, err := ResolveFlags(Full, nil)
	require.NoError(t, err)
	if !full.Load || !full.SelectDocs || !full.ListDocs || !full.Run ||
		!full.Exec || !full.WriteStdin || !full.PollSession ||
		!full.KillSession {
		t.Fatalf("ResolveFlags(full) = %+v, want all tools enabled", full)
	}
	if !full.RequiresExecutionTools() || !full.RequiresExecSessionTools() {
		t.Fatalf("ResolveFlags(full) methods = exec:%v session:%v, want both true",
			full.RequiresExecutionTools(), full.RequiresExecSessionTools())
	}

	knowledgeOnly, err := ResolveFlags(KnowledgeOnly, nil)
	require.NoError(t, err)
	if !knowledgeOnly.Load || !knowledgeOnly.SelectDocs || !knowledgeOnly.ListDocs {
		t.Fatalf("ResolveFlags(knowledge_only) = %+v, want knowledge tools enabled", knowledgeOnly)
	}
	if knowledgeOnly.Run || knowledgeOnly.Exec || knowledgeOnly.WriteStdin ||
		knowledgeOnly.PollSession || knowledgeOnly.KillSession {
		t.Fatalf("ResolveFlags(knowledge_only) = %+v, want execution tools disabled", knowledgeOnly)
	}
	if knowledgeOnly.RequiresExecutionTools() || knowledgeOnly.RequiresExecSessionTools() {
		t.Fatalf("ResolveFlags(knowledge_only) methods = exec:%v session:%v, want both false",
			knowledgeOnly.RequiresExecutionTools(),
			knowledgeOnly.RequiresExecSessionTools())
	}
}

func TestResolveFlags_WithAllowedTools(t *testing.T) {
	flags, err := ResolveFlags(
		Full,
		[]string{ToolLoad, ToolRun},
	)
	require.NoError(t, err)
	require.Equal(t, Flags{
		Load: true,
		Run:  true,
	}, flags)

	loadOnly, err := ResolveFlags(
		Full,
		[]string{" skill_load "},
	)
	require.NoError(t, err)
	require.Equal(t, Flags{Load: true}, loadOnly)

	listOnly, err := ResolveFlags(
		Full,
		[]string{ToolListDocs},
	)
	require.NoError(t, err)
	require.Equal(t, Flags{ListDocs: true}, listOnly)

	selectOnly, err := ResolveFlags(
		Full,
		[]string{ToolSelectDocs},
	)
	require.NoError(t, err)
	require.Equal(t, Flags{SelectDocs: true}, selectOnly)

	interactive, err := ResolveFlags(
		Full,
		[]string{ToolRun, ToolExec, ToolPollSession, ToolKillSession},
	)
	require.NoError(t, err)
	require.Equal(t, Flags{
		Run:         true,
		Exec:        true,
		PollSession: true,
		KillSession: true,
	}, interactive)
}

func TestResolveFlags_WithAllowedTools_Invalid(t *testing.T) {
	tests := []struct {
		name         string
		allowedTools []string
		wantErr      string
	}{
		{
			name:         "unknown tool",
			allowedTools: []string{"skill_unknown"},
			wantErr:      `unknown skill tool "skill_unknown"`,
		},
		{
			name:         "exec requires run",
			allowedTools: []string{ToolLoad, ToolExec},
			wantErr:      ToolExec + " requires " + ToolRun,
		},
		{
			name:         "stdin requires exec",
			allowedTools: []string{ToolLoad, ToolRun, ToolWriteStdin},
			wantErr:      ToolWriteStdin + " requires " + ToolExec,
		},
		{
			name:         "poll requires exec",
			allowedTools: []string{ToolLoad, ToolRun, ToolPollSession},
			wantErr:      ToolPollSession + " requires " + ToolExec,
		},
		{
			name:         "kill requires exec",
			allowedTools: []string{ToolLoad, ToolRun, ToolKillSession},
			wantErr:      ToolKillSession + " requires " + ToolExec,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveFlags(Full, tt.allowedTools)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestFlagsHelpers(t *testing.T) {
	flags := Flags{
		Load:        true,
		ListDocs:    true,
		Run:         true,
		Exec:        true,
		WriteStdin:  true,
		PollSession: true,
		KillSession: true,
	}
	require.True(t, flags.Any())
	require.True(t, flags.HasKnowledgeTools())
	require.True(t, flags.HasDocHelpers())

	nonInteractive := flags.WithoutInteractiveExecution()
	require.Equal(t, Flags{
		Load:     true,
		ListDocs: true,
		Run:      true,
	}, nonInteractive)
}

func TestIsKnowledgeOnly(t *testing.T) {
	require.True(t, IsKnowledgeOnly(" KNOWLEDGE_ONLY "))
	require.False(t, IsKnowledgeOnly("full"))
	// Unknown profiles fall back to the KnowledgeOnly default, matching
	// the framework-level behavior that leaves execution tools off
	// unless they are explicitly opted in via Full.
	require.True(t, IsKnowledgeOnly("unknown"))
	require.True(t, IsKnowledgeOnly(""))
}

// TestIsExplicitKnowledgeOnly locks down the distinction that
// llmagent's auto-fallback relies on: an empty profile still
// normalizes to knowledge-only but counts as "unconfigured", while a
// literal "knowledge_only" (in any casing) is an explicit opt-out of
// the convenience fallbacks.
func TestIsExplicitKnowledgeOnly(t *testing.T) {
	require.True(t, IsExplicitKnowledgeOnly("knowledge_only"))
	require.True(t, IsExplicitKnowledgeOnly(" KNOWLEDGE_ONLY "))
	require.False(t, IsExplicitKnowledgeOnly(""))
	require.False(t, IsExplicitKnowledgeOnly("   "))
	require.False(t, IsExplicitKnowledgeOnly("full"))
	require.False(t, IsExplicitKnowledgeOnly("unknown"))
}
