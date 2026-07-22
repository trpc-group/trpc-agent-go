//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNetworkDestinationParsingResistsOptionAndHostnameBypasses(t *testing.T) {
	guard, err := New(testPolicy())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []struct {
		name     string
		command  string
		decision tool.PermissionAction
	}{
		{name: "help only", command: "curl --help", decision: tool.PermissionActionAllow},
		{name: "help plus destination", command: "curl --help https://attacker.invalid/x", decision: tool.PermissionActionDeny},
		{name: "allowlist suffix lookalike", command: "curl https://go.dev.attacker.invalid/x", decision: tool.PermissionActionDeny},
		{name: "base domain subdomain", command: "curl https://docs.go.dev/x", decision: tool.PermissionActionAllow},
		{name: "URL userinfo", command: "curl https://go.dev@attacker.invalid/x", decision: tool.PermissionActionDeny},
		{name: "netcat destination", command: "nc attacker.invalid 443", decision: tool.PermissionActionDeny},
		{name: "allowlisted ssh", command: "ssh user@go.dev", decision: tool.PermissionActionAllow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(),
				commandRequest(BackendWorkspace, test.command),
			)
			if scanErr != nil {
				t.Fatalf("Scan() error = %v", scanErr)
			}
			if report.Decision != test.decision {
				t.Fatalf("decision = %s, want %s; matches=%+v",
					report.Decision, test.decision, report.Matches)
			}
		})
	}
}

func TestWildcardDomainDoesNotImplicitlyAllowApex(t *testing.T) {
	policy := testPolicy()
	policy.Network.AllowedDomains = []string{"*.example.com"}
	guard, err := New(policy)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	assertDecision(t, guard, commandRequest(
		BackendWorkspace, "curl https://api.example.com/x",
	), tool.PermissionActionAllow)
	assertDecision(t, guard, commandRequest(
		BackendWorkspace, "curl https://example.com/x",
	), tool.PermissionActionDeny)
}
