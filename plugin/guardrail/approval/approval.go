//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package approval provides a runner-scoped tool approval plugin.
package approval

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Plugin is the approval plugin implementation.
type Plugin struct {
	name              string
	reviewer          review.Reviewer
	defaultToolPolicy ToolPolicy
	toolPolicies      map[string]ToolPolicy
	tokenCounter      model.TokenCounter
}

// New creates a new approval plugin.
func New(options ...Option) (*Plugin, error) {
	opts := newOptions(options...)
	if err := validateToolPolicy(opts.defaultToolPolicy); err != nil {
		return nil, fmt.Errorf("newing approval plugin: default tool policy: %w", err)
	}
	for toolName, policy := range opts.toolPolicies {
		if toolName == "" {
			return nil, fmt.Errorf("newing approval plugin: tool policy name is empty")
		}
		if err := validateToolPolicy(policy); err != nil {
			return nil, fmt.Errorf("newing approval plugin: tool %q policy: %w", toolName, err)
		}
	}
	if requiresReviewer(opts) && opts.reviewer == nil {
		return nil, fmt.Errorf("newing approval plugin: reviewer is nil")
	}
	return &Plugin{
		name:              opts.name,
		reviewer:          opts.reviewer,
		defaultToolPolicy: opts.defaultToolPolicy,
		toolPolicies:      opts.toolPolicies,
		tokenCounter:      model.NewSimpleTokenCounter(),
	}, nil
}

// Name implements plugin.Plugin.
func (p *Plugin) Name() string {
	return p.name
}

// Register implements plugin.Plugin.
func (p *Plugin) Register(r *plugin.Registry) {
	if p == nil || r == nil {
		return
	}
	r.BeforeTool(p.beforeTool())
}

func (p *Plugin) beforeTool() tool.BeforeToolCallbackStructured {
	return func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		if args == nil {
			return nil, nil
		}
		policy := p.resolveToolPolicy(args.ToolName)
		switch policy {
		case ToolPolicyDenied:
			return &tool.BeforeToolResult{
				CustomResult: fmt.Sprintf("tool %q is denied by approval policy", args.ToolName),
			}, nil
		case ToolPolicySkipApproval:
			return nil, nil
		case ToolPolicyRequireApproval:
			req, err := p.buildRequest(ctx, args)
			if err != nil {
				log.ErrorfContext(
					ctx,
					"Automatic approval review denied: approval review failed for tool %q: %v",
					args.ToolName,
					err,
				)
				return &tool.BeforeToolResult{
					CustomResult: fmt.Sprintf("approval review failed for tool %q: %v", args.ToolName, err),
				}, nil
			}
			decision, err := p.reviewer.Review(ctx, req)
			if err != nil {
				log.ErrorfContext(
					ctx,
					"Automatic approval review denied: approval review failed for tool %q: %v",
					args.ToolName,
					err,
				)
				return &tool.BeforeToolResult{
					CustomResult: fmt.Sprintf("approval review failed for tool %q: %v", args.ToolName, err),
				}, nil
			}
			if decision == nil {
				err = fmt.Errorf("approval reviewer returned nil decision")
				log.ErrorfContext(
					ctx,
					"Automatic approval review denied: approval review failed for tool %q: %v",
					args.ToolName,
					err,
				)
				return &tool.BeforeToolResult{
					CustomResult: fmt.Sprintf("approval review failed for tool %q: %v", args.ToolName, err),
				}, nil
			}
			riskLevel := strings.TrimSpace(decision.RiskLevel)
			reason := strings.TrimSpace(decision.Reason)
			if decision.Approved {
				log.InfofContext(
					ctx,
					"Automatic approval review approved (risk: %s): %s",
					riskLevel,
					reason,
				)
				return nil, nil
			}
			denyMessage := fmt.Sprintf(
				"Automatic approval review denied (risk: %s): %s",
				riskLevel,
				reason,
			)
			log.WarnContext(ctx, denyMessage)
			return &tool.BeforeToolResult{
				CustomResult: denyMessage,
			}, nil
		default:
			return &tool.BeforeToolResult{
				CustomResult: fmt.Sprintf("approval review failed for tool %q: %v", args.ToolName, fmt.Errorf("unsupported tool policy %q", policy)),
			}, nil
		}
	}
}

func (p *Plugin) resolveToolPolicy(toolName string) ToolPolicy {
	if policy, ok := p.toolPolicies[toolName]; ok {
		return policy
	}
	return p.defaultToolPolicy
}

func requiresReviewer(opts *options) bool {
	if opts.defaultToolPolicy == ToolPolicyRequireApproval {
		return true
	}
	for _, policy := range opts.toolPolicies {
		if policy == ToolPolicyRequireApproval {
			return true
		}
	}
	return false
}
