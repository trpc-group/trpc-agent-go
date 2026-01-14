//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates automatic optimal prompt caching with Anthropic Claude.
// This example shows that with the new auto-cache feature, Anthropic behaves like OpenAI -
// just enable caching with one switch and the framework handles everything automatically.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// generateLongSystemPrompt generates a system prompt with at least 1024 tokens.
// Anthropic requires minimum 1024 tokens for prompt caching to work.
func generateLongSystemPrompt() string {
	return `You are an expert AI assistant specialized in software engineering, data analysis, and problem-solving.

Your expertise includes:
- Programming languages: Go, Python, JavaScript, TypeScript, Java, C++, Rust, Ruby, PHP, Swift, Kotlin
- Frameworks: tRPC, gRPC, React, Vue, Angular, Django, Flask, Spring Boot, Express.js, FastAPI, Gin, Echo
- Databases: MySQL, PostgreSQL, MongoDB, Redis, Cassandra, DynamoDB, ElasticSearch, ClickHouse
- Cloud platforms: AWS, Azure, GCP, Kubernetes, Docker, Terraform, Ansible
- Best practices: Clean code, SOLID principles, design patterns, TDD, BDD, DDD
- Tools: Git, Docker, CI/CD, monitoring and observability, Prometheus, Grafana, Jaeger

When responding:
1. Provide clear, concise explanations
2. Include code examples when relevant
3. Consider performance and scalability
4. Follow best practices and industry standards
5. Explain trade-offs when multiple approaches exist

Your goal is to help developers build robust, efficient, and maintainable systems.

## Detailed Technical Guidelines

### Code Quality Standards
- Write self-documenting code with meaningful variable and function names
- Keep functions small and focused on a single responsibility
- Use consistent formatting and follow language-specific style guides
- Add comments for complex logic, but prefer clear code over excessive comments
- Handle errors gracefully and provide meaningful error messages

### Performance Optimization
- Profile before optimizing - measure don't guess
- Consider time and space complexity for algorithms
- Use appropriate data structures for the use case
- Implement caching strategies where beneficial
- Optimize database queries and use indexes appropriately
- Consider lazy loading and pagination for large datasets

### Security Best Practices
- Never store sensitive data in plain text
- Use parameterized queries to prevent SQL injection
- Implement proper authentication and authorization
- Validate and sanitize all user inputs
- Use HTTPS for all communications
- Keep dependencies updated and scan for vulnerabilities
- Follow the principle of least privilege

### Testing Strategies
- Write unit tests for individual functions and methods
- Create integration tests for component interactions
- Implement end-to-end tests for critical user flows
- Use mocking and stubbing appropriately
- Aim for meaningful test coverage, not just high numbers
- Test edge cases and error conditions

### Architecture Principles
- Design for scalability from the start
- Use microservices when appropriate, but don't over-engineer
- Implement proper logging and monitoring
- Design APIs with versioning in mind
- Use event-driven architecture for loose coupling
- Consider eventual consistency in distributed systems

### DevOps and Deployment
- Automate everything that can be automated
- Use infrastructure as code
- Implement blue-green or canary deployments
- Monitor application health and set up alerts
- Have a rollback strategy for failed deployments
- Document deployment procedures

### Documentation Standards
- Write clear README files for all projects
- Document API endpoints with examples
- Maintain architecture decision records (ADRs)
- Keep documentation up to date with code changes
- Include setup and installation instructions
- Document known issues and workarounds

## Extended Knowledge Base

### Design Patterns
The following design patterns are commonly used in software development:

1. Creational Patterns:
   - Singleton: Ensures a class has only one instance
   - Factory Method: Creates objects without specifying exact class
   - Abstract Factory: Creates families of related objects
   - Builder: Constructs complex objects step by step
   - Prototype: Creates new objects by copying existing ones

2. Structural Patterns:
   - Adapter: Allows incompatible interfaces to work together
   - Bridge: Separates abstraction from implementation
   - Composite: Composes objects into tree structures
   - Decorator: Adds behavior to objects dynamically
   - Facade: Provides simplified interface to complex subsystem
   - Flyweight: Shares common state between objects
   - Proxy: Provides placeholder for another object

3. Behavioral Patterns:
   - Chain of Responsibility: Passes request along chain of handlers
   - Command: Encapsulates request as an object
   - Iterator: Provides way to access elements sequentially
   - Mediator: Reduces coupling between components
   - Memento: Captures and restores object state
   - Observer: Notifies dependents of state changes
   - State: Alters behavior when internal state changes
   - Strategy: Defines family of interchangeable algorithms
   - Template Method: Defines skeleton of algorithm
   - Visitor: Separates algorithm from object structure

### Common Algorithms
Understanding these algorithms is essential for efficient problem-solving:

1. Sorting Algorithms:
   - Quick Sort: O(n log n) average, O(n²) worst
   - Merge Sort: O(n log n) guaranteed
   - Heap Sort: O(n log n) with O(1) space
   - Radix Sort: O(nk) for integers

2. Search Algorithms:
   - Binary Search: O(log n) for sorted arrays
   - Depth-First Search: O(V + E) for graphs
   - Breadth-First Search: O(V + E) for graphs
   - A* Search: Optimal pathfinding with heuristics

3. Graph Algorithms:
   - Dijkstra's: Shortest path in weighted graphs
   - Bellman-Ford: Handles negative weights
   - Floyd-Warshall: All pairs shortest paths
   - Kruskal's/Prim's: Minimum spanning trees

4. Dynamic Programming:
   - Memoization: Top-down approach
   - Tabulation: Bottom-up approach
   - Common problems: Knapsack, LCS, Edit Distance

You have access to a calculator tool and a time tool. Use them when users ask for mathematical calculations or current time.
Always use the calculator tool for any arithmetic operations to ensure accuracy.`
}

// CalculatorInput represents input parameters for the calculator tool.
type CalculatorInput struct {
	Operation string  `json:"operation" jsonschema:"description=The operation to perform: add, subtract, multiply, divide, sqrt, power"`
	A         float64 `json:"a" jsonschema:"description=First operand"`
	B         float64 `json:"b,omitempty" jsonschema:"description=Second operand (optional for sqrt)"`
}

// createCalculatorTool creates a calculator tool for mathematical operations.
func createCalculatorTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, input CalculatorInput) (string, error) {
			var result float64
			switch strings.ToLower(input.Operation) {
			case "add":
				result = input.A + input.B
			case "subtract":
				result = input.A - input.B
			case "multiply":
				result = input.A * input.B
			case "divide":
				if input.B == 0 {
					return "Error: Division by zero", nil
				}
				result = input.A / input.B
			case "sqrt":
				if input.A < 0 {
					return "Error: Cannot calculate square root of negative number", nil
				}
				result = math.Sqrt(input.A)
			case "power":
				result = math.Pow(input.A, input.B)
			default:
				return fmt.Sprintf("Error: Unknown operation '%s'. Supported: add, subtract, multiply, divide, sqrt, power", input.Operation), nil
			}
			return fmt.Sprintf("Result: %.6f", result), nil
		},
		function.WithName("calculator"),
		function.WithDescription("Perform mathematical calculations including basic arithmetic and scientific operations"),
	)
}

// TimeInput represents input for the time tool.
type TimeInput struct {
	Format   string `json:"format,omitempty" jsonschema:"description=Time format: 'full', 'date', 'time', 'unix'"`
	Timezone string `json:"timezone,omitempty" jsonschema:"description=Timezone name like 'UTC', 'America/New_York', 'Asia/Shanghai'"`
}

// createTimeTool creates a tool to get current time.
func createTimeTool() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, input TimeInput) (string, error) {
			loc := time.UTC
			if input.Timezone != "" {
				var err error
				loc, err = time.LoadLocation(input.Timezone)
				if err != nil {
					return fmt.Sprintf("Error: Invalid timezone '%s'", input.Timezone), nil
				}
			}

			now := time.Now().In(loc)
			format := input.Format
			if format == "" {
				format = "full"
			}

			var result string
			switch format {
			case "full":
				result = now.Format("2006-01-02 15:04:05 MST")
			case "date":
				result = now.Format("2006-01-02")
			case "time":
				result = now.Format("15:04:05")
			case "unix":
				result = fmt.Sprintf("%d", now.Unix())
			default:
				result = now.Format(format)
			}

			return fmt.Sprintf("Current time (%s): %s", loc.String(), result), nil
		},
		function.WithName("get_current_time"),
		function.WithDescription("Get the current date and time in various formats and timezones"),
	)
}

// UsageStats tracks token usage across requests.
type UsageStats struct {
	// Anthropic reports tokens separately:
	// - input_tokens: tokens processed (not from cache)
	// - cache_read_input_tokens: tokens read from cache
	// - cache_creation_input_tokens: tokens used to create cache
	// Total input = input_tokens + cache_read_input_tokens
	TotalInputTokens      int // input_tokens (new tokens processed)
	TotalCacheReadTokens  int // cache_read_input_tokens
	TotalRequests         int
	RequestsWithCacheRead int
}

func (u *UsageStats) Add(usage *model.Usage) {
	if usage == nil {
		return
	}
	u.TotalRequests++
	u.TotalInputTokens += usage.PromptTokens // This is input_tokens (not including cache)

	// Anthropic reports cache_read_input_tokens separately
	if usage.PromptTokensDetails.CachedTokens > 0 {
		u.TotalCacheReadTokens += usage.PromptTokensDetails.CachedTokens
		u.RequestsWithCacheRead++
	}
}

func (u *UsageStats) Print() {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("📊 Anthropic Prompt Cache Statistics")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Total Requests:           %d\n", u.TotalRequests)
	fmt.Printf("Requests with Cache Read: %d\n", u.RequestsWithCacheRead)

	// Total input = new tokens + cached tokens
	totalInput := u.TotalInputTokens + u.TotalCacheReadTokens
	fmt.Printf("Total Input Tokens:       %d\n", totalInput)
	fmt.Printf("  - New (processed):      %d\n", u.TotalInputTokens)
	fmt.Printf("  - Cached (read):        %d\n", u.TotalCacheReadTokens)

	if totalInput > 0 {
		cacheRate := float64(u.TotalCacheReadTokens) / float64(totalInput) * 100
		fmt.Printf("Cache Hit Rate:           %.2f%%\n", cacheRate)
	}
	if u.TotalCacheReadTokens > 0 {
		// Anthropic cached tokens are 90% cheaper
		// Cost without cache: totalInput * price
		// Cost with cache: u.TotalInputTokens * price + u.TotalCacheReadTokens * 0.1 * price
		// Savings = (totalInput - (u.TotalInputTokens + u.TotalCacheReadTokens * 0.1)) / totalInput
		costWithoutCache := float64(totalInput)
		costWithCache := float64(u.TotalInputTokens) + float64(u.TotalCacheReadTokens)*0.1
		savings := (costWithoutCache - costWithCache) / costWithoutCache * 100
		fmt.Printf("Estimated Cost Savings:   %.2f%%\n", savings)
	}
	fmt.Println(strings.Repeat("=", 60))
}

func main() {
	// Anthropic SDK supports both ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN
	// ANTHROPIC_API_KEY is the preferred/official one
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN") // fallback
	}
	if apiKey == "" {
		fmt.Println("⚠️  ANTHROPIC_API_KEY not set")
		fmt.Println("Please set ANTHROPIC_API_KEY (or ANTHROPIC_AUTH_TOKEN) environment variable to run this demo")
		return
	}

	fmt.Println("=== Anthropic Auto-Optimal Cache Demo ===")
	fmt.Println()
	fmt.Println("This demo shows the NEW auto-cache feature for Anthropic:")
	fmt.Println("  ✓ Just enable caching with WithEnablePromptCache(true)")
	fmt.Println("  ✓ Framework automatically applies optimal cache strategy")
	fmt.Println("  ✓ Works like OpenAI - no need to configure individual options")
	fmt.Println()
	fmt.Println("Auto-cache strategy (in priority order):")
	fmt.Println("  1. Multi-turn: Cache at last assistant message (covers everything)")
	fmt.Println("  2. With tools: Cache at last tool (covers system + tools)")
	fmt.Println("  3. System only: Cache at system prompt")
	fmt.Println()
	fmt.Println("Anthropic caching benefits:")
	fmt.Println("  - Cached tokens are 90% cheaper (vs 50% for OpenAI)")
	fmt.Println("  - Minimum 1024 tokens required")
	fmt.Println("  - Cache TTL: ~5 minutes")
	fmt.Println()

	ctx := context.Background()

	// Create model - just one switch to enable caching!
	// The framework automatically handles:
	// - System prompt caching
	// - Tools caching
	// - Multi-turn message caching
	llm := anthropic.New("claude-sonnet-4-20250514",
		anthropic.WithAPIKey(apiKey),
		anthropic.WithEnablePromptCache(true), // That's all you need!
	)

	// Create tools
	tools := []tool.Tool{
		createCalculatorTool(),
		createTimeTool(),
	}

	// Create agent with long system prompt for caching
	longSystemPrompt := generateLongSystemPrompt()
	maxTokens := 4096 // Required for Anthropic API
	agentInstance := llmagent.New(
		"anthropic-cache-demo-agent",
		llmagent.WithModel(llm),
		llmagent.WithInstruction(longSystemPrompt),
		llmagent.WithDescription("An AI assistant demonstrating auto-optimal prompt caching"),
		llmagent.WithTools(tools),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    false, // Disable streaming to ensure usage info is available
			MaxTokens: &maxTokens,
		}),
	)

	// Create session service for multi-turn conversation
	sessionService := inmemory.NewSessionService()

	// Create runner
	r := runner.NewRunner(
		"anthropic-cache-demo",
		agentInstance,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	userID := "demo-user"
	sessionID := fmt.Sprintf("cache-session-%d", time.Now().Unix())

	// Track usage statistics
	stats := &UsageStats{}

	// Define test queries - mix of regular questions and tool calls
	testQueries := []struct {
		query       string
		description string
		expectTool  bool
	}{
		{
			query:       "What is the singleton design pattern? Give a brief explanation.",
			description: "Turn 1: Regular question (cache creation)",
			expectTool:  false,
		},
		{
			query:       "Calculate 123 * 456 + 789",
			description: "Turn 2: Tool call - calculator",
			expectTool:  true,
		},
		{
			query:       "What time is it now in UTC?",
			description: "Turn 3: Tool call - time",
			expectTool:  true,
		},
		{
			query:       "What is the factory method pattern? Brief answer.",
			description: "Turn 4: Regular question (expecting cache hit on system+tools+history)",
			expectTool:  false,
		},
		{
			query:       "Calculate the square root of 144",
			description: "Turn 5: Tool call (expecting cache hit)",
			expectTool:  true,
		},
		{
			query:       "Explain the observer pattern briefly.",
			description: "Turn 6: Regular question (expecting cache hit)",
			expectTool:  false,
		},
	}

	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Starting multi-turn conversation with auto-caching...")
	fmt.Println(strings.Repeat("=", 60))

	for i, tq := range testQueries {
		fmt.Printf("\n📝 %s\n", tq.description)
		fmt.Printf("   Query: %s\n", tq.query)
		if tq.expectTool {
			fmt.Println("   [Expecting tool call]")
		}

		start := time.Now()

		// Run the agent
		message := model.NewUserMessage(tq.query)
		eventChan, err := r.Run(ctx, userID, sessionID, message)
		if err != nil {
			fmt.Printf("   ❌ Error: %v\n", err)
			continue
		}

		// Collect response
		var finalResponse string
		var usage *model.Usage
		var toolCalled bool

		for evt := range eventChan {
			// Handle errors
			if evt.Error != nil {
				fmt.Printf("   ⚠️  Event error: %s\n", evt.Error.Message)
				continue
			}

			// Check for tool calls
			if len(evt.Response.Choices) > 0 && len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
				toolCalled = true
				for _, tc := range evt.Response.Choices[0].Message.ToolCalls {
					fmt.Printf("   🔧 Tool call: %s\n", tc.Function.Name)
				}
			}

			// Collect content
			if len(evt.Response.Choices) > 0 {
				content := evt.Response.Choices[0].Message.Content
				if content != "" {
					finalResponse = content
				}
				// Also check delta for streaming
				delta := evt.Response.Choices[0].Delta.Content
				if delta != "" && finalResponse == "" {
					finalResponse = delta
				}
			}

			// Collect usage (usually in final event)
			if evt.Response.Usage != nil {
				usage = evt.Response.Usage
			}

			// Check for final response
			if evt.IsFinalResponse() {
				break
			}
		}

		elapsed := time.Since(start)

		// Print results
		if finalResponse != "" {
			// Truncate long responses
			displayResp := finalResponse
			if len(displayResp) > 200 {
				displayResp = displayResp[:200] + "..."
			}
			fmt.Printf("   💬 Response: %s\n", displayResp)
		}

		if toolCalled {
			fmt.Println("   ✓ Tool was called")
		}

		if usage != nil {
			stats.Add(usage)
			fmt.Printf("   ⏱️  Time: %v\n", elapsed)

			// Anthropic: total input = input_tokens + cache_read_input_tokens
			totalInput := usage.PromptTokens + usage.PromptTokensDetails.CachedTokens
			fmt.Printf("   📊 Total input: %d (new: %d, cached: %d)\n",
				totalInput,
				usage.PromptTokens,
				usage.PromptTokensDetails.CachedTokens)

			if usage.PromptTokensDetails.CachedTokens > 0 {
				cacheRate := float64(usage.PromptTokensDetails.CachedTokens) / float64(totalInput) * 100
				fmt.Printf("   💰 Cache hit rate: %.2f%% (cached tokens are 90%% cheaper!)\n", cacheRate)
			}
		} else {
			fmt.Printf("   ⏱️  Time: %v\n", elapsed)
			fmt.Println("   📊 Usage info not available")
		}

		// Small delay between requests
		time.Sleep(500 * time.Millisecond)

		// Show cache strategy being used
		if i == 0 {
			fmt.Println("\n   ℹ️  First turn: Cache breakpoint set at tools (covers system+tools)")
		} else {
			fmt.Printf("\n   ℹ️  Turn %d: Cache breakpoint moved to last assistant message\n", i+1)
			fmt.Println("       (covers system+tools+all previous messages)")
		}
	}

	// Print final statistics
	stats.Print()

	fmt.Println("\n✅ Demo completed!")
	fmt.Println()
	fmt.Println("Key observations:")
	fmt.Println("• Auto-cache requires just ONE switch: WithEnablePromptCache(true)")
	fmt.Println("• Framework automatically picks optimal cache strategy")
	fmt.Println("• Multi-turn conversations get maximum cache benefit")
	fmt.Println("• Cache hits reduce cost by up to 90% (Anthropic's pricing)")
	fmt.Println()
	fmt.Println("Compare with manual configuration (old way):")
	fmt.Println("  anthropic.WithEnablePromptCache(true)")
	fmt.Println("  anthropic.WithCacheSystemPrompt(true)")
	fmt.Println("  anthropic.WithCacheTools(true)")
	fmt.Println("  anthropic.WithCacheMessages(true)  // NEW!")
	fmt.Println()
	fmt.Println("Now it's as simple as OpenAI - just enable and forget!")
}
