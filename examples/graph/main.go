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
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
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

// State key constants for type safety and maintainability.
const (
	// Core message and input fields
	StateKeyMessages  = "messages"
	StateKeyInput     = "input"
	StateKeyMessage   = "message"
	StateKeyUserInput = "user_input"

	// Document processing fields
	StateKeyDocumentLength  = "document_length"
	StateKeyWordCount       = "word_count"
	StateKeyComplexityLevel = "complexity_level"
	StateKeyQualityScore    = "quality_score"
	StateKeyProcessingType  = "processing_type"

	// Workflow control fields
	StateKeyProcessingStage  = "processing_stage"
	StateKeyLastResponse     = "last_response"
	StateKeyProcessedContent = "processed_content"
	StateKeyFinalOutput      = "final_output"
	StateKeyResult           = "result"

	// Workflow metadata fields
	StateKeyWorkflowVersion  = "workflow_version"
	StateKeyProcessingMode   = "processing_mode"
	StateKeyQualityThreshold = "quality_threshold"
	StateKeySession          = "session"
)

// Complexity level constants for type safety.
const (
	ComplexitySimple   = "simple"
	ComplexityModerate = "moderate"
	ComplexityComplex  = "complex"
)

// Quality routing constants.
const (
	QualityGood = "good"
	QualityPoor = "poor"
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
	interactive = flag.Bool("interactive", false,
		"Run in interactive mode")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Document Processing Workflow Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Interactive: %v\n", *interactive)
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
	if err := w.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	if *interactive {
		// Start interactive mode.
		return w.startInteractiveMode(ctx)
	}

	// Run predefined examples.
	return w.runExamples(ctx)
}

// setup creates the graph agent and runner.
func (w *documentWorkflow) setup(ctx context.Context) error {
	// Create the document processing graph.
	workflowGraph, err := w.createDocumentProcessingGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent from the compiled graph.
	graphAgent, err := graphagent.New("document-processor", workflowGraph,
		graphagent.WithDescription("Comprehensive document processing workflow"),
		graphagent.WithChannelBufferSize(defaultChannelBufferSize),
		graphagent.WithInitialState(graph.State{
			StateKeyWorkflowVersion:  "2.0",
			StateKeyProcessingMode:   "comprehensive",
			StateKeyQualityThreshold: 0.75,
		}),
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

// createDocumentProcessingGraph creates a document processing workflow graph.
func (w *documentWorkflow) createDocumentProcessingGraph() (*graph.Graph, error) {
	// Create extended state schema for messages and metadata.
	schema := graph.ExtendedMessagesStateSchema()

	// Add custom fields for our workflow.
	schema.AddField(StateKeyDocumentLength, graph.StateField{
		Type:    reflect.TypeOf(0),
		Reducer: graph.DefaultReducer,
	})

	schema.AddField(StateKeyWordCount, graph.StateField{
		Type:    reflect.TypeOf(0),
		Reducer: graph.DefaultReducer,
	})

	schema.AddField(StateKeyComplexityLevel, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})

	schema.AddField(StateKeyQualityScore, graph.StateField{
		Type:    reflect.TypeOf(0.0),
		Reducer: graph.DefaultReducer,
	})

	schema.AddField(StateKeyProcessingType, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})

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

	// Create builder with schema.
	builder := graph.NewBuilderWithSchema(schema)

	// Build the workflow graph.
	builder.
		// Add preprocessing node.
		AddFunctionNode("preprocess", "Preprocess Document",
			"Validates and preprocesses the input document",
			w.preprocessDocument).

		// Add LLM analyzer node.
		AddLLMNode("analyze", "Document Analyzer", modelInstance,
			`You are a document analysis expert. Analyze the provided document and:
1. Classify the document type and complexity (simple, moderate, complex)
2. Extract key themes and topics
3. Assess content quality
Use the analyze_complexity tool for detailed analysis.
Respond with a structured analysis including complexity level and key insights.`,
			map[string]tool.Tool{"analyze_complexity": complexityTool}).

		// Add complexity routing.
		AddFunctionNode("route_complexity", "Route by Complexity",
			"Routes processing based on document complexity",
			w.routeComplexity).

		// Add simple processing path.
		AddFunctionNode("simple_process", "Simple Processing",
			"Handles straightforward document processing",
			w.simpleProcessing).

		// Add LLM summarizer node for complex documents.
		AddLLMNode("summarize", "Document Summarizer", modelInstance,
			`You are a document summarization expert. Create a comprehensive yet concise summary of the provided document.
Focus on:
1. Key points and main arguments
2. Important details and insights
3. Logical structure and flow
4. Conclusions and implications
Provide a well-structured summary that preserves the essential information.`,
			nil).

		// Add quality assessment.
		AddFunctionNode("assess_quality", "Assess Quality",
			"Evaluates the quality of processed content",
			w.assessQuality).

		// Add quality routing.
		AddFunctionNode("route_quality", "Route by Quality",
			"Routes based on content quality assessment",
			w.routeQuality).

		// Add LLM enhancer for low-quality content.
		AddLLMNode("enhance", "Content Enhancer", modelInstance,
			`You are a content enhancement expert. Improve the provided content by:
1. Enhancing clarity and readability
2. Improving structure and organization
3. Adding relevant details where appropriate
4. Ensuring consistency and coherence
Focus on making the content more engaging and professional while preserving the original meaning.`,
			nil).

		// Add final formatting.
		AddFunctionNode("format_output", "Format Output",
			"Formats the final processed document",
			w.formatOutput).

		// Set up the workflow routing.
		SetEntryPoint("preprocess").
		SetFinishPoint("format_output")

	// Add workflow edges.
	builder.
		AddEdge("preprocess", "analyze").
		AddEdge("analyze", "route_complexity")

		// Add conditional routing for complexity.
	builder.GetStateGraph().AddConditionalEdges("route_complexity", w.complexityCondition, map[string]string{
		ComplexitySimple:  "simple_process",
		ComplexityComplex: "summarize",
	})

	builder.
		AddEdge("simple_process", "assess_quality").
		AddEdge("summarize", "assess_quality").
		AddEdge("assess_quality", "route_quality")

	// Add conditional routing for quality.
	builder.GetStateGraph().AddConditionalEdges("route_quality", w.qualityCondition, map[string]string{
		QualityGood: "format_output",
		QualityPoor: "enhance",
	})

	builder.AddEdge("enhance", "format_output")

	// Build and return the graph.
	return builder.Build()
}

// Node function implementations.

func (w *documentWorkflow) preprocessDocument(ctx context.Context,
	state graph.State) (any, error) {

	// Get input from GraphAgent's state fields
	var input string

	// Try 'input' field first (GraphAgent stores user input here)
	if userInput, ok := state[StateKeyInput].(string); ok && userInput != "" {
		input = userInput
	} else if messageInput, ok := state[StateKeyMessage].(string); ok && messageInput != "" {
		// Try 'message' field as fallback
		input = messageInput
	} else if messages, ok := state[StateKeyMessages].([]model.Message); ok && len(messages) > 0 {
		// Fallback to messages array if available
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == model.RoleUser {
				input = messages[i].Content
				break
			}
		}
	}

	if input == "" {
		return nil, fmt.Errorf("no input document found (checked input, message, and messages fields)")
	}

	// Basic preprocessing
	input = strings.TrimSpace(input)
	if len(input) < 10 {
		return nil, fmt.Errorf("document too short for processing (minimum 10 characters)")
	}

	// Use StateBuilder for cleaner state construction
	return NewStateBuilder().
		SetDocumentLength(len(input)).
		SetWordCount(len(strings.Fields(input))).
		SetUserInput(input).
		Build(), nil
}

func (w *documentWorkflow) routeComplexity(ctx context.Context,
	state graph.State) (any, error) {

	// This is just a pass-through node; actual routing happens via conditional edges.
	return graph.State{
		StateKeyProcessingStage: "complexity_routing",
	}, nil
}

func (w *documentWorkflow) complexityCondition(ctx context.Context,
	state graph.State) (string, error) {

	// Check if we have complexity analysis from the analyzer.
	if lastResponse, ok := state[StateKeyLastResponse].(string); ok {
		responseLower := strings.ToLower(lastResponse)
		if strings.Contains(responseLower, "complex") ||
			strings.Contains(responseLower, "advanced") ||
			strings.Contains(responseLower, "sophisticated") {
			return ComplexityComplex, nil
		}
	}

	// Fallback to document length heuristic.
	if wordCount, ok := state[StateKeyWordCount].(int); ok {
		if wordCount > 200 {
			return ComplexityComplex, nil
		}
	}

	return ComplexitySimple, nil
}

func (w *documentWorkflow) simpleProcessing(ctx context.Context,
	state graph.State) (any, error) {

	// Use StateHelper for type-safe access
	helper := NewStateHelper(state)

	input, err := helper.GetUserInput()
	if err != nil {
		return nil, fmt.Errorf("failed to get user input: %w", err)
	}

	wordCount, err := helper.GetWordCount()
	if err != nil {
		return nil, fmt.Errorf("failed to get word count: %w", err)
	}

	// Simple processing: basic formatting and structure
	processed := fmt.Sprintf("=== PROCESSED DOCUMENT ===\n\n%s\n\n"+
		"=== PROCESSING DETAILS ===\n"+
		"- Processing Type: Simple\n"+
		"- Word Count: %d\n"+
		"- Processed At: %s\n",
		input,
		wordCount,
		time.Now().Format(time.RFC3339))

	// Use StateBuilder for clean state updates
	return NewStateBuilder().
		SetProcessedContent(processed).
		SetProcessingType("simple").
		SetComplexityLevel(ComplexitySimple).
		Build(), nil
}

func (w *documentWorkflow) assessQuality(ctx context.Context,
	state graph.State) (any, error) {

	var content string

	// Get content from either simple processing or LLM response.
	if processed, ok := state[StateKeyProcessedContent].(string); ok {
		content = processed
	} else if lastResponse, ok := state[StateKeyLastResponse].(string); ok {
		content = lastResponse
	} else {
		return nil, fmt.Errorf("no content found for quality assessment")
	}

	// Simple quality assessment based on content characteristics.
	quality := 0.5 // Base quality score.

	// Length factor.
	if len(content) > 100 {
		quality += 0.2
	}

	// Structure factor (presence of formatting).
	if strings.Contains(content, "\n") &&
		(strings.Contains(content, "===") || strings.Contains(content, "##")) {
		quality += 0.1
	}

	// Content richness (word variety).
	words := strings.Fields(content)
	if len(words) > 50 {
		quality += 0.15
	}

	// Complexity bonus.
	if complexityLevel, ok := state[StateKeyComplexityLevel].(string); ok && complexityLevel == ComplexityComplex {
		quality += 0.05
	}

	return graph.State{
		StateKeyQualityScore: quality,
	}, nil
}

func (w *documentWorkflow) routeQuality(ctx context.Context,
	state graph.State) (any, error) {

	// This is just a pass-through node; actual routing happens via conditional edges.
	return graph.State{
		StateKeyProcessingStage: "quality_routing",
	}, nil
}

func (w *documentWorkflow) qualityCondition(ctx context.Context,
	state graph.State) (string, error) {

	qualityScore, ok := state[StateKeyQualityScore].(float64)
	if !ok {
		return QualityPoor, fmt.Errorf("quality score not found")
	}

	threshold := 0.75
	if qualityScore >= threshold {
		return QualityGood, nil
	}
	return QualityPoor, nil
}

func (w *documentWorkflow) formatOutput(ctx context.Context,
	state graph.State) (any, error) {

	var content string

	// Get the final content.
	if enhanced, ok := state[StateKeyLastResponse].(string); ok {
		content = enhanced
	} else if processed, ok := state[StateKeyProcessedContent].(string); ok {
		content = processed
	} else {
		return nil, fmt.Errorf("no content found for formatting")
	}

	// Create final formatted output.
	processingType, _ := state[StateKeyProcessingType].(string)
	complexityLevel, _ := state[StateKeyComplexityLevel].(string)
	qualityScore, _ := state[StateKeyQualityScore].(float64)
	wordCount, _ := state[StateKeyWordCount].(int)

	finalOutput := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                    DOCUMENT PROCESSING RESULTS                   ║
╚══════════════════════════════════════════════════════════════════╝

%s

╔══════════════════════════════════════════════════════════════════╗
║                         PROCESSING DETAILS                       ║
╚══════════════════════════════════════════════════════════════════╝

📊 Processing Statistics:
   • Processing Type: %s
   • Complexity Level: %s
   • Quality Score: %.2f
   • Word Count: %d
   • Completed At: %s

✅ Processing completed successfully!
`,
		content,
		processingType,
		complexityLevel,
		qualityScore,
		wordCount,
		time.Now().Format("2006-01-02 15:04:05"))

	return graph.State{
		StateKeyFinalOutput: finalOutput,
		StateKeyResult:      finalOutput,
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
		level = ComplexitySimple
		score = 0.3
	} else if wordCount < 200 {
		level = ComplexityModerate
		score = 0.6
	} else {
		level = ComplexityComplex
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
func (w *documentWorkflow) processDocument(ctx context.Context,
	content string) error {

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
			if stageCount > 1 && len(event.Response.Choices) > 0 {
				content := event.Response.Choices[0].Message.Content
				if content != "" {
					fmt.Printf("\n🔄 Stage %d completed\n", stageCount)
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
