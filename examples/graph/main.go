//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package main demonstrates a comprehensive document processing workflow
// using GraphAgent with the Runner.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
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
	// Application name.
	appName = "document-workflow"
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

	fmt.Printf("🚀 Document Processing Workflow with GraphAgent\n")
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

// setup creates the runner with graph agent.
func (w *documentWorkflow) setup(ctx context.Context) error {
	// Create the document processing graph agent.
	graphAgent, err := w.createDocumentProcessingAgent()
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create session service.
	sessionService := inmemory.NewSessionService()

	// Create runner.
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

// createDocumentProcessingAgent creates a comprehensive document processing
// graph agent.
func (w *documentWorkflow) createDocumentProcessingAgent() (*graphagent.GraphAgent, error) {
	// Create specialized agents for different processing stages.
	validatorAgent, err := w.createValidatorAgent()
	if err != nil {
		return nil, fmt.Errorf("failed to create validator agent: %w", err)
	}

	analyzerAgent, err := w.createAnalyzerAgent()
	if err != nil {
		return nil, fmt.Errorf("failed to create analyzer agent: %w", err)
	}

	summarizerAgent, err := w.createSummarizerAgent()
	if err != nil {
		return nil, fmt.Errorf("failed to create summarizer agent: %w", err)
	}

	enhancerAgent, err := w.createEnhancerAgent()
	if err != nil {
		return nil, fmt.Errorf("failed to create enhancer agent: %w", err)
	}

	// Create the document processing workflow graph.
	workflowGraph, err := graph.NewBuilder().
		// Start the workflow.
		AddStartNode("start", "Start Document Processing").

		// Input validation and preprocessing.
		AddFunctionNode("preprocess", "Preprocess Input",
			"Preprocesses and validates input document",
			w.preprocessDocument).

		// Document validation using AI agent.
		AddAgentNode("validate", "Validate Document",
			"Validates document format and content quality",
			"validator").

		// Document analysis and classification.
		AddAgentNode("analyze", "Analyze Document",
			"Analyzes document type, complexity, and key themes",
			"analyzer").

		// Route based on document complexity.
		AddConditionNode("route_complexity", "Route by Complexity",
			"Routes processing based on document complexity",
			w.routeByComplexity).

		// Simple processing path.
		AddFunctionNode("simple_process", "Simple Processing",
			"Handles straightforward document processing",
			w.simpleProcessing).

		// Complex processing path with summarization.
		AddAgentNode("summarize", "Summarize Document",
			"Creates comprehensive summary for complex documents",
			"summarizer").

		// Quality assessment.
		AddFunctionNode("assess_quality", "Assess Quality",
			"Assesses the quality of processed content",
			w.assessQuality).

		// Quality-based routing.
		AddConditionNode("route_quality", "Route by Quality",
			"Routes based on content quality assessment",
			w.routeByQuality).

		// Enhancement for low-quality content.
		AddAgentNode("enhance", "Enhance Content",
			"Enhances and improves content quality",
			"enhancer").

		// Final output formatting.
		AddFunctionNode("format_output", "Format Output",
			"Formats the final processed document",
			w.formatOutput).

		// End the workflow.
		AddEndNode("end", "Processing Complete").

		// Define the workflow edges.
		AddEdge("start", "preprocess").
		AddEdge("preprocess", "validate").
		AddEdge("validate", "analyze").
		AddEdge("analyze", "route_complexity").
		AddEdge("route_complexity", "simple_process").
		AddEdge("route_complexity", "summarize").
		AddEdge("simple_process", "assess_quality").
		AddEdge("summarize", "assess_quality").
		AddEdge("assess_quality", "route_quality").
		AddEdge("route_quality", "enhance").
		AddEdge("route_quality", "format_output").
		AddEdge("enhance", "format_output").
		AddEdge("format_output", "end").
		Build()

	if err != nil {
		return nil, fmt.Errorf("failed to build workflow graph: %w", err)
	}

	// Create graph agent with all sub-agents.
	return graphagent.New("document-processor", workflowGraph,
		graphagent.WithDescription("Comprehensive document processing workflow"),
		graphagent.WithSubAgents([]agent.Agent{
			validatorAgent,
			analyzerAgent,
			summarizerAgent,
			enhancerAgent,
		}),
		graphagent.WithChannelBufferSize(defaultChannelBufferSize),
		graphagent.WithInitialState(graph.State{
			"workflow_version":  "2.0",
			"processing_mode":   "comprehensive",
			"quality_threshold": 0.75,
		}),
	)
}

// createValidatorAgent creates an agent for document validation.
func (w *documentWorkflow) createValidatorAgent() (*llmagent.LLMAgent, error) {
	modelInstance := openai.New(*modelName, openai.Options{
		ChannelBufferSize: defaultChannelBufferSize,
	})

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1000),
		Temperature: floatPtr(0.3), // Lower temperature for validation.
		Stream:      true,
	}

	return llmagent.New("validator",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Document validation specialist"),
		llmagent.WithInstruction(`You are a document validation expert. 
Your task is to:
1. Check document format and structure
2. Identify potential issues or inconsistencies
3. Assess content completeness
4. Provide validation feedback

Always respond with clear validation results and any recommendations.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(defaultChannelBufferSize),
	), nil
}

// createAnalyzerAgent creates an agent for document analysis.
func (w *documentWorkflow) createAnalyzerAgent() (*llmagent.LLMAgent, error) {
	modelInstance := openai.New(*modelName, openai.Options{
		ChannelBufferSize: defaultChannelBufferSize,
	})

	// Create analysis tools.
	complexityTool := function.NewFunctionTool(
		w.analyzeComplexity,
		function.WithName("analyze_complexity"),
		function.WithDescription("Analyzes document complexity level"),
	)

	themeTool := function.NewFunctionTool(
		w.extractThemes,
		function.WithName("extract_themes"),
		function.WithDescription("Extracts key themes from document"),
	)

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1500),
		Temperature: floatPtr(0.4),
		Stream:      true,
	}

	return llmagent.New("analyzer",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Document analysis specialist"),
		llmagent.WithInstruction(`You are a document analysis expert. 
Your task is to:
1. Classify document type and genre
2. Assess complexity level (simple, moderate, complex)
3. Extract key themes and topics
4. Identify main concepts and relationships

Use the available tools to perform detailed analysis. 
Provide structured analysis results.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(defaultChannelBufferSize),
		llmagent.WithTools([]tool.Tool{complexityTool, themeTool}),
	), nil
}

// createSummarizerAgent creates an agent for document summarization.
func (w *documentWorkflow) createSummarizerAgent() (*llmagent.LLMAgent, error) {
	modelInstance := openai.New(*modelName, openai.Options{
		ChannelBufferSize: defaultChannelBufferSize,
	})

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.5),
		Stream:      true,
	}

	return llmagent.New("summarizer",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Document summarization specialist"),
		llmagent.WithInstruction(`You are a document summarization expert. 
Your task is to:
1. Create comprehensive yet concise summaries
2. Preserve key information and main points
3. Maintain logical structure and flow
4. Highlight important insights and conclusions

Create summaries that are informative and well-structured.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(defaultChannelBufferSize),
	), nil
}

// createEnhancerAgent creates an agent for content enhancement.
func (w *documentWorkflow) createEnhancerAgent() (*llmagent.LLMAgent, error) {
	modelInstance := openai.New(*modelName, openai.Options{
		ChannelBufferSize: defaultChannelBufferSize,
	})

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(1500),
		Temperature: floatPtr(0.6),
		Stream:      true,
	}

	return llmagent.New("enhancer",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Content enhancement specialist"),
		llmagent.WithInstruction(`You are a content enhancement expert. 
Your task is to:
1. Improve clarity and readability
2. Enhance structure and organization
3. Add relevant details where needed
4. Ensure consistency and coherence

Focus on making content more engaging and professional.`),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(defaultChannelBufferSize),
	), nil
}

// Graph function node implementations.

func (w *documentWorkflow) preprocessDocument(ctx context.Context,
	state graph.State) (graph.State, error) {
	input, ok := state["input"].(string)
	if !ok {
		return state, fmt.Errorf("input must be a string")
	}

	// Basic preprocessing.
	input = strings.TrimSpace(input)
	if len(input) < 10 {
		return state, fmt.Errorf("document too short for processing")
	}

	state["preprocessed"] = true
	state["document_length"] = len(input)
	state["word_count"] = len(strings.Fields(input))
	state["preprocessing_time"] = time.Now()

	return state, nil
}

func (w *documentWorkflow) routeByComplexity(ctx context.Context,
	state graph.State) (string, error) {
	// Check if we have complexity analysis from the analyzer agent.
	if output, ok := state["last_agent_output"].(string); ok {
		// Simple heuristic: look for complexity indicators in agent output.
		outputLower := strings.ToLower(output)
		if strings.Contains(outputLower, "complex") ||
			strings.Contains(outputLower, "advanced") ||
			strings.Contains(outputLower, "sophisticated") {
			return "summarize", nil
		}
	}

	// Fallback to document length heuristic.
	if wordCount, ok := state["word_count"].(int); ok {
		if wordCount > 200 {
			return "summarize", nil
		}
	}

	return "simple_process", nil
}

func (w *documentWorkflow) simpleProcessing(ctx context.Context,
	state graph.State) (graph.State, error) {
	input := state["input"].(string)

	// Simple processing: basic formatting and structure.
	processed := fmt.Sprintf("PROCESSED DOCUMENT:\n\n%s\n\nProcessing completed at: %s",
		input, time.Now().Format(time.RFC3339))

	state["processed_content"] = processed
	state["processing_type"] = "simple"
	state["complexity_level"] = "simple"

	return state, nil
}

func (w *documentWorkflow) assessQuality(ctx context.Context,
	state graph.State) (graph.State, error) {
	var content string

	// Get content from either simple processing or summarization.
	if processed, ok := state["processed_content"].(string); ok {
		content = processed
	} else if output, ok := state["last_agent_output"].(string); ok {
		content = output
	} else {
		return state, fmt.Errorf("no content found for quality assessment")
	}

	// Simple quality assessment based on content characteristics.
	quality := 0.5 // Base quality score.

	// Length factor.
	if len(content) > 100 {
		quality += 0.2
	}

	// Structure factor (presence of formatting).
	if strings.Contains(content, "\n") {
		quality += 0.1
	}

	// Content richness (word variety).
	words := strings.Fields(content)
	if len(words) > 50 {
		quality += 0.15
	}

	state["quality_score"] = quality
	state["assessment_time"] = time.Now()

	return state, nil
}

func (w *documentWorkflow) routeByQuality(ctx context.Context,
	state graph.State) (string, error) {
	threshold := state["quality_threshold"].(float64)
	quality := state["quality_score"].(float64)

	if quality >= threshold {
		return "format_output", nil
	}
	return "enhance", nil
}

func (w *documentWorkflow) formatOutput(ctx context.Context,
	state graph.State) (graph.State, error) {
	var content string

	// Get the final content.
	if enhanced, ok := state["last_agent_output"].(string); ok {
		content = enhanced
	} else if processed, ok := state["processed_content"].(string); ok {
		content = processed
	} else {
		return state, fmt.Errorf("no content found for formatting")
	}

	// Create final formatted output.
	workflowVersion := state["workflow_version"].(string)
	processingMode := state["processing_mode"].(string)
	qualityScore := state["quality_score"].(float64)

	finalOutput := map[string]interface{}{
		"content":          content,
		"workflow_version": workflowVersion,
		"processing_mode":  processingMode,
		"quality_score":    qualityScore,
		"completed_at":     time.Now(),
		"status":           "success",
	}

	state["final_output"] = finalOutput
	return state, nil
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
		level = "simple"
		score = 0.3
	} else if wordCount < 200 {
		level = "moderate"
		score = 0.6
	} else {
		level = "complex"
		score = 0.9
	}

	return complexityResult{
		Level:         level,
		Score:         score,
		WordCount:     wordCount,
		SentenceCount: sentenceCount,
	}
}

func (w *documentWorkflow) extractThemes(args themeArgs) themeResult {
	text := strings.ToLower(args.Text)

	// Simple theme extraction based on keyword frequency.
	themes := []string{}

	// Business themes.
	if strings.Contains(text, "business") || strings.Contains(text, "market") {
		themes = append(themes, "business")
	}

	// Technical themes.
	if strings.Contains(text, "technical") || strings.Contains(text, "technology") {
		themes = append(themes, "technology")
	}

	// Academic themes.
	if strings.Contains(text, "research") || strings.Contains(text, "study") {
		themes = append(themes, "academic")
	}

	if len(themes) == 0 {
		themes = append(themes, "general")
	}

	return themeResult{
		Themes:     themes,
		Confidence: 0.7,
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
		fmt.Println(strings.Repeat("-", 40))

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
	fmt.Print("🤖 Workflow: ")

	var (
		fullContent     string
		workflowStarted bool
		stageCount      int
	)

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]

			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !workflowStarted {
					workflowStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// Track workflow stages.
		if event.Author == graph.AuthorGraphExecutor {
			stageCount++
			if stageCount > 1 && len(event.Response.Choices) > 0 {
				fmt.Printf("\n🔄 Stage %d: %s\n", stageCount,
					event.Response.Choices[0].Message.Content)
			}
		}

		// Check if this is the final event.
		if event.Done && !w.isIntermediateEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// isIntermediateEvent checks if an event is intermediate (not final).
func (w *documentWorkflow) isIntermediateEvent(event *event.Event) bool {
	// Check if this is a workflow stage event.
	if event.Author == graph.AuthorGraphExecutor && !event.Response.Done {
		return true
	}

	// Check if this is a tool response.
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	return false
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

type themeArgs struct {
	Text string `json:"text" description:"Text to extract themes from"`
}

type themeResult struct {
	Themes     []string `json:"themes"`
	Confidence float64  `json:"confidence"`
}

// Helper functions for creating pointers to primitive types.
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
