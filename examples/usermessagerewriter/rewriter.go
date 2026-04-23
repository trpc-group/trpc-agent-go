//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	rewritePrefix = "rewrite"
	expandPrefix  = "expand"
)

// rewriteUserMessage implements the runner user message rewriter hook.
func (c *rewriterChat) rewriteUserMessage(
	_ context.Context,
	args *agent.UserMessageRewriteArgs,
) ([]model.Message, error) {
	return c.rewriteMessages(args.OriginalMessage.Content), nil
}

// rewriteMessages turns one raw user input into the persisted message sequence.
func (c *rewriterChat) rewriteMessages(userInput string) []model.Message {
	lowerInput := strings.ToLower(userInput)
	if strings.HasPrefix(lowerInput, rewritePrefix) {
		content := strings.TrimSpace(userInput[len(rewritePrefix):])
		if content == "" {
			return []model.Message{model.NewUserMessage(userInput)}
		}
		return []model.Message{
			model.NewUserMessage("Please rewrite the following request into one concise support-style sentence: " + content),
		}
	}
	if strings.HasPrefix(lowerInput, expandPrefix) {
		content := strings.TrimSpace(userInput[len(expandPrefix):])
		if content == "" {
			return []model.Message{model.NewUserMessage(userInput)}
		}
		return []model.Message{
			model.NewUserMessage("Reference context: Treat the following request as urgent and answer with explicit next steps."),
			model.NewUserMessage(content),
		}
	}
	return []model.Message{model.NewUserMessage(userInput)}
}
