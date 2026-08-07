// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

func TestSensitiveLeakChecker_APIKey(t *testing.T) {
	c := NewSensitiveLeakChecker(nil)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "export API_KEY='sk-abc123def456'",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for API key in command")
	}
}

func TestSensitiveLeakChecker_PrivateKey(t *testing.T) {
	c := NewSensitiveLeakChecker(nil)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "cat private.key\n-----BEGIN RSA PRIVATE KEY-----\nabc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for private key in command")
	}
	if findings[0].RuleID != toolsafety.RuleSensitiveLeak {
		t.Errorf("expected SENSITIVE_LEAK, got %s", findings[0].RuleID)
	}
}

func TestSensitiveLeakChecker_GitHubToken(t *testing.T) {
	c := NewSensitiveLeakChecker(nil)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		Backend:  "workspaceexec",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Error("expected finding for GitHub token pattern")
	}
}

func TestSensitiveLeakChecker_SafeCommand(t *testing.T) {
	c := NewSensitiveLeakChecker(nil)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "echo hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for safe command, got %+v", findings)
	}
}

func TestSensitiveLeakChecker_Sanitize(t *testing.T) {
	c := NewSensitiveLeakChecker(nil)

	output := "API_KEY = 'sk-abc123def456xyz'"
	sanitized := c.SanitizeOutput(output)
	if sanitized == output {
		t.Errorf("expected API key to be redacted, got: %s", sanitized)
	}
	if sanitized != "***REDACTED***" {
		t.Errorf("unexpected sanitized output: %s", sanitized)
	}
}
