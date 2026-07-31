//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main scans tool execution samples without executing them.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

const (
	defaultPolicyPath  = "tool_safety_guard/tool_safety_policy.yaml"
	defaultSamplesPath = "tool_safety_guard/tool_safety_samples.json"
	defaultReportPath  = "tool_safety_guard/tool_safety_report.json"
	defaultAuditPath   = "tool_safety_guard/tool_safety_audit.jsonl"
)

type sample struct {
	Name             string           `json:"name"`
	Input            safety.ScanInput `json:"input"`
	ExpectedDecision safety.Decision  `json:"expected_decision"`
	ExpectedRuleID   safety.RuleID    `json:"expected_rule_id"`
}

type sampleReport struct {
	Name   string        `json:"name"`
	Report safety.Report `json:"report"`
}

func main() {
	policyPath := flag.String("policy", defaultPolicyPath, "Safety policy YAML or JSON path")
	samplesPath := flag.String("samples", defaultSamplesPath, "JSON scan samples path")
	reportPath := flag.String("report", defaultReportPath, "JSON report output path")
	auditPath := flag.String("audit", defaultAuditPath, "JSONL audit output path")
	flag.Parse()

	if err := run(context.Background(), *policyPath, *samplesPath, *reportPath, *auditPath); err != nil {
		log.Fatalf("tool safety example: %v", err)
	}
}

func run(
	ctx context.Context,
	policyPath string,
	samplesPath string,
	reportPath string,
	auditPath string,
) (err error) {
	policy, err := safety.LoadPolicyFile(policyPath)
	if err != nil {
		return err
	}
	samples, err := loadSamples(samplesPath)
	if err != nil {
		return err
	}
	audit, err := os.OpenFile(auditPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit output: %w", err)
	}
	defer func() {
		if closeErr := audit.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close audit output: %w", closeErr)
		}
	}()

	scanner, err := safety.NewScanner(policy, safety.WithAuditWriter(audit))
	if err != nil {
		return err
	}
	reports, err := scanSamples(ctx, scanner, samples)
	if err != nil {
		return err
	}
	if err := writeReports(reportPath, reports); err != nil {
		return err
	}
	fmt.Printf("\nScanned %d samples. No sample command was executed.\n", len(samples))
	return nil
}

func scanSamples(
	ctx context.Context,
	scanner *safety.Scanner,
	samples []sample,
) ([]sampleReport, error) {
	reports := make([]sampleReport, 0, len(samples))
	for _, item := range samples {
		report, err := scanner.Scan(ctx, item.Input)
		if err != nil {
			return nil, fmt.Errorf("scan %q: %w", item.Name, err)
		}
		if report.Decision != item.ExpectedDecision || report.RuleID != item.ExpectedRuleID {
			return nil, fmt.Errorf(
				"scan %q: got %s/%s, want %s/%s",
				item.Name,
				report.Decision,
				report.RuleID,
				item.ExpectedDecision,
				item.ExpectedRuleID,
			)
		}
		reports = append(reports, sampleReport{Name: item.Name, Report: report})
		fmt.Printf("%-30s %-5s %s\n", item.Name, report.Decision, report.RuleID)
	}
	return reports, nil
}

func loadSamples(path string) ([]sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open samples: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var samples []sample
	if err := dec.Decode(&samples); err != nil {
		return nil, fmt.Errorf("decode samples: %w", err)
	}
	return samples, nil
}

func writeReports(path string, reports []sampleReport) error {
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reports: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write reports: %w", err)
	}
	return nil
}
