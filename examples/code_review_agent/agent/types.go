package agent

import "time"

const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"

	DecisionAllow            = "allow"
	DecisionDeny             = "deny"
	DecisionAsk              = "ask"
	DecisionNeedsHumanReview = "needs_human_review"

	TaskStatusRunning  = "running"
	TaskStatusComplete = "complete"
	TaskStatusFailed   = "failed"
)

type Config struct {
	DiffFile            string
	RepoPath            string
	Files               []string
	SkillPath           string
	Runtime             string
	OutDir              string
	StorePath           string
	DryRun              bool
	RuleOnly            bool
	EnableStaticcheck   bool
	Timeout             time.Duration
	MaxOutputBytes      int64
	ForceSandboxFailure bool
}

type ReviewTask struct {
	ID           string    `json:"id"`
	Status       string    `json:"status"`
	InputKind    string    `json:"input_kind"`
	InputSummary string    `json:"input_summary"`
	RepoPath     string    `json:"repo_path,omitempty"`
	SkillPath    string    `json:"skill_path"`
	Runtime      string    `json:"runtime"`
	DryRun       bool      `json:"dry_run"`
	RuleOnly     bool      `json:"rule_only"`
	StartedAt    time.Time `json:"started_at"`
	CompletedAt  time.Time `json:"completed_at,omitempty"`
	Error        string    `json:"error,omitempty"`
}

type ReviewInput struct {
	RawDiff string        `json:"-"`
	Files   []ChangedFile `json:"files"`
	Summary InputSummary  `json:"summary"`
}

type InputSummary struct {
	DiffSHA256       string   `json:"diff_sha256"`
	FileCount        int      `json:"file_count"`
	GoFileCount      int      `json:"go_file_count"`
	AddedLineCount   int      `json:"added_line_count"`
	DeletedLineCount int      `json:"deleted_line_count"`
	Packages         []string `json:"packages"`
}

type ChangedFile struct {
	OldPath string `json:"old_path,omitempty"`
	NewPath string `json:"new_path"`
	Hunks   []Hunk `json:"hunks"`
	Package string `json:"package,omitempty"`
}

type Hunk struct {
	OldStart int        `json:"old_start"`
	OldLines int        `json:"old_lines"`
	NewStart int        `json:"new_start"`
	NewLines int        `json:"new_lines"`
	Header   string     `json:"header,omitempty"`
	Lines    []DiffLine `json:"lines"`
}

type DiffLine struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Content string `json:"content"`
}

type Finding struct {
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
	NeedsHuman     bool    `json:"needs_human_review,omitempty"`
}

type PermissionDecision struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Tool      string    `json:"tool"`
	Command   []string  `json:"command"`
	Decision  string    `json:"decision"`
	Risk      string    `json:"risk"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type SandboxRun struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	Runtime         string    `json:"runtime"`
	Tool            string    `json:"tool"`
	Command         []string  `json:"command"`
	Status          string    `json:"status"`
	ExitCode        int       `json:"exit_code"`
	DurationMS      int64     `json:"duration_ms"`
	Output          string    `json:"output"`
	ErrorType       string    `json:"error_type,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	OutputTruncated bool      `json:"output_truncated"`
}

type Artifact struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

type MonitoringSummary struct {
	TaskID                string         `json:"task_id"`
	TotalDurationMS       int64          `json:"total_duration_ms"`
	SandboxDurationMS     int64          `json:"sandbox_duration_ms"`
	ToolCallCount         int            `json:"tool_call_count"`
	PermissionIntercepts  int            `json:"permission_intercepts"`
	FindingCount          int            `json:"finding_count"`
	WarningCount          int            `json:"warning_count"`
	NeedsHumanReviewCount int            `json:"needs_human_review_count"`
	SeverityDistribution  map[string]int `json:"severity_distribution"`
	ExceptionDistribution map[string]int `json:"exception_distribution"`
	CreatedAt             time.Time      `json:"created_at"`
}

type ReviewReport struct {
	Task                ReviewTask           `json:"task"`
	Input               InputSummary         `json:"input"`
	Findings            []Finding            `json:"findings"`
	Warnings            []Finding            `json:"warnings"`
	NeedsHumanReview    []Finding            `json:"needs_human_review"`
	SandboxRuns         []SandboxRun         `json:"sandbox_runs"`
	PermissionDecisions []PermissionDecision `json:"permission_decisions"`
	Artifacts           []Artifact           `json:"artifacts"`
	Monitoring          MonitoringSummary    `json:"monitoring"`
	FinalConclusion     string               `json:"final_conclusion"`
	ReportJSONPath      string               `json:"report_json_path,omitempty"`
	ReportMarkdownPath  string               `json:"report_markdown_path,omitempty"`
}
