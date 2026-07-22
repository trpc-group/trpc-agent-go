//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

const exampleSecret = "sk-example-secret-must-never-appear"

func TestPublicCasesAndGeneratedArtifacts(t *testing.T) {
	temp := t.TempDir()
	cfg := config{
		policyPath:    "tool_safety_policy.yaml",
		fixturesPath:  "public_cases.json",
		reportPath:    filepath.Join(temp, "tool_safety_report.json"),
		auditPath:     filepath.Join(temp, "tool_safety_audit.jsonl"),
		deterministic: true,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("run public fixtures: %v", err)
	}
	reportBytes, err := os.ReadFile(cfg.reportPath)
	if err != nil {
		t.Fatalf("read generated report: %v", err)
	}
	if strings.Contains(string(reportBytes), exampleSecret) {
		t.Fatal("generated report contains the fixture secret")
	}
	var report outputReport
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode generated report: %v", err)
	}
	if report.Summary.Total < 12 {
		t.Fatalf("public fixture count = %d, want at least 12", report.Summary.Total)
	}
	if report.Summary.Failed != 0 || report.Summary.Passed != report.Summary.Total {
		t.Fatalf("public fixture summary = %+v", report.Summary)
	}
	if report.GeneratedAt != deterministicTime || report.DurationMS != 0 {
		t.Fatalf("deterministic metadata changed: time=%q duration=%d",
			report.GeneratedAt, report.DurationMS)
	}
	if report.Summary.HighRiskDetectionRate < 90 {
		t.Fatalf("high-risk detection rate = %.2f%%, want >= 90%%",
			report.Summary.HighRiskDetectionRate)
	}
	if report.Summary.SafeFalsePositiveRate > 10 {
		t.Fatalf("safe false-positive rate = %.2f%%, want <= 10%%",
			report.Summary.SafeFalsePositiveRate)
	}
	for category, detected := range report.Summary.CriticalCategoryChecks {
		if !detected {
			t.Errorf("critical category %s was not detected", category)
		}
	}

	auditBytes, err := os.ReadFile(cfg.auditPath)
	if err != nil {
		t.Fatalf("read generated audit: %v", err)
	}
	if strings.Contains(string(auditBytes), exampleSecret) {
		t.Fatal("generated audit contains the fixture secret")
	}
	assertAuditLines(t, auditBytes, report.Summary.Total)
}

func TestRequiredPublicCoverage(t *testing.T) {
	fixtures, err := loadFixtures("public_cases.json")
	if err != nil {
		t.Fatalf("load public fixtures: %v", err)
	}
	required := map[string]bool{
		"safe go test":            false,
		"dangerous deletion":      false,
		"credential read":         false,
		"non-allowlisted network": false,
		"allowlisted network":     false,
		"shell wrapper bypass":    false,
		"pipeline command":        false,
		"dependency install":      false,
		"long-running command":    false,
		"oversized output":        false,
		"hostexec long session":   false,
		"ask human review":        false,
	}
	for _, fixture := range fixtures {
		if _, ok := required[fixture.Requirement]; ok {
			required[fixture.Requirement] = true
		}
	}
	for requirement, found := range required {
		if !found {
			t.Errorf("required public case %q is missing", requirement)
		}
	}
}

func TestCorpusQualityGates(t *testing.T) {
	policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	guard, err := safety.New(policy)
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	safe := loadCorpus(t, filepath.Join("testdata", "safe_corpus.json"))
	dangerous := loadCorpus(t, filepath.Join("testdata", "dangerous_corpus.json"))

	falsePositives := 0
	for _, fixture := range safe {
		report, scanErr := guard.Scan(context.Background(), fixture.Request)
		if scanErr != nil {
			t.Fatalf("scan safe case %s: %v", fixture.ID, scanErr)
		}
		if report.Decision != tool.PermissionActionAllow {
			falsePositives++
		}
	}
	detected := 0
	for _, fixture := range dangerous {
		report, scanErr := guard.Scan(context.Background(), fixture.Request)
		if scanErr != nil {
			t.Fatalf("scan dangerous case %s: %v", fixture.ID, scanErr)
		}
		if report.Decision != tool.PermissionActionAllow {
			detected++
		}
	}
	detectionRate := percentage(detected, len(dangerous))
	falsePositiveRate := percentage(falsePositives, len(safe))
	if detectionRate < 90 {
		t.Fatalf("dangerous detection rate = %.2f%%, want >= 90%%", detectionRate)
	}
	if falsePositiveRate > 10 {
		t.Fatalf("safe false-positive rate = %.2f%%, want <= 10%%", falsePositiveRate)
	}
}

func TestPolicyChangesNetworkAllowlistWithoutCode(t *testing.T) {
	original, err := os.ReadFile("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("read example policy: %v", err)
	}
	changed := strings.Replace(string(original), "- go.dev", "- example.com", 1)
	if changed == string(original) {
		t.Fatal("example policy no longer contains the expected go.dev entry")
	}
	policyPath := filepath.Join(t.TempDir(), "changed-policy.yaml")
	if err := os.WriteFile(policyPath, []byte(changed), 0o600); err != nil {
		t.Fatalf("write changed policy: %v", err)
	}
	policy, err := safety.LoadPolicyFile(policyPath)
	if err != nil {
		t.Fatalf("load changed policy: %v", err)
	}
	guard, err := safety.New(policy)
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}

	goDev := networkRequest("curl https://go.dev/doc/")
	goReport, err := guard.Scan(context.Background(), goDev)
	if err != nil {
		t.Fatalf("scan removed domain: %v", err)
	}
	if goReport.Decision != tool.PermissionActionDeny ||
		!reportHasRule(goReport, "network.denied") {
		t.Fatalf("removed domain report = %+v, want network deny", goReport)
	}
	example := networkRequest("curl https://example.com/")
	exampleReport, err := guard.Scan(context.Background(), example)
	if err != nil {
		t.Fatalf("scan added domain: %v", err)
	}
	if exampleReport.Decision != tool.PermissionActionAllow {
		t.Fatalf("added domain decision = %s, want allow", exampleReport.Decision)
	}
}

func TestScan500SamplesUnderOneSecond(t *testing.T) {
	policy, err := safety.LoadPolicyFile("tool_safety_policy.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	guard, err := safety.New(policy)
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}
	fixtures, err := loadFixtures("public_cases.json")
	if err != nil {
		t.Fatalf("load fixtures: %v", err)
	}
	started := time.Now()
	for index := 0; index < 500; index++ {
		request := fixtures[index%len(fixtures)].Request
		if _, err := guard.Scan(context.Background(), request); err != nil {
			t.Fatalf("scan sample %d: %v", index, err)
		}
	}
	elapsed := time.Since(started)
	if elapsed >= time.Second {
		t.Fatalf("500 scans took %s, want < 1s", elapsed)
	}
}

func TestValidateOutputPathsProtectsInputs(t *testing.T) {
	cfg := config{
		policyPath:   "policy.yaml",
		fixturesPath: "fixtures.json",
		reportPath:   "policy.yaml",
		auditPath:    "audit.jsonl",
	}
	if err := validateOutputPaths(cfg); err == nil {
		t.Fatal("validateOutputPaths allowed report to overwrite policy")
	}
	cfg.reportPath = "report.json"
	cfg.auditPath = "report.json"
	if err := validateOutputPaths(cfg); err == nil {
		t.Fatal("validateOutputPaths allowed report and audit to share a path")
	}
}

func loadCorpus(t *testing.T, path string) []fixtureCase {
	t.Helper()
	fixtures, err := loadFixtures(path)
	if err != nil {
		t.Fatalf("load corpus %s: %v", path, err)
	}
	return fixtures
}

func networkRequest(command string) safety.Request {
	return safety.Request{
		ToolName:       "workspace_exec",
		Backend:        safety.BackendWorkspace,
		Command:        command,
		TimeoutMS:      20_000,
		MaxOutputBytes: 8_192,
	}
}

func assertAuditLines(t *testing.T, data []byte, expected int) {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	count := 0
	for scanner.Scan() {
		count++
		var event safety.AuditEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("decode audit line %d: %v", count, err)
		}
		if event.Decision == "" || event.RiskLevel == "" || event.RuleID == "" {
			t.Errorf("audit line %d missing decision fields: %+v", count, event)
		}
		if event.Timestamp.Format(time.RFC3339) != deterministicTime {
			t.Errorf("audit line %d timestamp = %s, want %s",
				count, event.Timestamp.Format(time.RFC3339), deterministicTime)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan audit lines: %v", err)
	}
	if count != expected {
		t.Fatalf("audit line count = %d, want %d", count, expected)
	}
}
