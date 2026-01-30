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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CodeExecutionResponseProcessor processes code execution responses from the model.
type CodeExecutionResponseProcessor struct {
}

const codeExecutionPayloadStateKey = "processor:code_execution_payload"

type codeExecutionPayload struct {
	truncatedContent string
	codeBlocks       []codeexecutor.CodeBlock
}

// NewCodeExecutionResponseProcessor creates a new instance of CodeExecutionResponseProcessor.
// This processor is responsible for handling code execution responses from the model.
func NewCodeExecutionResponseProcessor() *CodeExecutionResponseProcessor {
	return &CodeExecutionResponseProcessor{}
}

// PrepareCodeExecutionResponse captures code execution payload and clears the response content before emission.
func PrepareCodeExecutionResponse(invocation *agent.Invocation, rsp *model.Response) {
	if invocation == nil || rsp == nil || rsp.IsPartial {
		return
	}
	ce, ok := invocation.Agent.(agent.CodeExecutor)
	if !ok || ce == nil {
		return
	}
	e := ce.CodeExecutor()
	if e == nil {
		return
	}
	if len(rsp.Choices) == 0 {
		return
	}

	content := rsp.Choices[0].Message.Content
	codeBlocks := codeexecutor.ExtractCodeBlock(content, e.CodeBlockDelimiter())
	if len(codeBlocks) == 0 {
		return
	}
	invocation.SetState(codeExecutionPayloadStateKey, &codeExecutionPayload{
		truncatedContent: content, // TODO: Truncate the content if needed.
		codeBlocks:       codeBlocks,
	})
	rsp.Choices[0].Message.Content = ""
}

// ProcessResponse processes the model response, extracts code blocks, executes them,
// and emits events for the code execution result.
func (p *CodeExecutionResponseProcessor) ProcessResponse(
	ctx context.Context, invocation *agent.Invocation, req *model.Request, rsp *model.Response, ch chan<- *event.Event) {
	if invocation == nil || rsp == nil || rsp.IsPartial {
		return
	}
	raw, ok := invocation.GetState(codeExecutionPayloadStateKey)
	if !ok || raw == nil {
		return
	}
	payload, ok := raw.(*codeExecutionPayload)
	if !ok || payload == nil {
		return
	}
	ce, ok := invocation.Agent.(agent.CodeExecutor)
	if !ok || ce == nil {
		return
	}
	e := ce.CodeExecutor()
	if e == nil {
		return
	}

	if len(rsp.Choices) == 0 {
		return
	}

	if len(payload.codeBlocks) == 0 {
		return
	}

	//  [Step 2] Executes the code and emit 2 Events for code and execution result.
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: payload.truncatedContent},
				},
			},
		}),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithTag(event.CodeExecutionTag),
	))

	executionID := invocation.InvocationID
	if invocation.Session != nil {
		executionID = invocation.Session.ID
	}
	codeExecutionResult, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks:  payload.codeBlocks,
		ExecutionID: executionID,
	})
	if err != nil {
		agent.EmitEvent(ctx, invocation, ch, event.New(
			invocation.InvocationID,
			invocation.AgentName,
			event.WithResponse(&model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{Role: model.RoleAssistant, Content: "Code execution failed: " + err.Error()},
					},
				},
			}),
			event.WithObject(model.ObjectTypePostprocessingCodeExecution),
			event.WithTag(event.CodeExecutionResultTag), // Add tag for error result
		))
		return
	}
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: codeExecutionResult.String()},
				},
			},
		}),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithTag(event.CodeExecutionResultTag),
	))
}
