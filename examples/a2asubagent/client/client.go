//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides an A2A (Agent-to-Agent) client example.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use")
)

var agentURLS = []string{
	"http://localhost:8087/",
	"http://localhost:8088/",
}

func main() {
	flag.Parse()

	// Create remote agents as sub-agents
	remoteAgents := make([]agent.Agent, 0)
	for _, url := range agentURLS {
		agent, err := a2aagent.New(a2aagent.WithAgentCardURL(url))
		if err != nil {
			log.Printf("Failed to create agent from %s: %v", url, err)
			continue
		}
		remoteAgents = append(remoteAgents, agent)
		fmt.Printf("Connected to sub-agent: %s\n", agent.Info().Name)
	}

	if len(remoteAgents) == 0 {
		log.Fatal("No remote agents available")
	}

	// Create coordinator agent with sub-agents
	coordinatorAgent := buildCoordinatorAgent(remoteAgents)

	// Start chat
	startChat(coordinatorAgent)
}

func buildCoordinatorAgent(subAgents []agent.Agent) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(*modelName)
	desc := "You are a coordinator assistant that manages multiple sub-agents. You can handle user requests directly or delegate them to appropriate sub-agents."

	// Create LLM agent with sub-agents.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}
	return llmagent.New(
		"agent_coordinator",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSubAgents(subAgents),
	)
}

func startChat(coordinatorAgent agent.Agent) {
	// Display coordinator agent info
	fmt.Printf("\n------- Coordinator Agent -------\n")
	card := coordinatorAgent.Info()
	fmt.Printf("%s: %s\n", card.Name, card.Description)
	fmt.Printf("--------------------------------\n")

	// Create runner for coordinator agent
	coordinatorRunner := runner.NewRunner("test", coordinatorAgent)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer coordinatorRunner.Close()

	userID := "user1"
	sessionID := "session1"

	fmt.Println("\nChat with the coordinator agent. Type 'new' for a new session, or 'exit' to quit.")

	for {
		if err := processMessage(coordinatorRunner, userID, &sessionID); err != nil {
			if err.Error() == "exit" {
				fmt.Println("👋 Goodbye!")
				return
			}
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}
}

func processMessage(coordinatorRunner runner.Runner, userID string, sessionID *string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("User: ")
	if !scanner.Scan() {
		return fmt.Errorf("exit")
	}

	userInput := strings.TrimSpace(scanner.Text())
	if userInput == "" {
		return nil
	}

	switch strings.ToLower(userInput) {
	case "exit":
		return fmt.Errorf("exit")
	case "new":
		*sessionID = startNewSession()
		return nil
	}

	// Process with coordinator agent only
	events, err := coordinatorRunner.Run(context.Background(), userID, *sessionID, model.NewUserMessage(userInput))
	if err != nil {
		return fmt.Errorf("failed to run coordinator agent: %w", err)
	}
	if err := processResponse(events); err != nil {
		return fmt.Errorf("failed to process coordinator response: %w", err)
	}
	return nil
}

func startNewSession() string {
	newSessionID := fmt.Sprintf("session_%d", time.Now().UnixNano())
	fmt.Printf("🆕 Started new session: %s\n", newSessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
	return newSessionID
}

// processResponse handles streaming responses from coordinator agent.
func processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🎯 Coordinator: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
		currentAgent      string = "agent_coordinator"
	)

	for event := range eventChan {
		if err := handleTransferEvent(event, &fullContent, &toolCallsDetected, &assistantStarted, &currentAgent); err != nil {
			return err
		}
	}

	fmt.Println() // Final newline
	return nil
}

// handleTransferEvent processes a single event from the transfer system.
func handleTransferEvent(
	event *event.Event,
	fullContent *string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	currentAgent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle agent transfers.
	if handleTransfer(event, currentAgent, assistantStarted) {
		return nil
	}

	// Handle tool calls.
	if handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle content.
	handleContent(event, fullContent, toolCallsDetected, assistantStarted, currentAgent)

	// Handle tool responses.
	handleToolResponses(event)

	return nil
}

// handleToolCalls detects and displays tool calls.
func handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}

		if isTransferTool(event.Response.Choices[0].Message.ToolCalls[0]) {
			fmt.Printf("🔄 Initiating transfer...\n")
		} else {
			displayToolCalls(event)
		}
		return true
	}
	return false
}

// handleToolResponses processes tool response completion.
func handleToolResponses(event *event.Event) {
	if event.IsToolResultResponse() && len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 &&
		!isTransferTool(event.Response.Choices[0].Message.ToolCalls[0]) {
		fmt.Printf("   ✅ Tool completed\n")
	}
}

// handleContent processes streaming content.
func handleContent(
	event *event.Event,
	fullContent *string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	currentAgent *string,
) {
	if len(event.Response.Choices) > 0 {
		content := extractContent(event.Response.Choices[0])

		if content != "" {
			displayContent(event, content, fullContent, toolCallsDetected, assistantStarted, currentAgent)
		}
	}
}

// extractContent extracts content based on streaming mode.
func extractContent(choice model.Choice) string {
	return choice.Delta.Content
}

// displayContent handles content display logic.
func displayContent(
	event *event.Event,
	content string,
	fullContent *string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	currentAgent *string,
) {
	if !*assistantStarted && !*toolCallsDetected {
		displayAgentHeader(event, currentAgent)
		*assistantStarted = true
	} else if *toolCallsDetected && !*assistantStarted {
		if !isTransferResponse(event) {
			fmt.Printf("%s %s: ", getAgentIcon(event.Author), getAgentDisplayName(event.Author))
			*assistantStarted = true
		}
	}

	if !isTransferResponse(event) {
		fmt.Print(content)
		*fullContent += content
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

// handleTransfer processes agent transfer events.
func handleTransfer(event *event.Event, currentAgent *string, assistantStarted *bool) bool {
	if event.Object == model.ObjectTypeTransfer {
		fmt.Printf("\n🔄 Transfer Event: %s\n", event.Response.Choices[0].Message.Content)
		*currentAgent = getAgentFromTransfer(event)
		*assistantStarted = false
		return true
	}
	return false
}

// displayToolCalls shows tool call information.
func displayToolCalls(event *event.Event) {
	fmt.Printf("🔧 %s executing tools:\n", getAgentIcon(event.Author))
	for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s", toolCall.Function.Name)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf(" (%s)", string(toolCall.Function.Arguments))
		}
		fmt.Printf("\n")
	}
}

// displayAgentHeader shows the agent header when starting content.
func displayAgentHeader(event *event.Event, currentAgent *string) {
	if event.Author != *currentAgent {
		fmt.Printf("\n%s %s: ", getAgentIcon(event.Author), getAgentDisplayName(event.Author))
		*currentAgent = event.Author
	}
}

// Helper functions for display formatting.
func getAgentIcon(agentName string) string {
	switch agentName {
	case "agent_coordinator":
		return "🎯"
	case "calculator":
		return "🧮"
	case "CodeCheckAgent":
		return "🔍"
	default:
		return "🤖"
	}
}

func getAgentDisplayName(agentName string) string {
	switch agentName {
	case "agent_coordinator":
		return "Coordinator"
	case "calculator":
		return "Calculator"
	case "CodeCheckAgent":
		return "Code Checker"
	default:
		return agentName
	}
}

func getAgentFromTransfer(event *event.Event) string {
	// Parse the transfer event to determine target agent.
	if event.Response != nil && len(event.Response.Choices) > 0 {
		content := event.Response.Choices[0].Message.Content
		if strings.Contains(content, "calculator") {
			return "calculator"
		} else if strings.Contains(content, "CodeCheckAgent") {
			return "CodeCheckAgent"
		}
	}
	return "agent_coordinator"
}

func isTransferTool(toolCall model.ToolCall) bool {
	return toolCall.Function.Name == "transfer_to_agent"
}

func isTransferResponse(event *event.Event) bool {
	return len(event.Response.Choices) > 0 && event.Response.Choices[0].Message.ToolID != ""
}
