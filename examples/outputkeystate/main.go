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
// retrieves the data using a state access tool.
package main

import (
	"bufio"
	"context"
	"encoding/json"
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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	maxTokens   = 500
	temperature = 0.7
)

// StateAccessTool provides access to session state data.
type StateAccessTool struct {
	sessionService session.Service
	appName        string
	userID         string
	sessionID      string
}

// Declaration returns tool metadata.
func (t *StateAccessTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "get_session_state",
		Description: "Retrieve data from the current session state. Use this to access information stored by previous agents in the chain.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"key": {
					Type:        "string",
					Description: "The key of the data to retrieve from session state.",
				},
			},
			Required: []string{"key"},
		},
	}
}

// Call executes the tool to retrieve data from session state.
func (t *StateAccessTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var params map[string]any
	if err := json.Unmarshal(jsonArgs, &params); err != nil {
		return nil, fmt.Errorf("failed to unmarshal arguments: %w", err)
	}

	key, ok := params["key"].(string)
	if !ok {
		return nil, fmt.Errorf("key parameter must be a string")
	}

	// Create session key.
	sessionKey := session.Key{
		AppName:   t.appName,
		UserID:    t.userID,
		SessionID: t.sessionID,
	}

	// Get session state.
	sessionData, err := t.sessionService.GetSession(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Extract data from session state.
	if sessionData.State == nil {
		return map[string]any{
			"result": "No data found in session state",
		}, nil
	}

	// Look for the specific key in the state.
	if data, exists := sessionData.State[key]; exists {
		return map[string]any{
			"result": fmt.Sprintf("Found data for key '%s': %s", key, string(data)),
		}, nil
	}

	// If key not found, return available keys.
	availableKeys := make([]string, 0, len(sessionData.State))
	for k := range sessionData.State {
		availableKeys = append(availableKeys, k)
	}

	return map[string]any{
		"result": fmt.Sprintf("Key '%s' not found. Available keys: %v", key, availableKeys),
	}, nil
}

// outputKeyStateChainChat manages the output key state chain conversation.
type outputKeyStateChainChat struct {
	modelName      string
	runner         runner.Runner
	sessionService session.Service
	userID         string
	sessionID      string
}

// run starts the interactive chat session.
func (c *outputKeyStateChainChat) run() error {
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
func (c *outputKeyStateChainChat) setup(_ context.Context) error {
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
		llmagent.WithInstruction("You are a skilled research assistant specializing in comprehensive topic analysis. "+
			"When users ask questions, conduct thorough research and extract the most important facts, statistics, "+
			"and insights. Focus on accuracy, relevance, and providing actionable information. "+
			"Structure your findings in a clear, organized manner that would be valuable for content creation. "+
			"Be thorough but concise, and always cite sources when possible."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithOutputKey("research_findings"),
	)
	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("output-key-state-session-%d", time.Now().Unix())

	// Create state access tool for the writer agent.
	stateTool := &StateAccessTool{
		sessionService: sessionService,
		appName:        "output-key-state-chain-demo",
		userID:         c.userID,
		sessionID:      c.sessionID,
	}

	// Create Content Writer Agent that creates summaries based on research from state.
	writerAgent := llmagent.New(
		"writer-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A content writer that creates engaging summaries based on research findings from session state"),
		llmagent.WithInstruction("You are an experienced content writer and editor. Your task is to transform research findings "+
			"into compelling, well-structured content. First, use the get_session_state tool to retrieve the research data "+
			"using the key 'research_findings'. Then, create an engaging summary that is informative, accessible, and "+
			"tailored for a general audience. Use clear headings, bullet points where appropriate, and maintain a "+
			"conversational yet professional tone. Focus on the most important insights and present them in a logical flow. "+
			"Always start by retrieving the research data before beginning your writing process."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{stateTool}),
	)

	// Create Chain Agent with sub-agents.
	chainAgent := chainagent.New(
		"output-key-state-chain",
		chainagent.WithSubAgents([]agent.Agent{researchAgent, writerAgent}),
	)

	// Create runner with the chain agent and session service.
	appName := "output-key-state-chain-demo"
	c.runner = runner.NewRunner(
		appName,
		chainAgent,
		runner.WithSessionService(sessionService),
	)

	fmt.Printf("✅ Output Key State Chain ready! Session: %s\n", c.sessionID)
	fmt.Printf("📝 Agents: %s → %s\n",
		researchAgent.Info().Name,
		writerAgent.Info().Name)
	fmt.Printf("🔗 Data Flow: Research Agent (output_key) → Session State → Writer Agent (tool access)\n\n")

	return nil
}

// startChat runs the interactive conversation loop.
func (c *outputKeyStateChainChat) startChat(ctx context.Context) error {
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
func (c *outputKeyStateChainChat) processMessage(ctx context.Context, userMessage string) error {
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
func (c *outputKeyStateChainChat) processStreamingResponse(eventChan <-chan *event.Event) error {
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
func (c *outputKeyStateChainChat) handleChainEvent(
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
func (c *outputKeyStateChainChat) handleAgentTransition(
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
func (c *outputKeyStateChainChat) displayAgentTransition(currentAgent string) {
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
func (c *outputKeyStateChainChat) handleStreamingContent(event *event.Event, currentAgent *string) {
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

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🔑 Research & Content Creation Pipeline Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Println("Chain: Research Agent → Content Writer Agent")
	fmt.Println("Features: Comprehensive research → Engaging content creation")
	fmt.Println("Method: State-based data access with tool integration")
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
	fmt.Println("   1. Research Agent conducts comprehensive analysis and stores findings")
	fmt.Println("   2. Writer Agent retrieves research data using state access tool")
	fmt.Println("   3. Writer Agent creates engaging, well-structured content")
	fmt.Println()

	// Create and run the chat.
	chat := &outputKeyStateChainChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Output key state chain chat failed: %v", err)
	}
}
