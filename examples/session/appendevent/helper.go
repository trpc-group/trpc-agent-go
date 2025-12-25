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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// processResponse handles both streaming and non-streaming responses.
func (c *appendEventChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if err := c.handleEvent(event, &toolCallsDetected, &assistantStarted, &fullContent); err != nil {
			return err
		}

		if event.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

// handleEvent processes a single event from the event channel.
func (c *appendEventChat) handleEvent(
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
	if c.handleToolCalls(event, toolCallsDetected, assistantStarted) {
		return nil
	}

	// Handle tool responses.
	if c.handleToolResponses(event) {
		return nil
	}

	// Handle content.
	c.handleContent(event, toolCallsDetected, assistantStarted, fullContent)

	return nil
}

// handleToolCalls detects and displays tool calls.
func (c *appendEventChat) handleToolCalls(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
) bool {
	if len(event.Response.Choices) > 0 && len(event.Response.Choices[0].Message.ToolCalls) > 0 {
		*toolCallsDetected = true
		if *assistantStarted {
			fmt.Printf("\n")
		}
		fmt.Printf("🔧 Tool calls initiated:\n")
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
func (c *appendEventChat) handleToolResponses(event *event.Event) bool {
	if event.Response != nil && len(event.Response.Choices) > 0 {
		hasToolResponse := false
		for _, choice := range event.Response.Choices {
			if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
				fmt.Printf("✅ Tool response (ID: %s): %s\n",
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
func (c *appendEventChat) handleContent(
	event *event.Event,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if len(event.Response.Choices) > 0 {
		choice := event.Response.Choices[0]
		content := c.extractContent(choice)

		if content != "" {
			c.displayContent(content, toolCallsDetected, assistantStarted, fullContent)
		}
	}
}

// extractContent extracts content based on streaming mode.
func (c *appendEventChat) extractContent(choice model.Choice) string {
	if c.streaming {
		return choice.Delta.Content
	}
	return choice.Message.Content
}

// displayContent prints content to console.
func (c *appendEventChat) displayContent(
	content string,
	toolCallsDetected *bool,
	assistantStarted *bool,
	fullContent *string,
) {
	if !*assistantStarted {
		if *toolCallsDetected {
			fmt.Printf("\n🤖 Assistant: ")
		}
		*assistantStarted = true
	}
	fmt.Print(content)
	*fullContent += content
}

func (c *appendEventChat) startNewSession() {
	oldSessionID := c.sessionID
	c.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	c.rememberSession(c.sessionID)
	fmt.Printf("🆕 Started new session!\n")
	fmt.Printf("   Previous: %s\n", oldSessionID)
	fmt.Printf("   Current:  %s\n", c.sessionID)
	fmt.Printf("   (Conversation history has been reset)\n")
	fmt.Println()
}

func (c *appendEventChat) rememberSession(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	for _, existing := range c.sessionIDs {
		if existing == id {
			return
		}
	}
	c.sessionIDs = append(c.sessionIDs, id)
}

func (c *appendEventChat) listSessions() {
	if len(c.sessionIDs) == 0 {
		fmt.Println("(no sessions recorded yet)")
		fmt.Println()
		return
	}
	fmt.Println("🗂 Session roster:")
	for _, id := range c.sessionIDs {
		marker := " "
		if id == c.sessionID {
			marker = "*"
		}
		fmt.Printf("   %s %s\n", marker, id)
	}
	fmt.Println()
}

func (c *appendEventChat) switchSession(target string) {
	target = strings.TrimSpace(target)
	if target == "" {
		fmt.Println("⚠️  Usage: /use <session-id>")
		return
	}
	if target == c.sessionID {
		fmt.Printf("ℹ️  Already using session %s\n", target)
		return
	}
	c.sessionID = target
	c.rememberSession(target)
	fmt.Printf("🔁 Switched to session %s\n", target)
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
