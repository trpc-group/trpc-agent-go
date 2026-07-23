//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package assist_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/assist"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

func TestFakeModelAssist_Smoke(t *testing.T) {
	root := moduleRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ce := localexec.New(localexec.WithTimeout(20 * time.Second))
	res, err := assist.Run(ctx, assist.Config{
		SkillsRoot: filepath.Join(root, "skills"),
		Executor:   ce,
		Model:      assist.NewFakeModel(),
		Policy:     safety.DefaultGate().AsToolPolicy(),
		Prompt:     "Load code-review and run checks.",
		Timeout:    45 * time.Second,
	})
	if err != nil {
		t.Fatalf("assist: %v", err)
	}
	if res.Events == 0 {
		t.Fatal("expected events")
	}
	// Fake model should attempt at least skill_load.
	if res.ToolCalls < 1 {
		t.Fatalf("expected tool calls, got %d (final=%q)", res.ToolCalls, res.FinalText)
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) == "assist" {
		return filepath.Dir(wd)
	}
	return wd
}
