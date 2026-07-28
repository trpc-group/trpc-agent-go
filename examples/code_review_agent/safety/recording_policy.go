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
type RecordingToolPolicy struct {
	Inner tool.PermissionPolicy

	mu        sync.Mutex
	decisions []review.PermissionDecision
}

// NewRecordingToolPolicy returns a recording wrapper around inner.
func NewRecordingToolPolicy(inner tool.PermissionPolicy) *RecordingToolPolicy {
	return &RecordingToolPolicy{Inner: inner}
}

// CheckToolPermission implements tool.PermissionPolicy.
func (r *RecordingToolPolicy) CheckToolPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if r == nil || r.Inner == nil {
		return tool.AllowPermission(), nil
	}
	dec, err := r.Inner.CheckToolPermission(ctx, req)
	if err != nil {
		return dec, err
	}
	cmd, _ := extractExecCommand(req)
	toolName := ""
	if req != nil {
		toolName = req.ToolName
	}
	if cmd == "" {
		cmd = toolName
	}
	pd := review.PermissionDecision{
		ToolName:  toolName,
		Command:   Redact(cmd),
		Action:    string(dec.Action),
		Reason:    Redact(dec.Reason),
		CreatedAt: time.Now().UTC(),
	}
	r.mu.Lock()
	r.decisions = append(r.decisions, pd)
	r.mu.Unlock()
	return dec, nil
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
