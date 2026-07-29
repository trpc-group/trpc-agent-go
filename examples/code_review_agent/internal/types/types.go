//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package types defines shared data types used across all GraphAgent nodes.
package types

import "time"

// ── Diff parsing ──

// FileChange represents a single changed file parsed from a unified diff.
type FileChange struct {
	FilePath    string `json:"file_path"`
	PackageName string `json:"package_name"` // inferred from go.mod or path
	OldStart    int    `json:"old_start"`
	NewStart    int    `json:"new_start"`
	Hunks       []Hunk `json:"hunks"`
	Language    string `json:"language"` // "go"
}

// Hunk is one section of a unified diff.
type Hunk struct {
	OldStart int    `json:"old_start"`
	OldCount int    `json:"old_count"`
	NewStart int    `json:"new_start"`
	NewCount int    `json:"new_count"`
	Header   string `json:"header"`
	Lines    []Line `json:"lines"`
}

// Line is a single line within a hunk.
type Line struct {
	Type    string `json:"type"` // "+", "-", " "
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
	Content string `json:"content"`
}

// ── Sandbox ──

// SandboxCommand is a command to be executed in the sandbox.
type SandboxCommand struct {
	Name      string   `json:"name"`       // "go_vet" | "staticcheck" | "go_test" | "go_build"
	Cmd       string   `json:"cmd"`        // "go"
	Args      []string `json:"args"`       // ["vet", "./..."]
	Timeout   int      `json:"timeout"`    // ms, default 30000
	RiskLevel string   `json:"risk_level"` // low|medium|high (for PermissionFilter)
}

// SandboxResult captures the result of a sandbox command execution.
type SandboxResult struct {
	Command      string `json:"command"`
	ExitCode     int    `json:"exit_code"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	DurationMs   int64  `json:"duration_ms"`
	TimedOut     bool   `json:"timed_out"`
	ErrorType    string `json:"error_type"` // "" = success, "timeout"|"sandbox_crash"|"build_error"|"permission_denied"
	ArtifactPath string `json:"artifact_path"`
}

// ── Permission ──

// PermissionDecision records a single command permission check.
type PermissionDecision struct {
	Command   string    `json:"command"`
	RiskLevel string    `json:"risk_level"` // low|medium|high
	Decision  string    `json:"decision"`   // allow|deny|needs_human_review
	Reason    string    `json:"reason"`
	DecidedAt time.Time `json:"decided_at"`
}

// ── Rule Engine ──

// Rule is a single check rule loaded from skills/code-review/rules/.
type Rule struct {
	ID       string `json:"id"`        // "SEC-001"
	Category string `json:"category"`  // security|error_handling|sensitive_info|db_lifecycle|missing_test|goroutine_leak|resource_leak
	Severity string `json:"severity"`  // critical|high|medium|low
	RuleType string `json:"rule_type"` // token|tool|ast
	Pattern  string `json:"pattern"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

// ── Findings ──

// Finding is the unified review finding produced by all analysis nodes.
type Finding struct {
	ID             string  `json:"id"` // ULID
	TaskID         string  `json:"task_id"`
	Severity       string  `json:"severity"` // critical|high|medium|low|warning
	Category       string  `json:"category"` // security|error_handling|...
	File           string  `json:"file"`
	Line           int     `json:"line"` // 0 = file-level
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"` // ≤2000 chars
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`    // 0.0 ~ 1.0
	Source         string  `json:"source"`        // "rule_engine" | "llm" | "go_vet" | "staticcheck"
	DecisionKind   string  `json:"decision_kind"` // "deterministic" | "heuristic"
	RuleID         string  `json:"rule_id"`       // rule engine only
}

// ── Config ──

// ExecutorConfig configures the sandbox executor.
type ExecutorConfig struct {
	Type          string           `json:"type"`            // "local" | "cube" | "container" | "e2b"
	TimeoutSec    int              `json:"timeout_sec"`     // per-command
	MaxOutputMB   int              `json:"max_output_mb"`   // output size limit
	MaxArtifactMB int              `json:"max_artifact_mb"` // artifact file size limit (default 10)
	EnvWhitelist  []string         `json:"env_whitelist"`
	Commands      []SandboxCommand `json:"commands"`
}

// LLMConfig configures the LLM analyzer.
type LLMConfig struct {
	ModelName        string  `json:"model_name"`
	Temperature      float64 `json:"temperature"`
	MaxTokens        int     `json:"max_tokens"`
	SystemPrompt     string  `json:"system_prompt"`
	MockMode         bool    `json:"mock_mode"`
	MockFindingsPath string  `json:"mock_findings_path"` // testdata/mock_llm_findings.json
}

// DedupConfig configures deduplication and noise reduction.
type DedupConfig struct {
	ConfidenceThreshold float64 `json:"confidence_threshold"`  // default 0.6
	MaxFindingsPerFile  int     `json:"max_findings_per_file"` // default 20
	MaxTotalFindings    int     `json:"max_total_findings"`    // default 100
}

// SanitizeConfig configures sensitive data redaction.
type SanitizeConfig struct {
	Enabled     bool     `json:"enabled"`
	Patterns    []string `json:"patterns"`
	Replacement string   `json:"replacement"`
}

// DatabaseConfig configures database backend.
type DatabaseConfig struct {
	Driver string `json:"driver"` // sqlite|postgres|mysql
	DSN    string `json:"dsn"`
}

// SkillConfig configures skill loading.
type SkillConfig struct {
	Dir        string `json:"dir"`
	RulesGlob  string `json:"rules_glob"`
	ScriptsDir string `json:"scripts_dir"`
}

// PermConfig configures permission policy.
type PermConfig struct {
	DefaultPolicy map[string]string `json:"default_policy"` // risk_level -> decision
	Overrides     []PermOverride    `json:"overrides"`
}

// PermOverride is a command-level permission override.
type PermOverride struct {
	Pattern  string `json:"pattern"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// TelemetryConfig configures telemetry (reserved).
type TelemetryConfig struct {
	Enabled  bool   `json:"enabled"`
	Exporter string `json:"exporter"` // otlp|stdout|none
	Endpoint string `json:"endpoint"`
}

// ── Report ──

// ReviewReport is the aggregate report generated after a review.
type ReviewReport struct {
	TaskID               string            `json:"task_id"`
	FindingsCount        int               `json:"findings_count"`
	WarningsCount        int               `json:"warnings_count"`
	SeverityDistribution map[string]int    `json:"severity_distribution"`
	CategoryDistribution map[string]int    `json:"category_distribution"`
	Summary              string            `json:"summary"`
	NodeTimings          map[string]int64  `json:"node_timings_ms"`
	PermissionSummary    PermissionSummary `json:"permission_summary"`
	SandboxSummary       []SandboxSummary  `json:"sandbox_summary"`
}

// PermissionSummary aggregates permission decisions for the report.
type PermissionSummary struct {
	Total   int `json:"total"`
	Allowed int `json:"allowed"`
	Denied  int `json:"denied"`
	NeedsHR int `json:"needs_human_review"`
}

// SandboxSummary is the per-command sandbox execution summary.
type SandboxSummary struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	ErrorType  string `json:"error_type"`
}
