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
	ga, err := graphagent.New("knowledge-search-demo", graphCompiled,
		graphagent.WithDescription("RAG agent with Knowledge Search node"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	r := runner.NewRunner("knowledge-search-graph", ga)
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
	fmt.Println("🔍 Knowledge Search Example")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()
	fmt.Println("This example demonstrates a RAG (Retrieval-Augmented Generation) workflow:")
	fmt.Println()
	fmt.Println("  1. Query Rewriter  - Optimizes user question for vector search")
	fmt.Println("  2. Knowledge Search - Retrieves relevant documents from vector store")
	fmt.Println("  3. Answer Generator - Generates answer based on retrieved context")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Println("  - Vector Store: TCVector (configurable via environment variables)")
	fmt.Println("  - Embedder: OpenAI text-embedding-3-small")
	fmt.Println("  - Conditioned Filter: status = 'published'")
	fmt.Println()
	fmt.Println("Environment Variables Required:")
	fmt.Println("  OPENAI_API_KEY      - API key for LLM and Embedder")
	fmt.Println("  OPENAI_BASE_URL     - Base URL for Embedder (optional)")
	fmt.Println("  TCVECTOR_URL        - TCVector server URL")
	fmt.Println("  TCVECTOR_COLLECTION - Collection name")
	fmt.Println("  TCVECTOR_USERNAME   - Username")
	fmt.Println("  TCVECTOR_PASSWORD   - Password")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  <question>  - Ask a question to search the knowledge base")
	fmt.Println("  quit        - Exit the program")
	fmt.Println()
}

func runQuery(ctx context.Context, r runner.Runner, userID, sessionID, input string) error {
	msg := model.NewUserMessage(input)

	events, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var lastResponse string
	for ev := range events {
		if ev.Error != nil {
			fmt.Printf("❌ Event error: %s\n", ev.Error.Message)
			continue
		}

		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
		}

		// Log structured output from nodes
		if ev.StateDelta != nil {
			if raw, ok := ev.StateDelta["node_structured"]; ok {
				var ns map[string]any
				if err := json.Unmarshal(raw, &ns); err == nil {
					// Log query rewriter output
					if nodeOut, ok := ns["query_rewriter"].(map[string]any); ok {
						if op, ok := nodeOut["output_parsed"].(map[string]any); ok {
							if sq, _ := op["search_query"].(string); sq != "" {
								fmt.Printf("\n🔄 [Query Rewriter] search_query=%q\n", sq)
							}
						}
					}

					// Log knowledge search results
					if nodeOut, ok := ns["knowledge_search"].(map[string]any); ok {
						if docs, ok := nodeOut["documents"].([]any); ok {
							fmt.Printf("\n📚 [Knowledge Search] Found %d documents:\n", len(docs))
							for i, doc := range docs {
								if docMap, ok := doc.(map[string]any); ok {
									name, _ := docMap["name"].(string)
									score, _ := docMap["score"].(float64)
									text, _ := docMap["text"].(string)
									if len(text) > 100 {
										text = text[:100] + "..."
									}
									fmt.Printf("   %d. [%.2f] %s: %s\n", i+1, score, name, text)
								}
							}
						}
					}
				}
			}
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
