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
	"errors"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	countProgressToolName   = "count_progress"
	defaultCountSteps       = 5
	maxCountSteps           = 20
	defaultCountDelayMS     = 200
	minCountDelayMS         = 50
	maxCountDelayMS         = 2000
	defaultCountStepLatency = 200 * time.Millisecond
)

type countProgressArgs struct {
	Steps   int `json:"steps,omitempty" description:"How many steps to count before finishing."`
	DelayMS int `json:"delay_ms,omitempty" description:"Delay in milliseconds between streamed updates."`
}

type countProgressUpdate struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

type countProgressResult struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

func newCountProgressTool() tool.Tool {
	return function.NewStreamableFunctionTool[countProgressArgs, countProgressResult](
		func(ctx context.Context, args countProgressArgs) (*tool.StreamReader, error) {
			stream := tool.NewStream(16)
			go runCountProgress(ctx, normalizeCountProgressArgs(args), stream.Writer)
			return stream.Reader, nil
		},
		function.WithName(countProgressToolName),
		function.WithDescription("Count upward step by step and stream numeric progress updates before returning a final result."),
	)
}

func normalizeCountProgressArgs(args countProgressArgs) countProgressArgs {
	if args.Steps <= 0 {
		args.Steps = defaultCountSteps
	}
	if args.Steps > maxCountSteps {
		args.Steps = maxCountSteps
	}
	if args.DelayMS <= 0 {
		args.DelayMS = defaultCountDelayMS
	}
	if args.DelayMS < minCountDelayMS {
		args.DelayMS = minCountDelayMS
	}
	if args.DelayMS > maxCountDelayMS {
		args.DelayMS = maxCountDelayMS
	}
	return args
}

func runCountProgress(ctx context.Context, args countProgressArgs, writer *tool.StreamWriter) {
	defer writer.Close()
	if args.Steps <= 0 {
		writer.Send(tool.StreamChunk{}, errors.New("steps must be greater than zero"))
		return
	}
	delay := time.Duration(args.DelayMS) * time.Millisecond
	if delay <= 0 {
		delay = defaultCountStepLatency
	}
	for step := 1; step <= args.Steps; step++ {
		if err := ctx.Err(); err != nil {
			writer.Send(tool.StreamChunk{}, err)
			return
		}
		if err := sendCountProgressUpdate(ctx, writer, countProgressUpdate{
			Current: step,
			Total:   args.Steps,
		}, delay); err != nil {
			writer.Send(tool.StreamChunk{}, err)
			return
		}
	}
	writer.Send(tool.StreamChunk{
		Content: tool.FinalResultChunk{Result: countProgressResult{
			Completed: args.Steps,
			Total:     args.Steps,
		}},
	}, nil)
}

func sendCountProgressUpdate(ctx context.Context, writer *tool.StreamWriter, progress countProgressUpdate, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if closed := writer.Send(tool.StreamChunk{Content: progress}, nil); closed {
		return context.Canceled
	}
	return sleepWithContext(ctx, delay)
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
