//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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

func (a *demoApp) printBanner() {
	fmt.Println("🔐 Guardrail approval demo")
	fmt.Printf("Model: %s\n", a.modelName)
	fmt.Printf("Streaming: %t\n", a.streaming)
	fmt.Printf("Base dir: %s\n", a.baseDir)
	fmt.Printf("Session: %s\n", a.sessionID)
	fmt.Printf("Commands: %s, %s\n", cmdHelp, cmdExit)
	fmt.Println(strings.Repeat("=", 50))
}

func (a *demoApp) printHelp() {
	fmt.Println("Try prompts such as:")
	fmt.Println(`  - "List the files in the current directory."`)
	fmt.Println(`  - "Run pwd and explain the result."`)
	fmt.Println(`  - "Run go test ./... and summarize failures."`)
	fmt.Println("Guardrail behavior:")
	fmt.Printf("  - %s requires approval review.\n", toolExecCommand)
	fmt.Printf("  - %s bypasses approval.\n", toolWriteStdin)
	fmt.Printf("  - %s is denied by policy.\n", toolKillSession)
}

func (a *demoApp) printEvents(eventCh <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")
	assistantStarted := false
	for evt := range eventCh {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}
		if evt.IsToolCallResponse() {
			if assistantStarted {
				fmt.Println()
				assistantStarted = false
			}
			printToolCalls(evt)
			continue
		}
		if evt.IsToolResultResponse() {
			printToolResults(evt)
			continue
		}
		if evt.IsRunnerCompletion() {
			break
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		content := choice.Message.Content
		if evt.Response.IsPartial {
			content = choice.Delta.Content
		}
		if content == "" {
			continue
		}
		fmt.Print(content)
		assistantStarted = true
	}
	if assistantStarted {
		fmt.Println()
	}
	return nil
}

func printToolCalls(evt *event.Event) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	fmt.Println("🔧 Tool calls:")
	for _, toolCall := range evt.Response.Choices[0].Message.ToolCalls {
		fmt.Printf("  - %s\n", toolCall.Function.Name)
		if len(toolCall.Function.Arguments) > 0 {
			fmt.Printf("    args: %s\n", string(toolCall.Function.Arguments))
		}
	}
}

func printToolResults(evt *event.Event) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	fmt.Println("📦 Tool results:")
	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleTool || msg.Content == "" {
			continue
		}
		fmt.Printf("  - %s\n", msg.ToolName)
		fmt.Printf("    result: %s\n", msg.Content)
	}
}
