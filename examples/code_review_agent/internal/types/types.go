// Package types defines shared data types used across all GraphAgent nodes.
package types

import "time"

// ── Diff parsing ──

// FileChange represents a single changed file parsed from a unified diff.
type FileChange struct {
	FilePath    string `json:"file_path"`    // path relative to repo root
	PackageName string `json:"package_name"` // inferred from go.mod or path
	OldStart    int    `json:"old_start"`    // starting line in the old file
	NewStart    int    `json:"new_start"`    // starting line in the new file
	Hunks       []Hunk `json:"hunks"`        // diff hunks for this file
	Language    string `json:"language"`     // "go"
}

// Hunk is one section of a unified diff.
type Hunk struct {
	OldStart int    `json:"old_start"` // starting line in the original file
	OldCount int    `json:"old_count"` // number of original lines in the hunk
	NewStart int    `json:"new_start"` // starting line in the new file
	NewCount int    `json:"new_count"` // number of new lines in the hunk
	Header   string `json:"header"`    // @@ header string
	Lines    []Line `json:"lines"`     // lines comprising this hunk
}

// Line is a single line within a hunk.
type Line struct {
	Type    string `json:"type"`     // "+", "-", " "
	OldLine int    `json:"old_line"` // line number in the original file
	NewLine int    `json:"new_line"` // line number in the new file
	Content string `json:"content"`  // line content
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
	Command      string `json:"command"`       // command that was executed
	ExitCode     int    `json:"exit_code"`     // process exit code
	Stdout       string `json:"stdout"`        // standard output
	Stderr       string `json:"stderr"`        // standard error
	DurationMs   int64  `json:"duration_ms"`   // execution duration in milliseconds
	TimedOut     bool   `json:"timed_out"`     // whether the command timed out
	ErrorType    string `json:"error_type"`    // "" = success, "timeout"|"sandbox_crash"|"build_error"|"permission_denied"
	ArtifactPath string `json:"artifact_path"` // path to any produced artifact
}

// ── Permission ──

// PermissionDecision records a single command permission check.
type PermissionDecision struct {
	Command   string    `json:"command"`    // command being checked
	RiskLevel string    `json:"risk_level"` // low|medium|high
	Decision  string    `json:"decision"`   // allow|deny|needs_human_review
	Reason    string    `json:"reason"`     // justification for the decision
	DecidedAt time.Time `json:"decided_at"` // when the decision was made
}

// ── Rule Engine ──

// Rule is a single check rule loaded from skills/code-review/rules/.
type Rule struct {
	ID       string `json:"id"`        // "SEC-001"
	Category string `json:"category"`  // security|error_handling|sensitive_info|db_lifecycle|missing_test|goroutine_leak|resource_leak
	Severity string `json:"severity"`  // critical|high|medium|low
	RuleType string `json:"rule_type"` // token|tool|ast
	Pattern  string `json:"pattern"`   // search pattern for matching
	Message  string `json:"message"`   // description of the issue
	Fix      string `json:"fix"`       // suggested fix or remediation
}

// ── Findings ──

// Finding is the unified review finding produced by all analysis nodes.
type Finding struct {
	ID             string  `json:"id"`             // ULID
	TaskID         string  `json:"task_id"`        // associated review task ID
	Severity       string  `json:"severity"`       // critical|high|medium|low|warning
	Category       string  `json:"category"`       // security|error_handling|...
	File           string  `json:"file"`           // affected file path
	Line           int     `json:"line"`           // 0 = file-level
	Title          string  `json:"title"`          // short description of the finding
	Evidence       string  `json:"evidence"`       // ≤2000 chars
	Recommendation string  `json:"recommendation"` // suggested action to address the issue
	Confidence     float64 `json:"confidence"`     // 0.0 ~ 1.0
	Source         string  `json:"source"`         // "rule_engine" | "llm" | "go_vet" | "staticcheck"
	DecisionKind   string  `json:"decision_kind"`  // "deterministic" | "heuristic"
	RuleID         string  `json:"rule_id"`        // rule engine only
}

// ── LLM Error Tracking ──

// LLMError records an LLM analyzer failure for monitoring purposes.
// Downstream nodes use this to distinguish "no issues found" from "LLM was
// unreachable / errored" in metrics and exception tables.
type LLMError struct {
	ErrorType string `json:"error_type"` // "no_model" | "llm_failure" | "mock_load"
	Detail    string `json:"detail"`     // detailed error message
}

// ── Config ──

// ExecutorConfig configures the sandbox executor.
type ExecutorConfig struct {
	Type          string           `json:"type"`            // "local" | "cube" | "container" | "e2b"
	TimeoutSec    int              `json:"timeout_sec"`     // per-command timeout in seconds
	MaxOutputMB   int              `json:"max_output_mb"`   // output size limit in MB
	MaxArtifactMB int              `json:"max_artifact_mb"` // artifact file size limit in MB (default 10)
	EnvWhitelist  []string         `json:"env_whitelist"`   // allowed environment variable names
	Commands      []SandboxCommand `json:"commands"`        // commands to execute
}

// LLMConfig configures the LLM analyzer.
type LLMConfig struct {
	ModelName        string  `json:"model_name"`         // model identifier for LLM inference
	Temperature      float64 `json:"temperature"`        // sampling temperature (0.0 ~ 2.0)
	MaxTokens        int     `json:"max_tokens"`         // maximum tokens in the response
	SystemPrompt     string  `json:"system_prompt"`      // system-level instruction prompt
	MockMode         bool    `json:"mock_mode"`          // if true, use mock findings instead of real LLM
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
	Enabled     bool     `json:"enabled"`     // whether sanitization is enabled
	Patterns    []string `json:"patterns"`    // regex patterns for matching sensitive data
	Replacement string   `json:"replacement"` // text to replace matched content with
}

// DatabaseConfig configures database backend.
type DatabaseConfig struct {
	Driver string `json:"driver"` // sqlite|postgres|mysql
	DSN    string `json:"dsn"`    // data source name (connection string)
}

// SkillConfig configures skill loading.
type SkillConfig struct {
	Dir        string `json:"dir"`         // root directory for skill definitions
	RulesGlob  string `json:"rules_glob"`  // glob pattern for rule files
	ScriptsDir string `json:"scripts_dir"` // directory containing sandbox scripts
}

// TelemetryConfig configures telemetry (reserved).
type TelemetryConfig struct {
	Enabled  bool   `json:"enabled"`  // whether telemetry is enabled
	Exporter string `json:"exporter"` // otlp|stdout|none
	Endpoint string `json:"endpoint"` // telemetry exporter endpoint URL
}

// ── Report ──

// ReviewReport is the aggregate report generated after a review.
type ReviewReport struct {
	TaskID               string            `json:"task_id"`               // unique review task identifier
	FindingsCount        int               `json:"findings_count"`        // total number of findings
	WarningsCount        int               `json:"warnings_count"`        // total number of warnings
	SeverityDistribution map[string]int    `json:"severity_distribution"` // count by severity level
	CategoryDistribution map[string]int    `json:"category_distribution"` // count by category
	Summary              string            `json:"summary"`               // human-readable summary of the review
	NodeTimings          map[string]int64  `json:"node_timings_ms"`       // per-node execution time in ms
	PermissionSummary    PermissionSummary `json:"permission_summary"`    // aggregated permission decisions
	SandboxSummary       []SandboxSummary  `json:"sandbox_summary"`       // per-command sandbox summaries
}

// PermissionSummary aggregates permission decisions for the report.
type PermissionSummary struct {
	Total   int `json:"total"`              // total number of permission decisions
	Allowed int `json:"allowed"`            // count of allowed commands
	Denied  int `json:"denied"`             // count of denied commands
	NeedsHR int `json:"needs_human_review"` // count of commands needing human review
}

// SandboxSummary is the per-command sandbox execution summary.
type SandboxSummary struct {
	Command    string `json:"command"`     // command that was executed
	ExitCode   int    `json:"exit_code"`   // process exit code
	DurationMs int64  `json:"duration_ms"` // execution duration in milliseconds
	TimedOut   bool   `json:"timed_out"`   // whether the command timed out
	ErrorType  string `json:"error_type"`  // error classification if any
}
