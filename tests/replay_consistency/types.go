package replayconsistency

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type BackendKind string

const (
	BackendKindSession BackendKind = "session"
	BackendKindMemory  BackendKind = "memory"
)

type OperationKind string

const (
	OperationKindAppendEvent      OperationKind = "append_event"
	OperationKindUpdateState      OperationKind = "update_state"
	OperationKindDeleteState      OperationKind = "delete_state"
	OperationKindClearState       OperationKind = "clear_state"
	OperationKindAddMemory        OperationKind = "add_memory"
	OperationKindUpdateMemory     OperationKind = "update_memory"
	OperationKindDeleteMemory     OperationKind = "delete_memory"
	OperationKindCreateSummary    OperationKind = "create_summary"
	OperationKindAppendTrackEvent OperationKind = "append_track_event"
	OperationKindReadBack         OperationKind = "read_back"
)

type ReplayCase struct {
	Name                string
	Description         string
	Operations          []Operation
	AllowedDiffPatterns []string
	Tags                []string
}

type Operation struct {
	Kind OperationKind

	Event       *event.Event
	StatePatch  session.StateMap
	StateDelete []string
	ClearState  bool

	MemoryAdd    *MemoryWrite
	MemoryUpdate *MemoryWrite
	MemoryDelete *memory.Key

	Summary *session.Summary
	Track   *session.TrackEvent

	FilterKey string
	Note      string
}

type MemoryWrite struct {
	UserKey  memory.UserKey
	MemoryID string
	Content  string
	Topics   []string
	Metadata *memory.Metadata
}

type HarnessOptions struct {
	BaselineBackend string
	LightMode       bool
	MaxCases        int
	SkipEnv         bool
}

type Backend interface {
	Name() string
	Kind() BackendKind
	Supports(feature string) bool
	Close() error
}

type ReplayHarness struct {
	Backends []Backend
	Cases    []ReplayCase
	Options  HarnessOptions
}

type Diff struct {
	CaseName    string    `json:"case_name"`
	Backend     string    `json:"backend"`
	Path        string    `json:"path"`
	Baseline    string    `json:"baseline"`
	Actual      string    `json:"actual"`
	AllowedDiff bool      `json:"allowed_diff"`
	Explanation string    `json:"explanation"`
	SessionID   string    `json:"session_id,omitempty"`
	SummaryID   string    `json:"summary_id,omitempty"`
	SummaryKey  string    `json:"summary_key,omitempty"`
	TrackName   string    `json:"track_name,omitempty"`
	MemoryID    string    `json:"memory_id,omitempty"`
	OccurredAt  time.Time `json:"occurred_at,omitempty"`
}

type CaseResult struct {
	CaseName string
	Backend  string
	Snapshot NormalizedSnapshot
	Diffs    []Diff
	Error    string
}

type Report struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Cases       []string      `json:"cases"`
	Diffs       []Diff        `json:"diffs"`
	Results     []CaseResult  `json:"results,omitempty"`
	Summary     ReportSummary `json:"summary"`
}

type ReportSummary struct {
	CasesRun         int `json:"cases_run"`
	BackendsRun      int `json:"backends_run"`
	DiffCount        int `json:"diff_count"`
	AllowedDiffCount int `json:"allowed_diff_count"`
}
