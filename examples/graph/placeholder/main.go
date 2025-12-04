//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates placeholder usage in a Graph (StateGraph + GraphAgent)
// workflow with session state integration. It mirrors the capabilities from
// examples/placeholder but implemented as a graph with a single LLM node.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

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
	defaultModelName = "deepseek-chat"
	appName          = "graph-placeholder-demo"
)

type demo struct {
	modelName      string
	sessionService session.Service
	runner         runner.Runner
	userID         string
	sessionID      string
}

func main() {
	model := flag.String("model", defaultModelName, "Model name to use")
	flag.Parse()

	fmt.Println("🔗 Graph Placeholder Demo")
	fmt.Printf("Model: %s\n", *model)
	fmt.Println("Type 'exit' to end the session")
	fmt.Println("Commands: /set-user-topics <topics>, /set-app-banner <text>, /show-state")
	fmt.Println(strings.Repeat("=", 60))

	d := &demo{modelName: *model}
	if err := d.run(context.Background()); err != nil {
		log.Fatalf("demo failed: %v", err)
	}
}

func (d *demo) run(ctx context.Context) error {
	if err := d.initialize(ctx); err != nil {
		return err
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer d.runner.Close()

	return d.loop(ctx)
}

func (d *demo) initialize(ctx context.Context) error {
	// Session service.
	d.sessionService = inmemory.NewSessionService()

	// Create initial session state with placeholders.
	d.userID = "user"
	d.sessionID = fmt.Sprintf("sess-%d", time.Now().Unix())
	if _, err := d.sessionService.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	}, session.StateMap{
		// Unprefixed (readonly) placeholder example
		"research_topics": []byte("artificial intelligence, machine learning, deep learning, neural networks"),
		// Prefixed placeholders (mutable via service)
		"user:topics": []byte("quantum computing, cryptography"),
		"app:banner":  []byte("Research Mode"),
	}); err != nil {
		return fmt.Errorf("create session failed: %w", err)
	}

	// Build graph with a single LLM node using placeholders in instruction.
	mdl := openai.New(d.modelName)
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)
	sg.AddLLMNode("research-node", mdl,
		"You are a specialized research assistant. "+
			"Focus on read-only topics: {research_topics}. "+
			"Also consider user interests: {user:topics?}. "+
			"If an app banner is provided, show it briefly: {app:banner?}. "+
			"Provide comprehensive analysis and up-to-date insights.",
		nil,
	).SetEntryPoint("research-node").SetFinishPoint("research-node")

	g, err := sg.Compile()
	if err != nil {
		return fmt.Errorf("compile graph failed: %w", err)
	}

	// GraphAgent + Runner
	ga, err := graphagent.New("graph-placeholder-agent", g,
		graphagent.WithDescription("Graph agent showcasing placeholder injection in LLM node"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("create graph agent failed: %w", err)
	}
	d.runner = runner.NewRunner(appName, ga, runner.WithSessionService(d.sessionService))

	fmt.Printf("✅ Initialized. Session: %s\n", d.sessionID)
	fmt.Println("🔗 Placeholders: {research_topics} (readonly), {user:topics?}, {app:banner?}")
	return nil
}

func (d *demo) loop(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("input scanner error: %w", err)
			}
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "exit") {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		switch {
		case strings.HasPrefix(line, "/set-user-topics "):
			d.handleSetUserTopics(ctx, strings.TrimPrefix(line, "/set-user-topics "))
			continue
		case strings.HasPrefix(line, "/set-app-banner "):
			d.handleSetAppBanner(ctx, strings.TrimPrefix(line, "/set-app-banner "))
			continue
		case strings.HasPrefix(line, "/show-state"):
			d.handleShowState(ctx)
			continue
		}

		if err := d.ask(ctx, line); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	return nil
}

func (d *demo) handleSetUserTopics(ctx context.Context, topics string) {
	topics = strings.TrimSpace(topics)
	if topics == "" {
		fmt.Println("❌ Please provide topics. Usage: /set-user-topics <topics>")
		return
	}
	if err := d.sessionService.UpdateUserState(ctx, session.UserKey{AppName: appName, UserID: d.userID}, session.StateMap{
		"topics": []byte(topics),
	}); err != nil {
		fmt.Printf("❌ Error updating user topics: %v\n", err)
		return
	}
	fmt.Printf("✅ User topics updated to: %s\n", topics)
}

func (d *demo) handleSetAppBanner(ctx context.Context, banner string) {
	banner = strings.TrimSpace(banner)
	if banner == "" {
		fmt.Println("❌ Please provide a banner. Usage: /set-app-banner <banner>")
		return
	}
	if err := d.sessionService.UpdateAppState(ctx, appName, session.StateMap{
		"banner": []byte(banner),
	}); err != nil {
		fmt.Printf("❌ Error updating app banner: %v\n", err)
		return
	}
	fmt.Printf("✅ App banner updated to: %s\n", banner)
}

func (d *demo) handleShowState(ctx context.Context) {
	sess, err := d.sessionService.GetSession(ctx, session.Key{AppName: appName, UserID: d.userID, SessionID: d.sessionID})
	if err != nil {
		fmt.Printf("❌ Error retrieving session state: %v\n", err)
		return
	}
	if sess == nil {
		fmt.Println("📋 No session found.")
		return
	}
	fmt.Println("📋 Current Session State:")
	for k, v := range sess.State {
		fmt.Printf("   - %s: %s\n", k, string(v))
	}
}

func (d *demo) ask(ctx context.Context, text string) error {
	msg := model.NewUserMessage(text)
	ch, err := d.runner.Run(ctx, d.userID, d.sessionID, msg)
	if err != nil {
		return fmt.Errorf("run failed: %w", err)
	}
	return d.stream(ch)
}

func (d *demo) stream(ch <-chan *event.Event) error {
	var started bool
	for ev := range ch {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}
		// Stream tokens for model responses.
		if len(ev.Choices) > 0 {
			delta := ev.Choices[0].Delta.Content
			if delta != "" {
				if !started {
					fmt.Print("🤖 Research Node: ")
					started = true
				}
				fmt.Print(delta)
			}
		}
		// Stop line on completion.
		if ev.Done && ev.Response != nil && ev.Response.Object == model.ObjectTypeRunnerCompletion && started {
			fmt.Println()
			started = false
		}
	}
	return nil
}
