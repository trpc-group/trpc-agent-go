//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates model switching without the runner.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// chatApp manages the conversation and model switching.
type chatApp struct {
	defaultModel string
	agent        *llmagent.LLMAgent
	models       map[string]model.Model
	sessionID    string
}

func main() {
	// Flags.
	defaultModel := flag.String("model", "gpt-4o-mini", "Default model name")
	flag.Parse()

	app := &chatApp{defaultModel: *defaultModel}
	ctx := context.Background()

	if err := app.setup(ctx); err != nil {
		fmt.Printf("❌ Setup failed: %v\n", err)
		return
	}
	if err := app.startChat(ctx); err != nil {
		fmt.Printf("❌ Chat failed: %v\n", err)
	}
}

// setup initializes models and the agent.
func (a *chatApp) setup(_ context.Context) error {
	fmt.Printf("🚀 Model Switching (no runner)\n")
	fmt.Printf("Default model: %s\n", a.defaultModel)
	fmt.Printf("Commands: /switch X, /new, /exit\n\n")

	// Prepare model map.
	a.models = map[string]model.Model{}
	preload := []string{a.defaultModel, "gpt-4o", "gpt-3.5-turbo"}
	for _, name := range preload {
		if name == "" {
			continue
		}
		if _, ok := a.models[name]; ok {
			continue
		}
		a.models[name] = openai.New(name)
	}

	// Create an agent with the default model.
	a.agent = llmagent.New(
		"switching-agent",
		llmagent.WithModel(a.models[a.defaultModel]),
	)

	// Initialize session id.
	a.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("✅ Ready. Session: %s\n\n", a.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (a *chatApp) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Switch command.
		if strings.HasPrefix(strings.ToLower(userInput), "/switch") {
			fields := strings.Fields(userInput)
			if len(fields) < 2 {
				fmt.Println("Usage: /switch <model-name>.")
				continue
			}
			if err := a.handleSwitch(fields[1]); err != nil {
				fmt.Printf("❌ %v\n", err)
			}
			continue
		}

		// New session.
		if strings.EqualFold(userInput, "/new") {
			a.startNewSession()
			continue
		}

		// Exit.
		if strings.EqualFold(userInput, "/exit") {
			fmt.Println("👋 Bye.")
			return nil
		}

		// Normal message.
		if err := a.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processMessage constructs an invocation and prints the agent response.
func (a *chatApp) processMessage(ctx context.Context, text string) error {
	inv := &agent.Invocation{
		InvocationID: fmt.Sprintf("inv-%d", time.Now().UnixNano()),
		Session: &session.Session{
			ID:      a.sessionID,
			AppName: "model-switch",
			UserID:  "user",
		},
		Message: model.NewUserMessage(text),
	}

	events, err := a.agent.Run(ctx, inv)
	if err != nil {
		return err
	}
	return a.processResponse(events)
}

// processResponse prints streaming or non-streaming responses.
func (a *chatApp) processResponse(eventChan <-chan *event.Event) error {
	var out strings.Builder
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Choices) > 0 {
			ch := ev.Choices[0]
			if ch.Delta.Content != "" {
				out.WriteString(ch.Delta.Content)
			}
			if ch.Message.Content != "" {
				out.WriteString(ch.Message.Content)
			}
		}
		if ev.Done {
			break
		}
	}
	resp := strings.TrimSpace(out.String())
	if resp != "" {
		fmt.Printf("🤖 %s\n", resp)
	}
	return nil
}

// handleSwitch switches active model by name using SetModel.
func (a *chatApp) handleSwitch(name string) error {
	m, ok := a.models[name]
	if !ok {
		fmt.Printf("Available models: ")
		first := true
		for k := range a.models {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s", k)
			first = false
		}
		fmt.Printf("\n")
		return fmt.Errorf("model '%s' not found", name)
	}
	// Set the model dynamically.
	a.agent.SetModel(m)
	fmt.Printf("✅ Switched model to: %s\n", name)
	return nil
}

// startNewSession resets the session id.
func (a *chatApp) startNewSession() {
	old := a.sessionID
	a.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("🆕 New session. Previous: %s, Current: %s\n", old, a.sessionID)
}
