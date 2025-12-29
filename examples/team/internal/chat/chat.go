//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	DefaultExitCommand = "/exit"

	defaultPrompt = "You: "
)

type LoopConfig struct {
	Runner        runner.Runner
	UserID        string
	SessionID     string
	Timeout       time.Duration
	ShowInner     bool
	RootAgentName string
	ExitCommand   string
}

func Run(ctx context.Context, cfg LoopConfig) error {
	if cfg.Runner == nil {
		return errors.New("runner is nil")
	}
	if cfg.UserID == "" {
		return errors.New("user id is empty")
	}
	if cfg.SessionID == "" {
		return errors.New("session id is empty")
	}
	if cfg.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if cfg.ExitCommand == "" {
		cfg.ExitCommand = DefaultExitCommand
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(defaultPrompt)
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if text == cfg.ExitCommand {
			return nil
		}

		reqCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		eventChannel, err := cfg.Runner.Run(
			reqCtx,
			cfg.UserID,
			cfg.SessionID,
			model.NewUserMessage(text),
		)
		if err != nil {
			cancel()
			fmt.Printf("Error: %v\n", err)
			continue
		}

		printEvents(eventChannel, cfg.ShowInner, cfg.RootAgentName)
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			fmt.Fprintln(os.Stderr, "\n[timeout] error: request timed out")
		}
		fmt.Println()
		cancel()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	return nil
}

func printEvents(
	eventChannel <-chan *event.Event,
	showInner bool,
	rootAgentName string,
) {
	printedDelta := make(map[string]bool)
	printedToolCalls := make(map[string]bool)
	printedToolResults := make(map[string]bool)
	toolNameByID := make(map[string]string)
	printedPrefix := make(map[string]bool)

	atLineStart := true

	for eventItem := range eventChannel {
		if eventItem == nil {
			continue
		}
		if eventItem.Error != nil {
			fmt.Fprintf(
				os.Stderr,
				"\n[%s] error: %s\n",
				eventItem.Author,
				eventItem.Error.Message,
			)
			continue
		}
		if eventItem.Response == nil ||
			len(eventItem.Response.Choices) == 0 {
			continue
		}

		if eventItem.Object == model.ObjectTypeTransfer {
			fmt.Printf(
				"\n[%s] %s\n",
				eventItem.Author,
				firstContent(eventItem),
			)
			atLineStart = true
			continue
		}

		responseID := eventItem.Response.ID
		if eventItem.Response.IsToolCallResponse() &&
			!printedToolCalls[responseID] {
			printedToolCalls[responseID] = true
			recordToolIDs(toolNameByID, eventItem)
			printToolCalls(eventItem, showInner)
			atLineStart = true
		}

		if eventItem.IsToolResultResponse() {
			printToolResults(
				toolNameByID,
				printedToolResults,
				eventItem,
			)
			atLineStart = true
			continue
		}

		if eventItem.Response.IsPartial {
			text := firstDelta(eventItem)
			if text != "" {
				if showInner && eventItem.Author != rootAgentName &&
					!printedPrefix[responseID] {
					if !atLineStart {
						fmt.Println()
					}
					fmt.Printf("[%s] ", eventItem.Author)
					printedPrefix[responseID] = true
				}
				printedDelta[responseID] = true
				fmt.Print(text)
				atLineStart = strings.HasSuffix(text, "\n")
			}
			continue
		}

		if printedDelta[responseID] {
			if eventItem.IsFinalResponse() {
				delete(printedDelta, responseID)
				fmt.Println()
				atLineStart = true
			}
			continue
		}

		text := firstContent(eventItem)
		if text != "" {
			if showInner && eventItem.Author != rootAgentName &&
				!printedPrefix[responseID] {
				if !atLineStart {
					fmt.Println()
				}
				fmt.Printf("[%s] ", eventItem.Author)
				printedPrefix[responseID] = true
			}
			fmt.Print(text)
			atLineStart = strings.HasSuffix(text, "\n")
		}

		if eventItem.IsFinalResponse() {
			fmt.Println()
			atLineStart = true
		}
	}
}

func printToolCalls(ev *event.Event, showArgs bool) {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return
	}

	choice := ev.Response.Choices[0]
	toolCalls := choice.Message.ToolCalls
	if len(toolCalls) == 0 {
		toolCalls = choice.Delta.ToolCalls
	}
	if len(toolCalls) == 0 {
		return
	}

	fmt.Print("\n[tools] ")
	for i, tc := range toolCalls {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(tc.Function.Name)
	}
	fmt.Println()

	if !showArgs {
		return
	}
	for _, tc := range toolCalls {
		if len(tc.Function.Arguments) == 0 {
			continue
		}
		fmt.Printf(
			"[tool.args] %s: %s\n",
			tc.Function.Name,
			string(tc.Function.Arguments),
		)
	}
}

func recordToolIDs(toolNameByID map[string]string, ev *event.Event) {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return
	}

	choice := ev.Response.Choices[0]
	toolCalls := choice.Message.ToolCalls
	if len(toolCalls) == 0 {
		toolCalls = choice.Delta.ToolCalls
	}
	for _, tc := range toolCalls {
		if tc.ID == "" {
			continue
		}
		toolNameByID[tc.ID] = tc.Function.Name
	}
}

func printToolResults(
	toolNameByID map[string]string,
	printed map[string]bool,
	ev *event.Event,
) {
	if ev == nil || ev.Response == nil {
		return
	}
	for _, choice := range ev.Response.Choices {
		toolID := choice.Message.ToolID
		if toolID == "" {
			toolID = choice.Delta.ToolID
		}
		if toolID == "" || printed[toolID] {
			continue
		}
		printed[toolID] = true

		name := toolNameByID[toolID]
		if name == "" {
			name = toolID
		}
		fmt.Printf("[tool.done] %s\n", name)
	}
}

func firstDelta(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	return ev.Response.Choices[0].Delta.Content
}

func firstContent(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ""
	}
	return ev.Response.Choices[0].Message.Content
}
