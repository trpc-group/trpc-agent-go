package inference

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Inference executes the agent against the provided invocations and converts
// streamed events into evalset-compatible invocation records.
func Inference(
	ctx context.Context,
	invocations []*evalset.Invocation,
	agent agent.Agent,
	initialSession *evalset.SessionInput,
	sessionID string,
	sessionService session.Service,
	artifactService artifact.Service,
	memoryService memory.Service,
) ([]*evalset.Invocation, error) {
	if initialSession == nil {
		return nil, fmt.Errorf("session input is nil")
	}
	r := runner.NewRunner(
		initialSession.AppName,
		agent,
		runner.WithSessionService(sessionService),
		runner.WithArtifactService(artifactService),
	)
	responseInvocations := make([]*evalset.Invocation, 0, len(invocations))
	for _, invocation := range invocations {
		responseInvocation, err := runInferenceForInvocation(ctx, r, initialSession.UserID, sessionID, invocation)
		if err != nil {
			return nil, err
		}
		responseInvocations = append(responseInvocations, responseInvocation)
	}
	return responseInvocations, nil
}

func runInferenceForInvocation(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	invocation *evalset.Invocation,
) (*evalset.Invocation, error) {
	if err := validateInvocation(invocation); err != nil {
		return nil, err
	}

	return buildInvocationFromRun(ctx, r, userID, sessionID, invocation)
}

func buildInvocationFromRun(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	invocation *evalset.Invocation,
) (*evalset.Invocation, error) {
	events, err := r.Run(ctx, userID, sessionID, model.Message{Content: invocation.UserContent.Parts[0].Text})
	if err != nil {
		return nil, err
	}

	var finalResponse model.Message
	toolUses := make([]evalset.FunctionCall, 0)
	invocationID := ""

	for event := range events {
		if event == nil {
			continue
		}
		if invocationID == "" && event.InvocationID != "" {
			invocationID = event.InvocationID
		}

		if event.Done && event.Response != nil && len(event.Response.Choices) > 0 {
			finalResponse = event.Response.Choices[0].Message
			continue
		}

		if event.Response != nil && len(event.Response.Choices) > 0 {
			message := event.Response.Choices[0].Message
			if message.ToolCalls != nil {
				for _, toolCall := range message.ToolCalls {
					if toolCall.Function.Name == "" {
						continue
					}
					var args map[string]interface{}
					if len(toolCall.Function.Arguments) > 0 {
						if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
							return nil, fmt.Errorf("unmarshal tool arguments: %w", err)
						}
					}
					toolUses = append(toolUses, evalset.FunctionCall{
						ID:   toolCall.ID,
						Name: toolCall.Function.Name,
						Args: args,
					})
				}
			}
		}
	}

	return &evalset.Invocation{
		InvocationID:  invocationID,
		UserContent:   invocation.UserContent,
		FinalResponse: messageToContent(finalResponse),
		IntermediateData: &evalset.IntermediateData{
			ToolUses: toolUses,
		},
	}, nil
}

func messageToContent(finalResponse model.Message) *evalset.Content {
	if finalResponse.Role == "" && finalResponse.Content == "" && len(finalResponse.ToolCalls) == 0 {
		return nil
	}
	// Create a copy to avoid referencing loop variable when used in callers.
	resp := finalResponse
	return &evalset.Content{
		Role: string(resp.Role),
		Parts: []evalset.Part{{
			Text: resp.Content,
		}},
	}
}

func validateInvocation(invocation *evalset.Invocation) error {
	if invocation.UserContent == nil {
		return fmt.Errorf("invocation user content is nil for eval case invocation %q", invocation.InvocationID)
	}
	if len(invocation.UserContent.Parts) == 0 {
		return fmt.Errorf("user content parts are empty for eval case invocation %q", invocation.InvocationID)
	}
	return nil
}
