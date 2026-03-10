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

import "testing"

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"":                   Full,
		"full":               Full,
		" FULL ":             Full,
		"knowledge_only":     KnowledgeOnly,
		" KNOWLEDGE_ONLY \n": KnowledgeOnly,
		"unknown":            Full,
	}
	for in, want := range tests {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveFlags(t *testing.T) {
	full := ResolveFlags(Full)
	if !full.Load || !full.SelectDocs || !full.ListDocs || !full.Run ||
		!full.Exec || !full.WriteStdin || !full.PollSession ||
		!full.KillSession {
		t.Fatalf("ResolveFlags(full) = %+v, want all tools enabled", full)
	}
	if !full.RequiresExecutionTools() || !full.RequiresExecSessionTools() {
		t.Fatalf("ResolveFlags(full) methods = exec:%v session:%v, want both true",
			full.RequiresExecutionTools(), full.RequiresExecSessionTools())
	}

	knowledgeOnly := ResolveFlags(KnowledgeOnly)
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
