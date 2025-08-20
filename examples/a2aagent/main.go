//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	a2alog "trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2a "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "DeepSeek-V3-Online-64K", "Model to use")
)

var a2aURL = []string{
	// other a2a agent url you like
	"http://j.woa.com/apiserver/assistant/a2a",
	"http://0.0.0.0:8888",
	"http://0.0.0.0:8889",
}

func main() {
	flag.Parse()

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	logger, _ := config.Build()
	a2alog.Default = logger.Sugar()
	agentlog.Default = logger.Sugar()

	go func() {
		runRemoteAgent("agent1", "i am a remote agent, i can tell a joke", "0.0.0.0:8888")
	}()

	go func() {
		runRemoteAgent("agent2", "i am a remote agent, i can translate", "0.0.0.0:8889")
	}()

	time.Sleep(1 * time.Second)
	startChat()
}

func startChat() {
	var agentList []agent.Agent
	for _, host := range a2aURL {
		a2aAgent, err := a2aagent.New(a2aagent.WithAgentCardURL(host))
		if err != nil {
			fmt.Printf("Failed to create a2a agent: %v", err)
			return
		}
		agentList = append(agentList, a2aAgent)
		card := a2aAgent.GetAgentCard()
		fmt.Printf("\n------- Agent Card -------\n")
		fmt.Printf("Name: %s\n", card.Name)
		fmt.Printf("Description: %s\n", card.Description)
		fmt.Printf("URL: %s\n", host)
		fmt.Printf("------------------------\n")
	}

	modelInstance := openai.New(*modelName,
		// enable tajiji func toolcall
		openai.WithExtraFields(map[string]interface{}{
			"openai_infer": true,
			"tool_choice":  "auto",
		}))
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}
	desc := "You are a helpful AI assistant that coordinates with other agents to complete user requests. Your job is to understand the user's request and delegate it to the appropriate sub-agent."
	llmAgent := llmagent.New(
		"entranceAgent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithSubAgents(agentList),
	)

	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner("test", llmAgent, runner.WithSessionService(sessionService))

	userID := "user1"
	sessionID := "session1"

	fmt.Println("Chat with the agent. Type 'new' for a new session, or 'exit' to quit.")

	for {
		if err := processMessage(r, userID, &sessionID); err != nil {
			if err.Error() == "exit" {
				fmt.Println("👋 Goodbye!")
				return
			}
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}
}

func processMessage(r runner.Runner, userID string, sessionID *string) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("You: ")
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

	events, err := r.Run(context.Background(), userID, *sessionID, model.NewUserMessage(userInput))
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}

	if err := processResponse(events); err != nil {
		return fmt.Errorf("failed to process response: %w", err)
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

func runRemoteAgent(agentName, desc, host string) {
	remoteAgent := buildRemoteAgent(agentName, desc)
	server, err := a2a.New(
		a2a.WithHost(host),
		a2a.WithAgent(remoteAgent),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a server: %v", err)
	}
	server.Start(host)
}

func buildRemoteAgent(agentName, desc string) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(*modelName)

	// Create LLM agent with tools.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
	}
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
	)

	return llmAgent
}

// processResponse handles both streaming and non-streaming responses with tool call visualization.
func processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if err := handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}

		// Check if this is the final event.
		if event.Done && !isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func handleEvent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle tool calls.
	if handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle tool responses.
	if handleToolResponses(event) {
		return nil
	}

	// Handle content.
	handleContent(event, toolCallsDetected, assistantStarted, fullContent)

	return nil
}

// handleToolCalls detects and displays tool calls.
func handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 CallableTool calls initiated:\n")
		for _, toolCall := range event.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

// handleToolResponses detects and displays tool responses.
func handleToolResponses(event *event.Event) bool {
	if event.Response != nil && len(event.Response.Choices) > 0 {
		hasToolResponse := false
		for _, choice := range event.Response.Choices {
			// Handle traditional tool responses (Role: tool)
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
					choice.Message.ToolID,
					strings.TrimSpace(choice.Message.Content))
				hasToolResponse = true
			}
		}
		if hasToolResponse {
			return true
		}
	}
	return false
}

// handleContent processes and displays content.
func handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(event.Choices) > 0 {
		choice := event.Choices[0]
		content := extractContent(choice)

		if content != "" {
			displayContent(content, toolCallsDetected, assistantStarted, fullContent)
		}
	}
}

// extractContent extracts content based on streaming mode.
func extractContent(choice model.Choice) string {
	// For streaming mode, use delta content
	if choice.Delta.Content != "" {
		return choice.Delta.Content
	}
	// For non-streaming responses (like A2A agent responses), use message content
	return choice.Message.Content
}

// displayContent prints content to console.
func displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}

// isToolEvent checks if an event is a tool response (not a final response).
func isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	// Check if this is a tool response by examining choices.
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
