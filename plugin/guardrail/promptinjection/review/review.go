//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package review provides reviewer abstractions for prompt injection detection.
package review

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Category is the prompt injection category returned by the reviewer.
type Category string

const (
	// CategorySystemOverride indicates attempts to override higher-priority instructions.
	CategorySystemOverride Category = "system_override"
	// CategoryPolicyBypass indicates attempts to bypass policy or safety controls.
	CategoryPolicyBypass Category = "policy_bypass"
	// CategoryPromptExfiltration indicates attempts to reveal hidden prompts or configuration.
	CategoryPromptExfiltration Category = "prompt_exfiltration"
	// CategoryRoleHijack indicates attempts to impersonate a privileged or higher-priority role.
	CategoryRoleHijack Category = "role_hijack"
	// CategoryToolMisuseInduction indicates attempts to induce unsafe or incorrect tool behavior.
	CategoryToolMisuseInduction Category = "tool_misuse_induction"
)

// TranscriptEntry is a compact transcript line used as review evidence.
type TranscriptEntry struct {
	Role    model.Role
	Content string
}

// Request is the stable prompt injection review request contract.
type Request struct {
	LastUserInput string
	Transcript    []TranscriptEntry
}

// Decision is the stable reviewer output consumed by the prompt injection plugin.
type Decision struct {
	Blocked  bool
	Category Category
	Reason   string
}

// Reviewer evaluates a prompt injection review request and returns a decision.
type Reviewer interface {
	Review(ctx context.Context, req *Request) (*Decision, error)
}

type guardianReviewer struct {
	runner            runner.Runner
	systemPrompt      string
	userIDSupplier    UserIDSupplier
	sessionIDSupplier SessionIDSupplier
}

type decisionPayload struct {
	Blocked  bool     `json:"blocked"`
	Category Category `json:"category"`
	Reason   string   `json:"reason"`
}

// New creates the built-in prompt injection reviewer backed by a runner.
func New(r runner.Runner, options ...Option) (Reviewer, error) {
	if r == nil {
		return nil, fmt.Errorf("newing prompt injection reviewer: runner is nil")
	}
	opts := newOptions(options...)
	if opts.userIDSupplier == nil {
		return nil, fmt.Errorf("newing prompt injection reviewer: user id supplier is nil")
	}
	if opts.sessionIDSupplier == nil {
		return nil, fmt.Errorf("newing prompt injection reviewer: session id supplier is nil")
	}
	return &guardianReviewer{
		runner:            r,
		systemPrompt:      opts.systemPrompt,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
	}, nil
}

func (r *guardianReviewer) Review(ctx context.Context, req *Request) (*Decision, error) {
	if req == nil {
		return nil, fmt.Errorf("reviewing prompt injection request: request is nil")
	}
	userID, err := r.userIDSupplier(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("reviewing prompt injection request: supply user id: %w", err)
	}
	if userID == "" {
		return nil, fmt.Errorf("reviewing prompt injection request: supplied user id is empty")
	}
	sessionID, err := r.sessionIDSupplier(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("reviewing prompt injection request: supply session id: %w", err)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("reviewing prompt injection request: supplied session id is empty")
	}
	userMessage, err := renderUserMessage(req)
	if err != nil {
		return nil, fmt.Errorf("reviewing prompt injection request: render user message: %w", err)
	}
	eventCh, err := r.runner.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(userMessage),
		agent.WithGlobalInstruction(r.systemPrompt),
		agent.WithStructuredOutputJSON(
			new(decisionPayload),
			true,
			"Return the prompt injection decision as JSON.",
		),
	)
	if err != nil {
		return nil, fmt.Errorf("reviewing prompt injection request: runner run: %w", err)
	}
	payload, err := collectDecisionPayload(ctx, eventCh)
	if err != nil {
		return nil, fmt.Errorf("reviewing prompt injection request: collect decision: %w", err)
	}
	if err := validateDecisionPayload(payload); err != nil {
		return nil, fmt.Errorf("reviewing prompt injection request: %w", err)
	}
	return &Decision{
		Blocked:  payload.Blocked,
		Category: payload.Category,
		Reason:   payload.Reason,
	}, nil
}

func validateDecisionPayload(payload *decisionPayload) error {
	if payload == nil {
		return fmt.Errorf("decision payload is nil")
	}
	if err := validateCategory(payload.Category); err != nil {
		return err
	}
	if payload.Blocked && payload.Category == "" {
		return fmt.Errorf("blocked decision category is empty")
	}
	return nil
}

func validateCategory(category Category) error {
	switch category {
	case "", CategorySystemOverride, CategoryPolicyBypass, CategoryPromptExfiltration, CategoryRoleHijack, CategoryToolMisuseInduction:
		return nil
	default:
		return fmt.Errorf("invalid category %q", category)
	}
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
