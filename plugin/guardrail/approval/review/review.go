//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package review provides approval reviewer abstractions and the built-in guardian reviewer.
package review

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Reviewer evaluates a review request and returns an approval decision.
type Reviewer interface {
	Review(ctx context.Context, req *Request) (*Decision, error)
}

// Request is the stable approval request contract passed to reviewers.
type Request struct {
	Action     Action
	Transcript []TranscriptEntry
}

// Action is the exact tool action being reviewed.
type Action struct {
	ToolName        string
	ToolDescription string
	Arguments       json.RawMessage
}

// TranscriptEntry is a compact transcript line used as approval evidence.
type TranscriptEntry struct {
	Role    model.Role
	Content string
}

// Decision is the stable reviewer output consumed by the approval plugin.
type Decision struct {
	Approved  bool
	RiskScore int
	RiskLevel string
	Reason    string
}

type guardianReviewer struct {
	runner            runner.Runner
	systemPrompt      string
	riskThreshold     int
	userIDSupplier    UserIDSupplier
	sessionIDSupplier SessionIDSupplier
}

type decisionPayload struct {
	RiskScore int    `json:"risk_score"`
	RiskLevel string `json:"risk_level"`
	Reason    string `json:"reason"`
}

// New creates the built-in guardian reviewer backed by a runner.
func New(r runner.Runner, options ...Option) (Reviewer, error) {
	if r == nil {
		return nil, fmt.Errorf("newing approval reviewer: runner is nil")
	}
	opts := newOptions(options...)
	if opts.riskThreshold < 0 || opts.riskThreshold > 100 {
		return nil, fmt.Errorf("newing approval reviewer: risk threshold %d out of range", opts.riskThreshold)
	}
	if opts.userIDSupplier == nil {
		return nil, fmt.Errorf("newing approval reviewer: user id supplier is nil")
	}
	if opts.sessionIDSupplier == nil {
		return nil, fmt.Errorf("newing approval reviewer: session id supplier is nil")
	}
	return &guardianReviewer{
		runner:            r,
		systemPrompt:      opts.systemPrompt,
		riskThreshold:     opts.riskThreshold,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
	}, nil
}

func (r *guardianReviewer) Review(ctx context.Context, req *Request) (*Decision, error) {
	if req == nil {
		return nil, fmt.Errorf("reviewing approval request: request is nil")
	}
	if req.Action.ToolName == "" {
		return nil, fmt.Errorf("reviewing approval request: action tool name is empty")
	}
	userID, err := r.userIDSupplier(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("reviewing approval request: supply user id: %w", err)
	}
	if userID == "" {
		return nil, fmt.Errorf("reviewing approval request: supplied user id is empty")
	}
	sessionID, err := r.sessionIDSupplier(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("reviewing approval request: supply session id: %w", err)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("reviewing approval request: supplied session id is empty")
	}
	systemPrompt, err := renderSystemPrompt(r.systemPrompt, r.riskThreshold)
	if err != nil {
		return nil, fmt.Errorf("reviewing approval request: render system prompt: %w", err)
	}
	userMessage, err := renderUserMessage(req)
	if err != nil {
		return nil, fmt.Errorf("reviewing approval request: render user message: %w", err)
	}
	eventCh, err := r.runner.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(userMessage),
		agent.WithGlobalInstruction(systemPrompt),
		agent.WithStructuredOutputJSON(
			new(decisionPayload),
			true,
			"Return the approval decision as JSON.",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("reviewing approval request: runner run: %w", err)
	}
	payload, err := collectDecisionPayload(ctx, eventCh)
	if err != nil {
		return nil, fmt.Errorf("reviewing approval request: collect decision: %w", err)
	}
	if payload.RiskScore < 0 || payload.RiskScore > 100 {
		return nil, fmt.Errorf("reviewing approval request: risk score %d out of range", payload.RiskScore)
	}
	return &Decision{
		Approved:  payload.RiskScore < r.riskThreshold,
		RiskScore: payload.RiskScore,
		RiskLevel: payload.RiskLevel,
		Reason:    payload.Reason,
	}, nil
}

func collectDecisionPayload(ctx context.Context, events <-chan *event.Event) (*decisionPayload, error) {
	if events == nil {
		return nil, fmt.Errorf("runner returned nil event channel")
	}
	var payload *decisionPayload
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case evt, ok := <-events:
			if !ok {
				if payload == nil {
					return nil, fmt.Errorf("missing structured output")
				}
				return payload, nil
			}
			if evt == nil || evt.StructuredOutput == nil {
				continue
			}
			switch value := evt.StructuredOutput.(type) {
			case *decisionPayload:
				payload = value
			case decisionPayload:
				copied := value
				payload = &copied
			default:
				return nil, fmt.Errorf("unexpected structured output type %T", evt.StructuredOutput)
			}
		}
	}
}
