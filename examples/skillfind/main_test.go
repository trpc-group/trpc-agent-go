//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestBuildAgentInstruction(t *testing.T) {
	disabled := buildAgentInstruction(false)
	if !strings.Contains(disabled, "Do not call skill_run") {
		t.Fatalf("disabled instruction = %q", disabled)
	}

	enabled := buildAgentInstruction(true)
	if !strings.Contains(enabled, "user explicitly asks") {
		t.Fatalf("enabled instruction = %q", enabled)
	}
}

func TestSkillToolProfile(t *testing.T) {
	if got := skillToolProfile(false); got !=
		llmagent.SkillToolProfileKnowledgeOnly {
		t.Fatalf("disabled profile = %q", got)
	}
	if got := skillToolProfile(true); got !=
		llmagent.SkillToolProfileFull {
		t.Fatalf("enabled profile = %q", got)
	}
}

func TestResetUserSkillRoot(t *testing.T) {
	userRoot := t.TempDir()
	writeTestSkill(t, userRoot, "hello")

	repo, err := skill.NewFSRepository(userRoot)
	if err != nil {
		t.Fatalf("NewFSRepository() error = %v", err)
	}
	if _, err := repo.Get("hello"); err != nil {
		t.Fatalf("repo.Get() before reset error = %v", err)
	}

	chat := &skillFindChat{
		userSkillsDir: userRoot,
		sessionID:     "session-1",
		repo:          repo,
	}
	if err := chat.resetUserSkillRoot(); err != nil {
		t.Fatalf("resetUserSkillRoot() error = %v", err)
	}
	if chat.sessionID == "session-1" {
		t.Fatal("expected reset to create a new session id")
	}
	if _, err := os.Stat(userRoot); err != nil {
		t.Fatalf("Stat(%q) error = %v", userRoot, err)
	}
	if _, err := repo.Get("hello"); err == nil {
		t.Fatal("expected repo.Get() to fail after reset")
	}
}

func writeTestSkill(t *testing.T, root string, skillName string) {
	t.Helper()

	skillDir := filepath.Join(root, skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	content := "---\nname: " + skillName +
		"\ndescription: test skill\n---\nbody\n"
	if err := os.WriteFile(
		filepath.Join(skillDir, skillFileName),
		[]byte(content),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
