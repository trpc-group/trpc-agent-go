//
// Tencent is pleased to support the open source community
// by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName      = "managedrunner-demo"
	demoUserID   = "demo-user"
	agentName    = "ticker-agent"
	agentDesc    = "Emits periodic tick events until the context ends."
	messageText  = "start"
	tickFormat   = "tick %d"
	statusFormat = "  status: events=%d last=%s\n"
)

const (
	tickInterval       = 200 * time.Millisecond
	statusPollInterval = 300 * time.Millisecond
)

const (
	requestIDDetached     = "demo-run-detached"
	sessionDetached       = "demo-session-detached"
	requestIDManualCancel = "demo-run-manual-cancel"
	sessionManualCancel   = "demo-session-manual-cancel"
	requestIDMinDeadline  = "demo-run-min-deadline"
	sessionMinDeadline    = "demo-session-min-deadline"
)

const (
	parentCancelAfter   = 500 * time.Millisecond
	maxRunDetached      = 2 * time.Second
	manualCancelAfter   = 1 * time.Second
	maxRunManualCancel  = 10 * time.Second
	parentTimeout       = 1200 * time.Millisecond
	maxRunMinDeadline   = 5 * time.Second
	eventChannelBufSize = 1
)

func main() {
	baseRunner := runner.NewRunner(
		appName,
		newTickerAgent(agentName, tickInterval),
	)
	defer baseRunner.Close()

	managedRunner, ok := baseRunner.(runner.ManagedRunner)
	if !ok {
		fmt.Fprintln(
			os.Stderr,
			"runner does not implement runner.ManagedRunner",
		)
		os.Exit(1)
	}

	if err := demoDetachedCancel(managedRunner); err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}
	if err := demoManualCancel(managedRunner); err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}
	if err := demoMinDeadline(managedRunner); err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}
}

func demoDetachedCancel(managedRunner runner.ManagedRunner) error {
	fmt.Println("Demo 1: detached cancellation + max runtime")
	fmt.Printf("  parent cancel after: %s\n", parentCancelAfter)
	fmt.Printf("  max run duration:    %s\n", maxRunDetached)

	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	go func() {
		time.Sleep(parentCancelAfter)
		fmt.Println("  -> parent ctx cancelled")
		parentCancel()
	}()

	start := time.Now()
	eventChan, err := managedRunner.Run(
		parentCtx,
		demoUserID,
		sessionDetached,
		model.NewUserMessage(messageText),
		agent.WithRequestID(requestIDDetached),
		agent.WithDetachedCancel(true),
		agent.WithMaxRunDuration(maxRunDetached),
	)
	if err != nil {
		return err
	}
	consumeEvents(managedRunner, requestIDDetached, start, eventChan)

	fmt.Printf("  -> finished after: %s\n\n", time.Since(start))
	return nil
}

func demoManualCancel(managedRunner runner.ManagedRunner) error {
	fmt.Println("Demo 2: cancel a run by requestID")
	fmt.Printf("  cancel after:      %s\n", manualCancelAfter)
	fmt.Printf("  max run duration:  %s\n", maxRunManualCancel)

	start := time.Now()
	eventChan, err := managedRunner.Run(
		context.Background(),
		demoUserID,
		sessionManualCancel,
		model.NewUserMessage(messageText),
		agent.WithRequestID(requestIDManualCancel),
		agent.WithDetachedCancel(true),
		agent.WithMaxRunDuration(maxRunManualCancel),
	)
	if err != nil {
		return err
	}

	go func() {
		time.Sleep(manualCancelAfter)
		fmt.Println("  -> managed cancel called")
		_ = managedRunner.Cancel(requestIDManualCancel)
	}()

	consumeEvents(managedRunner, requestIDManualCancel, start, eventChan)
	fmt.Printf("  -> finished after: %s\n\n", time.Since(start))
	return nil
}

func demoMinDeadline(managedRunner runner.ManagedRunner) error {
	fmt.Println("Demo 3: earlier of parent deadline and max runtime")
	fmt.Printf("  parent timeout:    %s\n", parentTimeout)
	fmt.Printf("  max run duration:  %s\n", maxRunMinDeadline)

	parentCtx, parentCancel := context.WithTimeout(
		context.Background(),
		parentTimeout,
	)
	defer parentCancel()

	start := time.Now()
	eventChan, err := managedRunner.Run(
		parentCtx,
		demoUserID,
		sessionMinDeadline,
		model.NewUserMessage(messageText),
		agent.WithRequestID(requestIDMinDeadline),
		agent.WithDetachedCancel(true),
		agent.WithMaxRunDuration(maxRunMinDeadline),
	)
	if err != nil {
		return err
	}
	consumeEvents(managedRunner, requestIDMinDeadline, start, eventChan)
	fmt.Printf("  -> finished after: %s\n\n", time.Since(start))
	return nil
}

func consumeEvents(
	managedRunner runner.ManagedRunner,
	requestID string,
	start time.Time,
	eventChan <-chan *event.Event,
) {
	done := make(chan struct{})
	go pollStatus(managedRunner, requestID, done)

	seenRequestID := false
	for evt := range eventChan {
		if evt == nil {
			continue
		}
		if !seenRequestID && evt.RequestID != "" {
			seenRequestID = true
			fmt.Printf("  observed requestID: %s\n", evt.RequestID)
		}

		elapsed := time.Since(start).Truncate(time.Millisecond)
		if evt.IsRunnerCompletion() {
			fmt.Printf("[%s] runner completion\n", elapsed)
			continue
		}

		content := firstContent(evt)
		if content == "" {
			continue
		}
		fmt.Printf("[%s] %s: %s\n", elapsed, evt.Author, content)
	}

	close(done)
}

func pollStatus(
	managedRunner runner.ManagedRunner,
	requestID string,
	done <-chan struct{},
) {
	ticker := time.NewTicker(statusPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			status, ok := managedRunner.RunStatus(requestID)
			if !ok {
				return
			}
			last := status.LastEventAt.Format(time.Kitchen)
			fmt.Printf(statusFormat, status.EventCount, last)
		}
	}
}

func firstContent(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	for _, choice := range evt.Choices {
		if choice.Delta.Content != "" {
			return choice.Delta.Content
		}
		if choice.Message.Content != "" {
			return choice.Message.Content
		}
	}
	return ""
}

type tickerAgent struct {
	name         string
	tickInterval time.Duration
}

func newTickerAgent(name string, tickInterval time.Duration) *tickerAgent {
	return &tickerAgent{
		name:         name,
		tickInterval: tickInterval,
	}
}

func (a *tickerAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is nil")
	}
	out := make(chan *event.Event, eventChannelBufSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(out)

		ticker := time.NewTicker(a.tickInterval)
		defer ticker.Stop()

		tickCount := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tickCount++
				evt := tickEvent(invocation, a.name, tickCount)
				if err := agent.EmitEvent(ctx, invocation, out, evt); err != nil {
					return
				}
			}
		}
	}(runCtx)
	return out, nil
}

func (a *tickerAgent) Tools() []tool.Tool { return nil }

func (a *tickerAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: agentDesc,
	}
}

func (a *tickerAgent) SubAgents() []agent.Agent { return nil }

func (a *tickerAgent) FindSubAgent(_ string) agent.Agent { return nil }

func tickEvent(
	invocation *agent.Invocation,
	author string,
	tickCount int,
) *event.Event {
	rsp := &model.Response{
		Created: time.Now().Unix(),
		Done:    false,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.NewAssistantMessage(
					fmt.Sprintf(tickFormat, tickCount),
				),
			},
		},
		IsPartial: true,
	}
	return event.NewResponseEvent(invocation.InvocationID, author, rsp)
}
