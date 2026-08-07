//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

func TestFixtureSchemaIsStrict(t *testing.T) {
	valid := `{"version":1,"fixtures":{"sample":{"description":"sample","diff":"diff","expected":{"finding_rule_ids":[],"warning_rule_ids":[]}}}}`
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown field", data: strings.Replace(valid, `"diff":"diff"`, `"diff":"diff","unknown":true`, 1), want: "unknown field"},
		{name: "trailing value", data: valid + `{}`, want: "trailing"},
		{name: "missing expected", data: `{"version":1,"fixtures":{"sample":{"description":"sample","diff":"diff"}}}`, want: "missing expected"},
		{name: "missing expected field", data: strings.Replace(valid, `,"warning_rule_ids":[]`, ``, 1), want: "missing warning_rule_ids"},
		{name: "empty rule id", data: strings.Replace(valid, `"finding_rule_ids":[]`, `"finding_rule_ids":[""]`, 1), want: "empty or padded"},
		{name: "duplicate rule id", data: strings.Replace(valid, `"finding_rule_ids":[]`, `"finding_rule_ids":["a","a"]`, 1), want: "duplicate rule id"},
		{name: "negative duration", data: strings.Replace(valid, `"expected":`, `"fake_sandbox":{"test":{"duration_ms":-1}},"expected":`, 1), want: "duration_ms"},
		{name: "invalid exit code", data: strings.Replace(valid, `"expected":`, `"fake_sandbox":{"test":{"exit_code":-2}},"expected":`, 1), want: "exit_code"},
		{name: "skipped timeout", data: strings.Replace(valid, `"expected":`, `"fake_sandbox":{"test":{"skipped":true,"timed_out":true}},"expected":`, 1), want: "both skipped and timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFixtures([]byte(tt.data))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parse fixtures err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFakeSandboxUsesDefaultsAndDeclaredExactValues(t *testing.T) {
	defaultRunner := newFakeSandboxRunner(nil)
	for _, kind := range []commandKind{
		commandCheckGoVersion,
		commandCheckGoTest,
		commandCheckGoVet,
		commandCheckStaticcheck,
	} {
		run := defaultRunner.RunSandboxCommand(nil, commandSpec{Kind: kind})
		if run.ExitCode != 0 || run.DurationMS != 1 || run.Runtime != runtimeFake ||
			run.Command != string(kind) {
			t.Fatalf("default %s run = %+v", kind, run)
		}
		if kind == commandCheckGoVersion && run.Stdout != "go version fake" {
			t.Fatalf("go version default = %+v", run)
		}
		if kind != commandCheckGoVersion && run.Stdout != "ok" {
			t.Fatalf("command default = %+v", run)
		}
	}

	fixture := fixtureItem{FakeSandbox: fixtureSandboxConfig{
		Test: &fixtureSandboxRun{ExitCode: 3, Stderr: "failed"},
	}}
	runner := newFakeSandboxRunner(&fixture)
	declared := runner.RunSandboxCommand(nil, commandSpec{Kind: commandCheckGoTest})
	if declared.ExitCode != 3 || declared.Stderr != "failed" || declared.DurationMS != 0 ||
		declared.Stdout != "" {
		t.Fatalf("declared run = %+v", declared)
	}
	missing := runner.RunSandboxCommand(nil, commandSpec{Kind: commandCheckGoVet})
	if missing.ExitCode != 0 || missing.DurationMS != 1 || missing.Stdout != "ok" {
		t.Fatalf("missing command default = %+v", missing)
	}
}

func TestPublicFixturesDriveExpectedRuleIDs(t *testing.T) {
	data, err := os.ReadFile(fixturesPath())
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := parseFixtures(data)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(fixtures.Fixtures))
	for name := range fixtures.Fixtures {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fixture := fixtures.Fixtures[name]
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runForTest(t, []string{"--fixture", name, "--dry-run"}, nil, nil)
			if code != 0 {
				t.Fatalf("exit code = %d; stderr: %s", code, stderr)
			}
			var summary reviewSummary
			if err := json.Unmarshal([]byte(stdout), &summary); err != nil {
				t.Fatalf("unmarshal summary: %v\n%s", err, stdout)
			}
			assertStringSlice(t, summary.FindingRuleIDs, *fixture.Expected.FindingRuleIDs)
			assertStringSlice(t, summary.WarningRuleIDs, *fixture.Expected.WarningRuleIDs)
		})
	}
}
