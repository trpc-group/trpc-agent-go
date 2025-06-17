package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
	"trpc.group/trpc-go/trpc-agent-go/examples/tools"
)

// Command line flags
var (
	openaiURL     = flag.String("openai-url", "https://api.openai.com/v1", "OpenAI API base URL")
	modelName     = flag.String("model-name", "gpt-3.5-turbo", "Model name to use")
	modelProvider = flag.String("model-provider", "openai", "Model provider")
	apiKeyEnv     = flag.String("api-key-env", "OPENAI_API_KEY", "Environment variable for API key")
	interactive   = flag.Bool("interactive", false, "Run in interactive mode")
)

func createRealModel() (model.Model, error) {
	apiKey := os.Getenv(*apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("API key not found in environment variable %s", *apiKeyEnv)
	}

	switch *modelProvider {
	case "openai":
		defaultOptions := model.DefaultOptions()
		defaultOptions.PresencePenalty = 1
		openaiModel := model.NewOpenAIStreamingModel(*modelName,
			model.WithOpenAIAPIKey(apiKey),
			model.WithOpenAIBaseURL(*openaiURL),
			model.WithOpenAIDefaultOptions(defaultOptions),
		)
		return openaiModel, nil
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", *modelProvider)
	}
}

func runBasicTests(ctx context.Context, llmAgent agent.Agent) {
	fmt.Println("=== Test 1: Basic Text Response ===")

	textContent := event.NewTextContent("Hello! Please introduce yourself in one sentence.")
	fmt.Printf("Calling LLM with text: %s\n", textContent.GetText())

	response, err := llmAgent.Process(ctx, textContent)
	if err != nil {
		log.Printf("❌ Basic text test failed: %v", err)
		fmt.Printf("Debug: Error details: %v\n", err)
		return
	}

	fmt.Printf("Input: %s\n", textContent.GetText())
	fmt.Printf("Output: %s\n", response.GetText())
	fmt.Println("✅ Basic text response successful")

	fmt.Println("\n=== Test 2: Async Processing ===")

	asyncContent := event.NewTextContent("Tell me a very short joke.")
	eventCh, err := llmAgent.ProcessAsync(ctx, asyncContent)
	if err != nil {
		log.Printf("❌ Async test failed: %v", err)
		return
	}

	fmt.Printf("Input: %s\n", asyncContent.GetText())
	fmt.Print("Output: ")
	for event := range eventCh {
		if event.Content != nil && event.Content.GetText() != "" {
			fmt.Print(event.Content.GetText())
		}
	}
	fmt.Println()
	fmt.Println("✅ Async processing successful")

	fmt.Println("\n=== Test 3: Tool Usage (Basic) ===")
	fmt.Println("Note: Full tool integration will be enhanced in future iterations")

	toolRequestContent := event.NewTextContent("Please calculate 123 + 456.")
	toolEventCh, err := llmAgent.ProcessAsync(ctx, toolRequestContent)
	if err != nil {
		log.Printf("❌ Tool test failed: %v", err)
		return
	}

	fmt.Printf("Input: %s\n", toolRequestContent.GetText())
	fmt.Print("Processing: ")

	for event := range toolEventCh {
		if event.Content != nil {
			if text := event.Content.GetText(); text != "" {
				fmt.Printf("\n💬 Response: %s", text)
			}
		}
	}

	fmt.Println()
	fmt.Println("✅ Basic processing successful (tool integration is work in progress)")
}

func runInteractiveMode(ctx context.Context, llmAgent agent.Agent) {
	fmt.Println("\n=== Interactive Mode ===")
	fmt.Println("Type 'exit' to quit")

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}

		input := scanner.Text()
		if input == "exit" {
			break
		}

		if input == "" {
			continue
		}

		// Process user input
		inputContent := event.NewTextContent(input)
		eventCh, err := llmAgent.ProcessAsync(ctx, inputContent)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Print("Assistant: ")
		for event := range eventCh {
			if event.Content != nil {
				if text := event.Content.GetText(); text != "" {
					fmt.Print(text)
				}
			}
		}
		fmt.Println()
	}

	fmt.Println("Goodbye!")
}

func main() {
	flag.Parse()

	ctx := context.Background()

	// Create the model
	llmModel, err := createRealModel()
	if err != nil {
		log.Fatalf("Failed to create model: %v", err)
	}

	// Create tools using the BaseTool interface
	calculator := tools.NewSimpleCalculatorTool()

	// Create agent config with new BaseTool interface
	agentConfig := agent.LLMAgentConfig{
		Name:         "Calculator Agent",
		Description:  "An agent that can perform calculations",
		Model:        llmModel,
		SystemPrompt: "You are a helpful assistant that can perform calculations. Use the calculator tool when users ask for math operations.",
		Tools:        []tool.BaseTool{calculator},
	}

	// Create the agent
	llmAgent, err := agent.NewLLMAgent(agentConfig)
	if err != nil {
		log.Fatalf("Failed to create LLM agent: %v", err)
	}

	fmt.Printf("Created LLM agent: %s\n", llmAgent.Name())
	fmt.Printf("Model: %s (%s)\n", llmAgent.GetModel().Name(), llmAgent.GetModel().Provider())
	fmt.Printf("Has tools: %v\n", llmAgent.HasTools())

	if *interactive {
		runInteractiveMode(ctx, llmAgent)
	} else {
		runBasicTests(ctx, llmAgent)
	}
}
