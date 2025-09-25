package inference

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Inference executes the agent against the provided invocations.
func Inference(
	ctx context.Context,
	agent agent.Agent,
	invocations []*evalset.Invocation,
	initialSession *evalset.SessionInput,
	sessionID string,
	opt ...runner.Option,
) ([]*evalset.Invocation, error) {
	if len(invocations) == 0 {
		return nil, fmt.Errorf("invocations are empty")
	}
	if initialSession == nil {
		return nil, fmt.Errorf("session input is nil")
	}
	r := runner.NewRunner(initialSession.AppName, agent, opt...)
	// Accumulate each invocation response.
	responseInvocations := make([]*evalset.Invocation, 0, len(invocations))
	for _, invocation := range invocations {
		responseInvocation, err := inferencePerInvocation(ctx, r, initialSession.UserID, sessionID, invocation)
		if err != nil {
			return nil, err
		}
		responseInvocations = append(responseInvocations, responseInvocation)
	}
	return responseInvocations, nil
}

// inferencePerInvocation executes the agent for a single invocation.
func inferencePerInvocation(
	ctx context.Context,
	r runner.Runner,
	userID string,
	sessionID string,
	invocation *evalset.Invocation,
) (*evalset.Invocation, error) {
	if invocation.UserContent == nil {
		return nil, fmt.Errorf("invocation user content is nil for eval case invocation %q", invocation.InvocationID)
	}
	if len(invocation.UserContent.Parts) == 0 {
		return nil, fmt.Errorf("user content parts are empty for eval case invocation %q", invocation.InvocationID)
	}
	// Convert the evalset content into a model message.
	message, err := convertContentToMessage(invocation.UserContent)
	if err != nil {
		return nil, fmt.Errorf("convert content to message: %w", err)
	}
	events, err := r.Run(ctx, userID, sessionID, *message)
	if err != nil {
		return nil, fmt.Errorf("runner run: %w", err)
	}
	// Capture the invocation ID, final response, and tool uses.
	var (
		invocationID  string
		finalResponse *model.Message
		toolUses      []*evalset.FunctionCall
	)
	for event := range events {
		if event == nil {
			continue
		}
		if event.Error != nil {
			return nil, fmt.Errorf("event error: %w", event.Error)
		}
		// Capture the invocation ID.
		if invocationID == "" && event.InvocationID != "" {
			invocationID = event.InvocationID
		}
		// Capture the final response.
		if event.IsFinalResponse() {
			finalResponse = &event.Response.Choices[0].Message
			continue
		}
		// Capture tool call uses.
		if event.IsToolCallResponse() {
			toolCall := event.Response.Choices[0].Message.ToolCalls[0]
			toolUse, err := convertToolCallsToFunctionCalls(&toolCall)
			if err != nil {
				return nil, fmt.Errorf("convert tool calls to function calls: %w", err)
			}
			toolUses = append(toolUses, toolUse)
		}
	}
	// Convert the final response to evalset content.
	finalContent, err := convertMessageToContent(finalResponse)
	if err != nil {
		return nil, fmt.Errorf("convert message to content: %w", err)
	}
	return &evalset.Invocation{
		InvocationID:  invocationID,
		UserContent:   invocation.UserContent,
		FinalResponse: finalContent,
		IntermediateData: &evalset.IntermediateData{
			ToolUses: toolUses,
		},
	}, nil
}

// convertContentToMessage transforms evalset input content into a model message.
func convertContentToMessage(content *evalset.Content) (*model.Message, error) {
	if content == nil {
		return nil, fmt.Errorf("content is nil")
	}
	if len(content.Parts) == 0 {
		return nil, fmt.Errorf("content parts are empty")
	}
	if content.Parts[0].Text == "" {
		return nil, fmt.Errorf("content part text is empty")
	}
	return &model.Message{
		Role:    model.Role(content.Role),
		Content: content.Parts[0].Text,
	}, nil
}

// convertMessageToContent converts the model response back into evalset content.
func convertMessageToContent(finalResponse *model.Message) (*evalset.Content, error) {
	if finalResponse == nil {
		return nil, fmt.Errorf("final response is nil")
	}
	if finalResponse.Content == "" {
		return nil, fmt.Errorf("final response content is empty")
	}
	return &evalset.Content{
		Role: finalResponse.Role,
		Parts: []evalset.Part{{
			Text: finalResponse.Content,
		}},
	}, nil
}

// convertToolCallsToFunctionCalls maps model-level tool calls to the evalset FunctionCall structure.
func convertToolCallsToFunctionCalls(toolCalls *model.ToolCall) (*evalset.FunctionCall, error) {
	if toolCalls == nil {
		return nil, fmt.Errorf("tool calls is nil")
	}
	if toolCalls.Function.Name == "" {
		return nil, fmt.Errorf("tool call function name is empty")
	}
	var args map[string]interface{}
	if len(toolCalls.Function.Arguments) > 0 {
		if err := json.Unmarshal(toolCalls.Function.Arguments, &args); err != nil {
			return nil, fmt.Errorf("unmarshal tool arguments: %w", err)
		}
	}
	return &evalset.FunctionCall{
		ID:   toolCalls.ID,
		Name: toolCalls.Function.Name,
		Args: args,
	}, nil
}
