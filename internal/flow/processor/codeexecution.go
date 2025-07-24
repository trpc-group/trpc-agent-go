package processor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type CodeExecutionResponseProcessor struct {
}

func NewCodeExecutionResponseProcessor() *CodeExecutionResponseProcessor {
	return &CodeExecutionResponseProcessor{}
}

func (p *CodeExecutionResponseProcessor) ProcessResponse(
	ctx context.Context, invocation *agent.Invocation, rsp *model.Response, ch chan<- *event.Event) {
	log.Infof("CodeExecutionResponseProcessor: sent post event")
	ce, ok := invocation.Agent.(agent.CodeExecutor)
	if !ok || ce == nil {
		log.Info("CodeExecutionResponseProcessor: Agent does not implement CodeExecutor interface, skipping code execution.")
		return
	}
	e := ce.CodeExecutor()
	if e == nil {
		log.Info("CodeExecutionResponseProcessor: CodeExecutor is nil, skipping code execution.")
		return
	}

	// [Step 1] Extract code from the model predict response
	if rsp.IsPartial {
		log.Info("CodeExecutionResponseProcessor: Skipping partial response.")
		return
	}
	if len(rsp.Choices) == 0 {
		log.Info("CodeExecutionResponseProcessor: No choices in response, skipping code execution.")
		return
	}

	codeBlocks := codeexecutor.ExtractCodeBlock(rsp.Choices[0].Message.Content, e.CodeBlockDelimiter())
	if len(codeBlocks) == 0 {
		log.Info("CodeExecutionResponseProcessor: No code blocks found in response, skipping code execution.")
		return
	}

	//  [Step 2] Executes the code and emits Events for execution result.
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
					Message: model.Message{Role: model.RoleAssistant, Content: codeexecutor.BuildCodeExecutionResult(codeExecutionResult)},
				},
			},
		}))

}
