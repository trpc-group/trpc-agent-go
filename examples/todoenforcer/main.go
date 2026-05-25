//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the todoenforcer extension.
//
// Run side-by-side comparisons:
//
//	# baseline: tool/todo without any enforcement (model can exit early)
//	go run ./examples/todoenforcer --enforce=false \
//	  --seed "Plan and execute a 4-step deployment: write config, run tests, deploy to staging, verify with smoke test."
//
//	# hardened: same prompt with todoenforcer wired in
//	go run ./examples/todoenforcer --enforce=true \
//	  --seed "Plan and execute a 4-step deployment: write config, run tests, deploy to staging, verify with smoke test."
//
// In the hardened run, watch the [enforce] lines: they show every
// blocked attempt the extension caught, the nudge it queued, and (if
// the model gives up via todo_declare_blocker) the formal blocker
// declaration. The key behavioural promise the demo verifies is
// that the model can no longer end the turn while open items
// remain — it must either work them, or formally declare a
// blocker.
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
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension/todoenforcer"
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
	modelName  = flag.String("model", "deepseek-chat", "Model identifier (any OpenAI-compatible name)")
	streaming  = flag.Bool("streaming", true, "Stream the assistant response")
	variant    = flag.String("variant", "openai", "Provider variant passed to the OpenAI client")
	enforce    = flag.Bool("enforce", true, "Install the todoenforcer extension (set false to compare baseline behaviour)")
	maxRetries = flag.Int("max-retries", 3, "todoenforcer block-retry budget per invocation")
	maxTokens  = flag.Int("max-tokens", 4000, "Max completion tokens (lower if your provider rejects the default)")
	seed       = flag.String("seed", "", "Optional first user message; if set, the chat auto-starts with it")
	prefill    = flag.Bool("prefill-todos", false, "Pre-seed the session with one in_progress + one pending todo BEFORE the first turn. Useful when your provider's OpenAI-compatible tool-call adapter is unstable and you need to verify the enforcer fires without depending on the model successfully invoking todo_write first.")
)

const (
	appName   = "todoenforcer-demo"
	agentName = "todoenforcer-assistant"
)

func main() {
	flag.Parse()

	fmt.Println("todoenforcer extension demo")
	fmt.Printf("Model:       %s (variant=%s)\n", *modelName, *variant)
	fmt.Printf("Streaming:   %t\n", *streaming)
	fmt.Printf("Enforce:     %t (max-retries=%d)\n", *enforce, *maxRetries)
	fmt.Println("Commands:    /exit to quit, /list to print the current checklist")
	fmt.Println(strings.Repeat("=", 70))

	c := &chat{
		modelName:  *modelName,
		streaming:  *streaming,
		variant:    *variant,
		enforce:    *enforce,
		maxRetries: *maxRetries,
		maxTokens:  *maxTokens,
		prefill:    *prefill,
	}
	if err := c.run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}

// chat wraps the runner, session service and conversation loop.
type chat struct {
	modelName  string
	streaming  bool
	variant    string
	enforce    bool
	maxRetries int
	maxTokens  int
	prefill    bool

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

	if c.prefill {
		if err := c.prefillTodos(ctx); err != nil {
			return fmt.Errorf("prefill: %w", err)
		}
	}

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

// prefillTodos simulates a mid-task resumption scenario. It
// reproduces, by hand, the exact pair of artefacts a real prior
// turn would have left behind:
//
//  1. session.State holds the current todo list (this is what
//     the enforcer's AfterModel reads to detect open items).
//  2. The session's event log holds the chat history that proves
//     the model itself already wrote the list — a user turn
//     asking for the work, an assistant turn calling todo_write,
//     and a tool turn carrying todo_write's response.
//
// Both are required for the demo to feel honest:
//
//   - Without (1) the enforcer never trips, since there are no
//     open items in state.
//   - Without (2) the model is blind to its own past — when the
//     user later says "please continue what you were doing", the
//     model has nothing in its visible chat history to continue
//     from. It will (correctly) say "I don't have any prior
//     context" and force the enforcer into a teaching role
//     ("here is a task you never knew about"), which is NOT the
//     real production semantics. The extension is meant to remind
//     a model of work it KNOWS it owes, not to dump unfamiliar
//     work onto it.
//
// We construct (2) using event.NewResponseEvent + AppendEvent,
// the same path examples/session/appendevent/ uses. The Branch
// field on each event matches the agent's branch (the agent
// name in a single-agent setup), so when the runner builds the
// next request these messages show up in the right context.
func (c *chat) prefillTodos(ctx context.Context) error {
	items := []todo.Item{
		{
			Content:    "Inspect the Kubernetes pod logs for the recent failure",
			ActiveForm: "Inspecting the Kubernetes pod logs for the recent failure",
			Status:     todo.StatusInProgress,
		},
		{
			Content:    "Identify the root cause and propose a fix",
			ActiveForm: "Identifying the root cause and proposing a fix",
			Status:     todo.StatusPending,
		},
	}
	rawState, err := json.Marshal(items)
	if err != nil {
		return err
	}
	// Branch defaults to the agent name for a single-agent setup
	// — same convention printTodos / GetTodos / the enforcer use.
	stateKey := todo.DefaultStateKeyPrefix + ":" + agentName
	sess, err := c.sessSvc.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	}, session.StateMap{stateKey: rawState})
	if err != nil {
		return err
	}

	// Build the synthetic prior turn. The arguments and result
	// payload mirror what a real todo_write call would carry, so
	// downstream consumers (event log replays, evaluation
	// pipelines, ...) see a turn that is structurally
	// indistinguishable from one the model actually produced.
	priorInvocationID := uuid.New().String()
	toolCallID := "prefill-" + uuid.New().String()

	priorUserMsg := "I just got a page about a Kubernetes pod failure in prod. " +
		"Please investigate: dig the pod logs, find the root cause, then " +
		"propose a fix. Plan the work with todo_write first."

	todoWriteArgs, err := json.Marshal(map[string]any{"todos": items})
	if err != nil {
		return err
	}
	todoWriteResult, err := json.Marshal(map[string]any{
		"message": "Todos have been modified successfully.",
		"todos":   items,
	})
	if err != nil {
		return err
	}

	events := []*event.Event{
		// 1. The original user request that kicked the work off.
		newPrefillEvent(priorInvocationID, "user", model.NewUserMessage(priorUserMsg)),
		// 2. The assistant turn that called todo_write to plan.
		newPrefillEvent(priorInvocationID, agentName, model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   toolCallID,
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      todo.DefaultToolName,
					Arguments: todoWriteArgs,
				},
			}},
		}),
		// 3. The tool turn carrying todo_write's response.
		// ToolName is set even though the spec doesn't strictly
		// require it for assistant↔tool pairing — several
		// Some OpenAI-compatible adapters reject tool messages
		// whose name field is empty, even when the matching ID is
		// present.
		newPrefillEvent(priorInvocationID, agentName, model.Message{
			Role:     model.RoleTool,
			ToolID:   toolCallID,
			ToolName: todo.DefaultToolName,
			Content:  string(todoWriteResult),
		}),
	}
	for _, evt := range events {
		evt.Branch = agentName
		if err := c.sessSvc.AppendEvent(ctx, sess, evt); err != nil {
			return fmt.Errorf("append prefill event: %w", err)
		}
	}

	fmt.Println("Prefilled the session with this synthetic prior turn:")
	fmt.Printf("  user → %s\n", truncate(priorUserMsg, 90))
	fmt.Printf("  assistant → tool_call %s(%d items)\n", todo.DefaultToolName, len(items))
	fmt.Printf("  tool → %s response\n", todo.DefaultToolName)
	fmt.Println("Open todos already in state:")
	fmt.Println(formatTodos(items))
	fmt.Println(strings.Repeat("-", 70))
	return nil
}

// newPrefillEvent is a thin wrapper that fills in the boilerplate
// every prefilled event needs: a non-streaming, non-partial
// response carrying exactly one Choice with the supplied message.
// IsValidContent() returns true for tool-call / tool-result
// messages even when Content is empty, which is why the assistant
// tool_call leg is well-formed despite having no text body.
func newPrefillEvent(invocationID, author string, msg model.Message) *event.Event {
	return event.NewResponseEvent(invocationID, author, &model.Response{
		Done: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: msg,
		}},
	})
}

func (c *chat) setup() error {
	modelInstance := openai.New(c.modelName, openai.WithVariant(openai.Variant(c.variant)))
	c.sessSvc = sessioninmemory.NewSessionService()

	// Two construction paths so the same demo can show both
	// "raw tool/todo" baseline and "hardened by todoenforcer".
	// Other than the WithExtensions / WithTools split, the agent
	// configuration is identical, which keeps the comparison
	// honest — any behavioural difference you see in [enforce]
	// lines comes purely from the extension being installed.
	instruction := "You are a careful assistant. When a user asks you to do " +
		"anything with more than 2 steps, call todo_write to plan first, " +
		"then work the items one by one, flipping status as you go. " +
		"Do not produce a final answer while items remain open.\n\n" +
		todo.DefaultToolPrompt

	agentOpts := []llmagent.Option{
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Demo agent that uses todo_write under enforced compliance."),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: intPtr(c.maxTokens),
			Stream:    c.streaming,
		}),
	}

	if c.enforce {
		// Install the extension. WithExtensions also routes the
		// tools the enforcer contributes via extension.Registry.Tools
		// — the enforcer ships both todo_write AND
		// todo_declare_blocker, so we do NOT pass either via
		// WithTools. Passing them here on top would trigger the
		// name-collision dedup and silently drop the enforcer's
		// copies; the dedup is correct but it's not what we want
		// the demo to show.
		enforcer := todoenforcer.New(
			todoenforcer.WithMaxRetries(c.maxRetries),
			todoenforcer.WithOnEnforce(c.observeEnforce),
		)
		agentOpts = append(agentOpts, llmagent.WithExtensions(enforcer))
	} else {
		// Baseline: hand-install todo so the model has a place to
		// write a plan, but with no enforcement. The model is free
		// to mark items pending and still emit a final answer —
		// which is exactly the behaviour the extension exists to
		// fix.
		agentOpts = append(agentOpts, llmagent.WithTools([]tool.Tool{todo.New()}))
	}

	llmAgent := llmagent.New(agentName, agentOpts...)

	c.runner = runner.NewRunner(appName, llmAgent, runner.WithSessionService(c.sessSvc))
	c.userID = "demo-user"
	c.sessionID = fmt.Sprintf("demo-session-%d", time.Now().Unix())

	fmt.Printf("Ready. Session: %s\n\n", c.sessionID)
	return nil
}

// observeEnforce is the OnEnforce callback. It runs on the model
// callback hot path, so we keep it cheap (just printing). We
// serialise prints with a mutex because the enforcer hook can
// race with the streaming output goroutine in processResponse —
// without it [enforce] lines occasionally interleave with
// assistant deltas mid-token.
var enforceMu sync.Mutex

func (c *chat) observeEnforce(evt todoenforcer.EnforceEvent) {
	enforceMu.Lock()
	defer enforceMu.Unlock()
	switch evt.Reason {
	case todoenforcer.ReasonBlocked:
		fmt.Printf("\n  [enforce] BLOCKED (attempt %d/%d): %d in_progress, %d pending → nudge queued\n",
			evt.AttemptNumber, evt.MaxRetries, evt.InProgressCount, evt.PendingCount)
	case todoenforcer.ReasonExhausted:
		fmt.Printf("\n  [enforce] EXHAUSTED after %d attempts: %d in_progress, %d pending → fail-open\n",
			evt.AttemptNumber, evt.InProgressCount, evt.PendingCount)
	case todoenforcer.ReasonBlockerDeclared:
		fmt.Printf("\n  [enforce] BLOCKER_DECLARED: %q → final response will be allowed for the rest of this turn\n",
			evt.BlockerReason)
	}
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

// processResponse renders the stream: tool calls, tool responses
// and assistant text are each formatted distinctly so the
// enforcement loop is easy to follow visually.
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

		if ch.Message.Role == model.RoleTool && ch.Message.ToolID != "" {
			raw := strings.TrimSpace(ch.Message.Content)
			fmt.Printf("  [tool-result] %s\n", truncate(raw, 200))
			var out todo.Output
			if err := json.Unmarshal([]byte(raw), &out); err == nil && len(out.Todos) > 0 {
				fmt.Printf("  [tool-result decoded] %d todos (was %d)\n",
					len(out.Todos), len(out.OldTodos))
				for _, it := range out.Todos {
					fmt.Printf("    - [%s] %s\n", it.Status, it.Content)
				}
			}
			continue
		}

		text := ch.Delta.Content
		if text == "" {
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

// printTodos reads the canonical checklist from session state at
// the end of a turn. The enforcer reads from the same place, so
// what you see here is what the enforcer's AfterModel saw when it
// decided to block (or pass).
func (c *chat) printTodos() {
	sess, err := c.sessSvc.GetSession(context.Background(), session.Key{
		AppName:   appName,
		UserID:    c.userID,
		SessionID: c.sessionID,
	})
	if err != nil || sess == nil {
		return
	}
	items, err := todo.GetTodos(sess, agentName)
	if err != nil {
		fmt.Printf("[todo] decode error: %v\n", err)
		return
	}
	fmt.Println("----- Current checklist -----")
	fmt.Println(formatTodos(items))
	fmt.Println("-----------------------------")
}

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

// truncate keeps the demo output legible when a tool result is
// long (the JSON payload of a 10-item todo list is otherwise
// noisy enough to push the [enforce] lines off-screen).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func intPtr(i int) *int { return &i }
