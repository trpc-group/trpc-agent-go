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
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// RLM implements a ReAct-driven Recursive Language Model agent.
// The LLM autonomously decides what to do next via tool calls:
// execute_code, rlm_query, or final_answer.
type RLM struct {
	model       model.Model
	serviceAddr string
	depth       int
	maxDepth    int
	agentID     string
	limiter     *RateLimiter
	rootQuery   string
}

const modelCallTimeout = 2 * time.Minute

// Run executes the ReAct agent loop driven by tool calling.
func (r *RLM) Run(ctx context.Context, query, promptContext, boundary, stopCondition string) (string, error) {
	repl := NewREPL(promptContext, r.serviceAddr, r.depth, r.rootQuery)
	tools := r.buildTools(repl)
	toolMap := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		toolMap[t.Declaration().Name] = t
	}

	systemPrompt := BuildSystemPrompt(len(promptContext), countLines(promptContext),
		r.depth, r.maxDepth)

	userMsg := buildUserMessage(r.rootQuery, query, r.agentID, r.depth,
		len(promptContext), countLines(promptContext), boundary, stopCondition)

	messages := []model.Message{
		{Role: model.RoleSystem, Content: systemPrompt},
		{Role: model.RoleUser, Content: userMsg},
	}

	for step := 1; ; step++ {
		log.Printf("[%s] step %d: calling LLM (%d messages)", r.agentID, step, len(messages))

		if err := r.limiter.Wait(ctx); err != nil {
			return "", fmt.Errorf("step %d rate limit: %w", step, err)
		}
		resp, err := generateSync(ctx, r.model, messages, toolMap)
		if err != nil {
			return "", fmt.Errorf("step %d: %w", step, err)
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty response at step %d", step)
		}

		choice := resp.Choices[0]

		if len(choice.Message.ToolCalls) == 0 {
			log.Printf("[%s] step %d: LLM returned text (%d chars), treating as final answer",
				r.agentID, step, len(choice.Message.Content))
			return choice.Message.Content, nil
		}

		toolNames := make([]string, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			toolNames[i] = tc.Function.Name
		}
		log.Printf("[%s] step %d: LLM requested %d tool call(s): %v",
			r.agentID, step, len(choice.Message.ToolCalls), toolNames)

		messages = append(messages, choice.Message)

		for _, tc := range choice.Message.ToolCalls {
			log.Printf("[%s] step %d: executing %s", r.agentID, step, tc.Function.Name)
			result, err := r.executeTool(ctx, toolMap, tc)
			if err != nil {
				return "", fmt.Errorf("tool %s: %w", tc.Function.Name, err)
			}

			if tc.Function.Name == "final_answer" {
				var args struct {
					Answer string `json:"answer"`
				}
				json.Unmarshal(tc.Function.Arguments, &args)
				log.Printf("[%s] step %d: final_answer received (%d chars)", r.agentID, step, len(args.Answer))
				return args.Answer, nil
			}

			log.Printf("[%s] step %d: %s result (%d chars)", r.agentID, step, tc.Function.Name, len(result))
			messages = append(messages, model.Message{
				Role:    model.RoleTool,
				ToolID:  tc.ID,
				Content: result,
			})
		}
	}

}

func (r *RLM) executeTool(ctx context.Context, toolMap map[string]tool.Tool, tc model.ToolCall) (string, error) {
	t, ok := toolMap[tc.Function.Name]
	if !ok {
		return fmt.Sprintf(`{"error": "unknown tool: %s"}`, tc.Function.Name), nil
	}
	callable, ok := t.(tool.CallableTool)
	if !ok {
		return `{"error": "tool is not callable"}`, nil
	}
	result, err := callable.Call(ctx, tc.Function.Arguments)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error()), nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf(`{"result": %q}`, fmt.Sprint(result)), nil
	}
	return string(data), nil
}

func generateSync(ctx context.Context, llm model.Model, messages []model.Message, tools map[string]tool.Tool) (*model.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, modelCallTimeout)
	defer cancel()

	req := &model.Request{
		Messages: messages,
		Tools:    tools,
	}

	const maxRetries = 5
	baseDelay := 5 * time.Second

	for attempt := 0; ; attempt++ {
		ch, err := llm.GenerateContent(ctx, req)
		if err != nil {
			return nil, err
		}
		var final *model.Response
		var apiErr error
		for resp := range ch {
			if resp.Error != nil {
				apiErr = fmt.Errorf("API error: %s", resp.Error.Message)
				continue
			}
			final = resp
		}

		if apiErr != nil {
			if attempt < maxRetries && isRateLimitError(apiErr) {
				delay := baseDelay * time.Duration(1<<attempt)
				if delay > time.Minute {
					delay = time.Minute
				}
				log.Printf("[retry] 429 rate limited, attempt %d/%d, waiting %s", attempt+1, maxRetries, delay)
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, apiErr
		}
		if final == nil {
			return nil, fmt.Errorf("no response received")
		}
		return final, nil
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "429") || strings.Contains(s, "Too Many Requests") || strings.Contains(s, "rate limit")
}

func buildUserMessage(rootQuery, query, agentID string, depth, contextLen, contextLines int, boundary, stopCondition string) string {
	var b strings.Builder

	if depth == 0 {
		b.WriteString(fmt.Sprintf("## Task\n%s\n", query))
	} else {
		b.WriteString(fmt.Sprintf("## Original Task (from root)\n%s\n", rootQuery))
		if query != rootQuery {
			b.WriteString(fmt.Sprintf("\n## Your Sub-task (from parent)\n%s\n", query))
		}
	}

	b.WriteString(fmt.Sprintf("\n## Agent Info\n- Agent ID: %s\n- Depth: %d\n- Context: %d chars, %d lines\n",
		agentID, depth, contextLen, contextLines))

	if boundary != "" {
		b.WriteString(fmt.Sprintf("\n## Scope\n%s\n", boundary))
	}
	if stopCondition != "" {
		b.WriteString(fmt.Sprintf("\n## Stop Condition\n%s\n", stopCondition))
	}

	b.WriteString("\nA `context` variable is loaded in the REPL. Start by inspecting it with execute_code.")
	return b.String()
}
