//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFakeRunAndShowTaskReloadsSQLiteAudit(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "audit.db")

	run := exec.Command("go", "run", ".", "--fixture", "clean", "--runtime", "fake", "--task-id", "cli-task", "--out", dir, "--db", db)
	runOut, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("go run review: %v\n%s", err, runOut)
	}
	if !strings.Contains(string(runOut), "task_id=cli-task status=completed") {
		t.Fatalf("unexpected run output: %s", runOut)
	}

	show := exec.Command("go", "run", ".", "--show-task", "cli-task", "--out", dir, "--db", db)
	showOut, err := show.CombinedOutput()
	if err != nil {
		t.Fatalf("go run show-task: %v\n%s", err, showOut)
	}
	if !strings.Contains(string(showOut), "task_id=cli-task status=completed") {
		t.Fatalf("unexpected show-task output: %s", showOut)
	}
}
