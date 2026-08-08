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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGuard_CloseResetsSessionTracker verifies that Guard.Close drops the
// session tracking state so the tracker does not grow without bound over
// the guard's lifetime.
func TestGuard_CloseResetsSessionTracker(t *testing.T) {
	g, err := NewGuard(WithPolicy(covercoreNoAuditPolicy()))
	require.NoError(t, err)

	g.sessions.register("sess-1")
	g.sessions.kill("sess-2")
	require.True(t, g.sessions.isKnown("sess-1"))
	require.True(t, g.sessions.isKilled("sess-2"))

	require.NoError(t, g.Close())
	require.False(t, g.sessions.isKnown("sess-1"))
	require.False(t, g.sessions.isKilled("sess-2"))
}

func TestSessionTracker_KilledSessionCannotBeReused(t *testing.T) {
	sessions := newSessionTracker()
	sessions.register("sess-1")
	sessions.kill("sess-1")
	require.False(t, sessions.isKnown("sess-1"))
	require.True(t, sessions.isKilled("sess-1"))

	findings := ruleSessionInputBoundary(ScanInput{
		ToolName:     "write_stdin",
		SessionID:    "sess-1",
		SessionInput: "echo hello",
	}, sessions)
	require.Contains(
		t,
		ruleIDSet(findings),
		"host.finalized_session_input",
	)
}

func TestSessionTracker_QuarantineDoesNotResurrectKilledSession(
	t *testing.T,
) {
	sessions := newSessionTracker()
	sessions.register("sess-1")
	sessions.kill("sess-1")
	sessions.quarantine("sess-1")
	require.False(t, sessions.isKnown("sess-1"))
	require.True(t, sessions.isKilled("sess-1"))
}

func TestSessionTracker_BoundsKilledTombstones(t *testing.T) {
	sessions := newSessionTracker()
	for i := 0; i < maxKilledSessions+10; i++ {
		sessions.kill(itoa(i))
	}
	require.LessOrEqual(t, len(sessions.killed), maxKilledSessions)

	// A newly registered session may safely reuse an expired or killed
	// identifier.
	sessions.register("100")
	require.True(t, sessions.isKnown("100"))
	require.False(t, sessions.isKilled("100"))

}

func TestSessionTracker_BoundsKnownSessions(t *testing.T) {
	sessions := newSessionTracker()
	for i := 0; i < maxKnownSessions+10; i++ {
		sessions.register(itoa(i))
	}
	require.LessOrEqual(t, len(sessions.known), maxKnownSessions)
	require.LessOrEqual(t, len(sessions.knownOrder),
		maxKnownSessions)
	require.LessOrEqual(t, len(sessions.inputGates),
		maxKnownSessions)
}

func TestSessionTracker_SerializesInput(t *testing.T) {
	sessions := newSessionTracker()
	sessions.register("session")

	release, err := sessions.acquireInput(context.Background(), "session")
	require.NoError(t, err)
	require.NotNil(t, release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = sessions.acquireInput(ctx, "session")
	require.ErrorIs(t, err, context.DeadlineExceeded)

	release()
	release, err = sessions.acquireInput(context.Background(), "session")
	require.NoError(t, err)
	require.NotNil(t, release)
	release()
}

func TestScanner_SubmitOnlyChecksSessionLifecycle(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()

	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "unknown",
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.Contains(t, ruleIDSet(report.Findings), "host.unknown_session")

	scanner.sessions.register("killed")
	scanner.sessions.kill("killed")
	report, err = scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "killed",
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.NotEqual(t, DecisionAllow, report.Decision)
	require.Contains(
		t,
		ruleIDSet(report.Findings),
		"host.finalized_session_input",
	)
}

func TestScanner_LimitsSessionInputWithoutTracker(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = nil

	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:     "write_stdin",
		Backend:      BackendHostExec,
		SessionID:    "external",
		SessionInput: strings.Repeat("x", maxSessionInputBuffer+1),
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(
		t,
		ruleIDSet(report.Findings),
		"host.session_input_too_large",
	)
}

func TestSessionTracker_PreservesWhitespaceChunks(t *testing.T) {
	sessions := newSessionTracker()
	sessions.register("session")
	sessions.commitInput("session", "   ", false)
	_, combined, found, withinLimit := sessions.previewInput(
		"session",
		"\t",
		false,
	)
	require.True(t, found)
	require.True(t, withinLimit)
	require.Equal(t, "   \t", combined)
}

func TestScanner_ScansTrackedSessionInput(t *testing.T) {
	policy := testPolicy(t)
	policy.Rules.HostExec.Action = DecisionAsk
	scanner := newTestScanner(t, policy)
	scanner.sessions = newSessionTracker()

	tests := []struct {
		name     string
		info     sessionInfo
		input    string
		decision Decision
		ruleID   string
	}{
		{
			name: "shell command denied",
			info: sessionInfo{
				Backend:   BackendHostExec,
				InputMode: sessionInputShell,
			},
			input:    "rm -rf /",
			decision: DecisionDeny,
			ruleID:   "command.dangerous_delete",
		},
		{
			name: "python command denied",
			info: sessionInfo{
				Backend:   BackendHostExec,
				InputMode: sessionInputCode,
				Language:  "python",
			},
			input:    `import os; os.system("rm -rf /")`,
			decision: DecisionDeny,
			ruleID:   "command.dangerous_delete",
		},
		{
			name: "data consumer remains data",
			info: sessionInfo{
				Backend:   BackendHostExec,
				InputMode: sessionInputData,
			},
			input:    "rm -rf /",
			decision: DecisionAllow,
		},
		{
			name: "unknown input mode asks",
			info: sessionInfo{
				Backend:   BackendHostExec,
				InputMode: sessionInputUnknown,
			},
			input:    "hello",
			decision: DecisionAsk,
			ruleID:   "host.unclassified_session_input",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scanner.sessions.registerWithInfo("session", tc.info)
			report, err := scanner.Scan(context.Background(), ScanInput{
				ToolName:     "write_stdin",
				Backend:      BackendHostExec,
				SessionID:    "session",
				SessionInput: tc.input,
			})
			require.NoError(t, err)
			require.Equal(t, tc.decision, report.Decision)
			if tc.ruleID != "" {
				require.Contains(t, ruleIDSet(report.Findings), tc.ruleID)
			}
		})
	}
}

func TestScanner_ScansInitialWorkspaceStdin(t *testing.T) {
	policy := testPolicy(t)
	policy.Rules.HostExec.Action = DecisionAsk
	policy.AllowedCommands = append(policy.AllowedCommands, "python")
	report, err := newTestScanner(t, policy).Scan(
		context.Background(),
		ScanInput{
			ToolName:     "workspace_exec",
			Backend:      BackendWorkspaceExec,
			Command:      "python",
			SessionInput: `import os; os.system("rm -rf /")`,
			Timeout:      time.Second,
		},
	)
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "command.dangerous_delete")

	policy.AllowedCommands = append(policy.AllowedCommands, "perl")
	report, err = newTestScanner(t, policy).Scan(
		context.Background(),
		ScanInput{
			ToolName:     "workspace_exec",
			Backend:      BackendWorkspaceExec,
			Command:      "perl",
			SessionInput: `system("rm -rf /")`,
			Timeout:      time.Second,
		},
	)
	require.NoError(t, err)
	require.Equal(t, DecisionAsk, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "host.unclassified_session_input")
}

func TestScanner_ScansCumulativeSessionInput(t *testing.T) {
	policy := testPolicy(t)
	scanner := newTestScanner(t, policy)
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("python", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputCode,
		Language:  "python",
	})

	first := ScanInput{
		ToolName:     "write_stdin",
		Backend:      BackendHostExec,
		SessionID:    "python",
		SessionInput: "os.sys",
	}
	report, err := scanner.Scan(context.Background(), first)
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)
	scanner.sessions.commitInput("python", first.SessionInput, false)

	report, err = scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "python",
		SessionInput:  `tem("rm -rf /")`,
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "command.dangerous_delete")
}

func TestScanner_ShellSessionAllowsConsecutiveSubmittedCommands(
	t *testing.T,
) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("shell", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputShell,
	})

	for _, command := range []string{"ls", "pwd"} {
		report, err := scanner.Scan(context.Background(), ScanInput{
			ToolName:      "write_stdin",
			Backend:       BackendHostExec,
			SessionID:     "shell",
			SessionInput:  command,
			sessionSubmit: true,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
		scanner.sessions.commitInput("shell", command, true)
		info, ok := scanner.sessions.lookup("shell")
		require.True(t, ok)
		require.Empty(t, info.Pending)
	}
}

func TestScanner_DeniesOversizedCumulativeSessionInput(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("shell", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputShell,
	})
	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:     "write_stdin",
		Backend:      BackendHostExec,
		SessionID:    "shell",
		SessionInput: strings.Repeat("x", maxSessionInputBuffer+1),
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "host.session_input_too_large")
	for _, finding := range report.Findings {
		if finding.RuleID == "host.session_input_too_large" {
			require.Contains(t, finding.Recommendation, "Restart the session")
			return
		}
	}
	t.Fatal("missing host.session_input_too_large finding")
}

func TestScanner_ShellSessionUsesShellsafeInputLimit(t *testing.T) {
	policy := testPolicy(t)
	policy.Rules.HostExec.Enabled = false
	policy.Rules.HostExec.Action = DecisionAllow
	scanner := newTestScanner(t, policy)
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("shell", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputShell,
	})
	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:     "write_stdin",
		Backend:      BackendHostExec,
		SessionID:    "shell",
		SessionInput: strings.Repeat("x", maxShellSessionInputBuffer+1),
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, map[string]bool{
		"host.session_input_too_large": true,
	}, ruleIDSet(report.Findings))
}

func TestRuleSessionInputBoundaryIgnoresHostPolicy(t *testing.T) {
	policy := testPolicy(t)
	policy.Rules.HostExec.Enabled = false
	policy.Rules.HostExec.Action = DecisionAllow
	findings := ruleSessionInputBoundary(
		ScanInput{
			Command:      "perl",
			SessionInput: "print 1",
		},
		nil,
	)
	require.Len(t, findings, 1)
	require.Equal(t, DecisionAsk, findings[0].Decision)

	scanner := newTestScanner(t, policy)
	scanner.sessions = newSessionTracker()
	report, err := scanner.Scan(
		context.Background(),
		ScanInput{
			ToolName:     "write_stdin",
			Backend:      BackendHostExec,
			SessionID:    "missing",
			SessionInput: "rm -rf /",
		},
	)
	require.NoError(t, err)
	require.Equal(t, DecisionAsk, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "host.unknown_session")
}

func TestScanner_SessionInputReportHasSummaryAndHash(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("shell", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputShell,
	})
	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:     "write_stdin",
		Backend:      BackendHostExec,
		SessionID:    "shell",
		SessionInput: "echo hello",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.CommandHash)
	require.Contains(t, report.Command, "stdin:echo hello")
}

func TestScanner_PreservesMultilineCodeSessionContext(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("python", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputCode,
		Language:  "python",
	})

	for _, chunk := range []string{
		"from requests import (",
		"request,",
		")",
	} {
		report, err := scanner.Scan(context.Background(), ScanInput{
			ToolName:      "write_stdin",
			Backend:       BackendHostExec,
			SessionID:     "python",
			SessionInput:  chunk,
			sessionSubmit: true,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
		scanner.sessions.commitInput("python", chunk, true)
	}

	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "python",
		SessionInput:  `request("GET", "https://evil.example")`,
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "network.non_whitelisted_domain")
}

func TestScanner_PreservesContinuedImportSessionContext(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("python", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputCode,
		Language:  "python",
	})

	for _, chunk := range []string{
		`from requests import request \`,
		"as send",
	} {
		report, err := scanner.Scan(context.Background(), ScanInput{
			ToolName:      "write_stdin",
			Backend:       BackendHostExec,
			SessionID:     "python",
			SessionInput:  chunk,
			sessionSubmit: true,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
		scanner.sessions.commitInput("python", chunk, true)
	}

	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "python",
		SessionInput:  `send("GET", "https://evil.example")`,
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "network.non_whitelisted_domain")
}

func TestScanner_PreservesCRLFContinuedImportSessionContext(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("python", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputCode,
		Language:  "python",
	})

	for _, chunk := range []string{
		"from requests import request \\\r",
		"as send",
	} {
		report, err := scanner.Scan(context.Background(), ScanInput{
			ToolName:      "write_stdin",
			Backend:       BackendHostExec,
			SessionID:     "python",
			SessionInput:  chunk,
			sessionSubmit: true,
		})
		require.NoError(t, err)
		require.Equal(t, DecisionAllow, report.Decision)
		scanner.sessions.commitInput("python", chunk, true)
	}

	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "python",
		SessionInput:  `send("GET", "https://evil.example")`,
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "network.non_whitelisted_domain")
}

func TestScanner_PreservesCodeFindingsFromSessionInput(t *testing.T) {
	scanner := newTestScanner(t, testPolicy(t))
	scanner.sessions = newSessionTracker()
	scanner.sessions.registerWithInfo("python", sessionInfo{
		Backend:   BackendHostExec,
		InputMode: sessionInputCode,
		Language:  "python",
	})

	report, err := scanner.Scan(context.Background(), ScanInput{
		ToolName:      "write_stdin",
		Backend:       BackendHostExec,
		SessionID:     "python",
		SessionInput:  `import shutil; p=chr(47); shutil.rmtree(p)`,
		sessionSubmit: true,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Contains(t, ruleIDSet(report.Findings), "code.dangerous_delete")
}
