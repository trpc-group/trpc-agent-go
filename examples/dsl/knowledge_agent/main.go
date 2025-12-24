package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Import to register builtin components
	dslvalidator "trpc.group/trpc-go/trpc-agent-go/dsl/validator"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	if err := runInteractive(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runInteractive() error {
	ctx := context.Background()

	// Load graph definition
	data, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}
	parser := dsl.NewParser()
	graphDef, err := parser.Parse(data)
	if err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}
	fmt.Printf("✅ Graph loaded: %s\n", graphDef.Name)

	// Validate graph definition
	validator := dslvalidator.New()
	if err := validator.Validate(graphDef); err != nil {
		return fmt.Errorf("graph validation failed: %w", err)
	}
	fmt.Println("✅ Graph validated successfully")

	// Compile
	comp := compiler.New(
		compiler.WithAllowEnvSecrets(true),
	)

	graphCompiled, err := comp.Compile(graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")

	// Create graph agent
	ga, err := graphagent.New("knowledge-agent-demo", graphCompiled,
		graphagent.WithDescription("Agent with knowledge_search tool (agentic mode)"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	r := runner.NewRunner("knowledge-agent-graph", ga)
	defer r.Close()

	printUsage()

	userID := "user"
	sessionID := "session-1"

	reader := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !reader.Scan() {
			break
		}
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}

		if line == "quit" || line == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := runQuery(ctx, r, userID, sessionID, line); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}

	return reader.Err()
}

func printUsage() {
	fmt.Println()
	fmt.Println("🔍 Knowledge Agent Example (Tool Mode)")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()
	fmt.Println("This example demonstrates an Agent with knowledge_search as a tool:")
	fmt.Println()
	fmt.Println("  - The Agent autonomously decides when to call knowledge_search")
	fmt.Println("  - Supports conditioned_filter for static metadata filtering")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Println("  - Vector Store: TCVector")
	fmt.Println("  - Embedder: OpenAI text-embedding-3-small")
	fmt.Println("  - conditioned_filter: category IN ['machine_learning', 'programming'] AND format = 'markdown'")
	fmt.Println()
	fmt.Println("Environment Variables Required:")
	fmt.Println("  OPENAI_API_KEY      - API key for LLM")
	fmt.Println("  OPENAI_BASE_URL     - Base URL for Embedder")
	fmt.Println("  TCVECTOR_URL        - TCVector server URL")
	fmt.Println("  TCVECTOR_COLLECTION - Collection name")
	fmt.Println("  TCVECTOR_USERNAME   - Username")
	fmt.Println("  TCVECTOR_PASSWORD   - Password")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  <question>  - Ask a question (Agent will decide if knowledge search is needed)")
	fmt.Println("  quit        - Exit the program")
	fmt.Println()
}

func runQuery(ctx context.Context, r runner.Runner, userID, sessionID, input string) error {
	msg := model.NewUserMessage(input)

	events, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	fmt.Print("🤖 Assistant: ")

	var (
		lastResponse      string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for ev := range events {
		if ev.Error != nil {
			fmt.Printf("\n❌ Event error: %s\n", ev.Error.Message)
			continue
		}

		// Handle tool calls from Response (non-streaming complete response)
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]

			// Check for tool calls
			if len(choice.Message.ToolCalls) > 0 {
				toolCallsDetected = true
				if assistantStarted {
					fmt.Println()
				}
				fmt.Printf("\n🔧 Tool calls initiated:\n")
				for _, tc := range choice.Message.ToolCalls {
					fmt.Printf("   • %s (ID: %s)\n", tc.Function.Name, tc.ID)
					if len(tc.Function.Arguments) > 0 {
						fmt.Printf("     Args: %s\n", string(tc.Function.Arguments))
					}
				}
				fmt.Printf("\n🔄 Executing tools...\n")
				continue
			}

			// Check for tool responses
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				content := choice.Message.Content
				if len(content) > 300 {
					content = content[:300] + "... (truncated)"
				}
				fmt.Printf("✅ Tool response (ID: %s): %s\n", choice.Message.ToolID, strings.TrimSpace(content))
				continue
			}
		}

		// Handle streaming content from Choices
		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]

			// Print streaming content
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}

			// Print tool calls from streaming
			for _, tc := range choice.Delta.ToolCalls {
				if tc.Function.Name != "" {
					toolCallsDetected = true
					if assistantStarted {
						fmt.Println()
					}
					fmt.Printf("\n🔧 [Tool Call] %s (id: %s)\n", tc.Function.Name, tc.ID)
				}
				if len(tc.Function.Arguments) > 0 {
					fmt.Printf("   Arguments: %s\n", string(tc.Function.Arguments))
				}
			}

			// Print tool results from streaming
			if choice.Delta.Role == model.RoleTool && choice.Delta.ToolID != "" {
				content := choice.Delta.Content
				if len(content) > 300 {
					content = content[:300] + "... (truncated)"
				}
				fmt.Printf("✅ Tool response (ID: %s): %s\n", choice.Delta.ToolID, strings.TrimSpace(content))
			}
		}

		// Log state delta
		if ev.StateDelta != nil {
			if lastRespBytes, ok := ev.StateDelta["last_response"]; ok {
				var resp string
				if err := json.Unmarshal(lastRespBytes, &resp); err == nil {
					lastResponse = resp
				}
			}
		}
	}

	fmt.Println()
	if strings.TrimSpace(lastResponse) != "" {
		fmt.Printf("🤖 Final Response:\n%s\n", lastResponse)
	}

	return nil
}
