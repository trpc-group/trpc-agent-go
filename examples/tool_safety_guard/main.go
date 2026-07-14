// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
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
	policy, e := LoadPolicy(*policyPath)
	must(e)
	data, e := os.ReadFile(*samplesPath)
	must(e)
	var samples []Sample
	must(json.Unmarshal(data, &samples))
	audit, e := os.Create(*auditPath)
	must(e)
	defer audit.Close()
	writer := bufio.NewWriter(audit)
	defer writer.Flush()
	report := SafetyReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Policy:      *policyPath,
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
		must(e)
		_, e = writer.Write(append(line, '\n'))
		must(e)
	}
	out, e := json.MarshalIndent(report, "", "  ")
	must(e)
	must(os.WriteFile(*reportPath, append(out, '\n'), 0o644))
	fmt.Printf("samples=%d expected=%d duration audited\n", report.Samples, report.MatchedExpected)
}
func must(e error) {
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
