//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package main demonstrates a document processing workflow using the graph package.
// This example shows how to build and execute graphs with conditional routing,
// LLM nodes, and function nodes.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// Default model name for deepseek-chat.
	defaultModelName = "deepseek-chat"
	// Default channel buffer size.
	defaultChannelBufferSize = 512
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
)

func main() {
	// Parse command line flags.
	flag.Parse()
	fmt.Printf("🚀 Document Processing Workflow Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))
	// Create and run the workflow.
	workflow := &documentWorkflow{
		modelName: *modelName,
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// documentWorkflow manages the document processing workflow.
type documentWorkflow struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

// run starts the document processing workflow.
func (w *documentWorkflow) run() error {
	ctx := context.Background()
	// Setup the workflow.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	return w.startInteractiveMode(ctx)
}

// setup creates the graph agent and runner.
func (w *documentWorkflow) setup() error {
	// Create the document processing graph.
	workflowGraph, err := w.createDocumentProcessingGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent from the compiled graph.
	graphAgent, err := graphagent.New("document-processor", workflowGraph,
		graphagent.WithDescription("Comprehensive document processing workflow"),
		graphagent.WithChannelBufferSize(defaultChannelBufferSize),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner with the graph agent.
	appName := "document-workflow"
	w.runner = runner.NewRunner(
		appName,
		graphAgent,
		runner.WithSessionService(sessionService),
	)

	// Setup identifiers.
	w.userID = "user"
	w.sessionID = fmt.Sprintf("workflow-session-%d", time.Now().Unix())

	fmt.Printf("✅ Document workflow ready! Session: %s\n\n", w.sessionID)
	return nil
}

const (
	complexitySimple   = "simple"
	complexityModerate = "moderate"
	complexityComplex  = "complex"
)

const (
	stateKeyDocumentLength  = "document_length"
	stateKeyWordCount       = "word_count"
	stateKeyComplexityLevel = "complexity_level"
	stateKeyProcessingStage = "processing_stage"
)

// createDocumentProcessingGraph creates a document processing workflow graph.
func (w *documentWorkflow) createDocumentProcessingGraph() (*graph.Graph, error) {
	// Create extended state schema for messages and metadata.
	schema := graph.MessagesStateSchema()

	// Create model instance.
	modelInstance := openai.New(*modelName, openai.Options{
		ChannelBufferSize: defaultChannelBufferSize,
	})

	// Create analysis tools.
	complexityTool := function.NewFunctionTool(
		w.analyzeComplexity,
		function.WithName("analyze_complexity"),
		function.WithDescription("Analyzes document complexity level"),
	)

	// Create stateGraph with schema.
	stateGraph := graph.NewStateGraph(schema)
	tools := map[string]tool.Tool{
		"analyze_complexity": complexityTool,
	}

	// Build the workflow graph.
	stateGraph.
		// Add preprocessing node.
		AddNode("preprocess", w.preprocessDocument).

		// Add LLM analyzer node.
		AddLLMNode("analyze", modelInstance,
			`You are a document analysis expert. Analyze the provided document and:
1. Classify the document type and complexity (simple, moderate, complex)
2. Extract key themes and topics
3. Assess content quality
Use the analyze_complexity tool for detailed analysis.
Respond with a structured analysis including only the complexity level itself: "simple" or "complex".`,
			tools).
		AddToolsNode("tools", tools).

		// Add complexity routing.
		AddNode("route_complexity", w.routeComplexity).

		// Add LLM summarizer node for complex documents.
		AddLLMNode("summarize", modelInstance,
			`You are a document summarization expert. Create a comprehensive yet concise summary of the provided document.
Focus on:
1. Key points and main arguments
2. Important details and insights
3. Logical structure and flow
4. Conclusions and implications
Provide a well-structured summary that preserves the essential information.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).

		// Add LLM enhancer for low-quality content.
		AddLLMNode("enhance", modelInstance,
			`You are a content enhancement expert. Improve the provided content by:
1. Enhancing clarity and readability
2. Improving structure and organization
3. Adding relevant details where appropriate
4. Ensuring consistency and coherence
Focus on making the content more engaging and professional while preserving the original meaning.
Remember: only output the final result itself, no other text.`,
			map[string]tool.Tool{}).

		// Add final formatting.
		AddNode("format_output", w.formatOutput).

		// Set up the workflow routing.
		SetEntryPoint("preprocess").
		SetFinishPoint("format_output")

	// Add workflow edges.
	stateGraph.AddEdge("preprocess", "analyze")
	stateGraph.AddToolsConditionalEdges("analyze", "tools", "route_complexity")
	stateGraph.AddEdge("tools", "analyze")

	// Add conditional routing for complexity.
	stateGraph.AddConditionalEdges("route_complexity", w.complexityCondition, map[string]string{
		complexitySimple:  "enhance",
		complexityComplex: "summarize",
	})

	stateGraph.AddEdge("enhance", "format_output")
	stateGraph.AddEdge("summarize", "format_output")

	// Build and return the graph.
	return stateGraph.Compile()
}

// Node function implementations.

func (w *documentWorkflow) preprocessDocument(ctx context.Context, state graph.State) (any, error) {
	// Get input from GraphAgent's state fields
	var input string
	if userInput, ok := state[graph.StateKeyUserInput].(string); ok {
		input = userInput
	}
	if input == "" {
		return nil, errors.New("no input document found (checked input field)")
	}
	// Basic preprocessing
	input = strings.TrimSpace(input)
	if len(input) < 10 {
		return nil, errors.New("document too short for processing (minimum 10 characters)")
	}
	// Return state with preprocessing results.
	return graph.State{
		stateKeyDocumentLength:  len(input),
		stateKeyWordCount:       len(strings.Fields(input)),
		graph.StateKeyUserInput: input,
		stateKeyProcessingStage: "preprocessing",
	}, nil
}

func (w *documentWorkflow) routeComplexity(ctx context.Context, state graph.State) (any, error) {
	// This is just a pass-through node; actual routing happens via conditional edges.
	return graph.State{
		stateKeyProcessingStage: "complexity_routing",
	}, nil
}

func (w *documentWorkflow) complexityCondition(ctx context.Context, state graph.State) (level string, err error) {
	defer func() {
		state[stateKeyComplexityLevel] = level
	}()
	// Check if we have complexity analysis from the analyzer.
	if lastResponse, ok := state[graph.StateKeyLastResponse].(string); ok {
		responseLower := strings.ToLower(lastResponse)
		if strings.Contains(responseLower, " complex ") {
			return complexityComplex, nil
		}
	}
	// Fallback to document length heuristic.
	const complexityThreshold = 200
	if wordCount, ok := state[stateKeyWordCount].(int); ok {
		if wordCount > complexityThreshold {
			return complexityComplex, nil
		}
	}
	return complexitySimple, nil
}

func (w *documentWorkflow) formatOutput(ctx context.Context, state graph.State) (any, error) {
	content, ok := state[graph.StateKeyLastResponse].(string)
	if !ok {
		return nil, fmt.Errorf("no content found for formatting")
	}
	// Create final formatted output.
	complexityLevel, _ := state[stateKeyComplexityLevel].(string)
	wordCount, _ := state[stateKeyWordCount].(int)

	finalOutput := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                    DOCUMENT PROCESSING RESULTS                   ║
╚══════════════════════════════════════════════════════════════════╝

%s

╔══════════════════════════════════════════════════════════════════╗
║                         PROCESSING DETAILS                       ║
╚══════════════════════════════════════════════════════════════════╝

📊 Processing Statistics:
   • Complexity Level: %s
   • Word Count: %d
   • Completed At: %s

✅ Processing completed successfully!
`,
		content,
		complexityLevel,
		wordCount,
		time.Now().Format("2006-01-02 15:04:05"))

	return graph.State{
		graph.StateKeyLastResponse: finalOutput,
	}, nil
}

// Tool function implementations.

func (w *documentWorkflow) analyzeComplexity(args complexityArgs) complexityResult {
	text := args.Text

	// Simple complexity analysis.
	wordCount := len(strings.Fields(text))
	sentenceCount := strings.Count(text, ".") + strings.Count(text, "!") +
		strings.Count(text, "?")

	var level string
	var score float64

	if wordCount < 50 {
		level = complexitySimple
		score = 0.3
	} else if wordCount < 200 {
		level = complexityModerate
		score = 0.6
	} else {
		level = complexityComplex
		score = 0.9
	}

	return complexityResult{
		Level:         level,
		Score:         score,
		WordCount:     wordCount,
		SentenceCount: sentenceCount,
	}
}

// runExamples runs predefined workflow examples.
func (w *documentWorkflow) runExamples(ctx context.Context) error {
	examples := []struct {
		name    string
		content string
	}{
		{
			name: "Simple Business Report",
			content: "This quarterly report shows positive growth trends. " +
				"Revenue increased by 15% compared to last quarter. " +
				"Customer satisfaction remains high at 92%.",
		},
		{
			name: "Complex Technical Document",
			content: "The implementation of microservices architecture requires " +
				"careful consideration of service boundaries, data consistency, " +
				"and inter-service communication patterns. Key challenges include " +
				"distributed transaction management, service discovery, and " +
				"monitoring across multiple services. The proposed solution " +
				"leverages container orchestration with Kubernetes, implements " +
				"event-driven architecture using message queues, and establishes " +
				"comprehensive observability through distributed tracing and " +
				"centralized logging. Performance benchmarks indicate 40% " +
				"improvement in scalability and 25% reduction in response times.",
		},
		{
			name: "Research Abstract",
			content: "This study investigates the impact of artificial intelligence " +
				"on modern workplace productivity. Through comprehensive analysis " +
				"of 500 organizations across various industries, we examine the " +
				"correlation between AI adoption and employee efficiency metrics. " +
				"Our findings suggest that organizations implementing AI tools " +
				"experience an average productivity increase of 23%, while also " +
				"reporting improved job satisfaction among employees who receive " +
				"adequate training and support during the transition process.",
		},
	}

	for _, example := range examples {
		fmt.Printf("\n🔄 Processing: %s\n", example.name)
		fmt.Println(strings.Repeat("-", 60))

		if err := w.processDocument(ctx, example.content); err != nil {
			fmt.Printf("❌ Error processing %s: %v\n", example.name, err)
			continue
		}

		fmt.Printf("✅ Completed: %s\n", example.name)
	}

	return nil
}

// startInteractiveMode starts the interactive document processing mode.
func (w *documentWorkflow) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 Interactive Document Processing Mode")
	fmt.Println("   Enter your document content (or 'exit' to quit)")
	fmt.Println("   Type 'help' for available commands")
	fmt.Println()

	for {
		fmt.Print("📄 Document: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "help":
			w.showHelp()
			continue
		}

		// Process the document.
		if err := w.processDocument(ctx, input); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between documents.
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processDocument processes a single document through the workflow.
func (w *documentWorkflow) processDocument(ctx context.Context, content string) error {
	// Create user message.
	message := model.NewUserMessage(content)
	// Run the workflow through the runner.
	eventChan, err := w.runner.Run(ctx, w.userID, w.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run workflow: %w", err)
	}
	// Process streaming response.
	return w.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming workflow response.
func (w *documentWorkflow) processStreamingResponse(
	eventChan <-chan *event.Event) error {
	var (
		workflowStarted bool
		stageCount      int
		finalResult     string
	)
	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}
		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !workflowStarted {
					fmt.Print("🤖 Workflow: ")
					workflowStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
			// Store final result if this is a completion.
			if choice.Message.Content != "" && event.Done {
				finalResult = choice.Message.Content
			}
		}
		// Track workflow stages.
		if event.Author == graph.AuthorGraphExecutor {
			stageCount++
			if stageCount >= 1 && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				if content != "" {
					fmt.Printf("\n🔄 Stage %d completed, %s\n", stageCount, content)
				}
			}
		}
		// Handle completion.
		if event.Done {
			// Check if we have a final output in the completion message.
			if finalResult != "" && strings.Contains(finalResult, "DOCUMENT PROCESSING RESULTS") {
				fmt.Printf("\n\n%s\n", finalResult)
			}
			break
		}
	}
	return nil
}

// showHelp displays available commands.
func (w *documentWorkflow) showHelp() {
	fmt.Println("📚 Available Commands:")
	fmt.Println("   help  - Show this help message")
	fmt.Println("   exit  - Exit the application")
	fmt.Println()
	fmt.Println("💡 Usage:")
	fmt.Println("   Simply paste or type your document content")
	fmt.Println("   The workflow will automatically:")
	fmt.Println("   • Validate and preprocess the document")
	fmt.Println("   • Analyze complexity and themes")
	fmt.Println("   • Route to appropriate processing path")
	fmt.Println("   • Assess and enhance quality if needed")
	fmt.Println("   • Format the final output")
	fmt.Println()
}

// Type definitions for tool functions.

type complexityArgs struct {
	Text string `json:"text" description:"Text to analyze for complexity"`
}

type complexityResult struct {
	Level         string  `json:"level"`
	Score         float64 `json:"score"`
	WordCount     int     `json:"word_count"`
	SentenceCount int     `json:"sentence_count"`
}
