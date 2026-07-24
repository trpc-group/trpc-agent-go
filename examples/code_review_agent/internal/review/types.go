//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package review 提供公共审查结构、diff 解析、去重和脱敏 facade。
package review

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Result 是审查输出。
type Result struct {
	TaskID            string            `json:"task_id"`
	Findings          []Finding         `json:"findings"`
	Warnings          []Finding         `json:"warnings,omitempty"`
	HumanReviewItems  []Finding         `json:"human_review_items"`
	Metrics           Metrics           `json:"metrics,omitempty"`
	InputMetadata     InputMetadata     `json:"input_metadata,omitempty"`
	GovernanceSummary GovernanceSummary `json:"governance_summary"`
	SandboxSummary    SandboxSummary    `json:"sandbox_summary"`
	Artifacts         []ArtifactSummary `json:"artifacts"`
	Summary           string            `json:"summary,omitempty"`
	Conclusion        Conclusion        `json:"conclusion,omitempty"`
	Created           time.Time         `json:"created_at,omitempty"`
}

// InputMetadata 描述本次输入涉及的 Go 工程信息。
type InputMetadata struct {
	ChangedGoFiles   []string `json:"changed_go_files,omitempty"`
	PackageNames     []string `json:"package_names,omitempty"`
	ModulePath       string   `json:"module_path,omitempty"`
	BaseRef          string   `json:"base_ref,omitempty"`
	HeadRef          string   `json:"head_ref,omitempty"`
	HasTests         bool     `json:"has_tests,omitempty"`
	TouchedTestFiles []string `json:"touched_test_files,omitempty"`
}

// Conclusion 是最终审查结论。
type Conclusion struct {
	Status  string `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Metrics 保存审查指标。
type Metrics struct {
	Mode                string         `json:"mode"`
	SandboxRequested    bool           `json:"sandbox_requested"`
	SandboxExecuted     bool           `json:"sandbox_executed"`
	ModelRequested      bool           `json:"model_requested"`
	ModelExecuted       bool           `json:"model_executed"`
	TotalDurationMS     int64          `json:"total_duration_ms,omitempty"`
	SandboxDurationMS   int64          `json:"sandbox_duration_ms,omitempty"`
	ModelDurationMS     int64          `json:"model_duration_ms,omitempty"`
	ToolCallCount       int            `json:"tool_call_count,omitempty"`
	ModelCallCount      int            `json:"model_call_count,omitempty"`
	ModelProvider       string         `json:"model_provider,omitempty"`
	ModelName           string         `json:"model_name,omitempty"`
	ModelBackend        string         `json:"model_backend,omitempty"`
	PermissionBlocks    int            `json:"permission_block_count,omitempty"`
	FindingCount        int            `json:"finding_count,omitempty"`
	ModelFindingCount   int            `json:"model_finding_count,omitempty"`
	ModelExceptionCount int            `json:"model_exception_count,omitempty"`
	SeverityCounts      map[string]int `json:"severity_counts,omitempty"`
	ExceptionCounts     map[string]int `json:"exception_counts,omitempty"`
	RedactionCount      int            `json:"redaction_count,omitempty"`
}

// GovernanceSummary 汇总治理决策。
type GovernanceSummary struct {
	PermissionDecisions []PermissionDecisionSummary `json:"permission_decisions,omitempty"`
	FilterDecisions     []FilterDecisionSummary     `json:"filter_decisions,omitempty"`
	PermissionBlocks    int                         `json:"permission_blocks,omitempty"`
}

// PermissionDecisionSummary 是权限决策摘要。
type PermissionDecisionSummary struct {
	Command string `json:"command"`
	Action  string `json:"action"`
	Reason  string `json:"reason,omitempty"`
}

// FilterDecisionSummary 是过滤决策摘要。
type FilterDecisionSummary struct {
	Target string `json:"target"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// SandboxSummary 汇总沙箱执行。
type SandboxSummary struct {
	Runs []SandboxRunSummary `json:"runs,omitempty"`
}

// SandboxRunSummary 是单次沙箱摘要。
type SandboxRunSummary struct {
	Command          string `json:"command"`
	Runtime          string `json:"runtime"`
	Status           string `json:"status"`
	TimeoutMS        int64  `json:"timeout_ms"`
	OutputLimitBytes int    `json:"output_limit_bytes"`
	EnvWhitelist     string `json:"env_whitelist,omitempty"`
	ExitCode         int    `json:"exit_code,omitempty"`
	StdoutDigest     string `json:"stdout_digest,omitempty"`
	StderrDigest     string `json:"stderr_digest,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
}

// ArtifactSummary 描述产物引用。
type ArtifactSummary struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// Finding 是结构化审查问题。
type Finding struct {
	Severity       string `json:"severity"`
	Category       string `json:"category"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Title          string `json:"title"`
	Evidence       string `json:"evidence,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
	Confidence     string `json:"confidence,omitempty"`
	Source         string `json:"source"`
	RuleID         string `json:"rule_id"`
	Status         string `json:"status,omitempty"`
}

// DedupeKey 返回去重键。
func (f Finding) DedupeKey() string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		strings.ToLower(strings.TrimSpace(f.File)),
		fmt.Sprintf("%d", f.Line),
		strings.ToLower(strings.TrimSpace(f.Category)),
		strings.ToLower(strings.TrimSpace(f.RuleID)),
	}, "|")))
	return hex.EncodeToString(sum[:])
}

// ParsedDiff 是标准化 diff。
type ParsedDiff struct {
	Files []ParsedFile `json:"files"`
}

// ParsedFile 描述变更文件。
type ParsedFile struct {
	Path        string `json:"path"`
	Language    string `json:"language"`
	PackageName string `json:"package_name,omitempty"`
	IsTestFile  bool   `json:"is_test_file"`
	ChangeType  string `json:"change_type,omitempty"`
	Hunks       []Hunk `json:"hunks"`
}

// Hunk 表示 diff 片段。
type Hunk struct {
	File           string   `json:"file"`
	OldStart       int      `json:"old_start"`
	OldLines       int      `json:"old_lines"`
	NewStart       int      `json:"new_start"`
	NewLines       int      `json:"new_lines"`
	Context        []string `json:"context,omitempty"`
	CandidateLines []int    `json:"candidate_lines,omitempty"`
	Lines          []Line   `json:"lines,omitempty"`
}

// Line 保存 diff 行。
type Line struct {
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
}

// RedactSecrets 脱敏常见密钥。
func RedactSecrets(input string) string {
	out := input
	replacers := []struct {
		re   *regexp.Regexp
		with string
	}{
		{
			re:   regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
			with: `[REDACTED_PRIVATE_KEY]`,
		},
		{
			re:   regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)\b\s*[:=]\s*("[^"]+"|'[^']+'|[^\s,;\[]+)`),
			with: `$1=[REDACTED]`,
		},
		{
			re:   regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/=]+`),
			with: `Bearer [REDACTED]`,
		},
		{
			re:   regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
			with: `[REDACTED]`,
		},
		{
			re:   regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
			with: `[REDACTED]`,
		},
		{
			re:   regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
			with: `[REDACTED]`,
		},
		{
			re:   regexp.MustCompile(`[A-Za-z0-9_-]{3,}\.[A-Za-z0-9_-]{3,}\.[A-Za-z0-9_-]{3,}`),
			with: `[REDACTED]`,
		},
		{
			re:   regexp.MustCompile(`([a-z][a-z0-9+.-]*://[^/\s:@]+):([^@\s/]+)@`),
			with: `${1}:[REDACTED]@`,
		},
		{
			re:   regexp.MustCompile(`(?i)(password=)[^&\s]+`),
			with: `${1}[REDACTED]`,
		},
	}
	for _, replacer := range replacers {
		out = replacer.re.ReplaceAllString(out, replacer.with)
	}
	return out
}

// DedupeFindings 去重并稳定排序。
func DedupeFindings(findings []Finding) []Finding {
	seen := map[string]struct{}{}
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		key := f.DedupeKey()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

// MustJSON 返回格式化 JSON。
func MustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}
