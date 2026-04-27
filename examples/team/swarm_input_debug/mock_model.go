//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

type scriptedSwarmModel struct {
	name string
}

func (m *scriptedSwarmModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *scriptedSwarmModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		inv, _ := agent.InvocationFromContext(ctx)
		if inv != nil && inv.AgentName == parentName {
			ch <- m.parentTransferResponse(req)
			return
		}
		ch <- m.childResponse(req)
	}()
	return ch, nil
}

func (m *scriptedSwarmModel) parentTransferResponse(req *model.Request) *model.Response {
	args := transfer.Request{
		AgentName: childName,
		Message:   renderDefaultChildInput(lastUserContent(req)),
	}
	return &model.Response{
		ID:        "mock-parent-transfer",
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Model:     m.name,
		Timestamp: time.Now(),
		Done:      true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "call_mock_transfer",
					Function: model.FunctionDefinitionParam{
						Name:      transfer.TransferToolName,
						Arguments: mustMarshalJSON(args),
					},
				}},
			},
		}},
	}
}

func (m *scriptedSwarmModel) childResponse(req *model.Request) *model.Response {
	return &model.Response{
		ID:        "mock-child-response",
		Object:    model.ObjectTypeChatCompletion,
		Created:   time.Now().Unix(),
		Model:     m.name,
		Timestamp: time.Now(),
		Done:      true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: fmt.Sprintf("Mock child response. The child model request contains %d messages.", len(req.Messages)),
			},
		}},
	}
}

func renderDefaultChildInput(input string) string {
	rendered, err := renderChildInput(defaultChildTemplate, childInputTemplateData{
		Input:     input,
		FromAgent: parentName,
		ToAgent:   childName,
	})
	if err != nil {
		return input
	}
	return rendered
}

func mustMarshalJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func lastUserContent(req *model.Request) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == model.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}
