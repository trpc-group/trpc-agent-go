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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

// lastPendingTicketID remembers the most recent pending approval ticket id.
var lastPendingTicketID string

func main() {
	flag.Parse()

	fmt.Printf("🚀 Human-in-the-Loop (HIL) Reimbursement Demo\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Streaming: %t\n", *streaming)
	fmt.Println(strings.Repeat("=", 50))

	r, err := setupRunner(*modelName, *streaming)
	if err != nil {
		log.Fatal(err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	printHelp()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if !dispatchInput(context.Background(), r, input) {
			return
		}
	}
}

// setupRunner creates the runner with in-memory session.
func setupRunner(model string, stream bool) (runner.Runner, error) {
	r := runner.NewRunner("human_in_the_loop",
		newLLMAgent(model, stream),
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	return r, nil
}

// printHelp prints quick usage hints for the demo.
func printHelp() {
	fmt.Println("💡 Try:")
	fmt.Println("   • Please reimburse $50 for meals")
	fmt.Println("   • Please reimburse $200 for conference travel")
	fmt.Println("   • Type '/exit' to quit")
	fmt.Println()
}

// dispatchInput routes a single user input. Returns false to exit.
func dispatchInput(ctx context.Context, r runner.Runner, input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "/exit" {
		fmt.Println("👋 Goodbye!")
		return false
	}
	_, _ = processStreamingResponse(ctx, r, model.NewUserMessage(input))
	fmt.Println()
	return true
}

const userID, sessionID = "user-123", "session-123"

func processStreamingResponse(ctx context.Context, r runner.Runner, message model.Message) (*model.ToolCall, *askForApprovalOutput) {
	eventChan, err := r.Run(ctx, userID, sessionID, message)
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
		autoApprovalSent  bool
	)
	for e := range eventChan {
		// Handle errors.
		if e.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", e.Error.Message)
			continue
		}

		// Detect and display tool calls.
		if longRunningFunctionCall, toolCallsDetected, assistantStarted = handleToolCalls(e,
			longRunningFunctionCall, toolCallsDetected, assistantStarted); longRunningFunctionCall != nil {
			// If a long-running call is detected and we haven't sent approval yet, simulate it.
			if !autoApprovalSent && lastPendingTicketID != "" {
				fmt.Printf("--- Simulating external approval for ticket: %s ---\n", lastPendingTicketID)
				updated := map[string]string{
					"status":            "approved",
					"ticket_id":         lastPendingTicketID,
					"approver_feedback": "Approved by manager",
				}
				bts, _ := json.Marshal(updated)
				fmt.Printf("--- Sending updated tool result to agent ---\n")
				_, _ = processStreamingResponse(ctx, r, model.NewUserMessage(string(bts)))
				autoApprovalSent = true
			}
			continue
		}

		// Detect tool responses.
		if initialToolResponse = handleToolResponses(e, longRunningFunctionCall); initialToolResponse != nil {
			// If we got a pending approval, simulate external approval automatically (if not already sent).
			if !autoApprovalSent && strings.ToLower(initialToolResponse.Status) == "pending" {
				fmt.Printf("--- Simulating external approval for ticket: %s ---\n", initialToolResponse.TicketID)
				updated := map[string]string{
					"status":            "approved",
					"ticket_id":         initialToolResponse.TicketID,
					"approver_feedback": "Approved by manager",
				}
				bts, _ := json.Marshal(updated)
				fmt.Printf("--- Sending updated tool result to agent ---\n")
				_, _ = processStreamingResponse(ctx, r, model.NewUserMessage(string(bts)))
				autoApprovalSent = true
			}
			continue
		}

		// Process streaming content.
		fullContent, assistantStarted = processStreamingContent(e, toolCallsDetected, assistantStarted, fullContent)

		// Check if this is the final e.
		// Don't break on tool response events (Done=true but not final assistant response).
		if e.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}
	return longRunningFunctionCall, initialToolResponse
}

// handleToolCalls processes tool call events and returns updated state
func handleToolCalls(e *event.Event, longRunningFunctionCall *model.ToolCall, toolCallsDetected bool, assistantStarted bool) (*model.ToolCall, bool, bool) {
	if len(e.Choices) == 0 || len(e.Choices[0].Message.ToolCalls) == 0 {
		return longRunningFunctionCall, toolCallsDetected, assistantStarted
	}

	toolCallsDetected = true
	if assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 CallableTool calls initiated:\n")
	hasLongRunning := false
	for _, toolCall := range e.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
		}
		if _, ok := e.LongRunningToolIDs[toolCall.ID]; ok {
			// Create a local copy to avoid implicit memory aliasing in Go <= 1.21.
			tc := toolCall
			longRunningFunctionCall = &tc
			hasLongRunning = true
			fmt.Printf("(Captured as long_running_function_call for %s)\n", toolCall.Function.Name)
			// Remember the tool call ID as a fallback ticket for shorthand approval.
			lastPendingTicketID = toolCall.ID
			fmt.Printf("💬 Waiting for approval.\n")
		}
	}
	if hasLongRunning {
		fmt.Printf("\n⏸️ Waiting for human approval...\n")
	} else {
		fmt.Printf("\n🔄 Executing tools...\n")
	}
	return longRunningFunctionCall, toolCallsDetected, assistantStarted
}

// handleToolResponses processes tool response events
func handleToolResponses(e *event.Event, longRunningFunctionCall *model.ToolCall) *askForApprovalOutput {
	if e.Response == nil || len(e.Response.Choices) == 0 {
		return nil
	}

	var initialToolResponse *askForApprovalOutput
	for _, choice := range e.Response.Choices {
		if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
			fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
				choice.Message.ToolID,
				strings.TrimSpace(choice.Message.Content))
			if longRunningFunctionCall != nil && longRunningFunctionCall.ID == choice.Message.ToolID {
				fmt.Printf("Captured as initial_tool_response for %s, content: %s", longRunningFunctionCall.Function.Name, choice.Message.Content)
				initialToolResponse = &askForApprovalOutput{}
				if err := json.Unmarshal([]byte(choice.Message.Content), initialToolResponse); err == nil {
					// Remember the last pending ticket id to enable shorthand approvals.
					if strings.ToLower(initialToolResponse.Status) == "pending" && initialToolResponse.TicketID != "" {
						lastPendingTicketID = initialToolResponse.TicketID
						fmt.Printf("💬 Approval pending.\n")
					}
				} else {
					log.Fatalf("failed to unmarshal ask for approval output: %v", err)
				}
			}
		}
	}
	return initialToolResponse
}

// processStreamingContent processes streaming content events
func processStreamingContent(e *event.Event, toolCallsDetected bool, assistantStarted bool, fullContent string) (string, bool) {
	if len(e.Choices) == 0 {
		return fullContent, assistantStarted
	}

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
	return fullContent, assistantStarted
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
