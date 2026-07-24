// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import "testing"

func TestHostexecLifecycleRisksAreBackendGated(t *testing.T) {
	trueValue := true
	yield := 200
	cases := []struct {
		name       string
		backend    string
		command    string
		hostexec   *HostExecRequest
		parse      ShellParsePolicy
		wantID     string
		wantAction Decision
	}{
		{
			name:       "background request",
			backend:    "hostexec",
			command:    "sleep 1",
			hostexec:   &HostExecRequest{Background: true},
			wantID:     "hostexec-background-session",
			wantAction: DecisionAsk,
		},
		{
			name:       "background shell operator",
			backend:    "hostexec",
			command:    "sleep 1 &",
			parse:      ShellParsePolicy{FailureDecision: DecisionAsk},
			wantID:     "hostexec-background-session",
			wantAction: DecisionAsk,
		},
		{
			name:       "privilege escalation",
			backend:    "hostexec",
			command:    "sudo id",
			wantID:     "hostexec-privilege-escalation",
			wantAction: DecisionDeny,
		},
		{
			name:       "pty request",
			backend:    "hostexec",
			command:    "echo ready",
			hostexec:   &HostExecRequest{TTY: &trueValue},
			wantID:     "hostexec-interactive-session",
			wantAction: DecisionAsk,
		},
		{
			name:       "pty alias request",
			backend:    "hostexec",
			command:    "echo ready",
			hostexec:   &HostExecRequest{PTY: &trueValue},
			wantID:     "hostexec-interactive-session",
			wantAction: DecisionAsk,
		},
		{
			name:       "yielded session",
			backend:    "hostexec",
			command:    "echo ready",
			hostexec:   &HostExecRequest{YieldTimeMS: &yield},
			wantID:     "hostexec-interactive-session",
			wantAction: DecisionAsk,
		},
		{
			name:       "interactive command",
			backend:    "hostexec",
			command:    "top",
			wantID:     "hostexec-interactive-command",
			wantAction: DecisionAsk,
		},
		{
			name:       "residue command",
			backend:    "hostexec",
			command:    "nohup worker",
			wantID:     "hostexec-process-residue",
			wantAction: DecisionAsk,
		},
		{
			name:       "foreground command is benign",
			backend:    "hostexec",
			command:    "echo ready",
			wantAction: DecisionAllow,
		},
		{
			name:       "argument text is not privilege escalation",
			backend:    "hostexec",
			command:    "echo sudo",
			wantAction: DecisionAllow,
		},
		{
			name:       "argument text is not residue command",
			backend:    "hostexec",
			command:    "echo nohup",
			wantAction: DecisionAllow,
		},
		{
			name:       "workspaceexec is not hostexec",
			backend:    "workspaceexec",
			command:    "sudo id",
			hostexec:   &HostExecRequest{Background: true, TTY: &trueValue},
			wantAction: DecisionAllow,
		},
		{
			name:       "backend matching is exact",
			backend:    "HostExec",
			command:    "sudo id",
			wantAction: DecisionAllow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := AdaptShellCommand(tc.command, tc.parse)
			report := NewScanner(nil).Scan(ScanInput{
				Backend:      tc.backend,
				Command:      tc.command,
				ShellCommand: &view,
				HostExec:     tc.hostexec,
			})
			if report.Decision != tc.wantAction {
				t.Fatalf("decision = %q, want %q; report %#v", report.Decision, tc.wantAction, report)
			}
			if tc.wantID != "" && !hasEvidence(report.Evidences, tc.wantID) {
				t.Fatalf("missing %q in %#v", tc.wantID, report.Evidences)
			}
			if tc.wantID == "" {
				for _, evidence := range report.Evidences {
					if len(evidence.RuleID) >= len("hostexec-") && evidence.RuleID[:len("hostexec-")] == "hostexec-" {
						t.Fatalf("non-host backend matched hostexec rule: %#v", evidence)
					}
				}
			}
		})
	}
}
