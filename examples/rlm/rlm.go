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
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RLM implements the core Recursive Language Model loop.
// Each iteration: LLM generates code → Starlark REPL executes → results fed back → repeat.
type RLM struct {
	model       model.Model
	serviceAddr string
	depth       int
	maxDepth    int
	maxIter     int
}

// Run executes the RLM loop on the given context and query.
func (r *RLM) Run(ctx context.Context, query, promptContext, boundary, stopCondition string) (string, error) {
	repl := NewREPL(promptContext, r.serviceAddr, r.depth)

	systemPrompt := BuildSystemPrompt(len(promptContext), countLines(promptContext), r.depth, r.maxDepth, r.maxIter)

	userMsg := fmt.Sprintf(
		"Answer the following query by writing code in the REPL.\n\nQuery: %s",
		query,
	)
	if boundary != "" {
		userMsg += fmt.Sprintf("\n\nBoundary: %s", boundary)
	}
	if stopCondition != "" {
		userMsg += fmt.Sprintf("\n\nStop condition: %s", stopCondition)
	}
	userMsg += "\n\nStart by inspecting the context: print(len(context)), print(context[:500])."

	messages := []model.Message{
		{Role: model.RoleSystem, Content: systemPrompt},
		{Role: model.RoleUser, Content: userMsg},
	}

	for i := 0; i < r.maxIter; i++ {
		fmt.Printf("\n--- [depth=%d] Iteration %d/%d ---\n", r.depth, i+1, r.maxIter)

		resp, err := generateSync(ctx, r.model, messages)
		if err != nil {
			return "", fmt.Errorf("iteration %d: %w", i+1, err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty response at iteration %d", i+1)
		}

		content := resp.Choices[0].Message.Content
		messages = append(messages, model.Message{
			Role: model.RoleAssistant, Content: content,
		})

		fmt.Printf("  [LLM] %s\n", truncate(content, 200))

		codeBlocks := ExtractCodeBlocks(content)
		if len(codeBlocks) == 0 {
			messages = append(messages, model.Message{
				Role:    model.RoleUser,
				Content: "Please write code in a ```python code block to interact with the context. Call FINAL(answer) when done.",
			})
			continue
		}

		for _, code := range codeBlocks {
			result := repl.Execute(code)

			if result.Stdout != "" {
				fmt.Printf("  [REPL stdout] %s\n", truncate(result.Stdout, 300))
			}
			if result.Stderr != "" {
				fmt.Printf("  [REPL stderr] %s\n", truncate(result.Stderr, 300))
			}
			if result.FinalAnswer != "" {
				fmt.Printf("  [REPL] FINAL answer received (%d chars)\n", len(result.FinalAnswer))
				return result.FinalAnswer, nil
			}

			feedback := formatREPLResult(result)
			feedback += fmt.Sprintf("\n\n[iteration %d/%d, depth %d/%d]", i+1, r.maxIter, r.depth, r.maxDepth)
			messages = append(messages, model.Message{
				Role:    model.RoleUser,
				Content: feedback,
			})
		}
	}

	// Exhausted iterations — force a final answer.
	messages = append(messages, model.Message{
		Role:    model.RoleUser,
		Content: "Maximum iterations reached. Provide your best answer now based on what you have gathered.",
	})
	resp, err := generateSync(ctx, r.model, messages)
	if err != nil {
		return "", fmt.Errorf("final answer: %w", err)
	}
	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("no final answer produced")
}

// generateSync collects streaming responses into a single aggregated response.
func generateSync(ctx context.Context, llm model.Model, messages []model.Message) (*model.Response, error) {
	ch, err := llm.GenerateContent(ctx, &model.Request{Messages: messages})
	if err != nil {
		return nil, err
	}
	var final *model.Response
	for resp := range ch {
		if resp.Error != nil {
			return nil, fmt.Errorf("API error: %s", resp.Error.Message)
		}
		final = resp
	}
	if final == nil {
		return nil, fmt.Errorf("no response received")
	}
	return final, nil
}

func formatREPLResult(result *REPLResult) string {
	var parts []string
	if result.Stdout != "" {
		parts = append(parts, "Output:\n"+result.Stdout)
	}
	if result.Stderr != "" {
		parts = append(parts, "Error:\n"+result.Stderr)
	}
	if len(parts) == 0 {
		return "REPL result: (no output)"
	}
	return "REPL result:\n" + strings.Join(parts, "\n")
}
