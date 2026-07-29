//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package assist

import (
	"context"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// FakeModel is a deterministic, credential-free model.Model for the example's
// integration and acceptance paths.
type FakeModel struct {
	mu        sync.Mutex
	toolCalls []string
}

// NewFakeModel constructs a deterministic fake review model.
func NewFakeModel() *FakeModel {
	return &FakeModel{}
}

// Info implements model.Model.
func (m *FakeModel) Info() model.Info {
	return model.Info{Name: "code-review-fake-model"}
}

// GenerateContent implements model.Model with a fixed Skill, workspace, and
// structured-output trajectory.
func (m *FakeModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	var response *model.Response
	if request != nil && request.StructuredOutput != nil {
		response = assistantResponse("fake-structured", fakeStructuredOutput)
	} else {
		skillLoaded, workspaceChecked := fakeToolHistory(request)
		switch {
		case !skillLoaded:
			m.recordToolCall("skill_load")
			response = toolCallResponse("fake-skill", "skill_load", []byte(`{"skill":"code-review"}`))
		case !workspaceChecked:
			m.recordToolCall("workspace_exec")
			response = toolCallResponse(
				"fake-vet",
				"workspace_exec",
				[]byte(`{"command":"go vet ./...","timeout":120}`),
			)
		default:
			response = assistantResponse("fake-evidence", "go vet completed; inspect added line 1")
		}
	}

	responses := make(chan *model.Response, 1)
	select {
	case <-ctx.Done():
		close(responses)
	case responses <- response:
		close(responses)
	}
	return responses, nil
}

func fakeToolHistory(request *model.Request) (skillLoaded, workspaceChecked bool) {
	if request == nil {
		return false, false
	}
	for _, message := range request.Messages {
		for _, call := range message.ToolCalls {
			switch call.Function.Name {
			case "skill_load":
				skillLoaded = true
			case "workspace_exec":
				workspaceChecked = true
			}
		}
	}
	return skillLoaded, workspaceChecked
}

func (m *FakeModel) recordToolCall(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCalls = append(m.toolCalls, name)
}

// ToolCalls returns the deterministic tool-call names emitted so far.
func (m *FakeModel) ToolCalls() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.toolCalls...)
}

func toolCallResponse(id, name string, arguments []byte) *model.Response {
	return &model.Response{
		ID:     id,
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   id,
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: arguments,
					},
				}},
			},
		}},
	}
}

func assistantResponse(id, content string) *model.Response {
	return &model.Response{
		ID:     id,
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(content),
		}},
	}
}

const fakeStructuredOutput = `{"findings":[{"schema_version":"review/v1","severity":"medium","category":"correctness","layer":"unified","file":"file.go","line":1,"semantic_anchor":"returned-error","title":"check returned error","evidence":"go vet completed and the added line ignores an error","recommendation":"handle the returned error","confidence":"high","source":"model","rule_id":"model/correctness/v1","disposition":"finding"}]}`

var _ model.Model = (*FakeModel)(nil)
