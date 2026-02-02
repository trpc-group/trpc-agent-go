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
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	appName   = "cancelrun-demo"
	userID    = "demo-user"
	sessionID = "demo-session"

	agentName = "slow-writer"

	startMessage = "start"

	maxRunDuration = 15 * time.Second
	chunkDelay     = 120 * time.Millisecond
	eventChanBuf   = 8
)

func main() {
	fmt.Println("Cancel a Run demo")
	fmt.Println("Press Enter to cancel.")
	fmt.Println("Press Ctrl+C to cancel (SIGINT).")
	fmt.Println()

	baseCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
	)
	defer stop()

	ctx, cancel := context.WithTimeout(baseCtx, maxRunDuration)
	defer cancel()

	go cancelOnEnter(cancel)

	r := runner.NewRunner(appName, newSlowWriter(agentName, chunkDelay))
	defer r.Close()

	eventCh, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(startMessage),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run failed: %v\n", err)
		os.Exit(1)
	}

	printEvents(eventCh)
	printExitReason(ctx)
}

func cancelOnEnter(cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	_, _ = reader.ReadString('\n')
	cancel()
}

func printEvents(eventCh <-chan *event.Event) {
	for evt := range eventCh {
		if evt == nil || evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			fmt.Fprintf(
				os.Stderr,
				"event error: %s\n",
				evt.Response.Error.Message,
			)
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
				continue
			}
			if choice.Message.Content != "" {
				fmt.Print(choice.Message.Content)
			}
		}
	}
	fmt.Println()
}

func printExitReason(ctx context.Context) {
	if ctx == nil {
		return
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		fmt.Println("Run stopped: context canceled.")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Println("Run stopped: timeout reached.")
	default:
		fmt.Println("Run finished.")
	}
}

type slowWriter struct {
	name  string
	delay time.Duration
}

func newSlowWriter(name string, delay time.Duration) *slowWriter {
	return &slowWriter{name: name, delay: delay}
}

func (a *slowWriter) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is nil")
	}
	out := make(chan *event.Event, eventChanBuf)
	runCtx := agent.CloneContext(ctx)

	go func() {
		defer close(out)
		a.stream(runCtx, invocation, out)
	}()

	return out, nil
}

func (a *slowWriter) stream(
	ctx context.Context,
	invocation *agent.Invocation,
	out chan<- *event.Event,
) {
	for i := 0; i < len(demoIntroChunks); i++ {
		evt := demoEvent(invocation, a.name, demoIntroChunks[i])
		if err := agent.EmitEvent(ctx, invocation, out, evt); err != nil {
			return
		}
	}

	if a.delay <= 0 {
		a.delay = chunkDelay
	}
	ticker := time.NewTicker(a.delay)
	defer ticker.Stop()

	count := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count++
			content := fmt.Sprintf(demoChunkFormat, count)
			evt := demoEvent(invocation, a.name, content)
			if err := agent.EmitEvent(
				ctx,
				invocation,
				out,
				evt,
			); err != nil {
				return
			}
		}
	}
}

func (a *slowWriter) Tools() []tool.Tool { return nil }

func (a *slowWriter) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: "Streams text slowly until cancelled.",
	}
}

func (a *slowWriter) SubAgents() []agent.Agent { return nil }

func (a *slowWriter) FindSubAgent(_ string) agent.Agent { return nil }

const demoChunkFormat = "chunk %d\n"

var demoIntroChunks = []string{
	"Streaming some text...\n",
	"Press Enter (or Ctrl+C) to stop.\n",
	"\n",
}

func demoEvent(
	invocation *agent.Invocation,
	author string,
	content string,
) *event.Event {
	rsp := &model.Response{
		Object:    model.ObjectTypeChatCompletionChunk,
		Created:   time.Now().Unix(),
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{
			{
				Index: 0,
				Delta: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			},
		},
	}
	return event.NewResponseEvent(invocation.InvocationID, author, rsp)
}
