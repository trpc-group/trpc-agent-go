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

func TestScannerRedactsSecrets(t *testing.T) {
	report := NewScanner(DefaultPolicy()).Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  `echo token=sk-secret`,
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, "sk-secret")
	require.NotContains(t, report.Findings[0].Evidence, "sk-secret")
}

func TestScannerOptionallyRedactsSensitivePaths(t *testing.T) {
	p := DefaultPolicy()
	p.RedactSensitivePaths = true
	report := NewScanner(p).Scan(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "cat ~/.ssh/id_rsa",
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, "~/.ssh")
	require.NotContains(t, report.Findings[0].Evidence, "~/.ssh")
	require.Contains(t, report.Findings[0].Evidence, sensitivePathRedaction)
}

func TestScannerRedactsSensitivePathAliases(t *testing.T) {
	p := DefaultPolicy()
	p.RedactSensitivePaths = true
	report := NewScanner(p).Scan(context.Background(), Request{
		ToolName: "hostexec_exec_command",
		Backend:  BackendHostExec,
		Command:  "cat /home/deploy/.ssh/id_rsa /root/.aws/credentials .env.production",
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.True(t, report.Redacted)
	require.NotContains(t, report.Command, "/home/deploy/.ssh")
	require.NotContains(t, report.Command, "/root/.aws")
	require.NotContains(t, report.Command, ".env.production")
	for _, f := range report.Findings {
		require.NotContains(t, f.Evidence, "/home/deploy/.ssh")
		require.NotContains(t, f.Evidence, "/root/.aws")
		require.NotContains(t, f.Evidence, ".env.production")
	}
}

func TestScannerScansOutputForSecretLeakage(t *testing.T) {
	report := NewScanner(DefaultPolicy()).ScanOutput(context.Background(), Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
	}, "finished with password=super-secret")
	require.Equal(t, DecisionDeny, report.Decision)
	require.True(t, report.Redacted)
	require.Equal(t, ruleSecretLeakage, report.Findings[0].RuleID)
	require.NotContains(t, report.Findings[0].Evidence, "super-secret")
}

func TestScannerRedactsCommonSecretShapes(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		secret string
	}{
		{
			name:   "json token",
			text:   `{"token":"abc def ghi"}`,
			secret: "abc def ghi",
		},
		{
			name:   "bearer authorization",
			text:   "Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
			secret: "abcdefghijklmnopqrstuvwxyz012345",
		},
		{
			name:   "github token",
			text:   "token=ghp_abcdefghijklmnopqrstuvwxyz1234567890",
			secret: "ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		},
		{
			name:   "openai key",
			text:   "OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456",
			secret: "sk-abcdefghijklmnopqrstuvwxyz123456",
		},
		{
			name:   "jwt",
			text:   "bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
			secret: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := NewScanner(DefaultPolicy()).ScanOutput(context.Background(), Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
			}, tt.text)
			require.Equal(t, DecisionDeny, report.Decision)
			require.True(t, report.Redacted)
			require.NotContains(t, report.Findings[0].Evidence, tt.secret)
		})
	}
}
