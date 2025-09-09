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

// NewCodeExecutionResponseProcessor creates a new instance of CodeExecutionResponseProcessor.
// This processor is responsible for handling code execution responses from the model.
func NewCodeExecutionResponseProcessor() *CodeExecutionResponseProcessor {
	return &CodeExecutionResponseProcessor{}
}

// ProcessResponse processes the model response, extracts code blocks, executes them,
// and emits events for the code execution result.
func (p *CodeExecutionResponseProcessor) ProcessResponse(
	ctx context.Context, invocation *agent.Invocation, req *model.Request, rsp *model.Response, ch chan<- *event.Event) {
	ce, ok := invocation.Agent.(agent.CodeExecutor)
	if !ok || ce == nil {
		return
	}
	e := ce.CodeExecutor()
	if e == nil {
		return
	}

	// [Step 1] Extract code from the model predict response,
	// and truncate the content to the part with the first code block().
	if rsp.IsPartial {
		return
	}
	if len(rsp.Choices) == 0 {
		return
	}

	codeBlocks := codeexecutor.ExtractCodeBlock(rsp.Choices[0].Message.Content, e.CodeBlockDelimiter())
	if len(codeBlocks) == 0 {
		return
	}
	truncatedContent := rsp.Choices[0].Message.Content // todo: truncate the content

	//  [Step 2] Executes the code and emit 2 Events for code and execution result.
	ch <- event.New(invocation.InvocationID, invocation.AgentName, event.WithBranch(invocation.Branch),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: truncatedContent},
				},
			},
		}))

	codeExecutionResult, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks:  codeBlocks,
		ExecutionID: invocation.Session.ID,
	})
	if err != nil {
		ch <- event.New(invocation.InvocationID, invocation.AgentName, event.WithBranch(invocation.Branch),
			event.WithObject(model.ObjectTypePostprocessingCodeExecution),
			event.WithResponse(&model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{Role: model.RoleAssistant, Content: "Code execution failed: " + err.Error()},
					},
				},
			}))
		return
	}
	ch <- event.New(invocation.InvocationID, invocation.AgentName, event.WithBranch(invocation.Branch),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: codeExecutionResult.String()},
				},
			},
		}))
	//  [Step 3] Skip processing the original model response to continue code generation loop.
	rsp.Choices[0].Message.Content = ""
}
