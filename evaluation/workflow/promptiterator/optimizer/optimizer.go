//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package optimizer provides prompt optimization for the prompt iteration workflow.
package optimizer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/internal/runneroutput"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Optimizer produces the next prompt using an aggregated gradient.
type Optimizer interface {
	Optimize(ctx context.Context, currentPrompt string, gradient *issue.AggregatedGradient) (string, error)
}

type optimizer struct {
	r                 runner.Runner
	userIDSupplier    func(context.Context) string
	sessionIDSupplier func(context.Context) string
	runOptions        []agent.RunOption
	messageBuilder    func(context.Context, string, *issue.AggregatedGradient) (model.Message, error)
}

// New creates a runner-based Optimizer implementation.
func New(r runner.Runner, opt ...Option) (Optimizer, error) {
	if r == nil {
		return nil, errors.New("runner is nil")
	}
	opts := newOptions(opt...)
	if opts.userIDSupplier == nil {
		return nil, errors.New("user id supplier is nil")
	}
	if opts.sessionIDSupplier == nil {
		return nil, errors.New("session id supplier is nil")
	}
	if opts.messageBuilder == nil {
		return nil, errors.New("message builder is nil")
	}
	return &optimizer{
		r:                 r,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
		runOptions:        opts.runOptions,
		messageBuilder:    opts.messageBuilder,
	}, nil
}

// Optimize generates the next prompt text.
func (o *optimizer) Optimize(ctx context.Context, currentPrompt string, gradient *issue.AggregatedGradient) (string, error) {
	if strings.TrimSpace(currentPrompt) == "" {
		return "", errors.New("current prompt is empty")
	}
	if gradient == nil {
		return "", errors.New("gradient is nil")
	}
	userID := o.userIDSupplier(ctx)
	sessionID := o.sessionIDSupplier(ctx)
	msg, err := o.messageBuilder(ctx, currentPrompt, gradient)
	if err != nil {
		return "", err
	}
	events, err := o.r.Run(ctx, userID, sessionID, msg, o.runOptions...)
	if err != nil {
		return "", fmt.Errorf("runner run: %w", err)
	}
	captured, err := runneroutput.CaptureRunnerOutputs(events)
	if err != nil {
		return "", err
	}
	nextPrompt := strings.TrimSpace(captured.FinalContent)
	if nextPrompt == "" {
		return "", errors.New("optimizer final content is empty")
	}
	return nextPrompt, nil
}
