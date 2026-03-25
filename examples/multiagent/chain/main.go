//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates multi-agent sequential processing using ChainAgent
// with streaming output, session management, and tool calling.
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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	maxTokens   = 500 // Reduced for faster, more concise responses
	temperature = 0.7
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	disablePrefix := flag.Bool("no-prefix", false, "Disable 'For context:' prefix when passing data between agents")
	flag.Parse()

	fmt.Printf("🔗 Multi-Agent Chain Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: web_search, knowledge_base\n")
	fmt.Println("Chain: Planning → Research → Writing")
	if *disablePrefix {
		fmt.Println("Note: Context prefix is DISABLED for clean data passing")
	} else {
		fmt.Println("Note: Context prefix is ENABLED (use -no-prefix to disable)")
	}
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &chainChat{
		modelName:     *modelName,
		disablePrefix: *disablePrefix,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chain chat failed: %v", err)
	}
}

// chainChat manages the multi-agent conversation.
type chainChat struct {
	modelName     string
	disablePrefix bool
	runner        runner.Runner
	userID        string
	sessionID     string
}

// run starts the interactive chat session.
func (c *chainChat) run() error {
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
func (c *chainChat) setup(_ context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName)

	// Create shared tools for research agent.
	webSearchTool := function.NewFunctionTool(
		c.webSearch,
		function.WithName("web_search"),
		function.WithDescription("Search the web for current information on any topic"),
	)
	knowledgeTool := function.NewFunctionTool(
		c.queryKnowledge,
		function.WithName("knowledge_base"),
		function.WithDescription("Query internal knowledge base for factual information"),
	)

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      true,
	}

	// Create Planning Agent.
	planningAgent := llmagent.New(
		"planning-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Analyzes user requests and creates structured plans"),
		llmagent.WithInstruction("You are a planning specialist. Analyze the user's request and create a brief, structured plan (2-3 steps max). Be concise and specific about what needs to be done. Keep your response under 100 words."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithAddContextPrefix(!c.disablePrefix), // Use flag to control prefix
		llmagent.WithAgentCallbacks(agent.NewCallbacks().RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
				fmt.Printf("\n--- The %s is executing task planning\n", args.Invocation.AgentName)
				if args.Invocation.AgentName != "planning-agent" {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", args.Invocation.AgentName, "planning-agent")
				}
				ctxInvocation, ok := agent.InvocationFromContext(ctx)
				if !ok {
					return nil, fmt.Errorf("failed to get invocation from context")
				}
				if ctxInvocation.AgentName != args.Invocation.AgentName {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", ctxInvocation.AgentName, args.Invocation.AgentName)
				}
				return nil, nil
			},
		)),
	)

	// Create Research Agent with tools.
	researchAgent := llmagent.New(
		"research-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Gathers information using available tools and resources"),
		llmagent.WithInstruction("You are a research specialist. Use the available tools to gather key information. Be concise and fact-based. Keep your response under 150 words."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{webSearchTool, knowledgeTool}),
		llmagent.WithAddContextPrefix(!c.disablePrefix), // Use flag to control prefix
		llmagent.WithAgentCallbacks(agent.NewCallbacks().RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
				fmt.Printf("\n----- The %s is searching for information\n", args.Invocation.AgentName)
				if args.Invocation.AgentName != "research-agent" {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", args.Invocation.AgentName, "research-agent")
				}
				ctxInvocation, ok := agent.InvocationFromContext(ctx)
				if !ok {
					return nil, fmt.Errorf("failed to get invocation from context")
				}
				if ctxInvocation.AgentName != args.Invocation.AgentName {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", ctxInvocation.AgentName, args.Invocation.AgentName)
				}
				return nil, nil
			},
		)),
	)

	// Create Writing Agent.
	writingAgent := llmagent.New(
		"writing-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Composes final responses based on planning and research"),
		llmagent.WithInstruction("You are a writing specialist. Create a brief, well-structured response based on the plan and research from previous agents. Be clear and concise. Keep your response under 200 words."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithAddContextPrefix(!c.disablePrefix), // Use flag to control prefix
		llmagent.WithAgentCallbacks(agent.NewCallbacks().RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
				fmt.Printf("\n----- The %s is composing the final response\n", args.Invocation.AgentName)
				if args.Invocation.AgentName != "writing-agent" {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", args.Invocation.AgentName, "writing-agent")
				}
				ctxInvocation, ok := agent.InvocationFromContext(ctx)
				if !ok {
					return nil, fmt.Errorf("failed to get invocation from context")
				}
				if ctxInvocation.AgentName != args.Invocation.AgentName {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", ctxInvocation.AgentName, args.Invocation.AgentName)
				}
				return nil, nil
			},
		)),
	)

	// Create Chain Agent with sub-agents.
	chainAgent := chainagent.New(
		"multi-agent-chain",
		chainagent.WithSubAgents([]agent.Agent{planningAgent, researchAgent, writingAgent}),
		chainagent.WithAgentCallbacks(agent.NewCallbacks().RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
				fmt.Printf("\n--- The %s is Runing\n", args.Invocation.AgentName)
				if args.Invocation.AgentName != "multi-agent-chain" {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", args.Invocation.AgentName,
						"multi-agent-chain")
				}
				ctxInvocation, ok := agent.InvocationFromContext(ctx)
				if !ok {
					return nil, fmt.Errorf("failed to get invocation from context")
				}
				if ctxInvocation.AgentName != args.Invocation.AgentName {
					return nil, fmt.Errorf("agent name mismatch: %s != %s", ctxInvocation.AgentName, args.Invocation.AgentName)
				}
				return nil, nil
			},
		)),
	)

	// Create runner with the chain agent.
	appName := "chain-agent-demo"
	c.runner = runner.NewRunner(appName, chainAgent)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chain-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chain ready! Session: %s\n", c.sessionID)
	fmt.Printf("📝 Agents: %s → %s → %s\n\n",
		planningAgent.Info().Name,
		researchAgent.Info().Name,
		writingAgent.Info().Name)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *chainChat) startChat(ctx context.Context) error {
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
func (c *chainChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	requestID := uuid.NewString()
	// Run the chain agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.WithRequestID(requestID))
	if err != nil {
		return fmt.Errorf("failed to run chain agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response from the agent chain.
func (c *chainChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		currentAgent    string
		agentStarted    bool
		toolCallsActive bool
	)

	for event := range eventChan {
		if err := c.handleChainEvent(event, &currentAgent, &agentStarted, &toolCallsActive); err != nil {
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
func (c *chainChat) handleChainEvent(
	event *event.Event,
	currentAgent *string,
	agentStarted *bool,
	toolCallsActive *bool,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle agent transitions.
	c.handleAgentTransition(event, currentAgent, agentStarted, toolCallsActive)

	// Handle tool calls.
	c.handleToolCalls(event, toolCallsActive)

	// Handle tool responses.
	c.handleToolResponses(event)

	// Handle streaming content.
	c.handleStreamingContent(event, currentAgent, toolCallsActive)

	return nil
}

// handleAgentTransition manages agent switching and display.
func (c *chainChat) handleAgentTransition(
	event *event.Event,
	currentAgent *string,
	agentStarted *bool,
	toolCallsActive *bool,
) {
	if event.Author != *currentAgent {
		if *agentStarted {
			fmt.Printf("\n")
		}
		*currentAgent = event.Author
		*agentStarted = true
		*toolCallsActive = false

		// Display agent transition.
		c.displayAgentTransition(*currentAgent)
	}
}

// displayAgentTransition shows the current agent with appropriate emoji.
func (c *chainChat) displayAgentTransition(currentAgent string) {
	switch currentAgent {
	case "planning-agent":
		fmt.Printf("📋 Planning Agent: ")
	case "research-agent":
		fmt.Printf("🔍 Research Agent: ")
	case "writing-agent":
		fmt.Printf("✍️  Writing Agent: ")
	default:
		// No display for unknown agents.
	}
}

// handleToolCalls detects and displays tool calls.
func (c *chainChat) handleToolCalls(event *event.Event, toolCallsActive *bool) {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsActive = true
		fmt.Printf("\n🔧 Using tools:\n")
		for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("🔄 Executing...\n")
	}
}

// handleToolResponses processes tool responses.
func (c *chainChat) handleToolResponses(event *event.Event) {
	if event.Response != nil && len(event.Response.Choices) > 0 {
		for _, choice := range event.Response.Choices {
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				c.displayToolResponse(choice)
			}
		}
	}
}

// displayToolResponse shows tool response information.
func (c *chainChat) displayToolResponse(choice model.Choice) {
	fmt.Printf("✅ Tool result (ID: %s): %s\n",
		choice.Message.ToolID,
		strings.TrimSpace(choice.Message.Content))
}

// handleStreamingContent processes streaming content from agents.
func (c *chainChat) handleStreamingContent(event *event.Event, currentAgent *string, toolCallsActive *bool) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		if choice.Delta.Content != "" {
			if *toolCallsActive {
				*toolCallsActive = false
				fmt.Printf("\n%s (continued): ", c.getAgentEmoji(*currentAgent))
			}
			fmt.Print(choice.Delta.Content)
		}
	}
}

// getAgentEmoji returns the appropriate emoji for the agent.
func (c *chainChat) getAgentEmoji(agentName string) string {
	switch agentName {
	case "planning-agent":
		return "📋 Planning Agent"
	case "research-agent":
		return "🔍 Research Agent"
	case "writing-agent":
		return "✍️  Writing Agent"
	default:
		return "🤖 " + agentName
	}
}

// Tool implementations.

// webSearch simulates a web search tool.
func (c *chainChat) webSearch(_ context.Context, args webSearchArgs) (webSearchResult, error) {
	// Simulate web search with relevant information.
	results := []string{
		fmt.Sprintf("Recent information about '%s' from reliable sources", args.Query),
		"Current trends and developments in the field",
		"Expert opinions and analysis from industry leaders",
	}

	return webSearchResult{
		Query:   args.Query,
		Results: results,
		Count:   len(results),
	}, nil
}

// queryKnowledge simulates a knowledge base query.
func (c *chainChat) queryKnowledge(ctx context.Context, args knowledgeArgs) (knowledgeResult, error) {
	// Simulate knowledge base query.
	facts := []string{
		fmt.Sprintf("Factual information about '%s'", args.Topic),
		"Historical context and background",
		"Technical specifications and details",
	}

	return knowledgeResult{
		Topic: args.Topic,
		Facts: facts,
		Count: len(facts),
	}, nil
}

// Tool argument and result types.

type webSearchArgs struct {
	Query string `json:"query" description:"Search query for web search"`
}

type webSearchResult struct {
	Query   string   `json:"query"`
	Results []string `json:"results"`
	Count   int      `json:"count"`
}

type knowledgeArgs struct {
	Topic string `json:"topic" description:"Topic to query in knowledge base"`
}

type knowledgeResult struct {
	Topic string   `json:"topic"`
	Facts []string `json:"facts"`
	Count int      `json:"count"`
}

// Helper functions.

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
