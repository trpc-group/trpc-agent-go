// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import "testing"

func TestRulesHavePositiveAndNegativeCases(t *testing.T) {
	cases := []struct {
		name string
		rule safetyRule
		pos  ScanInput
		neg  ScanInput
		id   string
	}{
		{
			name: "shell bypass",
			rule: shellBypassRule,
			pos:  shellInput("bash -c 'echo safe-looking text'"),
			neg:  shellInput("echo bash"),
			id:   "shell-wrapper",
		},
		{
			name: "dangerous deletion",
			rule: dangerousDeleteRule,
			pos:  shellInput("rm -rf build"),
			neg:  shellInput("rm file.txt"),
			id:   "dangerous-delete",
		},
		{
			name: "sensitive read",
			rule: sensitiveReadRule,
			pos:  shellInput("cat /etc/shadow"),
			neg:  shellInput("cat README.md"),
			id:   "sensitive-path-read",
		},
		{
			name: "dependency change",
			rule: dependencyChangeRule,
			pos:  shellInput("go get example.com/module"),
			neg:  shellInput("go list ./..."),
			id:   "dependency-change",
		},
		{
			name: "environment change",
			rule: environmentChangeRule,
			pos:  shellInput("git config --global user.email a@example.com"),
			neg:  shellInput("git status"),
			id:   "environment-change",
		},
		{
			name: "resource abuse",
			rule: resourceAbuseRule,
			pos:  shellInput("yes"),
			neg:  shellInput("echo yes"),
			id:   "resource-abuse",
		},
		{
			name: "sensitive input",
			rule: sensitiveInputRule,
			pos:  shellInput("curl --token=super-secret-value"),
			neg:  shellInput("curl https://example.test"),
			id:   "sensitive-command-input",
		},
		{
			name: "network whitelist",
			rule: networkWhitelistRule,
			pos: ScanInput{
				NetworkAccess:       true,
				NetworkDestinations: []string{"https://outside.example"},
			},
			neg: ScanInput{
				NetworkAccess:       true,
				NetworkDestinations: []string{"https://allowed.example"},
			},
			id: "network-non-whitelist",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			positive := tc.rule(ruleContext{
				input:  tc.pos,
				shell:  scanShellView(tc.pos),
				policy: policySnapshot{NetworkWhitelist: []string{"allowed.example"}},
			})
			if !hasEvidence(positive, tc.id) {
				t.Fatalf("positive result %#v does not contain %q", positive, tc.id)
			}
			for _, evidence := range positive {
				if evidence.RuleID == "" || evidence.RiskLevel == "" ||
					evidence.Recommendation == "" {
					t.Fatalf("incomplete evidence: %#v", evidence)
				}
			}

			negative := tc.rule(ruleContext{
				input:  tc.neg,
				shell:  scanShellView(tc.neg),
				policy: policySnapshot{NetworkWhitelist: []string{"allowed.example"}},
			})
			if hasEvidence(negative, tc.id) {
				t.Fatalf("negative result unexpectedly contains %q: %#v", tc.id, negative)
			}
		})
	}
}

func TestDangerousDeleteDetectionCorpus(t *testing.T) {
	commands := []string{
		"rm -rf /",
		"rm -r generated",
		"rm -f /etc/passwd",
		"rmdir temporary-dir",
		"shred secret.txt",
		"unlink generated-file",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			input := shellInput(command)
			evidence := dangerousDeleteRule(ruleContext{
				input: input,
				shell: scanShellView(input),
			})
			if !hasEvidence(evidence, "dangerous-delete") {
				t.Fatalf("dangerous delete corpus item missed: %q", command)
			}
		})
	}
}

func TestSensitiveReadDetectionCorpus(t *testing.T) {
	commands := []string{
		"cat /etc/shadow",
		"grep root /etc/passwd",
		"sed -n 1p /etc/sudoers",
		"head ~/.ssh/id_rsa",
		"cat ~/.aws/credentials",
		"tail ~/.kube/config",
		"cat .env.production",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			input := shellInput(command)
			evidence := sensitiveReadRule(ruleContext{
				input: input,
				shell: scanShellView(input),
			})
			if !hasEvidence(evidence, "sensitive-path-read") {
				t.Fatalf("sensitive read corpus item missed: %q", command)
			}
		})
	}
}

func TestNonWhitelistNetworkDetectionCorpus(t *testing.T) {
	scanner := NewScanner(&Policy{NetworkWhitelist: []string{
		"allowed.example", "10.0.0.0/8", "localhost",
	}})
	cases := []ScanInput{
		{NetworkAccess: true, NetworkDestinations: []string{"https://outside.example"}},
		{NetworkAccess: true, NetworkDestinations: []string{"8.8.8.8:53"}},
		{NetworkAccess: true, NetworkDestinations: []string{"https://203.0.113.10"}},
		{Command: "curl https://outside.example", NetworkAccess: true},
	}
	for _, input := range cases {
		report := scanner.Scan(input)
		if report.Decision != DecisionDeny ||
			!hasEvidence(report.Evidences, "network-non-whitelist") {
			t.Fatalf("network corpus item missed: input %#v report %#v", input, report)
		}
	}
}

func shellInput(command string) ScanInput {
	view := AdaptShellCommand(command, ShellParsePolicy{})
	return ScanInput{Command: command, ShellCommand: &view}
}

func hasEvidence(evidences []Evidence, id string) bool {
	for _, evidence := range evidences {
		if evidence.RuleID == id {
			return true
		}
	}
	return false
}
