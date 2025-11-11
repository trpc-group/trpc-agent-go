//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates model switching with the runner.
//
// This example shows two ways to switch models:
//
// 1. Agent-level switching (/switch command):
//   - Changes the agent's default model permanently.
//   - Affects all subsequent requests until changed again.
//   - Use agent.SetModelByName() or agent.SetModel().
//   - Suitable for: changing default behavior, user preferences.
//
// 2. Per-request switching (/model command):
//   - Overrides model for a single request only.
//   - Agent's default model remains unchanged.
//   - Use agent.WithModelName() or agent.WithModel() in RunOptions.
//   - Suitable for: temporary overrides, A/B testing, special queries.
//
// Example usage:
//
//	/switch deepseek-reasoner  → All future requests use reasoner.
//	/model deepseek-chat       → Next request uses chat, then back to
//	                              reasoner.
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
	"trpc.group/trpc-go/trpc-agent-go/runner"
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

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer app.runner.Close()

	if err := app.startChat(ctx); err != nil {
		fmt.Printf("❌ Chat failed: %v\n", err)
	}
}

// chatApp manages the conversation and model switching.
//
// This struct demonstrates state management for two switching approaches:
//   - agent: holds the default model (changed by Method 1).
//   - nextModelName/usePerRequestSwitch: temporary state for Method 2.
type chatApp struct {
	defaultModel        string
	agent               *llmagent.LLMAgent
	runner              runner.Runner
	models              map[string]model.Model // Registered models for validation.
	sessionID           string                 // Current session ID.
	nextModelName       string                 // Model name for next request (empty = use agent's current model).
	usePerRequestSwitch bool                   // Whether to use per-request switching for next message.
}

// setup initializes models, agent, and runner.
//
// Key setup steps for model switching:
//  1. Pre-register all available models in a map.
//  2. Create agent with WithModels() to enable name-based lookup.
//  3. Set initial default model with WithModel().
//
// This setup enables both switching methods:
//   - Method 1: agent.SetModelByName() works because models are registered.
//   - Method 2: agent.WithModelName() in RunOptions also uses the registry.
func (a *chatApp) setup(_ context.Context) error {
	fmt.Printf("🚀 Model Switching Example\n")
	fmt.Printf("Model: %s\n", a.defaultModel)
	fmt.Printf("Commands: /switch X, /model X, /new, /exit\n")
	fmt.Println(strings.Repeat("=", 50))

	// Prepare model map with pre-registered models.
	// Pre-registration is required for name-based model switching.
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
	// WithModels: registers the model map for name-based lookup.
	// WithModel: sets the initial default model.
	a.agent = llmagent.New(
		"switching-agent",
		llmagent.WithModels(models),
		llmagent.WithModel(defaultModelInstance),
	)

	// Store models map for validation in handleModelCommand.
	a.models = models

	// Create runner.
	a.runner = runner.NewRunner("model-switch", a.agent)

	// Initialize session ID.
	a.sessionID = "session-1"

	fmt.Printf("\n✅ Chat ready! Session: %s\n\n", a.sessionID)
	return nil
}

// startChat runs the interactive conversation loop.
//
// Comparison of two model switching methods:
//
// ┌─────────────────┬──────────────────────┬──────────────────────┐
// │ Feature         │ Method 1: Agent-level│ Method 2: Per-request│
// ├─────────────────┼──────────────────────┼──────────────────────┤
// │ Scope           │ All future requests  │ Single request only  │
// │ Persistence     │ Until changed again  │ Auto-revert after use│
// │ Thread-safety   │ Yes (atomic)         │ Yes (isolated)       │
// │ API             │ SetModelByName()     │ WithModelName()      │
// │ State location  │ Agent instance       │ RunOptions           │
// │ Typical use     │ User preference      │ A/B testing, fallback│
// └─────────────────┴──────────────────────┴──────────────────────┘
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

		// Model command: sets model for next request only (per-request
		// override).
		if strings.HasPrefix(strings.ToLower(userInput), "/model") {
			fields := strings.Fields(userInput)
			if len(fields) < 2 {
				fmt.Println("Usage: /model <model-name>.")
				continue
			}
			if err := a.handleModelCommand(fields[1]); err != nil {
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

// processMessage sends a message to the agent via runner.
//
// This function demonstrates how to apply per-request model switching:
//  1. Check if a temporary model override is set.
//  2. If yes, add agent.WithModelName() to RunOptions.
//  3. Clear the temporary state after use.
//
// Alternative approaches for per-request switching:
//   - agent.WithModelName(name): lookup from registered models.
//   - agent.WithModel(instance): use a specific model instance.
//   - agent.WithRuntimeState(map): pass additional runtime parameters.
//
// Note: RunOptions are passed as variadic arguments to runner.Run().
func (a *chatApp) processMessage(ctx context.Context, text string) error {
	// Build run options.
	var runOpts []agent.RunOption

	// Apply per-request model override if specified.
	// This demonstrates Method 2: per-request switching.
	if a.usePerRequestSwitch && a.nextModelName != "" {
		fmt.Printf("🔧 Per-request override: using model %s for this request only\n", a.nextModelName)
		// Option 1: Switch by model name (recommended if model is pre-registered).
		runOpts = append(runOpts, agent.WithModelName(a.nextModelName))

		// Option 2: Switch by model instance (use when you need custom config).
		// modelInstance := openai.New(a.nextModelName)
		// runOpts = append(runOpts, agent.WithModel(modelInstance))

		// Reset for next request to ensure this only affects current request.
		a.nextModelName = ""
		a.usePerRequestSwitch = false
	}

	// Run the agent via runner.
	// The runner will use the per-request model if specified, otherwise
	// falls back to the agent's default model.
	events, err := a.runner.Run(
		ctx,
		"user",
		a.sessionID,
		model.NewUserMessage(text),
		runOpts...,
	)
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

// handleSwitch switches agent's default model (affects all subsequent
// requests).
//
// Agent-level switching (Method 1):
//   - Permanently changes the agent's default model.
//   - All subsequent requests use the new model.
//   - Thread-safe: can be called concurrently.
//   - State persists until explicitly changed again.
//
// Implementation options:
//
//	a) SetModelByName(name): lookup model from pre-registered models map.
//	b) SetModel(instance): directly set a model instance.
//
// Use cases:
//   - User changes their preferred model.
//   - Application switches to a different model tier.
//   - Adapting to different conversation contexts.
func (a *chatApp) handleSwitch(name string) error {
	// Switch model by name using SetModelByName method.
	// This changes the agent's default model for all subsequent requests.
	if err := a.agent.SetModelByName(name); err != nil {
		// List available models on error.
		fmt.Printf("Available models: deepseek-chat, deepseek-reasoner\n")
		return fmt.Errorf("failed to switch model: %w", err)
	}

	// Alternative: use SetModel to switch by model instance.
	// This is useful when you need to create a new model with specific
	// configuration:
	//   model := openai.New("deepseek-reasoner")
	//   a.agent.SetModel(model)
	fmt.Printf("✅ Agent-level switch: all requests will now use %s\n", name)
	return nil
}

// handleModelCommand sets model for next request only (per-request override).
//
// Per-request switching (Method 2):
//   - Temporarily overrides model for a single request.
//   - Agent's default model remains unchanged.
//   - No side effects on concurrent requests.
//   - Automatically reverts after the request completes.
//
// Implementation:
//   - Validate the model name exists in registered models.
//   - Store the model name/instance temporarily.
//   - Pass it via agent.WithModelName() or agent.WithModel() in RunOptions.
//   - Clear the temporary state after use.
//
// Use cases:
//   - Testing different models for comparison.
//   - Using a specialized model for specific query types.
//   - A/B testing without affecting other users.
//   - Fallback to a different model for retry scenarios.
func (a *chatApp) handleModelCommand(name string) error {
	// Validate that the model exists in the agent's registered models.
	// This prevents runtime errors when the request is executed.
	if _, ok := a.models[name]; !ok {
		// List available models on error.
		fmt.Printf("Available models: deepseek-chat, deepseek-reasoner\n")
		return fmt.Errorf("model %q not found in registered models", name)
	}

	// This uses per-request model switching via agent.WithModelName().
	// The agent's default model remains unchanged.
	a.nextModelName = name
	a.usePerRequestSwitch = true
	fmt.Printf("✅ Per-request mode: next request will use %s (agent default unchanged)\n", name)
	return nil
}

// startNewSession creates a new session ID and clears history.
func (a *chatApp) startNewSession() {
	old := a.sessionID
	// Generate a new session ID based on timestamp.
	a.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	fmt.Printf("🆕 New session started. Previous: %s, Current: %s\n", old, a.sessionID)
	fmt.Println("   (Session history has been cleared)")
}
