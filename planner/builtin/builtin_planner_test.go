//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package builtin

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{
			name: "empty options",
			opts: Options{},
		},
		{
			name: "with reasoning effort",
			opts: Options{
				ReasoningEffort: stringPtr("medium"),
			},
		},
		{
			name: "with thinking enabled",
			opts: Options{
				ThinkingEnabled: boolPtr(true),
			},
		},
		{
			name: "with thinking tokens",
			opts: Options{
				ThinkingTokens: intPtr(2048),
			},
		},
		{
			name: "with all options",
			opts: Options{
				ReasoningEffort: stringPtr("high"),
				ThinkingEnabled: boolPtr(true),
				ThinkingTokens:  intPtr(3000),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.opts)
			if p == nil {
				t.Error("New() returned nil")
			}

			// Verify interface implementation.
			var _ planner.Planner = p
		})
	}
}

func TestPlanner_BuildPlanInstr(t *testing.T) {
	tests := []struct {
		name    string
		planner *Planner
		want    *model.Request
	}{
		{
			name:    "empty planner",
			planner: &Planner{},
			want: &model.Request{
				GenerationConfig: model.GenerationConfig{
					ReasoningEffort: nil,
					ThinkingEnabled: nil,
					ThinkingTokens:  nil,
				},
			},
		},
		{
			name: "with reasoning effort",
			planner: &Planner{
				reasoningEffort: stringPtr("low"),
			},
			want: &model.Request{
				GenerationConfig: model.GenerationConfig{
					ReasoningEffort: stringPtr("low"),
					ThinkingEnabled: nil,
					ThinkingTokens:  nil,
				},
			},
		},
		{
			name: "with thinking config",
			planner: &Planner{
				thinkingEnabled: boolPtr(true),
				thinkingTokens:  intPtr(1500),
			},
			want: &model.Request{
				GenerationConfig: model.GenerationConfig{
					ReasoningEffort: nil,
					ThinkingEnabled: boolPtr(true),
					ThinkingTokens:  intPtr(1500),
				},
			},
		},
		{
			name: "with all configs",
			planner: &Planner{
				reasoningEffort: stringPtr("high"),
				thinkingEnabled: boolPtr(false),
				thinkingTokens:  intPtr(2000),
			},
			want: &model.Request{
				GenerationConfig: model.GenerationConfig{
					ReasoningEffort: stringPtr("high"),
					ThinkingEnabled: boolPtr(false),
					ThinkingTokens:  intPtr(2000),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			invocation := &agent.Invocation{}
			req := &model.Request{}

			result := tt.planner.BuildPlanningInstruction(ctx, invocation, req)

			// Verify return value is empty string.
			if result != "" {
				t.Errorf("BuildPlanningInstruction() = %q, want empty string", result)
			}

			// Verify thinking configuration was applied to request.
			if !equalStringPtr(req.ReasoningEffort, tt.want.GenerationConfig.ReasoningEffort) {
				t.Errorf("ReasoningEffort = %v, want %v", req.ReasoningEffort, tt.want.GenerationConfig.ReasoningEffort)
			}
			if !equalBoolPtr(req.ThinkingEnabled, tt.want.GenerationConfig.ThinkingEnabled) {
				t.Errorf("ThinkingEnabled = %v, want %v", req.ThinkingEnabled, tt.want.GenerationConfig.ThinkingEnabled)
			}
			if !equalIntPtr(req.ThinkingTokens, tt.want.GenerationConfig.ThinkingTokens) {
				t.Errorf("ThinkingTokens = %v, want %v", req.ThinkingTokens, tt.want.GenerationConfig.ThinkingTokens)
			}
		})
	}
}

func TestPlanner_ProcessPlanningResponse(t *testing.T) {
	p := New(Options{})
	ctx := context.Background()
	invocation := &agent.Invocation{}
	response := &model.Response{}

	result := p.ProcessPlanningResponse(ctx, invocation, response)
	if result != nil {
		t.Errorf("ProcessPlanningResponse() = %v, want nil", result)
	}
}

// Helper functions for pointer comparisons and creation.
func stringPtr(s string) *string { return &s }
func boolPtr(b bool) *bool       { return &b }
func intPtr(i int) *int          { return &i }

func equalStringPtr(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func equalIntPtr(a, b *int) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
