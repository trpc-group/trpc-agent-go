//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package report 生成 JSON 和 Markdown 报告。
package report

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// BuildJSON 生成 JSON 报告。
func BuildJSON(result review.Result) ([]byte, error) {
	result.Findings = review.DedupeFindings(result.Findings)
	result.HumanReviewItems = humanReviewItems(result)
	return json.MarshalIndent(result, "", "  ")
}

// BuildMarkdown 生成 Markdown 报告。
func BuildMarkdown(result review.Result) string {
	findings := review.DedupeFindings(result.Findings)
	var b strings.Builder
	b.WriteString("# Review Report\n\n")
	if result.Summary != "" {
		b.WriteString(result.Summary)
		b.WriteString("\n\n")
	}
	writeConclusion(&b, result.Conclusion)
	fmt.Fprintf(&b, "Capabilities: mode=%s sandbox=requested:%t/executed:%t model=requested:%t/executed:%t\n\n",
		result.Metrics.Mode, result.Metrics.SandboxRequested, result.Metrics.SandboxExecuted,
		result.Metrics.ModelRequested, result.Metrics.ModelExecuted)
	if result.Metrics.FindingCount > 0 || result.Metrics.TotalDurationMS > 0 {
		fmt.Fprintf(&b, "Metrics: findings=%d total_ms=%d sandbox_ms=%d model_ms=%d tool_calls=%d model_calls=%d model_findings=%d model_exceptions=%d permission_blocks=%d redactions=%d\n\n",
			result.Metrics.FindingCount,
			result.Metrics.TotalDurationMS,
			result.Metrics.SandboxDurationMS,
			result.Metrics.ModelDurationMS,
			result.Metrics.ToolCallCount,
			result.Metrics.ModelCallCount,
			result.Metrics.ModelFindingCount,
			result.Metrics.ModelExceptionCount,
			result.Metrics.PermissionBlocks,
			result.Metrics.RedactionCount,
		)
	}
	if result.Metrics.ModelProvider != "" || result.Metrics.ModelName != "" || result.Metrics.ModelBackend != "" {
		fmt.Fprintf(&b, "Model: provider=%s name=%s backend=%s\n\n",
			result.Metrics.ModelProvider,
			result.Metrics.ModelName,
			result.Metrics.ModelBackend,
		)
	}
	if len(result.Metrics.SeverityCounts) > 0 {
		b.WriteString("Severity Counts:\n")
		for severity, count := range result.Metrics.SeverityCounts {
			fmt.Fprintf(&b, "- %s: %d\n", severity, count)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Findings: %d\n\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "- [%s] %s:%d %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Title)
		if f.Evidence != "" {
			fmt.Fprintf(&b, "  - Evidence: %s\n", f.Evidence)
		}
		if f.Recommendation != "" {
			fmt.Fprintf(&b, "  - Recommendation: %s\n", f.Recommendation)
		}
	}
	writeHumanReview(&b, humanReviewItems(result))
	writeGovernance(&b, result.GovernanceSummary)
	writeSandbox(&b, result.SandboxSummary)
	writeArtifacts(&b, result.Artifacts)
	return b.String()
}

// BuildMarkdownChinese 生成中文 Markdown 报告。
func BuildMarkdownChinese(result review.Result) string {
	findings := review.DedupeFindings(result.Findings)
	var b strings.Builder
	b.WriteString("# 代码审查报告\n\n")
	if result.Summary != "" {
		b.WriteString(result.Summary)
		b.WriteString("\n\n")
	}
	writeConclusionChinese(&b, result.Conclusion)
	fmt.Fprintf(&b, "能力: mode=%s sandbox=requested:%t/executed:%t model=requested:%t/executed:%t\n\n",
		result.Metrics.Mode, result.Metrics.SandboxRequested, result.Metrics.SandboxExecuted,
		result.Metrics.ModelRequested, result.Metrics.ModelExecuted)
	if result.Metrics.FindingCount > 0 || result.Metrics.TotalDurationMS > 0 {
		fmt.Fprintf(&b, "指标: findings=%d total_ms=%d sandbox_ms=%d model_ms=%d tool_calls=%d model_calls=%d model_findings=%d model_exceptions=%d permission_blocks=%d redactions=%d\n\n",
			result.Metrics.FindingCount,
			result.Metrics.TotalDurationMS,
			result.Metrics.SandboxDurationMS,
			result.Metrics.ModelDurationMS,
			result.Metrics.ToolCallCount,
			result.Metrics.ModelCallCount,
			result.Metrics.ModelFindingCount,
			result.Metrics.ModelExceptionCount,
			result.Metrics.PermissionBlocks,
			result.Metrics.RedactionCount,
		)
	}
	if result.Metrics.ModelProvider != "" || result.Metrics.ModelName != "" || result.Metrics.ModelBackend != "" {
		fmt.Fprintf(&b, "模型: provider=%s name=%s backend=%s\n\n",
			result.Metrics.ModelProvider,
			result.Metrics.ModelName,
			result.Metrics.ModelBackend,
		)
	}
	if len(result.Metrics.SeverityCounts) > 0 {
		b.WriteString("严重级别统计:\n")
		for severity, count := range result.Metrics.SeverityCounts {
			fmt.Fprintf(&b, "- %s: %d\n", severity, count)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "审查发现: %d\n\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(&b, "- [%s] %s:%d %s\n", strings.ToUpper(f.Severity), f.File, f.Line, f.Title)
		writeFindingMetadataChinese(&b, f)
		writeLocalizedRuleTextChinese(&b, f)
		if f.Evidence != "" {
			fmt.Fprintf(&b, "  - 证据: %s\n", f.Evidence)
		}
		if f.Recommendation != "" {
			fmt.Fprintf(&b, "  - 修复建议: %s\n", f.Recommendation)
		}
	}
	writeHumanReviewChinese(&b, humanReviewItems(result))
	writeGovernanceChinese(&b, result.GovernanceSummary)
	writeSandboxChinese(&b, result.SandboxSummary)
	writeArtifactsChinese(&b, result.Artifacts)
	return b.String()
}

// humanReviewItems 汇总人工复核项。
func humanReviewItems(result review.Result) []review.Finding {
	items := append([]review.Finding(nil), result.HumanReviewItems...)
	for _, warning := range result.Warnings {
		if warning.Status == "needs_human_review" || warning.Status == "ask" {
			items = append(items, warning)
		}
	}
	return review.DedupeFindings(items)
}

func writeFindingMetadataChinese(b *strings.Builder, f review.Finding) {
	var parts []string
	if f.Source != "" {
		parts = append(parts, "来源: "+f.Source)
	}
	if f.RuleID != "" {
		parts = append(parts, "规则: "+f.RuleID)
	}
	if f.Category != "" {
		parts = append(parts, "类别: "+f.Category)
	}
	if f.Confidence != "" {
		parts = append(parts, "置信度: "+f.Confidence)
	}
	if f.Status != "" {
		parts = append(parts, "状态: "+f.Status)
	}
	if len(parts) > 0 {
		fmt.Fprintf(b, "  - %s\n", strings.Join(parts, "；"))
	}
}

// writeConclusion 渲染最终结论。
func writeConclusion(b *strings.Builder, conclusion review.Conclusion) {
	if conclusion.Status == "" {
		return
	}
	b.WriteString("## Conclusion\n\n")
	fmt.Fprintf(b, "- Status: %s\n", conclusion.Status)
	if conclusion.Reason != "" {
		fmt.Fprintf(b, "- Reason: %s\n", conclusion.Reason)
	}
	if conclusion.Summary != "" {
		fmt.Fprintf(b, "- Summary: %s\n", conclusion.Summary)
	}
	b.WriteString("\n")
}

// writeConclusionChinese 渲染中文最终结论。
func writeConclusionChinese(b *strings.Builder, conclusion review.Conclusion) {
	if conclusion.Status == "" {
		return
	}
	b.WriteString("## 最终结论\n\n")
	fmt.Fprintf(b, "- 状态: %s\n", conclusion.Status)
	if conclusion.Reason != "" {
		fmt.Fprintf(b, "- 原因: %s\n", conclusion.Reason)
	}
	if conclusion.Summary != "" {
		fmt.Fprintf(b, "- 摘要: %s\n", conclusion.Summary)
	}
	b.WriteString("\n")
}

// writeHumanReview 渲染人工复核项。
func writeHumanReview(b *strings.Builder, items []review.Finding) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n## Human Review\n\n")
	for _, item := range items {
		fmt.Fprintf(b, "- [%s] %s\n", strings.ToUpper(item.Severity), item.Title)
		if item.Recommendation != "" {
			fmt.Fprintf(b, "  - Recommendation: %s\n", item.Recommendation)
		}
	}
}

// writeHumanReviewChinese 渲染中文人工复核项。
func writeHumanReviewChinese(b *strings.Builder, items []review.Finding) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n## 人工复核\n\n")
	for _, item := range items {
		fmt.Fprintf(b, "- [%s] %s\n", strings.ToUpper(item.Severity), item.Title)
		writeFindingMetadataChinese(b, item)
		writeLocalizedRuleTextChinese(b, item)
		if item.Recommendation != "" {
			fmt.Fprintf(b, "  - 修复建议: %s\n", item.Recommendation)
		}
	}
}

// writeGovernance 渲染治理摘要。
func writeGovernance(b *strings.Builder, summary review.GovernanceSummary) {
	if len(summary.PermissionDecisions) == 0 && len(summary.FilterDecisions) == 0 && summary.PermissionBlocks == 0 {
		return
	}
	b.WriteString("\n## Governance\n\n")
	if summary.PermissionBlocks > 0 {
		fmt.Fprintf(b, "- Permission blocks: %d\n", summary.PermissionBlocks)
	}
	for _, decision := range summary.PermissionDecisions {
		fmt.Fprintf(b, "- Permission %s: %s", decision.Action, decision.Command)
		if decision.Reason != "" {
			fmt.Fprintf(b, " (%s)", decision.Reason)
		}
		b.WriteString("\n")
	}
	for _, decision := range summary.FilterDecisions {
		fmt.Fprintf(b, "- Filter %s: %s", decision.Action, decision.Target)
		if decision.Reason != "" {
			fmt.Fprintf(b, " (%s)", decision.Reason)
		}
		b.WriteString("\n")
	}
}

// writeGovernanceChinese 渲染中文治理摘要。
func writeGovernanceChinese(b *strings.Builder, summary review.GovernanceSummary) {
	if len(summary.PermissionDecisions) == 0 && len(summary.FilterDecisions) == 0 && summary.PermissionBlocks == 0 {
		return
	}
	b.WriteString("\n## 治理拦截\n\n")
	if summary.PermissionBlocks > 0 {
		fmt.Fprintf(b, "- Permission 拦截: %d\n", summary.PermissionBlocks)
	}
	for _, decision := range summary.PermissionDecisions {
		fmt.Fprintf(b, "- Permission %s: %s", decision.Action, decision.Command)
		if decision.Reason != "" {
			fmt.Fprintf(b, " (%s)", decision.Reason)
		}
		b.WriteString("\n")
	}
	for _, decision := range summary.FilterDecisions {
		fmt.Fprintf(b, "- Filter %s: %s", decision.Action, decision.Target)
		if decision.Reason != "" {
			fmt.Fprintf(b, " (%s)", decision.Reason)
		}
		b.WriteString("\n")
	}
}

// writeSandbox 渲染沙箱摘要。
func writeSandbox(b *strings.Builder, summary review.SandboxSummary) {
	if len(summary.Runs) == 0 {
		return
	}
	b.WriteString("\n## Sandbox\n\n")
	for _, run := range summary.Runs {
		fmt.Fprintf(b, "- %s via %s: %s, timeout_ms=%d, output_limit_bytes=%d, duration_ms=%d\n",
			run.Command, run.Runtime, run.Status, run.TimeoutMS, run.OutputLimitBytes, run.DurationMS)
	}
}

// writeSandboxChinese 渲染中文沙箱摘要。
func writeSandboxChinese(b *strings.Builder, summary review.SandboxSummary) {
	if len(summary.Runs) == 0 {
		return
	}
	b.WriteString("\n## 沙箱执行\n\n")
	for _, run := range summary.Runs {
		fmt.Fprintf(b, "- %s via %s: %s, timeout_ms=%d, output_limit_bytes=%d, duration_ms=%d\n",
			run.Command, run.Runtime, run.Status, run.TimeoutMS, run.OutputLimitBytes, run.DurationMS)
	}
}

// writeArtifacts 渲染产物摘要。
func writeArtifacts(b *strings.Builder, artifacts []review.ArtifactSummary) {
	if len(artifacts) == 0 {
		return
	}
	b.WriteString("\n## Artifacts\n\n")
	for _, artifact := range artifacts {
		fmt.Fprintf(b, "- %s (%s)", artifact.Name, artifact.Kind)
		if artifact.Path != "" {
			fmt.Fprintf(b, ": %s", artifact.Path)
		}
		b.WriteString("\n")
	}
}

type localizedRuleText struct {
	Title          string
	Recommendation string
}

var deterministicRuleChineseText = map[string]localizedRuleText{
	"secret-leak": {
		Title:          "新增代码疑似包含敏感信息",
		Recommendation: "不要把 API key、token 或 password 写入代码；改用环境变量、密钥管理服务或安全配置注入。",
	},
	"panic-direct": {
		Title:          "新增代码直接调用 panic",
		Recommendation: "返回带上下文的 error，或在调用方显式处理失败路径，避免服务进程被异常终止。",
	},
	"goroutine-leak": {
		Title:          "新增 goroutine 缺少生命周期控制",
		Recommendation: "用 context、WaitGroup、errgroup 或明确的 done signal 绑定 goroutine 生命周期。",
	},
	"context-leak": {
		Title:          "派生 context 后没有释放 cancel",
		Recommendation: "在创建 WithCancel、WithTimeout 或 WithDeadline 后，在同一作用域 defer cancel()。",
	},
	"resource-leak": {
		Title:          "打开的资源缺少关闭路径",
		Recommendation: "资源成功打开后立即安排 Close，通常是在错误检查后 defer Close()。",
	},
	"db-lifecycle": {
		Title:          "数据库连接或事务缺少生命周期收尾",
		Recommendation: "连接句柄需要 Close，事务路径需要 Commit/Rollback，并确保失败路径也会释放资源。",
	},
	"http-body-close": {
		Title:          "HTTP 响应体未关闭",
		Recommendation: "请求成功且 response 非空后 defer resp.Body.Close()，避免连接泄漏。",
	},
	"sql-string-concat": {
		Title:          "SQL 查询通过字符串拼接构造",
		Recommendation: "使用参数化查询或占位符，不要拼接用户可控输入。",
	},
	"command-injection": {
		Title:          "命令执行使用 shell 或动态参数",
		Recommendation: "避免 shell -c，使用 exec.CommandContext 传入经过校验的字面量参数。",
	},
	"context-background-misuse": {
		Title:          "已支持 context 的函数中重新使用 context.Background",
		Recommendation: "继续传递已有 ctx，保留取消、超时和 trace 上下文。",
	},
	"mutex-unlock-missing": {
		Title:          "Mutex 加锁后缺少可见解锁路径",
		Recommendation: "Lock 成功后立即 defer Unlock，避免早返回或异常路径导致死锁。",
	},
	"defer-in-loop": {
		Title:          "循环中使用 defer 可能延迟释放资源",
		Recommendation: "把循环体抽成 helper，或在进入下一轮前显式关闭资源。",
	},
	"bare-return-err": {
		Title:          "直接返回 error，缺少操作上下文",
		Recommendation: "使用 fmt.Errorf(\"operation: %w\", err) 或等价方式补充失败位置和操作语义。",
	},
	"string-concat-loop": {
		Title:          "循环中字符串拼接可能造成重复分配",
		Recommendation: "对重复拼接使用 strings.Builder 或 bytes.Buffer；低置信度项需要人工判断实际热点。",
	},
	"todo-marker": {
		Title:          "新增代码包含 TODO 或 FIXME 标记",
		Recommendation: "合入前删除临时标记，或转成有 owner 的跟踪 issue。",
	},
	"missing-test-hint": {
		Title:          "新增函数可能缺少针对性测试",
		Recommendation: "为新增路径补充单元测试，至少覆盖正常路径和关键失败路径。",
	},
}

func writeLocalizedRuleTextChinese(b *strings.Builder, f review.Finding) {
	if !isDeterministicFindingSource(f.Source) {
		return
	}
	localized, ok := deterministicRuleChineseText[f.RuleID]
	if !ok {
		return
	}
	fmt.Fprintf(b, "  - 中文标题: %s\n", localized.Title)
	if f.Title != "" {
		fmt.Fprintf(b, "  - 原始标题: %s\n", f.Title)
	}
	if localized.Recommendation != "" {
		fmt.Fprintf(b, "  - 中文建议: %s\n", localized.Recommendation)
	}
	if f.Recommendation != "" {
		fmt.Fprintf(b, "  - 原始建议: %s\n", f.Recommendation)
	}
}

func isDeterministicFindingSource(source string) bool {
	switch source {
	case "rule", "skill_run":
		return true
	default:
		return false
	}
}

// writeArtifactsChinese 渲染中文产物摘要。
func writeArtifactsChinese(b *strings.Builder, artifacts []review.ArtifactSummary) {
	if len(artifacts) == 0 {
		return
	}
	b.WriteString("\n## 产物\n\n")
	for _, artifact := range artifacts {
		fmt.Fprintf(b, "- %s (%s)", artifact.Name, artifact.Kind)
		if artifact.Path != "" {
			fmt.Fprintf(b, ": %s", artifact.Path)
		}
		b.WriteString("\n")
	}
}
