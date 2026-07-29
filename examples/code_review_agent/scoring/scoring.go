// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package scoring 提供代码审查风险评分系统。
//
// 不是简单的 high/medium/low 分级，而是 0-100 的连续风险分数，
// 带多维度 breakdown，让审查结果更量化、更可比较。
//
// 评分维度：
//   - 安全问题（30%）：密钥泄漏、注入风险
//   - 资源泄漏（20%）：goroutine、文件、连接
//   - 错误处理（15%）：忽略 error、panic
//   - 测试覆盖（15%）：缺少测试
//   - 代码质量（10%）：复杂度、重复
//   - 性能风险（10%）：不必要的拷贝、阻塞
package scoring

import (
	"fmt"
	"math"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// RiskScore 是代码审查的风险评分结果。
type RiskScore struct {
	Score        float64              `json:"score"`             // 总分 0-100（越高越危险）
	Grade        string               `json:"grade"`             // 等级 A/B/C/D/F
	Breakdown    map[string]Dimension `json:"breakdown"`         // 各维度得分
	FindingCount int                  `json:"finding_count"`     // findings 总数
	WARNING      string               `json:"warning,omitempty"` // 警告信息
}

// Dimension 是单个评分维度。
type Dimension struct {
	Name     string  `json:"name"`     // 维度名称
	Weight   float64 `json:"weight"`   // 权重
	Score    float64 `json:"score"`    // 该维度得分 0-100
	Count    int     `json:"count"`    // findings 数量
	Weighted float64 `json:"weighted"` // 加权得分
}

// ========== 评分维度定义 ==========

var dimensions = []struct {
	Name     string
	Weight   float64
	Category findings.Category
}{
	{"安全问题", 0.30, findings.CategorySecurity},
	{"敏感信息", 0.15, findings.CategorySensitiveLeak},
	{"资源泄漏", 0.20, findings.CategoryResource},
	{"错误处理", 0.15, findings.CategoryErrorHandling},
	{"测试覆盖", 0.15, findings.CategoryTesting},
	{"并发问题", 0.05, findings.CategoryConcurrency},
}

// ========== 评分函数 ==========

// Calculate 根据 findings 列表计算风险评分。
func Calculate(findingsList []findings.Finding, warnings []findings.Finding) RiskScore {
	result := RiskScore{
		Breakdown: make(map[string]Dimension),
	}

	// 计算各维度得分
	for _, dim := range dimensions {
		count := 0
		severitySum := 0.0

		for _, f := range findingsList {
			if f.Category == dim.Category {
				count++
				severitySum += severityWeight(f.Severity)
			}
		}

		// 该维度得分 = min(100, 平均严重度 * 100)
		dimScore := 0.0
		if count > 0 {
			dimScore = math.Min(100, (severitySum/float64(count))*100)
		}

		weighted := dimScore * dim.Weight

		result.Breakdown[dim.Name] = Dimension{
			Name:     dim.Name,
			Weight:   dim.Weight,
			Score:    dimScore,
			Count:    count,
			Weighted: weighted,
		}
	}

	// 计算总分
	totalScore := 0.0
	for _, d := range result.Breakdown {
		totalScore += d.Weighted
	}
	result.Score = math.Round(totalScore*100) / 100

	// 计算等级
	result.Grade = calculateGrade(result.Score)

	// 统计
	result.FindingCount = len(findingsList)

	// 警告
	if len(warnings) > 0 {
		result.WARNING = fmt.Sprintf("有 %d 条低置信度警告需要人工复核", len(warnings))
	}

	return result
}

// severityWeight 返回严重级别的权重。
func severityWeight(sev findings.Severity) float64 {
	switch sev {
	case findings.SeverityHigh:
		return 1.0
	case findings.SeverityMedium:
		return 0.6
	case findings.SeverityLow:
		return 0.3
	case findings.SeverityInfo:
		return 0.1
	default:
		return 0.0
	}
}

// calculateGrade 根据分数计算等级。
func calculateGrade(score float64) string {
	switch {
	case score >= 80:
		return "F"
	case score >= 60:
		return "D"
	case score >= 40:
		return "C"
	case score >= 20:
		return "B"
	default:
		return "A"
	}
}

// ========== 报告生成 ==========

// ToReport 生成文字报告。
func (r *RiskScore) ToReport() string {
	report := fmt.Sprintf("代码审查风险评分：%.0f/100 (%s)\n\n", r.Score, r.Grade)

	report += "各维度得分：\n"
	for _, name := range []string{"安全问题", "敏感信息", "资源泄漏", "错误处理", "测试覆盖", "并发问题"} {
		dim, ok := r.Breakdown[name]
		if !ok {
			continue
		}
		bar := renderBar(dim.Score)
		report += fmt.Sprintf("  %-8s %s %.0f/100 (权重%.0f%%, %d个问题)\n",
			name, bar, dim.Score, dim.Weight*100, dim.Count)
	}

	report += fmt.Sprintf("\n发现总数：%d\n", r.FindingCount)

	if r.WARNING != "" {
		report += "\n⚠️ " + r.WARNING + "\n"
	}

	return report
}

// renderBar 渲染进度条。
func renderBar(score float64) string {
	filled := int(score / 10)
	empty := 10 - filled
	bar := ""
	for i := 0; i < filled; i++ {
		bar += "█"
	}
	for i := 0; i < empty; i++ {
		bar += "░"
	}
	return bar
}
