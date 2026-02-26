//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type mockRepo struct {
	sums []skill.Summary
	full map[string]*skill.Skill
}

func (m *mockRepo) Summaries() []skill.Summary { return m.sums }
func (m *mockRepo) Get(name string) (*skill.Skill, error) {
	if sk, ok := m.full[name]; ok {
		return sk, nil
	}
	return nil, nil
}
func (m *mockRepo) Path(name string) (string, error) { return "", nil }

func TestSkillsRequestProcessor_ProcessRequest_OverviewAndDocs(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{
			{Name: "calc", Description: "math ops"},
			{Name: "file", Description: "file tools"},
		},
		full: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Body:    "Calc body",
				Docs: []skill.Doc{{
					Path:    "USAGE.md",
					Content: "use me",
				}},
			},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
				skill.StateKeyDocsPrefix + "calc":   []byte("*"),
			},
		},
	}

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("base sys"),
		},
	}

	ch := make(chan *event.Event, 2)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	// System message should be merged with overview and loaded content.
	idx := 0
	require.Equal(t, model.RoleSystem, req.Messages[idx].Role)
	sys := req.Messages[idx].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.Contains(t, sys, "- calc: math ops")
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.Contains(t, sys, ".venv/")
	require.Contains(t, sys, "Avoid include_all_docs")
	require.Contains(t, sys, "Use skill_run only for commands required")
	require.Contains(t, sys, "[Loaded] calc")
	require.Contains(t, sys, "Calc body")
	require.Contains(t, sys, "[Doc] USAGE.md")
	require.Contains(t, sys, "use me")

	// A preprocessing event should be emitted.
	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev.Object)
}

func TestSkillsRequestProcessor_NoDuplicateOverview(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "x", Description: "d"}},
		full: map[string]*skill.Skill{},
	}
	inv := &agent.Invocation{Session: &session.Session{}}
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("sys")},
	}
	p := NewSkillsRequestProcessor(repo)
	ch := make(chan *event.Event, 2)

	p.ProcessRequest(context.Background(), inv, req, ch)
	// Run again; header must not duplicate.
	p.ProcessRequest(context.Background(), inv, req, ch)

	sys := req.Messages[0].Content
	// Count occurrences of header.
	cnt := strings.Count(sys, skillsOverviewHeader)
	require.Equal(t, 1, cnt)
}

func TestSkillsRequestProcessor_ToolingGuidance_Disabled(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "x", Description: "d"}},
		full: map[string]*skill.Skill{},
	}
	inv := &agent.Invocation{Session: &session.Session{}}
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillsToolingGuidance(""),
	)
	ch := make(chan *event.Event, 1)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.NotContains(t, sys, skillsToolingGuidanceHeader)
}

func TestSkillsRequestProcessor_ArrayDocs_NoSystemMessage(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Body:    "B",
				Docs: []skill.Doc{
					{Path: "USAGE.md", Content: "use"},
					{Path: "EXTRA.txt", Content: "x"},
				},
			},
		},
	}
	inv := &agent.Invocation{Session: &session.Session{
		State: session.StateMap{
			skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			skill.StateKeyDocsPrefix + "calc":   []byte("[\"USAGE.md\"]"),
		},
	}}
	// No system message initially.
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
	)
	ch := make(chan *event.Event, 2)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.Contains(t, sys, "[Loaded] calc")
	require.Contains(t, sys, "USAGE.md")
	// EXTRA.txt not selected
	require.NotContains(t, sys, "EXTRA.txt")
}

func TestSkillsRequestProcessor_MergeIntoEmptySystem(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}
	inv := &agent.Invocation{Session: &session.Session{
		State: session.StateMap{
			skill.StateKeyLoadedPrefix + "calc": []byte("1"),
		},
	}}
	// Pre-existing empty system message.
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage(""),
	}}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
	)
	ch := make(chan *event.Event, 2)
	p.ProcessRequest(context.Background(), inv, req, ch)
	// Should fill content into the empty system message.
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.NotEmpty(t, req.Messages[0].Content)
	require.Contains(t, req.Messages[0].Content, "[Loaded] calc")
}

func TestSkillsRequestProcessor_InvalidDocsSelectionJSON(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Body:    "B",
				Docs:    []skill.Doc{{Path: "USAGE.md", Content: "use"}},
			},
		},
	}
	// Docs selection is invalid JSON; should be ignored.
	inv := &agent.Invocation{Session: &session.Session{
		State: session.StateMap{
			skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			skill.StateKeyDocsPrefix + "calc":   []byte("[bad]"),
		},
	}}
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
	)
	ch := make(chan *event.Event, 2)
	p.ProcessRequest(context.Background(), inv, req, ch)
	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	// Body present, docs ignored
	require.Contains(t, sys, "[Loaded] calc")
	require.NotContains(t, sys, "USAGE.md")
}

func TestSkillsRequestProcessor_NoOverviewWhenNoSummaries(t *testing.T) {
	repo := &mockRepo{sums: nil, full: map[string]*skill.Skill{}}
	inv := &agent.Invocation{Session: &session.Session{}}
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(repo)
	ch := make(chan *event.Event, 1)
	p.ProcessRequest(context.Background(), inv, req, ch)
	// No system message injected when no summaries.
	require.Empty(t, req.Messages)
	// Still emits a preprocessing instruction for trace consistency.
	e := <-ch
	require.NotNil(t, e)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, e.Object)
}

func TestSkillsRequestProcessor_BuildDocsText_EdgeCases(t *testing.T) {
	p := NewSkillsRequestProcessor(&mockRepo{})
	// nil skill yields empty
	require.Equal(t, "", p.buildDocsText(nil, []string{"a"}))
	// no matching docs yields empty
	sk := &skill.Skill{Docs: []skill.Doc{{Path: "X.md", Content: "x"}}}
	require.Equal(t, "", p.buildDocsText(sk, []string{"Y.md"}))
}

func TestSkillsRequestProcessor_MergeIntoSystem_Edge(t *testing.T) {
	p := NewSkillsRequestProcessor(&mockRepo{})
	// nil request should be a no-op
	p.mergeIntoSystem(nil, "content")

	// empty content should not modify messages
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("sys"),
	}}
	p.mergeIntoSystem(req, "")
	require.Equal(t, "sys", req.Messages[0].Content)
}

func TestSkillsRequestProcessor_SkillLoadModeOnce_OffloadsLoadedSkills(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			},
		},
	}
	req := &model.Request{Messages: nil}

	ch := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeOnce),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	require.Contains(t, sys, "[Loaded] calc")
	require.Contains(t, sys, "B")

	v, ok := inv.Session.GetState(skill.StateKeyLoadedPrefix + "calc")
	require.True(t, ok)
	require.Empty(t, v)

	ev1 := <-ch
	require.NotNil(t, ev1)
	require.Equal(t, model.ObjectTypeStateUpdate, ev1.Object)
	require.Contains(t, ev1.StateDelta, skill.StateKeyLoadedPrefix+"calc")

	ev2 := <-ch
	require.NotNil(t, ev2)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev2.Object)
}

func TestSkillsRequestProcessor_SkillLoadModeTurn_ClearsOncePerInvocation(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
				skill.StateKeyDocsPrefix + "calc":   []byte("*"),
			},
		},
	}

	req1 := &model.Request{Messages: nil}
	ch1 := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeTurn),
	)
	p.ProcessRequest(context.Background(), inv, req1, ch1)

	require.NotEmpty(t, req1.Messages)
	sys1 := req1.Messages[0].Content
	require.Contains(t, sys1, skillsOverviewHeader)
	require.NotContains(t, sys1, "[Loaded] calc")

	loadedVal, ok := inv.Session.GetState(skill.StateKeyLoadedPrefix + "calc")
	require.True(t, ok)
	require.Empty(t, loadedVal)
	docsVal, ok := inv.Session.GetState(skill.StateKeyDocsPrefix + "calc")
	require.True(t, ok)
	require.Empty(t, docsVal)

	ev1 := <-ch1
	require.NotNil(t, ev1)
	require.Equal(t, model.ObjectTypeStateUpdate, ev1.Object)

	ev2 := <-ch1
	require.NotNil(t, ev2)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev2.Object)

	inv.Session.SetState(skill.StateKeyLoadedPrefix+"calc", []byte("1"))
	req2 := &model.Request{Messages: nil}
	ch2 := make(chan *event.Event, 2)
	p.ProcessRequest(context.Background(), inv, req2, ch2)

	require.NotEmpty(t, req2.Messages)
	sys2 := req2.Messages[0].Content
	require.Contains(t, sys2, "[Loaded] calc")
	require.Contains(t, sys2, "B")

	ev3 := <-ch2
	require.NotNil(t, ev3)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev3.Object)
}

func TestSkillsRequestProcessor_ToolResultMode_OverviewOnly(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{
			{Name: "calc", Description: "math ops"},
		},
		full: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Body:    "Calc body",
				Docs: []skill.Doc{{
					Path:    "USAGE.md",
					Content: "use me",
				}},
			},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
				skill.StateKeyDocsPrefix + "calc":   []byte("*"),
			},
		},
	}

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("base sys"),
		},
	}

	ch := make(chan *event.Event, 2)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
		WithSkillsLoadedContentInToolResults(true),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.NotContains(t, sys, "[Loaded] calc")
	require.NotContains(t, sys, "[Doc] USAGE.md")

	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev.Object)
}

func TestSkillsToolResultRequestProcessor_MaterializesIntoLastToolMsg(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Body:    "B",
				Docs: []skill.Doc{{
					Path:    "USAGE.md",
					Content: "use",
				}},
			},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
				skill.StateKeyDocsPrefix + "calc":   []byte("*"),
			},
		},
	}

	args1, err := json.Marshal(skillNameInput{Skill: "calc"})
	require.NoError(t, err)
	args2, err := json.Marshal(skillNameInput{Skill: "calc"})
	require.NoError(t, err)

	const (
		toolCallID1 = "tc1"
		toolCallID2 = "tc2"
	)
	assistant := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{
				Type: "function",
				ID:   toolCallID1,
				Function: model.FunctionDefinitionParam{
					Name:      skillToolLoad,
					Arguments: args1,
				},
			},
			{
				Type: "function",
				ID:   toolCallID2,
				Function: model.FunctionDefinitionParam{
					Name:      skillToolLoad,
					Arguments: args2,
				},
			},
		},
	}

	baseOut := loadedPrefix + " calc"
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			assistant,
			{
				Role:     model.RoleTool,
				ToolName: skillToolLoad,
				ToolID:   toolCallID1,
				Content:  baseOut,
			},
			{
				Role:     model.RoleTool,
				ToolName: skillToolLoad,
				ToolID:   toolCallID2,
				Content:  baseOut,
			},
		},
	}

	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeSession),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Equal(t, baseOut, req.Messages[2].Content)
	lastTool := req.Messages[3].Content
	require.NotContains(t, lastTool, baseOut)
	require.Contains(t, lastTool, "[Loaded] calc")
	require.Contains(t, lastTool, "B")
	require.Contains(t, lastTool, "[Doc] USAGE.md")
	require.Contains(t, lastTool, "use")

	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		require.NotContains(t, m.Content, skillsLoadedContextHeader)
	}
}

func TestSkillsToolResultRequestProcessor_FallbackSystemMessageAdded(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			},
		},
	}

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			model.NewUserMessage("u"),
		},
	}

	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeSession),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	var found bool
	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(m.Content, skillsLoadedContextHeader) {
			found = true
			require.Contains(t, m.Content, "[Loaded] calc")
			require.Contains(t, m.Content, "B")
		}
	}
	require.True(t, found)

	inv.Session.SetState(skill.StateKeyLoadedPrefix+"calc", nil)
	p.ProcessRequest(context.Background(), inv, req, nil)

	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		require.NotContains(t, m.Content, skillsLoadedContextHeader)
	}
}

func TestSkillsToolResultRequestProcessor_SessionSummary_DisablesFallback(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			},
		},
	}
	inv.SetState(contentHasSessionSummaryStateKey, true)

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			model.NewUserMessage("u"),
		},
	}

	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeSession),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		require.NotContains(t, m.Content, skillsLoadedContextHeader)
	}
}

func TestSkillsToolResultRequestProcessor_SessionSummary_AllowsFallback(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			},
		},
	}
	inv.SetState(contentHasSessionSummaryStateKey, true)

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			model.NewUserMessage("u"),
		},
	}

	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeSession),
		WithSkipSkillsFallbackOnSessionSummary(false),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	var matchCount int
	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		if strings.Contains(m.Content, skillsLoadedContextHeader) {
			matchCount++
			require.Contains(t, m.Content, "[Loaded] calc")
			require.Contains(t, m.Content, "B")
		}
	}
	require.Equal(t, 1, matchCount)
}

func TestHasSessionSummary(t *testing.T) {
	require.False(t, hasSessionSummary(nil))

	inv := &agent.Invocation{}
	require.False(t, hasSessionSummary(inv))

	inv.SetState(contentHasSessionSummaryStateKey, "true")
	require.False(t, hasSessionSummary(inv))

	inv.SetState(contentHasSessionSummaryStateKey, true)
	require.True(t, hasSessionSummary(inv))
}

func TestSkillsToolResultRequestProcessor_BuildToolResultContent_Base(
	t *testing.T,
) {
	repo := &mockRepo{
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}

	p := NewSkillsToolResultRequestProcessor(repo)
	out, ok := p.buildToolResultContent(
		context.Background(),
		nil,
		"calc",
		"ok",
	)
	require.True(t, ok)
	require.Contains(t, out, "ok")
	require.Contains(t, out, "[Loaded] calc")
}

func TestSkillsToolResultRequestProcessor_SkillLoadModeOnce_Offloads(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
				skill.StateKeyDocsPrefix + "calc":   []byte("[]"),
			},
		},
	}

	args, err := json.Marshal(skillNameInput{Skill: "calc"})
	require.NoError(t, err)

	const toolCallID = "tc1"
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   toolCallID,
					Function: model.FunctionDefinitionParam{
						Name:      skillToolLoad,
						Arguments: args,
					},
				}},
			},
			{
				Role:     model.RoleTool,
				ToolName: skillToolLoad,
				ToolID:   toolCallID,
				Content:  loadedPrefix + " calc",
			},
		},
	}

	ch := make(chan *event.Event, 2)
	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeOnce),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	toolMsg := req.Messages[2].Content
	require.Contains(t, toolMsg, "[Loaded] calc")
	require.Contains(t, toolMsg, "B")

	loadedVal, ok := inv.Session.GetState(
		skill.StateKeyLoadedPrefix + "calc",
	)
	require.True(t, ok)
	require.Empty(t, loadedVal)

	docsVal, ok := inv.Session.GetState(
		skill.StateKeyDocsPrefix + "calc",
	)
	require.True(t, ok)
	require.Empty(t, docsVal)

	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, model.ObjectTypeStateUpdate, ev.Object)
	require.Contains(
		t,
		ev.StateDelta,
		skill.StateKeyLoadedPrefix+"calc",
	)
	require.Contains(
		t,
		ev.StateDelta,
		skill.StateKeyDocsPrefix+"calc",
	)
}

func TestParseLoadedSkillFromText(t *testing.T) {
	require.Equal(t, "", parseLoadedSkillFromText(""))
	require.Equal(t, "", parseLoadedSkillFromText("ok"))
	require.Equal(t, "", parseLoadedSkillFromText("loaded:"))
	require.Equal(t, "calc", parseLoadedSkillFromText("loaded: calc"))
	require.Equal(t, "calc", parseLoadedSkillFromText("Loaded: calc"))
	require.Equal(t, "calc", parseLoadedSkillFromText("  loaded: calc  "))
}

func TestSkillNameFromToolMessage_FallsBackToToolOutput(t *testing.T) {
	calls := toolCallIndex{
		"tc1": {
			ID:   "tc1",
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      skillToolLoad,
				Arguments: []byte("{not json}"),
			},
		},
	}
	m := model.Message{
		Role:     model.RoleTool,
		ToolName: skillToolLoad,
		ToolID:   "tc1",
		Content:  loadedPrefix + " calc",
	}
	require.Equal(t, "calc", skillNameFromToolMessage(m, calls))

	m.ToolID = "missing"
	require.Equal(t, "calc", skillNameFromToolMessage(m, calls))
}

func TestIndexToolCalls_SkipsEmptyIDsAndNonAssistant(t *testing.T) {
	msgs := []model.Message{
		{
			Role: model.RoleUser,
			ToolCalls: []model.ToolCall{{
				ID: "u1",
			}},
		},
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: ""},
				{ID: "a1"},
			},
		},
	}
	idx := indexToolCalls(msgs)

	_, ok := idx["a1"]
	require.True(t, ok)
	_, ok = idx["u1"]
	require.False(t, ok)
	_, ok = idx[""]
	require.False(t, ok)
}

func TestLastSkillToolMsgIndex_HandlesSelectDocs(t *testing.T) {
	args, err := json.Marshal(skillNameInput{Skill: "calc"})
	require.NoError(t, err)

	msgs := []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID: "tc1",
				Function: model.FunctionDefinitionParam{
					Name:      skillToolSelectDocs,
					Arguments: args,
				},
			}},
		},
		{
			Role:     model.RoleTool,
			ToolName: skillToolSelectDocs,
			ToolID:   "tc1",
			Content:  "{}",
		},
		{
			Role:     model.RoleTool,
			ToolName: "other",
			ToolID:   "tc1",
			Content:  "{}",
		},
	}

	calls := indexToolCalls(msgs)
	idx := lastSkillToolMsgIndex(msgs, calls)
	require.Equal(t, 1, idx["calc"])
}

func TestInsertAfterLastSystemMessage_NoSystemMessage(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("u"),
		},
	}
	insertAfterLastSystemMessage(
		req,
		model.NewSystemMessage("sys"),
	)

	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Equal(t, "sys", req.Messages[0].Content)
}

func TestUpsertLoadedContextMessage_UpdatesAndRemoves(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("base"),
			model.NewSystemMessage(
				skillsLoadedContextHeader + "\nold",
			),
			model.NewUserMessage("u"),
		},
	}
	p := &SkillsToolResultRequestProcessor{}
	p.upsertLoadedContextMessage(
		req,
		skillsLoadedContextHeader+"\nnew",
	)

	idx := findLoadedContextMessageIndex(req.Messages)
	require.GreaterOrEqual(t, idx, 0)
	require.Contains(t, req.Messages[idx].Content, "new")

	p.upsertLoadedContextMessage(req, "")
	require.Equal(t, -1, findLoadedContextMessageIndex(req.Messages))
}

func TestSkillsToolResultRequestProcessor_GetDocsSelection_InvalidJSON(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {
				Summary: skill.Summary{Name: "calc"},
				Docs: []skill.Doc{{
					Path:    "USAGE.md",
					Content: "use",
				}},
			},
		},
	}
	inv := &agent.Invocation{
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyDocsPrefix + "calc": []byte("[bad]"),
			},
		},
	}
	p := NewSkillsToolResultRequestProcessor(repo)
	require.Empty(t, p.getDocsSelection(inv, "calc"))

	inv.Session.SetState(skill.StateKeyDocsPrefix+"missing", []byte("*"))
	require.Empty(t, p.getDocsSelection(inv, "missing"))
}

func TestBuildDocsText_SkipsEmptyAndUnwanted(t *testing.T) {
	require.Equal(t, "", buildDocsText(nil, []string{"a"}))

	sk := &skill.Skill{
		Docs: []skill.Doc{
			{Path: "A.md", Content: ""},
			{Path: "B.md", Content: "b"},
		},
	}
	require.Equal(t, "", buildDocsText(sk, []string{"A.md"}))
	require.Equal(t, "", buildDocsText(sk, []string{"C.md"}))

	got := buildDocsText(sk, []string{"B.md"})
	require.Contains(t, got, "[Doc] B.md")
	require.Contains(t, got, "b")
}

func TestSkillsToolResultRequestProcessor_MaybeOffload_NoOpWhenNotOnce(
	t *testing.T,
) {
	repo := &mockRepo{
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}
	inv := &agent.Invocation{
		Session: &session.Session{
			State: session.StateMap{
				skill.StateKeyLoadedPrefix + "calc": []byte("1"),
			},
		},
	}
	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeSession),
	)

	ch := make(chan *event.Event, 1)
	p.maybeOffloadLoadedSkills(
		context.Background(),
		inv,
		[]string{"calc"},
		ch,
	)

	v, ok := inv.Session.GetState(skill.StateKeyLoadedPrefix + "calc")
	require.True(t, ok)
	require.Equal(t, []byte("1"), v)
	require.Len(t, ch, 0)
}
