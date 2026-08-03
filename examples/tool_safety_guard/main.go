//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main scans deterministic command fixtures with the tool safety guard.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

const (
	reportSchemaVersion = "1.0"
	deterministicTime   = "2026-01-01T00:00:00Z"
)

type config struct {
	policyPath    string
	fixturesPath  string
	reportPath    string
	auditPath     string
	deterministic bool
}

type fixtureCase struct {
	ID               string                `json:"id"`
	Description      string                `json:"description,omitempty"`
	Requirement      string                `json:"requirement,omitempty"`
	Request          safety.Request        `json:"request"`
	ExpectedDecision tool.PermissionAction `json:"expected_decision"`
	ExpectedRuleIDs  []string              `json:"expected_rule_ids,omitempty"`
}

type caseResult struct {
	ID               string                `json:"id"`
	Description      string                `json:"description,omitempty"`
	Requirement      string                `json:"requirement,omitempty"`
	ExpectedDecision tool.PermissionAction `json:"expected_decision"`
	ExpectedRuleIDs  []string              `json:"expected_rule_ids,omitempty"`
	Passed           bool                  `json:"passed"`
	Failures         []string              `json:"failures,omitempty"`
	ScanError        string                `json:"scan_error,omitempty"`
	Report           safety.Report         `json:"report"`
}

type reportSummary struct {
	Total                  int             `json:"total"`
	Passed                 int             `json:"passed"`
	Failed                 int             `json:"failed"`
	Decisions              map[string]int  `json:"decisions"`
	RiskLevels             map[string]int  `json:"risk_levels"`
	HighRiskTotal          int             `json:"high_risk_total"`
	HighRiskDetected       int             `json:"high_risk_detected"`
	HighRiskDetectionRate  float64         `json:"high_risk_detection_rate_percent"`
	SafeTotal              int             `json:"safe_total"`
	SafeFalsePositives     int             `json:"safe_false_positives"`
	SafeFalsePositiveRate  float64         `json:"safe_false_positive_rate_percent"`
	CriticalCategoryChecks map[string]bool `json:"critical_category_checks"`
}

type outputReport struct {
	SchemaVersion string        `json:"schema_version"`
	GeneratedAt   string        `json:"generated_at"`
	Deterministic bool          `json:"deterministic"`
	PolicyID      string        `json:"policy_id"`
	PolicySHA256  string        `json:"policy_sha256"`
	PolicyFile    string        `json:"policy_file"`
	FixturesFile  string        `json:"fixtures_file"`
	DurationMS    int64         `json:"duration_ms"`
	Summary       reportSummary `json:"summary"`
	Results       []caseResult  `json:"results"`
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.policyPath, "policy", assetPath("tool_safety_policy.yaml"),
		"path to a strict YAML or JSON tool safety policy")
	flag.StringVar(&cfg.fixturesPath, "fixtures", assetPath("public_cases.json"),
		"path to the JSON fixture array")
	flag.StringVar(&cfg.reportPath, "report", assetPath("tool_safety_report.json"),
		"path for the structured scan report")
	flag.StringVar(&cfg.auditPath, "audit", assetPath("tool_safety_audit.jsonl"),
		"path for JSON Lines audit events")
	flag.BoolVar(&cfg.deterministic, "deterministic", false,
		"fix timestamps and durations for reproducible checked-in artifacts")
	flag.Parse()

	if err := run(context.Background(), cfg); err != nil {
		log.Fatalf("tool safety guard example failed: %v", err)
	}
}

func run(ctx context.Context, cfg config) error {
	if err := validateOutputPaths(cfg); err != nil {
		return err
	}
	policy, err := safety.LoadPolicyFile(cfg.policyPath)
	if err != nil {
		return err
	}
	fixtures, err := loadFixtures(cfg.fixturesPath)
	if err != nil {
		return err
	}
	auditFile, err := createOutput(cfg.auditPath)
	if err != nil {
		return fmt.Errorf("create audit output: %w", err)
	}
	auditClosed := false
	defer func() {
		if !auditClosed {
			_ = auditFile.Close()
		}
	}()

	jsonlSink := safety.NewJSONLAuditSink(auditFile)
	var auditSink safety.AuditSink = jsonlSink
	if cfg.deterministic {
		fixed, parseErr := time.Parse(time.RFC3339, deterministicTime)
		if parseErr != nil {
			return parseErr
		}
		auditSink = safety.AuditSinkFunc(func(ctx context.Context, event safety.AuditEvent) error {
			event.Timestamp = fixed
			event.DurationMS = 0
			return jsonlSink.WriteAudit(ctx, event)
		})
	}
	guard, err := safety.New(policy, safety.WithAuditSink(auditSink))
	if err != nil {
		return fmt.Errorf("create safety guard: %w", err)
	}

	started := time.Now()
	results := make([]caseResult, 0, len(fixtures))
	for _, fixture := range fixtures {
		report, scanErr := guard.Scan(ctx, fixture.Request)
		if cfg.deterministic {
			report.DurationMS = 0
		}
		result := evaluateFixture(fixture, report, scanErr)
		results = append(results, result)
	}
	if err := auditFile.Close(); err != nil {
		return fmt.Errorf("close audit output: %w", err)
	}
	auditClosed = true

	duration := time.Since(started).Milliseconds()
	generatedAt := time.Now().UTC().Format(time.RFC3339)
	if cfg.deterministic {
		duration = 0
		generatedAt = deterministicTime
	}
	policyHash, err := hashFile(cfg.policyPath)
	if err != nil {
		return fmt.Errorf("hash policy: %w", err)
	}
	out := outputReport{
		SchemaVersion: reportSchemaVersion,
		GeneratedAt:   generatedAt,
		Deterministic: cfg.deterministic,
		PolicyID:      policy.PolicyID,
		PolicySHA256:  policyHash,
		PolicyFile:    filepath.Base(cfg.policyPath),
		FixturesFile:  filepath.Base(cfg.fixturesPath),
		DurationMS:    duration,
		Summary:       summarize(results),
		Results:       results,
	}
	if err := writeJSON(cfg.reportPath, out); err != nil {
		return err
	}
	if out.Summary.Failed != 0 {
		return fmt.Errorf("%d fixture expectations failed; inspect %s",
			out.Summary.Failed, cfg.reportPath)
	}
	fmt.Printf("scanned %d fixtures: %d passed, report=%s audit=%s\n",
		out.Summary.Total, out.Summary.Passed, cfg.reportPath, cfg.auditPath)
	return nil
}

func assetPath(name string) string {
	if _, err := os.Stat(name); err == nil {
		return name
	}
	return filepath.Join("tool_safety_guard", name)
}

func validateOutputPaths(cfg config) error {
	paths := map[string]string{
		"policy":   cfg.policyPath,
		"fixtures": cfg.fixturesPath,
		"report":   cfg.reportPath,
		"audit":    cfg.auditPath,
	}
	for name, path := range paths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s path is empty", name)
		}
	}
	inputs := []string{cfg.policyPath, cfg.fixturesPath}
	for name, path := range map[string]string{
		"report": cfg.reportPath,
		"audit":  cfg.auditPath,
	} {
		for _, input := range inputs {
			if pathsAlias(path, input) {
				return fmt.Errorf("%s output must not overwrite an input file", name)
			}
		}
	}
	if pathsAlias(cfg.reportPath, cfg.auditPath) {
		return errors.New("report and audit outputs must be different files")
	}
	return nil
}

func pathsAlias(left, right string) bool {
	leftPath, leftErr := filepath.Abs(left)
	if leftErr != nil {
		leftPath = filepath.Clean(left)
	}
	rightPath, rightErr := filepath.Abs(right)
	if rightErr != nil {
		rightPath = filepath.Clean(right)
	}
	if leftPath == rightPath {
		return true
	}
	leftInfo, leftErr := os.Stat(leftPath)
	rightInfo, rightErr := os.Stat(rightPath)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func loadFixtures(path string) ([]fixtureCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fixtures: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var fixtures []fixtureCase
	if err := decoder.Decode(&fixtures); err != nil {
		return nil, fmt.Errorf("decode fixtures: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode fixtures: trailing JSON value")
		}
		return nil, fmt.Errorf("decode fixtures: %w", err)
	}
	if len(fixtures) == 0 {
		return nil, errors.New("fixtures are empty")
	}
	seen := make(map[string]struct{}, len(fixtures))
	for index, fixture := range fixtures {
		if strings.TrimSpace(fixture.ID) == "" {
			return nil, fmt.Errorf("fixture %d has an empty id", index)
		}
		if _, exists := seen[fixture.ID]; exists {
			return nil, fmt.Errorf("duplicate fixture id %q", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		if strings.TrimSpace(fixture.Request.ToolName) == "" {
			return nil, fmt.Errorf("fixture %q has an empty tool_name", fixture.ID)
		}
		if fixture.Request.Backend == "" {
			return nil, fmt.Errorf("fixture %q has an empty backend", fixture.ID)
		}
		switch fixture.ExpectedDecision {
		case tool.PermissionActionAllow, tool.PermissionActionDeny, tool.PermissionActionAsk:
		default:
			return nil, fmt.Errorf("fixture %q has invalid expected_decision %q",
				fixture.ID, fixture.ExpectedDecision)
		}
	}
	return fixtures, nil
}

func evaluateFixture(fixture fixtureCase, report safety.Report, scanErr error) caseResult {
	result := caseResult{
		ID:               fixture.ID,
		Description:      fixture.Description,
		Requirement:      fixture.Requirement,
		ExpectedDecision: fixture.ExpectedDecision,
		ExpectedRuleIDs:  append([]string(nil), fixture.ExpectedRuleIDs...),
		Report:           report,
	}
	if scanErr != nil {
		clean, _ := safety.NewRedactor().RedactString(scanErr.Error())
		result.ScanError = clean
		result.Failures = append(result.Failures, "scan returned an error")
	}
	if report.Decision != fixture.ExpectedDecision {
		result.Failures = append(result.Failures, fmt.Sprintf(
			"decision mismatch: expected %s, got %s",
			fixture.ExpectedDecision, report.Decision,
		))
	}
	for _, ruleID := range fixture.ExpectedRuleIDs {
		if !reportHasRule(report, ruleID) {
			result.Failures = append(result.Failures,
				fmt.Sprintf("expected rule %s was not reported", ruleID))
		}
	}
	missing := missingRequiredReportFields(report)
	if len(missing) != 0 {
		result.Failures = append(result.Failures,
			"missing report fields: "+strings.Join(missing, ", "))
	}
	result.Passed = len(result.Failures) == 0
	return result
}

func reportHasRule(report safety.Report, ruleID string) bool {
	if report.RuleID == ruleID {
		return true
	}
	for _, match := range report.Matches {
		if match.RuleID == ruleID {
			return true
		}
	}
	return false
}

func missingRequiredReportFields(report safety.Report) []string {
	missing := make([]string, 0, 7)
	if report.Decision == "" {
		missing = append(missing, "decision")
	}
	if report.RiskLevel == "" {
		missing = append(missing, "risk_level")
	}
	if strings.TrimSpace(report.RuleID) == "" {
		missing = append(missing, "rule_id")
	}
	if strings.TrimSpace(report.Evidence) == "" {
		missing = append(missing, "evidence")
	}
	if strings.TrimSpace(report.Recommendation) == "" {
		missing = append(missing, "recommendation")
	}
	if strings.TrimSpace(report.ToolName) == "" {
		missing = append(missing, "tool_name")
	}
	if report.Backend == "" {
		missing = append(missing, "backend")
	}
	return missing
}

func summarize(results []caseResult) reportSummary {
	summary := reportSummary{
		Total:      len(results),
		Decisions:  make(map[string]int),
		RiskLevels: make(map[string]int),
		CriticalCategoryChecks: map[string]bool{
			"credential.access":  false,
			"destructive.delete": false,
			"network.denied":     false,
		},
	}
	for _, result := range results {
		if result.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.Decisions[string(result.Report.Decision)]++
		summary.RiskLevels[string(result.Report.RiskLevel)]++
		switch result.ExpectedDecision {
		case tool.PermissionActionDeny:
			summary.HighRiskTotal++
			if result.Report.Decision != tool.PermissionActionAllow {
				summary.HighRiskDetected++
			}
		case tool.PermissionActionAllow:
			summary.SafeTotal++
			if result.Report.Decision != tool.PermissionActionAllow {
				summary.SafeFalsePositives++
			}
		}
		for ruleID := range summary.CriticalCategoryChecks {
			if reportHasRule(result.Report, ruleID) &&
				result.Report.Decision == tool.PermissionActionDeny {
				summary.CriticalCategoryChecks[ruleID] = true
			}
		}
	}
	summary.HighRiskDetectionRate = percentage(
		summary.HighRiskDetected, summary.HighRiskTotal,
	)
	summary.SafeFalsePositiveRate = percentage(
		summary.SafeFalsePositives, summary.SafeTotal,
	)
	return summary
}

func percentage(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) * 100 / float64(denominator)
}

func createOutput(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
}

func writeJSON(path string, value any) error {
	file, err := createOutput(path)
	if err != nil {
		return fmt.Errorf("create report output: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("encode report: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close report output: %w", closeErr)
	}
	return nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	digest := sha256.Sum256(normalized)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
