//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"fmt"
	"strings"
)

type executionPlan struct {
	Mode             string
	SandboxRequested bool
	ModelRequested   bool
}

func normalizeExecutionPlan(req Request) (executionPlan, error) {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = ModeReview
	}
	plan := executionPlan{}
	switch mode {
	case ModeReview:
		plan.Mode = ModeReview
	case ModeDryRun:
		plan.Mode = ModeDryRun
	case ModeRuleOnly:
		plan.Mode = ModeReview
	case ModeSandbox:
		plan.Mode = ModeReview
		plan.SandboxRequested = true
	case ModeFakeModel:
		plan.Mode = ModeReview
		plan.ModelRequested = true
	default:
		return executionPlan{}, fmt.Errorf("unsupported review mode %q", mode)
	}
	if req.SandboxEnabled != nil {
		plan.SandboxRequested = *req.SandboxEnabled
	}
	if req.ModelEnabled != nil {
		plan.ModelRequested = *req.ModelEnabled
	}
	if plan.Mode == ModeDryRun && (plan.SandboxRequested || plan.ModelRequested) {
		return executionPlan{}, fmt.Errorf("dry-run cannot enable sandbox or model review")
	}
	return plan, nil
}

// ValidateRequest validates mode/capability combinations before dependencies are created.
func ValidateRequest(req Request) error {
	_, err := normalizeExecutionPlan(req)
	return err
}
