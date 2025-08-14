//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

// Package main demonstrates placeholder usage in agent instructions with session
// service integration. This example shows how to use {research_topics} placeholder
// that gets replaced with actual values from session state during agent execution.
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

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	maxTokens   = 500
	temperature = 0.7
)

// placeholderDemo manages the interactive session with placeholder functionality.
type placeholderDemo struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

// run starts the interactive demo session.
func (d *placeholderDemo) run() error {
	ctx := context.Background()

	// Initialize the demo environment.
	if err := d.initialize(ctx); err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	// Start interactive command-line session.
	return d.startInteractiveSession(ctx)
}

// initialize sets up the runner with placeholder-enabled agent and session service.
func (d *placeholderDemo) initialize(ctx context.Context) error {
	// Create OpenAI model instance.
	modelInstance := openai.New(d.modelName)

	// Initialize in-memory session service.
	sessionService := inmemory.NewSessionService()
	d.sessionService = sessionService

	// Configure generation parameters.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      true,
	}

	// Setup session identifiers.
	d.userID = "demo-user"
	d.sessionID = fmt.Sprintf("placeholder-demo-%d", time.Now().Unix())
	appName := "placeholder-demo"

	// Create session with initial research topics.
	sessionService.CreateSession(ctx, session.Key{
		AppName:   appName,
		UserID:    d.userID,
		SessionID: d.sessionID,
	}, session.StateMap{
		"research_topics": []byte("artificial intelligence, machine learning, " +
			"deep learning, neural networks"),
	})

	// Create research agent with placeholder in instructions.
	researchAgent := llmagent.New(
		"research-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Research assistant that uses placeholder "+
			"values from session state"),
		llmagent.WithInstruction("You are a specialized research assistant. "+
			"Focus your research on the following topics: {research_topics}. "+
			"Provide comprehensive analysis, recent developments, and practical "+
			"applications. Be thorough but concise, and always cite sources "+
			"when possible."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Create runner with session service integration.
	d.runner = runner.NewRunner(
		appName,
		researchAgent,
		runner.WithSessionService(sessionService),
	)

	fmt.Printf("✅ Placeholder Demo initialized! Session: %s\n", d.sessionID)
	fmt.Printf("📝 Agent: %s\n", researchAgent.Info().Name)
	fmt.Printf("🔗 Placeholder: {research_topics} → Session State\n\n")

	return nil
}

// startInteractiveSession runs the command-line interactive loop.
func (d *placeholderDemo) startInteractiveSession(ctx context.Context) error {
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

		// Handle exit command.
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Handle session state commands.
		if strings.HasPrefix(userInput, "/set-topics ") {
			d.handleSetTopics(ctx, userInput)
			continue
		}

		if strings.HasPrefix(userInput, "/show-topics") {
			d.handleShowTopics(ctx)
			continue
		}

		// Process regular user message.
		if err := d.processUserMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between interactions.
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// handleSetTopics updates the research topics in session state.
func (d *placeholderDemo) handleSetTopics(ctx context.Context, input string) {
	topics := strings.TrimPrefix(input, "/set-topics ")
	if topics == "" {
		fmt.Println("❌ Please provide topics. Usage: /set-topics <topics>")
		return
	}

	// Update user state with new topics.
	err := d.sessionService.UpdateUserState(ctx, session.UserKey{
		AppName: "placeholder-demo",
		UserID:  d.userID,
	}, session.StateMap{
		"research_topics": []byte(topics),
	})
	if err != nil {
		fmt.Printf("❌ Error updating topics: %v\n", err)
		return
	}

	fmt.Printf("✅ Research topics updated to: %s\n", topics)
}

// handleShowTopics displays current research topics from session state.
func (d *placeholderDemo) handleShowTopics(ctx context.Context) {
	state, err := d.sessionService.GetSession(ctx, session.Key{
		AppName:   "placeholder-demo",
		UserID:    d.userID,
		SessionID: d.sessionID,
	})
	if err != nil {
		fmt.Printf("❌ Error retrieving session state: %v\n", err)
		return
	}

	if state == nil {
		fmt.Println("📋 No session found.")
		return
	}

	if topics, exists := state.State["research_topics"]; exists {
		fmt.Printf("📋 Current research topics: %s\n", string(topics))
	} else {
		fmt.Println("📋 No research topics set.")
	}
}

// processUserMessage handles a single user message through the agent.
func (d *placeholderDemo) processUserMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Execute the agent through the runner.
	eventChan, err := d.runner.Run(ctx, d.userID, d.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	// Process the streaming response.
	return d.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response from the agent.
func (d *placeholderDemo) processStreamingResponse(eventChan <-chan *event.Event) error {
	var agentStarted bool

	for event := range eventChan {
		if err := d.handleEvent(event, &agentStarted); err != nil {
			return err
		}

		// Check for completion.
		if event.Done && event.Response != nil &&
			event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

// handleEvent processes a single event from the agent.
func (d *placeholderDemo) handleEvent(event *event.Event, agentStarted *bool) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle agent start.
	if !*agentStarted {
		*agentStarted = true
		fmt.Printf("🔬 Research Agent: ")
	}

	// Handle streaming content.
	if len(event.Choices) > 0 {
		choice := event.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
		}
	}

	return nil
}

// Helper functions for configuration.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

func main() {
	// Parse command line arguments.
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()

	fmt.Printf("🔑 Placeholder Demo - Session State Integration\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the session\n")
	fmt.Println("Features: Dynamic placeholder replacement with session state")
	fmt.Println("Commands: /set-topics <topics>, /show-topics")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()
	fmt.Println("💡 Example interactions:")
	fmt.Println("   • Ask: 'What are the latest developments?'")
	fmt.Println("   • Set topics: /set-topics 'quantum computing, cryptography'")
	fmt.Println("   • Show topics: /show-topics")
	fmt.Println("   • Ask: 'Explain recent breakthroughs'")
	fmt.Println()
	fmt.Println("🔄 How placeholders work:")
	fmt.Println("   1. {research_topics} in agent instructions")
	fmt.Println("   2. Gets replaced with session state value")
	fmt.Println("   3. Agent uses actual topics for research")
	fmt.Println()

	// Create and run the demo.
	demo := &placeholderDemo{
		modelName: *modelName,
	}

	if err := demo.run(); err != nil {
		log.Fatalf("Placeholder demo failed: %v", err)
	}
}
