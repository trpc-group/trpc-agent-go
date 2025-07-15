package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	r := runner.NewRunner("human_in_the_loop", newLLMAgent(), runner.WithSessionService(inmemory.NewSessionService()))
	callAgent := func(ctx context.Context, query string) error {

		fmt.Printf("User Query: %s\n", query)
		fmt.Printf("--- Running agent's initial turn ---\n")
		longRunningFunctionCall, initialToolResponse := processStreamingResponse(ctx, r, model.NewUserMessage(query))
		fmt.Printf("--- End of agent's initial turn ---\n")

		if longRunningFunctionCall != nil && initialToolResponse != nil && initialToolResponse.Status == "pending" {
			fmt.Printf("--- Simulating external approval for ticket: %s ---\n", initialToolResponse.TicketID)
			updatedToolOutputData := map[string]string{
				"status":            "approved",
				"ticketId":          initialToolResponse.TicketID,
				"approver_feedback": "Approved by manager at " + time.Now().String(),
			}
			bts, _ := json.Marshal(updatedToolOutputData)
			fmt.Printf("--- Sending updated tool result to agent for call ID %s: %s ---\n", initialToolResponse.TicketID, updatedToolOutputData)
			fmt.Printf("--- Running agent's turn AFTER receiving updated tool result ---\n")
			_, _ = processStreamingResponse(ctx, r, model.NewUserMessage(string(bts)))
			fmt.Printf("--- End of agent's turn AFTER receiving updated tool result ---\n")
		} else if longRunningFunctionCall != nil && initialToolResponse == nil {
			fmt.Printf("--- Long running function '%s' was called, but its initial response was not captured. ---", longRunningFunctionCall.Function.Name)
		} else if longRunningFunctionCall == nil {
			fmt.Printf(
				"--- No long running function call was detected in the initial turn. ---")
		}

		return nil
	}

	if err := callAgent(context.Background(), "Please reimburse $50 for meals"); err != nil {
		log.Fatal(err)
	}
	if err := callAgent(context.Background(), "Please reimburse $200 for conference travel"); err != nil {
		log.Fatal(err)
	}
}

const userID, sessionID = "user-123", "session-123"

func processStreamingResponse(ctx context.Context, r runner.Runner, message model.Message) (*model.ToolCall, *askForApprovalOutput) {
	eventChan, err := r.Run(ctx, userID, sessionID, message, agent.RunOptions{})
	if err != nil {
		log.Fatalf("failed to run agent: %v", err)
	}
	var (
		longRunningFunctionCall *model.ToolCall
		initialToolResponse     *askForApprovalOutput
	)

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)
	for e := range eventChan {
		// Handle errors.
		if e.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", e.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if len(e.Choices) > 0 && len(e.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 CallableTool calls initiated:\n")
			for _, toolCall := range e.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
				if _, ok := e.LongRunningToolIDs[toolCall.ID]; ok {
					longRunningFunctionCall = &toolCall
					fmt.Printf("(Captured as long_running_function_call for %s)\n", toolCall.Function.Name)
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		// Detect tool responses.
		if e.Response != nil && len(e.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range e.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
					if longRunningFunctionCall != nil && longRunningFunctionCall.ID == choice.Message.ToolID {
						fmt.Printf("Captured as initial_tool_response for %s, content: %s", longRunningFunctionCall.Function.Name, choice.Message.Content)
						initialToolResponse = &askForApprovalOutput{}
						err := json.Unmarshal([]byte(choice.Message.Content), initialToolResponse)
						if err != nil {
							log.Fatalf("failed to unmarshal ask for approval output: %v", err)
						}
					}
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Process streaming content.
		if len(e.Choices) > 0 {
			choice := e.Choices[0]

			// Handle streaming delta content.
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		// Check if this is the final e.
		// Don't break on tool response events (Done=true but not final assistant response).
		if e.Done && !isToolEvent(e) {
			fmt.Printf("\n")
			break
		}
	}
	return longRunningFunctionCall, initialToolResponse
}

func isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}

	// Check if this is a tool response by examining choices.
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}

	return false
}
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
