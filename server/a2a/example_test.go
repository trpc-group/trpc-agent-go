package a2a

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestA2AServerExample(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		agentDesc string
		userInput string
		streaming bool
		host      string
		expectErr bool
	}{
		{
			name:      "runner_execution_non_streaming",
			agentName: "agent_joker",
			agentDesc: "i am a remote agent, i can tell a joke",
			userInput: "tell me a joke",
			streaming: false,
			host:      "localhost:18881",
			expectErr: false,
		},
		{
			name:      "runner_execution_streaming",
			agentName: "agent_helper",
			agentDesc: "i am a helpful assistant",
			userInput: "help me with something",
			streaming: true,
			host:      "localhost:18882",
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fmt.Printf("=== Testing %s ===\n", tt.name)
			fmt.Printf("Agent: %s\n", tt.agentName)
			fmt.Printf("Description: %s\n", tt.agentDesc)
			fmt.Printf("User Input: %s\n", tt.userInput)
			fmt.Printf("Streaming: %v\n", tt.streaming)
			fmt.Printf("Host: %s\n", tt.host)

			// Step 1: Create mock agent for the server
			mockAgent := createExampleMockAgent(tt.agentName, tt.agentDesc, tt.streaming)

			// Step 2: Create and start a2a server with mock agent
			server, err := New(
				WithDebugLogging(false),
				WithErrorHandler(func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
					errMsg := protocol.NewMessage(
						protocol.MessageRoleAgent,
						[]protocol.Part{
							protocol.NewTextPart("your own error msg"),
						},
					)
					return &errMsg, nil
				}),
				WithHost(tt.host),
				WithAgent(mockAgent, tt.streaming),
				WithProcessMessageHook(
					func(next taskmanager.MessageProcessor) taskmanager.MessageProcessor {
						return &exampleHookProcessor{next: next}
					},
				),
			)

			if err != nil {
				t.Fatalf("Failed to create a2a server: %v", err)
			}

			defer server.Stop(context.Background())

			// Start server in goroutine
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				server.Start(tt.host)
			}()

			// Wait for server to start
			time.Sleep(100 * time.Millisecond)

			// Step 3: Create a2a agent that connects to the server
			httpURL := fmt.Sprintf("http://%s", tt.host)
			optionalStateKey := "meta"

			a2aAgent, err := createTestA2AAgent(httpURL, optionalStateKey)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Failed to create a2a agent: %v", err)
			}

			// Verify agent card similar to the removed TestA2AServerExample
			testAgentCard(t, mockAgent, tt.agentName, tt.agentDesc, tt.host, tt.streaming)

			// Step 4: Create runner with a2a agent
			sessionService := inmemory.NewSessionService()
			testRunner := runner.NewRunner("test", a2aAgent, runner.WithSessionService(sessionService))

			// Step 5: Execute runner similar to example's processMessage
			userID := "user1"
			sessionID := "session1"

			fmt.Printf("🚀 Starting runner execution...\n")

			// Execute runner similar to example
			events, err := testRunner.Run(
				context.Background(),
				userID,
				sessionID,
				model.NewUserMessage(tt.userInput),
				agent.WithRuntimeState(map[string]any{optionalStateKey: "test"}),
			)

			if err != nil {
				t.Fatalf("Failed to run agent: %v", err)
			}

			// Step 6: Process response similar to example
			if err := processTestResponse(t, events, tt.streaming); err != nil {
				t.Fatalf("Failed to process response: %v", err)
			}

			fmt.Printf("=== Test %s completed successfully ===\n\n", tt.name)
		})
	}
}

// createExampleMockAgent creates a mock agent similar to the one in the example
func createExampleMockAgent(name, desc string, streaming bool) agent.Agent {
	return &exampleMockAgent{
		name:        name,
		description: desc,
		streaming:   streaming,
	}
}

// exampleMockAgent mimics the behavior of the agent in the example
type exampleMockAgent struct {
	name        string
	description string
	streaming   bool
}

func (e *exampleMockAgent) Info() agent.Info {
	return agent.Info{
		Name:        e.name,
		Description: e.description,
	}
}

func (e *exampleMockAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (e *exampleMockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 10)

	// Use unified response for all agent types to simplify testing
	responseContent := "Hello! This is a mock agent response for testing purposes."

	if e.streaming {
		// Simulate streaming response
		words := []string{"Hello", "from", "streaming", "agent:", responseContent}
		for i, word := range words {
			isDone := i == len(words)-1
			ch <- &event.Event{
				Response: &model.Response{
					ID:      fmt.Sprintf("stream-response-%d", i),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   "mock-model",
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: word + " ",
							},
						},
					},
					Done: isDone,
				},
				InvocationID: invocation.InvocationID,
				Author:       e.name,
				ID:           fmt.Sprintf("event-%d", i),
				Timestamp:    time.Now(),
			}
		}
	} else {
		// Non-streaming response
		ch <- &event.Event{
			Response: &model.Response{
				ID:      "response-1",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "mock-model",
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: responseContent,
						},
					},
				},
				Done: true,
			},
			InvocationID: invocation.InvocationID,
			Author:       e.name,
			ID:           "event-1",
			Timestamp:    time.Now(),
		}
	}

	close(ch)
	return ch, nil
}

func (e *exampleMockAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

func (e *exampleMockAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

// exampleHookProcessor mimics the hook processor from the example
type exampleHookProcessor struct {
	next taskmanager.MessageProcessor
}

func (h *exampleHookProcessor) ProcessMessage(
	ctx context.Context,
	message protocol.Message,
	options taskmanager.ProcessOptions,
	handler taskmanager.TaskHandler,
) (*taskmanager.MessageProcessingResult, error) {
	// Log message similar to example (using fmt.Printf for testing)
	fmt.Printf("A2A Server: received message: %+v\n", message.MessageID)
	fmt.Printf("A2A Server: received state: %+v\n", message.Metadata)
	return h.next.ProcessMessage(ctx, message, options, handler)
}

// testAgentCard verifies the agent card is built correctly
func testAgentCard(t *testing.T, agent agent.Agent, expectedName, expectedDesc, expectedHost string, streaming bool) {
	options := &options{
		agent:           agent,
		host:            expectedHost,
		enableStreaming: streaming,
		errorHandler:    defaultErrorHandler,
	}

	card := buildAgentCard(options)

	if card.Name != expectedName {
		t.Errorf("Expected agent name %s, got %s", expectedName, card.Name)
	}

	if card.Description != expectedDesc {
		t.Errorf("Expected agent description %s, got %s", expectedDesc, card.Description)
	}

	expectedURL := fmt.Sprintf("http://%s", expectedHost)
	if card.URL != expectedURL {
		t.Errorf("Expected agent URL %s, got %s", expectedURL, card.URL)
	}

	if card.Capabilities.Streaming == nil {
		t.Error("Expected streaming capability to be set")
	} else if *card.Capabilities.Streaming != streaming {
		t.Errorf("Expected streaming %v, got %v", streaming, *card.Capabilities.Streaming)
	}

	// Verify skills are created
	if len(card.Skills) == 0 {
		t.Error("Expected at least one skill")
	}

	// First skill should be the default agent skill
	defaultSkill := card.Skills[0]
	if defaultSkill.Name != expectedName {
		t.Errorf("Expected default skill name %s, got %s", expectedName, defaultSkill.Name)
	}

	if defaultSkill.Description == nil || *defaultSkill.Description != expectedDesc {
		t.Errorf("Expected default skill description %s, got %v", expectedDesc, defaultSkill.Description)
	}
}

// processTestResponse processes the event channel similar to example's processResponse
func processTestResponse(t *testing.T, eventChan <-chan *event.Event, streaming bool) error {
	t.Helper()

	var (
		fullContent       string
		toolCallsDetected bool
		eventCount        int
	)

	for event := range eventChan {
		eventCount++

		// Handle errors
		if event.Error != nil {
			return fmt.Errorf("event error: %s", event.Error.Message)
		}

		// Handle tool calls
		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			continue
		}

		// Handle content
		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if streaming {
				fullContent += choice.Delta.Content
			} else {
				fullContent += choice.Message.Content
			}
		}

		// Check final event
		if event.Response != nil && event.Response.Done && !isTestToolEvent(event) {
			break
		}
	}

	// Basic validation
	if eventCount == 0 {
		return fmt.Errorf("no events received")
	}
	if fullContent == "" && !toolCallsDetected {
		return fmt.Errorf("no content received")
	}

	// Validate content
	if !toolCallsDetected {
		trimmed := strings.TrimSpace(fullContent)
		if len(trimmed) < 5 {
			return fmt.Errorf("content too short: %d chars", len(trimmed))
		}
	}

	return nil
}

// isTestToolEvent checks if an event is a tool response (similar to example's isToolEvent)
func isTestToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	// Check if this is a tool response by examining choices
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}

// createTestA2AAgent creates an a2a agent that connects to the server
// similar to the example in examples/a2aagent/main.go
func createTestA2AAgent(httpURL, optionalStateKey string) (agent.Agent, error) {
	a2aAgent, err := a2aagent.New(
		a2aagent.WithAgentCardURL(httpURL),
		// optional: specify the state key that transferred to the remote agent by metadata
		a2aagent.WithTransferStateKey(optionalStateKey),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create a2a agent: %w", err)
	}

	// Verify agent card similar to example
	card := a2aAgent.GetAgentCard()
	fmt.Printf("------- Agent Card -------\n")
	fmt.Printf("Name: %s\n", card.Name)
	fmt.Printf("Description: %s\n", card.Description)
	fmt.Printf("URL: %s\n", httpURL)
	fmt.Printf("------------------------\n")

	return a2aAgent, nil
}
