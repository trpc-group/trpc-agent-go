//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func (c *hedgeChat) printBanner() {
	fmt.Printf("🚀 Model hedge chat with LLMAgent and Runner\n")
	fmt.Printf("Primary model: %s (%s)\n", c.config.primaryModelName, c.config.primaryBaseURL)
	fmt.Printf("Backup model: %s (%s)\n", c.config.backupModelName, c.config.backupBaseURL)
	fmt.Printf("Hedge delay: %s\n", c.config.hedgeDelay)
	fmt.Printf("Streaming: %t\n", c.config.streaming)
	fmt.Printf("Hedge policy: launch the primary immediately, then hedge backups on delay or early failure.\n")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
}

func printCommands() {
	fmt.Println("💡 Commands:")
	fmt.Println("   /new      - Start a new session")
	fmt.Println("   /exit     - End the conversation")
	fmt.Println()
}

func (c *hedgeChat) processResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	var output strings.Builder
	for evt := range eventChan {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			return nil
		}
		if len(evt.Choices) == 0 {
			if evt.Done {
				break
			}
			continue
		}
		content := c.extractContent(evt.Choices[0])
		if content != "" {
			fmt.Print(content)
			output.WriteString(content)
		}
		if evt.Done {
			break
		}
	}
	if output.Len() > 0 {
		fmt.Println()
	} else {
		fmt.Println("(empty response)")
	}
	return nil
}

func (c *hedgeChat) extractContent(choice model.Choice) string {
	if choice.Delta.Content != "" {
		return choice.Delta.Content
	}
	return choice.Message.Content
}
