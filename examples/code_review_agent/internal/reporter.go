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

// ReportConfig 控制报告生成行为。
type ReportConfig struct {
	OutputJSON     string // review_report.json 输出路径
	OutputMarkdown string // review_report.md 输出路径
	TaskTitle      string // 任务标题（如 PR 标题）
	Author         string // 作者
	Branch         string // 分支名
}

// GenerateJSONReport 生成 JSON 格式的审查报告。
func GenerateJSONReport(path string, task *ReviewTask, dedupCount int) error {
	type reportEntry struct {
		TaskID       string    `json:"task_id"`
		Title        string    `json:"title,omitempty"`
		Author       string    `json:"author,omitempty"`
		Branch       string    `json:"branch,omitempty"`
		Status       string    `json:"status"`
		CreatedAt    string    `json:"created_at"`
		DurationMs   int64     `json:"duration_ms"`
		InputType    string    `json:"input_type"`
		TotalFiles   int       `json:"total_files"`
		Summary      ReviewSummary `json:"summary"`
		DedupRemoved int       `json:"dedup_removed"`
		Findings     []Finding `json:"findings"`
	}

	entry := reportEntry{
		TaskID:       task.ID,
		Status:       task.Status,
		CreatedAt:    time.Unix(task.CreatedAt, 0).Format(time.RFC3339),
		DurationMs:   task.DurationMs,
		InputType:    task.InputType,
		TotalFiles:   task.TotalFiles,
		Summary:      task.Summary,
		DedupRemoved: dedupCount,
		Findings:     task.Findings,
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

	// 监控指标
	sb.WriteString("## 监控指标\n\n")
	sb.WriteString(fmt.Sprintf("- 总耗时: %dms\n", task.DurationMs))
	sb.WriteString(fmt.Sprintf("- 审查文件数: %d\n", task.TotalFiles))
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
