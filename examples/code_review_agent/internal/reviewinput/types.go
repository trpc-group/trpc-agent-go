// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package reviewinput prepares every supported code-review input shape for the
// same task, Agent message, artifact, and workspace execution path.
package reviewinput

import (
	"context"
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

const (
	// InputKindDiffFile identifies an explicit diff or patch as the input fact.
	InputKindDiffFile = "diff_file"
	// InputKindRepoPath identifies changes collected from a Git worktree.
	InputKindRepoPath = "repo_path"
	// InputKindFixture identifies a named public or test review fixture.
	InputKindFixture = "fixture"

	// ReviewModePatchOnly means no complete repository snapshot is available.
	ReviewModePatchOnly = "patch_only"
	// ReviewModeRepoBacked means workspace tools can inspect a task-owned repo.
	ReviewModeRepoBacked = "repo_backed"

	inputArtifactName = "review_input.diff"
)

// Spec expresses the user's input choice. InputKind is derived by
// the preparer so callers cannot create contradictory states.
type Spec struct {
	DiffFile  string   // Path to diff file
	RepoPath  string   // Path to repository
	Fixture   string   // Name of fixture to use
	Paths     []string // Specific file paths to review
	PathsFile string   // Path to file containing file paths to review (one path per line)
}

// TaskScope is the stable identity shared by Review Store, Session Service,
// Artifact Service, and the workspace created for one review task.
type TaskScope struct {
	TaskID  string
	AppName string
	UserID  string
}

// ChangedFile describes one file participating in the review scope.
type ChangedFile struct {
	Path               string `json:"path"`
	OldPath            string `json:"old_path,omitempty"`
	Status             string `json:"status"`
	Language           string `json:"language,omitempty"`
	IsGo               bool   `json:"is_go"`
	IsTest             bool   `json:"is_test"`
	HasCompleteContext bool   `json:"has_complete_context"`
	HunkCount          int    `json:"hunk_count"`
	AddedLines         int    `json:"added_lines"`
	ChangedLines       int    `json:"changed_lines"`
	DeletedLines       int    `json:"deleted_lines"`
	Binary             bool   `json:"binary"`
}

// ChangedHunk is the Agent-visible, masked representation of a unified-diff
// hunk. CandidateLines contains new-file line numbers for added lines.
type ChangedHunk struct {
	ID             string `json:"id"`
	File           string `json:"file"`
	OldStart       int    `json:"old_start"`
	OldLines       int    `json:"old_lines"`
	NewStart       int    `json:"new_start"`
	NewLines       int    `json:"new_lines"`
	Section        string `json:"section,omitempty"`
	Body           string `json:"body"`
	CandidateLines []int  `json:"candidate_lines"`
}

// GoPackage captures package context without executing user code on the host.
type GoPackage struct {
	ModulePath       string `json:"module_path,omitempty"`
	ModuleRoot       string `json:"module_root,omitempty"`
	Directory        string `json:"directory"`
	PackageName      string `json:"package_name,omitempty"`
	ImportPath       string `json:"import_path,omitempty"`
	SuggestedTestArg string `json:"suggested_test_arg,omitempty"`
	Complete         bool   `json:"complete"`
}

// SecretSignal keeps the location and masked evidence needed to review a
// credential leak while excluding the credential itself.
type SecretSignal struct {
	Kind        string  `json:"kind"`
	RuleID      string  `json:"rule_id"`
	File        string  `json:"file"`
	Line        int     `json:"line"`
	Evidence    string  `json:"evidence"`
	Confidence  float64 `json:"confidence"`
	Fingerprint string  `json:"fingerprint"`
}

// RedactionSummary is safe to persist in the task projection.
type RedactionSummary struct {
	Count  int            `json:"count"`
	ByKind map[string]int `json:"by_kind,omitempty"`
}

// parsedInput is the complete structured result of input parsing. All
// text fields that can contain source content are already masked.
type parsedInput struct {
	ChangedFiles  []ChangedFile    `json:"changed_files"`
	ChangedHunks  []ChangedHunk    `json:"changed_hunks"`
	GoPackages    []GoPackage      `json:"go_packages"`
	SecretSignals []SecretSignal   `json:"secret_signals"`
	Redactions    RedactionSummary `json:"redactions"`
}

// inputSummary is the bounded Review Store projection. Complete masked input
// content belongs to Artifact Service rather than the task row.
type inputSummary struct {
	InputKind      string           `json:"input_kind"`
	ReviewMode     string           `json:"review_mode"`
	RequestedPaths []string         `json:"requested_paths,omitempty"`
	ChangedFiles   []ChangedFile    `json:"changed_files"`
	GoPackages     []GoPackage      `json:"go_packages"`
	HunkCount      int              `json:"hunk_count"`
	CandidateLines int              `json:"candidate_line_count"`
	SecretSignals  []SecretSignal   `json:"secret_signals"`
	Redactions     RedactionSummary `json:"redactions"`
}

// PreparedInput is the narrow result consumed by reviewer orchestration.
// Close removes only task-owned temporary input state; it never touches the
// user's repository.
type PreparedInput struct {
	InputKind       string
	ReviewMode      string
	Message         string
	SummaryJSON     string
	ArtifactName    string
	ArtifactVersion int
	Bootstrap       codeexecutor.WorkspaceBootstrapSpec
	parsed          parsedInput
	cleanup         func() error
}

// Close releases task-owned temporary snapshot files.
func (p *PreparedInput) Close() error {
	if p == nil || p.cleanup == nil {
		return nil
	}
	cleanup := p.cleanup
	p.cleanup = nil
	return cleanup()
}

// ArtifactStore is the small portion of artifact.Service used while preparing
// review input. artifact.Service itself satisfies this interface.
type ArtifactStore interface {
	SaveArtifact(context.Context, artifact.SessionInfo, string, *artifact.Artifact) (version int, err error)
}

// Limits bound model-visible context without limiting the complete masked diff
// saved to Artifact Service and staged into the workspace.
type Limits struct {
	MaxMessageBytes int
	MaxFiles        int
	MaxHunks        int
	MaxHunkBytes    int
	GitTimeout      time.Duration
}

func (l Limits) withDefaults() Limits {
	if l.MaxMessageBytes <= 0 {
		l.MaxMessageBytes = 32 * 1024
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = 80
	}
	if l.MaxHunks <= 0 {
		l.MaxHunks = 120
	}
	if l.MaxHunkBytes <= 0 {
		l.MaxHunkBytes = 4 * 1024
	}
	if l.GitTimeout <= 0 {
		l.GitTimeout = 30 * time.Second
	}
	return l
}

func (s TaskScope) validate() error {
	if s.TaskID == "" || s.AppName == "" || s.UserID == "" {
		return errors.New("review input task scope requires task id, app name, and user id")
	}
	return nil
}
