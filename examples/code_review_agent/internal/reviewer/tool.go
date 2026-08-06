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
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	requestToolPermissionName      = "request_tool_permission"
	submitReviewResultsName        = "submit_review_results"
	permissionStatusGranted        = "granted"
	permissionStatusDenied         = tool.PermissionResultStatusDenied
	permissionStatusApprovalNeeded = tool.PermissionResultStatusApprovalRequired
)

type requestToolPermissionInput struct {
	TargetTool      string                     `json:"target_tool"`
	TargetArguments map[string]json.RawMessage `json:"target_arguments"`
	Reason          string                     `json:"reason"`
}

type requestToolPermissionOutput struct {
	TargetTool      string                     `json:"target_tool"`
	TargetArguments map[string]json.RawMessage `json:"target_arguments"`
	Status          string                     `json:"status"`
}

type submitReviewResultsInput struct {
	Findings         []reviewResultInput `json:"findings" jsonschema:"description=High-confidence findings after a mandatory root-cause merge pass; if one edit fixes two observations submit one finding in the most-specific category and merge the evidence,required"`
	Warnings         []reviewResultInput `json:"warnings" jsonschema:"description=Low-confidence warnings that should not be mixed into findings,required"`
	NeedsHumanReview []reviewResultInput `json:"needs_human_review" jsonschema:"description=Items that require human review before being treated as findings,required"`
	Conclusion       string              `json:"conclusion" jsonschema:"description=Short final conclusion for the current review task,required"`
}

type reviewResultInput struct {
	Severity       string  `json:"severity" jsonschema:"description=Review severity: critical, high, medium, low, or warning"`
	Category       string  `json:"category" jsonschema:"description=Most-specific review category: correctness, security, sensitive_info, concurrency, resource_lifecycle, error_handling, tests, or database_lifecycle. Use database_lifecycle for database/sql resources. Correctness is only a fallback; a missing Close is one lifecycle item and a removed cancellation path plus its unused context is one concurrency item"`
	File           string  `json:"file" jsonschema:"description=Repository-relative file path"`
	Line           int     `json:"line" jsonschema:"description=Line number for the issue, or 0 when no exact line is available"`
	Title          string  `json:"title" jsonschema:"description=Concise title for the review item"`
	Evidence       string  `json:"evidence" jsonschema:"description=Concrete evidence from the diff, tool output, or inspected code"`
	Recommendation string  `json:"recommendation" jsonschema:"description=Actionable fix recommendation,required"`
	Confidence     float64 `json:"confidence" jsonschema:"description=Confidence score from 0 to 1"`
	Source         string  `json:"source" jsonschema:"description=Evidence source, such as agent, skill, static_rule, sandbox, go_test, go_vet"`
	RuleID         string  `json:"rule_id" jsonschema:"description=Stable non-empty issue-class identity used for classification and deduplication"`
}

type submitReviewResultsOutput struct {
	Status           string `json:"status"`
	FindingCount     int    `json:"finding_count"`
	WarningCount     int    `json:"warning_count"`
	HumanReviewCount int    `json:"human_review_count"`
}

func (g *governedExecution) requestToolPermission(
	ctx context.Context,
	in requestToolPermissionInput,
) (requestToolPermissionOutput, error) {
	if g == nil || g.recorder == nil || g.approver == nil {
		return requestToolPermissionOutput{}, fmt.Errorf("permission request tool is not configured")
	}
	targetTool := strings.TrimSpace(in.TargetTool)
	if targetTool == "" {
		return requestToolPermissionOutput{}, fmt.Errorf("target_tool is required")
	}
	if in.TargetArguments == nil {
		return requestToolPermissionOutput{}, fmt.Errorf("target_arguments must be a JSON object")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return requestToolPermissionOutput{}, fmt.Errorf("reason is required")
	}
	targetArguments, err := json.Marshal(in.TargetArguments)
	if err != nil {
		return requestToolPermissionOutput{}, fmt.Errorf("encode target_arguments: %w", err)
	}
	if targetTool == workspaceExecToolName {
		if _, denyReason := validateWorkspacePolicy(targetArguments); denyReason != "" {
			return requestToolPermissionOutput{}, fmt.Errorf("%s", denyReason)
		}
	}
	identity, err := approvalIdentity(targetTool, targetArguments)
	if err != nil {
		return requestToolPermissionOutput{}, err
	}

	decision, err := g.approver.decide(
		ctx,
		targetTool,
		g.recorder.mask(string(targetArguments)),
		g.recorder.mask(reason),
	)
	if err != nil {
		return requestToolPermissionOutput{}, err
	}
	if err := g.recordPermissionRequest(
		ctx,
		targetTool,
		targetArguments,
		reason,
		decision,
	); err != nil {
		return requestToolPermissionOutput{}, err
	}

	var status string
	switch decision.Action {
	case tool.PermissionActionAllow:
		g.grant(targetTool, identity)
		status = permissionStatusGranted
	case tool.PermissionActionDeny:
		status = permissionStatusDenied
	case tool.PermissionActionAsk:
		status = permissionStatusApprovalNeeded
	default:
		return requestToolPermissionOutput{}, fmt.Errorf(
			"unsupported approval decision %q",
			decision.Action,
		)
	}
	return requestToolPermissionOutput{
		Status:          status,
		TargetTool:      targetTool,
		TargetArguments: in.TargetArguments,
	}, nil
}

func (g *governedExecution) recordPermissionRequest(
	ctx context.Context,
	targetTool string,
	targetArguments []byte,
	reason string,
	decision tool.PermissionDecision,
) error {
	taskID, err := reviewTaskIDFromContext(ctx)
	if err != nil {
		return err
	}
	toolCallID, ok := tool.ToolCallIDFromContext(ctx)
	if !ok || toolCallID == "" {
		return fmt.Errorf("permission request requires a non-empty tool call id")
	}
	return g.recorder.RecordPermissionDecision(ctx, taskID, store.PermissionDecisionRecord{
		ToolCallID:     toolCallID,
		DecisionKind:   decisionKindPermissionRequest,
		Operation:      targetTool,
		ToolName:       targetTool,
		CommandPreview: string(targetArguments),
		Decision:       string(decision.Action),
		Reason:         reason,
	})
}

// submit_review_results: result validation only, no execution grants.
func newSubmitReviewResultsTool(recorder *reviewRecorder) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in submitReviewResultsInput) (submitReviewResultsOutput, error) {
			return submitReviewResults(ctx, recorder, in)
		},
		function.WithName(submitReviewResultsName),
		function.WithDescription("Submit code review findings, warnings, human-review items, and conclusion for the current review task."),
	)
}

func submitReviewResults(
	ctx context.Context,
	recorder *reviewRecorder,
	in submitReviewResultsInput,
) (submitReviewResultsOutput, error) {
	taskID, err := reviewTaskIDFromContext(ctx)
	if err != nil {
		return submitReviewResultsOutput{}, err
	}
	if recorder == nil {
		return submitReviewResultsOutput{}, fmt.Errorf("review recorder is not configured")
	}
	if strings.TrimSpace(in.Conclusion) == "" {
		return submitReviewResultsOutput{}, fmt.Errorf("review conclusion is required")
	}
	submission, err := canonicalizeReviewToolSubmission(in)
	if err != nil {
		return submitReviewResultsOutput{}, err
	}
	counts, err := recorder.SubmitReviewResults(
		ctx,
		taskID,
		submission.Results,
		in.Conclusion,
	)
	if err != nil {
		return submitReviewResultsOutput{}, err
	}
	return submitReviewResultsOutput{
		Status:           "accepted",
		FindingCount:     counts.FindingCount,
		WarningCount:     counts.WarningCount,
		HumanReviewCount: counts.HumanReviewCount,
	}, nil
}

func requestToolPermissionInputSchema() *tool.Schema {
	return &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"target_tool": {
				Type:        "string",
				Description: "Exact model-visible name of the tool that returned approval_required.",
			},
			"target_arguments": {
				Type:                 "object",
				Description:          "Complete argument object that will be copied when retrying the target tool.",
				AdditionalProperties: true,
			},
			"reason": {
				Type:        "string",
				Description: "Concise user-facing Reason explaining why this target tool call is needed.",
			},
		},
		Required:             []string{"target_tool", "target_arguments", "reason"},
		AdditionalProperties: false,
	}
}

func requestToolPermissionOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"status": {
				Type: "string",
				Enum: []any{
					permissionStatusGranted,
					permissionStatusDenied,
					permissionStatusApprovalNeeded,
				},
			},
			"target_tool": {Type: "string"},
			"target_arguments": {
				Type:                 "object",
				AdditionalProperties: true,
			},
		},
		Required:             []string{"status", "target_tool", "target_arguments"},
		AdditionalProperties: false,
	}
}
