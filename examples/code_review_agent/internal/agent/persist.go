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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

// persist 保存审计和报告数据。
func (a *Agent) persist(ctx context.Context, task storage.Task, result review.Result, decisions []storage.DecisionRecord, runs []storage.SandboxRunRecord, jsonReport, markdownReport, markdownChineseReport, diagnosticsReport []byte) error {
	now := time.Now()
	mode := result.Metrics.Mode
	sandboxRequested := result.Metrics.SandboxRequested
	sandboxExecuted := result.Metrics.SandboxExecuted
	modelRequested := result.Metrics.ModelRequested
	modelExecuted := result.Metrics.ModelExecuted
	record := storage.ReviewRecord{Task: task}
	// 收集权限决策。
	for _, decision := range decisions {
		if decision.Command == "" && decision.Action == "" {
			continue
		}
		record.Decisions = append(record.Decisions, decision)
	}
	if result.Metrics.RedactionCount > 0 {
		record.FilterDecisions = append(record.FilterDecisions, storage.FilterDecisionRecord{
			TaskID: task.ID,
			Target: "finding.evidence",
			Action: "redact",
			Reason: "secret pattern",
			At:     now,
		})
	}
	// 收集沙箱摘要。
	for _, run := range runs {
		if run.Command == "" && run.Status == "" {
			continue
		}
		if run.FinishedAt.IsZero() {
			run.FinishedAt = now
		}
		run.ArtifactCount = len(result.Artifacts)
		record.SandboxRuns = append(record.SandboxRuns, run)
	}
	record.Findings = persistedReviewItems(result)
	record.Metrics = storage.MetricsRecord{
		TaskID: task.ID, Mode: &mode,
		SandboxRequested:     &sandboxRequested,
		SandboxExecuted:      &sandboxExecuted,
		ModelRequested:       &modelRequested,
		ModelExecuted:        &modelExecuted,
		TotalDurationMS:      result.Metrics.TotalDurationMS,
		SandboxDurationMS:    result.Metrics.SandboxDurationMS,
		ModelDurationMS:      result.Metrics.ModelDurationMS,
		ToolCallCount:        result.Metrics.ToolCallCount,
		ModelCallCount:       result.Metrics.ModelCallCount,
		ModelProvider:        result.Metrics.ModelProvider,
		ModelName:            result.Metrics.ModelName,
		ModelBackend:         result.Metrics.ModelBackend,
		PermissionBlockCount: result.Metrics.PermissionBlocks,
		FindingCount:         result.Metrics.FindingCount,
		ModelFindingCount:    result.Metrics.ModelFindingCount,
		ModelExceptionCount:  result.Metrics.ModelExceptionCount,
		SeverityCountsJSON:   string(review.MustJSON(result.Metrics.SeverityCounts)),
		ExceptionCountsJSON:  string(review.MustJSON(result.Metrics.ExceptionCounts)),
		RedactionCount:       result.Metrics.RedactionCount,
		At:                   now,
	}
	// 收集产物引用。
	for _, artifact := range result.Artifacts {
		digest := artifact.Digest
		var size int64
		if artifact.Name == "review_report.json" {
			digest = digestBytes(jsonReport)
			size = int64(len(jsonReport))
		}
		if artifact.Name == "review_report.md" {
			digest = digestBytes(markdownReport)
			size = int64(len(markdownReport))
		}
		if artifact.Name == "review_report.zh.md" {
			digest = digestBytes(markdownChineseReport)
			size = int64(len(markdownChineseReport))
		}
		if artifact.Name == "review_diagnostics.json" {
			digest = digestBytes(diagnosticsReport)
			size = int64(len(diagnosticsReport))
		}
		record.Artifacts = append(record.Artifacts, storage.ArtifactRecord{
			TaskID: task.ID,
			Name:   artifact.Name,
			Kind:   artifact.Kind,
			Path:   artifact.Path,
			Digest: digest,
			Size:   size,
			At:     now,
		})
	}
	record.Report = storage.ReportRecord{JSON: jsonReport, Markdown: markdownReport, CreatedAt: now}
	return a.store.SaveReview(ctx, record)
}

// persistedReviewItems 返回需要落库的审查项。
func persistedReviewItems(result review.Result) []review.Finding {
	// 用 status 区分 finding、warning 和复核项。
	items := make([]review.Finding, 0, len(result.Findings)+len(result.Warnings)+len(result.HumanReviewItems))
	items = append(items, result.Findings...)
	items = append(items, result.Warnings...)
	items = append(items, result.HumanReviewItems...)
	return review.DedupeFindings(items)
}
