//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// PermissionPolicy adapts the approval configuration to the framework's final
// tool permission check, after before-tool callbacks finalize arguments. Do not
// also register this Plugin in BeforeTool mode for the same run: that would
// review twice, and the earlier review would still see pre-callback arguments.
//
// Static denials run first, then base (if non-nil), then the reviewer when
// required. SkipApproval skips only the reviewer. Base deny/ask/error decisions
// are never overridden. The framework runs tool-owned PermissionChecker checks
// before this policy. Review/provider errors and nil decisions prevent execution.
// Ask decisions retain the framework's non-executing approval_required behavior;
// this adapter does not implement human approval or retries.
//
// The returned policy owns no resources and may be reused across runs. Supplied
// reviewers, providers, and base policies must support concurrent calls when
// the agent executes tools concurrently. A nil Plugin produces a policy that
// returns an error. Configure the Plugin with New before using it.
func (p *Plugin) PermissionPolicy(base tool.PermissionPolicy) tool.PermissionPolicy {
	return &permissionPolicy{plugin: p, base: base}
}

type permissionPolicy struct {
	plugin *Plugin
	base   tool.PermissionPolicy
}

func (p *permissionPolicy) CheckToolPermission(
	ctx context.Context, input *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	if p.plugin == nil || ctx == nil {
		return tool.PermissionDecision{}, fmt.Errorf("approval permission: plugin or context is nil")
	}
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	req, err := itool.SnapshotPermissionRequest(input)
	if err != nil {
		return tool.PermissionDecision{}, err
	}
	policy := p.plugin.resolveToolPolicy(req.ToolName)
	if err := validateToolPolicy(policy); err != nil {
		return tool.PermissionDecision{}, err
	}
	if policy == ToolPolicyDenied {
		return tool.DenyPermission(fmt.Sprintf("tool %q is denied by approval policy", req.ToolName)), nil
	}
	if p.base != nil {
		baseInput, err := itool.SnapshotPermissionRequest(req)
		if err != nil {
			return tool.PermissionDecision{}, err
		}
		decision, err := p.base.CheckToolPermission(ctx, baseInput)
		if err != nil {
			return tool.PermissionDecision{}, err
		}
		decision, err = tool.NormalizePermissionDecision(decision)
		if err != nil || decision.Action != tool.PermissionActionAllow {
			return decision, err
		}
	}
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	if policy == ToolPolicySkipApproval {
		return tool.AllowPermission(), nil
	}
	if p.plugin.reviewer == nil {
		return tool.PermissionDecision{}, fmt.Errorf("approval permission: reviewer is nil")
	}
	reviewRequest, err := p.plugin.buildPermissionRequest(ctx, req)
	if err != nil {
		return tool.PermissionDecision{}, fmt.Errorf("approval review failed: %w", err)
	}
	decision, err := p.plugin.reviewer.Review(ctx, reviewRequest)
	if err != nil {
		return tool.PermissionDecision{}, fmt.Errorf("approval review failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return tool.PermissionDecision{}, err
	}
	if decision == nil {
		return tool.PermissionDecision{}, fmt.Errorf("approval review failed: reviewer returned nil decision")
	}
	if !decision.Approved {
		reason := fmt.Sprintf("Automatic approval review denied (risk: %s): %s", decision.RiskLevel, decision.Reason)
		log.WarnContext(ctx, reason)
		return tool.DenyPermission(reason), nil
	}
	log.InfofContext(ctx, "Automatic approval review approved (risk: %s): %s", decision.RiskLevel, decision.Reason)
	return tool.AllowPermission(), nil
}

func (p *Plugin) buildPermissionRequest(ctx context.Context, input *tool.PermissionRequest) (*review.Request, error) {
	req := &review.Request{
		Action: review.Action{ToolName: input.ToolName,
			ToolDescription: declarationDescription(input.Declaration), Arguments: cloneJSON(input.Arguments)},
		Context: &review.Context{ToolCallID: input.ToolCallID, ToolMetadata: input.Metadata},
	}
	if input.Declaration != nil {
		schema, err := snapshotReviewSchema(input.Declaration.InputSchema)
		if err != nil {
			return nil, err
		}
		req.Context.InputSchema = schema
	}
	if err := p.buildEvidence(ctx, req); err != nil {
		return nil, err
	}
	if p.contextProvider != nil {
		// The provider sees the same facts, but cannot mutate the request that
		// will be reviewed or retain a mutable alias to its schema/slices.
		data, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("snapshot context provider input: %w", err)
		}
		var providerInput review.Request
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&providerInput); err != nil {
			return nil, err
		}
		supplement, err := p.contextProvider(ctx, &providerInput)
		if err != nil {
			return nil, fmt.Errorf("collect review context: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req.Context.Supplement, err = boundedSupplement(supplement)
		if err != nil {
			return nil, err
		}
	}
	return req, nil
}

// snapshotReviewSchema preserves the JSON evidence, including exact numbers,
// without copying arbitrary Go objects or sharing mutable values with tools.
// Only reviews need serialization; ordinary permission policies do not.
func snapshotReviewSchema(schema *tool.Schema) (*tool.Schema, error) {
	if schema == nil {
		return nil, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("snapshot review schema: %w", err)
	}
	var snapshot tool.Schema
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("snapshot review schema: %w", err)
	}
	return &snapshot, nil
}

func boundedSupplement(input review.Supplement) (review.Supplement, error) {
	if !utf8.ValidString(input.ReviewInstructions) || utf8.RuneCountInString(input.ReviewInstructions) > 16000 {
		return review.Supplement{}, fmt.Errorf("review instructions must be valid UTF-8 and at most 16000 runes")
	}
	if len(input.Facts) > 32 {
		return review.Supplement{}, fmt.Errorf("review supplement exceeds 32 facts")
	}
	out := review.Supplement{ReviewInstructions: input.ReviewInstructions}
	for _, fact := range input.Facts {
		if !utf8.ValidString(fact.Source) || !utf8.ValidString(fact.Content) || utf8.RuneCountInString(fact.Source) > 1024 {
			return review.Supplement{}, fmt.Errorf("review fact must be valid UTF-8 with a source of at most 1024 runes")
		}
		content, truncated := truncateEvidence(fact.Content, 4000)
		fact.Content, fact.Truncated = content, fact.Truncated || truncated
		out.Facts = append(out.Facts, fact)
	}
	return out, nil
}
