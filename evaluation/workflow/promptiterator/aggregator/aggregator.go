//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package aggregator provides gradient aggregation for the prompt iteration workflow.
package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/internal/runneroutput"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Aggregator converts IssueRecord slices into an AggregatedGradient using a runner.
type Aggregator interface {
	Aggregate(ctx context.Context, rawIssues []issue.IssueRecord) (*issue.AggregatedGradient, error)
}

type aggregator struct {
	r                 runner.Runner
	userIDSupplier    func(context.Context) string
	sessionIDSupplier func(context.Context) string
	runOptions        []agent.RunOption
	messageBuilder    func(context.Context, []issue.IssueRecord) (model.Message, error)
}

// New creates a runner-based Aggregator.
func New(r runner.Runner, opt ...Option) (Aggregator, error) {
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
	return &aggregator{
		r:                 r,
		userIDSupplier:    opts.userIDSupplier,
		sessionIDSupplier: opts.sessionIDSupplier,
		runOptions:        opts.runOptions,
		messageBuilder:    opts.messageBuilder,
	}, nil
}

// Aggregate aggregates raw issues into an aggregated gradient.
func (a *aggregator) Aggregate(ctx context.Context, rawIssues []issue.IssueRecord) (*issue.AggregatedGradient, error) {
	if len(rawIssues) == 0 {
		return nil, errors.New("raw issues are empty")
	}
	userID := a.userIDSupplier(ctx)
	sessionID := a.sessionIDSupplier(ctx)
	msg, err := a.messageBuilder(ctx, rawIssues)
	if err != nil {
		return nil, err
	}
	events, err := a.r.Run(ctx, userID, sessionID, msg, a.runOptions...)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	captured, err := runneroutput.CaptureRunnerOutputs(events)
	if err != nil {
		return nil, err
	}
	if captured.StructuredOutput != nil {
		gradient, err := parseAggregatedGradientFromAny(captured.StructuredOutput)
		if err != nil {
			return nil, fmt.Errorf("parse structured output: %w", err)
		}
		if gradient == nil {
			return nil, errors.New("aggregated gradient is empty")
		}
		return gradient, nil
	}
	gradient, err := parseAggregatedGradientFromContent(captured.FinalContent)
	if err != nil {
		return nil, fmt.Errorf("parse final content: %w", err)
	}
	if gradient == nil {
		return nil, errors.New("aggregated gradient is empty")
	}
	return gradient, nil
}

func parseAggregatedGradientFromAny(payload any) (*issue.AggregatedGradient, error) {
	if payload == nil {
		return nil, nil
	}
	switch v := payload.(type) {
	case *issue.AggregatedGradient:
		return v, nil
	case issue.AggregatedGradient:
		clone := v
		return &clone, nil
	case string:
		return parseAggregatedGradientFromContent(v)
	case []byte:
		return parseAggregatedGradientFromContent(string(v))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal structured output: %w", err)
		}
		var out issue.AggregatedGradient
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("unmarshal structured output: %w", err)
		}
		return &out, nil
	}
}

func parseAggregatedGradientFromContent(content string) (*issue.AggregatedGradient, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	var out issue.AggregatedGradient
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &out); err != nil {
		return nil, fmt.Errorf("unmarshal final content: %w", err)
	}
	return &out, nil
}
