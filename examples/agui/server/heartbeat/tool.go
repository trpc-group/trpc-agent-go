//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const waitToolName = "wait_before_answer"

type waitBeforeAnswerArgs struct {
	Reason string `json:"reason,omitempty" description:"Short reason for waiting before answering."`
}

type waitBeforeAnswerResult struct {
	WaitedMS int64  `json:"waited_ms"`
	Message  string `json:"message"`
}

func newWaitTool(quietPeriod time.Duration) tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ waitBeforeAnswerArgs) (waitBeforeAnswerResult, error) {
			if err := sleepWithContext(ctx, quietPeriod); err != nil {
				return waitBeforeAnswerResult{}, err
			}
			return waitBeforeAnswerResult{
				WaitedMS: quietPeriod.Milliseconds(),
				Message:  "Quiet period completed.",
			}, nil
		},
		function.WithName(waitToolName),
		function.WithDescription("Wait for the server-configured quiet period before the assistant answers."),
	)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
