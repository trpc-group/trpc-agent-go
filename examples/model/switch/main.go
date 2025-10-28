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

func main() {
	// Flags.
	defaultModel := flag.String("model", "deepseek-chat", "Default model name")
	flag.Parse()

	app := &chatApp{
		defaultModel: *defaultModel,
	}
	ctx := context.Background()

	if err := app.setup(ctx); err != nil {
		fmt.Printf("❌ Setup failed: %v\n", err)
		return
	}
	if err := app.startChat(ctx); err != nil {
		fmt.Printf("❌ Chat failed: %v\n", err)
	}
}

// chatApp manages the conversation and model switching.
type chatApp struct {
	defaultModel        string
	agent               *llmagent.LLMAgent
	sessionID           string
	nextModelName       string // Model name for next request (empty = use agent's current model).
	usePerRequestSwitch bool   // Whether to use per-request switching for next message.
}

// setup initializes models and the agent.
func (a *chatApp) setup(_ context.Context) error {
	fmt.Printf("🚀 Model Switching (no runner)\n")
	fmt.Printf("Model: %s\n", a.defaultModel)
	fmt.Printf("Commands: /switch X, /model X, /new, /exit\n")
	fmt.Println(strings.Repeat("=", 50))

	// Prepare model map with pre-registered models.
	models := map[string]model.Model{
		"deepseek-chat":     openai.New("deepseek-chat"),
		"deepseek-reasoner": openai.New("deepseek-reasoner"),
	}

	// Get the default model instance.
	defaultModelInstance, ok := models[a.defaultModel]
	if !ok {
		return fmt.Errorf("default model %q not found in registered models", a.defaultModel)
	}

	// Create an agent with pre-registered models.
	// Use WithModels to register all models, and WithModel to set the initial model.
	a.agent = llmagent.New(
		"switching-agent",
		llmagent.WithModels(models),
		llmagent.WithModel(defaultModelInstance),
	)

	// Initialize session id.
	a.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("\n✅ Chat ready! Session: %s\n\n", a.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
func (a *chatApp) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Special commands:")
	fmt.Println("   /switch <model>  - 🔄 Agent-level: change default model for all requests")
	fmt.Println("   /model <model>   - 🎯 Per-request: use model for next request only")
	fmt.Println("   /new             - 🆕 Start a new session")
	fmt.Println("   /exit            - 👋 End the conversation")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Switch command: changes agent's default model (affects all subsequent requests).
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

		// Model command: sets model for next request only (per-request override).
		if strings.HasPrefix(strings.ToLower(userInput), "/model") {
			fields := strings.Fields(userInput)
			if len(fields) < 2 {
				fmt.Println("Usage: /model <model-name>.")
				continue
			}
			a.handleModelCommand(fields[1])
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
	session := &session.Session{
		ID:      a.sessionID,
		AppName: "model-switch",
		UserID:  "user",
	}

	// Build invocation options.
	invOpts := []agent.InvocationOptions{
		agent.WithInvocationSession(session),
		agent.WithInvocationMessage(model.NewUserMessage(text)),
	}

	// Apply per-request model override if specified.
	if a.usePerRequestSwitch && a.nextModelName != "" {
		fmt.Printf("🔧 Per-request override: using model %s for this request only\n", a.nextModelName)
		invOpts = append(invOpts, agent.WithInvocationRunOptions(agent.RunOptions{
			ModelName: a.nextModelName,
		}))
		// Reset for next request.
		a.nextModelName = ""
		a.usePerRequestSwitch = false
	}

	invocation := agent.NewInvocation(invOpts...)

	events, err := a.agent.Run(ctx, invocation)
	if err != nil {
		return err
	}
	return a.processResponse(events)
}

// processResponse prints streaming or non-streaming responses.
func (a *chatApp) processResponse(eventChan <-chan *event.Event) error {
	var out strings.Builder
	firstChunk := true

	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}
		if len(ev.Choices) > 0 {
			ch := ev.Choices[0]
			// Handle streaming delta content.
			if ch.Delta.Content != "" {
				if firstChunk {
					fmt.Print("🤖 ")
					firstChunk = false
				}
				fmt.Print(ch.Delta.Content)
				out.WriteString(ch.Delta.Content)
			}
			// Handle non-streaming message content.
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
		// If streaming, we already printed it; just add newline.
		if !firstChunk {
			fmt.Println()
		} else {
			// Non-streaming: print the complete response.
			fmt.Printf("🤖 %s\n", resp)
		}
	}
	return nil
}

// handleSwitch switches agent's default model (affects all subsequent requests).
func (a *chatApp) handleSwitch(name string) error {
	// Switch model by name using SetModelByName method.
	// This changes the agent's default model for all subsequent requests.
	if err := a.agent.SetModelByName(name); err != nil {
		// List available models on error.
		fmt.Printf("Available models: deepseek-chat, deepseek-reasoner\n")
		return fmt.Errorf("failed to switch model: %w", err)
	}

	// Or you can use SetModel to switch model by model instance, like this:
	//		model := openai.New("deepseek-reasoner")
	//		a.agent.SetModel(model)
	fmt.Printf("✅ Agent-level switch: all requests will now use %s\n", name)
	return nil
}

// handleModelCommand sets model for next request only (per-request override).
func (a *chatApp) handleModelCommand(name string) {
	// This uses per-request model switching via RunOptions.
	// The agent's default model remains unchanged.
	a.nextModelName = name
	a.usePerRequestSwitch = true
	fmt.Printf("✅ Per-request mode: next request will use %s (agent default unchanged)\n", name)
}

// startNewSession resets the session id.
func (a *chatApp) startNewSession() {
	old := a.sessionID
	a.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("🆕 New session. Previous: %s, Current: %s\n", old, a.sessionID)
}
