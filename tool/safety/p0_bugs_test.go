//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file contains red tests that reproduce the P0 issues called out
// in the code review.  Each test must FAIL on the current implementation
// and PASS after the corresponding fix.  They double as regression
// tests once the bugs are fixed.
//
// P0-3 (Blocked field redundant with Decision) is a design issue rather
// than a behavioral bug, so it is not covered here; a behavioural test
// for it would be tautological.  Resolved by the Decision→Verdict
// rename and removal of the Blocked field.

func countRisksByRuleID(risks []Risk, ruleID string) int {
	n := 0
	for _, r := range risks {
		if r.RuleID == ruleID {
			n++
		}
	}
	return n
}

// ----------------------------------------------------------------------
// P0-1: policy.DefaultVerdict must be honored when no rule fires.
//
// Currently scanner.computeVerdict() always returns VerdictAllow when
// no rule fires, ignoring policy.DefaultVerdict.  A user who sets
// default_verdict: ask expects "no rule fired" to be sent for human
// review, but gets allow.  The test below exercises both the ask and
// deny variants of this bug.
// ----------------------------------------------------------------------

func TestP0_DefaultVerdictAsk_HonoredForSafeCommand(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictAsk
	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	// "echo hello" is safe: shellsafe accepts echo (allowed), no rule
	// should fire.  The policy asks for human review by default, so
	// the final decision must be ask, not allow.
	report := scan(t, s, "echo hello", BackendWorkspaceExec)

	if report.Verdict != VerdictAsk {
		t.Errorf("default_verdict=ask: got %s want %s; risks=%+v",
			report.Verdict, VerdictAsk, report.Risks)
	}
}

func TestP0_DefaultVerdictDeny_HonoredForSafeCommand(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictDeny
	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "echo hello", BackendWorkspaceExec)

	if report.Verdict != VerdictDeny {
		t.Errorf("default_verdict=deny: got %s want %s; risks=%+v",
			report.Verdict, VerdictDeny, report.Risks)
	}
}

// ----------------------------------------------------------------------
// P0-2: each RuleID must appear at most once in a report.
//
// When shellsafe rejects a command via the user-configured deny list,
// scanner.go manually appends a Risk with RuleID="dangerous_command".
// The dangerousCommandRule then ALSO fires (its Check has no awareness
// of the prior rejection) and appends a second Risk with the same
// RuleID.  This breaks the audit invariant that RuleID is unique and
// produces duplicate OTel span attributes.
// ----------------------------------------------------------------------

func TestP0_NoDuplicateDangerousCommandRisk(t *testing.T) {
	s := newTestScanner(t)

	// "rm -rf /" hits both the shellsafe deny list and the
	// dangerousCommandRule's rm -rf pattern.
	report := scan(t, s, "rm -rf /", BackendWorkspaceExec)

	n := countRisksByRuleID(report.Risks, "dangerous_command")
	if n != 1 {
		t.Errorf("expected exactly 1 dangerous_command risk, got %d: %+v",
			n, report.Risks)
	}
}

// ----------------------------------------------------------------------
// P0-4: DependencyPolicy.DeniedPackages must be enforced.
//
// dependencyRule.Check currently only inspects AllowedManagers and
// emits a medium risk for any install from an allowed manager.  The
// DeniedPackages field is loaded from YAML and stored on the struct
// but never read.  A user listing a package in denied_packages
// expects the install to be denied, not merely reviewed.
// ----------------------------------------------------------------------

func TestP0_DeniedPackage_BlocksInstall(t *testing.T) {
	policy := DefaultPolicy()
	// Make shellsafe accept "pip" so the command reaches the rules.
	policy.Commands.Allowed = []string{"pip"}
	policy.DependencyPolicy.AllowedManagers = []string{"pip"}
	policy.DependencyPolicy.DeniedPackages = []string{"malicious-pkg"}

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "pip install malicious-pkg", BackendWorkspaceExec)

	if report.Verdict != VerdictDeny {
		t.Errorf("denied package install must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
	if report.RiskLevel != RiskHigh && report.RiskLevel != RiskCritical {
		t.Errorf("denied package install must escalate to high/critical: got %s",
			report.RiskLevel)
	}
}

// ----------------------------------------------------------------------
// P0-4: ResourceLimits.AllowedSleepSeconds must be enforced.
//
// resourceAbuseRule stores maxSleep in its struct but never reads it
// in Check().  A user setting allowed_sleep_seconds: 5 expects
// "sleep 100" to be denied, but the rule never fires and the command
// slips through when Commands.Allowed is empty (so shellsafe's allow
// list check is inactive).
// ----------------------------------------------------------------------

func TestP0_AllowedSleepSeconds_BlocksLongSleep(t *testing.T) {
	policy := DefaultPolicy()
	// Disable shellsafe's allow/deny list so the command reaches the
	// rules.  This isolates the resourceAbuseRule behaviour.
	policy.Commands.Allowed = nil
	policy.Commands.Denied = nil
	policy.ResourceLimits.AllowedSleepSeconds = 5

	s, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}

	report := scan(t, s, "sleep 100", BackendWorkspaceExec)

	if report.Verdict != VerdictDeny {
		t.Errorf("sleep 100 with allowed_sleep_seconds=5 must be denied: got %s; risks=%+v",
			report.Verdict, report.Risks)
	}
}

// ----------------------------------------------------------------------
// P0-5: AuditLogger must use the policy's SensitivePatterns, not the
// defaults.
//
// NewAuditLogger currently hardcodes
//     NewRedactor(DefaultPolicy().SensitivePatterns)
// so a user who customises sensitive_patterns in YAML gets no
// redaction for those patterns in the audit log.  This is a
// data-leak path: secrets matching the user's own patterns can be
// persisted to disk in cleartext.
// ----------------------------------------------------------------------

func TestP0_CustomSensitivePatterns_RedactedInAuditLog(t *testing.T) {
	tmpDir := t.TempDir()
	policyPath := filepath.Join(tmpDir, "policy.yaml")
	auditPath := filepath.Join(tmpDir, "audit.jsonl")

	const customSecret = "PROJECT_SPECIFIC_TOKEN_abc123def456"
	yamlData := `version: "1.0"
default_verdict: allow
commands:
  allowed: [echo]
sensitive_patterns:
  - "PROJECT_SPECIFIC_TOKEN_[a-z0-9]+"
`
	if err := os.WriteFile(policyPath, []byte(yamlData), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	pp, err := NewPermissionPolicy(policyPath, auditPath)
	if err != nil {
		t.Fatalf("NewPermissionPolicy: %v", err)
	}
	defer pp.auditLogger.Close() //nolint:errcheck

	// Drive the audit path directly: the bug is in the redactor
	// selection, not in CheckToolPermission, so we bypass the
	// framework request shape and call Log with a report that
	// contains the custom secret.
	report, err := pp.scanner.ScanCommand(context.Background(), &ScanRequest{
		ToolName: "echo",
		Command:  "echo " + customSecret,
		Backend:  BackendWorkspaceExec,
	})
	if err != nil {
		t.Fatalf("ScanCommand: %v", err)
	}

	if err := pp.auditLogger.Log(context.Background(), report, 0); err != nil {
		t.Fatalf("Log: %v", err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), customSecret) {
		t.Errorf("audit log leaked custom secret %q; log content: %s",
			customSecret, data)
	}
}
