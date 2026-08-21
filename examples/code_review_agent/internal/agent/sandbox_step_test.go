//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunGoSandboxChecksReturnsUnsupportedAuditForNonVendoredContainerModuleRepo(t *testing.T) {
	t.Parallel()

	timeout := 10 * time.Second
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/plain\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	ag := &Agent{cfg: Config{
		Runtime:          RuntimeContainer,
		Timeout:          timeout,
		OutputLimitBytes: 4096,
	}}

	decisions, runs := ag.runGoSandboxChecks(context.Background(), "task-non-vendored", repo)
	if len(decisions) != 1 || decisions[0].Action != "unsupported" {
		t.Fatalf("expected one unsupported decision, got %+v", decisions)
	}
	if !strings.Contains(decisions[0].Reason, "vendor/modules.txt") {
		t.Fatalf("decision reason = %q, want vendoring guidance", decisions[0].Reason)
	}
	if len(runs) != 1 {
		t.Fatalf("expected one unsupported run, got %+v", runs)
	}
	run := runs[0]
	if run.Runtime != RuntimeContainer || run.Status != "unsupported" || run.ExecutionStarted {
		t.Fatalf("unexpected unsupported run audit: %+v", run)
	}
	if run.TimeoutMS != timeout.Milliseconds() || run.OutputLimitBytes != 4096 {
		t.Fatalf("unsupported run should retain configured bounds, got %+v", run)
	}
	if !strings.Contains(run.Output, "network access disabled") {
		t.Fatalf("run output = %q, want network isolation guidance", run.Output)
	}
}
