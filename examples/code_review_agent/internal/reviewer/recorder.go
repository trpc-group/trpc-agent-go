//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const runtimeStateReviewTaskID = "review_task_id"

type reviewRecorder struct {
	store     ReviewStore
	sanitizer *redact.Sanitizer
	clock     func() time.Time
}

func newReviewRecorder(store ReviewStore, sanitizer *redact.Sanitizer) *reviewRecorder {
	return &reviewRecorder{store: store, sanitizer: sanitizer, clock: time.Now}
}

func (r *reviewRecorder) CreateTask(ctx context.Context, task store.ReviewTaskRecord) error {
	if r == nil || r.store == nil {
		return errors.New("review recorder requires a store")
	}
	if task.StartedAt.IsZero() {
		task.StartedAt = r.clock()
	}
	return r.store.SaveTask(ctx, task)
}

func (r *reviewRecorder) RecordPermissionDecision(ctx context.Context, taskID string, decision store.PermissionDecisionRecord) error {
	if r == nil || r.store == nil {
		return errors.New("review recorder requires a store")
	}
	if decision.DecidedAt.IsZero() {
		decision.DecidedAt = r.clock()
	}
	decision.CommandPreview = r.mask(decision.CommandPreview)
	decision.Reason = r.mask(decision.Reason)
	return r.store.SavePermissionDecision(ctx, taskID, decision)
}

func (r *reviewRecorder) RecordInput(ctx context.Context, taskID string, input store.TaskInputRecord) error {
	if r == nil || r.store == nil {
		return errors.New("review recorder requires a store")
	}
	input.InputSummaryJSON = r.mask(input.InputSummaryJSON)
	return r.store.UpdateTaskInput(ctx, taskID, input)
}

func (r *reviewRecorder) RecordSandboxRun(ctx context.Context, taskID string, run store.SandboxRunRecord) error {
	if r == nil || r.store == nil {
		return errors.New("review recorder requires a store")
	}
	run.CommandPreview = r.mask(run.CommandPreview)
	run.StdoutSummary = r.mask(run.StdoutSummary)
	run.StderrSummary = r.mask(run.StderrSummary)
	run.ErrorMessage = r.mask(run.ErrorMessage)
	return r.store.SaveSandboxRun(ctx, taskID, run)
}

// FinalizeTask masks only the user-controlled error text. ErrorType is produced
// by the orchestration boundary before masking so cancellation, timeout, and
// provider failures retain a useful stable category.
func (r *reviewRecorder) FinalizeTask(
	ctx context.Context,
	taskID string,
	finalization store.TaskFinalization,
) error {
	if r == nil || r.store == nil {
		return errors.New("review recorder requires a store")
	}
	finalization.ErrorMessage = r.mask(finalization.ErrorMessage)
	return r.store.FinalizeTask(ctx, taskID, finalization)
}

func (r *reviewRecorder) SubmitReviewResults(
	ctx context.Context,
	taskID string,
	results []store.ReviewResultRecord,
	conclusion string,
) (store.ReviewResultCounts, error) {
	if r == nil || r.store == nil {
		return store.ReviewResultCounts{}, errors.New("review recorder requires a store")
	}
	maskedResults := make([]store.ReviewResultRecord, 0, len(results))
	for _, result := range results {
		result.Severity = r.mask(result.Severity)
		result.Category = r.mask(result.Category)
		result.File = r.mask(result.File)
		result.Title = r.mask(result.Title)
		result.Evidence = r.mask(result.Evidence)
		result.Recommendation = r.mask(result.Recommendation)
		result.Source = r.mask(result.Source)
		result.RuleID = r.mask(result.RuleID)
		if result.CreatedAt.IsZero() {
			result.CreatedAt = r.clock()
		}
		maskedResults = append(maskedResults, result)
	}
	return r.store.SubmitReviewResults(ctx, taskID, maskedResults, r.mask(conclusion))
}

func (r *reviewRecorder) Snapshot(ctx context.Context, taskID string) (store.ReviewSnapshot, error) {
	if r == nil || r.store == nil {
		return store.ReviewSnapshot{}, errors.New("review recorder requires a store")
	}
	return r.store.LoadTaskSnapshot(ctx, taskID)
}

func (r *reviewRecorder) mask(value string) string {
	if r == nil || r.sanitizer == nil || value == "" {
		return value
	}
	masked, _ := r.sanitizer.MaskString(value)
	return masked
}

// reviewTaskIDFromContext extracts the review task ID from the invocation context
func reviewTaskIDFromContext(ctx context.Context) (taskID string, err error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.RunOptions.RuntimeState == nil {
		return "", errors.New("review task id is missing from runtime state")
	}
	value, ok := inv.RunOptions.RuntimeState[runtimeStateReviewTaskID]
	if ok {
		taskID, ok = value.(string)
	}
	if !ok || taskID == "" {
		return "", fmt.Errorf("runtime state %q must be a non-empty string", runtimeStateReviewTaskID)
	}
	return taskID, nil
}
