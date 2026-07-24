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

func TestGuardStableAggregation(t *testing.T) {
	guard, err := NewGuard(
		DefaultPolicy(),
		func(options *guardOptions) error {
			options.rules = []rule{
				staticRule{findings: []Finding{
					newFinding("Z_ASK", RiskLevelCritical, DecisionAsk, "ask", "review"),
					newFinding("CMD_NOT_ALLOWED", RiskLevelHigh, DecisionDeny, "generic", "deny"),
					newFinding("PATH_SSH_CREDENTIAL", RiskLevelHigh, DecisionDeny, "credential", "deny"),
					newFinding("A_REVIEW", RiskLevelCritical, DecisionNeedsHumanReview, "review", "review"),
				}},
			}
			return nil
		},
	)
	require.NoError(t, err)
	report, scanErr := guard.Scan(context.Background(), scanCommand("go env"))
	require.NoError(t, scanErr)
	require.Equal(t, DecisionDeny, report.Decision)
	require.Equal(t, "PATH_SSH_CREDENTIAL", report.RuleID)
	require.Len(t, report.Findings, 4)
	require.Equal(t, "PATH_SSH_CREDENTIAL", report.Findings[0].RuleID)
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

func TestNewGuardValidation(t *testing.T) {
	_, err := NewGuard(Policy{})
	require.Error(t, err)
	_, err = NewGuard(DefaultPolicy(), nil)
	require.Error(t, err)
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
