//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptinjection provides a runner-scoped prompt injection guardrail plugin.
package promptinjection

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	promptreview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/promptinjection/review"
)

// Plugin is the prompt injection guardrail implementation.
type Plugin struct {
	name         string
	reviewer     promptreview.Reviewer
	tokenCounter model.TokenCounter
}

// New creates a new prompt injection plugin.
func New(options ...Option) (*Plugin, error) {
	opts := newOptions(options...)
	if opts.reviewer == nil {
		return nil, fmt.Errorf("newing prompt injection plugin: reviewer is nil")
	}
	return &Plugin{
		name:         opts.name,
		reviewer:     opts.reviewer,
		tokenCounter: model.NewSimpleTokenCounter(),
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
	r.BeforeModel(p.beforeModel())
}

func (p *Plugin) beforeModel() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		if p == nil || args == nil || args.Request == nil {
			return nil, nil
		}
		req := p.buildReviewRequest(ctx, args.Request.Messages)
		if req == nil {
			return nil, nil
		}
		decision, err := p.reviewer.Review(ctx, req)
		if err != nil {
			log.ErrorfContext(ctx, "Prompt injection review denied: %v", err)
			return &model.BeforeModelResult{CustomResponse: p.blockedResponse("")}, nil
		}
		if decision == nil {
			err = fmt.Errorf("prompt injection reviewer returned nil decision")
			log.ErrorfContext(ctx, "Prompt injection review denied: %v", err)
			return &model.BeforeModelResult{CustomResponse: p.blockedResponse("")}, nil
		}
		if !decision.Blocked {
			return nil, nil
		}
		denyMessage := promptInjectionDenyMessage(decision)
		log.WarnContext(ctx, denyMessage)
		return &model.BeforeModelResult{CustomResponse: p.blockedResponse(denyMessage)}, nil
	}
}

func (p *Plugin) blockedResponse(content string) *model.Response {
	if content == "" {
		content = "The input was blocked by the prompt injection guardrail."
	}
	return &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(content),
		}},
	}
}

func promptInjectionDenyMessage(decision *promptreview.Decision) string {
	return fmt.Sprintf(
		"Prompt injection detected (category: %s): %s",
		decision.Category,
		decision.Reason,
	)
}
