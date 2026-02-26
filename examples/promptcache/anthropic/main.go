//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates Anthropic prompt caching with three independent controls.
// The example is structured in three phases to clearly show each cache option's effect:
//
//	Phase 1: System prompt caching only - stable system instructions
//	Phase 2: System + Tools caching - adding tool definitions caching
//	Phase 3: System + Tools + Messages caching - multi-turn dynamic breakpoint
//
// Usage:
//
//	export ANTHROPIC_API_KEY=sk-ant-xxx
//	go run main.go
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

1. Sorting: Quick Sort, Merge Sort, Heap Sort, Radix Sort
2. Search: Binary Search, DFS, BFS, A* Search
3. Graph: Dijkstra, Bellman-Ford, Floyd-Warshall, Kruskal/Prim
4. Dynamic Programming: Memoization, Tabulation, Knapsack, LCS, Edit Distance

You have access to a calculator tool and a time tool. Use them when users ask for mathematical calculations or current time.
Always use the calculator tool for any arithmetic operations to ensure accuracy.`
}

// CalculatorInput represents input parameters for the calculator tool.
type CalculatorInput struct {
	Operation string  `json:"operation" jsonschema:"description=The operation to perform: add, subtract, multiply, divide, sqrt, power"`
	A         float64 `json:"a" jsonschema:"description=First operand"`
	B         float64 `json:"b,omitempty" jsonschema:"description=Second operand (optional for sqrt)"`
}

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
				return fmt.Sprintf("Error: Unknown operation '%s'", input.Operation), nil
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

// turnUsage records the cache metrics for a single request turn.
type turnUsage struct {
	Turn                int
	Phase               string
	Query               string
	InputTokens         int // new tokens (not cached)
	CacheReadTokens     int // tokens served from cache
	CacheCreationTokens int // tokens written to cache
	Elapsed             time.Duration
}

func (t *turnUsage) totalInput() int {
	return t.InputTokens + t.CacheReadTokens
}

func (t *turnUsage) cacheHitRate() float64 {
	total := t.totalInput()
	if total == 0 {
		return 0
	}
	return float64(t.CacheReadTokens) / float64(total) * 100
}

// printTurnResult prints the result for a single turn.
func printTurnResult(tu *turnUsage, response string) {
	display := response
	if len(display) > 150 {
		display = display[:150] + "..."
	}
	fmt.Printf("   Response: %s\n", display)
	fmt.Printf("   Time: %v\n", tu.Elapsed)
	total := tu.totalInput()
	fmt.Printf("   Tokens - total_input: %d, new: %d, cache_read: %d, cache_creation: %d\n",
		total, tu.InputTokens, tu.CacheReadTokens, tu.CacheCreationTokens)
	if tu.CacheReadTokens > 0 {
		fmt.Printf("   Cache hit rate: %.1f%%\n", tu.cacheHitRate())
	}
}

// printPhaseStats prints aggregate stats for a phase.
func printPhaseStats(usages []*turnUsage) {
	var totalInput, totalCacheRead, totalCacheCreation, turns int
	for _, u := range usages {
		totalInput += u.InputTokens
		totalCacheRead += u.CacheReadTokens
		totalCacheCreation += u.CacheCreationTokens
		turns++
	}
	fmt.Println(strings.Repeat("-", 50))
	fmt.Printf("Phase stats: %d turns, total_new=%d, cache_read=%d, cache_creation=%d\n",
		turns, totalInput, totalCacheRead, totalCacheCreation)
	total := totalInput + totalCacheRead
	if total > 0 && totalCacheRead > 0 {
		rate := float64(totalCacheRead) / float64(total) * 100
		savings := float64(totalCacheRead) * 0.9 / float64(total) * 100
		fmt.Printf("Overall cache hit rate: %.1f%%, estimated savings: %.1f%%\n", rate, savings)
	}
	fmt.Println()
}

// runTurn executes one turn and collects usage metrics.
func runTurn(ctx context.Context, r runner.Runner, userID, sessionID, query string) (string, *model.Usage, error) {
	message := model.NewUserMessage(query)
	eventChan, err := r.Run(ctx, userID, sessionID, message)
	if err != nil {
		return "", nil, err
	}

	var finalResponse string
	var usage *model.Usage

	for evt := range eventChan {
		if evt.Error != nil {
			fmt.Printf("   Event error: %s\n", evt.Error.Message)
			continue
		}
		if len(evt.Response.Choices) > 0 {
			if len(evt.Response.Choices[0].Message.ToolCalls) > 0 {
				for _, tc := range evt.Response.Choices[0].Message.ToolCalls {
					fmt.Printf("   [tool call] %s\n", tc.Function.Name)
				}
			}
			if content := evt.Response.Choices[0].Message.Content; content != "" {
				finalResponse = content
			}
		}
		if evt.Response.Usage != nil {
			usage = evt.Response.Usage
		}
		if evt.IsFinalResponse() {
			break
		}
	}
	return finalResponse, usage, nil
}

// extractUsage extracts cache-related metrics from model.Usage.
func extractUsage(usage *model.Usage) (inputTokens, cacheRead, cacheCreation int) {
	if usage == nil {
		return
	}
	inputTokens = usage.PromptTokens
	cacheRead = usage.PromptTokensDetails.CachedTokens
	// CacheCreationTokens is reported via PromptTokens when cache is first created.
	// Anthropic SDK reports it separately; we capture it from the raw usage if available.
	// In the current model.Usage mapping, cache_creation_input_tokens is not directly exposed
	// as a separate field, but the relationship is:
	//   total billed input = input_tokens + cache_read + cache_creation
	// For display purposes, if no cache read and inputTokens is large, that likely includes creation.
	return
}

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		fmt.Println("Please set ANTHROPIC_API_KEY environment variable")
		return
	}

	fmt.Println("=== Anthropic Prompt Cache - Three Phase Verification ===")
	fmt.Println()
	fmt.Println("Three independent cache options:")
	fmt.Println("  WithCacheSystemPrompt(true) - cache stable system instructions")
	fmt.Println("  WithCacheTools(true)         - cache tool definitions")
	fmt.Println("  WithCacheMessages(true)      - cache conversation history (dynamic breakpoint)")
	fmt.Println()
	fmt.Println("Anthropic cache key facts:")
	fmt.Println("  - Minimum 1024 tokens required for a cache breakpoint")
	fmt.Println("  - Cache read: 90% cheaper than normal input")
	fmt.Println("  - Cache creation: 25% extra cost (one-time)")
	fmt.Println("  - Cache TTL: ~5 minutes")
	fmt.Println()

	ctx := context.Background()
	tools := []tool.Tool{createCalculatorTool(), createTimeTool()}
	longSystemPrompt := generateLongSystemPrompt()
	maxTokens := 1024

	// ================================================================
	// Phase 1: System prompt caching only
	// ================================================================
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Phase 1: WithCacheSystemPrompt(true) ONLY")
	fmt.Println("  - System prompt (~1200 tokens) gets a cache breakpoint")
	fmt.Println("  - Tools and messages are NOT cached")
	fmt.Println("  - Expected: Turn 1 creates cache, Turn 2 reads from cache")
	fmt.Println(strings.Repeat("=", 60))

	llm1 := anthropic.New("claude-4-5-sonnet-20250929",
		anthropic.WithAPIKey(apiKey),
		anthropic.WithHeaders(map[string]string{"Venus-Sticky-Routing": "token"}),
		anthropic.WithCacheSystemPrompt(true),
	)
	agent1 := llmagent.New("phase1-agent",
		llmagent.WithModel(llm1),
		llmagent.WithInstruction(longSystemPrompt),
		llmagent.WithDescription("Phase 1: system prompt cache only"),
		llmagent.WithTools(tools),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: &maxTokens,
		}),
	)
	session1 := inmemory.NewSessionService()
	r1 := runner.NewRunner("phase1", agent1, runner.WithSessionService(session1))
	defer r1.Close()

	sid1 := fmt.Sprintf("phase1-%d", time.Now().Unix())
	phase1Queries := []struct {
		query string
		desc  string
	}{
		{"What is the singleton design pattern? One sentence answer.", "Turn 1: cache creation for system prompt"},
		{"What is the factory method pattern? One sentence answer.", "Turn 2: system prompt should hit cache"},
	}

	var phase1Usages []*turnUsage
	for i, q := range phase1Queries {
		fmt.Printf("\n[Turn %d] %s\n", i+1, q.desc)
		fmt.Printf("   Query: %s\n", q.query)

		start := time.Now()
		resp, usage, err := runTurn(ctx, r1, "user1", sid1, q.query)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}

		tu := &turnUsage{Turn: i + 1, Phase: "Phase1", Query: q.query, Elapsed: elapsed}
		if usage != nil {
			tu.InputTokens = usage.PromptTokens
			tu.CacheReadTokens = usage.PromptTokensDetails.CachedTokens
		}
		phase1Usages = append(phase1Usages, tu)
		printTurnResult(tu, resp)
		time.Sleep(1 * time.Second)
	}
	printPhaseStats(phase1Usages)

	// ================================================================
	// Phase 2: System + Tools caching
	// ================================================================
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Phase 2: WithCacheSystemPrompt(true) + WithCacheTools(true)")
	fmt.Println("  - Both system prompt and tool definitions get cache breakpoints")
	fmt.Println("  - Messages are NOT cached")
	fmt.Println("  - Expected: more tokens cached (system + tools prefix)")
	fmt.Println(strings.Repeat("=", 60))

	llm2 := anthropic.New("claude-4-5-sonnet-20250929",
		anthropic.WithAPIKey(apiKey),
		anthropic.WithHeaders(map[string]string{"Venus-Sticky-Routing": "token"}),
		anthropic.WithCacheSystemPrompt(true),
		anthropic.WithCacheTools(true),
	)
	agent2 := llmagent.New("phase2-agent",
		llmagent.WithModel(llm2),
		llmagent.WithInstruction(longSystemPrompt),
		llmagent.WithDescription("Phase 2: system + tools cache"),
		llmagent.WithTools(tools),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: &maxTokens,
		}),
	)
	session2 := inmemory.NewSessionService()
	r2 := runner.NewRunner("phase2", agent2, runner.WithSessionService(session2))
	defer r2.Close()

	sid2 := fmt.Sprintf("phase2-%d", time.Now().Unix())
	phase2Queries := []struct {
		query string
		desc  string
	}{
		{"Calculate 123 * 456", "Turn 1: cache creation (system + tools)"},
		{"Calculate 789 + 321", "Turn 2: system + tools should hit cache"},
	}

	var phase2Usages []*turnUsage
	for i, q := range phase2Queries {
		fmt.Printf("\n[Turn %d] %s\n", i+1, q.desc)
		fmt.Printf("   Query: %s\n", q.query)

		start := time.Now()
		resp, usage, err := runTurn(ctx, r2, "user1", sid2, q.query)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}

		tu := &turnUsage{Turn: i + 1, Phase: "Phase2", Query: q.query, Elapsed: elapsed}
		if usage != nil {
			tu.InputTokens = usage.PromptTokens
			tu.CacheReadTokens = usage.PromptTokensDetails.CachedTokens
		}
		phase2Usages = append(phase2Usages, tu)
		printTurnResult(tu, resp)
		time.Sleep(1 * time.Second)
	}
	printPhaseStats(phase2Usages)

	// ================================================================
	// Phase 3: All three - System + Tools + Messages (dynamic breakpoint)
	// ================================================================
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Phase 3: All caching enabled (System + Tools + Messages)")
	fmt.Println("  - WithCacheMessages(true) adds a dynamic cache breakpoint")
	fmt.Println("    at the LAST assistant message in conversation history.")
	fmt.Println()
	fmt.Println("  How dynamic message caching works:")
	fmt.Println("    Turn 1: [user] -> no assistant msg yet, only system+tools cached")
	fmt.Println("    Turn 2: [user, asst, user] -> breakpoint at asst (index 1)")
	fmt.Println("    Turn 3: [user, asst, user, asst, user] -> breakpoint moves to asst (index 3)")
	fmt.Println("    Each turn, the breakpoint moves forward to cover more history.")
	fmt.Println()
	fmt.Println("  This means:")
	fmt.Println("    - Turn 1: cache miss (first request, creating cache)")
	fmt.Println("    - Turn 2: cache HIT on system+tools, new cache created at asst msg")
	fmt.Println("    - Turn 3: cache HIT on system+tools+msgs up to prev asst msg")
	fmt.Println("    - Turn N: increasingly more tokens served from cache")
	fmt.Println(strings.Repeat("=", 60))

	llm3 := anthropic.New("claude-4-5-sonnet-20250929",
		anthropic.WithAPIKey(apiKey),
		anthropic.WithHeaders(map[string]string{"Venus-Sticky-Routing": "token"}),
		anthropic.WithCacheSystemPrompt(true),
		anthropic.WithCacheTools(true),
		anthropic.WithCacheMessages(true),
	)
	agent3 := llmagent.New("phase3-agent",
		llmagent.WithModel(llm3),
		llmagent.WithInstruction(longSystemPrompt),
		llmagent.WithDescription("Phase 3: full cache with dynamic message breakpoint"),
		llmagent.WithTools(tools),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens: &maxTokens,
		}),
	)
	session3 := inmemory.NewSessionService()
	r3 := runner.NewRunner("phase3", agent3, runner.WithSessionService(session3))
	defer r3.Close()

	sid3 := fmt.Sprintf("phase3-%d", time.Now().Unix())

	// 6 turns to clearly show the dynamic breakpoint moving forward
	phase3Queries := []struct {
		query string
		desc  string
	}{
		{
			"What is the observer pattern? One sentence.",
			"Turn 1: no history yet, cache creation (system+tools)",
		},
		{
			"Please use the calculator tool to add 12345 and 67890.",
			"Turn 2: system+tools from cache, breakpoint set at Turn 1's assistant msg",
		},
		{
			"Please use the get_current_time tool to tell me the current time in UTC.",
			"Turn 3: system+tools+Turn1-2 history from cache, breakpoint moves to Turn 2's assistant msg",
		},
		{
			"What is the strategy pattern? One sentence.",
			"Turn 4: even more history cached, breakpoint moves to Turn 3's assistant msg",
		},
		{
			"Please use the calculator tool to multiply 999 by 111.",
			"Turn 5: most of the conversation served from cache",
		},
		{
			"Summarize all the design patterns you've mentioned so far in this conversation.",
			"Turn 6: references prior turns - large context mostly from cache",
		},
	}

	var phase3Usages []*turnUsage
	for i, q := range phase3Queries {
		fmt.Printf("\n[Turn %d] %s\n", i+1, q.desc)
		fmt.Printf("   Query: %s\n", q.query)

		start := time.Now()
		resp, usage, err := runTurn(ctx, r3, "user1", sid3, q.query)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("   Error: %v\n", err)
			continue
		}

		tu := &turnUsage{Turn: i + 1, Phase: "Phase3", Query: q.query, Elapsed: elapsed}
		if usage != nil {
			tu.InputTokens = usage.PromptTokens
			tu.CacheReadTokens = usage.PromptTokensDetails.CachedTokens
		}
		phase3Usages = append(phase3Usages, tu)
		printTurnResult(tu, resp)
		time.Sleep(1 * time.Second)
	}
	printPhaseStats(phase3Usages)

	// ================================================================
	// Final Summary: Compare all three phases
	// ================================================================
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("FINAL COMPARISON")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("%-10s %-8s %-12s %-12s %-10s\n", "Phase", "Turns", "CacheRead", "NewTokens", "HitRate")
	fmt.Println(strings.Repeat("-", 55))

	allPhases := []struct {
		name   string
		usages []*turnUsage
	}{
		{"Phase1", phase1Usages},
		{"Phase2", phase2Usages},
		{"Phase3", phase3Usages},
	}

	for _, p := range allPhases {
		var totalNew, totalCacheRead int
		for _, u := range p.usages {
			totalNew += u.InputTokens
			totalCacheRead += u.CacheReadTokens
		}
		total := totalNew + totalCacheRead
		rate := 0.0
		if total > 0 {
			rate = float64(totalCacheRead) / float64(total) * 100
		}
		fmt.Printf("%-10s %-8d %-12d %-12d %.1f%%\n",
			p.name, len(p.usages), totalCacheRead, totalNew, rate)
	}

	fmt.Println()
	fmt.Println("Key takeaways:")
	fmt.Println("  Phase 1: Only system prompt cached - baseline savings")
	fmt.Println("  Phase 2: System + tools cached - more tokens covered")
	fmt.Println("  Phase 3: Dynamic message caching - cache grows with conversation")
	fmt.Println("           Breakpoint moves to latest assistant message each turn,")
	fmt.Println("           so cache hit rate increases as conversation gets longer.")
	fmt.Println()
	fmt.Println("When to use each option:")
	fmt.Println("  WithCacheSystemPrompt(true) - system prompt is stable (recommended)")
	fmt.Println("  WithCacheTools(true)         - tools don't change often (recommended)")
	fmt.Println("  WithCacheMessages(true)      - multi-turn conversations with 3+ turns")
	fmt.Println("                                 (25% creation cost per turn, 90% savings on reads)")
}
