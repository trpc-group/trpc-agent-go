package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// Severity 表示 finding 的严重级别。
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityWarning  Severity = "warning"
)

// severityOrder 返回 severity 的数值顺序（越大越严重）。
func severityOrder(s Severity) int {
	switch s {
	case SeverityWarning:
		return 0
	case SeverityLow:
		return 1
	case SeverityMedium:
		return 2
	case SeverityHigh:
		return 3
	case SeverityCritical:
		return 4
	default:
		return 0
	}
}

// Category 表示 finding 的分类。
type Category string

const (
	CategorySecurity      Category = "security"
	CategoryConcurrency   Category = "goroutine_context"
	CategoryResource      Category = "resource_cleanup"
	CategoryErrorHandling Category = "error_handling"
	CategoryTesting       Category = "test_coverage"
	CategoryDBLifecycle   Category = "db_lifecycle"
	CategorySensitive     Category = "sensitive_info"
)

// Finding 表示一次代码审查发现的问题。
type Finding struct {
	Severity       Severity `json:"severity"`
	Category       Category `json:"category"`
	File           string   `json:"file"`
	Line           int      `json:"line"`
	Title          string   `json:"title"`
	Evidence       string   `json:"evidence"`
	Recommendation string   `json:"recommendation"`
	Confidence     float64  `json:"confidence"`
	Source         string   `json:"source"` // "rule" / "go_vet" / "sandbox_script"
	RuleID         string   `json:"rule_id"`
	DedupKey       string   `json:"dedup_key"`
	IsDuplicate    bool     `json:"is_duplicate"`
	Timestamp      int64    `json:"timestamp"`
}

// NewFinding 创建一个新的 Finding 并自动计算 dedup key。
func NewFinding(
	severity Severity, category Category,
	file string, line int,
	title, evidence, recommendation string,
	source, ruleID string,
) Finding {
	f := Finding{
		Severity:       severity,
		Category:       category,
		File:           file,
		Line:           line,
		Title:          title,
		Evidence:       evidence,
		Recommendation: recommendation,
		Confidence:     1.0,
		Source:         source,
		RuleID:         ruleID,
		Timestamp:      time.Now().Unix(),
	}
	f.DedupKey = computeDedupKey(f)
	return f
}

// computeDedupKey 基于 file + line + category + rule_id 计算去重键。
func computeDedupKey(f Finding) string {
	raw := fmt.Sprintf("%s:%d:%s:%s", f.File, f.Line, f.Category, f.RuleID)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:16])
}

// DeduplicateFindings 对 findings 进行去重。同一 dedup key 只保留最高 severity。
// 返回去重后的 findings，IsDuplicate 标记的被过滤掉。
func DeduplicateFindings(findings []Finding) []Finding {
	if len(findings) <= 1 {
		return findings
	}

	// 按 dedup key 分组，保留 severity 最高的。
	best := make(map[string]int) // dedup_key -> index in result
	result := make([]Finding, 0, len(findings))

	for i := range findings {
		f := findings[i]
		if idx, ok := best[f.DedupKey]; ok {
			// 已存在，比较 severity
			if severityOrder(f.Severity) > severityOrder(result[idx].Severity) {
				// 当前更严重，替换
				result[idx].IsDuplicate = true
				f.IsDuplicate = false
				result[idx] = f
				best[f.DedupKey] = idx
			} else {
				// 已有更严重的，标记为重复
				findings[i].IsDuplicate = true
			}
		} else {
			best[f.DedupKey] = len(result)
			result = append(result, f)
		}
	}

	return result
}

// ReviewSummary 是审查结果的统计汇总。
type ReviewSummary struct {
	Total      int `json:"total"`
	Critical   int `json:"critical"`
	High       int `json:"high"`
	Medium     int `json:"medium"`
	Low        int `json:"low"`
	Warning    int `json:"warning"`
	Duplicates int `json:"duplicates"`
}

// ComputeSummary 从 findings 列表计算统计汇总。
func ComputeSummary(findings []Finding) ReviewSummary {
	var s ReviewSummary
	s.Total = len(findings)
	for _, f := range findings {
		if f.IsDuplicate {
			s.Duplicates++
		}
		switch f.Severity {
		case SeverityCritical:
			s.Critical++
		case SeverityHigh:
			s.High++
		case SeverityMedium:
			s.Medium++
		case SeverityLow:
			s.Low++
		case SeverityWarning:
			s.Warning++
		}
	}
	return s
}

// CountNonDuplicate 统计非重复的 finding 数量。
func CountNonDuplicate(findings []Finding) int {
	count := 0
	for _, f := range findings {
		if !f.IsDuplicate {
			count++
		}
	}
	return count
}

// SortFindings 按严重级别降序、文件路径升序排列 findings。
func SortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Severity != b.Severity {
			return severityOrder(a.Severity) > severityOrder(b.Severity)
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

// ReviewTask 表示一次审查任务。
type ReviewTask struct {
	ID            string        `json:"id"`
	InputType     string        `json:"input_type"` // "diff_file" / "repo_path"
	InputHash     string        `json:"input_hash"`
	Status        string        `json:"status"` // "pending" / "running" / "completed" / "failed"
	CreatedAt     int64         `json:"created_at"`
	CompletedAt   int64         `json:"completed_at"`
	TotalFiles    int           `json:"total_files"`
	TotalFindings int           `json:"total_findings"`
	Summary       ReviewSummary `json:"summary"`
	Findings      []Finding     `json:"findings"`
	DurationMs    int64         `json:"duration_ms"`
	ErrorMessage  string        `json:"error_message,omitempty"`
}

// NewReviewTask 创建一个新的审查任务。
func NewReviewTask(id, inputType, inputHash string) *ReviewTask {
	return &ReviewTask{
		ID:        id,
		InputType: inputType,
		InputHash: inputHash,
		Status:    "pending",
		CreatedAt: time.Now().Unix(),
	}
}
