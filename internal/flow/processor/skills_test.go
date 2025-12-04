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
	p := NewSkillsRequestProcessor(repo)
	p.ProcessRequest(context.Background(), inv, req, ch)

	// System message should be merged with overview and loaded content.
	idx := 0
	require.Equal(t, model.RoleSystem, req.Messages[idx].Role)
	sys := req.Messages[idx].Content
	require.Contains(t, sys, "Available skills:")
	require.Contains(t, sys, "- calc: math ops")
	require.Contains(t, sys, "Tooling and workspace guidance:")
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
	cnt := strings.Count(sys, "Available skills:")
	require.Equal(t, 1, cnt)
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
	p := NewSkillsRequestProcessor(repo)
	ch := make(chan *event.Event, 2)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	sys := req.Messages[0].Content
	require.Contains(t, sys, "Available skills:")
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
	p := NewSkillsRequestProcessor(repo)
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
	p := NewSkillsRequestProcessor(repo)
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
