//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
}

func assertErrorContains(t *testing.T, err error, wanted string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), wanted) {
		t.Fatalf("error = %v, want substring %q", err, wanted)
	}
}

func assertFalse(t *testing.T, value bool) {
	t.Helper()
	if value {
		t.Fatal("value = true, want false")
	}
}

func assertNotEmpty(t *testing.T, value any) {
	t.Helper()
	switch typed := value.(type) {
	case string:
		if typed == "" {
			t.Fatal("string is empty")
		}
	case []string:
		if len(typed) == 0 {
			t.Fatal("slice is empty")
		}
	default:
		t.Fatalf("unsupported emptiness assertion type %T", value)
	}
}

func assertContains(t *testing.T, value, wanted string) {
	t.Helper()
	if !strings.Contains(value, wanted) {
		t.Fatalf("%q does not contain %q", value, wanted)
	}
}

func assertNotContains(t *testing.T, value, unwanted string) {
	t.Helper()
	if strings.Contains(value, unwanted) {
		t.Fatalf("%q contains %q", value, unwanted)
	}
}

func assertElementsMatch(t *testing.T, wanted, got []string) {
	t.Helper()
	counts := make(map[string]int, len(wanted))
	for _, value := range wanted {
		counts[value]++
	}
	for _, value := range got {
		counts[value]--
	}
	for value, count := range counts {
		if count != 0 {
			t.Fatalf("element %q count delta = %d; want=%v got=%v", value, count, wanted, got)
		}
	}
}

func assertEqual(t *testing.T, wanted, got any) {
	t.Helper()
	if !reflect.DeepEqual(wanted, got) {
		t.Fatalf("got %#v, want %#v", got, wanted)
	}
}

func assertZero(t *testing.T, value any) {
	t.Helper()
	if !reflect.ValueOf(value).IsZero() {
		t.Fatalf("value = %#v, want zero", value)
	}
}

func TestLoadFixturesRejectsMalformedCorpora(t *testing.T) {
	valid := `[{
		"id":"one",
		"request":{
			"tool_name":"workspace_exec",
			"backend":"workspace",
			"command":"go test ./...",
			"timeout_ms":1000,
			"max_output_bytes":1024
		},
		"expected_decision":"allow"
	}]`
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed", content: "{"},
		{name: "unknown field", content: strings.Replace(valid, `"id":"one"`, `"id":"one","unknown":true`, 1)},
		{name: "trailing value", content: valid + ` {}`},
		{name: "empty array", content: `[]`},
		{name: "empty id", content: strings.Replace(valid, `"id":"one"`, `"id":" "`, 1)},
		{name: "duplicate id", content: `[
			{"id":"same","request":{"tool_name":"workspace_exec","backend":"workspace"},"expected_decision":"ask"},
			{"id":"same","request":{"tool_name":"workspace_exec","backend":"workspace"},"expected_decision":"ask"}
		]`},
		{name: "empty tool", content: strings.Replace(valid, `"tool_name":"workspace_exec"`, `"tool_name":" "`, 1)},
		{name: "empty backend", content: strings.Replace(valid, `"backend":"workspace"`, `"backend":""`, 1)},
		{name: "invalid decision", content: strings.Replace(valid, `"expected_decision":"allow"`, `"expected_decision":"invalid"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixtures.json")
			assertNoError(t, os.WriteFile(path, []byte(test.content), 0o600))
			_, err := loadFixtures(path)
			assertError(t, err)
		})
	}

	_, err := loadFixtures(filepath.Join(t.TempDir(), "missing.json"))
	assertErrorContains(t, err, "open fixtures")
}

func TestEvaluateFixtureReportsEveryFailureWithoutSecrets(t *testing.T) {
	fixture := fixtureCase{
		ID:               "failure",
		ExpectedDecision: tool.PermissionActionDeny,
		ExpectedRuleIDs:  []string{"credential.access"},
	}
	result := evaluateFixture(
		fixture,
		safety.Report{Decision: tool.PermissionActionAllow},
		errors.New("token=evaluation-secret"),
	)

	assertFalse(t, result.Passed)
	assertNotEmpty(t, result.Failures)
	assertNotContains(t, result.ScanError, "evaluation-secret")
	assertContains(t, result.ScanError, safety.RedactedValue)
	assertContains(t, strings.Join(result.Failures, "\n"), "decision mismatch")
	assertContains(t, strings.Join(result.Failures, "\n"), "expected rule")
	assertElementsMatch(t, []string{
		"risk_level", "rule_id", "evidence", "recommendation", "tool_name", "backend",
	}, missingRequiredReportFields(result.Report))

}

func TestRunErrorPathsAndNonDeterministicMode(t *testing.T) {
	base := config{
		policyPath:   "tool_safety_policy.yaml",
		fixturesPath: "public_cases.json",
	}
	t.Run("invalid outputs", func(t *testing.T) {
		cfg := base
		cfg.reportPath = cfg.policyPath
		cfg.auditPath = filepath.Join(t.TempDir(), "audit.jsonl")
		assertError(t, run(context.Background(), cfg))
	})
	t.Run("missing policy", func(t *testing.T) {
		cfg := base
		cfg.policyPath = filepath.Join(t.TempDir(), "missing.yaml")
		cfg.reportPath = filepath.Join(t.TempDir(), "report.json")
		cfg.auditPath = filepath.Join(t.TempDir(), "audit.jsonl")
		assertError(t, run(context.Background(), cfg))
	})
	t.Run("missing fixtures", func(t *testing.T) {
		cfg := base
		cfg.fixturesPath = filepath.Join(t.TempDir(), "missing.json")
		cfg.reportPath = filepath.Join(t.TempDir(), "report.json")
		cfg.auditPath = filepath.Join(t.TempDir(), "audit.jsonl")
		assertError(t, run(context.Background(), cfg))
	})
	t.Run("audit is directory", func(t *testing.T) {
		dir := t.TempDir()
		auditDir := filepath.Join(dir, "audit")
		assertNoError(t, os.Mkdir(auditDir, 0o755))
		cfg := base
		cfg.reportPath = filepath.Join(dir, "report.json")
		cfg.auditPath = auditDir
		assertErrorContains(t, run(context.Background(), cfg), "create audit output")
	})
	t.Run("report is directory", func(t *testing.T) {
		dir := t.TempDir()
		reportDir := filepath.Join(dir, "report")
		assertNoError(t, os.Mkdir(reportDir, 0o755))
		cfg := base
		cfg.reportPath = reportDir
		cfg.auditPath = filepath.Join(dir, "audit.jsonl")
		assertErrorContains(t, run(context.Background(), cfg), "create report output")
	})
	t.Run("expectation failure still writes report", func(t *testing.T) {
		dir := t.TempDir()
		fixturePath := filepath.Join(dir, "failing.json")
		fixture := []fixtureCase{{
			ID: "wrong-expectation",
			Request: safety.Request{
				ToolName:       "workspace_exec",
				Backend:        safety.BackendWorkspace,
				Command:        "go test ./tool/safety",
				TimeoutMS:      1000,
				MaxOutputBytes: 1024,
			},
			ExpectedDecision: tool.PermissionActionDeny,
		}}
		encoded, err := json.Marshal(fixture)
		assertNoError(t, err)
		assertNoError(t, os.WriteFile(fixturePath, encoded, 0o600))
		cfg := base
		cfg.fixturesPath = fixturePath
		cfg.reportPath = filepath.Join(dir, "report.json")
		cfg.auditPath = filepath.Join(dir, "audit.jsonl")
		assertErrorContains(t, run(context.Background(), cfg), "fixture expectations failed")
		var report outputReport
		data, err := os.ReadFile(cfg.reportPath)
		assertNoError(t, err)
		assertNoError(t, json.Unmarshal(data, &report))
		assertEqual(t, 1, report.Summary.Failed)
	})
	t.Run("non deterministic success", func(t *testing.T) {
		dir := t.TempDir()
		cfg := base
		cfg.reportPath = filepath.Join(dir, "report.json")
		cfg.auditPath = filepath.Join(dir, "audit.jsonl")
		assertNoError(t, run(context.Background(), cfg))
		var report outputReport
		data, err := os.ReadFile(cfg.reportPath)
		assertNoError(t, err)
		assertNoError(t, json.Unmarshal(data, &report))
		assertFalse(t, report.Deterministic)
		assertNotEmpty(t, report.GeneratedAt)
	})
}

func TestFileHelpersAndAssetPath(t *testing.T) {
	assertEqual(t, "tool_safety_policy.yaml", assetPath("tool_safety_policy.yaml"))
	missing := "asset-that-does-not-exist.json"
	assertEqual(t, filepath.Join("tool_safety_guard", missing), assetPath(missing))
	assertZero(t, percentage(1, 0))

	dir := t.TempDir()
	lf := filepath.Join(dir, "lf.yaml")
	crlf := filepath.Join(dir, "crlf.yaml")
	assertNoError(t, os.WriteFile(lf, []byte("version: v1\npolicy_id: same\n"), 0o600))
	assertNoError(t, os.WriteFile(crlf, []byte("version: v1\r\npolicy_id: same\r\n"), 0o600))
	lfHash, err := hashFile(lf)
	assertNoError(t, err)
	crlfHash, err := hashFile(crlf)
	assertNoError(t, err)
	assertEqual(t, lfHash, crlfHash)
	_, err = hashFile(filepath.Join(dir, "missing"))
	assertError(t, err)

	assertErrorContains(t, writeJSON(filepath.Join(dir, "bad.json"), func() {}), "encode report")
	parentFile := filepath.Join(dir, "parent")
	assertNoError(t, os.WriteFile(parentFile, []byte("file"), 0o600))
	_, err = createOutput(filepath.Join(parentFile, "child.json"))
	assertError(t, err)
}

func TestCheckedInDeterministicArtifactsMatchGenerator(t *testing.T) {
	dir := t.TempDir()
	cfg := config{
		policyPath:    "tool_safety_policy.yaml",
		fixturesPath:  "public_cases.json",
		reportPath:    filepath.Join(dir, "tool_safety_report.json"),
		auditPath:     filepath.Join(dir, "tool_safety_audit.jsonl"),
		deterministic: true,
	}
	assertNoError(t, run(context.Background(), cfg))

	generatedReport, err := os.ReadFile(cfg.reportPath)
	assertNoError(t, err)
	checkedReport, err := os.ReadFile("tool_safety_report.json")
	assertNoError(t, err)
	var generated outputReport
	var checked outputReport
	assertNoError(t, json.Unmarshal(generatedReport, &generated))
	assertNoError(t, json.Unmarshal(checkedReport, &checked))
	assertEqual(t, generated, checked)

	generatedAudit, err := os.ReadFile(cfg.auditPath)
	assertNoError(t, err)
	checkedAudit, err := os.ReadFile("tool_safety_audit.jsonl")
	assertNoError(t, err)
	normalize := func(value []byte) string {
		return strings.ReplaceAll(string(value), "\r\n", "\n")
	}
	assertEqual(t, normalize(generatedAudit), normalize(checkedAudit))
}
