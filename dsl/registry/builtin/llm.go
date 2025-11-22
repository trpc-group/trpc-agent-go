//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package builtin provides built-in components for DSL workflows.
package builtin

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func init() {
	// Auto-register LLM component at package init time
	registry.MustRegister(&LLMComponent{})
}

// LLMComponent is a built-in component for LLM model calls.
type LLMComponent struct{}

// Metadata returns the component metadata.
func (c *LLMComponent) Metadata() registry.ComponentMetadata {
	return registry.ComponentMetadata{
		Name:        "builtin.llm",
		DisplayName: "LLM Model",
		Description: "Call a Large Language Model with messages",
		Category:    "LLM",
		Version:     "2.0.0",
		Inputs: []registry.ParameterSchema{
			{
				Name:        "messages",
				DisplayName: "Messages",
				Description: "Input messages for the LLM",
				Type:        "[]model.Message",
				GoType:      reflect.TypeOf([]model.Message{}),
				Required:    true,
				Reducer:     "message",
			},
		},

		Outputs: []registry.ParameterSchema{
			{
				Name:        "messages",
				DisplayName: "Messages",
				Description: "Updated messages including LLM response",
				Type:        "[]model.Message",
				GoType:      reflect.TypeOf([]model.Message{}),
				Reducer:     "message",
			},
		},

		ConfigSchema: []registry.ParameterSchema{
			{
				Name:        "model_name",
				DisplayName: "Model Name",
				Description: "Name of the model registered in ModelRegistry",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    true,
				Placeholder: "deepseek-chat",
			},
			{
				Name:        "instruction",
				DisplayName: "System Instruction",
				Description: "System instruction for the LLM",
				Type:        "string",
				GoType:      reflect.TypeOf(""),
				Required:    false,
				Placeholder: "You are a helpful assistant",
			},
			{
				Name:        "temperature",
				DisplayName: "Temperature",
				Description: "Sampling temperature (0.0 to 2.0)",
				Type:        "float64",
				GoType:      reflect.TypeOf(0.0),
				Required:    false,
				Default:     0.7,
				Validation: &registry.ValidationRules{
					Min: floatPtr(0.0),
					Max: floatPtr(2.0),
				},
			},
			{
				Name:        "max_tokens",
				DisplayName: "Max Tokens",
				Description: "Maximum number of tokens to generate",
				Type:        "int",
				GoType:      reflect.TypeOf(0),
				Required:    false,
			},
		},
	}
}

// Execute executes the LLM component.
// NOTE: This method is deprecated and not used when compiling DSL workflows.
// The Compiler uses createLLMNodeFunc which directly calls graph.NewLLMNodeFunc
// with the model instance from ModelRegistry. This method is kept for backward
// compatibility with non-DSL usage.
func (c *LLMComponent) Execute(ctx context.Context, config registry.ComponentConfig, state graph.State) (any, error) {
	// Get messages from state
	messagesVal, ok := state["messages"]
	if !ok {
		return nil, fmt.Errorf("messages not found in state")
	}

	messages, ok := messagesVal.([]model.Message)
	if !ok {
		return nil, fmt.Errorf("messages has wrong type: expected []model.Message, got %T", messagesVal)
	}

	// Get model from state
	modelVal, ok := state["model"]
	if !ok {
		return nil, fmt.Errorf("model not found in state")
	}

	llmModel, ok := modelVal.(model.Model)
	if !ok {
		return nil, fmt.Errorf("model has wrong type: expected model.Model, got %T", modelVal)
	}

	// Build request
	request := &model.Request{
		Messages: messages,
	}

	// Apply config
	if instruction := config.GetString("instruction"); instruction != "" {
		// Prepend system message
		systemMsg := model.Message{
			Role:    "system",
			Content: instruction,
		}
		request.Messages = append([]model.Message{systemMsg}, request.Messages...)
	}

	if temp := config.GetFloat("temperature"); temp > 0 {
		tempPtr := temp
		request.Temperature = &tempPtr
	}

	if maxTokens := config.GetInt("max_tokens"); maxTokens > 0 {
		maxTokensPtr := maxTokens
		request.MaxTokens = &maxTokensPtr
	}

	// Get tools from state (if available)
	if toolsVal, ok := state["tools"]; ok {
		if toolsMap, ok := toolsVal.(map[string]tool.Tool); ok {
			request.Tools = toolsMap
		}
	}

	// Call model
	responseChan, err := llmModel.GenerateContent(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to generate content: %w", err)
	}

	// Collect response
	var finalResponse *model.Response
	for response := range responseChan {
		finalResponse = response
		if response.Done {
			break
		}
	}

	if finalResponse == nil {
		return nil, fmt.Errorf("no response from model")
	}

	// Check for errors in response
	if finalResponse.Error != nil {
		return nil, fmt.Errorf("model returned error: %v", finalResponse.Error)
	}

	// Extract assistant message
	if len(finalResponse.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	assistantMessage := finalResponse.Choices[0].Message

	// Get current node ID from state for node_responses
	var nodeID string
	if nodeIDData, exists := state[graph.StateKeyCurrentNodeID]; exists {
		if id, ok := nodeIDData.(string); ok {
			nodeID = id
		}
	}

	fmt.Printf("🔍 [DEBUG LLM] Node %s executed, response length: %d\n", nodeID, len(assistantMessage.Content))

	// Return updated state with new message, last_response, and node_responses
	return graph.State{
		"messages":       []model.Message{assistantMessage},
		"last_response":  assistantMessage.Content,
		"node_responses": map[string]any{nodeID: assistantMessage.Content},
	}, nil
}

func floatPtr(f float64) *float64 {
	return &f
}
