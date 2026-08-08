//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package orchestrator owns the end-to-end review task lifecycle.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/telemetry"
)

// RuleReviewer is the deterministic rule capability consumed by Orchestrator.
type RuleReviewer interface {
	Review(input.Diff, rules.Snapshots) ([]findings.Candidate, error)
}

// SandboxRunner is the bounded checker capability consumed by Orchestrator.
type SandboxRunner interface {
	Run(context.Context, sandbox.Request) (sandbox.Result, error)
}

// AssistFunc optionally produces model-assisted candidates from loaded input.
type AssistFunc func(context.Context, string, input.Loaded) ([]findings.Candidate, error)

// LoadFunc obtains one bounded, normalized review input.
type LoadFunc func(context.Context) (input.Loaded, error)

// Config supplies all host-owned review dependencies.
type Config struct {
	TaskID          string
	Mode            review.Mode
	Store           review.ReviewStore
	Load            LoadFunc
	Rules           RuleReviewer
	Sandbox         SandboxRunner
	Assist          AssistFunc
	Artifacts       artifact.Service
	ArtifactSession artifact.SessionInfo
	OutputDirectory string
	Now             func() time.Time
	Telemetry       *telemetry.ReviewTracer
}

// Result contains the canonical document, publication records, and persisted
// review reconstructed after successful completion.
type Result struct {
	Document  report.Document
	Published report.Published
	Stored    review.StoredReview
}

// Orchestrator executes one configured review task exactly once.
type Orchestrator struct {
	config Config
	run    bool
}

// New validates required dependencies without performing review work.
func New(config Config) (*Orchestrator, error) {
	if config.TaskID == "" || redact.String(config.TaskID) != config.TaskID {
		return nil, errors.New("new review orchestrator: invalid task id")
	}
	switch config.Mode {
	case review.ModeRuleOnly:
	case review.ModeFakeModel, review.ModeModel:
		if config.Assist == nil {
			return nil, errors.New("new review orchestrator: assistant is required for model mode")
		}
	default:
		return nil, errors.New("new review orchestrator: invalid mode")
	}
	if config.Store == nil || config.Load == nil || config.Rules == nil ||
		config.Sandbox == nil || config.Artifacts == nil {
		return nil, errors.New("new review orchestrator: pipeline dependencies are required")
	}
	if config.OutputDirectory == "" {
		return nil, errors.New("new review orchestrator: output directory is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Telemetry == nil {
		config.Telemetry = telemetry.New(nil)
	}
	return &Orchestrator{config: config}, nil
}

// Run executes the configured task. A task record is committed before input
// processing, and every later terminal error is persisted with redaction.
func (o *Orchestrator) Run(ctx context.Context) (result Result, err error) {
	if ctx == nil {
		return Result{}, errors.New("run review: context is required")
	}
	if o.run {
		return Result{}, errors.New("run review: orchestrator is single use")
	}
	o.run = true
	ctx, rootSpan := o.config.Telemetry.StartReview(ctx, o.config.Mode)
	defer rootSpan.End()

	createdAt := o.now()
	task := review.Task{
		SchemaVersion: review.SchemaVersion,
		ID:            o.config.TaskID,
		Status:        review.TaskStatusPending,
		Phase:         review.PhaseCreated,
		Mode:          o.config.Mode,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}
	if err := o.config.Store.CreateTask(ctx, task); err != nil {
		return Result{}, fmt.Errorf("create review task: %w", redact.Error(err))
	}
	currentPhase := review.PhaseCreated
	fail := func(cause error) (Result, error) {
		safeErr := redact.Error(cause)
		terminalCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var terminalErr error
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			terminalErr = o.config.Store.CancelTask(
				terminalCtx, task.ID, currentPhase, safeErr.Error(), o.now())
		} else {
			terminalErr = o.config.Store.FailTask(
				terminalCtx, task.ID, currentPhase, safeErr.Error(), o.now())
		}
		if terminalErr != nil {
			return Result{}, errors.Join(safeErr, redact.Error(terminalErr))
		}
		return Result{}, safeErr
	}
	transition := func(phase review.Phase) error {
		phaseCtx, span := o.config.Telemetry.StartPhase(ctx, phase)
		defer span.End()
		if err := o.config.Store.TransitionPhase(phaseCtx, task.ID, phase, o.now()); err != nil {
			o.config.Telemetry.AddOutcome(span, "error", "storage_error")
			return err
		}
		currentPhase = phase
		o.config.Telemetry.AddOutcome(span, "success", "none")
		return nil
	}

	if err := transition(review.PhaseInput); err != nil {
		return fail(fmt.Errorf("enter input phase: %w", err))
	}
	loaded, err := o.config.Load(ctx)
	if err != nil {
		return fail(fmt.Errorf("load review input: %w", err))
	}
	reviewInput := review.ReviewInput{
		SchemaVersion: review.SchemaVersion,
		TaskID:        task.ID,
		Source:        loaded.Source,
		Digest:        loaded.Digest,
		ChangedFiles:  changedFiles(loaded.Diff),
	}
	if err := reviewInput.Validate(); err != nil {
		return fail(err)
	}

	snapshots := snapshotMap(loaded.Snapshots)
	if err := transition(review.PhaseRules); err != nil {
		return fail(fmt.Errorf("enter rules phase: %w", err))
	}
	candidates, err := o.config.Rules.Review(loaded.Diff, snapshots)
	if err != nil {
		return fail(fmt.Errorf("run deterministic rules: %w", err))
	}

	if err := transition(review.PhaseSandbox); err != nil {
		return fail(fmt.Errorf("enter sandbox phase: %w", err))
	}
	sandboxResult, err := o.config.Sandbox.Run(ctx, sandbox.Request{
		TaskID: task.ID,
		Diff:   loaded.Diff,
		Files:  sandboxFiles(loaded.Snapshots),
	})
	for _, run := range sandboxResult.Runs {
		if recordErr := o.config.Store.RecordSandboxRun(ctx, run); recordErr != nil {
			return fail(fmt.Errorf("record sandbox run: %w", recordErr))
		}
	}
	if err != nil {
		return fail(fmt.Errorf("run sandbox checks: %w", err))
	}
	candidates = append(candidates, sandboxResult.Candidates...)

	modelDegraded := false
	if o.config.Mode != review.ModeRuleOnly {
		if err := transition(review.PhaseAssist); err != nil {
			return fail(fmt.Errorf("enter assist phase: %w", err))
		}
		assisted, assistErr := o.config.Assist(ctx, task.ID, loaded)
		if assistErr != nil {
			modelDegraded = true
		} else {
			candidates = append(candidates, assisted...)
		}
	}

	if err := transition(review.PhaseFinalize); err != nil {
		return fail(fmt.Errorf("enter finalize phase: %w", err))
	}
	canonicalFindings, err := findings.Normalize(task.ID, loaded.Diff, candidates)
	if err != nil {
		return fail(fmt.Errorf("normalize review findings: %w", err))
	}
	partial, err := o.config.Store.GetReview(ctx, task.ID)
	if err != nil {
		return fail(fmt.Errorf("load review records for finalization: %w", err))
	}
	completedAt := o.now()
	completedTask := task
	completedTask.Status = review.TaskStatusCompleted
	completedTask.Phase = review.PhaseCompleted
	completedTask.UpdatedAt = completedAt
	metrics := buildMetrics(
		createdAt, completedAt, partial.Report.SandboxRuns,
		partial.Report.GovernanceDecisions, canonicalFindings, modelDegraded,
	)
	conclusion := buildConclusion(metrics, modelDegraded)
	canonicalReport := review.Report{
		SchemaVersion:       review.SchemaVersion,
		Task:                completedTask,
		Input:               reviewInput,
		SandboxRuns:         partial.Report.SandboxRuns,
		GovernanceDecisions: partial.Report.GovernanceDecisions,
		Findings:            canonicalFindings,
		Metrics:             metrics,
		Conclusion:          conclusion,
	}
	document, err := report.Finalize(canonicalReport)
	if err != nil {
		return fail(err)
	}
	if err := report.WriteLocal(o.config.OutputDirectory, document); err != nil {
		return fail(err)
	}
	published, err := report.Publish(
		ctx, o.config.Artifacts, o.config.ArtifactSession, task.ID, document)
	if err != nil {
		return fail(err)
	}
	completion := review.Completion{
		TaskID:               task.ID,
		UpdatedAt:            completedAt,
		Input:                reviewInput,
		Findings:             canonicalFindings,
		PublicationArtifacts: published.Artifacts,
		Metrics:              metrics,
		Report:               published.Metadata,
		Conclusion:           conclusion,
	}
	if err := o.config.Store.CompleteTask(ctx, completion); err != nil {
		return fail(fmt.Errorf("complete review task: %w", err))
	}
	stored, err := o.config.Store.GetReview(ctx, task.ID)
	if err != nil {
		return Result{}, fmt.Errorf("get completed review: %w", redact.Error(err))
	}
	return Result{Document: document, Published: published, Stored: stored}, nil
}

func (o *Orchestrator) now() time.Time {
	return o.config.Now().UTC()
}

func changedFiles(diff input.Diff) []string {
	set := make(map[string]struct{}, len(diff.Files))
	for _, file := range diff.Files {
		name := file.NewPath
		if name == "" {
			name = file.OldPath
		}
		if name != "" {
			set[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func snapshotMap(source []input.Snapshot) rules.Snapshots {
	result := make(rules.Snapshots, len(source))
	for _, snapshot := range source {
		result[rules.SnapshotKey{Layer: snapshot.Layer, Path: snapshot.Path}] =
			append([]byte(nil), snapshot.Content...)
	}
	return result
}

func sandboxFiles(source []input.Snapshot) []codeexecutor.PutFile {
	selected := make(map[string]input.Snapshot, len(source))
	for _, snapshot := range source {
		current, exists := selected[snapshot.Path]
		if !exists || layerRank(snapshot.Layer) > layerRank(current.Layer) {
			selected[snapshot.Path] = snapshot
		}
	}
	paths := make([]string, 0, len(selected))
	for name := range selected {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	result := make([]codeexecutor.PutFile, 0, len(paths))
	for _, name := range paths {
		result = append(result, codeexecutor.PutFile{
			Path: name, Content: append([]byte(nil), selected[name].Content...), Mode: 0o444,
		})
	}
	return result
}

func layerRank(layer review.ChangeLayer) int {
	switch layer {
	case review.ChangeLayerWorktree:
		return 3
	case review.ChangeLayerStaged:
		return 2
	case review.ChangeLayerUnified:
		return 1
	default:
		return 0
	}
}

func buildMetrics(
	createdAt time.Time,
	completedAt time.Time,
	runs []review.SandboxRun,
	decisions []review.GovernanceDecision,
	canonicalFindings []review.Finding,
	modelDegraded bool,
) review.Metrics {
	metrics := review.Metrics{
		SchemaVersion:   review.SchemaVersion,
		TotalDuration:   completedAt.Sub(createdAt),
		ToolInvocations: len(runs),
		FindingTotal:    len(canonicalFindings),
		SeverityCounts:  make(map[review.Severity]int),
		ErrorTypeCounts: make(map[string]int),
	}
	for _, run := range runs {
		metrics.SandboxDuration += run.Duration
		switch run.Status {
		case review.SandboxStatusFailed:
			metrics.ErrorTypeCounts["sandbox_failed"]++
		case review.SandboxStatusTimedOut:
			metrics.ErrorTypeCounts["sandbox_timeout"]++
		case review.SandboxStatusUnavailable:
			metrics.ErrorTypeCounts["sandbox_unavailable"]++
		case review.SandboxStatusCanceled:
			metrics.ErrorTypeCounts["sandbox_canceled"]++
		}
		if run.Truncated {
			metrics.ErrorTypeCounts["sandbox_truncated"]++
		}
	}
	for _, decision := range decisions {
		if decision.Action == review.DecisionActionDeny ||
			decision.Action == review.DecisionActionAsk {
			metrics.PermissionBlocks++
		}
	}
	for _, finding := range canonicalFindings {
		metrics.SeverityCounts[finding.Severity]++
		switch finding.Disposition {
		case review.DispositionWarning:
			metrics.WarningCount++
		case review.DispositionNeedsHumanReview:
			metrics.HumanReviewCount++
		}
	}
	if modelDegraded {
		metrics.ErrorTypeCounts["model_error"]++
	}
	return metrics
}

func buildConclusion(metrics review.Metrics, modelDegraded bool) string {
	return fmt.Sprintf(
		"review completed: %d findings, %d warnings, %d human review items, "+
			"%d governance blocks, model degraded=%t",
		metrics.FindingTotal,
		metrics.WarningCount,
		metrics.HumanReviewCount,
		metrics.PermissionBlocks,
		modelDegraded,
	)
}
