//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CodeExecutionResponseProcessor processes code execution responses from the model.
// It extracts code blocks from the model's response, executes them, and handles the results.
//
// Key features:
//   - Extracts code blocks (e.g., ```python ... ```) from the response
//   - Executes the code using the configured code executor
//   - If the response contains both code and FINAL_ANSWER, replaces the answer with execution result
//   - If execution fails, notifies the AI to retry
//   - If no FINAL_ANSWER, returns execution result to AI for further processing
type CodeExecutionResponseProcessor struct{}

// NewCodeExecutionResponseProcessor creates a new instance of CodeExecutionResponseProcessor.
func NewCodeExecutionResponseProcessor() *CodeExecutionResponseProcessor {
	return &CodeExecutionResponseProcessor{}
}

// ProcessResponse processes the model response by extracting and executing code blocks.
//
// The processing flow:
//  1. Extract code blocks from response content
//  2. Execute the code blocks
//  3. Handle the execution result:
//     - If FINAL_ANSWER exists: replace it with actual execution output
//     - If no FINAL_ANSWER: send result back to AI for continued processing
//     - If execution fails: notify AI to retry
func (p *CodeExecutionResponseProcessor) ProcessResponse(
	ctx context.Context, invocation *agent.Invocation, req *model.Request, rsp *model.Response, ch chan<- *event.Event) {
	// Validate inputs
	if invocation == nil || rsp == nil || rsp.IsPartial || len(rsp.Choices) == 0 {
		return
	}

	// Check if the agent supports code execution
	ce, ok := invocation.Agent.(agent.CodeExecutor)
	if !ok || ce == nil {
		return
	}
	executor := ce.CodeExecutor()
	if executor == nil {
		return
	}

	// Extract code blocks from the response
	originalContent := rsp.Choices[0].Message.Content
	codeBlocks := codeexecutor.ExtractCodeBlock(originalContent, executor.CodeBlockDelimiter())
	if len(codeBlocks) == 0 {
		return
	}

	// Check if response already contains FINAL_ANSWER
	hasFinalAnswer, _ := extractFinalAnswer(originalContent)

	// Emit code execution event (shows the code being executed)
	p.emitCodeEvent(ctx, invocation, ch, originalContent)

	// Execute the code
	result, err := executor.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks:  codeBlocks,
		ExecutionID: invocation.Session.ID,
	})

	if err != nil {
		p.handleExecutionError(ctx, invocation, ch, rsp, err)
		return
	}

	// Emit execution result event
	p.emitResultEvent(ctx, invocation, ch, result.String())

	// Handle the execution result based on whether FINAL_ANSWER exists
	executionOutput := extractExecutionOutput(result.String())

	if hasFinalAnswer && executionOutput != "" {
		// Replace the AI's answer with the actual execution result
		newContent := replaceFinalAnswer(originalContent, executionOutput)
		rsp.Choices[0].Message.Content = newContent
		// Keep Done status unchanged - this is the final response
		return
	}

	// No FINAL_ANSWER - send result back to AI for continued processing
	p.emitContinuationEvent(ctx, invocation, ch, result.String())

	// Clear original response to trigger another AI iteration
	rsp.Choices[0].Message.Content = ""
	rsp.Choices[0].Message.ToolCalls = nil
	rsp.Done = false
}

// emitCodeEvent emits an event showing the code being executed.
func (p *CodeExecutionResponseProcessor) emitCodeEvent(
	ctx context.Context, invocation *agent.Invocation, ch chan<- *event.Event, content string) {
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: content},
			}},
		}),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithTag(event.CodeExecutionTag),
	))
}

// emitResultEvent emits an event with the code execution result.
func (p *CodeExecutionResponseProcessor) emitResultEvent(
	ctx context.Context, invocation *agent.Invocation, ch chan<- *event.Event, result string) {
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: result},
			}},
		}),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithTag(event.CodeExecutionResultTag),
	))
}

// emitContinuationEvent emits a chat completion event to continue the conversation.
func (p *CodeExecutionResponseProcessor) emitContinuationEvent(
	ctx context.Context, invocation *agent.Invocation, ch chan<- *event.Event, result string) {
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I executed the Python code and got the following result:\n\n" + result,
				},
			}},
		}),
	))
}

// handleExecutionError handles code execution failures.
func (p *CodeExecutionResponseProcessor) handleExecutionError(
	ctx context.Context, invocation *agent.Invocation, ch chan<- *event.Event, rsp *model.Response, err error) {
	errorMsg := "Code execution failed: " + err.Error()

	// Emit error event
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: errorMsg},
			}},
		}),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithTag(event.CodeExecutionResultTag),
	))

	// Emit continuation event for AI to retry
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I attempted to execute the Python code but encountered an error:\n\n" + errorMsg,
				},
			}},
		}),
	))

	// Clear response to trigger retry
	rsp.Choices[0].Message.Content = ""
	rsp.Choices[0].Message.ToolCalls = nil
	rsp.Done = false
}

// extractFinalAnswer extracts the FINAL_ANSWER value from the content.
// Returns (true, answer) if found, (false, "") otherwise.
//
// Supported formats:
//   - /*FINAL_ANSWER*/ value /*...*/
//   - FINAL ANSWER: value
func extractFinalAnswer(content string) (bool, string) {
	// Pattern 1: /*FINAL_ANSWER*/ value /*...*/
	pattern1 := regexp.MustCompile(`(?is)/\*FINAL_ANSWER\*/\s*(.+?)(?:/\*[A-Z_]+\*/|\z)`)
	if matches := pattern1.FindStringSubmatch(content); len(matches) > 1 {
		return true, strings.TrimSpace(matches[1])
	}

	// Pattern 2: FINAL ANSWER: value
	pattern2 := regexp.MustCompile(`(?i)FINAL\s*ANSWER\s*:\s*(.+?)(?:\n|$)`)
	if matches := pattern2.FindStringSubmatch(content); len(matches) > 1 {
		return true, strings.TrimSpace(matches[1])
	}

	return false, ""
}

// extractExecutionOutput extracts the actual output value from code execution result.
// Example: "Code execution result:\n89706.00\n" -> "89706.00"
func extractExecutionOutput(result string) string {
	// Remove "Code execution result:" prefix
	result = strings.TrimPrefix(result, "Code execution result:")
	result = strings.TrimSpace(result)

	// Return first non-empty line
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return result
}

// replaceFinalAnswer replaces the FINAL_ANSWER value with a new value.
// Returns the modified content.
func replaceFinalAnswer(content, newValue string) string {
	// Pattern 1: /*FINAL_ANSWER*/ value /*...*/
	pattern1 := regexp.MustCompile(`(?is)(/\*FINAL_ANSWER\*/\s*).+?(/\*[A-Z_]+\*/|\z)`)
	if pattern1.MatchString(content) {
		return pattern1.ReplaceAllString(content, "${1}"+newValue+"\n${2}")
	}

	// Pattern 2: FINAL ANSWER: value
	pattern2 := regexp.MustCompile(`(?i)(FINAL\s*ANSWER\s*:\s*).+?(\n|$)`)
	if pattern2.MatchString(content) {
		return pattern2.ReplaceAllString(content, "${1}"+newValue+"${2}")
	}

	return content
}
