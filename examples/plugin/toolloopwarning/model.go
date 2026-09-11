//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	firstArguments  = `{"query":"trpc-agent-go","limit":3}`
	secondArguments = `{ "limit": 3, "query": "trpc-agent-go" }`
	finalAnswer     = "The repeated tool loop was detected, so I stopped calling the tool."
)

// scriptedModel repeats one tool call twice, then verifies that the plugin
// added its request-local warning before returning a final answer. A scripted
// model keeps the example deterministic; the Runner, agent, tool, plugin, and
// session paths are the same ones used with a provider-backed model.
type scriptedModel struct {
	step int
}

func (m *scriptedModel) Info() model.Info {
	return model.Info{Name: "tool-loop-warning-scripted-model"}
}

func (m *scriptedModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("scripted model received a nil request")
	}
	m.step++
	warningPresent := requestContainsWarning(request)
	fmt.Printf(
		"model request %d: loop warning=%t\n",
		m.step,
		warningPresent,
	)

	var response *model.Response
	switch m.step {
	case 1:
		if warningPresent {
			return nil, errors.New("first model request unexpectedly contains the loop warning")
		}
		response = toolCallResponse("call-search-1", firstArguments)
	case 2:
		if warningPresent {
			return nil, errors.New("second model request unexpectedly contains the loop warning")
		}
		response = toolCallResponse("call-search-2", secondArguments)
	case 3:
		if !warningPresent {
			return nil, errors.New("third model request does not contain the loop warning")
		}
		response = assistantResponse(finalAnswer)
	default:
		return nil, fmt.Errorf("unexpected model request %d", m.step)
	}

	responses := make(chan *model.Response, 1)
	responses <- response
	close(responses)
	return responses, nil
}

func requestContainsWarning(request *model.Request) bool {
	if request == nil {
		return false
	}
	for _, message := range request.Messages {
		if message.Role == model.RoleUser && message.Content == loopWarning {
			return true
		}
	}
	return false
}

func toolCallResponse(id string, arguments string) *model.Response {
	return &model.Response{
		ID:      id,
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   id,
					Function: model.FunctionDefinitionParam{
						Name:      searchTool,
						Arguments: []byte(arguments),
					},
				}},
			},
		}},
	}
}

func assistantResponse(content string) *model.Response {
	return &model.Response{
		ID:      "final-response",
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(content),
		}},
	}
}
