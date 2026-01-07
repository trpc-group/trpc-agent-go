//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates {invocation:*} placeholders in a GraphAgent
// (StateGraph + Runner) workflow.
//
// {invocation:*} reads from invocation-scoped state (invocation.SetState),
// which lives only for the current run. This is useful for request metadata
// you do not want to store in the session.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName          = "graph-invocation-placeholder-demo"
	defaultModelName = "deepseek-chat"
	defaultUserID    = "user"

	agentName = "invocation-placeholder-agent"
	nodeID    = "assistant"

	invKeyRequestID = "request_id"
	invKeyCase      = "case"

	cmdHelp      = "/help"
	cmdShowState = "/show-state"
	cmdClearCase = "/clear-case"
	cmdCasePref  = "/case "
	exitWord     = "exit"
)

type ctxKeyCase struct{}

type demo struct {
	modelName string

	userID    string
	sessionID string
	caseValue string

	sessionService session.Service
	runner         runner.Runner
}

func main() {
	modelName := flag.String(
		"model",
		defaultModelName,
		"Model name to use",
	)
	flag.Parse()

	fmt.Println("🔖 Graph Invocation Placeholder Demo")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type '%s' to quit\n", exitWord)
	fmt.Println("Commands:")
	fmt.Printf("  - %s\n", cmdHelp)
	fmt.Printf("  - %s\n", cmdShowState)
	fmt.Printf("  - %s <value>\n", strings.TrimSpace(cmdCasePref))
	fmt.Printf("  - %s\n", cmdClearCase)
	fmt.Println(strings.Repeat("=", 60))

	d := &demo{
		modelName: *modelName,
		userID:    defaultUserID,
	}
	if err := d.run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func (d *demo) run(ctx context.Context) error {
	if err := d.initialize(ctx); err != nil {
		return err
	}
	defer d.runner.Close()
	return d.loop(ctx)
}

func (d *demo) initialize(ctx context.Context) error {
	d.sessionService = inmemory.NewSessionService()
	d.sessionID = fmt.Sprintf("sess-%d", time.Now().Unix())

	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	}
	if _, err := d.sessionService.CreateSession(
		ctx,
		key,
		session.StateMap{},
	); err != nil {
		return fmt.Errorf("create session failed: %w", err)
	}

	mdl := openai.New(d.modelName)
	sg := graph.NewStateGraph(graph.MessagesStateSchema())

	instruction := strings.Join([]string{
		"You are a helpful assistant.",
		"",
		"Invocation state (per run):",
		"RequestID: {invocation:request_id}",
		"Case: {invocation:case?}",
		"",
		"Always start your reply with:",
		"RequestID=<id> Case=<case>",
	}, "\n")
	sg.AddLLMNode(nodeID, mdl, instruction, nil)
	sg.SetEntryPoint(nodeID).SetFinishPoint(nodeID)

	compiled, err := sg.Compile()
	if err != nil {
		return fmt.Errorf("compile graph failed: %w", err)
	}

	cbs := agent.NewCallbacks()
	cbs.RegisterBeforeAgent(d.beforeAgent)

	ga, err := graphagent.New(
		agentName,
		compiled,
		graphagent.WithAgentCallbacks(cbs),
	)
	if err != nil {
		return fmt.Errorf("create graph agent failed: %w", err)
	}

	d.runner = runner.NewRunner(
		appName,
		ga,
		runner.WithSessionService(d.sessionService),
	)

	fmt.Printf("✅ Session ready: %s\n", d.sessionID)
	fmt.Println("ℹ️  Use /case to set Case for the next run.")
	return nil
}

func (d *demo) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	if args == nil || args.Invocation == nil {
		return &agent.BeforeAgentResult{}, nil
	}
	inv := args.Invocation

	inv.SetState(invKeyRequestID, inv.RunOptions.RequestID)
	if v := ctx.Value(ctxKeyCase{}); v != nil {
		if s, ok := v.(string); ok && s != "" {
			inv.SetState(invKeyCase, s)
		}
	}

	return &agent.BeforeAgentResult{}, nil
}

func (d *demo) loop(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			return scanner.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, exitWord) {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if handled := d.handleCommand(ctx, line); handled {
			continue
		}

		msg := model.NewUserMessage(line)
		runCtx := context.WithValue(ctx, ctxKeyCase{}, d.caseValue)
		ch, err := d.runner.Run(runCtx, d.userID, d.sessionID, msg)
		if err != nil {
			fmt.Printf("❌ run failed: %v\n\n", err)
			continue
		}
		if err := stream(ch); err != nil {
			fmt.Printf("❌ stream error: %v\n", err)
		}
		fmt.Println()
	}
}

func (d *demo) handleCommand(ctx context.Context, line string) bool {
	switch {
	case strings.EqualFold(line, cmdHelp):
		d.printHelp()
		return true
	case strings.EqualFold(line, cmdShowState):
		d.printSessionState(ctx)
		return true
	case strings.EqualFold(line, cmdClearCase):
		d.caseValue = ""
		fmt.Println("✅ Case cleared")
		return true
	case strings.HasPrefix(line, cmdCasePref):
		d.caseValue = strings.TrimSpace(strings.TrimPrefix(line, cmdCasePref))
		if d.caseValue == "" {
			fmt.Printf("❌ Usage: %s<value>\n", cmdCasePref)
			return true
		}
		fmt.Printf("✅ Case set to: %s\n", d.caseValue)
		return true
	default:
		return false
	}
}

func (d *demo) printHelp() {
	fmt.Println("Commands:")
	fmt.Printf("  - %s: print help\n", cmdHelp)
	fmt.Printf("  - %s: show session state\n", cmdShowState)
	fmt.Printf("  - %s <value>: set Case for next run\n",
		strings.TrimSpace(cmdCasePref))
	fmt.Printf("  - %s: clear Case\n", cmdClearCase)
	fmt.Println("Notes:")
	fmt.Println("  - {invocation:*} is not stored in the session.")
	fmt.Println("  - Each run gets a new invocation.")
}

func (d *demo) printSessionState(ctx context.Context) {
	key := session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	}
	sess, err := d.sessionService.GetSession(ctx, key)
	if err != nil {
		fmt.Printf("❌ get session failed: %v\n", err)
		return
	}
	if sess == nil {
		fmt.Println("📋 Session State: (session not found)")
		return
	}

	state := sess.SnapshotState()
	if len(state) == 0 {
		fmt.Println("📋 Session State: (empty)")
		return
	}
	fmt.Println("📋 Session State:")
	for k, v := range state {
		fmt.Printf("  - %s: %s\n", k, string(v))
	}
}

func stream(ch <-chan *event.Event) error {
	var started bool
	for ev := range ch {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Choices) > 0 {
			delta := ev.Choices[0].Delta.Content
			if delta != "" {
				if !started {
					fmt.Print("🤖 Assistant: ")
					started = true
				}
				fmt.Print(delta)
			}
		}
		if !ev.Done || ev.Response == nil {
			continue
		}
		if ev.Response.Object != model.ObjectTypeRunnerCompletion {
			continue
		}
		if started {
			fmt.Println()
			started = false
		}
	}
	return nil
}
