//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package fakemodel provides a role-tagged deterministic model for model-backed examples and tests.
package fakemodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Role identifies the stage bound to a model instance without inspecting prompt text.
type Role string

// Model roles bind deterministic behavior to a stage without prompt-text routing.
const (
	RoleCandidate Role = "candidate"
	RoleJudge     Role = "judge"
	RoleOptimizer Role = "optimizer"
)

// Model implements model.Model with a fixed response selected by constructor role.
type Model struct {
	role           Role
	calls          atomic.Int64
	mu             sync.Mutex
	callsByVersion map[string]int
}

// New constructs a deterministic model for one explicit role.
func New(role Role) *Model { return &Model{role: role, callsByVersion: make(map[string]int)} }

// GenerateContent emits exactly one complete response.
func (m *Model) GenerateContent(_ context.Context, request *model.Request) (<-chan *model.Response, error) {
	call := m.calls.Add(1)
	if request == nil {
		request = &model.Request{}
	}
	content := ""
	var toolCalls []model.ToolCall
	switch m.role {
	case RoleCandidate:
		m.recordVersionCall(profileVersion(request.Messages))
		content, toolCalls = candidateResponse(request.Messages)
		if content == "" && len(toolCalls) == 0 {
			content = `{"game_id":"203","winner":"Central","decisive_moment":"sixth penalty round"}`
		}
	case RoleJudge:
		content = "reasoning: deterministic fixture comparison\nis_the_agent_response_valid: valid"
	case RoleOptimizer:
		version := call
		content = fmt.Sprintf(`{"Value":{"Text":"version: v%d\nProduce a grounded game recap with valid JSON and exact lookup_game arguments."},"Reason":"deterministic candidate v%d"}`, version, version)
		if version == 3 {
			content = `{"Value":{"Text":"version: v3\nProduce a grounded game recap with valid JSON and exact lookup_game arguments.\nSpecialize aggressively for the training examples."},"Reason":"deterministic candidate v3"}`
		}
	default:
		return nil, errors.New("unsupported fake model role")
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{
		ID: "fake-response", Object: model.ObjectTypeChatCompletion, Model: "fake-deterministic", Done: true,
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: content, ToolCalls: toolCalls}}},
	}
	close(responses)
	return responses, nil
}

// Info returns the stable model identity used in audit reports.
func (m *Model) Info() model.Info { return model.Info{Name: "fake-deterministic"} }

// Calls exposes the actual number of model requests made by the standard agent runner.
func (m *Model) Calls() int64 { return m.calls.Load() }

// CallsByVersion returns candidate calls grouped by the instruction version
// that was actually applied by the runner.
func (m *Model) CallsByVersion() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]int, len(m.callsByVersion))
	for version, calls := range m.callsByVersion {
		result[version] = calls
	}
	return result
}

func (m *Model) recordVersionCall(version string) {
	m.mu.Lock()
	m.callsByVersion[version]++
	m.mu.Unlock()
}

func candidateResponse(messages []model.Message) (string, []model.ToolCall) {
	all := make([]string, 0, len(messages))
	for _, message := range messages {
		all = append(all, message.Content)
	}
	joined := strings.Join(all, "\n")
	version := profileVersion(messages)
	caseID := ""
	for _, candidate := range []string{"101", "102", "103", "201", "202", "203"} {
		if strings.Contains(joined, "game "+candidate) {
			caseID = candidate
			break
		}
	}
	if (caseID == "103" || caseID == "203") && !strings.Contains(joined, "deterministic-fixture") {
		gameID := caseID
		if version == "baseline" || (version == "v3" && caseID == "203") {
			gameID = "000"
		}
		return "", []model.ToolCall{{
			Type: "function", ID: "call-" + caseID,
			Function: model.FunctionDefinitionParam{Name: "lookup_game", Arguments: []byte(`{"game_id":"` + gameID + `"}`)},
		}}
	}
	return responseFor(version, caseID), nil
}

func profileVersion(messages []model.Message) string {
	all := make([]string, 0, len(messages))
	for _, message := range messages {
		all = append(all, message.Content)
	}
	joined := strings.Join(all, "\n")
	for _, candidate := range []string{"v3", "v2", "v1"} {
		if strings.Contains(joined, "version: "+candidate) {
			return candidate
		}
	}
	return "baseline"
}

func responseFor(version, caseID string) string {
	expected := map[string]string{
		"101": `{"game_id":"101","winner":"Harbor","decisive_moment":"12-2 fourth-quarter run"}`,
		"102": `{"game_id":"102","winner":"North","decisive_moment":"overtime goal"}`,
		"103": `{"game_id":"103","winner":"East","decisive_moment":"late penalty"}`,
		"201": `{"game_id":"201","winner":"West","decisive_moment":"two consecutive steals"}`,
		"202": `{"game_id":"202","winner":"South","decisive_moment":"no unsupported decisive play"}`,
		"203": `{"game_id":"203","winner":"Central","decisive_moment":"sixth penalty round"}`,
	}
	if version == "baseline" {
		switch caseID {
		case "202", "203":
			return expected[caseID]
		case "101":
			return `{"game_id":"101","winner":"Harbor","decisive_moment":"unspecified"}`
		case "103":
			return `{"game_id":"103","winner":"East","decisive_moment":"unspecified"}`
		case "201":
			return `{"game_id":"201","winner":"West","decisive_moment":"unspecified"}`
		case "102":
			return "North won in overtime"
		}
	}
	if version == "v1" {
		switch caseID {
		case "102":
			return "North won in overtime"
		case "103":
			return `{"game_id":"103","winner":"East","decisive_moment":"generic finish"}`
		}
	}
	if version == "v2" && caseID == "103" {
		return `{"game_id":"103","winner":"East","decisive_moment":"generic finish"}`
	}
	return expected[caseID]
}
