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

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func printBanner(modelName string, streaming bool) {
	fmt.Printf("🚀 User Message Rewriter Demo\n")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Streaming: %t\n", streaming)
	fmt.Printf("Session backend: in-memory\n")
	fmt.Printf("Commands: /dump, /exit\n")
	fmt.Printf("Rewrite example: %s your original request\n", rewritePrefix)
	fmt.Printf("Expand example: %s please help me reply to this urgent order issue\n", expandPrefix)
	fmt.Println(strings.Repeat("=", 50))
}

func printRewritePreview(original string, messages []model.Message) {
	if len(messages) == 1 && messages[0].Content == original {
		return
	}
	if len(messages) == 1 {
		fmt.Println("✏️ Rewriter replaced current turn with:")
		fmt.Printf("   %s\n", messages[0].Content)
		return
	}
	fmt.Println("📚 Rewriter expanded current turn into:")
	for i, msg := range messages {
		fmt.Printf("   %d. %s\n", i+1, msg.Content)
	}
}

func printSessionTranscript(sess *session.Session) {
	if sess == nil || len(sess.Events) == 0 {
		fmt.Println("🗂️ Session transcript is empty.")
		return
	}
	fmt.Println("🗂️ Persisted session transcript:")
	index := 1
	for _, evt := range sess.Events {
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role == "" || msg.Content == "" {
				continue
			}
			fmt.Printf("   %d. [%s] %s\n", index, msg.Role, msg.Content)
			index++
		}
	}
	if index == 1 {
		fmt.Println("   (no printable transcript messages)")
	}
}
