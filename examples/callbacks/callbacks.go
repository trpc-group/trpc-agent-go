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
		RegisterBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, error) {
			fmt.Printf("🌐 Global BeforeModel: processing %d messages\n", len(req.Messages))
			return nil, nil
		}).
		RegisterAfterModel(func(ctx context.Context, req *model.Request, rsp *model.Response, modelErr error) (*model.Response, error) {
			if modelErr != nil {
				fmt.Printf("🌐 Global AfterModel: error occurred\n")
			} else {
				fmt.Printf("🌐 Global AfterModel: processed successfully\n")
			}
			return nil, nil
		})

	_ = tool.NewCallbacks().
		RegisterBeforeTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs *[]byte) (any, error) {
			fmt.Printf("🌐 Global BeforeTool: executing %s\n", toolName)
			// Note: jsonArgs is a pointer, so modifications will be visible to the caller.
			// This allows callbacks to modify tool arguments before execution.
			return nil, nil
		}).
		RegisterAfterTool(func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
			if runErr != nil {
				fmt.Printf("🌐 Global AfterTool: %s failed\n", toolName)
			} else {
				fmt.Printf("🌐 Global AfterTool: %s completed\n", toolName)
			}
			return nil, nil
		})

	_ = agent.NewCallbacks().
		RegisterBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
			fmt.Printf("🌐 Global BeforeAgent: starting %s\n", invocation.AgentName)
			return nil, nil
		}).
		RegisterAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
			if runErr != nil {
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
func (c *multiTurnChatWithCallbacks) createBeforeModelCallback() model.BeforeModelCallback {
	return func(ctx context.Context, req *model.Request) (*model.Response, error) {
		userMsg := c.extractLastUserMessage(req)
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
			return c.createCustomResponse(), nil
		}
		return nil, nil
	}
}

// createAfterModelCallback creates the after model callback.
func (c *multiTurnChatWithCallbacks) createAfterModelCallback() model.AfterModelCallback {
	return func(ctx context.Context, req *model.Request, resp *model.Response, runErr error) (*model.Response, error) {
		c.handleModelFinished(resp)
		c.demonstrateOriginalRequestAccess(req, resp)

		if c.shouldOverrideResponse(resp) {
			fmt.Printf("🟣 AfterModelCallback: triggered, overriding response for 'override me'.\n")
			return c.createOverrideResponse(), nil
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
func (c *multiTurnChatWithCallbacks) createBeforeToolCallback() tool.BeforeToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs *[]byte) (any, error) {
		if jsonArgs != nil {
			fmt.Printf("\n🟠 BeforeToolCallback: tool=%s, args=%s\n", toolName, string(*jsonArgs))
		} else {
			fmt.Printf("\n🟠 BeforeToolCallback: tool=%s, args=<nil>\n", toolName)
		}

		if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
			fmt.Printf("🟠 BeforeToolCallback: ✅ Invocation present in ctx (agent=%s, id=%s)\n", inv.AgentName, inv.InvocationID)
		} else {
			fmt.Printf("🟠 BeforeToolCallback: ❌ Invocation NOT found in ctx\n")
		}

		// Demonstrate argument modification capability.
		// Since jsonArgs is a pointer, we can modify the arguments that will be passed to the tool.
		if jsonArgs != nil && toolName == "calculator" {
			// Example: Add a timestamp to the arguments for logging purposes.
			originalArgs := string(*jsonArgs)
			modifiedArgs := fmt.Sprintf(`{"original":%s,"timestamp":"%d"}`, originalArgs, time.Now().Unix())
			*jsonArgs = []byte(modifiedArgs)
			fmt.Printf("🟠 BeforeToolCallback: Modified args for calculator: %s\n", modifiedArgs)
		}

		if jsonArgs != nil && c.shouldReturnCustomToolResult(toolName, *jsonArgs) {
			fmt.Println("\n🟠 BeforeToolCallback: triggered, custom result returned for calculator with 42.")
			return c.createCustomCalculatorResult(), nil
		}
		return nil, nil
	}
}

// createAfterToolCallback creates the after tool callback.
func (c *multiTurnChatWithCallbacks) createAfterToolCallback() tool.AfterToolCallback {
	return func(ctx context.Context, toolName string, toolDeclaration *tool.Declaration, jsonArgs []byte, result any, runErr error) (any, error) {
		fmt.Printf("\n🟤 AfterToolCallback: tool=%s, args=%s, result=%v, err=%v\n", toolName, string(jsonArgs), result, runErr)

		if c.shouldFormatTimeResult(toolName, result) {
			fmt.Println("\n🟤 AfterToolCallback: triggered, formatted result.")
			return c.formatTimeResult(result), nil
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
func (c *multiTurnChatWithCallbacks) createBeforeAgentCallback() agent.BeforeAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation) (*model.Response, error) {
		fmt.Printf("\n🟢 BeforeAgentCallback: agent=%s, invocationID=%s, userMsg=%q\n",
			invocation.AgentName,
			invocation.InvocationID,
			invocation.Message.Content,
		)
		return nil, nil
	}
}

// createAfterAgentCallback creates the after agent callback.
func (c *multiTurnChatWithCallbacks) createAfterAgentCallback() agent.AfterAgentCallback {
	return func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, error) {
		respContent := c.extractResponseContent(invocation)
		fmt.Printf("\n🟡 AfterAgentCallback: agent=%s, invocationID=%s, runErr=%v, userMsg=%q\n",
			invocation.AgentName,
			invocation.InvocationID,
			runErr,
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
