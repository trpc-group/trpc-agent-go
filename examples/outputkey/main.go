//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates output key functionality using ChainAgent
// with two sub-agents: one that stores data with output_key, and another that
// retrieves the data using placeholders in instructions.
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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
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

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🔑 Output Key Chain Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println("Chain: Research Agent → Content Writer Agent")
	fmt.Println("Features: Research findings → Engaging content creation")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println()
	fmt.Println("💡 Example queries to try:")
	fmt.Println("   • What are the latest developments in quantum computing?")
	fmt.Println("   • Explain the impact of AI on healthcare in 2024")
	fmt.Println("   • What are the environmental benefits of electric vehicles?")
	fmt.Println("   • How does blockchain technology work and what are its applications?")
	fmt.Println("   • What are the emerging trends in renewable energy?")
	fmt.Println()
	fmt.Println("🔄 How it works:")
	fmt.Println("   1. Research Agent finds comprehensive information on your topic")
	fmt.Println("   2. Writer Agent creates an engaging summary based on the research")
	fmt.Println()

	// Create and run the chat.
	chat := &outputKeyChainChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Output key chain chat failed: %v", err)
	}
}

// outputKeyChainChat manages the output key chain conversation.
type outputKeyChainChat struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

// run starts the interactive chat session.
func (c *outputKeyChainChat) run() error {
	ctx := context.Background()

	// Setup the runner with chain agent.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with chain agent and sub-agents.
func (c *outputKeyChainChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create session service.
	sessionService := inmemory.NewSessionService()
	c.sessionService = sessionService

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      true,
	}

	// Create Research Agent that finds and stores key information.
	researchAgent := llmagent.New(
		"research-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A research assistant that finds and extracts key information from user queries"),
		llmagent.WithInstruction("You are a skilled research assistant. When users ask questions, "+
			"conduct thorough research and extract the most important facts and information. "+
			"Focus on accuracy and provide comprehensive details that would be useful for "+
			"creating informative content. Be thorough but concise in your findings."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithOutputKey("research_findings"),
	)

	// Create Content Writer Agent that creates summaries based on research.
	writerAgent := llmagent.New(
		"writer-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A content writer that creates engaging summaries based on research findings"),
		llmagent.WithInstruction("You are an experienced content writer. Based on the research findings: {research_findings}, "+
			"create an engaging and informative summary. Make it interesting to read, well-structured, "+
			"and accessible to a general audience. Include key facts while maintaining a conversational tone."),
		llmagent.WithGenerationConfig(genConfig),
	)

	// Create Chain Agent with sub-agents.
	// No tools needed since we're using placeholder variables instead.
	chainAgent := chainagent.New(
		"output-key-chain",
		chainagent.WithSubAgents([]agent.Agent{researchAgent, writerAgent}),
	)

	// Create runner with the chain agent and session service.
	appName := "output-key-chain-demo"
	c.runner = runner.NewRunner(
		appName,
		chainAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("output-key-session-%d", time.Now().Unix())

	fmt.Printf("✅ Output Key Chain ready! Session: %s\n", c.sessionID)
	fmt.Printf("📝 Agents: %s → %s\n\n",
		researchAgent.Info().Name,
		writerAgent.Info().Name)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *outputKeyChainChat) startChat(ctx context.Context) error {
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

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange through the agent chain.
func (c *outputKeyChainChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the chain agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run chain agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response from the agent chain.
func (c *outputKeyChainChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		currentAgent string
		agentStarted bool
	)
	for event := range eventChan {
		if err := c.handleChainEvent(event, &currentAgent, &agentStarted); err != nil {
			return err
		}

		// Check if this is the final runner completion event.
		if event.Done && event.Response != nil && event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("\n")
			break
		}
	}
	return nil
}

// handleChainEvent processes a single event from the agent chain.
func (c *outputKeyChainChat) handleChainEvent(
	event *event.Event,
	currentAgent *string,
	agentStarted *bool,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle agent transitions.
	c.handleAgentTransition(event, currentAgent, agentStarted)

	// Handle streaming content.
	c.handleStreamingContent(event, currentAgent)

	return nil
}

// handleAgentTransition manages agent switching and display.
func (c *outputKeyChainChat) handleAgentTransition(
	event *event.Event,
	currentAgent *string,
	agentStarted *bool,
) {
	if event.Author != *currentAgent {
		if *agentStarted {
			fmt.Printf("\n")
		}
		*currentAgent = event.Author
		*agentStarted = true

		// Display agent transition.
		c.displayAgentTransition(*currentAgent)
	}
}

// displayAgentTransition shows the current agent with appropriate emoji.
func (c *outputKeyChainChat) displayAgentTransition(currentAgent string) {
	switch currentAgent {
	case "research-agent":
		fmt.Printf("🔬 Research Agent: ")
	case "writer-agent":
		fmt.Printf("✍️  Writer Agent: ")
	default:
		// No display for unknown agents.
	}
}

// handleStreamingContent processes streaming content from agents.
func (c *outputKeyChainChat) handleStreamingContent(event *event.Event, currentAgent *string) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		if choice.Delta.Content != "" {
			fmt.Print(choice.Delta.Content)
		}
	}
}

// Helper functions.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
