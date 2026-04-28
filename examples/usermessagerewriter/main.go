//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to rewrite the current-turn user message
// before Runner persists it into the session transcript.
package main

import (
	"flag"
	"log"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Name of the model to use")
	streaming = flag.Bool("streaming", true, "Enable streaming mode for responses")
)

const (
	appName   = "user-message-rewriter-demo"
	agentName = "support-assistant"
)

func main() {
	flag.Parse()
	printBanner(*modelName, *streaming)
	chat := &rewriterChat{
		modelName: *modelName,
		streaming: *streaming,
	}
	if err := chat.run(); err != nil {
		log.Fatalf("chat failed: %v", err)
	}
}
