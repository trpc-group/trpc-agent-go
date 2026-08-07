//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContainerRuntimeIntegration(t *testing.T) {
	if os.Getenv("CODE_REVIEW_AGENT_DOCKER") != "1" {
		t.Skip("set CODE_REVIEW_AGENT_DOCKER=1 to run Docker integration")
	}
	rt, err := NewContainerRuntime("../..")
	if err != nil {
		t.Fatalf("Docker container runtime unavailable: %v", err)
	}
	defer rt.Close()
	repo := t.TempDir()
	const nonce = "CODE_REVIEW_CONTAINER_NONCE_2004"
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/nonce\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testSource := "package nonce\n\nimport \"testing\"\n\nfunc TestNonce(t *testing.T) { t.Fatal(\"" + nonce + "\") }\n"
	if err := os.WriteFile(filepath.Join(repo, "nonce_test.go"), []byte(testSource), 0o600); err != nil {
		t.Fatal(err)
	}
	skillPath, err := filepath.Abs("../../skills/code-review")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := rt.Stage(ctx, Snapshot{Path: repo, SkillPath: skillPath}); err != nil {
		t.Fatalf("stage: %v", err)
	}
	defer rt.Cleanup(ctx)
	result, err := rt.Run(ctx, Command{ID: "go-test", Args: []string{"test", "."}, Timeout: 60 * time.Second, MaxStdoutBytes: 1 << 20, MaxStderrBytes: 1 << 20})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ExitCode == 0 || !strings.Contains(result.Stdout+result.Stderr, nonce) {
		t.Fatalf("bundled script did not execute nonce test: %#v", result)
	}
	staticcheck, err := rt.Run(ctx, Command{ID: "staticcheck", Args: []string{"staticcheck", "."}, Timeout: 60 * time.Second, MaxStdoutBytes: 1 << 20, MaxStderrBytes: 1 << 20})
	if err != nil {
		t.Fatalf("staticcheck run: %v", err)
	}
	if staticcheck.Outcome != OutcomeDependencyUnavailable {
		t.Fatalf("staticcheck outcome = %q, want %q", staticcheck.Outcome, OutcomeDependencyUnavailable)
	}
}
