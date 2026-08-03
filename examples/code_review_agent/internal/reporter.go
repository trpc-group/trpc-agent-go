//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ReportMeta 携带报告所需的治理拦截与沙箱执行数据。
type ReportMeta struct {
	Monitoring          MonitoringSummary
	PermissionDecisions []PermissionDecision
	SandboxRuns         []SandboxRun
}

// ReportConfig 控制报告生成行为。
type ReportConfig struct {
	OutputJSON     string // review_report.json 输出路径
	OutputMarkdown string // review_report.md 输出路径
	TaskTitle      string // 任务标题（如 PR 标题）
	Author         string // 作者
	Branch         string // 分支名
	Meta           ReportMeta
}

// GenerateJSONReport 生成 JSON 格式的审查报告。
func GenerateJSONReport(path string, task *ReviewTask, dedupCount int, meta ReportMeta) error {
	type reportEntry struct {
		TaskID               string               `json:"task_id"`
		Title                string               `json:"title,omitempty"`
		Author               string               `json:"author,omitempty"`
		Branch               string               `json:"branch,omitempty"`
		Status               string               `json:"status"`
		CreatedAt            string               `json:"created_at"`
		DurationMs           int64                `json:"duration_ms"`
		InputType            string               `json:"input_type"`
		TotalFiles           int                  `json:"total_files"`
		Summary              ReviewSummary        `json:"summary"`
		DedupRemoved         int                  `json:"dedup_removed"`
		Findings             []Finding            `json:"findings"`
		PermissionDecisions  []PermissionDecision `json:"permission_decisions,omitempty"`
		SandboxRuns          []SandboxRun         `json:"sandbox_runs,omitempty"`
		ToolCallsCount       int                  `json:"tool_calls_count"`
		PermissionIntercepts int                  `json:"permission_intercepts"`
		SandboxDurationMs    int64                `json:"sandbox_duration_ms"`
	}

	entry := reportEntry{
		TaskID:               task.ID,
		Status:               task.Status,
		CreatedAt:            time.Unix(task.CreatedAt, 0).Format(time.RFC3339),
		DurationMs:           task.DurationMs,
		InputType:            task.InputType,
		TotalFiles:           task.TotalFiles,
		Summary:              task.Summary,
		DedupRemoved:         dedupCount,
		Findings:             task.Findings,
		PermissionDecisions:  meta.PermissionDecisions,
		SandboxRuns:          meta.SandboxRuns,
		ToolCallsCount:       meta.Monitoring.ToolCallsCount,
		PermissionIntercepts: meta.Monitoring.PermissionIntercepts,
		SandboxDurationMs:    meta.Monitoring.SandboxDurationMs,
	}

	if len(task.Findings) == 0 {
		entry.Findings = []Finding{} // 确保 JSON 输出空数组而非 null
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 JSON 报告失败: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// GenerateMarkdownReport 生成 Markdown 格式的审查报告。
func GenerateMarkdownReport(path string, task *ReviewTask, cfg ReportConfig) error {
	var sb strings.Builder

	// 标题
	sb.WriteString("# 代码评审报告\n\n")

	// 基本信息
	sb.WriteString("## 基本信息\n\n")
	sb.WriteString("| 字段 | 值 |\n")
	sb.WriteString("|------|----|\n")
	sb.WriteString(fmt.Sprintf("| Task ID | `%s` |\n", task.ID))
	if cfg.TaskTitle != "" {
		sb.WriteString(fmt.Sprintf("| 标题 | %s |\n", cfg.TaskTitle))
	}
	if cfg.Author != "" {
		sb.WriteString(fmt.Sprintf("| 作者 | %s |\n", cfg.Author))
	}
	if cfg.Branch != "" {
		sb.WriteString(fmt.Sprintf("| 分支 | %s |\n", cfg.Branch))
	}
	sb.WriteString(fmt.Sprintf("| 状态 | %s |\n", task.Status))
	sb.WriteString(fmt.Sprintf("| 审查时间 | %s |\n", time.Unix(task.CreatedAt, 0).Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("| 耗时 | %dms |\n", task.DurationMs))
	sb.WriteString(fmt.Sprintf("| 审查文件数 | %d |\n", task.TotalFiles))
	sb.WriteString("\n")

	// 摘要
	sb.WriteString("## 审查摘要\n\n")
	sb.WriteString("| 严重级别 | 数量 |\n")
	sb.WriteString("|----------|------|\n")
	sb.WriteString(fmt.Sprintf("| 🔴 Critical | %d |\n", task.Summary.Critical))
	sb.WriteString(fmt.Sprintf("| 🟠 High | %d |\n", task.Summary.High))
	sb.WriteString(fmt.Sprintf("| 🟡 Medium | %d |\n", task.Summary.Medium))
	sb.WriteString(fmt.Sprintf("| 🔵 Low | %d |\n", task.Summary.Low))
	sb.WriteString(fmt.Sprintf("| ⚪ Warning | %d |\n", task.Summary.Warning))
	sb.WriteString(fmt.Sprintf("| **总计** | **%d** |\n", task.Summary.Total-task.Summary.Duplicates))
	sb.WriteString(fmt.Sprintf("| 去重移除 | %d |\n", task.Summary.Duplicates))
	sb.WriteString("\n")

	// Findings
	if len(task.Findings) == 0 {
		sb.WriteString("## ✅ 审查通过\n\n")
		sb.WriteString("未发现任何问题，代码质量良好。\n")
	} else {
		sb.WriteString("## 发现的问题\n\n")

		// 按严重级别分组
		groups := []struct {
			severity Severity
			icon     string
			heading  string
		}{
			{SeverityCritical, "🔴", "严重 (Critical)"},
			{SeverityHigh, "🟠", "高危 (High)"},
			{SeverityMedium, "🟡", "中危 (Medium)"},
			{SeverityLow, "🔵", "低危 (Low)"},
			{SeverityWarning, "⚪", "建议 (Warning)"},
		}

		for _, g := range groups {
			count := countBySeverity(task.Findings, g.severity)
			if count == 0 {
				continue
			}
			sb.WriteString(fmt.Sprintf("### %s %s (%d)\n\n", g.icon, g.heading, count))

			for _, f := range task.Findings {
				if f.Severity != g.severity || f.IsDuplicate {
					continue
				}
				sb.WriteString(fmt.Sprintf("#### %s\n\n", f.Title))
				if f.File != "" {
					sb.WriteString(fmt.Sprintf("- **文件**: `%s`", f.File))
					if f.Line > 0 {
						sb.WriteString(fmt.Sprintf(":%d", f.Line))
					}
					sb.WriteString("\n")
				}
				sb.WriteString(fmt.Sprintf("- **分类**: %s\n", f.Category))
				sb.WriteString(fmt.Sprintf("- **规则**: %s\n", f.RuleID))
				sb.WriteString(fmt.Sprintf("- **来源**: %s\n", f.Source))
				if f.Confidence < 1.0 {
					sb.WriteString(fmt.Sprintf("- **置信度**: %.0f%%\n", f.Confidence*100))
				}
				if f.Evidence != "" {
					sb.WriteString(fmt.Sprintf("- **证据**:\n  ```go\n  %s\n  ```\n", f.Evidence))
				}
				if f.Recommendation != "" {
					sb.WriteString(fmt.Sprintf("- **建议**: %s\n", f.Recommendation))
				}
				sb.WriteString("\n")
			}
		}
	}

	// 人工复核项
	if task.Summary.NeedsReview > 0 {
		sb.WriteString("## 人工复核项\n\n")
		sb.WriteString(fmt.Sprintf("以下 %d 条为低置信度发现，建议人工复核后再处理：\n\n", task.Summary.NeedsReview))
		for _, f := range task.Findings {
			if !f.NeedsHumanReview || f.IsDuplicate {
				continue
			}
			sb.WriteString(fmt.Sprintf("- **%s**", f.Title))
			if f.File != "" {
				sb.WriteString(fmt.Sprintf(" (`%s`:%d)", f.File, f.Line))
			}
			sb.WriteString(fmt.Sprintf(" — 置信度 %.0f%%\n", f.Confidence*100))
		}
		sb.WriteString("\n")
	}

	// 治理拦截摘要
	sb.WriteString("## 治理拦截摘要\n\n")
	if len(cfg.Meta.PermissionDecisions) == 0 {
		sb.WriteString("本次审查无沙箱命令进入安全门禁，或未配置沙箱执行。\n\n")
	} else {
		intercepted := 0
		byDecision := make(map[string]int)
		for _, d := range cfg.Meta.PermissionDecisions {
			byDecision[d.Decision]++
			if d.Intercepted {
				intercepted++
			}
		}
		sb.WriteString(fmt.Sprintf("| 指标 | 值 |\n"))
		sb.WriteString(fmt.Sprintf("|------|----|\n"))
		sb.WriteString(fmt.Sprintf("| 命令检查数 | %d |\n", len(cfg.Meta.PermissionDecisions)))
		sb.WriteString(fmt.Sprintf("| 拦截数 | %d |\n", intercepted))
		for _, decision := range []string{"allow", "deny", "ask", "needs_human_review"} {
			if n := byDecision[decision]; n > 0 {
				sb.WriteString(fmt.Sprintf("| %s 决策 | %d |\n", decision, n))
			}
		}
		sb.WriteString("\n")
		for _, d := range cfg.Meta.PermissionDecisions {
			sb.WriteString(fmt.Sprintf("- `%s` → **%s** (rule: %s)", d.Command, d.Decision, d.RuleID))
			if d.Intercepted {
				sb.WriteString(" 🔒 已拦截")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// 沙箱执行摘要
	sb.WriteString("## 沙箱执行摘要\n\n")
	if len(cfg.Meta.SandboxRuns) == 0 {
		sb.WriteString("本次审查未实际执行沙箱命令（dry-run 或无 repo-path）。\n\n")
	} else {
		var totalMs int64
		timeouts := 0
		for _, r := range cfg.Meta.SandboxRuns {
			totalMs += r.DurationMs
			if r.TimedOut {
				timeouts++
			}
		}
		sb.WriteString(fmt.Sprintf("| 指标 | 值 |\n"))
		sb.WriteString(fmt.Sprintf("|------|----|\n"))
		sb.WriteString(fmt.Sprintf("| 执行次数 | %d |\n", len(cfg.Meta.SandboxRuns)))
		sb.WriteString(fmt.Sprintf("| 沙箱总耗时 | %dms |\n", totalMs))
		sb.WriteString(fmt.Sprintf("| 超时次数 | %d |\n", timeouts))
		sb.WriteString("\n")
		for _, r := range cfg.Meta.SandboxRuns {
			status := "成功"
			if r.TimedOut {
				status = "超时"
			} else if r.ExitCode != 0 {
				status = fmt.Sprintf("失败(exit=%d)", r.ExitCode)
			}
			sb.WriteString(fmt.Sprintf("- `%s` → %s (%dms)\n", r.Command, status, r.DurationMs))
		}
		sb.WriteString("\n")
	}

	// 监控指标
	sb.WriteString("## 监控指标\n\n")
	sb.WriteString(fmt.Sprintf("- 总耗时: %dms\n", task.DurationMs))
	sb.WriteString(fmt.Sprintf("- 审查文件数: %d\n", task.TotalFiles))
	sb.WriteString(fmt.Sprintf("- 沙箱执行耗时: %dms\n", cfg.Meta.Monitoring.SandboxDurationMs))
	sb.WriteString(fmt.Sprintf("- 工具调用次数: %d\n", cfg.Meta.Monitoring.ToolCallsCount))
	sb.WriteString(fmt.Sprintf("- Permission 拦截次数: %d\n", cfg.Meta.Monitoring.PermissionIntercepts))
	sb.WriteString(fmt.Sprintf("- Finding 数量: %d\n", cfg.Meta.Monitoring.FindingCount))
	sb.WriteString("\n")

	// 页脚
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("*报告由 code_review_agent 生成于 %s*\n",
		time.Now().Format(time.RFC3339)))

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func countBySeverity(findings []Finding, severity Severity) int {
	count := 0
	for _, f := range findings {
		if f.Severity == severity && !f.IsDuplicate {
			count++
		}
	}
	return count
}
