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

func TestNetworkEgressChecker_Unauthorized(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewNetworkEgressChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "curl http://evil.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for unauthorized domain")
	}
	if findings[0].RuleID != toolsafety.RuleNetworkUnauthorized {
		t.Errorf("expected NETWORK_UNAUTHORIZED, got %s", findings[0].RuleID)
	}
}

func TestNetworkEgressChecker_AuthorizedDomain(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	policy.NetworkPolicy.AllowedDomains = []string{"api.github.com"}
	c := NewNetworkEgressChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "curl https://api.github.com/repos",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for authorized domain, got %+v", findings)
	}
}

func TestNetworkEgressChecker_NonNetworkCommand(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewNetworkEgressChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "ls -la",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for non-network command, got %+v", findings)
	}
}

func TestNetworkEgressChecker_WildcardDomain(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	policy.NetworkPolicy.AllowedDomains = []string{"*.github.com"}
	c := NewNetworkEgressChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "wget https://api.github.com/releases",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for wildcard domain, got %+v", findings)
	}
}

func TestNetworkEgressChecker_PrivateIP(t *testing.T) {
	policy := toolsafety.DefaultPolicy()
	c := NewNetworkEgressChecker(policy)

	findings, err := c.Check(context.Background(), &toolsafety.ScanRequest{
		ToolName: "test",
		Command:  "curl http://127.0.0.1:8080",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no findings for loopback IP, got %+v", findings)
	}
}
