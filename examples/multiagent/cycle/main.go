// Package main demonstrates multi-agent iterative processing using CycleAgent
// with streaming output, session management, and tool calling.
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
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	defaultChannelBufferSize = 256
	maxTokens                = 300 // Reduced for faster, more concise responses
	temperature              = 0.7
	defaultMaxIterations     = 3 // Default max iterations for cycle
)

func main() {
	// Parse command line flags.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	maxIter := flag.Int("max-iterations", defaultMaxIterations, "Maximum number of iterations for the cycle")
	flag.Parse()

	fmt.Printf("🔄 Multi-Agent Cycle Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Max Iterations: %d\n", *maxIter)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: record_score, solution_store\n")
	fmt.Println("Cycle: Generate → Critique → Improve → Repeat")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &cycleChat{
		modelName:     *modelName,
		maxIterations: *maxIter,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Cycle chat failed: %v", err)
	}
}

// cycleChat manages the multi-agent iterative conversation.
type cycleChat struct {
	modelName     string
	maxIterations int
	runner        runner.Runner
	userID        string
	sessionID     string
}

// run starts the interactive chat session.
func (c *cycleChat) run() error {
	ctx := context.Background()

	// Setup the runner with cycle agent.
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive chat.
	return c.startChat(ctx)
}

// setup creates the runner with cycle agent and sub-agents.
func (c *cycleChat) setup(ctx context.Context) error {
	// Create OpenAI model.
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: defaultChannelBufferSize,
	})

	// Create shared tools for the cycle.
	scoreTool := function.NewFunctionTool(
		c.recordScore,
		function.WithName("record_score"),
		function.WithDescription("Record the quality score and decision for the current content"),
	)
	solutionTool := function.NewFunctionTool(
		c.storeSolution,
		function.WithName("solution_store"),
		function.WithDescription("Store and track solution iterations for comparison"),
	)

	// Create generation config.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(maxTokens),
		Temperature: floatPtr(temperature),
		Stream:      true,
	}

	// Create Generate Agent - creates content based on user prompts.
	generateAgent := llmagent.New(
		"generate-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Generates content based on user prompts and improvement feedback"),
		llmagent.WithInstruction("You are a creative content generator. Create high-quality content based on the user's request. If this is a refinement iteration, incorporate the critic's feedback to improve your previous output. Be creative, specific, and engaging. Keep responses concise but complete."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(50),
		llmagent.WithTools([]tool.Tool{solutionTool}), // Can store iterations
	)

	// Create Critic Agent - evaluates content and provides feedback.
	criticAgent := llmagent.New(
		"critic-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Critically evaluates generated content and provides improvement feedback"),
		llmagent.WithInstruction("You are a critical evaluator. Carefully assess the generated content for quality, creativity, completeness, and engagement. Give a score from 0-100 and decide if it needs improvement (scores below 82 need improvement). Always use the record_score tool to formally record your decision. Provide specific, actionable feedback for improvements when needed."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(50),
		llmagent.WithTools([]tool.Tool{scoreTool}),
	)

	// Create quality-based escalation function for the cycle agent.
	qualityEscalationFunc := func(evt *event.Event) bool {
		if evt == nil || evt.Response == nil {
			return false // Continue cycle
		}

		// Check tool responses for quality assessment.
		if len(evt.Response.Choices) > 0 {
			for _, choice := range evt.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					content := choice.Message.Content

					// Check if this is a record_score tool result
					if strings.Contains(content, "record_score") || strings.Contains(content, "needs_improvement") {
						// Stop cycle when needs_improvement is false (quality threshold ≥82 met)
						if strings.Contains(content, "\"needs_improvement\":false") {
							return true // Stop cycle - quality threshold met
						} else if strings.Contains(content, "\"needs_improvement\":true") {
							return false // Continue cycle - needs improvement
						}
					}
				}
			}
		}

		// Default escalation: check for errors.
		if evt.Error != nil {
			return true
		}
		return false // Continue cycle
	}

	// Create Cycle Agent with sub-agents and injectable escalation logic.
	maxIterPtr := &c.maxIterations
	cycleAgent := cycleagent.New(cycleagent.Options{
		Name:              "multi-agent-cycle",
		SubAgents:         []agent.Agent{generateAgent, criticAgent},
		Tools:             []tool.Tool{scoreTool, solutionTool},
		MaxIterations:     maxIterPtr,
		ChannelBufferSize: defaultChannelBufferSize,
		EscalationFunc:    qualityEscalationFunc, // Injectable quality-based escalation
	})

	// Create runner with the cycle agent.
	appName := "cycle-agent-demo"
	c.runner = runner.NewRunner(appName, cycleAgent)

	// Setup identifiers.
	c.userID = "user"
	c.sessionID = fmt.Sprintf("cycle-session-%d", time.Now().Unix())

	fmt.Printf("✅ Cycle ready! Session: %s\n", c.sessionID)
	fmt.Printf("🔄 Agents: %s → %s (repeat up to %d times)\n\n",
		generateAgent.Info().Name,
		criticAgent.Info().Name,
		c.maxIterations)

	return nil
}

// startChat runs the interactive conversation loop.
func (c *cycleChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		// Handle exit command.
		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		// Process the user message.
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processMessage handles a single message exchange through the agent cycle.
func (c *cycleChat) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)

	// Run the cycle agent through the runner.
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message, agent.RunOptions{})
	if err != nil {
		return fmt.Errorf("failed to run cycle agent: %w", err)
	}

	// Process streaming response.
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles the streaming response from the cycle agent.
func (c *cycleChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		currentIteration = 0
		currentAgent     = ""
		agentStarted     = false
		toolCallsActive  = false
		lastAgent        = ""
		processedToolIDs = make(map[string]bool) // Track processed tool IDs to prevent duplicates
	)

	fmt.Printf("🤖 Cycle Response:\n")

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Track which agent is currently active and detect new iterations.
		if event.Author != currentAgent {
			if agentStarted {
				fmt.Printf("\n")
			}

			// Update lastAgent BEFORE checking for new iterations.
			lastAgent = currentAgent

			// Check if we're starting a new iteration (cycle back to generate-agent).
			if event.Author == "generate-agent" && lastAgent == "critic-agent" {
				currentIteration++
				fmt.Printf("\n🔄 **Iteration %d**\n", currentIteration+1)
			}

			currentAgent = event.Author
			agentStarted = true
			toolCallsActive = false

			// Display agent transition.
			if currentAgent != "" {
				emoji := c.getAgentEmoji(currentAgent)
				agentTitle := strings.Title(strings.Replace(currentAgent, "-", " ", -1))
				fmt.Printf("\n%s %s: ", emoji, agentTitle)
			}
		}

		// Detect and display tool calls.
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			if !toolCallsActive {
				toolCallsActive = true
				fmt.Printf("\n🔧 Using tools:\n")
				for _, toolCall := range event.Choices[0].Message.ToolCalls {
					fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				}
				fmt.Printf("🔄 Executing...\n")
			}
		}

		// Detect tool responses.
		if event.Response != nil && len(event.Response.Choices) > 0 {
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					// Skip if we've already processed this tool response.
					if processedToolIDs[choice.Message.ToolID] {
						continue
					}
					processedToolIDs[choice.Message.ToolID] = true

					content := strings.TrimSpace(choice.Message.Content)
					// Extract key info from JSON tool results.
					if strings.Contains(content, "\"score\":") {
						// Parse score from JSON.
						if scoreIdx := strings.Index(content, "\"score\":"); scoreIdx != -1 {
							scoreSection := content[scoreIdx+8:]
							if commaIdx := strings.Index(scoreSection, ","); commaIdx != -1 {
								score := scoreSection[:commaIdx]
								fmt.Printf("✅ Quality Score: %s/100\n", score)
							}
						}
						if strings.Contains(content, "\"needs_improvement\":true") {
							fmt.Printf("⚠️  Needs improvement - continuing iteration\n")
						} else if strings.Contains(content, "\"needs_improvement\":false") {
							fmt.Printf("🎉 Quality threshold met - cycle complete\n")
						}
					} else {
						// Show short summary for other tools.
						if len(content) > 100 {
							content = content[:97] + "..."
						}
						fmt.Printf("✅ Tool result: %s\n", content)
					}
				}
			}
		}

		// Process streaming content.
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Delta.Content != "" {
				if toolCallsActive {
					toolCallsActive = false
					fmt.Printf("\n%s (continued): ", c.getAgentEmoji(currentAgent))
				}
				fmt.Print(choice.Delta.Content)
			}
		}

		// Check if this is the final runner completion event.
		if event.Done && event.Response != nil && event.Response.Object == model.ObjectTypeRunnerCompletion {
			fmt.Printf("\n")
			break
		}
	}

	fmt.Printf("\n🏁 Cycle completed after %d iteration(s)\n", currentIteration+1)
	return nil
}

// getAgentEmoji returns an emoji for the agent based on its role.
func (c *cycleChat) getAgentEmoji(agentName string) string {
	switch {
	case strings.Contains(agentName, "generate"):
		return "🤖"
	case strings.Contains(agentName, "critic"):
		return "👀"
	default:
		return "🤖"
	}
}

// recordScore allows the critic agent to record its quality assessment decision.
func (c *cycleChat) recordScore(args scoreArgs) scoreResult {
	return scoreResult{
		Score:            args.Score,
		NeedsImprovement: args.NeedsImprovement,
		Feedback:         args.Feedback,
		Timestamp:        time.Now().Format("15:04:05"),
	}
}

// storeSolution simulates storing solution iterations.
func (c *cycleChat) storeSolution(args solutionArgs) solutionResult {
	timestamp := time.Now().Format("15:04:05")

	return solutionResult{
		Solution:  args.Solution,
		Version:   args.Version,
		Timestamp: timestamp,
		Stored:    true,
	}
}

// scoreArgs represents arguments for recording quality scores.
type scoreArgs struct {
	Score            int    `json:"score" description:"Quality score from 0-100"`
	NeedsImprovement bool   `json:"needs_improvement" description:"Whether the content needs improvement (true if score < 82)"`
	Feedback         string `json:"feedback" description:"Specific feedback or recommendations"`
}

// scoreResult represents the result of recording a quality score.
type scoreResult struct {
	Score            int    `json:"score"`
	NeedsImprovement bool   `json:"needs_improvement"`
	Feedback         string `json:"feedback"`
	Timestamp        string `json:"timestamp"`
}

// solutionArgs represents arguments for solution storage.
type solutionArgs struct {
	Solution string `json:"solution" description:"Solution to store"`
	Version  string `json:"version" description:"Version identifier for the solution"`
}

// solutionResult represents the result of solution storage.
type solutionResult struct {
	Solution  string `json:"solution"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
	Stored    bool   `json:"stored"`
}

// intPtr returns a pointer to an int.
func intPtr(i int) *int {
	return &i
}

// floatPtr returns a pointer to a float64.
func floatPtr(f float64) *float64 {
	return &f
}
