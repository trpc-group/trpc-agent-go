//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"context"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RecordingToolPolicy wraps a PermissionPolicy and records every decision for
// audit persistence (LLM assist tool calls included).
//
// The inner policy is required: NewRecordingToolPolicy(nil) installs a deny-all
// fallback, and a nil receiver denies. Failed Inner checks are still recorded
// before the error is returned.
type RecordingToolPolicy struct {
	inner tool.PermissionPolicy

	mu        sync.Mutex
	decisions []review.PermissionDecision
}

// NewRecordingToolPolicy returns a recording wrapper around inner.
// A nil inner is replaced with a deny-all policy so misconfiguration cannot
// silently allow tool calls or skip audit records.
func NewRecordingToolPolicy(inner tool.PermissionPolicy) *RecordingToolPolicy {
	if inner == nil {
		inner = tool.PermissionPolicyFunc(func(
			context.Context, *tool.PermissionRequest,
		) (tool.PermissionDecision, error) {
			return tool.DenyPermission("permission policy is required"), nil
		})
	}
	return &RecordingToolPolicy{inner: inner}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (r *RecordingToolPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if r == nil {
		return tool.DenyPermission("recording permission policy is nil"), nil
	}
	dec, err := r.inner.CheckToolPermission(ctx, req)
	cmd, _ := extractExecCommand(req)
	toolName := ""
	if req != nil {
		toolName = req.ToolName
	}
	if cmd == "" {
		cmd = toolName
	}
	action := string(dec.Action)
	reason := dec.Reason
	if err != nil {
		if action == "" {
			action = ActionDeny
		}
		if reason == "" {
			reason = err.Error()
		} else {
			reason = reason + "; " + err.Error()
		}
	}
	if action == "" {
		action = ActionDeny
	}
	pd := review.PermissionDecision{
		ToolName:  toolName,
		Command:   Redact(cmd),
		Action:    action,
		Reason:    Redact(reason),
		CreatedAt: time.Now().UTC(),
	}
	r.mu.Lock()
	r.decisions = append(r.decisions, pd)
	r.mu.Unlock()
	return dec, err
}

// Decisions returns a copy of recorded permission decisions.
func (r *RecordingToolPolicy) Decisions() []review.PermissionDecision {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]review.PermissionDecision, len(r.decisions))
	copy(out, r.decisions)
	return out
}
