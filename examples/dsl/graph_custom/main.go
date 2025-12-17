package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Import built-in components
	dslvalidator "trpc.group/trpc-go/trpc-agent-go/dsl/validator"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultModelName = "deepseek-chat"
)

var (
	modelName = flag.String("model", defaultModelName, "Name of the model to use")
	dslFile   = flag.String("dsl", "workflow.json", "Path to DSL JSON file")
	inputDoc  = flag.String("input", "", "Input document to process")
	verbose   = flag.Bool("verbose", false, "Enable verbose logging")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 DSL-Based Document Processing Workflow\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("DSL File: %s\n", *dslFile)
	fmt.Println(strings.Repeat("=", 50))

	// Register custom components
	if err := registerCustomComponents(); err != nil {
		log.Fatalf("Failed to register custom components: %v", err)
	}

	// Load DSL
	dslContent, err := os.ReadFile(*dslFile)
	if err != nil {
		log.Fatalf("Failed to read DSL file: %v", err)
	}

	// Parse DSL
	parser := dsl.NewParser()
	workflow, err := parser.Parse(dslContent)
	if err != nil {
		log.Fatalf("Failed to parse DSL: %v", err)
	}

	// Get configuration from environment
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}

	applyModelOverrides(workflow, *modelName, baseURL)

	// Validate DSL
	validator := dslvalidator.New()
	if err := validator.Validate(workflow); err != nil {
		log.Fatalf("DSL validation failed: %v", err)
	}

	// Create Tool Registry and register tools
	toolRegistry := registry.NewToolRegistry()
	complexityTool := CreateAnalyzeComplexityTool()
	toolRegistry.MustRegister("analyze_complexity", complexityTool)
	fmt.Printf("✅ Tool registered in ToolRegistry: analyze_complexity\n")

	// Compile DSL to Graph with Tool Registry
	comp := compiler.New(
		compiler.WithAllowEnvSecrets(true),
		compiler.WithToolProvider(toolRegistry),
	)

	compiledGraph, err := comp.Compile(workflow)
	if err != nil {
		log.Fatalf("Failed to compile DSL: %v", err)
	}

	// Create GraphAgent
	// Note: We do NOT pass model or tools in initial state!
	// They are resolved from registries during compilation.
	graphAgent, err := graphagent.New("document-processor", compiledGraph,
		graphagent.WithDescription("DSL-based document processing workflow"),
	)
	if err != nil {
		log.Fatalf("Failed to create graph agent: %v", err)
	}

	// Create session service
	sessionService := inmemory.NewSessionService()

	// Create runner
	appRunner := runner.NewRunner(
		"dsl-document-workflow",
		graphAgent,
		runner.WithSessionService(sessionService),
	)
	defer appRunner.Close()

	// Get input document
	var inputText string
	if *inputDoc != "" {
		inputText = *inputDoc
	} else {
		// Default sample document tuned to better exercise complexity analysis
		// (word count > 50 so the workflow is more likely to route to non-simple branches).
		inputText = "This sample document is designed to test the full document-processing workflow. " +
			"It contains several sentences that describe different aspects of the system, " +
			"including how it analyzes complexity, routes based on the result, and then either enhances " +
			"or summarizes the original content depending on the detected difficulty level."
	}

	// Run the workflow
	ctx := context.Background()
	userID := "user"
	sessionID := "session-1"

	message := model.NewUserMessage(inputText)
	eventChan, err := appRunner.Run(ctx, userID, sessionID, message)
	if err != nil {
		log.Fatalf("Failed to run workflow: %v", err)
	}

	// Process events
	fmt.Println("\n🔄 Processing workflow...")
	var finalOutput string
	for ev := range eventChan {
		if ev.Error != nil {
			fmt.Printf("❌ Error: %s\n", ev.Error.Message)
			continue
		}

		if *verbose && ev.Response != nil {
			fmt.Printf("� Event: %s\n", ev.Response.Object)
		}

		// Capture final response
		if ev.Done && ev.StateDelta != nil {
			if lastRespBytes, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var respStr string
				if err := json.Unmarshal(lastRespBytes, &respStr); err == nil {
					finalOutput = respStr
				}
			}
		}
	}

	// Print final output
	if finalOutput != "" {
		fmt.Printf("\n%s\n", finalOutput)
	} else {
		fmt.Printf("\n✅ Workflow completed successfully!\n")
	}
}

func applyModelOverrides(graphDef *dsl.Graph, modelName string, baseURL string) {
	if graphDef == nil {
		return
	}

	modelName = strings.TrimSpace(modelName)
	baseURL = strings.TrimSpace(baseURL)
	if modelName == "" && baseURL == "" {
		return
	}

	for i := range graphDef.Nodes {
		cfg := graphDef.Nodes[i].EngineNode.Config
		if cfg == nil {
			continue
		}
		specRaw, ok := cfg["model_spec"]
		if !ok || specRaw == nil {
			continue
		}
		spec, ok := specRaw.(map[string]any)
		if !ok {
			continue
		}
		if modelName != "" {
			spec["model_name"] = modelName
		}
		if baseURL != "" {
			spec["base_url"] = baseURL
		}
		cfg["model_spec"] = spec
	}
}

func registerCustomComponents() error {
	// Register custom components
	registry.MustRegister(&PreprocessDocumentComponent{})
	registry.MustRegister(&RouteComplexityComponent{})
	registry.MustRegister(&ComplexityConditionComponent{})
	registry.MustRegister(&FormatOutputComponent{})
	registry.MustRegister(&AnalyzeComplexityToolComponent{})

	fmt.Println("✅ Custom components registered:")
	fmt.Println("   - custom.preprocess_document")
	fmt.Println("   - custom.route_complexity")
	fmt.Println("   - custom.complexity_condition")
	fmt.Println("   - custom.format_output")
	fmt.Println("   - custom.analyze_complexity_tool")
	fmt.Println()

	return nil
}
