//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmreview

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestReviewAgentKnowledgeOnlyTools(t *testing.T) {
	repo, err := skill.NewFSRepository(sandbox.ResolveSkillsRoot("skills"))
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}

	agt := llmagent.New(
		agentName,
		llmagent.WithSkills(repo),
		llmagent.WithSkillToolProfile(llmagent.SkillToolProfileKnowledgeOnly),
	)

	names := make(map[string]bool)
	for _, tl := range agt.Tools() {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}
	if !names["skill_load"] {
		t.Fatal("expected skill_load tool")
	}
	if names["workspace_exec"] {
		t.Fatal("workspace_exec must not be exposed without CodeExecutor")
	}
	if names["skill_run"] || names["skill_exec"] {
		t.Fatalf("execution tools exposed: %+v", names)
	}
}
