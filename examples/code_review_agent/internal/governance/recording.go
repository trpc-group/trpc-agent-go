//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// DecisionSink persists one governance decision.
type DecisionSink interface {
	RecordGovernanceDecision(ctx context.Context, decision review.GovernanceDecision) error
}

// RecordingPolicy records every permission outcome exactly once.
type RecordingPolicy struct {
	policy *Policy
	sink   DecisionSink
	taskID string
	mu     sync.Mutex
	seen   map[string]struct{}
	filter error
}

// NewRecordingPolicy wraps policy with a mandatory decision sink.
func NewRecordingPolicy(policy *Policy, sink DecisionSink, taskID string) (*RecordingPolicy, error) {
	if policy == nil || sink == nil || !validTaskID(taskID) {
		return nil, errors.New("new recording policy: policy, sink, and task id are required")
	}
	return &RecordingPolicy{
		policy: policy,
		sink:   sink,
		taskID: taskID,
		seen:   make(map[string]struct{}),
	}, nil
}

// CheckToolPermission implements tool.PermissionPolicy and records the result.
func (p *RecordingPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p == nil || p.policy == nil || p.sink == nil {
		return tool.DenyPermission("permission recorder is unavailable"), errDecisionNotRecorded
	}
	decision, rule, policyErr := p.policy.evaluate(ctx, req)
	normalized, normalizeErr := tool.NormalizePermissionDecision(decision)
	if normalizeErr != nil {
		normalized = tool.DenyPermission("permission decision is invalid")
		rule = "deny-invalid-decision"
	}
	toolName := "unknown"
	if req != nil && req.ToolName != "" {
		toolName = req.ToolName
	}
	record := review.GovernanceDecision{
		SchemaVersion: review.SchemaVersion,
		TaskID:        p.taskID,
		DecisionID:    decisionID("permission", toolCallID(req)),
		Kind:          review.DecisionKindPermission,
		Tool:          redact.String(toolName),
		Action:        review.DecisionAction(normalized.Action),
		Reason:        redact.String(normalized.Reason),
		Rule:          rule,
	}
	if record.Reason == "" {
		record.Reason = "fixed review policy decision"
	}
	recordErr := p.recordOnce(ctx, record)
	if recordErr != nil {
		return tool.DenyPermission("permission decision could not be recorded"),
			fmt.Errorf("record permission decision: %w", redact.Error(recordErr))
	}
	return normalized, errors.Join(policyErr, normalizeErr)
}

// Filter returns a tool.FilterFunc that exposes only trusted review tools and
// records each tool identity decision once.
func (p *RecordingPolicy) Filter() tool.FilterFunc {
	return func(ctx context.Context, candidate tool.Tool) bool {
		if p == nil {
			return false
		}
		name := "unknown"
		if candidate != nil && candidate.Declaration() != nil && candidate.Declaration().Name != "" {
			name = candidate.Declaration().Name
		}
		allowed := p != nil && p.policy != nil && p.policy.visibleTool(candidate)
		action := review.DecisionActionDeny
		reason := "tool is hidden from the review model"
		rule := "filter-review-tools"
		if allowed {
			action = review.DecisionActionAllow
			reason = "tool is visible to the review model"
		}
		decision := review.GovernanceDecision{
			SchemaVersion: review.SchemaVersion,
			TaskID:        p.taskID,
			DecisionID:    decisionID("filter", name),
			Kind:          review.DecisionKindFilter,
			Tool:          redact.String(name),
			Action:        action,
			Reason:        reason,
			Rule:          rule,
		}
		if err := p.recordOnce(ctx, decision); err != nil {
			p.mu.Lock()
			p.filter = redact.Error(err)
			p.mu.Unlock()
			return false
		}
		return allowed
	}
}

// FilterError returns the last filter audit failure, if any.
func (p *RecordingPolicy) FilterError() error {
	if p == nil {
		return errDecisionNotRecorded
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.filter
}

var _ tool.PermissionPolicy = (*RecordingPolicy)(nil)

func toolCallID(req *tool.PermissionRequest) string {
	if req == nil {
		return "missing"
	}
	return req.ToolCallID
}

func decisionID(kind, value string) string {
	digest := sha256.Sum256([]byte(value))
	return kind + ":" + hex.EncodeToString(digest[:8])
}

func (p *RecordingPolicy) recordOnce(ctx context.Context, decision review.GovernanceDecision) error {
	if p == nil || p.sink == nil {
		return errDecisionNotRecorded
	}
	if err := decision.Validate(); err != nil {
		return redact.Error(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := string(decision.Kind) + "\x00" + decision.DecisionID
	if _, exists := p.seen[key]; exists {
		return nil
	}
	recordContext := ctx
	if recordContext == nil {
		recordContext = context.Background()
	}
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(recordContext), 2*time.Second)
	defer cancel()
	if err := p.sink.RecordGovernanceDecision(recordContext, decision); err != nil {
		return err
	}
	p.seen[key] = struct{}{}
	return nil
}

func validTaskID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '.' || character == '_' || character == ':' || character == '-')) {
			return false
		}
	}
	lower := strings.ToLower(value)
	return !strings.HasPrefix(lower, "sk-") && !strings.Contains(lower, "-sk-")
}
