//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package demo

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func runChat(ctx context.Context, c config, input io.Reader, output io.Writer) error {
	slow := function.NewFunctionTool(func(ctx context.Context, in workArgs) (string, error) {
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "completed " + in.Job, nil
		}
	}, function.WithName("work"), function.WithDescription("Perform a local slow job. Use when asked for a slow job."))
	ag := llmagent.New("chat", llmagent.WithModel(openai.New(c.model)), llmagent.WithInterruptOnUserMessage(true), llmagent.WithTools([]tool.Tool{slow}), llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}))
	r := runner.NewRunner("steer-chat", ag, runner.WithPersistInterruptedAssistant(c.persist))
	defer r.Close()
	return chatLoop(ctx, r, input, output)
}

func chatLoop(ctx context.Context, r runner.Runner, input io.Reader, output io.Writer) error {
	lines := make(chan string)
	inputErrors := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)
	if closer, ok := input.(io.Closer); ok {
		defer closer.Close()
	}
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-done:
				return
			}
		}
		inputErrors <- scanner.Err()
	}()
	sessionID := fmt.Sprintf("chat-%d", time.Now().UnixNano())
	var active <-chan *event.Event
	var requestID string
	var pending []string
	managed := r.(runner.ManagedRunner)
	defer func() {
		if active != nil {
			managed.Cancel(requestID)
		}
	}()
	fmt.Fprintln(output, "Type while the agent is working. /cancel stops the run; /quit exits. Ask for a slow job to try tool-boundary steering.")
	for {
		if active == nil && len(pending) > 0 {
			text := pending[0]
			pending = pending[1:]
			requestID = fmt.Sprintf("chat-run-%d", time.Now().UnixNano())
			var err error
			active, err = r.Run(ctx, "demo-user", sessionID, model.NewUserMessage(text), agent.WithRequestID(requestID))
			if err != nil {
				return err
			}
			fmt.Fprintf(output, "[run] started %s\n", requestID)
			for len(pending) > 0 {
				err = runner.EnqueueUserMessage(r, requestID, model.NewUserMessage(pending[0]))
				if errors.Is(err, runner.ErrRunNotFound) {
					break
				}
				if err != nil {
					return err
				}
				pending = pending[1:]
			}
		}
		if lines == nil && active == nil {
			return <-inputErrors
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			text := strings.TrimSpace(line)
			switch text {
			case "":
				continue
			case "/quit":
				return nil
			case "/cancel":
				if active != nil {
					managed.Cancel(requestID)
					fmt.Fprintln(output, "[run] cancellation requested")
				}
				continue
			}
			if active == nil || len(pending) > 0 {
				pending = append(pending, text)
				continue
			}
			err := runner.EnqueueUserMessage(r, requestID, model.NewUserMessage(text))
			if errors.Is(err, runner.ErrRunNotFound) {
				pending = append(pending, text)
				continue
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(output, "[queue] accepted")
		case evt, ok := <-active:
			if !ok {
				active = nil
				continue
			}
			if evt.Response == nil {
				continue
			}
			if evt.Error != nil {
				fmt.Fprintf(output, "[error] %s\n", evt.Error.Message)
				continue
			}
			if evt.IsRunnerCompletion() {
				fmt.Fprintln(output, "\n[run] completion")
				continue
			}
			if evt.Object == model.ObjectTypeModelInterrupted {
				fmt.Fprintf(output, "\n[model] interrupted %s\n", evt.Response.ID)
				continue
			}
			for _, choice := range evt.Choices {
				if evt.IsPartial {
					fmt.Fprint(output, choice.Delta.Content)
				}
				if choice.Message.Role == model.RoleUser {
					fmt.Fprintf(output, "\n[queue] consumed %s\n", choice.Message.Content)
				}
				if choice.Message.Role == model.RoleTool {
					fmt.Fprintf(output, "\n[tool] %s\n", choice.Message.Content)
				}
				if len(choice.Message.ToolCalls) > 0 {
					fmt.Fprintf(output, "\n[tool] accepted %d calls\n", len(choice.Message.ToolCalls))
				}
			}
		}
	}
}
