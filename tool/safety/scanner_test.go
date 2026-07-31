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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestScannerDoesNotTreatUnrelatedLongRMOptionsAsRecursive(t *testing.T) {
	scanner := NewScanner(DefaultPolicy())
	for _, command := range []string{
		"rm --dir --force build",
		"rm --no-preserve-root build",
	} {
		report, err := scanner.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Command:  command,
			Backend:  BackendWorkspaceExec,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range report.Findings {
			if finding.RuleID == ruleDangerousDelete {
				t.Fatalf("%q produced dangerous-delete finding: %+v", command, finding)
			}
		}
	}
}

func TestFoldedStringConstantsAreBounded(t *testing.T) {
	code := strings.Repeat("\"a\"+", 5000) + "\"a\""
	constants := foldedStringConstants(code)
	if len(constants) > maxFoldedConstants {
		t.Fatalf("constants = %d, want <= %d", len(constants), maxFoldedConstants)
	}
	for _, value := range constants {
		if len(value) > maxFoldedConstantLen {
			t.Fatalf("constant length = %d, want <= %d", len(value), maxFoldedConstantLen)
		}
	}
}

func TestScannerRequiredScenarios(t *testing.T) {
	base := DefaultPolicy()
	base.AllowedCommands = []string{
		"go", "echo", "wc", "cat", "curl", "sh", "sleep",
	}
	base.DeniedCommands = []string{"rm", "wget", "nc", "ssh"}
	base.NetworkAllowlist = []string{"github.com"}
	base.MaxTimeoutSec = 60
	base.MaxOutputBytes = 1024
	base.EnvAllowlist = []string{"PATH"}

	cases := []struct {
		name     string
		req      ScanRequest
		decision Decision
		rule     string
	}{
		{
			name: "safe go test",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "go test ./tool/safety",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionAllow,
			rule:     ruleAllow,
		},
		{
			name: "dangerous delete",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "rm -rf /",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionDeny,
			rule:     ruleDangerousDelete,
		},
		{
			name: "read ssh key",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "cat ~/.ssh/id_rsa",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionDeny,
			rule:     ruleSensitivePath,
		},
		{
			name: "non allowlisted network",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "curl https://evil.example.com/data",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionDeny,
			rule:     ruleNetwork,
		},
		{
			name: "allowlisted network",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "curl https://github.com/trpc-group/trpc-agent-go",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionAllow,
			rule:     ruleAllow,
		},
		{
			name: "shell wrapper bypass",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "sh -c 'curl https://evil.example.com'",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionDeny,
			rule:     ruleCommandPolicy,
		},
		{
			name: "pipeline requires review",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "echo hello | wc -c",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionAsk,
			rule:     rulePipeline,
		},
		{
			name: "dependency install",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "go install github.com/example/tool@latest",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionAsk,
			rule:     ruleDependency,
		},
		{
			name: "long sleep",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "sleep 600",
				Backend:  BackendWorkspaceExec,
			},
			decision: DecisionAsk,
			rule:     ruleTimeout,
		},
		{
			name: "oversized output",
			req: ScanRequest{
				ToolName:       "workspace_exec",
				Command:        "echo hello",
				Backend:        BackendWorkspaceExec,
				MaxOutputBytes: 1 << 20,
			},
			decision: DecisionAsk,
			rule:     ruleOutput,
		},
		{
			name: "hostexec pty long session",
			req: ScanRequest{
				ToolName:   "exec_command",
				Command:    "go test ./...",
				Backend:    BackendHostExec,
				TTY:        true,
				Background: true,
			},
			decision: DecisionAsk,
			rule:     ruleHostPTY,
		},
		{
			name: "ask human review for env",
			req: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "echo hello",
				Backend:  BackendWorkspaceExec,
				Env:      map[string]string{"CUSTOM": "1"},
			},
			decision: DecisionAsk,
			rule:     ruleEnv,
		},
	}

	scanner := NewScanner(base)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if report.Decision != tc.decision {
				t.Fatalf("decision = %s, want %s; report=%+v", report.Decision, tc.decision, report)
			}
			if report.RuleID != tc.rule {
				t.Fatalf("rule = %s, want %s; report=%+v", report.RuleID, tc.rule, report)
			}
			if report.Blocked != (tc.decision != DecisionAllow) {
				t.Fatalf("blocked = %v, want %v", report.Blocked, tc.decision != DecisionAllow)
			}
			if len(report.Evidence) == 0 || report.Recommendation == "" {
				t.Fatalf("report missing evidence/recommendation: %+v", report)
			}
		})
	}
}

func TestBuildReportAllowAndAskAggregatesToAsk(t *testing.T) {
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "echo hello | wc -c",
		Backend:  BackendWorkspaceExec,
	}

	findings := []Finding{
		finding(
			ruleAllow,
			DecisionAllow,
			RiskLow,
			"safe command",
			"Execution may continue.",
		),
		finding(
			rulePipeline,
			DecisionAsk,
			RiskMedium,
			"command contains a pipeline",
			"Review the pipeline before execution.",
		),
	}

	report := buildReport(req, findings)

	if report.RuleID != rulePipeline {
		t.Errorf(
			"RuleID = %q, want %q",
			report.RuleID,
			rulePipeline,
		)
	}

	if !report.Blocked {
		t.Error("Blocked = false, want true for an ask decision")
	}

	if len(report.Findings) != 2 {
		t.Errorf(
			"len(Findings) = %d, want 2",
			len(report.Findings),
		)
	}

	if len(report.Evidence) != 2 {
		t.Errorf(
			"len(Evidence) = %d, want 2",
			len(report.Evidence),
		)
	}
}

func TestBuildReportAskAndDenyAggregatesToDeny(t *testing.T) {
	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "rm -rf /",
		Backend:  BackendWorkspaceExec,
	}

	findings := []Finding{
		finding(
			rulePipeline,
			DecisionAsk,
			RiskCritical,
			"command requires human review",
			"Review the command before execution.",
		),
		finding(
			ruleDangerousDelete,
			DecisionDeny,
			RiskHigh,
			"command performs dangerous deletion",
			"Remove the destructive command.",
		),
	}

	report := buildReport(req, findings)

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q",
			report.Decision,
			DecisionDeny,
		)
	}

	if report.RuleID != ruleDangerousDelete {
		t.Errorf(
			"RuleID = %q, want %q",
			report.RuleID,
			ruleDangerousDelete,
		)
	}

	if !report.Blocked {
		t.Error("Blocked = false, want true for a deny decision")
	}

	if len(report.Findings) != 2 {
		t.Errorf(
			"len(Findings) = %d, want 2",
			len(report.Findings),
		)
	}
}

func TestBuildReportEqualRankFindingsKeepStableOrder(t *testing.T) {
	const (
		firstDenyRule  = "SAFE-TEST-FIRST-DENY"
		secondDenyRule = "SAFE-TEST-SECOND-DENY"
	)

	req := ScanRequest{
		ToolName: "workspace_exec",
		Command:  "test command",
		Backend:  BackendWorkspaceExec,
	}

	findings := []Finding{
		finding(
			rulePipeline,
			DecisionAsk,
			RiskCritical,
			"ask evidence",
			"Review the command.",
		),
		finding(
			firstDenyRule,
			DecisionDeny,
			RiskHigh,
			"first deny evidence",
			"Remove the first dangerous operation.",
		),
		finding(
			secondDenyRule,
			DecisionDeny,
			RiskHigh,
			"second deny evidence",
			"Remove the second dangerous operation.",
		),
	}

	report := buildReport(req, findings)

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q",
			report.Decision,
			DecisionDeny,
		)
	}

	if report.RuleID != firstDenyRule {
		t.Errorf(
			"RuleID = %q, want stable first rule %q",
			report.RuleID,
			firstDenyRule,
		)
	}

	wantRuleOrder := []string{
		firstDenyRule,
		secondDenyRule,
		rulePipeline,
	}

	if len(report.Findings) != len(wantRuleOrder) {
		t.Fatalf(
			"len(Findings) = %d, want %d",
			len(report.Findings),
			len(wantRuleOrder),
		)
	}

	for i, wantRule := range wantRuleOrder {
		if report.Findings[i].RuleID != wantRule {
			t.Errorf(
				"Findings[%d].RuleID = %q, want %q",
				i,
				report.Findings[i].RuleID,
				wantRule,
			)
		}
	}
}

func TestScannerRedactsSecrets(t *testing.T) {
	p := DefaultPolicy()
	p.AllowedCommands = []string{"echo"}
	p.EnvAllowlist = []string{"PATH"}
	scanner := NewScanner(p)
	report, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Command:  "echo api_key=sk-test-secret-value",
		Backend:  BackendWorkspaceExec,
		Env:      map[string]string{"PASSWORD": "supersecret"},
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if report.Decision != DecisionDeny {
		t.Fatalf("decision = %s, want deny", report.Decision)
	}
	if !report.Redacted {
		t.Fatalf("expected redaction: %+v", report)
	}
	if strings.Contains(report.Command, "sk-test-secret-value") {
		t.Fatalf("command was not redacted: %s", report.Command)
	}
}

func TestLoadPolicyFileYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_safety_policy.yaml")
	content := []byte(`
allowed_commands: ["go", "curl"]
denied_commands: ["rm"]
forbidden_paths: [".env"]
network_allowlist: ["example.com"]
max_timeout_sec: 7
max_output_bytes: 9
env_allowlist: ["PATH"]
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadPolicyFile(path)
	if err != nil {
		t.Fatalf("LoadPolicyFile() error = %v", err)
	}
	if got := strings.Join(p.NetworkAllowlist, ","); got != "example.com" {
		t.Fatalf("network_allowlist = %q", got)
	}
	if p.MaxTimeoutSec != 7 || p.MaxOutputBytes != 9 {
		t.Fatalf("limits not loaded: %+v", p)
	}
}

func TestPermissionPolicyDeniesBeforeExecution(t *testing.T) {
	p := DefaultPolicy()
	p.AllowedCommands = []string{"go", "cat"}
	scanner := NewScanner(p)
	policy := NewPermissionPolicy(scanner)
	args, err := json.Marshal(map[string]any{
		"command": "cat .env",
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CheckToolPermission() error = %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %s, want deny", decision.Action)
	}
	if !strings.Contains(decision.Reason, ruleSensitivePath) {
		t.Fatalf("reason missing rule id: %s", decision.Reason)
	}
}

func TestJSONLAuditorWritesEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_safety_audit.jsonl")
	p := DefaultPolicy()
	p.AllowedCommands = []string{"echo"}
	scanner := NewScanner(p, WithAuditor(NewJSONLAuditor(path)))
	if _, err := scanner.Scan(context.Background(), ScanRequest{
		ToolName: "workspace_exec",
		Command:  "echo hello",
		Backend:  BackendWorkspaceExec,
	}); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(b), `"tool_name":"workspace_exec"`) ||
		!strings.Contains(string(b), `"decision":"allow"`) {
		t.Fatalf("unexpected audit event: %s", b)
	}
}

func TestScanRequestFromArgsWorkspaceExec(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantCommand    string
		wantCwd        string
		wantEnvValue   string
		wantTimeout    int
		wantBackground bool
		wantTTY        bool
		wantMaxOutput  int
	}{
		{
			name: "canonical fields",
			args: `{
			  "command": "go test ./...",
			  "cwd": "sub/dir",
			  "env":{
			  "SAFE_VAR": "1"
			  },
			  "timeout_sec": 45,
			  "background": true,
			  "tty": true,
			  "max_output_bytes": 2048
			}`,
			wantCommand:    "go test ./...",
			wantCwd:        "sub/dir",
			wantEnvValue:   "1",
			wantTimeout:    45,
			wantBackground: true,
			wantTTY:        true,
			wantMaxOutput:  2048,
		},
		{
			name: "legacy aliases",
			args: `{
				"command": "echo hello",
				"timeoutSec": 12,
				"pty": true
			}`,
			wantCommand: "echo hello",
			wantTimeout: 12,
			wantTTY:     true,
		},
		{
			name: "timeout alias",
			args: `{
				"command": "pwd",
				"timeout": 9
			}`,
			wantCommand: "pwd",
			wantTimeout: 9,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ScanRequestFromArgs(
				"workspace_exec",
				[]byte(tc.args),
			)
			if err != nil {
				t.Fatalf(
					"ScanRequestFromArgs() error = %v",
					err,
				)
			}

			if req.ToolName != "workspace_exec" {
				t.Errorf(
					"ToolName = %q, want %q",
					req.ToolName,
					"workspace_exec",
				)
			}

			if req.Backend != BackendWorkspaceExec {
				t.Errorf(
					"Backend = %q, want %q",
					req.Backend,
					BackendWorkspaceExec,
				)
			}

			if req.Command != tc.wantCommand {
				t.Errorf(
					"Command = %q, want %q",
					req.Command,
					tc.wantCommand,
				)
			}

			if req.Cwd != tc.wantCwd {
				t.Errorf(
					"Cwd = %q, want %q",
					req.Cwd,
					tc.wantCwd,
				)
			}

			if req.Env["SAFE_VAR"] != tc.wantEnvValue {
				t.Errorf(
					"Env[SAFE_VAR] = %q, want %q",
					req.Env["SAFE_VAR"],
					tc.wantEnvValue,
				)
			}

			if req.TimeoutSec != tc.wantTimeout {
				t.Errorf(
					"TimeoutSec = %d, want %d",
					req.TimeoutSec,
					tc.wantTimeout,
				)
			}

			if req.Background != tc.wantBackground {
				t.Errorf(
					"Background = %v, want %v",
					req.Background,
					tc.wantBackground,
				)
			}

			if req.TTY != tc.wantTTY {
				t.Errorf(
					"TTY = %v, want %v",
					req.TTY,
					tc.wantTTY,
				)
			}

			if req.MaxOutputBytes != tc.wantMaxOutput {
				t.Errorf(
					"MaxOutputBytes = %d, want %d",
					req.MaxOutputBytes,
					tc.wantMaxOutput,
				)
			}
		})
	}
}

func TestScanRequestFromArgsWorkspaceExecUsesExecutionTimeoutPrecedence(
	t *testing.T,
) {
	args := []byte(`{
		"command": "sleep 600",
		"timeout": 30,
		"timeout_sec": 600,
		"timeoutSec": 900
	}`)

	req, err := ScanRequestFromArgs("workspace_exec", args)
	if err != nil {
		t.Fatalf(
			"ScanRequestFromArgs() error = %v",
			err,
		)
	}

	if req.TimeoutSec != 600 {
		t.Fatalf(
			"TimeoutSec = %d, want 600 to match workspaceexec execution precedence",
			req.TimeoutSec,
		)
	}
}

func TestScanRequestFromArgsWorkspaceExecRejectsInvalidArgs(
	t *testing.T,
) {
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "missing command",
			args:    `{}`,
			wantErr: "extract workspace_exec arguments: command is required",
		},
		{
			name:    "blank command",
			args:    `{"command":"   "}`,
			wantErr: "extract workspace_exec arguments: command is required",
		},
		{
			name:    "invalid JSON",
			args:    `{"command":`,
			wantErr: "extract workspace_exec arguments:",
		},
		{
			name: "invalid env type",
			args: `{
				"command": "echo hello",
				"env": ["PATH=/bin"]
			}`,
			wantErr: "extract workspace_exec arguments:",
		},
		{
			name: "invalid timeout type",
			args: `{
				"command": "echo hello",
				"timeout_sec": "60"
			}`,
			wantErr: "extract workspace_exec arguments:",
		},
		{
			name: "negative output limit",
			args: `{
				"command": "echo hello",
				"max_output_bytes": -1
			}`,
			wantErr: "extract workspace_exec arguments: " +
				"max_output_bytes must not be negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ScanRequestFromArgs(
				"workspace_exec",
				[]byte(tc.args),
			)
			if err == nil {
				t.Fatal(
					"ScanRequestFromArgs() succeeded for invalid arguments",
				)
			}

			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf(
					"ScanRequestFromArgs() error = %q, want it to contain %q",
					err,
					tc.wantErr,
				)
			}
		})
	}
}

func TestScanRequestFromArgsCodeExec(t *testing.T) {
	tests := []struct {
		name        string
		args        string
		wantCommand string
	}{
		{
			name: "array of code blocks",
			args: `{
				"code_blocks": [
					{
						"language": "bash",
						"code": "echo first"
					},
					{
						"language": "python",
						"code": "print('safe python')"
					},
					{
						"language": "shell",
						"code": "cat .env"
					}
				],
				"execution_id": "session-123"
			}`,
			wantCommand: "echo first\nprint('safe python')\ncat .env",
		},
		{
			name: "single code block object",
			args: `{
				"code_blocks": {
					"language": "sh",
					"code": "echo single"
				}
			}`,
			wantCommand: "echo single",
		},
		{
			name: "double encoded code blocks",
			args: `{
				"code_blocks": "[{\"language\":\"bash\",\"code\":\"echo double\"}]"
			}`,
			wantCommand: "echo double",
		},
		{
			name: "risky python block",
			args: `{
				"code_blocks": {
					"language": "python",
					"code": "import os\nos.system(\"cat .env\")"
				}
			}`,
			wantCommand: "import os\nos.system(\"cat .env\")",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ScanRequestFromArgs(
				"execute_code",
				[]byte(tc.args),
			)
			if err != nil {
				t.Fatalf(
					"ScanRequestFromArgs() error = %v",
					err,
				)
			}

			if req.ToolName != "execute_code" {
				t.Errorf(
					"ToolName = %q, want %q",
					req.ToolName,
					"execute_code",
				)
			}

			if req.Backend != BackendCodeExec {
				t.Errorf(
					"Backend = %q, want %q",
					req.Backend,
					BackendCodeExec,
				)
			}

			if req.Command != tc.wantCommand {
				t.Errorf(
					"Command = %q, want %q",
					req.Command,
					tc.wantCommand,
				)
			}
		})
	}
}

func TestScannerCodeExecDistinguishesValidatedNonShellCode(
	t *testing.T,
) {
	policy := DefaultPolicy()
	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		args         string
		wantDecision Decision
		wantRule     string
	}{
		{
			name: "safe Python code",
			args: `{
				"code_blocks": {
					"language": "python",
					"code": "print('hello')"
				}
			}`,
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name: "safe Go code",
			args: `{
				"code_blocks": {
					"language": "go",
					"code": "package main\nfunc main() {}"
				}
			}`,
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name: "shell code still uses shell scanner",
			args: `{
				"code_blocks": {
					"language": "bash",
					"code": "cat .env"
				}
			}`,
			wantDecision: DecisionDeny,
			wantRule:     ruleSensitivePath,
		},
		{
			name: "Python dangerous delete still uses semantic rules",
			args: `{
				"code_blocks": {
					"language": "python",
					"code": "import os\nos.system('rm -rf /')"
				}
			}`,
			wantDecision: DecisionDeny,
			wantRule:     ruleDangerousDelete,
		},
		{
			name: "Python network request still uses semantic rules",
			args: `{
				"code_blocks": {
					"language": "python",
					"code": "import requests\nrequests.get('https://evil.example.net/data')"
				}
			}`,
			wantDecision: DecisionDeny,
			wantRule:     ruleNetwork,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ScanRequestFromArgs(
				"execute_code",
				[]byte(tc.args),
			)
			if err != nil {
				t.Fatalf(
					"ScanRequestFromArgs() error = %v",
					err,
				)
			}

			report, err := scanner.Scan(
				context.Background(),
				req,
			)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}
			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func TestScannerCodeExecRejectsDangerousNonShellCode(
	t *testing.T,
) {
	scanner := NewScanner(DefaultPolicy())

	tests := []struct {
		name     string
		code     string
		wantRule string
	}{
		{
			name:     "read dotenv",
			code:     `open(".env").read()`,
			wantRule: ruleSensitivePath,
		},
		{
			name:     "remove system password file",
			code:     `os.remove("/etc/passwd")`,
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "recursively delete root",
			code:     `shutil.rmtree("/")`,
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "subprocess dangerous deletion",
			code:     `subprocess.run(["rm", "-rf", "/"])`,
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "non allowlisted HTTP client",
			code:     `httpx.get("https://evil.example.net")`,
			wantRule: ruleNetwork,
		},
		{
			name:     "Python infinite loop",
			code:     `while True: pass`,
			wantRule: ruleInfiniteLoop,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{
				"code_blocks": []map[string]string{
					{
						"language": "python",
						"code":     tc.code,
					},
				},
			})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			req, err := ScanRequestFromArgs("execute_code", args)
			if err != nil {
				t.Fatalf(
					"ScanRequestFromArgs() error = %v",
					err,
				)
			}
			report, err := scanner.Scan(context.Background(), req)
			if err != nil {
				t.Fatalf("Scanner.Scan() error = %v", err)
			}
			if report.Decision != DecisionDeny {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionDeny,
					report,
				)
			}
			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func BenchmarkScannerFiveHundredCommands(b *testing.B) {
	p := DefaultPolicy()
	p.AllowedCommands = []string{"go", "echo", "wc"}
	scanner := NewScanner(p)
	commands := make([]ScanRequest, 500)
	for i := range commands {
		commands[i] = ScanRequest{
			ToolName: "workspace_exec",
			Command:  "go test ./tool/safety",
			Backend:  BackendWorkspaceExec,
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, req := range commands {
			if _, err := scanner.Scan(context.Background(), req); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestScanRequestFromArgsHostExec(t *testing.T) {
	tests := []struct {
		name           string
		args           string
		wantCommand    string
		wantWorkdir    string
		wantEnvValue   string
		wantTimeout    int
		wantYield      int
		wantBackground bool
		wantTTY        bool
		wantMaxOutput  int
	}{
		{
			name: "canonical fields",
			args: `{
				"command": "go test ./...",
				"workdir": "sub/dir",
				"env": {
					"SAFE_VAR": "1"
				},
				"yield_time_ms": 250,
				"timeout_sec": 45,
				"background": true,
				"tty": true,
				"max_output_bytes": 2048
			}`,
			wantCommand:    "go test ./...",
			wantWorkdir:    "sub/dir",
			wantEnvValue:   "1",
			wantTimeout:    45,
			wantYield:      250,
			wantBackground: true,
			wantTTY:        true,
			wantMaxOutput:  2048,
		},
		{
			name: "legacy aliases",
			args: `{
				"command": "echo hello",
				"yieldMs": 0,
				"timeoutSec": 12,
				"pty": true
			}`,
			wantCommand: "echo hello",
			wantTimeout: 12,
			wantTTY:     true,
		},
		{
			name: "canonical timeout wins over legacy alias",
			args: `{
				"command": "sleep 45",
				"yield_time_ms": 0,
				"yieldMs": 10000,
				"timeout_sec": 45,
				"timeoutSec": 900
			}`,
			wantCommand: "sleep 45",
			wantTimeout: 45,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ScanRequestFromArgs(
				"exec_command",
				[]byte(tc.args),
			)
			if err != nil {
				t.Fatalf(
					"ScanRequestFromArgs() error = %v",
					err,
				)
			}

			if req.ToolName != "exec_command" {
				t.Errorf(
					"ToolName = %q, want %q",
					req.ToolName,
					"exec_command",
				)
			}

			if req.Backend != BackendHostExec {
				t.Errorf(
					"Backend = %q, want %q",
					req.Backend,
					BackendHostExec,
				)
			}

			if req.Command != tc.wantCommand {
				t.Errorf(
					"Command = %q, want %q",
					req.Command,
					tc.wantCommand,
				)
			}

			if req.Cwd != tc.wantWorkdir {
				t.Errorf(
					"Cwd = %q, want %q",
					req.Cwd,
					tc.wantWorkdir,
				)
			}

			if req.Env["SAFE_VAR"] != tc.wantEnvValue {
				t.Errorf(
					"Env[SAFE_VAR] = %q, want %q",
					req.Env["SAFE_VAR"],
					tc.wantEnvValue,
				)
			}

			if req.TimeoutSec != tc.wantTimeout {
				t.Errorf(
					"TimeoutSec = %d, want %d",
					req.TimeoutSec,
					tc.wantTimeout,
				)
			}

			if req.YieldTimeMS != tc.wantYield {
				t.Errorf(
					"YieldTimeMS = %d, want %d",
					req.YieldTimeMS,
					tc.wantYield,
				)
			}

			if req.Background != tc.wantBackground {
				t.Errorf(
					"Background = %v, want %v",
					req.Background,
					tc.wantBackground,
				)
			}

			if req.TTY != tc.wantTTY {
				t.Errorf(
					"TTY = %v, want %v",
					req.TTY,
					tc.wantTTY,
				)
			}

			if req.MaxOutputBytes != tc.wantMaxOutput {
				t.Errorf(
					"MaxOutputBytes = %d, want %d",
					req.MaxOutputBytes,
					tc.wantMaxOutput,
				)
			}
		})
	}
}

func TestScanRequestFromArgsHostExecMatchesSessionExecutionDefaults(
	t *testing.T,
) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil
	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		args         string
		wantDecision Decision
		wantRule     string
	}{
		{
			name: "explicit zero yield stays foreground",
			args: `{
				"command": "echo ready",
				"yield_time_ms": 0,
				"timeout_sec": 300
			}`,
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name: "positive yield can return a running session",
			args: `{
				"command": "echo ready",
				"yield_time_ms": 100,
				"timeout_sec": 300
			}`,
			wantDecision: DecisionAsk,
			wantRule:     "SAFE-HOSTEXEC-SESSION",
		},
		{
			name: "omitted yield uses ten second execution default",
			args: `{
				"command": "echo ready",
				"timeout_sec": 300
			}`,
			wantDecision: DecisionAsk,
			wantRule:     "SAFE-HOSTEXEC-SESSION",
		},
		{
			name: "omitted timeout uses thirty minute execution default",
			args: `{
				"command": "echo ready",
				"yield_time_ms": 0
			}`,
			wantDecision: DecisionAsk,
			wantRule:     ruleTimeout,
		},
		{
			name: "canonical zero yield wins over legacy alias",
			args: `{
				"command": "echo ready",
				"yield_time_ms": 0,
				"yieldMs": 10000,
				"timeout_sec": 300
			}`,
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name: "canonical positive yield wins over legacy alias",
			args: `{
				"command": "echo ready",
				"yield_time_ms": 10000,
				"yieldMs": 0,
				"timeout_sec": 300
			}`,
			wantDecision: DecisionAsk,
			wantRule:     "SAFE-HOSTEXEC-SESSION",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := ScanRequestFromArgs(
				"exec_command",
				[]byte(tc.args),
			)
			if err != nil {
				t.Fatalf(
					"ScanRequestFromArgs() error = %v",
					err,
				)
			}

			report, err := scanner.Scan(context.Background(), req)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}
			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}
		})
	}
}

func TestScanRequestFromArgsHostExecRejectsInvalidArgs(
	t *testing.T,
) {
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "missing command",
			args:    `{}`,
			wantErr: "extract hostexec arguments: command is required",
		},
		{
			name:    "blank command",
			args:    `{"command":"   "}`,
			wantErr: "extract hostexec arguments: command is required",
		},
		{
			name:    "invalid JSON",
			args:    `{"command":`,
			wantErr: "extract hostexec arguments:",
		},
		{
			name: "invalid env type",
			args: `{
				"command": "echo hello",
				"env": ["PATH=/bin"]
			}`,
			wantErr: "extract hostexec arguments:",
		},
		{
			name: "invalid timeout type",
			args: `{
				"command": "echo hello",
				"timeout_sec": "60"
			}`,
			wantErr: "extract hostexec arguments:",
		},
		{
			name: "invalid yield type",
			args: `{
				"command": "echo hello",
				"yield_time_ms": "100"
			}`,
			wantErr: "extract hostexec arguments:",
		},
		{
			name: "negative output limit",
			args: `{
				"command": "echo hello",
				"max_output_bytes": -1
			}`,
			wantErr: "extract hostexec arguments: " +
				"max_output_bytes must not be negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ScanRequestFromArgs(
				"exec_command",
				[]byte(tc.args),
			)
			if err == nil {
				t.Fatal(
					"ScanRequestFromArgs() succeeded for invalid arguments",
				)
			}

			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf(
					"ScanRequestFromArgs() error = %q, want it to contain %q",
					err,
					tc.wantErr,
				)
			}
		})
	}
}

func TestScanRequestFromArgsCodeExecRejectsInvalidArgs(
	t *testing.T,
) {
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "missing code blocks",
			args:    `{}`,
			wantErr: "extract codeexec arguments: code_blocks is required",
		},
		{
			name: "empty code blocks",
			args: `{
				"code_blocks": []
			}`,
			wantErr: "extract codeexec arguments: code_blocks must contain at least one block",
		},
		{
			name: "missing code",
			args: `{
				"code_blocks": {
					"language": "bash"
				}
			}`,
			wantErr: "extract codeexec arguments: code_blocks[0].code is required",
		},
		{
			name: "blank code",
			args: `{
				"code_blocks": {
					"language": "bash",
					"code": "   "
				}
			}`,
			wantErr: "extract codeexec arguments: code_blocks[0].code is required",
		},
		{
			name:    "invalid JSON",
			args:    `{"code_blocks":`,
			wantErr: "extract codeexec arguments:",
		},
		{
			name: "invalid code blocks type",
			args: `{
				"code_blocks": 42
			}`,
			wantErr: "extract codeexec arguments: code_blocks must be an array, object, or JSON string",
		},
		{
			name: "invalid double encoded JSON",
			args: `{
				"code_blocks": "not valid JSON"
			}`,
			wantErr: "extract codeexec arguments:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ScanRequestFromArgs(
				"execute_code",
				[]byte(tc.args),
			)
			if err == nil {
				t.Fatal(
					"ScanRequestFromArgs() succeeded for invalid arguments",
				)
			}

			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf(
					"ScanRequestFromArgs() error = %q, want it to contain %q",
					err,
					tc.wantErr,
				)
			}
		})
	}
}

func TestScanRequestFromArgsUnknownToolUsesUnknownBackend(
	t *testing.T,
) {
	req, err := ScanRequestFromArgs(
		"custom_exec",
		[]byte(`{"arbitrary_field":"value"}`),
	)
	if err != nil {
		t.Fatalf(
			"ScanRequestFromArgs() error = %v",
			err,
		)
	}

	if req.ToolName != "custom_exec" {
		t.Errorf(
			"ToolName = %q, want %q",
			req.ToolName,
			"custom_exec",
		)
	}

	if req.Backend != BackendUnknown {
		t.Errorf(
			"Backend = %q, want %q",
			req.Backend,
			BackendUnknown,
		)
	}

	if req.Command != "" {
		t.Errorf(
			"Command = %q, want empty because the tool contract is unknown",
			req.Command,
		)
	}
}

func TestScannerEmptyCommandUsesParseFailureAction(
	t *testing.T,
) {
	scanner := NewScanner(DefaultPolicy())

	tests := []struct {
		name     string
		toolName string
		backend  Backend
		command  string
	}{
		{
			name:     "workspaceexec empty command",
			toolName: "workspace_exec",
			backend:  BackendWorkspaceExec,
			command:  "",
		},
		{
			name:     "hostexec blank command",
			toolName: "exec_command",
			backend:  BackendHostExec,
			command:  "   ",
		},
		{
			name:     "codeexec empty scannable code",
			toolName: "execute_code",
			backend:  BackendCodeExec,
			command:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: tc.toolName,
					Backend:  tc.backend,
					Command:  tc.command,
				},
			)
			if err != nil {
				t.Fatalf(
					"Scan() error = %v",
					err,
				)
			}

			if report.Decision != DecisionAsk {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionAsk,
					report,
				)
			}

			if report.RuleID != ruleParse {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					ruleParse,
					report,
				)
			}

			if !report.Blocked {
				t.Errorf(
					"Blocked = false, want true; report=%+v",
					report,
				)
			}

			if len(report.Evidence) == 0 {
				t.Errorf(
					"Evidence is empty; report=%+v",
					report,
				)
			}
		})
	}
}

func TestScannerDistinguishesBackgroundFlagFromShellOperator(
	t *testing.T,
) {
	scanner := NewScanner(DefaultPolicy())

	tests := []struct {
		name       string
		command    string
		background bool
		wantRule   string
	}{
		{
			name:       "tool background flag",
			command:    "sleep 10",
			background: true,
			wantRule:   ruleBackground,
		},
		{
			name:       "shell background operator",
			command:    "sleep 10 &",
			background: false,
			wantRule:   ruleParse,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName:   "exec_command",
					Backend:    BackendHostExec,
					Command:    tc.command,
					Background: tc.background,
				},
			)
			if err != nil {
				t.Fatalf(
					"Scan() error = %v",
					err,
				)
			}

			if report.Decision != DecisionAsk {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionAsk,
					report,
				)
			}

			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}

			if !report.Blocked {
				t.Errorf(
					"Blocked = false, want true; report=%+v",
					report,
				)
			}
		})
	}
}

func TestScannerPreservesShellsafeParseEvidence(
	t *testing.T,
) {
	scanner := NewScanner(DefaultPolicy())

	tests := []struct {
		name         string
		command      string
		wantEvidence string
	}{
		{
			name:         "command substitution",
			command:      "echo $(date)",
			wantEvidence: "command substitution",
		},
		{
			name:         "output redirection",
			command:      "echo hello > output.txt",
			wantEvidence: "output redirection",
		},
		{
			name:         "parameter expansion",
			command:      "echo $HOME",
			wantEvidence: "parameter expansion",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Backend:  BackendWorkspaceExec,
					Command:  tc.command,
				},
			)
			if err != nil {
				t.Fatalf(
					"Scan() error = %v",
					err,
				)
			}

			if report.Decision != DecisionAsk {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionAsk,
					report,
				)
			}

			if report.RuleID != ruleParse {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					ruleParse,
					report,
				)
			}

			evidence := strings.Join(
				report.Evidence,
				"\n",
			)

			if !strings.Contains(
				evidence,
				tc.wantEvidence,
			) {
				t.Errorf(
					"Evidence = %q, want it to contain %q",
					evidence,
					tc.wantEvidence,
				)
			}

			if report.Recommendation == "" {
				t.Errorf(
					"Recommendation is empty; report=%+v",
					report,
				)
			}
		})
	}
}

func TestScannerUsesConfiguredParseFailureAction(
	t *testing.T,
) {
	policy := DefaultPolicy()
	policy.ParseFailureAction = DecisionDeny

	scanner := NewScanner(policy)

	report, err := scanner.Scan(
		context.Background(),
		ScanRequest{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspaceExec,
			Command:  "echo $(date)",
		},
	)
	if err != nil {
		t.Fatalf(
			"Scan() error = %v",
			err,
		)
	}

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q; report=%+v",
			report.Decision,
			DecisionDeny,
			report,
		)
	}

	if report.RuleID != ruleParse {
		t.Errorf(
			"RuleID = %q, want %q; report=%+v",
			report.RuleID,
			ruleParse,
			report,
		)
	}

	if !report.Blocked {
		t.Errorf(
			"Blocked = false, want true; report=%+v",
			report,
		)
	}

	if len(report.Evidence) == 0 ||
		!strings.Contains(
			report.Evidence[0],
			"command substitution",
		) {
		t.Errorf(
			"Evidence does not preserve the parse reason: %+v",
			report.Evidence,
		)
	}
}

func TestLoadedPolicyChangesCommandPathAndDomainBehavior(
	t *testing.T,
) {
	path := filepath.Join(
		t.TempDir(),
		"tool_safety_policy.yaml",
	)

	content := []byte(`
allowed_commands:
  - echo
  - cat
  - curl
denied_commands:
  - rm
forbidden_paths:
  - private.txt
network_allowlist:
  - example.com
`)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf(
			"WriteFile() error = %v",
			err,
		)
	}

	policy, err := LoadPolicyFile(path)
	if err != nil {
		t.Fatalf(
			"LoadPolicyFile() error = %v",
			err,
		)
	}

	scanner := NewScanner(policy)

	tests := []struct {
		name         string
		command      string
		wantDecision Decision
		wantRule     string
	}{
		{
			name:         "configured command allowed",
			command:      "echo hello",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "command outside configured allowlist denied",
			command:      "git status",
			wantDecision: DecisionDeny,
			wantRule:     ruleCommandPolicy,
		},
		{
			name:         "configured forbidden path denied",
			command:      "cat private.txt",
			wantDecision: DecisionDeny,
			wantRule:     ruleSensitivePath,
		},
		{
			name:         "configured domain allowed",
			command:      "curl https://example.com/data",
			wantDecision: DecisionAllow,
			wantRule:     ruleAllow,
		},
		{
			name:         "domain outside configured allowlist denied",
			command:      "curl https://evil.example/data",
			wantDecision: DecisionDeny,
			wantRule:     ruleNetwork,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Backend:  BackendWorkspaceExec,
					Command:  tc.command,
				},
			)
			if err != nil {
				t.Fatalf(
					"Scan() error = %v",
					err,
				)
			}

			if report.Decision != tc.wantDecision {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					tc.wantDecision,
					report,
				)
			}

			if report.RuleID != tc.wantRule {
				t.Errorf(
					"RuleID = %q, want %q; report=%+v",
					report.RuleID,
					tc.wantRule,
					report,
				)
			}

			wantBlocked :=
				tc.wantDecision != DecisionAllow

			if report.Blocked != wantBlocked {
				t.Errorf(
					"Blocked = %v, want %v; report=%+v",
					report.Blocked,
					wantBlocked,
					report,
				)
			}
		})
	}
}

func TestScannerEnvSecretMarksReportRedacted(t *testing.T) {
	const secret = "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"

	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil
	policy.EnvAllowlist = []string{"PATH"}

	scanner := NewScanner(policy)

	report, err := scanner.Scan(
		context.Background(),
		ScanRequest{
			ToolName: "workspace_exec",
			Command:  "echo ready",
			Backend:  BackendWorkspaceExec,
			Env: map[string]string{
				"PATH": secret,
			},
		},
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if report.Decision != DecisionDeny {
		t.Errorf(
			"Decision = %q, want %q; report=%+v",
			report.Decision,
			DecisionDeny,
			report,
		)
	}

	if !report.Redacted {
		t.Errorf(
			"Redacted = false, want true; report=%+v",
			report,
		)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal(report) error = %v", err)
	}

	if strings.Contains(string(encoded), secret) {
		t.Errorf(
			"report contains secret %q: %s",
			secret,
			encoded,
		)
	}
}

func TestRedactingAfterToolCallbackRemovesSecretsFromToolOutput(
	t *testing.T,
) {
	secrets := []string{
		"abcdefghijklmnopqrstuvwxyz",
		"correct-horse-battery-staple",
		"sk-abcdefghijklmnop",
		syntheticAWSAccessKey("ABCDEFGHIJKLMNOP"),
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij",
		"github_pat_11AA22BB33CC44DD55EE66FF77GG88HH",
		"super-secret-private-key-material",
	}

	originalOutput := map[string]any{
		"stdout": "Authorization: Bearer " +
			secrets[0],
		"configuration": map[string]any{
			"password": "password=" + secrets[1],
			"api_key":  "api_key=" + secrets[2],
		},
		"credentials": []any{
			secrets[3],
			secrets[4],
			secrets[5],
		},
		"private_key": "-----BEGIN PRIVATE KEY-----\n" +
			secrets[6] + "\n" +
			"-----END PRIVATE KEY-----",
		"safe": "ordinary output",
	}

	callback := NewRedactingAfterToolCallback()

	result, err := callback(
		context.Background(),
		&tool.AfterToolArgs{
			Result: originalOutput,
		},
	)
	if err != nil {
		t.Fatalf("AfterTool callback error = %v", err)
	}
	if result == nil || result.CustomResult == nil {
		t.Fatal(
			"callback did not replace secret-bearing tool output",
		)
	}

	encoded, err := json.Marshal(result.CustomResult)
	if err != nil {
		t.Fatalf(
			"json.Marshal(CustomResult) error = %v",
			err,
		)
	}

	for _, secret := range secrets {
		if strings.Contains(
			string(encoded),
			secret,
		) {
			t.Errorf(
				"redacted tool output contains secret %q: %s",
				secret,
				encoded,
			)
		}
	}

	if !strings.Contains(
		string(encoded),
		"[REDACTED]",
	) {
		t.Errorf(
			"redacted tool output does not contain marker: %s",
			encoded,
		)
	}

	if !strings.Contains(
		string(encoded),
		"ordinary output",
	) {
		t.Errorf(
			"safe output was unexpectedly removed: %s",
			encoded,
		)
	}

	// RedactValue must not mutate the original tool result; the callback
	// returns a redacted copy through CustomResult.
	originalEncoded, err := json.Marshal(originalOutput)
	if err != nil {
		t.Fatalf(
			"json.Marshal(originalOutput) error = %v",
			err,
		)
	}

	if !strings.Contains(
		string(originalEncoded),
		secrets[4],
	) {
		t.Errorf(
			"original Tool output was unexpectedly mutated: %s",
			originalEncoded,
		)
	}
}

func syntheticAWSAccessKey(suffix string) string {
	return "AKIA" + suffix
}

func TestRedactValueUsesSecretFieldNames(t *testing.T) {
	const (
		passwordValue = "correct-horse-battery-staple"
		tokenValue    = "ordinary-access-token-value"
		apiKeyValue   = "ordinary-api-key-value"
	)
	original := map[string]any{
		"password":     passwordValue,
		"access_token": tokenValue,
		"nested": map[string]any{
			"apiKey": apiKeyValue,
		},
		"safe":            "ordinary output",
		"password_policy": "enabled",
		"token_budget":    100,
	}

	value, changed, err := RedactValue(original)
	if err != nil {
		t.Fatalf("RedactValue() error = %v", err)
	}
	if !changed {
		t.Fatal("RedactValue() changed = false, want true")
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(redacted value) error = %v", err)
	}
	for _, secret := range []string{
		passwordValue,
		tokenValue,
		apiKeyValue,
	} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf(
				"redacted value contains secret %q: %s",
				secret,
				encoded,
			)
		}
	}
	if count := strings.Count(
		string(encoded),
		"[REDACTED]",
	); count != 3 {
		t.Errorf(
			"redaction marker count = %d, want 3; value=%s",
			count,
			encoded,
		)
	}
	for _, safe := range []string{
		"ordinary output",
		"enabled",
		"password_policy",
		"token_budget",
	} {
		if !strings.Contains(string(encoded), safe) {
			t.Errorf(
				"safe value %q was removed: %s",
				safe,
				encoded,
			)
		}
	}

	originalEncoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(original) error = %v", err)
	}
	if !strings.Contains(string(originalEncoded), passwordValue) {
		t.Errorf(
			"RedactValue() mutated original value: %s",
			originalEncoded,
		)
	}
}

func TestRedactValueUsesSecretFieldNameVariants(t *testing.T) {
	secretFields := map[string]any{
		"db_password":           "database password value",
		"userPassword":          "user password value",
		"x-api-key":             "API key value",
		"secret_key":            "secret key value",
		"github_token":          "GitHub token value",
		"aws_secret_access_key": "AWS secret access key value",
	}
	original := map[string]any{
		"secrets":         secretFields,
		"password_policy": "enabled",
		"token_budget":    100,
		"safe":            "ordinary output",
	}

	value, changed, err := RedactValue(original)
	if err != nil {
		t.Fatalf("RedactValue() error = %v", err)
	}
	if !changed {
		t.Fatal("RedactValue() changed = false, want true")
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(redacted value) error = %v", err)
	}
	for key, secret := range secretFields {
		if strings.Contains(string(encoded), fmt.Sprint(secret)) {
			t.Errorf(
				"redacted field %q still contains secret: %s",
				key,
				encoded,
			)
		}
	}
	if count := strings.Count(
		string(encoded),
		"[REDACTED]",
	); count != len(secretFields) {
		t.Errorf(
			"redaction marker count = %d, want %d; value=%s",
			count,
			len(secretFields),
			encoded,
		)
	}
	for _, safe := range []string{
		"password_policy",
		"token_budget",
		"enabled",
		"ordinary output",
	} {
		if !strings.Contains(string(encoded), safe) {
			t.Errorf("safe value %q was removed: %s", safe, encoded)
		}
	}
}

func TestScannerOTelAttributesExcludeHighCardinalityData(
	t *testing.T,
) {
	const (
		commandSecret = "sk-abcdefghijklmnop"
		envSecret     = "ghp_" +
			"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij"
		testCwd = "/private/workspace/customer-project"
	)

	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(
		trace.WithSpanProcessor(recorder),
	)

	ctx, span := provider.Tracer(
		"safety-test",
	).Start(
		context.Background(),
		"scan-secret-command",
	)

	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"echo"}
	policy.DeniedCommands = nil
	policy.EnvAllowlist = []string{"PATH"}

	scanner := NewScanner(policy)

	report, err := scanner.Scan(
		ctx,
		ScanRequest{
			ToolName: "workspace_exec",
			Command: "echo api_key=" +
				commandSecret,
			Backend: BackendWorkspaceExec,
			Cwd:     testCwd,
			Env: map[string]string{
				"PATH": envSecret,
			},
			MaxOutputBytes: 1024,
		},
	)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf(
			"ended spans = %d, want 1",
			len(ended),
		)
	}

	attrs := make(map[string]string)
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] =
			attr.Value.AsString()
	}

	wantAttrs := map[string]string{
		OTelAttrDecision:  string(report.Decision),
		OTelAttrRiskLevel: string(report.RiskLevel),
		OTelAttrRuleID:    report.RuleID,
		OTelAttrBackend:   string(report.Backend),
	}

	if len(attrs) != len(wantAttrs) {
		t.Errorf(
			"attribute count = %d, want exactly %d; attrs=%+v",
			len(attrs),
			len(wantAttrs),
			attrs,
		)
	}

	for key, wantValue := range wantAttrs {
		if attrs[key] != wantValue {
			t.Errorf(
				"attribute %q = %q, want %q",
				key,
				attrs[key],
				wantValue,
			)
		}
	}

	for key, value := range attrs {
		for _, forbidden := range []string{
			commandSecret,
			envSecret,
			testCwd,
			"api_key",
		} {
			if strings.Contains(key, forbidden) ||
				strings.Contains(value, forbidden) {
				t.Errorf(
					"OTel attribute leaks %q: %q=%q",
					forbidden,
					key,
					value,
				)
			}
		}
	}
}
