//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mragent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type staticModel struct{}

func (m *staticModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("ok"),
		}},
	}
	close(ch)
	return ch, nil
}

func (m *staticModel) Info() model.Info { return model.Info{Name: "static"} }

func TestNewAgent(t *testing.T) {
	agt, err := NewAgent(&staticModel{}, WithMaxToolRounds(2))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if agt == nil {
		t.Fatal("NewAgent returned nil agent")
	}
}

func TestNewGraphRequiresModel(t *testing.T) {
	if _, err := NewGraph(nil); err == nil {
		t.Fatal("NewGraph should require a model")
	}
}

func TestNewGraphUsesExplicitMRAgentStateSchema(t *testing.T) {
	g, err := NewGraph(&staticModel{})
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	for _, key := range []string{
		StateKeyActiveCues,
		StateKeyActiveTags,
		StateKeyVisitedPaths,
		StateKeyEvidence,
		StateKeyRouteDecision,
		StateKeyBudget,
		StateKeyRelationEvaluations,
	} {
		if _, ok := g.Schema().Fields[key]; !ok {
			t.Fatalf("schema missing %s", key)
		}
	}
	if field := g.Schema().Fields[StateKeyBudget]; field.Type != reflect.TypeOf(Budget{}) {
		t.Fatalf("budget schema type = %v", field.Type)
	}
}

func TestToolSetIncludesAssociativeTools(t *testing.T) {
	tools := NewToolSet().Tools(context.Background())
	names := make(map[string]struct{}, len(tools))
	for _, tl := range tools {
		names[tl.Declaration().Name] = struct{}{}
	}
	for _, name := range []string{
		memory.CueSearchToolName,
		memory.TagExpandToolName,
		memory.ContentLoadToolName,
		SessionLoadToolName,
	} {
		if _, ok := names[name]; !ok {
			t.Fatalf("expected tool %s", name)
		}
	}
}

func TestToolSetCanDisableSessionLoad(t *testing.T) {
	tools := NewToolSet(WithToolSetSessionLoadTool(false)).Tools(context.Background())
	for _, tl := range tools {
		if tl.Declaration().Name == SessionLoadToolName {
			t.Fatal("session_load should be disabled")
		}
	}
}

func TestDefaultPromptMatchesSessionLoadToolAvailability(t *testing.T) {
	enabled := newOptions()
	if !strings.Contains(enabled.ReconstructionInstruction, "call session_load only when") {
		t.Fatal("default prompt should mention session_load when the tool is enabled")
	}

	disabled := newOptions(WithSessionLoadTool(false))
	if strings.Contains(disabled.ReconstructionInstruction, "call session_load only when") {
		t.Fatal("default prompt should not ask for session_load when the tool is disabled")
	}
	if !strings.Contains(disabled.ReconstructionInstruction, "do not call session_load") {
		t.Fatal("default prompt should explicitly forbid session_load when the tool is disabled")
	}
}

func TestAbsorbToolsUpdatesExplicitState(t *testing.T) {
	toolOutput := memorytoolTagExpandJSON(t)
	response := []toolNodeResponse{
		{
			ToolName: memory.TagExpandToolName,
			Output:   json.RawMessage(toolOutput),
		},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal tool response: %v", err)
	}
	state := graph.State{
		StateKeyBudget: Budget{MaxToolRounds: 2, MaxCues: 6, MaxPaths: 4},
		graph.StateKeyNodeResponses: map[string]any{
			nodeTools: string(raw),
		},
	}

	out, err := absorbToolsNode()(context.Background(), state)
	if err != nil {
		t.Fatalf("absorbToolsNode: %v", err)
	}
	updated, ok := out.(graph.State)
	if !ok {
		t.Fatalf("unexpected output %T", out)
	}
	if len(updated[StateKeyVisitedPaths].([]memory.Path)) != 1 {
		t.Fatalf("visited paths not updated: %#v", updated[StateKeyVisitedPaths])
	}
	if len(updated[StateKeyEvidence].([]Evidence)) != 1 {
		t.Fatalf("evidence not updated: %#v", updated[StateKeyEvidence])
	}
	budget := updated[StateKeyBudget].(Budget)
	if budget.ToolRounds != 1 || budget.TagExpansions != 1 {
		t.Fatalf("budget not updated: %#v", budget)
	}
	decision := updated[StateKeyRouteDecision].(RouteDecision)
	if decision.Next != routeReconstruct {
		t.Fatalf("route decision = %#v", decision)
	}
}

func memorytoolTagExpandJSON(t *testing.T) []byte {
	t.Helper()
	content := memory.Content{
		ID:   "content-1",
		Text: "The user graduated with a degree in Business Administration.",
		Ref: memory.ContentRef{
			Kind:    memory.RefKindSessionEvent,
			EventID: "answer_1",
			TurnID:  "answer_1",
		},
	}
	data, err := json.Marshal(struct {
		Tags  []memory.Tag  `json:"tags"`
		Paths []memory.Path `json:"paths"`
		Count int           `json:"count"`
	}{
		Tags: []memory.Tag{{
			ID:        "tag-1",
			Text:      "education",
			CueID:     "cue-1",
			ContentID: "content-1",
			Weight:    1,
		}},
		Paths: []memory.Path{{
			Cue:     memory.Cue{ID: "cue-1", Text: "degree business administration"},
			Tag:     memory.Tag{ID: "tag-1", Text: "education", ContentID: "content-1"},
			Content: &content,
			Score:   1,
		}},
		Count: 1,
	})
	if err != nil {
		t.Fatalf("marshal tag expand output: %v", err)
	}
	return data
}
