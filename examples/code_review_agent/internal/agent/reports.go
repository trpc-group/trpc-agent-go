//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

type artifactPayload struct {
	Name string
	Data []byte
}

type reportBundle struct {
	JSON        []byte
	Markdown    []byte
	MarkdownZH  []byte
	Diagnostics []byte
}

type reviewResultContext struct {
	TaskID        string
	InputMetadata review.InputMetadata
	StartedAt     time.Time
	ToolCallCount int
	Decisions     []storage.DecisionRecord
	Runs          []storage.SandboxRunRecord
	Model         llm.RunSummary
	Plan          executionPlan
}

// finalizeReviewResult 补齐报告、落库和 telemetry 共用字段。
func finalizeReviewResult(result review.Result, ctx reviewResultContext) review.Result {
	result.TaskID = ctx.TaskID
	result.Created = time.Now()
	result.InputMetadata = ctx.InputMetadata
	result.Metrics.Mode = ctx.Plan.Mode
	result.Metrics.SandboxRequested = ctx.Plan.SandboxRequested
	result.Metrics.SandboxExecuted = sandboxExecutionStarted(ctx.Runs)
	result.Metrics.ModelRequested = ctx.Plan.ModelRequested
	result.Metrics.ModelExecuted = ctx.Model.CallCount > 0
	result.Metrics.TotalDurationMS = time.Since(ctx.StartedAt).Milliseconds()
	result.Metrics.ToolCallCount = ctx.ToolCallCount
	result.Metrics.SandboxDurationMS = totalSandboxDuration(ctx.Runs)
	result.Metrics.ModelCallCount = ctx.Model.CallCount
	result.Metrics.ModelDurationMS = ctx.Model.DurationMS
	result.Metrics.ModelFindingCount = ctx.Model.FindingCount
	result.Metrics.ModelExceptionCount = ctx.Model.ExceptionCount
	result.Metrics.ModelProvider = ctx.Model.Provider
	result.Metrics.ModelName = ctx.Model.Name
	result.Metrics.ModelBackend = ctx.Model.Backend
	result.Metrics.FindingCount = len(result.Findings)
	result.Metrics.RedactionCount = redactionCount(result.Findings, result.Warnings)
	result.Metrics.SeverityCounts = severityCounts(result.Findings, result.Warnings)
	if result.Metrics.ExceptionCounts == nil {
		result.Metrics.ExceptionCounts = map[string]int{}
	}
	delete(result.Metrics.ExceptionCounts, "sandbox_failed")
	for _, run := range ctx.Runs {
		if run.Status == "failed" || run.Status == "error" || run.Status == "timed_out" {
			incrementException(result.Metrics.ExceptionCounts, "sandbox_failed")
		}
	}
	result.Metrics.PermissionBlocks = permissionBlockCount(ctx.Decisions)
	result.HumanReviewItems = humanReviewItems(result.Warnings)
	result.GovernanceSummary = governanceSummary(ctx.Decisions, result.Metrics.PermissionBlocks)
	result.SandboxSummary = sandboxSummary(ctx.Runs)
	result.Artifacts = reportArtifacts()
	result.Summary = fmt.Sprintf("%d findings, %d warnings", len(result.Findings), len(result.Warnings))
	result.Conclusion = conclusion(result)
	return result
}

func sandboxExecutionStarted(runs []storage.SandboxRunRecord) bool {
	for _, run := range runs {
		if run.ExecutionStarted && run.Command != defaultSkillCommand {
			return true
		}
	}
	return false
}

// buildReportBundle 生成三份对外产物。
func buildReportBundle(result review.Result) (reportBundle, error) {
	jsonReport, err := report.BuildJSON(result)
	if err != nil {
		return reportBundle{}, err
	}
	md := report.BuildMarkdown(result)
	mdZH := report.BuildMarkdownChinese(result)
	diagnosticsReport, err := buildDiagnostics(result)
	if err != nil {
		return reportBundle{}, err
	}
	return reportBundle{
		JSON:        jsonReport,
		Markdown:    []byte(md),
		MarkdownZH:  []byte(mdZH),
		Diagnostics: diagnosticsReport,
	}, nil
}

// writeReviewArtifacts 同步写本地文件和官方 artifact service。
func (a *Agent) writeReviewArtifacts(ctx context.Context, taskID string, result review.Result, bundle reportBundle) error {
	payloads := bundle.payloads()
	if err := enforceArtifactLimits(a.cfg, payloads); err != nil {
		return err
	}
	if err := writeReports(a.cfg.OutputDir, bundle.JSON, bundle.Markdown, bundle.MarkdownZH, bundle.Diagnostics); err != nil {
		return err
	}
	if a.artifactService == nil {
		return nil
	}
	return a.saveArtifacts(ctx, taskID, result, payloads)
}

func (b reportBundle) payloads() []artifactPayload {
	return reportPayloads(b.JSON, b.Markdown, b.MarkdownZH, b.Diagnostics)
}

// writeReports 写入报告文件。
func writeReports(dir string, jsonReport, markdownReport, markdownChineseReport, diagnosticsReport []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "review_report.json"), jsonReport, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "review_report.md"), markdownReport, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "review_report.zh.md"), markdownChineseReport, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "review_diagnostics.json"), diagnosticsReport, 0o644)
}

// reportPayloads 返回待写入产物。
func reportPayloads(jsonReport, markdownReport, markdownChineseReport, diagnosticsReport []byte) []artifactPayload {
	return []artifactPayload{
		{Name: "review_report.json", Data: jsonReport},
		{Name: "review_report.md", Data: markdownReport},
		{Name: "review_report.zh.md", Data: markdownChineseReport},
		{Name: "review_diagnostics.json", Data: diagnosticsReport},
	}
}

// enforceArtifactLimits 阻止超大产物落盘或入库。
func enforceArtifactLimits(cfg Config, artifacts []artifactPayload) error {
	if cfg.MaxArtifactCount > 0 && len(artifacts) > cfg.MaxArtifactCount {
		return fmt.Errorf("artifact count limit exceeded: %d > %d", len(artifacts), cfg.MaxArtifactCount)
	}
	var total int64
	for _, artifact := range artifacts {
		if artifactMIMEType(artifact.Name) == "" || filepath.Base(artifact.Name) != artifact.Name {
			return fmt.Errorf("artifact name %q is not allowed", artifact.Name)
		}
		total += int64(len(artifact.Data))
	}
	if cfg.MaxArtifactTotalBytes > 0 && total > cfg.MaxArtifactTotalBytes {
		return fmt.Errorf("artifact total size limit exceeded: %d > %d", total, cfg.MaxArtifactTotalBytes)
	}
	for _, artifact := range artifacts {
		if int64(len(artifact.Data)) > cfg.MaxArtifactBytes {
			return fmt.Errorf("artifact %s exceeds size limit: %d > %d", artifact.Name, len(artifact.Data), cfg.MaxArtifactBytes)
		}
	}
	return nil
}

// buildDiagnostics 生成独立诊断产物。
func buildDiagnostics(result review.Result) ([]byte, error) {
	payload := struct {
		TaskID            string                   `json:"task_id"`
		Metrics           review.Metrics           `json:"metrics"`
		InputMetadata     review.InputMetadata     `json:"input_metadata,omitempty"`
		GovernanceSummary review.GovernanceSummary `json:"governance_summary"`
		SandboxSummary    review.SandboxSummary    `json:"sandbox_summary"`
		Artifacts         []review.ArtifactSummary `json:"artifacts"`
		Conclusion        review.Conclusion        `json:"conclusion"`
	}{
		TaskID:            result.TaskID,
		Metrics:           result.Metrics,
		InputMetadata:     result.InputMetadata,
		GovernanceSummary: result.GovernanceSummary,
		SandboxSummary:    result.SandboxSummary,
		Artifacts:         result.Artifacts,
		Conclusion:        result.Conclusion,
	}
	return json.MarshalIndent(payload, "", "  ")
}

// severityCounts 汇总严重级别。
func severityCounts(findings, warnings []review.Finding) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		out[f.Severity]++
	}
	for _, f := range warnings {
		out[f.Severity]++
	}
	return out
}

// redactionCount 统计脱敏次数。
func redactionCount(findings, warnings []review.Finding) int {
	total := 0
	for _, finding := range append(findings, warnings...) {
		if strings.Contains(finding.Evidence, "[REDACTED]") {
			total++
		}
	}
	return total
}

// humanReviewItems 提取人工复核项。
func humanReviewItems(warnings []review.Finding) []review.Finding {
	var out []review.Finding
	for _, warning := range warnings {
		if warning.Status == "needs_human_review" || warning.Status == "ask" {
			out = append(out, warning)
		}
	}
	return review.DedupeFindings(out)
}

// governanceSummary 生成治理摘要。
func governanceSummary(decisions []storage.DecisionRecord, blocks int) review.GovernanceSummary {
	out := review.GovernanceSummary{PermissionBlocks: blocks}
	for _, decision := range decisions {
		if decision.Command == "" && decision.Action == "" {
			continue
		}
		out.PermissionDecisions = append(out.PermissionDecisions, review.PermissionDecisionSummary{
			Command: decision.Command,
			Action:  decision.Action,
			Reason:  decision.Reason,
		})
	}
	return out
}

// permissionBlockCount 统计所有非 allow 治理决策。
func permissionBlockCount(decisions []storage.DecisionRecord) int {
	blocks := 0
	for _, decision := range decisions {
		if decision.Command == "" && decision.Action == "" {
			continue
		}
		if decision.Action != "allow" && decision.Action != "dry_run" {
			blocks++
		}
	}
	return blocks
}

// sandboxSummary 生成沙箱摘要。
func sandboxSummary(runs []storage.SandboxRunRecord) review.SandboxSummary {
	out := review.SandboxSummary{}
	for _, run := range runs {
		if run.Command == "" {
			continue
		}
		out.Runs = append(out.Runs, review.SandboxRunSummary{
			Command:          run.Command,
			Runtime:          run.Runtime,
			Status:           run.Status,
			TimeoutMS:        run.TimeoutMS,
			OutputLimitBytes: run.OutputLimitBytes,
			EnvWhitelist:     run.EnvWhitelist,
			ExitCode:         run.ExitCode,
			StdoutDigest:     run.StdoutDigest,
			StderrDigest:     run.StderrDigest,
			DurationMS:       run.DurationMS,
		})
	}
	return out
}

// conclusion 生成最终审查结论。
func conclusion(result review.Result) review.Conclusion {
	if hasBlockingFinding(result.Findings) {
		return review.Conclusion{
			Status:  "fail",
			Reason:  "blocking_findings",
			Summary: "Critical or high severity findings require changes before merge.",
		}
	}
	if len(result.HumanReviewItems) > 0 || hasSandboxException(result.Metrics.ExceptionCounts) {
		return review.Conclusion{
			Status:  "needs_human_review",
			Reason:  "review_required",
			Summary: "Manual review is required for governance or sandbox signals.",
		}
	}
	return review.Conclusion{
		Status:  "pass",
		Reason:  "no_blocking_findings",
		Summary: "No blocking findings were detected by the deterministic review chain.",
	}
}

func hasBlockingFinding(findings []review.Finding) bool {
	for _, finding := range findings {
		switch strings.ToLower(finding.Severity) {
		case "critical", "high":
			return true
		}
	}
	return false
}

func hasSandboxException(counts map[string]int) bool {
	for name, count := range counts {
		if count > 0 && strings.Contains(name, "sandbox") {
			return true
		}
	}
	return false
}

// reportArtifacts 声明报告产物。
func reportArtifacts() []review.ArtifactSummary {
	return []review.ArtifactSummary{
		{Name: "review_report.json", Kind: "report", Path: "review_report.json"},
		{Name: "review_report.md", Kind: "report", Path: "review_report.md"},
		{Name: "review_report.zh.md", Kind: "report", Path: "review_report.zh.md"},
		{Name: "review_diagnostics.json", Kind: "diagnostic", Path: "review_diagnostics.json"},
	}
}

// totalSandboxDuration 汇总沙箱耗时。
func totalSandboxDuration(runs []storage.SandboxRunRecord) int64 {
	var total int64
	for _, run := range runs {
		total += run.DurationMS
	}
	return total
}

// resultWithRunError 将执行错误转为复核项。
func resultWithRunError(result review.Result, err error) review.Result {
	if result.Metrics.ExceptionCounts == nil {
		result.Metrics.ExceptionCounts = map[string]int{}
	}
	incrementException(result.Metrics.ExceptionCounts, "skill_run")
	result.Warnings = append(result.Warnings, review.Finding{
		Severity:       "low",
		Category:       "sandbox",
		Title:          "Sandbox command failed",
		Evidence:       review.RedactSecrets(err.Error()),
		Recommendation: "Inspect sandbox stderr digest and rerun the command in an isolated workspace.",
		Confidence:     "high",
		Source:         "sandbox",
		RuleID:         "sandbox-command-failed",
		Status:         "needs_human_review",
	})
	return result
}

// incrementException 增加指定异常类型计数。
func incrementException(counts map[string]int, key string) {
	if counts == nil {
		return
	}
	counts[key]++
}
