//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a customer support workflow using GraphAgent with A2A sub-agents.
// This example shows how to build and execute graphs that coordinate with remote agents
// through the A2A protocol for specialized tasks.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"time"

	"go.uber.org/zap"
	a2alog "trpc.group/trpc-go/trpc-a2a-go/log"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2a "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// Default model name for deepseek-chat.
	defaultModelName = "deepseek-chat"
	// Default A2A server host.
	defaultA2AHost = "0.0.0.0:8888"
)

// State keys for customer support workflow.
const (
	stateKeyCustomerQuery    = "customer_query"
	stateKeyQueryType        = "query_type"
	stateKeyPriority         = "priority"
	stateKeyTechnicalDetails = "technical_details"
	stateKeyResponse         = "response"
	stateKeyFinalAnswer      = "final_answer"
)

// Query types.
const (
	queryTypeTechnical = "technical"
	queryTypeBilling   = "billing"
	queryTypeGeneral   = "general"
)

// Priority levels.
const (
	priorityHigh   = "high"
	priorityMedium = "medium"
	priorityLow    = "low"
)

// Node names for the workflow graph.
const (
	nodeAnalyzeQuery     = "analyze_query"
	nodeTechnicalSupport = "technical_support"
	nodeBillingSupport   = "billing_support"
	nodeGeneralSupport   = "general_support"
	nodeFormatResponse   = "format_response"
)

// Agent names.
const (
	agentTechnicalSupport = "technical-support-agent"
	a2aStateKeyMetadata   = "meta"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	a2aHost = flag.String("a2a-host", defaultA2AHost,
		"A2A server host to connect to")
	interactive = flag.Bool("interactive", false,
		"Run in interactive mode")
)

func main() {
	// Parse command line flags.
	flag.Parse()
	fmt.Printf("🚀 Customer Support Workflow with A2A Sub-Agent Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("A2A Host: %s\n", *a2aHost)
	fmt.Println(strings.Repeat("=", 60))

	// Create and run the workflow.
	workflow := &customerSupportWorkflow{
		modelName: *modelName,
		a2aHost:   *a2aHost,
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// customerSupportWorkflow manages the customer support workflow with A2A sub-agents.
type customerSupportWorkflow struct {
	modelName string
	a2aHost   string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the customer support workflow.
func (w *customerSupportWorkflow) run() error {
	ctx := context.Background()

	// Setup logging.
	w.setupLogging()

	// Start A2A server in background.
	go w.startA2AServer()

	// Wait for server to start.
	time.Sleep(2 * time.Second)

	// Setup the workflow.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer w.runner.Close()

	if *interactive {
		return w.startInteractiveMode(ctx)
	}
	return w.runDefaultExamples(ctx)
}

// setupLogging configures logging for the workflow.
func (w *customerSupportWorkflow) setupLogging() {
	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	logger, _ := config.Build()
	a2alog.Default = logger.Sugar()
	agentlog.Default = logger.Sugar()
}

type hookProcessor struct {
	next taskmanager.MessageProcessor
}

func (h *hookProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	fmt.Printf("A2A Server: received message:%+v\n", message.MessageID)
	fmt.Printf("A2A Server: received state: %+v\n", message.Metadata)
	return h.next.ProcessMessage(ctx, message, options, handler)
}

// startA2AServer starts the A2A server with a specialized remote agent.
func (w *customerSupportWorkflow) startA2AServer() {
	remoteAgent := w.buildRemoteAgent()
	server, err := a2a.New(
		a2a.WithHost(w.a2aHost),
		// Enable A2A streaming to demonstrate live token streaming end-to-end.
		a2a.WithAgent(remoteAgent, true),
		a2a.WithProcessMessageHook(
			func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
				return &hookProcessor{next: next}
			}),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a server: %v", err)
	}
	fmt.Printf("🔗 Starting A2A server on %s...\n", w.a2aHost)

	// Start server in a separate goroutine to avoid blocking
	go func() {
		// Redirect A2A server logs to avoid mixing with our output
		server.Start(w.a2aHost)
	}()

	// Give the server a moment to start
	time.Sleep(500 * time.Millisecond)
}

// buildRemoteAgent creates a specialized remote agent for technical support.
func (w *customerSupportWorkflow) buildRemoteAgent() agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(w.modelName)
	// Create specialized tools for technical support.
	tools := []tool.Tool{
		function.NewFunctionTool(
			func(ctx context.Context, args map[string]any) (map[string]any, error) {
				return map[string]any{
					"status":           "operational",
					"uptime":           "99.9%",
					"last_maintenance": "2024-01-15",
				}, nil
			},
			function.WithName("check_system_status"),
			function.WithDescription("Check the current system status"),
		),
		function.NewFunctionTool(
			func(ctx context.Context, args map[string]any) (map[string]any, error) {
				return map[string]any{
					"recent_errors": []string{
						"2024-01-20 10:30:15 - Connection timeout (resolved)",
						"2024-01-19 15:45:22 - Database query slow (monitoring)",
					},
					"total_errors": 2,
				}, nil
			},
			function.WithName("get_error_logs"),
			function.WithDescription("Retrieve recent error logs"),
		),
	}
	// Create LLM agent with tools for technical support.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1500),
		Temperature: floatPtr(0.3),
		// Enable streaming so A2A can forward live deltas
		Stream: true,
	}
	desc := "A specialized technical support agent that can diagnose system issues, check logs, and provide detailed technical assistance."
	llmAgent := llmagent.New(
		"technical-support-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools(tools),
	)
	return llmAgent
}

// setup creates the graph agent and runner.
func (w *customerSupportWorkflow) setup() error {
	// Create the customer support workflow graph.
	workflowGraph, err := w.createCustomerSupportGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create A2A agent for remote technical support.
	a2aURL := fmt.Sprintf("http://%s", w.a2aHost)
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(a2aURL),
		// Important: use the same name as the agent node ID so the graph can
		// resolve and invoke this sub-agent.
		a2aagent.WithName(nodeTechnicalSupport),
		a2aagent.WithTransferStateKey(a2aStateKeyMetadata),
	)
	if err != nil {
		return fmt.Errorf("failed to create a2a agent: %w", err)
	}

	// Create GraphAgent from the compiled graph with A2A sub-agent.
	graphAgent, err := graphagent.New("customer-support-coordinator", workflowGraph,
		graphagent.WithDescription("Customer support workflow with A2A technical support sub-agent"),
		graphagent.WithSubAgents([]agent.Agent{a2aAgent}),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner.
	w.runner = runner.NewRunner(
		"customer-support-workflow",
		graphAgent,
		runner.WithSessionService(sessionService),
	)
	// Generate session ID.
	w.userID = "customer-123"
	w.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	return nil
}

// createCustomerSupportGraph creates the customer support workflow graph.
func (w *customerSupportWorkflow) createCustomerSupportGraph() (*graph.Graph, error) {
	// Define state schema for customer support workflow.
	schema := graph.NewStateSchema().
		AddField(stateKeyCustomerQuery, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(stateKeyQueryType, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(stateKeyPriority, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(stateKeyTechnicalDetails, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(stateKeyResponse, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(stateKeyFinalAnswer, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	// Create the workflow graph.
	workflowGraph, err := graph.NewStateGraph(schema).
		// Initial analysis node.
		AddNode(nodeAnalyzeQuery, w.analyzeCustomerQuery,
			graph.WithName("Analyze Customer Query"),
			graph.WithDescription("Analyzes the customer query to determine type and priority"),
		).
		// Technical support using built-in agent node that routes to sub-agent.
		AddAgentNode(nodeTechnicalSupport,
			graph.WithName(nodeTechnicalSupport),
			graph.WithDescription("Routes to A2A technical support agent for specialized assistance"),
		).
		// Billing support node.
		AddNode(nodeBillingSupport, w.handleBillingQuery,
			graph.WithName("Billing Support"),
			graph.WithDescription("Handles billing and payment related queries"),
		).
		// General support node.
		AddNode(nodeGeneralSupport, w.handleGeneralQuery,
			graph.WithName("General Support"),
			graph.WithDescription("Handles general customer service queries"),
		).
		// Final response formatting.
		AddNode(nodeFormatResponse, w.formatFinalResponse,
			graph.WithName("Format Response"),
			graph.WithDescription("Formats the final response for the customer"),
		).
		// Route based on query type.
		AddConditionalEdges(nodeAnalyzeQuery, w.routeByQueryType, map[string]string{
			queryTypeTechnical: nodeTechnicalSupport,
			queryTypeBilling:   nodeBillingSupport,
			queryTypeGeneral:   nodeGeneralSupport,
		}).
		// Set up the workflow edges.
		SetEntryPoint(nodeAnalyzeQuery).
		AddEdge(nodeTechnicalSupport, nodeFormatResponse).
		AddEdge(nodeBillingSupport, nodeFormatResponse).
		AddEdge(nodeGeneralSupport, nodeFormatResponse).
		SetFinishPoint(nodeFormatResponse).
		Compile()
	if err != nil {
		return nil, fmt.Errorf("failed to compile graph: %w", err)
	}
	return workflowGraph, nil
}

// runDefaultExamples runs predefined customer support examples.
func (w *customerSupportWorkflow) runDefaultExamples(ctx context.Context) error {
	examples := []string{
		"My application is showing error 500, can you help me troubleshoot?",
		"I need help with my billing statement from last month",
		"What are your business hours?",
		"The system is running very slowly today, what's wrong?",
	}
	fmt.Printf("📋 Running %d example queries...\n\n", len(examples))
	for i, query := range examples {
		fmt.Printf("--- Example %d ---\n", i+1)
		fmt.Printf("Customer: %s\n", query)
		// Response content will stream/live-print below; avoid preface duplication.
		if err := w.processQuery(ctx, query); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
		// Add a small delay between queries to prevent race conditions
		if i < len(examples)-1 {
			time.Sleep(1 * time.Second)
		}
	}
	return nil
}

// startInteractiveMode starts interactive mode for user input.
func (w *customerSupportWorkflow) startInteractiveMode(ctx context.Context) error {
	fmt.Printf("🎯 Interactive Mode - Type your customer support queries\n")
	fmt.Printf("Commands: 'help', 'exit', or just type your query\n\n")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("Customer: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch input {
		case "exit", "quit":
			fmt.Printf("👋 Goodbye!\n")
			return nil
		case "help":
			w.showHelp()
			continue
		}

		// Response content will stream/live-print below; avoid preface duplication.
		if err := w.processQuery(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}

	return scanner.Err()
}

// processQuery processes a single customer query.
func (w *customerSupportWorkflow) processQuery(ctx context.Context, query string) error {
	// Create user message.
	userMessage := model.Message{
		Role:    model.RoleUser,
		Content: query,
	}

	// Run the workflow.
	events, err := w.runner.Run(
		ctx, w.userID, w.sessionID, userMessage, agent.WithRuntimeState(map[string]any{a2aStateKeyMetadata: "test"}),
	)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process streaming response.
	return w.processStreamingResponse(events)
}

// processStreamingResponse handles the streaming workflow response.
func (w *customerSupportWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		stageCount      int
		responseStarted bool
		a2aStreamed     bool // streaming seen from A2A agent
	)

	for event := range eventChan {
		// Track node execution events.
		if event.StateDelta != nil {
			if nodeData, exists := event.StateDelta[graph.MetadataKeyNode]; exists {
				var nodeMetadata graph.NodeExecutionMetadata
				if err := json.Unmarshal(nodeData, &nodeMetadata); err == nil {
					switch nodeMetadata.Phase {
					case graph.ExecutionPhaseStart:
						fmt.Printf("\n🚀 Entering node: %s (%s)\n",
							nodeMetadata.NodeID, nodeMetadata.NodeType)

						if nodeMetadata.NodeType == graph.NodeTypeLLM {
							fmt.Printf("   🤖 Using model: %s\n", w.modelName)

							if nodeMetadata.ModelInput != "" {
								fmt.Printf("   📝 Model Input: %s\n",
									truncateString(nodeMetadata.ModelInput,
										100))
							}
						}

						if nodeMetadata.NodeType == graph.NodeTypeAgent {
							fmt.Printf("   🤖 Executing A2A agent: %s\n",
								nodeMetadata.NodeID)
						}
					case graph.ExecutionPhaseComplete:
						fmt.Printf("✅ Completed node: %s (%s)\n",
							nodeMetadata.NodeID, nodeMetadata.NodeType)
					case graph.ExecutionPhaseError:
						fmt.Printf("❌ Error in node: %s (%s)\n",
							nodeMetadata.NodeID, nodeMetadata.NodeType)
					}
				}
			}
		}

		// Handle errors after metadata so node-level error
		// metadata remains visible.
		if event.Error != nil {
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Process streaming content from LLM nodes and A2A agents.
		if len(event.Response.Choices) > 0 {
			choice := event.Response.Choices[0]
			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !responseStarted {
					if event.Author == nodeTechnicalSupport {
						fmt.Print("🤖 A2A Stream: ")
						a2aStreamed = true
					} else {
						fmt.Print("🤖 Response: ")
					}
					responseStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
			// Add newline when streaming is complete (when choice is done).
			if (choice.Delta.Content == "" || event.Done) && responseStarted {
				fmt.Println()           // Add newline after streaming completes
				responseStarted = false // Reset for next response
			}
		}

		// Check for A2A agent responses in state updates.
		// Only label as A2A when the event author is the A2A agent to avoid
		// mislabeling final formatted responses. If we've already streamed from A2A,
		// skip this to avoid duplication.
		if event.StateDelta != nil && event.Author == nodeTechnicalSupport && !a2aStreamed {
			if responseData, exists := event.StateDelta[graph.StateKeyLastResponse]; exists {
				// Try to decode as structured model.Response first
				var response *model.Response
				if err := json.Unmarshal(responseData, &response); err == nil && response != nil {
					if len(response.Choices) > 0 && response.Choices[0].Message.Content != "" {
						if !responseStarted {
							fmt.Print("🤖 A2A Agent Response: ")
							responseStarted = true
						}
						fmt.Print(response.Choices[0].Message.Content)
						fmt.Println()
						responseStarted = false
					}
				} else {
					var s string
					if err := json.Unmarshal(responseData, &s); err == nil && s != "" {
						if !responseStarted {
							fmt.Print("🤖 A2A Agent Response: ")
							responseStarted = true
						}
						fmt.Println(s)
						responseStarted = false
					}
				}
			}
		}

		// Track workflow stages (concise to avoid duplicate long content).
		if event.Author == graph.AuthorGraphExecutor {
			stageCount++
			fmt.Printf("\n🔄 Stage %d completed\n", stageCount)
		}

		// Handle completion and final response.
		if event.Done {
			// If A2A streaming happened, avoid repeating the same content; show a concise completion mark.
			if a2aStreamed {
				fmt.Println("✅ A2A Completed")
			} else {
				// Print final response once: prefer formatted final_answer, otherwise fallback to completion response.
				printed := false
				if event.StateDelta != nil {
					if finalAnswerData, exists := event.StateDelta[stateKeyFinalAnswer]; exists {
						var finalAnswer string
						if err := json.Unmarshal(finalAnswerData, &finalAnswer); err == nil && finalAnswer != "" {
							fmt.Print("🤖 Final Response: ")
							fmt.Println(finalAnswer)
							printed = true
						}
					}
				}
				if !printed && event.Response != nil && len(event.Response.Choices) > 0 {
					content := event.Response.Choices[0].Message.Content
					if content != "" {
						fmt.Print("🤖 Final Response: ")
						fmt.Println(content)
					}
				}
			}
			break
		}
	}

	return nil
}

// showHelp displays help information.
func (w *customerSupportWorkflow) showHelp() {
	fmt.Printf("\n📖 Available Commands:\n")
	fmt.Printf("  help     - Show this help message\n")
	fmt.Printf("  exit     - Exit the application\n")
	fmt.Printf("  quit     - Exit the application\n")
	fmt.Printf("\n💡 Example Queries:\n")
	fmt.Printf("  - Technical: \"My app is showing error 500\"\n")
	fmt.Printf("  - Billing: \"I need help with my invoice\"\n")
	fmt.Printf("  - General: \"What are your business hours?\"\n\n")
}

// Node functions for the workflow.

// analyzeCustomerQuery analyzes the customer query to determine type and priority.
func (w *customerSupportWorkflow) analyzeCustomerQuery(ctx context.Context, state graph.State) (any, error) {
	query := state[graph.StateKeyUserInput].(string)

	// Simple keyword-based analysis for demonstration.
	queryLower := strings.ToLower(query)

	var queryType, priority string

	// Determine query type.
	switch {
	case strings.Contains(queryLower, "error") || strings.Contains(queryLower, "bug") ||
		strings.Contains(queryLower, "crash") || strings.Contains(queryLower, "slow") ||
		strings.Contains(queryLower, "troubleshoot") || strings.Contains(queryLower, "technical"):
		queryType = queryTypeTechnical
	case strings.Contains(queryLower, "bill") || strings.Contains(queryLower, "payment") ||
		strings.Contains(queryLower, "invoice") || strings.Contains(queryLower, "charge") ||
		strings.Contains(queryLower, "refund"):
		queryType = queryTypeBilling
	default:
		queryType = queryTypeGeneral
	}

	// Determine priority.
	switch {
	case strings.Contains(queryLower, "urgent") || strings.Contains(queryLower, "emergency") ||
		strings.Contains(queryLower, "down") || strings.Contains(queryLower, "broken"):
		priority = priorityHigh
	case strings.Contains(queryLower, "help") || strings.Contains(queryLower, "issue"):
		priority = priorityMedium
	default:
		priority = priorityLow
	}

	return newStateBuilder().
		SetCustomerQuery(query).
		SetQueryType(queryType).
		SetPriority(priority).
		Build(), nil
}

// routeByQueryType routes the workflow based on query type.
func (w *customerSupportWorkflow) routeByQueryType(ctx context.Context, state graph.State) (string, error) {
	queryType := state[stateKeyQueryType].(string)
	return queryType, nil
}

// handleBillingQuery handles billing-related queries.
func (w *customerSupportWorkflow) handleBillingQuery(ctx context.Context, state graph.State) (any, error) {
	query := state[stateKeyCustomerQuery].(string)
	priority := state[stateKeyPriority].(string)

	response := fmt.Sprintf("I understand you have a billing inquiry: \"%s\". "+
		"This has been classified as %s priority. "+
		"Our billing team will review your request and get back to you within 24 hours. "+
		"In the meantime, you can check your account dashboard for recent transactions.",
		query, priority)

	// Return state update with response
	return newStateBuilder().
		SetResponse(response).
		Build(), nil
}

// handleGeneralQuery handles general customer service queries.
func (w *customerSupportWorkflow) handleGeneralQuery(ctx context.Context, state graph.State) (any, error) {
	query := state[stateKeyCustomerQuery].(string)

	response := fmt.Sprintf("Thank you for your inquiry: \"%s\". "+
		"I'm here to help with general questions. "+
		"Our customer service team is available Monday-Friday, 9 AM to 6 PM EST. "+
		"For immediate assistance, you can also check our FAQ section on our website.",
		query)

	// Return state update with response
	return newStateBuilder().
		SetResponse(response).
		Build(), nil
}

// formatFinalResponse formats the final response for the customer.
func (w *customerSupportWorkflow) formatFinalResponse(ctx context.Context, state graph.State) (any, error) {
	// Get the response from the previous node
	var response string
	if responseVal, exists := state[stateKeyResponse]; exists {
		if resp, ok := responseVal.(string); ok {
			response = resp
		}
	}

	// If no response from previous node, try to get from agent/LLM response.
	if response == "" {
		if lastResponse, exists := state[graph.StateKeyLastResponse]; exists {
			switch resp := lastResponse.(type) {
			case string:
				response = resp
			case *model.Response:
				if resp != nil && len(resp.Choices) > 0 {
					response = resp.Choices[0].Message.Content
				}
			}
		}
	}

	queryType := state[stateKeyQueryType].(string)
	priority := state[stateKeyPriority].(string)

	finalResponse := fmt.Sprintf("🎯 Customer Support Response\n"+
		"Query Type: %s\n"+
		"Priority: %s\n"+
		"Response: %s\n"+
		"Thank you for contacting our support team!",
		queryType, priority, response)

	// Return state update with final response. Also set graph.StateKeyLastResponse
	// so completion events carry the final content for consumers that only read
	// the standard last_response field.
	return graph.State{
		stateKeyFinalAnswer:        finalResponse,
		graph.StateKeyLastResponse: finalResponse,
	}, nil
}

// Helper functions.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

// stateBuilder helps build state updates in a type-safe way.
type stateBuilder struct {
	state graph.State
}

// newStateBuilder creates a new state builder.
func newStateBuilder() *stateBuilder {
	return &stateBuilder{
		state: make(graph.State),
	}
}

// SetCustomerQuery sets the customer query.
func (sb *stateBuilder) SetCustomerQuery(query string) *stateBuilder {
	sb.state[stateKeyCustomerQuery] = query
	return sb
}

// SetQueryType sets the query type.
func (sb *stateBuilder) SetQueryType(queryType string) *stateBuilder {
	sb.state[stateKeyQueryType] = queryType
	return sb
}

// SetPriority sets the priority.
func (sb *stateBuilder) SetPriority(priority string) *stateBuilder {
	sb.state[stateKeyPriority] = priority
	return sb
}

// SetResponse sets the response.
func (sb *stateBuilder) SetResponse(response string) *stateBuilder {
	sb.state[stateKeyResponse] = response
	return sb
}

// SetFinalAnswer sets the final answer.
func (sb *stateBuilder) SetFinalAnswer(answer string) *stateBuilder {
	sb.state[stateKeyFinalAnswer] = answer
	return sb
}

// Build returns the built state.
func (sb *stateBuilder) Build() graph.State {
	return sb.state
}

// truncateString truncates a string to the specified length and adds ellipsis if needed.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
