// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

type Sample struct {
	Name             string  `json:"name"`
	ExpectedDecision string  `json:"expected_decision"`
	Request          Request `json:"request"`
}
type SafetyReport struct {
	GeneratedAt     string       `json:"generated_at"`
	Policy          string       `json:"policy"`
	Samples         int          `json:"samples"`
	MatchedExpected int          `json:"matched_expected"`
	Results         []ScanResult `json:"results"`
}

func main() {
	policyPath := flag.String("policy", "tool_safety_policy.json", "policy JSON")
	samplesPath := flag.String("samples", "samples.json", "sample requests JSON")
	reportPath := flag.String("report", "tool_safety_report.json", "report JSON")
	auditPath := flag.String("audit", "tool_safety_audit.jsonl", "audit JSONL")
	flag.Parse()
	if e := run(*policyPath, *samplesPath, *reportPath, *auditPath); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func run(policyPath, samplesPath, reportPath, auditPath string) error {
	policy, e := LoadPolicy(policyPath)
	if e != nil {
		return e
	}
	data, e := os.ReadFile(samplesPath)
	if e != nil {
		return e
	}
	var samples []Sample
	if e = json.Unmarshal(data, &samples); e != nil {
		return e
	}
	audit, e := os.Create(auditPath)
	if e != nil {
		return e
	}
	writer := bufio.NewWriter(audit)
	report := SafetyReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Policy:      policyPath,
		Samples:     len(samples),
	}
	guard := NewGuard(policy)
	for _, sample := range samples {
		result := guard.Scan(sample.Request)
		report.Results = append(report.Results, result)
		if result.Decision == sample.ExpectedDecision {
			report.MatchedExpected++
		}
		event := AuditEvent{
			Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
			ToolName:       sample.Request.ToolName,
			Decision:       result.Decision,
			RiskLevel:      result.RiskLevel,
			RuleID:         result.RuleID,
			Backend:        sample.Request.Backend,
			DurationMicros: result.DurationMicros,
			Redacted:       result.Redacted,
			Blocked:        result.Blocked,
		}
		line, e := json.Marshal(event)
		if e != nil {
			return errors.Join(e, flushAndCloseAudit(writer, audit))
		}
		if _, e = writer.Write(append(line, '\n')); e != nil {
			return errors.Join(e, flushAndCloseAudit(writer, audit))
		}
	}
	if e = flushAndCloseAudit(writer, audit); e != nil {
		return e
	}
	out, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(reportPath, append(out, '\n'), 0o644); e != nil {
		return e
	}
	fmt.Printf("samples=%d expected=%d duration audited\n", report.Samples, report.MatchedExpected)
	if report.MatchedExpected != report.Samples {
		return fmt.Errorf("sample expectations matched %d of %d requests", report.MatchedExpected, report.Samples)
	}
	return nil
}

func flushAndCloseAudit(writer *bufio.Writer, closer io.Closer) error {
	return errors.Join(writer.Flush(), closer.Close())
}
