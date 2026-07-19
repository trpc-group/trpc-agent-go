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
			input:      scanCommand("curl https://api.github.com/repos/x/y"),
			decision:   DecisionAllow,
			requiredID: "SAFETY_NO_FINDINGS",
		},
		{
			name:       "shell wrapper",
			input:      scanCommand("bash -c 'echo safe'"),
			decision:   DecisionDeny,
			requiredID: "CMD_SHELL_WRAPPER",
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
		{"curl remap", "curl --resolve example.com:443:127.0.0.1 https://example.com", "NETWORK_CURL_REMAP", DecisionDeny},
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

	safeSegments := make([]string, 500)
	for index := range safeSegments {
		safeSegments[index] = "go env"
	}
	started := time.Now()
	report, scanErr := guard.Scan(
		context.Background(),
		scanCommand(strings.Join(safeSegments, " | ")),
	)
	require.NoError(t, scanErr)
	require.LessOrEqual(t, time.Since(started), time.Second)
	require.Equal(t, DecisionAsk, report.Decision)
	compound := requireFinding(t, report, "SHELL_COMPOUND_COMMAND")
	require.Contains(t, compound.Evidence, "parsed_segments=500")
	require.NoError(t, findingAbsent(report, "CMD_DANGEROUS_DELETE"))

	safeSegments[499] = "rm -rf /"
	started = time.Now()
	report, scanErr = guard.Scan(
		context.Background(),
		scanCommand(strings.Join(safeSegments, " | ")),
	)
	require.NoError(t, scanErr)
	require.LessOrEqual(t, time.Since(started), time.Second)
	require.Equal(t, DecisionDeny, report.Decision)
	dangerous := requireFinding(t, report, "CMD_DANGEROUS_DELETE")
	require.Contains(t, dangerous.Evidence, "segment_index=499")
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
	guard, err := NewGuard(
		DefaultPolicy(),
		func(options *guardOptions) error {
			options.rules = []rule{staticRule{findings: []Finding{
				newFinding("ASK_LOW", RiskLevelLow, DecisionAsk, "ask", "review"),
				newFinding("CMD_NOT_ALLOWED", RiskLevelHigh, DecisionDeny, "generic", "deny"),
				newFinding("PATH_SSH_CREDENTIAL", RiskLevelHigh, DecisionDeny, "credential", "deny"),
				newFinding("REVIEW_CRITICAL", RiskLevelCritical, DecisionNeedsHumanReview, "review", "review"),
			}}}
			return nil
		},
	)
	require.NoError(t, err)
	report, scanErr := guard.Scan(context.Background(), scanCommand("go env"))
	require.NoError(t, scanErr)
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

func TestGuardRulePanicFailsClosed(t *testing.T) {
	guard, err := NewGuard(
		DefaultPolicy(),
		func(options *guardOptions) error {
			options.rules = []rule{panicRule{}, staticRule{}}
			return nil
		},
	)
	require.NoError(t, err)
	report, scanErr := guard.Scan(context.Background(), scanCommand("go env"))
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "SAFETY_RULE_PANIC", report.RuleID)
}

func TestGuardRuleAndIdentifierPanicFailsClosed(t *testing.T) {
	guard, err := NewGuard(
		DefaultPolicy(),
		func(options *guardOptions) error {
			options.rules = []rule{doublePanicRule{}}
			return nil
		},
	)
	require.NoError(t, err)
	report, scanErr := guard.Scan(context.Background(), scanCommand("go env"))
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "SAFETY_SCAN_FAILED", report.RuleID)
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

func TestGuardCancellationDuringScanNeverAllows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	guard, err := NewGuard(
		DefaultPolicy(),
		func(options *guardOptions) error {
			options.rules = []rule{cancelingRule{cancel: cancel}}
			return nil
		},
	)
	require.NoError(t, err)

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

	started := make(chan struct{})
	resume := make(chan struct{})
	observed := make(chan ScanInput, 1)
	copyGuard, err := NewGuard(
		DefaultPolicy(),
		func(options *guardOptions) error {
			options.rules = []rule{captureRule{
				started:  started,
				resume:   resume,
				observed: observed,
			}}
			return nil
		},
	)
	require.NoError(t, err)
	input := scanCommand("go env")
	input.Args = []string{"safe"}
	input.Env = map[string]string{"LANG": "C"}
	input.CodeBlocks = []CodeBlockInput{{Language: "go", Code: "safe"}}
	done := make(chan struct{})
	go func() {
		_, _ = copyGuard.Scan(context.Background(), input)
		close(done)
	}()
	<-started
	input.Args[0] = "mutated"
	input.Env["LANG"] = "mutated"
	input.CodeBlocks[0].Code = "mutated"
	close(resume)
	snapshot := <-observed
	<-done
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
	input.MaxOutputSize = 1 << 30
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.NoError(t, findingAbsent(report, "RESOURCE_OUTPUT_LIMIT_EXCEEDED"))
	require.NoError(t, findingAbsent(report, "SECRET_IN_TOOL_OUTPUT"))
}

func scanCommand(command string) ScanInput {
	return ScanInput{
		ToolName:      "workspace_exec",
		Kind:          ExecutionKindWorkspaceExec,
		Operation:     OperationExecute,
		Command:       command,
		WorkingDir:    ".",
		Env:           map[string]string{},
		Backend:       BackendWorkspaceExec,
		Timeout:       30 * time.Second,
		MaxOutputSize: 1 << 20,
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

type staticRule struct {
	findings []Finding
}

func (staticRule) ID() string { return "static" }

func (rule staticRule) Evaluate(
	context.Context,
	ScanInput,
	Policy,
) []Finding {
	return append([]Finding(nil), rule.findings...)
}

type panicRule struct{}

func (panicRule) ID() string { return "panic" }

func (panicRule) Evaluate(context.Context, ScanInput, Policy) []Finding {
	panic("test panic")
}

type doublePanicRule struct{}

func (doublePanicRule) ID() string { panic("test identifier panic") }

func (doublePanicRule) Evaluate(context.Context, ScanInput, Policy) []Finding {
	panic("test evaluation panic")
}

type cancelingRule struct {
	cancel context.CancelFunc
}

func (cancelingRule) ID() string { return "canceling" }

func (rule cancelingRule) Evaluate(context.Context, ScanInput, Policy) []Finding {
	rule.cancel()
	return nil
}

type captureRule struct {
	started  chan<- struct{}
	resume   <-chan struct{}
	observed chan<- ScanInput
}

func (captureRule) ID() string { return "capture" }

func (rule captureRule) Evaluate(
	_ context.Context,
	input ScanInput,
	_ Policy,
) []Finding {
	close(rule.started)
	<-rule.resume
	rule.observed <- input
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
actions:
  parse_error: deny
  unknown_language: needs_human_review
  pipeline: ask
  dependency_install: ask
  host_pty: ask
  host_background: deny
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
	_, err = NewGuard(DefaultPolicy(), func(options *guardOptions) error {
		options.rules = nil
		return nil
	})
	require.ErrorContains(t, err, "guard requires rules")

	invalidLimits := DefaultPolicy()
	invalidLimits.maxTimeout = 0
	_, err = NewGuard(invalidLimits)
	require.ErrorContains(t, err, "invalid policy limits")

	failOpenAction := DefaultPolicy()
	failOpenAction.parseErrorAction = DecisionAllow
	_, err = NewGuard(failOpenAction)
	require.ErrorContains(t, err, "invalid policy action")
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
		Kind:      ExecutionKindWorkspaceSession,
		Operation: OperationSessionPoll,
		Backend:   BackendWorkspaceExec,
		Timeout:   30 * time.Second,
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
		ToolName:  "workspace_session",
		Kind:      ExecutionKindWorkspaceSession,
		Operation: OperationSessionInput,
		Backend:   BackendWorkspaceExec,
		Timeout:   30 * time.Second,
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

func TestScanNonShellExecutableText(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	input := ScanInput{
		ToolName:  "code_execution",
		Kind:      ExecutionKindCodeExec,
		Operation: OperationCodeExecute,
		Backend:   BackendCodeExec,
		CodeBlocks: []CodeBlockInput{{
			Language: "python",
			Code:     "import os; os.system('rm -rf /')",
		}},
	}
	report, scanErr := guard.Scan(context.Background(), input)
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
}

func TestScanCommandDenied(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
	require.NoError(t, err)

	report, scanErr := guard.Scan(context.Background(), scanCommand("wget evil.example/file"))
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
}

func TestScanWithEnvURLDetectionProxy(t *testing.T) {
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
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
		"CURL_HOME":      `C:\tools\curl`,
		"CURL_CA_BUNDLE": `C:\certs\ca.pem`,
		"NO_PROXY":       "internal.example",
	}, policy)
	require.Empty(t, ignored)

	for _, key := range []string{"URL", "SERVICE_URL", "PROXY", "HTTPS_PROXY"} {
		findings := networkEnvironmentFindings(
			map[string]string{key: "https://evil.example/path"},
			policy,
		)
		require.NotEmpty(t, findings, key)
	}
}

func TestResourceSleepInspectionUsesParsedCommands(t *testing.T) {
	guard, err := NewGuard(DefaultPolicy())
	require.NoError(t, err)

	for _, command := range []string{
		"sleep 1; sleep 120",
		"sleep invalid",
		"sleep 5 6",
		"sleep 1e100",
	} {
		report, scanErr := guard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, scanErr)
		requireFinding(t, report, "RESOURCE_LONG_SLEEP")
	}

	report, scanErr := guard.Scan(
		context.Background(),
		scanCommand("echo sleep 120"),
	)
	require.NoError(t, scanErr)
	require.NoError(t, findingAbsent(report, "RESOURCE_LONG_SLEEP"))
}

func TestSleepCommandFollowsPlatformExecutableRules(t *testing.T) {
	require.True(t, sleepCommand("sleep"))
	if runtime.GOOS == "windows" {
		require.True(t, sleepCommand("SLEEP.EXE"))
	} else {
		require.False(t, sleepCommand("sleep.exe"))
		require.False(t, sleepCommand("SLEEP"))
	}
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
	policy := DefaultPolicy()
	guard, err := NewGuard(policy)
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
			report, scanErr := guard.Scan(context.Background(), scanCommand(test.command))
			require.NoError(t, scanErr)
			requireFinding(t, report, "CMD_DANGEROUS_DELETE")
		})
	}
}
