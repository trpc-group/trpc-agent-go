// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package report

import (
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// generateMarkdown 生成 Markdown 格式的审查报告。
func generateMarkdown(r *ReviewReport) string {
	var b strings.Builder

	// 标题
	b.WriteString("# 代码审查报告\n\n")

	// 基本信息
	b.WriteString("## 基本信息\n\n")
	b.WriteString(fmt.Sprintf("- **任务 ID**: `%s`\n", r.TaskID))
	b.WriteString(fmt.Sprintf("- **输入类型**: %s\n", r.InputType))
	b.WriteString(fmt.Sprintf("- **输入路径**: `%s`\n", r.InputPath))
	b.WriteString(fmt.Sprintf("- **开始时间**: %s\n", r.StartTime))
	b.WriteString(fmt.Sprintf("- **结束时间**: %s\n", r.EndTime))
	b.WriteString(fmt.Sprintf("- **耗时**: %s\n", r.Duration))
	b.WriteString(fmt.Sprintf("- **变更文件数**: %d（其中 Go 文件 %d 个）\n", r.FilesCount, r.GoFilesCount))
	b.WriteString("\n")

	// 摘要
	b.WriteString("## 审查摘要\n\n")
	b.WriteString(fmt.Sprintf("- **高置信度发现**: %d 条\n", r.Summary.TotalFindings))
	b.WriteString(fmt.Sprintf("- **低置信度警告**: %d 条（需人工复核）\n", r.Summary.TotalWarnings))
	b.WriteString(fmt.Sprintf("- **去重移除**: %d 条\n", r.Summary.DedupRemoved))
	b.WriteString("\n")

	// 严重级别统计
	if len(r.Summary.BySeverity) > 0 {
		b.WriteString("### 严重级别分布\n\n")
		b.WriteString("| 级别 | 数量 |\n")
		b.WriteString("|------|------|\n")
		for _, sev := range []string{"high", "medium", "low", "info"} {
			if count, ok := r.Summary.BySeverity[sev]; ok && count > 0 {
				b.WriteString(fmt.Sprintf("| %s | %d |\n", severityIcon(sev), count))
			}
		}
		b.WriteString("\n")
	}

	// 分类统计（按确定顺序输出，避免 map 遍历随机性）
	if len(r.Summary.ByCategory) > 0 {
		b.WriteString("### 问题分类分布\n\n")
		b.WriteString("| 分类 | 数量 |\n")
		b.WriteString("|------|------|\n")
		// 收集并排序 key
		var cats []string
		for cat := range r.Summary.ByCategory {
			cats = append(cats, cat)
		}
		sort.Strings(cats)
		for _, cat := range cats {
			if count := r.Summary.ByCategory[cat]; count > 0 {
				b.WriteString(fmt.Sprintf("| %s | %d |\n", cat, count))
			}
		}
		b.WriteString("\n")
	}

	// 高置信度发现详情
	if len(r.Findings) > 0 {
		b.WriteString("## 审查发现\n\n")
		for i, f := range r.Findings {
			writeFindingDetail(&b, i+1, f)
		}
	} else {
		b.WriteString("## 审查发现\n\n")
		b.WriteString("✅ 未发现高置信度问题。\n\n")
	}

	// 低置信度警告（人工复核项）
	if len(r.Warnings) > 0 {
		b.WriteString("## 需人工复核\n\n")
		b.WriteString("以下发现置信度较低，建议人工确认：\n\n")
		b.WriteString("| # | 文件 | 行号 | 问题 | 置信度 |\n")
		b.WriteString("|---|------|------|------|--------|\n")
		for i, w := range r.Warnings {
			b.WriteString(fmt.Sprintf("| %d | `%s` | %d | %s | %.0f%% |\n",
				i+1, w.File, w.Line, w.Title, w.Confidence*100))
		}
		b.WriteString("\n")
		for i, w := range r.Warnings {
			writeFindingDetail(&b, i+1, w)
		}
	} else {
		b.WriteString("## 需人工复核\n\n")
		b.WriteString("✅ 无需人工复核的项目。\n\n")
	}

	// 治理拦截摘要
	b.WriteString("## 治理拦截摘要\n\n")
	if r.Governance.TotalChecks > 0 {
		b.WriteString(fmt.Sprintf("- **总检查次数**: %d\n", r.Governance.TotalChecks))
		b.WriteString(fmt.Sprintf("- **允许执行**: %d\n", r.Governance.Allowed))
		b.WriteString(fmt.Sprintf("- **拒绝执行**: %d\n", r.Governance.Denied))
		b.WriteString(fmt.Sprintf("- **需要人工确认**: %d\n", r.Governance.AskHuman))
		if len(r.Governance.DeniedCommands) > 0 {
			b.WriteString("- **被拦截命令**:\n")
			for _, cmd := range r.Governance.DeniedCommands {
				b.WriteString(fmt.Sprintf("  - `%s`\n", cmd))
			}
		}
	} else {
		b.WriteString("未执行治理检查（dry-run 模式或无 Go 文件变更）。\n")
	}
	b.WriteString("\n")

	// 沙箱执行摘要
	b.WriteString("## 沙箱执行摘要\n\n")
	if r.SandboxSummary.TotalRuns > 0 {
		b.WriteString(fmt.Sprintf("- **总执行次数**: %d\n", r.SandboxSummary.TotalRuns))
		b.WriteString(fmt.Sprintf("- **成功**: %d\n", r.SandboxSummary.Successful))
		b.WriteString(fmt.Sprintf("- **失败**: %d\n", r.SandboxSummary.Failed))
		b.WriteString(fmt.Sprintf("- **超时**: %d\n", r.SandboxSummary.TimedOut))
		b.WriteString(fmt.Sprintf("- **总耗时**: %s\n", r.SandboxSummary.TotalDuration))

		if len(r.SandboxRuns) > 0 {
			b.WriteString("\n| 命令 | 后端 | 退出码 | 耗时 | 截断 |\n")
			b.WriteString("|------|------|--------|------|------|\n")
			for _, run := range r.SandboxRuns {
				truncated := "否"
				if run.Truncated {
					truncated = "是"
				}
				b.WriteString(fmt.Sprintf("| `%s` | %s | %d | %s | %s |\n",
					run.Command, run.Backend, run.ExitCode, run.Duration, truncated))
			}
		}
	} else {
		b.WriteString("未执行沙箱检查（dry-run 模式或无 Go 文件变更）。\n")
	}
	b.WriteString("\n")

	// 监控指标
	b.WriteString("## 监控指标\n\n")
	b.WriteString(fmt.Sprintf("- **总耗时**: %s\n", r.Monitor.TotalDuration))
	b.WriteString(fmt.Sprintf("- **规则执行耗时**: %s\n", r.Monitor.RuleDuration))
	b.WriteString(fmt.Sprintf("- **沙箱执行耗时**: %s\n", r.Monitor.SandboxDuration))
	b.WriteString(fmt.Sprintf("- **扫描文件数**: %d\n", r.Monitor.FilesScanned))
	b.WriteString(fmt.Sprintf("- **规则数量**: %d\n", r.Monitor.RuleCount))
	b.WriteString(fmt.Sprintf("- **权限拦截次数**: %d\n", r.Monitor.PermissionDenied))
	b.WriteString(fmt.Sprintf("- **异常次数**: %d\n", r.Monitor.ExceptionCount))
	b.WriteString(fmt.Sprintf("- **风险评分**: %.0f/100 (%s)\n", r.Monitor.RiskScore, r.Monitor.RiskGrade))
	b.WriteString("\n")

	// 修复建议汇总
	if len(r.Findings) > 0 {
		b.WriteString("## 修复建议汇总\n\n")
		for _, f := range r.Findings {
			b.WriteString(fmt.Sprintf("- **%s** (`%s:%d`): %s\n", f.Title, f.File, f.Line, f.Recommendation))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	b.WriteString("*报告由 code-review-agent 自动生成*\n")

	return b.String()
}

// writeFindingDetail 写入单个 finding 的详情。
func writeFindingDetail(b *strings.Builder, index int, f findings.Finding) {
	b.WriteString(fmt.Sprintf("### %d. %s\n\n", index, f.Title))
	b.WriteString(fmt.Sprintf("- **严重级别**: %s\n", severityIcon(string(f.Severity))))
	b.WriteString(fmt.Sprintf("- **分类**: %s\n", f.Category))
	b.WriteString(fmt.Sprintf("- **文件**: `%s`\n", f.File))
	b.WriteString(fmt.Sprintf("- **行号**: %d\n", f.Line))
	b.WriteString(fmt.Sprintf("- **规则**: `%s` (%s)\n", f.RuleID, f.Source))
	b.WriteString(fmt.Sprintf("- **置信度**: %.0f%%\n", f.Confidence*100))
	b.WriteString("\n")

	b.WriteString("**问题代码**:\n")
	b.WriteString("```\n")
	b.WriteString(f.Evidence)
	b.WriteString("\n```\n\n")

	b.WriteString("**修复建议**:\n")
	b.WriteString(f.Recommendation)
	b.WriteString("\n\n")
}

// severityIcon 返回严重级别对应的图标和文字。
func severityIcon(sev string) string {
	switch sev {
	case "high":
		return "🔴 high"
	case "medium":
		return "🟡 medium"
	case "low":
		return "🔵 low"
	case "info":
		return "⚪ info"
	default:
		return sev
	}
}
