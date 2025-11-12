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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	// Global callback configurations using chain registration.
	// This demonstrates how to create reusable callback configurations.
	_ = model.NewCallbacks().
		RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			fmt.Printf("🌐 Global BeforeModel: processing %d messages\n", len(args.Request.Messages))
			return nil, nil
		}).
		RegisterAfterModel(func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			if args.Error != nil {
				fmt.Printf("🌐 Global AfterModel: error occurred\n")
			} else {
				fmt.Printf("🌐 Global AfterModel: processed successfully\n")
			}
			return nil, nil
		})

	_ = tool.NewCallbacks().
		RegisterBeforeTool(func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
			fmt.Printf("🌐 Global BeforeTool: executing %s\n", args.ToolName)
			// Note: args.Arguments is a slice, so modifications will be visible to the caller.
			// This allows callbacks to modify tool arguments before execution.
			return nil, nil
		}).
		RegisterAfterTool(func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
			if args.Error != nil {
				fmt.Printf("🌐 Global AfterTool: %s failed\n", args.ToolName)
			} else {
				fmt.Printf("🌐 Global AfterTool: %s completed\n", args.ToolName)
			}
			return nil, nil
		})

	_ = agent.NewCallbacks().
		RegisterBeforeAgent(func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
			fmt.Printf("🌐 Global BeforeAgent: starting %s\n", args.Invocation.AgentName)
			return nil, nil
		}).
		RegisterAfterAgent(func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
			if args.Error != nil {
				fmt.Printf("🌐 Global AfterAgent: execution failed\n")
			} else {
				fmt.Printf("🌐 Global AfterAgent: execution completed\n")
			}
			return nil, nil
		})
)

// createModelCallbacks creates and configures model callbacks.
func (c *multiTurnChatWithCallbacks) createModelCallbacks() *model.Callbacks {
	// Using traditional registration.
	modelCallbacks := model.NewCallbacks()
	modelCallbacks.RegisterBeforeModel(c.createBeforeModelCallback())
	modelCallbacks.RegisterAfterModel(c.createAfterModelCallback())
	return modelCallbacks
}

// createBeforeModelCallback creates the before model callback.
func (c *multiTurnChatWithCallbacks) createBeforeModelCallback() model.BeforeModelCallbackStructured {
	return func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		userMsg := c.extractLastUserMessage(args.Request)
		fmt.Printf("\n🔵 BeforeModelCallback: model=%s, lastUserMsg=%q\n",
			c.modelName,
			userMsg,
		)
		// You can get the invocation from the context.
		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
			fmt.Printf("🔵 BeforeModelCallback: ✅ Invocation present in ctx (agent=%s, id=%s)\n", inv.AgentName, inv.InvocationID)
		} else {
			fmt.Printf("🔵 BeforeModelCallback: ❌ Invocation NOT found in ctx\n")
		}

		if c.shouldReturnCustomResponse(userMsg) {
			fmt.Printf("🔵 BeforeModelCallback: triggered, returning custom response for 'custom model'.\n")
			return &model.BeforeModelResult{
				CustomResponse: c.createCustomResponse(),
			}, nil
		}
		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback.
func (c *multiTurnChatWithCallbacks) createAfterModelCallback() model.AfterModelCallbackStructured {
	return func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
		c.handleModelFinished(args.Response)
		c.demonstrateOriginalRequestAccess(args.Request, args.Response)

		if c.shouldOverrideResponse(args.Response) {
			fmt.Printf("🟣 AfterModelCallback: triggered, overriding response for 'override me'.\n")
			return &model.AfterModelResult{
				CustomResponse: c.createOverrideResponse(),
			}, nil
		}
		return nil, nil
	}
}

// createToolCallbacks creates and configures tool callbacks.
func (c *multiTurnChatWithCallbacks) createToolCallbacks() *tool.Callbacks {
	// Using traditional registration.
	toolCallbacks := tool.NewCallbacks()
	toolCallbacks.RegisterBeforeTool(c.createBeforeToolCallback())
	toolCallbacks.RegisterAfterTool(c.createAfterToolCallback())
	return toolCallbacks
}

// createBeforeToolCallback creates the before tool callback.
func (c *multiTurnChatWithCallbacks) createBeforeToolCallback() tool.BeforeToolCallbackStructured {
	return func(ctx context.Context, args *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
		if args.Arguments != nil {
			fmt.Printf("\n🟠 BeforeToolCallback: tool=%s, args=%s\n", args.ToolName, string(args.Arguments))
		} else {
			fmt.Printf("\n🟠 BeforeToolCallback: tool=%s, args=<nil>\n", args.ToolName)
		}

		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
			fmt.Printf("🟠 BeforeToolCallback: ✅ Invocation present in ctx (agent=%s, id=%s)\n", inv.AgentName, inv.InvocationID)
		} else {
			fmt.Printf("🟠 BeforeToolCallback: ❌ Invocation NOT found in ctx\n")
		}

		// Demonstrate argument modification capability.
		// Since args.Arguments is a slice, we can modify the arguments that will be passed to the tool.
		if args.Arguments != nil && args.ToolName == "calculator" {
			// Example: Add a timestamp to the arguments for logging purposes.
			originalArgs := string(args.Arguments)
			modifiedArgs := fmt.Sprintf(`{"original":%s,"timestamp":"%d"}`, originalArgs, time.Now().Unix())
			args.Arguments = []byte(modifiedArgs)
			fmt.Printf("🟠 BeforeToolCallback: Modified args for calculator: %s\n", modifiedArgs)
		}

		if args.Arguments != nil && c.shouldReturnCustomToolResult(args.ToolName, args.Arguments) {
			fmt.Println("\n🟠 BeforeToolCallback: triggered, custom result returned for calculator with 42.")
			return &tool.BeforeToolResult{
				CustomResult: c.createCustomCalculatorResult(),
			}, nil
		}
		return nil, nil
	}
}

// createAfterToolCallback creates the after tool callback.
func (c *multiTurnChatWithCallbacks) createAfterToolCallback() tool.AfterToolCallbackStructured {
	return func(ctx context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
		fmt.Printf("\n🟤 AfterToolCallback: tool=%s, args=%s, result=%v, err=%v\n", args.ToolName, string(args.Arguments), args.Result, args.Error)

		if c.shouldFormatTimeResult(args.ToolName, args.Result) {
			fmt.Println("\n🟤 AfterToolCallback: triggered, formatted result.")
			return &tool.AfterToolResult{
				CustomResult: c.formatTimeResult(args.Result),
			}, nil
		}
		return nil, nil
	}
}

// createAgentCallbacks creates and configures agent callbacks.
func (c *multiTurnChatWithCallbacks) createAgentCallbacks() *agent.Callbacks {
	// Using traditional registration.
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(c.createBeforeAgentCallback())
	agentCallbacks.RegisterAfterAgent(c.createAfterAgentCallback())
	return agentCallbacks
}

// createBeforeAgentCallback creates the before agent callback.
func (c *multiTurnChatWithCallbacks) createBeforeAgentCallback() agent.BeforeAgentCallbackStructured {
	return func(ctx context.Context, args *agent.BeforeAgentArgs) (*agent.BeforeAgentResult, error) {
		fmt.Printf("\n🟢 BeforeAgentCallback: agent=%s, invocationID=%s, userMsg=%q\n",
			args.Invocation.AgentName,
			args.Invocation.InvocationID,
			args.Invocation.Message.Content,
		)
		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback.
func (c *multiTurnChatWithCallbacks) createAfterAgentCallback() agent.AfterAgentCallbackStructured {
	return func(ctx context.Context, args *agent.AfterAgentArgs) (*agent.AfterAgentResult, error) {
		respContent := c.extractResponseContent(args.Invocation)
		fmt.Printf("\n🟡 AfterAgentCallback: agent=%s, invocationID=%s, runErr=%v, userMsg=%q\n",
			args.Invocation.AgentName,
			args.Invocation.InvocationID,
			args.Error,
			respContent,
		)
		return nil, nil
	}
}

// Helper functions for callback logic.

func (c *multiTurnChatWithCallbacks) extractLastUserMessage(req *model.Request) string {
	if len(req.Messages) > 0 {
		return req.Messages[len(req.Messages)-1].Content
	}
	return ""
}

func (c *multiTurnChatWithCallbacks) shouldReturnCustomResponse(userMsg string) bool {
	return userMsg != "" && strings.Contains(userMsg, "custom model")
}

func (c *multiTurnChatWithCallbacks) createCustomResponse() *model.Response {
	return &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "[This is a custom response from before model callback]",
			},
		}},
	}
}

func (c *multiTurnChatWithCallbacks) handleModelFinished(resp *model.Response) {
	if resp != nil && resp.Done {
		fmt.Printf("\n🟣 AfterModelCallback: model=%s has finished\n", c.modelName)
	}
}

func (c *multiTurnChatWithCallbacks) demonstrateOriginalRequestAccess(req *model.Request, resp *model.Response) {
	// Only demonstrate when the response is complete (Done=true) to avoid multiple triggers during streaming.
	if resp == nil || !resp.Done {
		return
	}

	if req != nil && len(req.Messages) > 0 {
		lastUserMsg := req.Messages[len(req.Messages)-1].Content
		if strings.Contains(lastUserMsg, "original request") {
			fmt.Printf("🟣 AfterModelCallback: detected 'original request' in user message: %q\n", lastUserMsg)
			fmt.Printf("🟣 AfterModelCallback: this demonstrates access to the original request in after callback.\n")
		}
	}
}

func (c *multiTurnChatWithCallbacks) shouldOverrideResponse(resp *model.Response) bool {
	return resp != nil && len(resp.Choices) > 0 && strings.Contains(resp.Choices[0].Message.Content, "override me")
}

func (c *multiTurnChatWithCallbacks) createOverrideResponse() *model.Response {
	return &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: "[This response was overridden by after model callback]",
			},
		}},
	}
}

func (c *multiTurnChatWithCallbacks) shouldReturnCustomToolResult(toolName string, jsonArgs []byte) bool {
	return toolName == "calculator" && strings.Contains(string(jsonArgs), "42")
}

func (c *multiTurnChatWithCallbacks) createCustomCalculatorResult() calculatorResult {
	return calculatorResult{
		Operation: "custom",
		A:         42,
		B:         42,
		Result:    4242,
	}
}

func (c *multiTurnChatWithCallbacks) shouldFormatTimeResult(toolName string, _ any) bool {
	return toolName == "current_time"
}

func (c *multiTurnChatWithCallbacks) formatTimeResult(result any) any {
	if timeResult, ok := result.(timeResult); ok {
		timeResult.Formatted = fmt.Sprintf("%s %s (%s)", timeResult.Date, timeResult.Time, timeResult.Timezone)
		return timeResult
	}
	return result
}

func (c *multiTurnChatWithCallbacks) extractResponseContent(invocation *agent.Invocation) string {
	if invocation != nil && invocation.Message.Content != "" {
		return invocation.Message.Content
	}
	return "<nil>"
}
