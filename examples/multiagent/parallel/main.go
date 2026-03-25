//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a parallel multi-agent system using the trpc-agent-go framework.
// This example shows how to coordinate multiple agents working concurrently on different aspects
// of the same problem, with proper handling of interleaved event streams.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"flag"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	maxTokens   = 350 // Increased slightly for more detailed analysis
	temperature = 0.7
)

// parallelChat manages the parallel multi-agent conversation.
type parallelChat struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// NewParallelChat creates a new parallel chat instance.
func NewParallelChat(modelName string) *parallelChat {
	return &parallelChat{
		modelName: modelName,
	}
}

// displayWelcomeMessage shows the initial welcome and instructions.
func (c *parallelChat) displayWelcomeMessage() {
	fmt.Println("╭──────────────────────────────────────────────────────────────────╮")
	fmt.Println("│                     🤖 Parallel Multi-Agent System 🤖            │")
	fmt.Println("├──────────────────────────────────────────────────────────────────┤")
	fmt.Println("│                                                                  │")
	fmt.Println("│ This example demonstrates agents analyzing different aspects:    │")
	fmt.Println("│ • 📊 Market Analysis - Market trends, size, competition          │")
	fmt.Println("│ • ⚙️  Technical Assessment - Implementation, requirements        │")
	fmt.Println("│ • ⚠️  Risk Evaluation - Challenges, risks, mitigation           │")
	fmt.Println("│ • 🚀 Opportunity Analysis - Benefits, potential, ROI             │")
	fmt.Println("│                                                                  │")
	fmt.Println("│ All agents work simultaneously on different perspectives of     │")
	fmt.Println("│ your query, providing comprehensive multi-angle analysis.       │")
	fmt.Println("│                                                                  │")
	fmt.Println("│ Example queries:                                                 │")
	fmt.Println("│ • 'Should we implement blockchain for supply chain?'            │")
	fmt.Println("│ • 'Evaluate adopting remote work permanently'                   │")
	fmt.Println("│ • 'Assess launching an AI-powered customer service bot'         │")
	fmt.Println("│                                                                  │")
	fmt.Println("│ Commands: 'help', 'quit', 'exit'                                │")
	fmt.Println("╰──────────────────────────────────────────────────────────────────╯")
	fmt.Println()
}

// setup creates the runner with parallel agent and sub-agents.
func (c *parallelChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create generation config.
	// Note: Streaming disabled for parallel agents to avoid character-level interleaving
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      false,
	}

	// Market Analysis Agent - Focuses on market dynamics, trends, competition.
	marketAgent := llmagent.New(
		"market-analysis",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Analyzes market trends, size, competition, and dynamics"),
		llmagent.WithInstruction("You are a Market Analysis specialist. Analyze the market perspective of the given topic. Focus on: market size and growth, competitive landscape, industry trends, customer demand, market positioning, and economic factors. Provide concrete data points where possible. Be analytical but concise. End with 'Market analysis complete.'"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{}),
	)

	// Technical Assessment Agent - Focuses on implementation, technical requirements.
	technicalAgent := llmagent.New(
		"technical-assessment",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Evaluates technical feasibility, requirements, and implementation"),
		llmagent.WithInstruction("You are a Technical Assessment specialist. Evaluate the technical aspects of the given topic. Focus on: technical feasibility, implementation requirements, technology stack, infrastructure needs, scalability considerations, integration challenges, and technical best practices. Be specific about technical details. End with 'Technical assessment complete.'"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{}),
	)

	// Risk Evaluation Agent - Focuses on risks, challenges, and mitigation.
	riskAgent := llmagent.New(
		"risk-evaluation",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Identifies risks, challenges, and mitigation strategies"),
		llmagent.WithInstruction("You are a Risk Evaluation specialist. Identify and assess risks related to the given topic. Focus on: potential risks and challenges, regulatory compliance, security concerns, operational risks, financial risks, timeline risks, and mitigation strategies. Prioritize risks by severity and likelihood. End with 'Risk evaluation complete.'"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{}),
	)

	// Opportunity Analysis Agent - Focuses on benefits, opportunities, ROI.
	opportunityAgent := llmagent.New(
		"opportunity-analysis",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Analyzes opportunities, benefits, and potential returns"),
		llmagent.WithInstruction("You are an Opportunity Analysis specialist. Identify opportunities and benefits related to the given topic. Focus on: strategic advantages, cost savings, revenue opportunities, efficiency gains, competitive advantages, innovation potential, and ROI projections. Quantify benefits where possible. End with 'Opportunity analysis complete.'"),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{}),
	)

	// Create the parallel agent coordinator.
	parallelAgent := parallelagent.New(
		"parallel-demo",
		parallelagent.WithSubAgents([]agent.Agent{marketAgent, technicalAgent, riskAgent, opportunityAgent}),
	)

	// Create runner with the parallel agent.
	appName := "parallel-agent-demo"
	c.runner = runner.NewRunner(appName, parallelAgent)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("parallel-session-%d", time.Now().Unix())

	fmt.Printf("✅ Parallel agents ready! Session: %s\n", c.sessionID)
	fmt.Printf("⚡ Agents running in parallel: %s | %s | %s | %s\n\n",
		marketAgent.Info().Name,
		technicalAgent.Info().Name,
		riskAgent.Info().Name,
		opportunityAgent.Info().Name)

	return nil
}

// run starts the interactive chat session.
func (c *parallelChat) run() error {
	ctx := context.Background()

	// Setup the runner with parallel agent.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Display welcome message.
	c.displayWelcomeMessage()

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Start interactive chat.
	return c.startChat(ctx)
}

// startChat runs the interactive conversation loop.
func (c *parallelChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("💬 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle special commands.
		switch strings.ToLower(userInput) {
		case "help":
			c.displayWelcomeMessage()
			continue
		case "quit", "exit":
			fmt.Println("👋 Thank you for using the Parallel Multi-Agent System!")
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

// processMessage handles a single message exchange through the parallel agents.
func (c *parallelChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	fmt.Printf("🚀 Starting parallel analysis of: \"%s\"\n", userMessage)
	fmt.Println("📊 Agents analyzing different perspectives...")
	fmt.Println(strings.Repeat("─", 80))

	// Track timing for performance insights.
	startTime := time.Now()

	requestID := uuid.NewString()
	// Run the parallel agent system through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run parallel agents: %w", err)
	}

	// Process events as they arrive from parallel agents.
	if err := c.handleParallelEvents(eventChan); err != nil {
		return fmt.Errorf("error processing parallel events: %w", err)
	}

	// Display completion information.
	elapsed := time.Since(startTime)
	fmt.Println(strings.Repeat("─", 80))
	fmt.Printf("✅ Multi-perspective analysis completed in %v\n", elapsed.Truncate(time.Millisecond))

	return nil
}

// handleParallelEvents processes events from parallel agents with proper visualization.
func (c *parallelChat) handleParallelEvents(eventChan <-chan *event.Event) error {
	agentIcons := map[string]string{
		"market-analysis":      "📊",
		"technical-assessment": "⚙️",
		"risk-evaluation":      "⚠️",
		"opportunity-analysis": "🚀",
		"parallel-coordinator": "🤖",
	}

	currentAgents := make(map[string]bool) // Track which agents are active

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error from %s: %s\n", event.Author, event.Error.Message)
			continue
		}

		// Get agent identifier for display.
		agentIcon := agentIcons[event.Author]
		if agentIcon == "" {
			agentIcon = "🔷" // Default icon for unknown agents.
		}

		// Track agent activity (first time seeing this agent in this session).
		if !currentAgents[event.Author] && event.Author != "parallel-coordinator" {
			currentAgents[event.Author] = true
			fmt.Printf("%s [%s] Started analysis...\n", agentIcon, event.Author)
		}

		// Handle different event types.
		switch {
		case event.IsToolResultResponse():
			fmt.Printf("%s [%s] 🔧 Using tool...\n", agentIcon, event.Author)

		case len(event.Response.Choices) > 0:
			choice := event.Response.Choices[0]
			// With streaming=false, display only complete response content
			if choice.Message.Content != "" {
				fmt.Printf("%s [%s]: %s\n\n", agentIcon, event.Author, choice.Message.Content)
			}
		}

		// Check if this is the final runner completion event.
		if event.Done && event.Response != nil && event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("\n🎯 All parallel analyses completed successfully!\n")
			break
		}
	}

	return nil
}

// Helper functions.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

func main() {
	// Parse command-line flags.
	modelName := flag.String("model", "deepseek-chat", "Model to use for the agents")
	flag.Parse()

	fmt.Printf("⚡ Parallel Multi-Agent Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println("Agents: Market 📊 | Technical ⚙️ | Risk ⚠️ | Opportunity 🚀")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the parallel chat.
	chat := NewParallelChat(*modelName)

	if err := chat.run(); err != nil {
		fmt.Printf("Parallel chat failed: %v\n", err)
		os.Exit(1)
	}
}
