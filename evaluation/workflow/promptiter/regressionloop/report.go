// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.

package regressionloop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func GenerateReport(ctx *PipelineContext) *OptimizationReport {
	return &OptimizationReport{
		RunMeta: RunMeta{
			StartTime:  time.Now(),
			EndTime:    time.Now(),
			DurationMS: 0,
			Mode:       ctx.Config.Mode,
			Seed:       ctx.Config.Seed,
			ConfigHash: "",
		},
		BaselineTrainScore:  getScore(ctx.BaselineTrain),
		BaselineValScore:    getScore(ctx.BaselineVal),
		CandidateTrainScore: getScore(ctx.CandidateTrain),
		CandidateValScore:   getScore(ctx.CandidateVal),
		ScoreDeltaTrain:     getScore(ctx.CandidateTrain) - getScore(ctx.BaselineTrain),
		ScoreDeltaVal:       getScore(ctx.CandidateVal) - getScore(ctx.BaselineVal),
		GateDecision:        *ctx.GateDecision,
		CaseDeltas:          ctx.CaseDeltas,
		AttributionSummary:  GetAttributionSummary(ctx.Attributions),
		Candidates:          ctx.Candidates,
		TotalCost:           ctx.TotalCost,
		TotalCalls:          ctx.TotalCalls,
		TotalLatencyMS:      ctx.TotalLatencyMS,
	}
}

func getScore(result *engine.EvaluationResult) float64 {
	if result == nil {
		return 0.0
	}
	return result.OverallScore
}

func WriteReports(report *OptimizationReport, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	jsonPath := filepath.Join(outputDir, "optimization_report.json")
	if err := writeJSONReport(report, jsonPath); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}

	mdPath := filepath.Join(outputDir, "optimization_report.md")
	if err := writeMarkdownReport(report, mdPath); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}

	return nil
}

func writeJSONReport(report *OptimizationReport, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func writeMarkdownReport(report *OptimizationReport, path string) error {
	const mdTemplate = `# Optimization Report

## Summary

{{- if eq .GateDecision.Result "accept"}}
✅ **Candidate Accepted**
{{- else}}
❌ **Candidate Rejected**
{{- end}}

**Mode:** {{.RunMeta.Mode}} | **Seed:** {{.RunMeta.Seed}} | **Duration:** {{.RunMeta.DurationMS}}ms

## Score Comparison

| Set | Baseline | Candidate | Delta |
|-----|----------|-----------|-------|
| Train | {{printf "%.4f" .BaselineTrainScore}} | {{printf "%.4f" .CandidateTrainScore}} | {{printf "%+.4f" .ScoreDeltaTrain}} |
| Validation | {{printf "%.4f" .BaselineValScore}} | {{printf "%.4f" .CandidateValScore}} | {{printf "%+.4f" .ScoreDeltaVal}} |

## Gate Decision

**Result:** {{.GateDecision.Result}} | **Stage:** {{.GateDecision.Stage}} | **Score Delta:** {{printf "%.4f" .GateDecision.ScoreDelta}}

{{- if .GateDecision.AcceptanceReasons}}

### Acceptance Reasons
{{- range .GateDecision.AcceptanceReasons}}
- {{.}}
{{- end}}
{{- end}}

{{- if .GateDecision.RejectionReasons}}

### Rejection Reasons
{{- range .GateDecision.RejectionReasons}}
- {{.}}
{{- end}}
{{- end}}

## Rule Results

| Rule | Passed | Threshold | Actual | Reason |
|------|--------|-----------|--------|--------|
{{- range .GateDecision.RuleResults}}
| {{.RuleType}} | {{.Passed}} | {{printf "%.4f" .Threshold}} | {{printf "%.4f" .ActualValue}} | {{.Reason}} |
{{- end}}

## Case Deltas

| Case ID | Delta Type | Baseline Score | Candidate Score | Delta |
|---------|------------|----------------|-----------------|-------|
{{- range .CaseDeltas}}
| {{.EvalCaseID}} | {{.DeltaType}} | {{printf "%.4f" .BaselineScore}} | {{printf "%.4f" .CandidateScore}} | {{printf "%+.4f" .ScoreDelta}} |
{{- end}}

## Attribution Summary

| Category | Count |
|----------|-------|
{{- range $key, $value := .AttributionSummary}}
| {{$key}} | {{$value}} |
{{- end}}

## Candidates

| Round | Validation Score | Accepted |
|-------|-----------------|----------|
{{- range .Candidates}}
| {{.Round}} | {{printf "%.4f" .ValidationScore}} | {{.Accepted}} |
{{- end}}

## Cost Summary

- **Total Cost:** {{printf "%.2f" .TotalCost}}
- **Total Calls:** {{.TotalCalls}}
- **Total Latency:** {{.TotalLatencyMS}}ms
`

	tmpl, err := template.New("report").Parse(mdTemplate)
	if err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return tmpl.Execute(file, report)
}

func SaveAuditTrail(ctx *PipelineContext, outputDir string) error {
	auditDir := filepath.Join(outputDir, "audit")
	if err := os.MkdirAll(auditDir, 0755); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}

	runMetaPath := filepath.Join(auditDir, "run_meta.json")
	runMeta := RunMeta{
		StartTime:  time.Now(),
		EndTime:    time.Now(),
		DurationMS: 0,
		Mode:       ctx.Config.Mode,
		Seed:       ctx.Config.Seed,
	}
	if hash, err := ctx.Config.Hash(); err == nil {
		runMeta.ConfigHash = hash
	}
	data, _ := json.MarshalIndent(runMeta, "", "  ")
	os.WriteFile(runMetaPath, data, 0644)

	gateDecisionPath := filepath.Join(auditDir, "gate_decision.json")
	if ctx.GateDecision != nil {
		data, _ := json.MarshalIndent(ctx.GateDecision, "", "  ")
		os.WriteFile(gateDecisionPath, data, 0644)
	}

	attributionsPath := filepath.Join(auditDir, "attributions.json")
	data, _ = json.MarshalIndent(ctx.Attributions, "", "  ")
	os.WriteFile(attributionsPath, data, 0644)

	return nil
}
