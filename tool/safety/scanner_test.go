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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGuardPublicSamples(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedDomains = []string{"api.github.com"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	tests := publicSamples()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(context.Background(), test.input)
			require.NoError(t, scanErr)
			require.Equal(t, test.decision, report.Decision)
			require.NotEmpty(t, report.RiskLevel)
			require.NotEmpty(t, report.RuleID)
			require.NotEmpty(t, report.Evidence)
			require.NotEmpty(t, report.Recommendation)
			require.NotNil(t, report.Findings)
			require.Equal(t, test.decision != DecisionAllow, report.Blocked)
			if test.requiredID == "SAFETY_NO_FINDINGS" {
				require.Equal(t, test.requiredID, report.RuleID)
				require.Empty(t, report.Findings)
				return
			}
			requireFinding(t, report, test.requiredID)
		})
	}
}

type publicSample struct {
	name       string
	input      ScanInput
	decision   Decision
	requiredID string
}

func publicSamples() []publicSample {
	samples := publicSamplesCommands()
	samples = append(samples, publicSamplesPolicy()...)
	return append(samples, publicSamplesRuntime()...)
}

func publicSamplesCommands() []publicSample {
	return []publicSample{
		{
			name:       "safe go test",
			input:      scanCommand("go test ./..."),
			decision:   DecisionAllow,
			requiredID: "SAFETY_NO_FINDINGS",
		},
		{
			name:       "dangerous delete",
			input:      scanCommand("rm -rf /"),
			decision:   DecisionDeny,
			requiredID: "CMD_DANGEROUS_DELETE",
		},
		{
			name:       "SSH credential",
			input:      scanCommand("cat ~/.ssh/id_rsa"),
			decision:   DecisionDeny,
			requiredID: "PATH_SSH_CREDENTIAL",
		},
		{
			name:       "non-allowlisted network",
			input:      scanCommand("custom-fetch https://evil.example/file"),
			decision:   DecisionDeny,
			requiredID: "NETWORK_DOMAIN_DENIED",
		},
	}
}

func publicSamplesPolicy() []publicSample {
	return []publicSample{
		{
			name:       "allowlisted network",
			input:      scanCommand("curl -q --noproxy '*' https://api.github.com/repos/x/y"),
			decision:   DecisionAllow,
			requiredID: "SAFETY_NO_FINDINGS",
		},
		{
			name:       "shell wrapper",
			input:      scanCommand("bash -c 'echo safe'"),
			decision:   DecisionDeny,
			requiredID: "CMD_PROCESS_WRAPPER",
		},
		{
			name:       "pipeline",
			input:      scanCommand("go env | cat"),
			decision:   DecisionAsk,
			requiredID: "SHELL_COMPOUND_COMMAND",
		},
		{
			name:       "dependency install",
			input:      scanCommand("go install example.com/tool@latest"),
			decision:   DecisionAsk,
			requiredID: "DEPENDENCY_GO_INSTALL",
		},
	}
}

func publicSamplesRuntime() []publicSample {
	return []publicSample{
		{
			name:       "long sleep",
			input:      scanCommand("sleep 120"),
			decision:   DecisionDeny,
			requiredID: "RESOURCE_LONG_SLEEP",
		},
		{
			name:       "oversized output",
			input:      scanCommand("yes"),
			decision:   DecisionDeny,
			requiredID: "RESOURCE_UNBOUNDED_OUTPUT",
		},
		{
			name: "host PTY",
			input: func() ScanInput {
				input := scanCommand("go test ./...")
				input.ToolName = "exec_command"
				input.Kind = ExecutionKindHostExec
				input.Backend = BackendHostExec
				input.PTY = true
				input.Interactive = true
				return input
			}(),
			decision:   DecisionAsk,
			requiredID: "HOST_PTY_SESSION",
		},
		{
			name:       "dynamic network target",
			input:      scanCommand(`curl "https://${TARGET}/file"`),
			decision:   DecisionNeedsHumanReview,
			requiredID: "NETWORK_DYNAMIC_TARGET",
		},
	}
}

func TestGuardForcedCategoriesAndParseFailClosed(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedDomains = []string{"example.com"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	tests := []struct {
		name     string
		command  string
		ruleID   string
		decision Decision
	}{
		{"delete after parse failure", "rm -rf / $(whoami)", "CMD_DANGEROUS_DELETE", DecisionDeny},
		{"environment file", "cat .env", "PATH_ENV_FILE", DecisionDeny},
		{"credential file", "cat ~/.aws/credentials", "PATH_CREDENTIAL_FILE", DecisionDeny},
		{"IP literal", "curl https://127.0.0.1/file", "NETWORK_IP_LITERAL", DecisionDeny},
		{"dynamic URL", `curl "https://${TARGET}/file"`, "NETWORK_DYNAMIC_TARGET", DecisionNeedsHumanReview},
		{"curl remap", "curl --resolve example.com:443:127.0.0.1 https://example.com", ruleNetworkDestinationMap, DecisionDeny},
		{"privilege wrapper", "env sudo go test ./...", "CMD_PRIVILEGE_ESCALATION", DecisionDeny},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(context.Background(), scanCommand(test.command))
			require.NoError(t, scanErr)
			require.Equal(t, test.decision, report.Decision)
			requireFinding(t, report, test.ruleID)
		})
	}
}

func TestPathRuleUsesParsedArgvAndLexicalWorkingDirectory(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedCommands = []string{"cat"}
	policy.deniedCommands = nil
	policy.deniedPaths = []string{"/workspace/secrets", "/etc/passwd"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	tests := []struct {
		name     string
		input    ScanInput
		rule     string
		evidence string
	}{
		{
			name: "option value relative to cwd",
			input: func() ScanInput {
				input := scanCommand("cat --file=secrets/token")
				input.WorkingDir = "/workspace"
				return input
			}(),
			rule: "PATH_FORBIDDEN",
		},
		{
			name:  "quoted windows credential path",
			input: scanCommand(`cat "C:\Users\me\.ssh\id_rsa"`),
			rule:  "PATH_SSH_CREDENTIAL",
		},
		{
			name: "preparsed option value",
			input: ScanInput{
				ToolName: "custom.exec", Kind: ExecutionKindCustom,
				Operation: OperationExecute,
				Args:      []string{"cat", "--file=.env"}, Backend: BackendCustom,
			},
			rule: "PATH_ENV_FILE",
		},
		{
			name:     "quote concatenation",
			input:    scanCommand(`cat /e'tc'/passwd`),
			rule:     "PATH_FORBIDDEN",
			evidence: "source=command.segment0.argv1",
		},
		{
			name:     "backslash escape",
			input:    scanCommand(`cat /etc/pass\wd`),
			rule:     "PATH_FORBIDDEN",
			evidence: "source=command.segment0.argv1",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, scanErr := guard.Scan(context.Background(), test.input)
			require.NoError(t, scanErr)
			require.Equal(t, DecisionDeny, report.Decision)
			finding := requireFinding(t, report, test.rule)
			if test.evidence != "" {
				require.Contains(t, finding.Evidence, test.evidence)
			}
		})
	}
}

func TestCommandRuleBlocksProcessWrappersInEverySegment(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	commands := []string{
		"env curl https://evil.example",
		"xargs curl https://evil.example",
		"go version | /usr/bin/ENV curl https://evil.example",
		"go version | timeout 30 curl https://evil.example",
	}
	for _, command := range commands {
		report, scanErr := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, scanErr)
		require.Equal(t, DecisionDeny, report.Decision, command)
		requireFinding(t, report, "CMD_PROCESS_WRAPPER")
	}
}

func TestCommandRuleKeepsExplicitDenyPriority(t *testing.T) {
	policy := DefaultPolicy()
	policy.deniedCommands = append(policy.deniedCommands, "env")
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	report, err := guard.Scan(
		context.Background(), scanCommand("env curl https://evil.example"),
	)
	require.NoError(t, err)
	requireFinding(t, report, "CMD_DENIED")
}

func TestGuardQuotedNearMisses(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	for _, command := range []string{
		`echo "safe | literal"`,
		`echo "safe && literal"`,
		`echo "rm -rf /"`,
		`echo "curl https://evil.example/file"`,
	} {
		report, scanErr := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, scanErr)
		require.Equal(t, DecisionAllow, report.Decision, command)
	}
}

func TestGuardFiveHundredSegments(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	segments := make([]string, 500)
	for index := range segments {
		segments[index] = "go env"
	}
	started := time.Now()
	report, scanErr := guard.Scan(
		context.Background(), scanCommand(strings.Join(segments, " | ")),
	)
	require.NoError(t, scanErr)
	require.LessOrEqual(t, time.Since(started), time.Second)
	require.Equal(t, DecisionAsk, report.Decision)
	require.Contains(t, report.Evidence, "parsed_segments=500")

	segments[499] = "rm -rf /"
	started = time.Now()
	report, scanErr = guard.Scan(
		context.Background(), scanCommand(strings.Join(segments, " | ")),
	)
	require.NoError(t, scanErr)
	require.LessOrEqual(t, time.Since(started), time.Second)
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "CMD_DANGEROUS_DELETE")
}

func TestGuardFiveHundredLineCode(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	lines := make([]string, 500)
	for index := range lines {
		lines[index] = "value = 1"
	}
	lines[499] = `open("/home/user/.ssh/id_rsa")`
	input := ScanInput{
		ToolName:  "code_execution",
		Kind:      ExecutionKindCodeExec,
		Operation: OperationCodeExecute,
		Backend:   BackendCodeExec,
		CodeBlocks: []CodeBlockInput{{
			Language: "python",
			Code:     strings.Join(lines, "\n"),
		}},
	}
	started := time.Now()
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.LessOrEqual(t, time.Since(started), time.Second)
	require.Equal(t, DecisionDeny, report.Decision)
	requireFinding(t, report, "PATH_SSH_CREDENTIAL")
	require.NoError(t, findingAbsent(report, "RESOURCE_TIMEOUT_MISSING"))
}

func TestGuardSelectsDeterministicPrimaryFinding(t *testing.T) {
	findings := []Finding{
		newFinding("ASK_MEDIUM", RiskLevelMedium, DecisionAsk, "ask", "review"),
		newFinding("CMD_NOT_ALLOWED", RiskLevelHigh, DecisionDeny, "generic", "deny"),
		newFinding("PATH_SSH_CREDENTIAL", RiskLevelHigh, DecisionDeny, "credential", "deny"),
		newFinding("REVIEW_CRITICAL", RiskLevelCritical, DecisionNeedsHumanReview, "review", "review"),
	}
	report := buildReport(DefaultPolicy(), scanCommand("go env"), scanOutcome{
		findings: findings,
	})
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "PATH_SSH_CREDENTIAL", report.RuleID)
	require.Equal(t, "PATH_SSH_CREDENTIAL", report.Findings[0].RuleID)
}

func TestGuardBoundsPublicReportFields(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	input := scanCommand(strings.Repeat("x", maxCommandBytes+100))
	input.ToolName = strings.Repeat("t", maxEvidenceBytes+100)
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.LessOrEqual(t, len(report.Command), maxCommandBytes)
	require.LessOrEqual(t, len(report.ToolName), maxEvidenceBytes)
	require.True(t, strings.HasSuffix(report.Command, "..."))
	require.True(t, strings.HasSuffix(report.ToolName, "..."))
}

func TestGuardCanceledContextNeverAllows(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, scanErr := guard.Scan(ctx, scanCommand("go test ./..."))
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "SAFETY_SCAN_FAILED", report.RuleID)
}

func TestGuardPolicyAndInputDefensiveCopies(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	policy.allowedCommands[0] = "unsafe-mutated"
	report, scanErr := guard.Scan(context.Background(), scanCommand("go test ./..."))
	require.NoError(t, scanErr)
	require.Equal(t, DecisionAllow, report.Decision)

	input := scanCommand("go env")
	input.Args = []string{"safe"}
	input.Env = map[string]string{"LANG": "C"}
	input.CodeBlocks = []CodeBlockInput{{Language: "go", Code: "safe"}}
	snapshot := cloneScanInput(input)
	input.Args[0] = "mutated"
	input.Env["LANG"] = "mutated"
	input.CodeBlocks[0].Code = "mutated"
	require.Equal(t, "safe", snapshot.Args[0])
	require.Equal(t, "C", snapshot.Env["LANG"])
	require.Equal(t, "safe", snapshot.CodeBlocks[0].Code)
}

func TestGuardConcurrentScan(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	const scans = 64
	var wait sync.WaitGroup
	errors := make(chan error, scans)
	for index := 0; index < scans; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			report, scanErr := guard.Scan(
				context.Background(),
				scanCommand("go test ./..."),
			)
			if scanErr != nil {
				errors <- scanErr
				return
			}
			if report.Decision != DecisionAllow {
				errors <- fmt.Errorf("unexpected decision %s", report.Decision)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for scanErr := range errors {
		require.NoError(t, scanErr)
	}
}

func TestGuardDoesNotProducePostcheckOnlyRules(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	input := scanCommand("go test ./...")
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.NoError(t, findingAbsent(report, "RESOURCE_OUTPUT_LIMIT_EXCEEDED"))
	require.NoError(t, findingAbsent(report, "SECRET_IN_TOOL_OUTPUT"))
}

func scanCommand(command string) ScanInput {
	return ScanInput{
		ToolName:   "workspace_exec",
		Kind:       ExecutionKindWorkspaceExec,
		Operation:  OperationExecute,
		Command:    command,
		WorkingDir: ".",
		Env:        map[string]string{},
		Backend:    BackendWorkspaceExec,
		Timeout:    30 * time.Second,
	}
}

func requireFinding(t *testing.T, report Report, ruleID string) Finding {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.RuleID == ruleID {
			return finding
		}
	}
	require.FailNow(t, "missing finding", "rule_id=%s; report=%+v", ruleID, report)
	return Finding{}
}

func findingAbsent(report Report, ruleID string) error {
	for _, finding := range report.Findings {
		if finding.RuleID == ruleID {
			return fmt.Errorf("unexpected finding %s", ruleID)
		}
	}
	return nil
}

func TestNewGuardFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
version: 1
commands:
  allowed: [go, date]
  denied: []
paths:
  denied: []
network:
  allowed_domains: []
environment:
  allowed: [LANG, HOME, PATH]
limits:
  max_timeout: 20s
  max_output_bytes: 2048
  max_sleep: 2s
  max_concurrency: 3
`), 0o600))

	guard, err := NewGuardFromFile(path)
	require.NoError(t, err)
	require.NotNil(t, guard)
	input := scanCommand("go test ./...")
	input.Timeout = 10 * time.Second
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionAllow, report.Decision)
}

func TestNewGuardRejectsInvalidPublicInputs(t *testing.T) {
	_, err := NewGuard(Policy{})
	require.Error(t, err)
	_, err = NewGuard(DefaultPolicy(), nil)
	require.Error(t, err)
	_, err = NewGuardFromFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
	invalidLimits := DefaultPolicy()
	invalidLimits.maxTimeout = 0
	_, err = NewGuard(invalidLimits)
	require.ErrorContains(t, err, "invalid policy limits")
}

func TestScanWithUnknownLanguage(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := ScanInput{
		ToolName:  "code_execution",
		Kind:      ExecutionKindCodeExec,
		Operation: OperationCodeExecute,
		Backend:   BackendCodeExec,
		CodeBlocks: []CodeBlockInput{{
			Language: "brainfuck",
			Code:     "++++++++++[>+++++++>++++++++++>+++>+<<<<-]>++.>+.+++++++..+++.>++.<<+++++++++++++++.>.+++.------.--------.>+.>.",
		}},
	}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "CODE_UNKNOWN_LANGUAGE")
}

func TestScanChecksEveryExecutableInputSurface(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	for _, mutate := range []func(*ScanInput){
		func(input *ScanInput) { input.Args = []string{"rm", "-rf", "/"} },
		func(input *ScanInput) {
			input.Language = "bash"
			input.Script = "rm -rf /"
		},
		func(input *ScanInput) {
			input.CodeBlocks = []CodeBlockInput{{Language: "sh", Code: "rm -rf /"}}
		},
		func(input *ScanInput) { input.InitialStdin = "rm -rf /" },
		func(input *ScanInput) {
			input.Operation = OperationSessionInput
			input.SessionInput = "rm -rf /"
		},
		func(input *ScanInput) {
			input.Operation = OperationSessionInput
			input.Submit = true
		},
	} {
		input := scanCommand("")
		mutate(&input)
		report, scanErr := guard.Scan(context.Background(), input)
		require.NoError(t, scanErr)
		if input.Submit && input.SessionInput == "" {
			require.NotEqual(t, DecisionAllow, report.Decision)
			continue
		}
		requireFinding(t, report, "CMD_DANGEROUS_DELETE")
	}
}

func TestScanWithTimeoutMissing(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Timeout = 0
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "RESOURCE_TIMEOUT_MISSING")
}

func TestScanWithTimeoutExceeded(t *testing.T) {
	policy := DefaultPolicy()
	policy.maxTimeout = 1 * time.Second
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Timeout = 2 * time.Hour
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "RESOURCE_TIMEOUT_EXCEEDED")
}

func TestScanCommandAllowListUsesPlatformCaseRules(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedCommands = []string{"go"}
	policy.deniedCommands = nil
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, err := guard.Scan(context.Background(), scanCommand("GO version"))
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		require.NoError(t, findingAbsent(report, "CMD_NOT_ALLOWED"))
		return
	}
	requireFinding(t, report, "CMD_NOT_ALLOWED")
}

func TestScanWithPTYAndInteractive(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Kind = ExecutionKindHostExec
	input.Backend = BackendHostExec
	input.PTY = true
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "HOST_PTY_SESSION")
	require.NoError(t, findingAbsent(report, "HOST_INTERACTIVE_SESSION"))
}

func TestScanWithInteractiveNoPTY(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Kind = ExecutionKindHostExec
	input.Backend = BackendHostExec
	input.Interactive = true
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "HOST_INTERACTIVE_SESSION")
}

func TestScanWithBackgroundProcess(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Kind = ExecutionKindHostExec
	input.Backend = BackendHostExec
	input.Background = true
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "HOST_BACKGROUND_PROCESS")
}

func TestScanSessionPollSkipsMostRules(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := ScanInput{
		ToolName:  "workspace_session",
		SessionID: "session-1",
		Kind:      ExecutionKindWorkspaceSession,
		Operation: OperationSessionPoll,
		Backend:   BackendWorkspaceExec,
	}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionAllow, report.Decision)
}

func TestScanSessionInput(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := ScanInput{
		ToolName:     "workspace_session",
		SessionID:    "session-1",
		Kind:         ExecutionKindWorkspaceSession,
		Operation:    OperationSessionInput,
		SessionInput: "whoami",
		Backend:      BackendWorkspaceExec,
	}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "HOST_SESSION_INPUT")
}

func TestScanWithSyscallOverwrite(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("dd if=/dev/zero of=/dev/sda bs=1M"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "CMD_SYSTEM_OVERWRITE")
}

func TestScanClassifiesDestructiveAndPrivilegedCommands(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	for _, test := range []struct {
		command string
		ruleID  string
	}{
		{"dd if=/dev/zero of=/tmp/image", "CMD_SYSTEM_OVERWRITE"},
		{"mkfs.ext4 /dev/sda1", "CMD_SYSTEM_OVERWRITE"},
		{"format C:", "CMD_SYSTEM_OVERWRITE"},
		{"sudo go version", "CMD_PRIVILEGE_ESCALATION"},
		{"doas go version", "CMD_PRIVILEGE_ESCALATION"},
		{"su -c whoami", "CMD_PRIVILEGE_ESCALATION"},
		{"runas /user:admin whoami", "CMD_PRIVILEGE_ESCALATION"},
		{"pkexec whoami", "CMD_PRIVILEGE_ESCALATION"},
	} {
		report, scanErr := guard.Scan(
			context.Background(), scanCommand(test.command),
		)
		require.NoError(t, scanErr)
		requireFinding(t, report, test.ruleID)
	}
}

func TestScanWithInfiniteLoop(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("while true; do echo loop; done"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "RESOURCE_INFINITE_LOOP")
}

func TestScanWithForkBomb(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand(":(){ :|:& };:"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "RESOURCE_FORK_BOMB")
}

func TestScanWithHighConcurrency(t *testing.T) {
	policy := DefaultPolicy()
	policy.maxConcurrency = 2
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("xargs -P 4 -n 1 echo"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "RESOURCE_HIGH_CONCURRENCY")
}

func TestScanWithSecretInEnv(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Env = map[string]string{"API_KEY": "sk-proj-secret-value"}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "ENV_SENSITIVE_VALUE")
}

func TestScanClassifiesSecretMaterial(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)
	for _, test := range []struct {
		code   string
		ruleID string
	}{
		{`api_key = "sk-proj-abc123"`, "SECRET_API_KEY"},
		{`token = "ghp_abcdefghijklmnopqrstuvwxyz"`, "SECRET_TOKEN"},
		{"-----BEGIN PRIVATE KEY-----\nmaterial\n-----END PRIVATE KEY-----", "SECRET_PRIVATE_KEY"},
		{`password = "correct horse battery staple"`, "SECRET_PASSWORD"},
		{`credential = "AKIAIOSFODNN7EXAMPLE"`, "SECRET_CLOUD_CREDENTIAL"},
	} {
		input := ScanInput{
			ToolName:  "code_execution",
			Kind:      ExecutionKindCodeExec,
			Operation: OperationCodeExecute,
			Backend:   BackendCodeExec,
			CodeBlocks: []CodeBlockInput{{
				Language: "python",
				Code:     test.code,
			}},
		}
		report, scanErr := guard.Scan(context.Background(), input)
		require.NoError(t, scanErr)
		requireFinding(t, report, test.ruleID)
	}
}

func TestScanWithAllowedEnv(t *testing.T) {
	policy := DefaultPolicy()
	policy.allowedEnv = []string{"LANG", "HOME", "PATH"}
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := scanCommand("go test ./...")
	input.Env = map[string]string{"UNLISTED_VAR": "value"}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	requireFinding(t, report, "ENV_KEY_NOT_ALLOWED")
}

func TestScanWithDependencySystemInstall(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("apt install nginx"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "DEPENDENCY_SYSTEM_INSTALL")
}

func TestScanWithDependencyNpmGlobalInstall(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("npm install -g typescript"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "DEPENDENCY_NPM_INSTALL")
}

func TestScanWithDependencyPipInstall(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("pip install requests"))
	require.NoError(t, scanErr)
	requireFinding(t, report, "DEPENDENCY_PIP_INSTALL")
}
