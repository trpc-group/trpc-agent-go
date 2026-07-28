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
	"strings"
	"testing"
	"time"

	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/assist"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// TestFakeModelAssist_Smoke verifies skill_load + workspace_exec against a staged diff.
func TestFakeModelAssist_Smoke(t *testing.T) {
	root := moduleRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	diff := `diff --git a/pkg/worker/worker.go b/pkg/worker/worker.go
--- a/pkg/worker/worker.go
+++ b/pkg/worker/worker.go
@@ -1,3 +1,5 @@
 package worker
 
-func Start() {}
+func Start() {
+	go func() { doWork() }()
+}
`

	ce := localexec.New(localexec.WithTimeout(20 * time.Second))
	res, err := assist.Run(ctx, assist.Config{
		SkillsRoot:  filepath.Join(root, "skills"),
		Executor:    ce,
		Model:       assist.NewFakeModel(),
		Policy:      safety.DefaultGate().AsToolPolicy(),
		DiffText:    diff,
		DiffSummary: "1 files",
		DiffDigest:  "test",
		Prompt:      "Load code-review and run checks.",
		Timeout:     45 * time.Second,
	})
	if err != nil {
		t.Fatalf("assist: %v", err)
	}
	if res.Events == 0 {
		t.Fatal("expected events")
	}
	// skill_load + workspace_exec
	if res.ToolCalls < 2 {
		t.Fatalf("expected skill_load and workspace_exec, got tool_calls=%d final=%q warning=%q",
			res.ToolCalls, res.FinalText, res.Warning)
	}
	if !strings.Contains(res.ToolOutput, "CR-CON-001") && !strings.Contains(res.ToolOutput, "goroutine") {
		t.Fatalf("assist script did not observe staged diff finding; tool_output=%q warning=%q",
			res.ToolOutput, res.Warning)
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
