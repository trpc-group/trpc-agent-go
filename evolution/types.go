//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evolution provides an async review pipeline that extracts reusable
// skills from completed sessions and persists them as managed
// SKILL.md files.
package evolution

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// sessionStateKeyLastReviewAt stores the last reviewed timestamp in session
// state for incremental delta scanning.
const sessionStateKeyLastReviewAt = "evolution:last_review_at"

// Service reviews completed sessions and persists reusable procedures.
//
// EnqueueLearningJob takes the work as a LearningJob struct so callers can
// optionally attach an evaluator Outcome (benchmark runners, RLHF
// pipelines, "thumbs down" feedback, etc.). Online services without an
// evaluator pass LearningJob{Session: sess}; the reviewer then behaves
// as a transcript-only reviewer.
type Service interface {
	EnqueueLearningJob(ctx context.Context, job LearningJob) error
	Close() error
}

// LearningJob is the unit of work submitted to the async reviewer.
//
// The exported shape carries only Session and (optionally) Outcome; the
// per-job context is captured internally by the worker so callers do
// not have to thread it through this struct.
type LearningJob struct {
	// Session is the session whose recent delta should be reviewed. Required.
	Session *session.Session

	// Outcome is the optional caller-observed verdict for this session.
	// Online services without an evaluator leave this nil; benchmark
	// runners and RLHF pipelines fill it so the reviewer can decide
	// whether a failed run is worth learning from (and how).
	//
	// When non-nil, the reviewer renders an explicit "## Session outcome"
	// section in its prompt and switches to failure-aware learning rules
	// (capture pitfalls from failures; never invent steps the agent did
	// not actually execute). When nil, the reviewer prompt is identical
	// to the transcript-only baseline.
	Outcome *Outcome

	// Scope optionally pins the skill sharing boundary for this job. When
	// empty, the service derives it from Session.AppName/UserID according to
	// the service's skill scope mode.
	Scope skill.SkillScope
}

// ReviewPolicyInput contains the information a ReviewPolicy can use to decide
// whether a session delta should be sent to the reviewer.
type ReviewPolicyInput struct {
	// AppName is copied from the session for convenience.
	AppName string

	// UserID is copied from the session for convenience.
	UserID string

	// SessionID is copied from the session for traceability.
	SessionID string

	// Scope is the resolved skill-sharing scope for this job. It is zero when
	// the service is configured without scoped skill routing.
	Scope skill.SkillScope

	// Scoped reports whether Scope is active for this job.
	Scoped bool

	// Outcome is the optional caller-observed verdict for this session.
	Outcome *Outcome

	// ReviewContext contains the heuristic signals extracted from the session
	// delta, including messages, compact transcript, and tool-call counts.
	ReviewContext *ReviewContext
}

// OutcomeStatus is a typed enum that classifies the evaluator verdict.
// Empty string means "no signal" and is treated as "unknown".
type OutcomeStatus string

// Recommended OutcomeStatus values. Callers may use other strings if
// their evaluator reports a domain-specific status; the reviewer prompt
// renders the value verbatim either way.
const (
	OutcomeUnknown    OutcomeStatus = ""
	OutcomeSuccess    OutcomeStatus = "success"
	OutcomePartial    OutcomeStatus = "partial"
	OutcomeFail       OutcomeStatus = "fail"
	OutcomeAgentError OutcomeStatus = "agent_error"
)

// Outcome carries the evaluator verdict that drives failure-aware
// reviewer decisions. It mirrors the contextual signal that an
// in-flow reviewer (e.g. Hermes's main-conversation skill review)
// would naturally read off the agent's natural-language self-report;
// trpc-agent-go's reviewer is a transcript replay and cannot infer
// pass/fail from tool calls alone, so callers with an evaluator should
// pass this through.
type Outcome struct {
	// Status is the evaluator verdict; "" means "no signal".
	Status OutcomeStatus `json:"status,omitempty"`

	// Score is an optional normalized numeric metric on a 0..1 scale.
	Score *float64 `json:"score,omitempty"`

	// Notes is a short evaluator one-liner (e.g.
	// "economic_snapshot.json not found"). A best-effort redactor runs
	// before the reviewer prompt is built, but callers should still avoid
	// attaching raw secrets or sensitive PII when possible.
	Notes string `json:"notes,omitempty"`

	// Evaluator names the source of the verdict ("skillcraft",
	// "user-thumbsdown", "regression-test", ...). Reviewer prompt may
	// weigh different evaluators differently in the future; today it
	// is rendered for traceability only.
	Evaluator string `json:"evaluator,omitempty"`
}

// ReviewInput holds everything the reviewer needs to decide what to extract.
type ReviewInput struct {
	AppName    string          `json:"app_name"`
	UserID     string          `json:"user_id"`
	SessionID  string          `json:"session_id"`
	Messages   []model.Message `json:"messages,omitempty"`
	Transcript []ReviewMessage `json:"transcript,omitempty"`
	// ExistingSkills carries the reviewer-facing view of the current skill
	// library. Each entry includes the skill name, description, and a
	// truncated body excerpt so the reviewer can detect substantive
	// duplicates instead of relying on name/description matching alone.
	// The service controls the per-skill body budget via
	// WithExistingSkillBodyMaxChars (set negative to omit bodies).
	ExistingSkills []ExistingSkill `json:"existing_skills,omitempty"`
	// Outcome is the caller-observed evaluator verdict for the session
	// being reviewed (nil when no evaluator was attached). The reviewer
	// uses it to switch into failure-aware learning when set; when nil
	// the prompt has no "## Session outcome" section and the reviewer
	// behaves as a transcript-only baseline.
	Outcome *Outcome `json:"outcome,omitempty"`
}

// ExistingSkill is the reviewer-facing view of one skill currently in the
// repository. It is intentionally a flat, transport-friendly struct so
// reviewer implementations (LLM-backed, rule-based, mocks) and tests can
// construct it directly without depending on the skill package.
//
// BodyExcerpt is the head of the SKILL.md body truncated to the
// WithExistingSkillBodyMaxChars budget. It is meant for
// substantive duplicate detection (does the proposed workflow already
// exist with different parameters?) and is intentionally not the full
// body — that would explode the reviewer prompt as the library grows.
// An empty BodyExcerpt means the worker chose not to include bodies
// (budget = 0) or the on-disk body could not be loaded.
type ExistingSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	BodyExcerpt string `json:"body_excerpt,omitempty"`
}

// ReviewDecision is the structured output of the reviewer model.
//
// Note: evolution intentionally does NOT extract or persist durable facts.
// Fact-style memory is the responsibility of `memory.Service` + the
// auto-memory extractor (see memory/<backend>.WithExtractor); evolution
// owns only the skill library.
type ReviewDecision struct {
	SkipReason string       `json:"skip_reason,omitempty"`
	Skills     []*SkillSpec `json:"skills,omitempty"`
	// Updates replace an existing skill in full. Each entry's Name must
	// match a skill currently in the repository; entries that do not are
	// dropped during normalization.
	Updates []*SkillUpdate `json:"updates,omitempty"`
	// Deletions remove existing skills from the repository by name.
	Deletions []string `json:"deletions,omitempty"`
}

// SkillUpdate replaces an existing skill in full. Patch-style updates can
// be added later without breaking this shape (see SkillPatch).
type SkillUpdate struct {
	// Name must match the Name of an existing skill in the repository.
	Name string `json:"name"`
	// NewSpec is the full replacement skill body. NewSpec.Name is forced to
	// match Name during apply so the on-disk directory does not move.
	NewSpec *SkillSpec `json:"new_spec"`
	// Reason is a free-form audit string explaining why the update is needed.
	Reason string `json:"reason,omitempty"`
}

// SkillSpec describes a reusable skill.
type SkillSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	WhenToUse   string   `json:"when_to_use"`
	Steps       []string `json:"steps"`
	Pitfalls    []string `json:"pitfalls,omitempty"`
}

// ReviewMessage is a compact, tool-aware transcript entry used by the
// reviewer and by offline benchmarks.
type ReviewMessage struct {
	Role      model.Role       `json:"role"`
	Content   string           `json:"content,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
	ToolID    string           `json:"tool_id,omitempty"`
	ToolCalls []ReviewToolCall `json:"tool_calls,omitempty"`
}

// ReviewToolCall captures the tool name and raw arguments so evolution logic
// can reason about whether a turn created or edited a managed skill.
type ReviewToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// ReviewContext captures heuristic signals from the session delta that the
// ReviewPolicy uses to decide whether a review is worthwhile.
type ReviewContext struct {
	LatestTs          time.Time
	Messages          []model.Message
	Transcript        []ReviewMessage
	ToolCallCount     int
	HasUserCorrection bool
	HasRecoveredError bool
}
