//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName  = flag.String("model", "deepseek-chat", "Model to use")
	host       = flag.String("host", "0.0.0.0:8888", "Host to use")
	streaming  = flag.Bool("streaming", true, "Streaming to use")
	remoteOnly = flag.Bool("remote-only", false, "Only output remote agent responses")
)

const (
	optionalStateKey = "meta"
)

func main() {
	flag.Parse()

	// runRemoteAgent will start a a2a server that build with a remote agent
	runRemoteAgent("agent_remote_joker", "I am a remote agent, I can tell a joke", *host)
	time.Sleep(1 * time.Second)

	httpURL := fmt.Sprintf("http://%s", *host)
	a2aAgent := buildA2AAgent(httpURL)

	// Build a different local agent
	localAgent := buildAgent("agent_local_joker", "I am a local agent, I can tell a joke")
	startChat(localAgent, a2aAgent)
}

func startChat(localAgent agent.Agent, a2aAgent *a2aagent.A2AAgent) {

	card := a2aAgent.GetAgentCard()
	fmt.Printf("\n------- Agent Card -------\n")
	fmt.Printf("Name: %s\n", card.Name)
	fmt.Printf("Description: %s\n", card.Description)
	fmt.Printf("URL: %s\n", card.URL)
	fmt.Printf("------------------------\n")

	localSessionService := inmemory.NewSessionService()
	remoteSessionService := inmemory.NewSessionService()

	remoteRunner := runner.NewRunner("test", a2aAgent, runner.WithSessionService(remoteSessionService))
	localRunner := runner.NewRunner("test", localAgent, runner.WithSessionService(localSessionService))

	// Use different userIDs and sessionIDs for remote and local agents
	remoteUserID := "remote_user"
	remoteSessionID := "remote_session1"
	localUserID := "local_user"
	localSessionID := "local_session1"

	fmt.Println("Chat with the agent. Type 'new' for a new session, or 'exit' to quit.")

	for {
		if err := processMessage(remoteRunner, localRunner, remoteUserID, &remoteSessionID, localUserID, &localSessionID); err != nil {
			if err.Error() == "exit" {
				fmt.Println("👋 Goodbye!")
				return
			}
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println() // Add spacing between turns
	}
}

func processMessage(
	remoteRunner runner.Runner,
	localRunner runner.Runner,
	remoteUserID string,
	remoteSessionID *string,
	localUserID string,
	localSessionID *string,
) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("User: ")
	if !scanner.Scan() {
		return fmt.Errorf("exit")
	}

	userInput := strings.TrimSpace(scanner.Text())
	if userInput == "" {
		return nil
	}

	switch strings.ToLower(userInput) {
	case "exit":
		return fmt.Errorf("exit")
	case "new":
		*remoteSessionID = startNewSession("remote")
		*localSessionID = startNewSession("local")
		return nil
	}

	fmt.Printf("%s remote agent %s\n", strings.Repeat("=", 8), strings.Repeat("=", 8))
	events, err := remoteRunner.Run(
		context.Background(),
		remoteUserID,
		*remoteSessionID,
		model.NewUserMessage(userInput),
		agent.WithRuntimeState(map[string]any{optionalStateKey: "test"}),
		// Example: Pass custom HTTP headers to A2A agent using WithA2ARequestOptions
		// This allows you to add authentication tokens, tracing IDs, or other custom headers
		agent.WithA2ARequestOptions(
			client.WithRequestHeader("X-Custom-Header", "custom-value"),
			client.WithRequestHeader("X-Request-ID", fmt.Sprintf("req-%d", time.Now().UnixNano())),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	if err := processResponse(events); err != nil {
		return fmt.Errorf("failed to process response: %w", err)
	}

	// Only run local agent if remote-only flag is not set
	if !*remoteOnly {
		fmt.Printf("\n%s local agent %s\n", strings.Repeat("=", 8), strings.Repeat("=", 8))
		events, err = localRunner.Run(
			context.Background(),
			localUserID,
			*localSessionID,
			model.NewUserMessage(userInput),
			agent.WithRuntimeState(map[string]any{optionalStateKey: "test"}),
		)
		if err != nil {
			return fmt.Errorf("failed to run agent: %w", err)
		}
		if err := processResponse(events); err != nil {
			return fmt.Errorf("failed to process response: %w", err)
		}
	}
	return nil
}

func startNewSession(prefix string) string {
	newSessionID := fmt.Sprintf("%s_session_%d", prefix, time.Now().UnixNano())
	fmt.Printf("🆕 Started new %s session: %s\n", prefix, newSessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
	return newSessionID
}

type hookProcessor struct {
	next taskmanager.MessageProcessor
}

func (h *hookProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	fmt.Printf("A2A Server: received message:%+v\n", message.MessageID)
	fmt.Printf("A2A Server: received state: %+v\n", message.Metadata)
	return h.next.ProcessMessage(ctx, message, options, handler)
}

func runRemoteAgent(agentName, desc, host string) {
	remoteAgent := buildAgent(agentName, desc)
	server, err := a2a.New(
		a2a.WithDebugLogging(false),
		a2a.WithErrorHandler(func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			errMsg := protocol.NewMessage(
				protocol.MessageRoleAgent,
				[]protocol.Part{
					protocol.NewTextPart("your own error msg"),
				},
			)
			return &errMsg, nil
		}),
		a2a.WithHost(host),
		a2a.WithAgent(remoteAgent, *streaming),
		a2a.WithProcessMessageHook(
			func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
				return &hookProcessor{next: next}
			},
		),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a server: %v", err)
	}
	go func() {
		server.Start(host)
	}()
}

func buildAgent(agentName, desc string) agent.Agent {
	// Create OpenAI model.
	modelInstance := openai.New(*modelName)

	// Create LLM agent.
	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      *streaming,
	}
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(desc),
		llmagent.WithInstruction(desc),
		llmagent.WithGenerationConfig(genConfig),
	)

	return llmAgent
}

func buildA2AAgent(httpURL string) *a2aagent.A2AAgent {
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(httpURL),

		// optional: specify the state key that transferred to the remote agent by metadata
		a2aagent.WithTransferStateKey(optionalStateKey),
	)
	if err != nil {
		log.Fatalf("Failed to create a2a agent: %v", err)
	}
	return a2aAgent
}

// processResponse handles both streaming and non-streaming responses with tool call visualization.
func processResponse(eventChan <-chan *event.Event) error {
	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if err := handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}

		// Check if this is the final event.
		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func handleEvent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) error {
	// Handle errors.
	if event.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
		return nil
	}

	// Handle tool calls.
	if handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle tool responses.
	if handleToolResponses(event) {
		return nil
	}

	// Handle content.
	handleContent(event, toolCallsDetected, assistantStarted, fullContent)

	return nil
}

// handleToolCalls detects and displays tool calls.
func handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 CallableTool calls initiated:\n")
		for _, toolCall := range event.Response.Choices[0].Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
			if len(toolCall.Function.Arguments) > 0 {
				fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
			}
		}
		fmt.Printf("\n🔄 Executing tools...\n")
		return true
	}
	return false
}

// handleToolResponses detects and displays tool responses.
func handleToolResponses(event *event.Event) bool {
	if event.Response != nil && len(event.Response.Choices) > 0 {
		hasToolResponse := false
		for _, choice := range event.Response.Choices {
			// Handle traditional tool responses (Role: tool)
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
					choice.Message.ToolID,
					strings.TrimSpace(choice.Message.Content))
				hasToolResponse = true
			}
		}
		if hasToolResponse {
			return true
		}
	}
	return false
}

// handleContent processes and displays content.
func handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		content := extractContent(choice)

		if content != "" {
			displayContent(content, toolCallsDetected, assistantStarted, fullContent)
		}
	}
}

// extractContent extracts content based on streaming mode.
func extractContent(choice model.Choice) string {
	if *streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

// displayContent prints content to console.
func displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		} else {
			fmt.Printf("🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
