//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalset

import (
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// EvalCase represents a single evaluation case.
type EvalCase struct {
	// EvalID uniquely identifies this evaluation case.
	EvalID string `json:"evalId,omitempty"`
	// Conversation contains the sequence of invocations.
	Conversation []*Invocation `json:"conversation,omitempty"`
	// SessionInput contains initialization data for the session.
	SessionInput *SessionInput `json:"sessionInput,omitempty"`
	// CreationTimestamp when this eval case was created.
	CreationTimestamp *epochtime.EpochTime `json:"creationTimestamp,omitempty"`
}

// Invocation represents a single invocation in a conversation.
type Invocation struct {
	// InvocationID uniquely identifies this invocation.
	InvocationID string `json:"invocationId,omitempty"`
	// UserContent represents the user's input.
	UserContent *model.Message `json:"userContent,omitempty"`
	// FinalResponse represents the agent's final response.
	FinalResponse *model.Message `json:"finalResponse,omitempty"`
	// IntermediateData contains intermediate steps during execution.
	IntermediateData *IntermediateData `json:"intermediateData,omitempty"`
	// CreationTimestamp when this invocation was created.
	CreationTimestamp *epochtime.EpochTime `json:"creationTimestamp,omitempty"`
}

// IntermediateData contains intermediate execution data.
type IntermediateData struct {
	// ToolCalls represents tool calls made during execution.
	ToolCalls []*model.ToolCall `json:"toolCalls,omitempty"`
	// ToolResponses represents tool responses made during execution.
	ToolResponses []*model.Message `json:"toolResponses,omitempty"`
	// IntermediateResponses represents intermediate responses, including text responses and tool responses.
	IntermediateResponses []*model.Message `json:"intermediateResponses,omitempty"`
}

// SessionInput represents values that help initialize a session.
type SessionInput struct {
	// AppName identifies the app.
	AppName string `json:"appName,omitempty"`
	// UserID identifies the user.
	UserID string `json:"userId,omitempty"`
	// State contains the initial state of the session.
	State map[string]any `json:"state,omitempty"`
}

// MarshalJSON marshals intermediate data while keeping tool arguments and responses decoded as JSON objects.
func (i *IntermediateData) MarshalJSON() ([]byte, error) {
	out := intermediateDataJSON{
		IntermediateResponses: i.IntermediateResponses,
	}
	for _, toolCall := range i.ToolCalls {
		if toolCall == nil {
			continue
		}
		call := toolCallJSON{
			ID:   toolCall.ID,
			Type: toolCall.Type,
			Function: functionJSON{
				Name:        toolCall.Function.Name,
				Strict:      toolCall.Function.Strict,
				Description: toolCall.Function.Description,
			},
		}
		if len(toolCall.Function.Arguments) > 0 {
			if err := json.Unmarshal(toolCall.Function.Arguments, &call.Function.Arguments); err != nil {
				return nil, fmt.Errorf("unmarshal tool call arguments: %w", err)
			}
		}
		out.ToolCalls = append(out.ToolCalls, call)
	}
	for _, toolResponse := range i.ToolResponses {
		if toolResponse == nil {
			continue
		}
		resp := toolResponseJSON{
			Role:     toolResponse.Role,
			ToolID:   toolResponse.ToolID,
			ToolName: toolResponse.ToolName,
		}
		if toolResponse.Content != "" {
			if err := json.Unmarshal([]byte(toolResponse.Content), &resp.Content); err != nil {
				return nil, fmt.Errorf("unmarshal tool response content: %w", err)
			}
		}
		out.ToolResponses = append(out.ToolResponses, resp)
	}
	return json.Marshal(&out)
}

// UnmarshalJSON unmarshals intermediate data while re-encoding tool arguments and responses as raw JSON strings.
func (i *IntermediateData) UnmarshalJSON(data []byte) error {
	var raw intermediateDataJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal intermediate data: %w", err)
	}
	i.IntermediateResponses = raw.IntermediateResponses
	for _, toolCall := range raw.ToolCalls {
		args, err := json.Marshal(toolCall.Function.Arguments)
		if err != nil {
			return fmt.Errorf("unmarshal tool call arguments: %w", err)
		}
		i.ToolCalls = append(i.ToolCalls, &model.ToolCall{
			ID:   toolCall.ID,
			Type: toolCall.Type,
			Function: model.FunctionDefinitionParam{
				Name:        toolCall.Function.Name,
				Strict:      toolCall.Function.Strict,
				Description: toolCall.Function.Description,
				Arguments:   args,
			},
		})
	}
	for _, toolResponse := range raw.ToolResponses {
		content, err := json.Marshal(toolResponse.Content)
		if err != nil {
			return fmt.Errorf("unmarshal tool response content: %w", err)
		}
		i.ToolResponses = append(i.ToolResponses, &model.Message{
			Role:     toolResponse.Role,
			ToolID:   toolResponse.ToolID,
			ToolName: toolResponse.ToolName,
			Content:  string(content),
		})
	}
	return nil
}

// intermediateDataJSON is the JSON representation of IntermediateData.
type intermediateDataJSON struct {
	// ToolCalls keeps structured tool call entries with decoded arguments.
	ToolCalls []toolCallJSON `json:"toolCalls,omitempty"`
	// ToolResponses keeps structured tool responses with decoded content.
	ToolResponses []toolResponseJSON `json:"toolResponses,omitempty"`
	// IntermediateResponses carries intermediate assistant messages.
	IntermediateResponses []*model.Message `json:"intermediateResponses,omitempty"`
}

// toolCallJSON is the JSON representation of ToolCall.
type toolCallJSON struct {
	// ID is the tool call identifier.
	ID string `json:"id,omitempty"`
	// Type is the tool call type (e.g., function).
	Type string `json:"type,omitempty"`
	// Function holds the function definition and arguments.
	Function functionJSON `json:"function,omitempty"`
}

// functionJSON is the JSON representation of Function.
type functionJSON struct {
	// Name is the function name.
	Name string `json:"name,omitempty"`
	// Strict mirrors strict mode in the function definition.
	Strict bool `json:"strict,omitempty"`
	// Description describes the function usage.
	Description string `json:"description,omitempty"`
	// Arguments keeps decoded argument object.
	Arguments map[string]any `json:"arguments,omitempty"`
}

// toolResponseJSON is the JSON representation of ToolResponse.
type toolResponseJSON struct {
	// Role is the role of the message, typically tool.
	Role model.Role `json:"role,omitempty"`
	// ToolID links back to the tool call identifier.
	ToolID string `json:"toolId,omitempty"`
	// ToolName is the tool name.
	ToolName string `json:"toolName,omitempty"`
	// Content keeps decoded tool response payload.
	Content map[string]any `json:"content,omitempty"`
}
