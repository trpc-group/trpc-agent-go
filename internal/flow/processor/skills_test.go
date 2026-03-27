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
	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("*"),
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
	require.NotContains(t, sys, skillsCapabilityHeader)
	require.Contains(t, sys, "- calc: math ops")
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.Contains(t, sys, ".venv/")
	require.Contains(t, sys, "Avoid include_all_docs")
	require.Contains(t, sys, "Loading a skill gives you instructions")
	require.Contains(t, sys, "routing summaries only")
	require.Contains(t, sys, "If the loaded content already provides enough guidance")
	require.Contains(t, sys, "load SKILL.md before the first skill_run or skill_exec")
	require.Contains(t, sys, "Do not infer commands, script entrypoints")
	require.Contains(t, sys, "Use execution tools only when running a command")
	require.Contains(t, sys, "When you execute, follow the tool description")
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
	require.NotContains(t, sys, skillsCapabilityHeader)
	require.NotContains(t, sys, skillsToolingGuidanceHeader)
}

func TestSkillsRequestProcessor_ExecToolsDisabled(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "x", Description: "d"}},
		full: map[string]*skill.Skill{},
	}
	inv := &agent.Invocation{Session: &session.Session{}}
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillExecToolsDisabled(),
	)
	ch := make(chan *event.Event, 1)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.NotContains(t, sys, "skill_exec")
	require.NotContains(t, sys, "skill_write_stdin")
	require.NotContains(t, sys, "skill_poll_session")
}

func TestSkillsRequestProcessor_KnowledgeOnlyGuidance(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "x", Description: "d"}},
		full: map[string]*skill.Skill{},
	}
	inv := &agent.Invocation{Session: &session.Session{}}
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillToolProfile(skillprofile.KnowledgeOnly),
	)
	ch := make(chan *event.Event, 1)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.Contains(t, sys, skillsCapabilityHeader)
	require.Contains(t, sys, "skill discovery and knowledge loading only")
	require.Contains(t, sys, "Execution-oriented skill tools are unavailable")
	require.Contains(t, sys, skillsToolingGuidanceHeader)
	require.NotContains(t, sys, "skill_run runs with CWD")
	require.NotContains(t, sys, ".venv/")
	require.Contains(t, sys, "Use skills for progressive disclosure only")
	require.Contains(t, sys, "inspect only the documentation needed")
}

func TestSkillsRequestProcessor_KnowledgeOnlyGuidance_Disabled(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "x", Description: "d"}},
		full: map[string]*skill.Skill{},
	}
	inv := &agent.Invocation{Session: &session.Session{}}
	req := &model.Request{Messages: nil}
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillToolProfile(skillprofile.KnowledgeOnly),
		WithSkillsToolingGuidance(""),
	)
	ch := make(chan *event.Event, 1)
	p.ProcessRequest(context.Background(), inv, req, ch)

	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.NotContains(t, sys, skillsCapabilityHeader)
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
	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("[\"USAGE.md\"]"),
			},
		},
	}
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
	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
			},
		},
	}
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
	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("[bad]"),
			},
		},
	}
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.LoadedOrderKey("tester"): []byte(
					`["calc"]`,
				),
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

	v, ok := inv.Session.GetState(skill.LoadedKey("tester", "calc"))
	require.True(t, ok)
	require.Empty(t, v)

	ev1 := <-ch
	require.NotNil(t, ev1)
	require.Equal(t, model.ObjectTypeStateUpdate, ev1.Object)
	require.Contains(
		t,
		ev1.StateDelta,
		skill.LoadedKey("tester", "calc"),
	)
	require.Contains(
		t,
		ev1.StateDelta,
		skill.LoadedOrderKey("tester"),
	)

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
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("*"),
				skill.LoadedOrderKey("tester"): []byte(
					`["calc"]`,
				),
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

	loadedVal, ok := inv.Session.GetState(skill.LoadedKey("tester", "calc"))
	require.True(t, ok)
	require.Empty(t, loadedVal)
	docsVal, ok := inv.Session.GetState(skill.DocsKey("tester", "calc"))
	require.True(t, ok)
	require.Empty(t, docsVal)
	orderVal, ok := inv.Session.GetState(skill.LoadedOrderKey("tester"))
	require.True(t, ok)
	require.Empty(t, orderVal)

	ev1 := <-ch1
	require.NotNil(t, ev1)
	require.Equal(t, model.ObjectTypeStateUpdate, ev1.Object)
	require.Contains(
		t,
		ev1.StateDelta,
		skill.LoadedOrderKey("tester"),
	)

	ev2 := <-ch1
	require.NotNil(t, ev2)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev2.Object)

	inv.Session.SetState(skill.LoadedKey("tester", "calc"), []byte("1"))
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

func TestSkillsRequestProcessor_TurnMode_IsolatesAgents(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}
	sess := &session.Session{
		State: session.StateMap{
			skill.LoadedKey("parent", "calc"): []byte("1"),
			skill.LoadedKey("child", "calc"):  []byte("1"),
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv-child",
		AgentName:    "child",
		Session:      sess,
	}
	req := &model.Request{Messages: nil}

	ch := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeTurn),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	parentVal, ok := sess.GetState(skill.LoadedKey("parent", "calc"))
	require.True(t, ok)
	require.Equal(t, []byte("1"), parentVal)

	childVal, ok := sess.GetState(skill.LoadedKey("child", "calc"))
	require.True(t, ok)
	require.Empty(t, childVal)
}

func TestSkillsRequestProcessor_LoadedSkills_DoNotLeakAcrossAgents(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{{Name: "calc", Description: "math"}},
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "B"},
		},
	}

	sess := &session.Session{
		State: session.StateMap{
			skill.LoadedKey("child", "calc"): []byte("1"),
		},
	}
	parentInv := &agent.Invocation{
		InvocationID: "inv-parent",
		AgentName:    "parent",
		Session:      sess,
	}
	req := &model.Request{Messages: nil}

	ch := make(chan *event.Event, 2)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
	)
	p.ProcessRequest(context.Background(), parentInv, req, ch)

	require.NotEmpty(t, req.Messages)
	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.NotContains(t, sys, "[Loaded] calc")
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("*"),
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

func TestSkillsRequestProcessor_MaxLoadedSkills_EvictsOldest(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{
			{Name: "a", Description: "A"},
			{Name: "b", Description: "B"},
			{Name: "c", Description: "C"},
			{Name: "d", Description: "D"},
		},
		full: map[string]*skill.Skill{
			"a": {Summary: skill.Summary{Name: "a"}, Body: "A body"},
			"b": {Summary: skill.Summary{Name: "b"}, Body: "B body"},
			"c": {Summary: skill.Summary{Name: "c"}, Body: "C body"},
			"d": {Summary: skill.Summary{Name: "d"}, Body: "D body"},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "a"): []byte("1"),
				skill.LoadedKey("tester", "b"): []byte("1"),
				skill.LoadedKey("tester", "c"): []byte("1"),
				skill.LoadedKey("tester", "d"): []byte("1"),
				skill.LoadedOrderKey("tester"): []byte(
					`["a","b","c","d"]`,
				),
			},
			Events: []event.Event{
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" a",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" b",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" c",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" d",
				),
			},
		},
	}

	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base sys"),
	}}
	ch := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
		WithMaxLoadedSkills(3),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	sys := req.Messages[0].Content
	require.NotContains(t, sys, "[Loaded] a")
	require.Contains(t, sys, "[Loaded] b")
	require.Contains(t, sys, "[Loaded] c")
	require.Contains(t, sys, "[Loaded] d")

	v, ok := inv.Session.GetState(skill.LoadedKey("tester", "a"))
	require.True(t, ok)
	require.Empty(t, v)

	v, ok = inv.Session.GetState(skill.LoadedKey("tester", "b"))
	require.True(t, ok)
	require.Equal(t, []byte("1"), v)

	ev1 := <-ch
	require.NotNil(t, ev1)
	require.Equal(t, model.ObjectTypeStateUpdate, ev1.Object)
	require.Contains(t, ev1.StateDelta, skill.LoadedKey("tester", "a"))
	require.Contains(t, ev1.StateDelta, skill.DocsKey("tester", "a"))
	require.Equal(
		t,
		`["b","c","d"]`,
		string(ev1.StateDelta[skill.LoadedOrderKey("tester")]),
	)

	ev2 := <-ch
	require.NotNil(t, ev2)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev2.Object)
}

func TestSkillsRequestProcessor_MaxLoadedSkills_SelectDocsTouchesSkill(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{
			{Name: "a", Description: "A"},
			{Name: "b", Description: "B"},
			{Name: "c", Description: "C"},
			{Name: "d", Description: "D"},
		},
		full: map[string]*skill.Skill{
			"a": {Summary: skill.Summary{Name: "a"}, Body: "A body"},
			"b": {Summary: skill.Summary{Name: "b"}, Body: "B body"},
			"c": {Summary: skill.Summary{Name: "c"}, Body: "C body"},
			"d": {Summary: skill.Summary{Name: "d"}, Body: "D body"},
		},
	}

	args, err := json.Marshal(skillNameInput{Skill: "a"})
	require.NoError(t, err)

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "a"): []byte("1"),
				skill.LoadedKey("tester", "b"): []byte("1"),
				skill.LoadedKey("tester", "c"): []byte("1"),
				skill.LoadedKey("tester", "d"): []byte("1"),
				skill.LoadedOrderKey("tester"): []byte(
					`["b","c","d","a"]`,
				),
			},
			Events: []event.Event{
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" a",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" b",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" c",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" d",
				),
				toolResponseEvent(
					"tester",
					skillToolSelectDocs,
					string(args),
				),
			},
		},
	}

	req := &model.Request{Messages: nil}
	ch := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
		WithMaxLoadedSkills(3),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	sys := req.Messages[0].Content
	require.Contains(t, sys, "[Loaded] a")
	require.NotContains(t, sys, "[Loaded] b")
	require.Contains(t, sys, "[Loaded] c")
	require.Contains(t, sys, "[Loaded] d")

	v, ok := inv.Session.GetState(skill.LoadedKey("tester", "b"))
	require.True(t, ok)
	require.Empty(t, v)
	orderVal, ok := inv.Session.GetState(skill.LoadedOrderKey("tester"))
	require.True(t, ok)
	require.Equal(t, []byte(`["c","d","a"]`), orderVal)
}

func TestSkillsRequestProcessor_MaxLoadedSkills_ToolResultMode_EvictsOldest(
	t *testing.T,
) {
	repo := &mockRepo{
		sums: []skill.Summary{
			{Name: "a", Description: "A"},
			{Name: "b", Description: "B"},
			{Name: "c", Description: "C"},
			{Name: "d", Description: "D"},
		},
		full: map[string]*skill.Skill{
			"a": {Summary: skill.Summary{Name: "a"}, Body: "A body"},
			"b": {Summary: skill.Summary{Name: "b"}, Body: "B body"},
			"c": {Summary: skill.Summary{Name: "c"}, Body: "C body"},
			"d": {Summary: skill.Summary{Name: "d"}, Body: "D body"},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "a"): []byte("1"),
				skill.LoadedKey("tester", "b"): []byte("1"),
				skill.LoadedKey("tester", "c"): []byte("1"),
				skill.LoadedKey("tester", "d"): []byte("1"),
				skill.LoadedOrderKey("tester"): []byte(
					`["a","b","c","d"]`,
				),
			},
			Events: []event.Event{
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" a",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" b",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" c",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" d",
				),
			},
		},
	}

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("base sys"),
		},
	}
	ch := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
		WithSkillsLoadedContentInToolResults(true),
		WithMaxLoadedSkills(3),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	sys := req.Messages[0].Content
	require.Contains(t, sys, skillsOverviewHeader)
	require.NotContains(t, sys, "[Loaded] a")
	require.NotContains(t, sys, "[Loaded] b")
	require.NotContains(t, sys, "[Loaded] c")
	require.NotContains(t, sys, "[Loaded] d")

	v, ok := inv.Session.GetState(skill.LoadedKey("tester", "a"))
	require.True(t, ok)
	require.Empty(t, v)

	ev1 := <-ch
	require.NotNil(t, ev1)
	require.Equal(t, model.ObjectTypeStateUpdate, ev1.Object)
	require.Equal(
		t,
		`["b","c","d"]`,
		string(ev1.StateDelta[skill.LoadedOrderKey("tester")]),
	)

	ev2 := <-ch
	require.NotNil(t, ev2)
	require.Equal(t, model.ObjectTypePreprocessingInstruction, ev2.Object)
}

func TestSkillsRequestProcessor_MaxLoadedSkills_UsesStoredOrder(t *testing.T) {
	repo := &mockRepo{
		sums: []skill.Summary{
			{Name: "alpha", Description: "A"},
			{Name: "beta", Description: "B"},
			{Name: "gamma", Description: "C"},
		},
		full: map[string]*skill.Skill{
			"alpha": {
				Summary: skill.Summary{Name: "alpha"},
				Body:    "Alpha body",
			},
			"beta": {
				Summary: skill.Summary{Name: "beta"},
				Body:    "Beta body",
			},
			"gamma": {
				Summary: skill.Summary{Name: "gamma"},
				Body:    "Gamma body",
			},
		},
	}

	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "alpha"): []byte("1"),
				skill.LoadedKey("tester", "beta"):  []byte("1"),
				skill.LoadedKey("tester", "gamma"): []byte("1"),
				skill.LoadedOrderKey("tester"): []byte(
					`["alpha","beta","gamma"]`,
				),
			},
		},
	}

	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base sys"),
	}}
	ch := make(chan *event.Event, 3)
	p := NewSkillsRequestProcessor(
		repo,
		WithSkillLoadMode(SkillLoadModeSession),
		WithMaxLoadedSkills(2),
	)
	p.ProcessRequest(context.Background(), inv, req, ch)

	sys := req.Messages[0].Content
	require.NotContains(t, sys, "[Loaded] alpha")
	require.Contains(t, sys, "[Loaded] beta")
	require.Contains(t, sys, "[Loaded] gamma")
}

func TestKeepMostRecentSkills_UsesStoredOrder(t *testing.T) {
	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedOrderKey("tester"): []byte(
					`["a","b","c","d"]`,
				),
			},
		},
	}
	loaded := []string{"d", "b", "a", "c"}
	keep := keepMostRecentSkills(inv, loaded, 2)
	require.Equal(t, []string{"c", "d"}, keep)
}

func TestLoadedSkillOrder_FallsBackFromInvalidStateToEvents(t *testing.T) {
	inv := &agent.Invocation{
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedOrderKey("tester"): []byte("{"),
			},
			Events: []event.Event{
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" b",
				),
				toolResponseEvent(
					"tester",
					skillToolLoad,
					loadedPrefix+" a",
				),
			},
		},
	}

	order := loadedSkillOrder(
		inv,
		[]string{"c", "a", "b"},
	)
	require.Equal(t, []string{"b", "a", "c"}, order)
}

func TestKeepMostRecentSkills_FillsAlphabeticallyWhenNoEvents(t *testing.T) {
	inv := &agent.Invocation{
		Session: &session.Session{},
	}

	loaded := []string{"d", "b", "a", "b", " ", "c"}
	const max = 3

	keep := keepMostRecentSkills(inv, loaded, max)
	require.Equal(t, []string{"b", "c", "d"}, keep)
}

func TestKeepMostRecentSkills_EarlyReturns(t *testing.T) {
	keep := keepMostRecentSkills(nil, []string{"a"}, 1)
	require.Nil(t, keep)

	inv := &agent.Invocation{Session: nil}
	keep = keepMostRecentSkills(inv, []string{"a"}, 1)
	require.Nil(t, keep)

	inv = &agent.Invocation{Session: &session.Session{}}
	keep = keepMostRecentSkills(inv, []string{"a"}, 0)
	require.Nil(t, keep)

	keep = keepMostRecentSkills(inv, []string{" ", ""}, 1)
	require.Nil(t, keep)
}

func TestAppendSkillsToOrderFromToolResponseEvent_EarlyReturns(t *testing.T) {
	loadedSet := map[string]struct{}{
		"a": {},
	}

	order := appendSkillsToOrderFromToolResponseEvent(
		event.Event{},
		"",
		loadedSet,
		nil,
	)
	require.Nil(t, order)

	order = appendSkillsToOrderFromToolResponseEvent(
		event.Event{
			Response: &model.Response{
				Object: "not_tool_response",
			},
		},
		"",
		loadedSet,
		nil,
	)
	require.Nil(t, order)

	order = appendSkillsToOrderFromToolResponseEvent(
		event.Event{
			Response: &model.Response{
				Object: model.ObjectTypeToolResponse,
			},
		},
		"",
		loadedSet,
		nil,
	)
	require.Nil(t, order)
}

func TestAppendSkillsToOrderFromToolResp_SkipsInvalidMessages(t *testing.T) {
	loadedSet := map[string]struct{}{
		"a": {},
		"b": {},
	}

	ev := event.Event{
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
				},
			}, {
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: "other_tool",
				},
			}, {
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: skillToolSelectDocs,
					Content:  "not json",
				},
			}, {
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: skillToolLoad,
					Content:  loadedPrefix + " c",
				},
			}, {
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: skillToolLoad,
					Content:  loadedPrefix + " b",
				},
			}, {
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: skillToolLoad,
					Content:  loadedPrefix + " a",
				},
			}},
		},
	}

	order := appendSkillsToOrderFromToolResponseEvent(
		ev,
		"",
		loadedSet,
		[]string{"b"},
	)
	require.Equal(t, []string{"b", "a"}, order)
}

func TestSkillNameFromToolResponse_UnknownTool(t *testing.T) {
	name := skillNameFromToolResponse(model.Message{
		ToolName: "unknown_tool",
		Content:  "ignored",
	})
	require.Empty(t, name)
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("*"),
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
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

	inv.Session.SetState(skill.LoadedKey("tester", "calc"), nil)
	p.ProcessRequest(context.Background(), inv, req, nil)

	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		require.NotContains(t, m.Content, skillsLoadedContextHeader)
	}
}

func TestSkillsToolResultRequestProcessor_SessionSummary_DisablesFallbackWithoutCompactionSignal(
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
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

func TestSkillsToolResultRequestProcessor_SessionSummary_SkipsFallbackWhenMaterialized(
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
			},
		},
	}
	inv.SetState(contentHasSessionSummaryStateKey, true)

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

	p := NewSkillsToolResultRequestProcessor(
		repo,
		WithSkillsToolResultLoadMode(SkillLoadModeSession),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Contains(t, req.Messages[2].Content, "[Loaded] calc")
	require.Contains(t, req.Messages[2].Content, "B")

	for _, m := range req.Messages {
		if m.Role != model.RoleSystem {
			continue
		}
		require.NotContains(t, m.Content, skillsLoadedContextHeader)
	}
}

func TestSkillsToolResultRequestProcessor_SessionSummary_ReenablesFallbackWhenToolHistoryMissing(
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
			},
		},
	}
	inv.SetState(contentHasSessionSummaryStateKey, true)
	inv.SetState(contentHasCompactedToolResultsStateKey, true)

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
				skill.LoadedKey("tester", "calc"): []byte("1"),
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
		repo,
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
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.DocsKey("tester", "calc"):   []byte("[]"),
				skill.LoadedOrderKey("tester"): []byte(
					`["calc"]`,
				),
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
		skill.LoadedKey("tester", "calc"),
	)
	require.True(t, ok)
	require.Empty(t, loadedVal)

	docsVal, ok := inv.Session.GetState(
		skill.DocsKey("tester", "calc"),
	)
	require.True(t, ok)
	require.Empty(t, docsVal)
	orderVal, ok := inv.Session.GetState(skill.LoadedOrderKey("tester"))
	require.True(t, ok)
	require.Empty(t, orderVal)

	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, model.ObjectTypeStateUpdate, ev.Object)
	require.Contains(
		t,
		ev.StateDelta,
		skill.LoadedKey("tester", "calc"),
	)
	require.Contains(
		t,
		ev.StateDelta,
		skill.DocsKey("tester", "calc"),
	)
	require.Contains(
		t,
		ev.StateDelta,
		skill.LoadedOrderKey("tester"),
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
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.DocsKey("tester", "calc"): []byte("[bad]"),
			},
		},
	}
	p := NewSkillsToolResultRequestProcessor(repo)
	require.Empty(t, p.getDocsSelection(inv, repo, "calc"))

	inv.Session.SetState(skill.DocsKey("tester", "missing"), []byte("*"))
	require.Empty(t, p.getDocsSelection(inv, repo, "missing"))
}

func TestSkillsToolResultRequestProcessor_RepositoryResolver_MaterializesToolResult(
	t *testing.T,
) {
	dynamicRepo := &mockRepo{
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "Dynamic body"},
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
			},
		},
	}
	args, err := json.Marshal(skillNameInput{Skill: "calc"})
	require.NoError(t, err)
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "tc1",
					Function: model.FunctionDefinitionParam{
						Name:      skillToolLoad,
						Arguments: args,
					},
				}},
			},
			{
				Role:     model.RoleTool,
				ToolName: skillToolLoad,
				ToolID:   "tc1",
				Content:  loadedPrefix + " calc",
			},
		},
	}
	p := NewSkillsToolResultRequestProcessor(
		nil,
		WithSkillsToolResultRepositoryResolver(
			func(*agent.Invocation) skill.Repository {
				return dynamicRepo
			},
		),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)
	require.Contains(t, req.Messages[2].Content, "[Loaded] calc")
	require.Contains(t, req.Messages[2].Content, "Dynamic body")
}

func TestSkillsToolResultRequestProcessor_RepositoryResolver_CanDisableStaticRepository(
	t *testing.T,
) {
	staticRepo := &mockRepo{
		full: map[string]*skill.Skill{
			"calc": {Summary: skill.Summary{Name: "calc"}, Body: "Static body"},
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
			},
		},
	}
	args, err := json.Marshal(skillNameInput{Skill: "calc"})
	require.NoError(t, err)
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   "tc1",
					Function: model.FunctionDefinitionParam{
						Name:      skillToolLoad,
						Arguments: args,
					},
				}},
			},
			{
				Role:     model.RoleTool,
				ToolName: skillToolLoad,
				ToolID:   "tc1",
				Content:  loadedPrefix + " calc",
			},
		},
	}
	p := NewSkillsToolResultRequestProcessor(
		staticRepo,
		WithSkillsToolResultRepositoryResolver(
			func(*agent.Invocation) skill.Repository {
				return nil
			},
		),
	)
	p.ProcessRequest(context.Background(), inv, req, nil)
	require.Equal(t, loadedPrefix+" calc", req.Messages[2].Content)
	require.Equal(t, -1, findLoadedContextMessageIndex(req.Messages))
}

func TestSkillsToolResultRequestProcessor_RepositoryResolver_DoesNotPanicOnNilInvocation(
	t *testing.T,
) {
	p := NewSkillsToolResultRequestProcessor(
		nil,
		WithSkillsToolResultRepositoryResolver(
			func(inv *agent.Invocation) skill.Repository {
				require.Nil(t, inv)
				return nil
			},
		),
	)
	require.NotPanics(t, func() {
		p.ProcessRequest(context.Background(), nil, &model.Request{}, nil)
	})
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
		AgentName: "tester",
		Session: &session.Session{
			State: session.StateMap{
				skill.LoadedKey("tester", "calc"): []byte("1"),
				skill.LoadedOrderKey("tester"): []byte(
					`["calc"]`,
				),
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

	v, ok := inv.Session.GetState(skill.LoadedKey("tester", "calc"))
	require.True(t, ok)
	require.Equal(t, []byte("1"), v)
	orderVal, ok := inv.Session.GetState(skill.LoadedOrderKey("tester"))
	require.True(t, ok)
	require.Equal(t, []byte(`["calc"]`), orderVal)
	require.Len(t, ch, 0)
}

func TestMaybeMigrateLegacySkillState_EarlyReturns(t *testing.T) {
	ch := make(chan *event.Event, 1)
	maybeMigrateLegacySkillState(context.Background(), nil, ch)
	require.Len(t, ch, 0)

	inv := &agent.Invocation{Session: nil}
	maybeMigrateLegacySkillState(context.Background(), inv, ch)
	require.Len(t, ch, 0)

	inv = &agent.Invocation{Session: &session.Session{}}
	maybeMigrateLegacySkillState(context.Background(), inv, ch)
	require.Len(t, ch, 0)
}

func TestMaybeMigrateLegacySkillState_MigratesLegacyKeys(t *testing.T) {
	const (
		coordinator = "coordinator"
		subAgent    = "sub"
		skillName   = "demo"
		loadedVal   = "1"
		docsVal     = "[\"A.md\"]"
	)

	legacyLoadedKey := skill.StateKeyLoadedPrefix + skillName
	legacyDocsKey := skill.StateKeyDocsPrefix + skillName
	unrelatedKey := "temp:unrelated"
	emptyLoadedKey := skill.StateKeyLoadedPrefix + "empty"

	sess := &session.Session{
		State: session.StateMap{
			legacyLoadedKey:            []byte(loadedVal),
			legacyDocsKey:              []byte(docsVal),
			skill.StateKeyLoadedPrefix: []byte("1"),
			emptyLoadedKey:             nil,
			unrelatedKey:               []byte("x"),
		},
		Events: []event.Event{
			toolResponseEvent(
				subAgent,
				skillToolLoad,
				loadedPrefix+" "+skillName,
			),
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    coordinator,
		Session:      sess,
	}

	ch := make(chan *event.Event, 1)
	maybeMigrateLegacySkillState(context.Background(), inv, ch)

	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, model.ObjectTypeStateUpdate, ev.Object)

	scopedLoadedKey := skill.LoadedKey(subAgent, skillName)
	scopedDocsKey := skill.DocsKey(subAgent, skillName)

	require.Equal(t, []byte(loadedVal), ev.StateDelta[scopedLoadedKey])
	require.Equal(t, []byte(docsVal), ev.StateDelta[scopedDocsKey])
	require.Contains(t, ev.StateDelta, legacyLoadedKey)
	require.Contains(t, ev.StateDelta, legacyDocsKey)
	require.Nil(t, ev.StateDelta[legacyLoadedKey])
	require.Nil(t, ev.StateDelta[legacyDocsKey])

	v, ok := sess.GetState(scopedLoadedKey)
	require.True(t, ok)
	require.Equal(t, []byte(loadedVal), v)

	v, ok = sess.GetState(scopedDocsKey)
	require.True(t, ok)
	require.Equal(t, []byte(docsVal), v)

	v, ok = sess.GetState(legacyLoadedKey)
	require.True(t, ok)
	require.Nil(t, v)

	v, ok = sess.GetState(legacyDocsKey)
	require.True(t, ok)
	require.Nil(t, v)

	maybeMigrateLegacySkillState(context.Background(), inv, ch)
	require.Len(t, ch, 0)
}

func TestMaybeMigrateLegacySkillState_ClearsLegacyWhenScopedExists(
	t *testing.T,
) {
	const (
		owner     = "sub"
		skillName = "demo"
		scopedVal = "new"
		legacyVal = "legacy"
	)

	legacyKey := skill.StateKeyLoadedPrefix + skillName
	scopedKey := skill.LoadedKey(owner, skillName)

	sess := &session.Session{
		State: session.StateMap{
			scopedKey: []byte(scopedVal),
			legacyKey: []byte(legacyVal),
		},
		Events: []event.Event{
			toolResponseEvent(
				owner,
				skillToolLoad,
				loadedPrefix+" "+skillName,
			),
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    "coordinator",
		Session:      sess,
	}

	ch := make(chan *event.Event, 1)
	maybeMigrateLegacySkillState(context.Background(), inv, ch)

	ev := <-ch
	require.NotNil(t, ev)
	require.Equal(t, model.ObjectTypeStateUpdate, ev.Object)
	require.Contains(t, ev.StateDelta, legacyKey)
	require.NotContains(t, ev.StateDelta, scopedKey)
	require.Nil(t, ev.StateDelta[legacyKey])

	v, ok := sess.GetState(scopedKey)
	require.True(t, ok)
	require.Equal(t, []byte(scopedVal), v)

	v, ok = sess.GetState(legacyKey)
	require.True(t, ok)
	require.Nil(t, v)
}

func TestMaybeMigrateLegacySkillState_NoOwnerKeepsLegacyState(
	t *testing.T,
) {
	const (
		skillName = "demo"
		legacyVal = "1"
	)

	legacyKey := skill.StateKeyLoadedPrefix + skillName
	sess := &session.Session{
		State: session.StateMap{
			legacyKey: []byte(legacyVal),
		},
	}
	inv := &agent.Invocation{
		InvocationID: "inv1",
		AgentName:    " ",
		Session:      sess,
	}

	ch := make(chan *event.Event, 1)
	maybeMigrateLegacySkillState(context.Background(), inv, ch)
	require.Len(t, ch, 0)

	v, ok := sess.GetState(legacyKey)
	require.True(t, ok)
	require.Equal(t, []byte(legacyVal), v)
}

func TestAddOwnersFromEvent_EarlyReturnsAndSkips(t *testing.T) {
	toolEvent := func(
		author string,
		role model.Role,
		toolName string,
		content string,
	) event.Event {
		return event.Event{
			Author: author,
			Response: &model.Response{
				Object: model.ObjectTypeToolResponse,
				Choices: []model.Choice{{
					Message: model.Message{
						Role:     role,
						ToolName: toolName,
						Content:  content,
					},
				}},
			},
		}
	}

	owners := map[string]string{}
	owners = addOwnersFromEvent(event.Event{}, owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(event.Event{
		Author: "a",
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
		},
	}, owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(event.Event{
		Author: "a",
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
		},
	}, owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(toolEvent(
		" ",
		model.RoleTool,
		skillToolLoad,
		loadedPrefix+" demo",
	), owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(toolEvent(
		"a",
		model.RoleAssistant,
		skillToolLoad,
		loadedPrefix+" demo",
	), owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(toolEvent(
		"a",
		model.RoleTool,
		"other_tool",
		loadedPrefix+" demo",
	), owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(toolEvent(
		"a",
		model.RoleTool,
		skillToolLoad,
		"not loaded",
	), owners)
	require.Empty(t, owners)

	owners = addOwnersFromEvent(toolEvent(
		"a",
		model.RoleTool,
		skillToolSelectDocs,
		"{",
	), owners)
	require.Empty(t, owners)
}

func TestLegacySkillOwners_PrefersMostRecent(t *testing.T) {
	const skillName = "demo"

	events := []event.Event{
		toolResponseEvent(
			"old",
			skillToolLoad,
			loadedPrefix+" "+skillName,
		),
		toolResponseEvent(
			"new",
			skillToolLoad,
			loadedPrefix+" "+skillName,
		),
	}
	owners := legacySkillOwners(events)
	require.Equal(t, "new", owners[skillName])
}

func toolResponseEvent(
	author string,
	toolName string,
	content string,
) event.Event {
	return event.Event{
		Author: author,
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: toolName,
					Content:  content,
				},
			}},
		},
	}
}
