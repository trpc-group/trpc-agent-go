//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the todo_write tool.
//
// After each assistant turn the current checklist is read from the
// session state and pretty-printed to the terminal so you can see how
// the agent plans, updates status, and eventually finishes a multi-step
// task.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use (any OpenAI-compatible identifier)")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
	variant   = flag.String("variant", "openai", "Variant passed to the OpenAI provider")
	seed      = flag.String("seed", "", "Optional first user message; if set, the chat auto-starts with it")
)

const (
	appName   = "todo-demo"
	agentName = "todo-assistant"
)

func main() {
	flag.Parse()

	fmt.Println("Todo tool demo: plan + track multi-step work")
	fmt.Printf("Model:     %s (variant=%s)\n", *modelName, *variant)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println("Commands:  /exit to quit, /list to print the current checklist")
	fmt.Println(strings.Repeat("=", 60))

	c := &chat{modelName: *modelName, streaming: *streaming, variant: *variant}
	if err := c.run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

// chat wraps the runner, session service and conversation loop.
type chat struct {
	modelName string
	streaming bool
	variant   string

	runner    runner.Runner
	sessSvc   session.Service
	userID    string
	sessionID string
}

func (c *chat) run() error {
	ctx := context.Background()
	if err := c.setup(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	defer c.runner.Close()

	if *seed != "" {
		fmt.Printf("You (seed): %s\n", *seed)
		if err := c.processMessage(ctx, *seed); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
		c.printTodos()
	}
	return c.startChat(ctx)
}

func (c *chat) setup() error {
	modelInstance := openai.New(c.modelName, openai.WithVariant(openai.Variant(c.variant)))
	c.sessSvc = sessioninmemory.NewSessionService()

	// Build the todo tool. The verification nudge hook demonstrates how
	// to attach an extra policy reminder without touching the tool core:
	// when the model closes out 3+ tasks all-at-once we remind it to do
	// a verification pass before declaring success.
	todoTool := todo.New(
		// Keep the final "all completed" state visible in the demo.
		// The default behavior (true) clears the list to signal a
		// fresh planning session next time.
		todo.WithClearOnAllDone(false),
		todo.WithNudgeHook(func(_ context.Context, _, newList []todo.Item) string {
			if len(newList) < 3 {
				return ""
			}
			allDone := true
			for _, it := range newList {
				if it.Status != todo.StatusCompleted {
					allDone = false
					break
				}
			}
			if !allDone {
				return ""
			}
			return "Reminder: all tasks are marked completed. " +
				"Before finishing, briefly summarise the outcome for the user."
		}),
	)

	instruction := "You are a careful assistant. When a user asks you to do " +
		"anything with more than 2 steps, call the todo_write tool to plan " +
		"first, then work the items one by one, flipping status as you go.\n\n" +
		todo.DefaultToolPrompt

	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Demo agent that uses todo_write to plan and track work."),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(8000),
			Stream:    c.streaming,
		}),
		llmagent.WithTools([]tool.Tool{todoTool}),
	)

	c.runner = runner.NewRunner(appName, llmAgent, runner.WithSessionService(c.sessSvc))
	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("demo-session-%d", time.Now().Unix())

	fmt.Printf("Ready. Session: %s\n\n", c.sessionID)
	return nil
}

func (c *chat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "":
			continue
		case "/exit":
			fmt.Println("bye")
			return nil
		case "/list":
			c.printTodos()
			continue
		}
		if err := c.processMessage(ctx, line); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
		c.printTodos()
	}
	return scanner.Err()
}

func (c *chat) processMessage(ctx context.Context, msg string) error {
	reqID := uuid.New().String()
	eventChan, err := c.runner.Run(
		ctx, c.userID, c.sessionID, model.NewUserMessage(msg),
		agent.WithRequestID(reqID),
	)
	if err != nil {
		return err
	}
	return c.processResponse(eventChan)
}

// processResponse renders the stream: tool calls, tool responses and
// assistant text are each formatted distinctly so the checklist updates
// are easy to spot.
func (c *chat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("Assistant: ")
	var assistantStarted bool
	var toolCallsPrinted bool

	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Printf("\n[error] %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		ch := evt.Response.Choices[0]

		// Tool calls - always show the arguments so you can watch the
		// model write the checklist.
		if len(ch.Message.ToolCalls) > 0 {
			if assistantStarted {
				fmt.Println()
			}
			for _, tc := range ch.Message.ToolCalls {
				fmt.Printf("  [tool-call] %s %s\n", tc.Function.Name, string(tc.Function.Arguments))
			}
			toolCallsPrinted = true
			continue
		}

		// Tool responses. We render two views side-by-side to show
		// both consumption patterns a real deployment will use:
		//
		//   1. The raw JSON — what the LLM sees (includes the nudge).
		//   2. A decoded todo.Output — what a cloud frontend (AG-UI,
		//      Web UI, etc.) consumes directly from the event stream,
		//      without any extra session fetch.
		if ch.Message.Role == model.RoleTool && ch.Message.ToolID != "" {
			raw := strings.TrimSpace(ch.Message.Content)
			fmt.Printf("  [tool-result raw ] %s\n", raw)
			var out todo.Output
			if err := json.Unmarshal([]byte(raw), &out); err == nil {
				fmt.Printf("  [tool-result decoded] %d todos (was %d)\n",
					len(out.Todos), len(out.OldTodos))
				for _, it := range out.Todos {
					fmt.Printf("    - [%s] %s\n", it.Status, it.Content)
				}
			}
			continue
		}

		// Assistant text (streaming delta or full).
		text := ch.Delta.Content
		if !c.streaming {
			text = ch.Message.Content
		}
		if text == "" {
			continue
		}
		if !assistantStarted {
			if toolCallsPrinted {
				fmt.Print("\nAssistant: ")
			}
			assistantStarted = true
		}
		fmt.Print(text)

		if evt.IsFinalResponse() {
			fmt.Println()
		}
	}
	return nil
}

// printTodos reads the current checklist from the session at the end of
// a turn and renders it as ASCII.
//
// Two consumption patterns exist and this demo shows both:
//
//  1. In-stream, structured: decode todo.Output from the tool-result
//     event (see processResponse above). This is what a cloud
//     frontend (AG-UI, WebSocket UI, etc.) will use — no extra fetch.
//  2. End-of-turn, canonical: read the session and call todo.GetTodos.
//     This is what a REST endpoint or an audit job would use when it
//     only needs the current state, not the change stream.
func (c *chat) printTodos() {
	sess, err := c.sessSvc.GetSession(context.Background(), session.Key{
		AppName:   appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	})
	if err != nil || sess == nil {
		return
	}
	// The tool writes under a branch-scoped key. A single-agent setup
	// uses the agent name as the branch (see agent.Invocation.Branch).
	items, err := todo.GetTodos(sess, agentName)
	if err != nil {
		fmt.Printf("[todo] decode error: %v\n", err)
		return
	}
	fmt.Println("----- Current checklist -----")
	fmt.Println(formatTodos(items))
	fmt.Println("-----------------------------")
}

// formatTodos is a tiny ASCII pretty-printer local to this demo. Rich
// frontends (AG-UI, web UIs, etc.) should render todo.Item directly in
// their own native style instead of reusing a fixed string like this.
func formatTodos(todos []todo.Item) string {
	if len(todos) == 0 {
		return "(no todos)"
	}
	glyph := func(s todo.Status) string {
		switch s {
		case todo.StatusCompleted:
			return "[x]"
		case todo.StatusInProgress:
			return "[>]"
		default:
			return "[ ]"
		}
	}
	var b strings.Builder
	for i, it := range todos {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- %s %s", glyph(it.Status), it.Content)
	}
	return b.String()
}

func intPtr(i int) *int { return &i }
