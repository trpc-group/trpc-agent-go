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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var nonExecutableCodeLanguages = map[string]struct{}{
	"":          {},
	"css":       {},
	"csv":       {},
	"html":      {},
	"json":      {},
	"markdown":  {},
	"md":        {},
	"mermaid":   {},
	"plain":     {},
	"plaintext": {},
	"text":      {},
	"txt":       {},
	"xml":       {},
	"yaml":      {},
	"yml":       {},
}

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
	codeBlocks := autoExecutableCodeBlocks(
		content,
		e.CodeBlockDelimiter(),
	)
	if len(codeBlocks) == 0 {
		return
	}
	truncatedContent := content // todo: truncate the content

	//  [Step 2] Executes the code and emit 2 Events for code and execution result.
	agent.EmitEvent(ctx, invocation, ch, event.New(
		invocation.InvocationID,
		invocation.AgentName,
		event.WithResponse(&model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{Role: model.RoleAssistant, Content: truncatedContent},
				},
			},
		}),
		event.WithObject(model.ObjectTypePostprocessingCodeExecution),
		event.WithTag(event.CodeExecutionTag),
	))

	codeExecutionResult, err := e.ExecuteCode(ctx, codeexecutor.CodeExecutionInput{
		CodeBlocks:  codeBlocks,
		ExecutionID: invocation.Session.ID,
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
	//  [Step 3] Skip processing the original model response to continue code generation loop.
	rsp.Choices[0].Message.Content = ""
}

func autoExecutableCodeBlocks(
	content string,
	delimiter codeexecutor.CodeBlockDelimiter,
) []codeexecutor.CodeBlock {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	blocks := codeexecutor.ExtractCodeBlock(trimmed, delimiter)
	if len(blocks) != 1 {
		return nil
	}

	block := blocks[0]
	if !isAutoExecutableCodeLanguage(block.Language) {
		return nil
	}

	expected := delimiter.Start + strings.TrimSpace(block.Language) +
		"\n" + block.Code + delimiter.End
	if trimmed != expected {
		return nil
	}

	return blocks
}

func isAutoExecutableCodeLanguage(language string) bool {
	normalized := strings.ToLower(strings.TrimSpace(language))
	_, blocked := nonExecutableCodeLanguages[normalized]
	return !blocked
}
