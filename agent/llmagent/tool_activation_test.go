//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/toolsnapshot"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type activationTool struct {
	name string
}

func (t activationTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

type activationToolSet struct {
	name  string
	tools []tool.Tool
}

func (s activationToolSet) Tools(context.Context) []tool.Tool {
	return s.tools
}

func (s activationToolSet) Close() error {
	return nil
}

func (s activationToolSet) Name() string {
	return s.name
}

func TestToolActivationOptionsValidateToolSets(t *testing.T) {
	require.Panics(t, func() {
		New(
			"agent",
			WithActivatableToolSets([]tool.ToolSet{
				activationToolSet{name: "dup"},
				activationToolSet{name: "dup"},
			}),
		)
	})
	require.Panics(t, func() {
		New(
			"agent",
			WithActivatableToolSets([]tool.ToolSet{
				activationToolSet{name: "github"},
			}),
			WithToolActivationOnSkillLoad("review", []string{"missing"}),
		)
	})
	require.Panics(t, func() {
		New(
			"agent",
			WithActivatableToolSets([]tool.ToolSet{
				activationToolSet{name: "github"},
			}),
			WithToolActivationOnSkillLoad("", []string{"github"}),
		)
	})
	require.Panics(t, func() {
		New(
			"agent",
			WithActivatableToolSets([]tool.ToolSet{
				activationToolSet{name: "github"},
			}),
			WithToolActivationOnSkillLoad("review", nil),
		)
	})
	require.Panics(t, func() {
		New(
			"agent",
			WithActivatableToolSets([]tool.ToolSet{
				activationToolSet{name: "github"},
			}),
			WithToolActivationOnSkillLoad(
				"review",
				[]string{"github"},
				WithToolActivationMode(ToolActivationMode("append")),
			),
		)
	})
	require.Panics(t, func() {
		New(
			"agent",
			WithActivatableToolSets([]tool.ToolSet{
				activationToolSet{name: "github"},
			}),
			WithToolActivationOnSkillLoad(
				"review",
				[]string{"github"},
				WithToolActivationLifetime(ToolActivationLifetime("turn")),
			),
		)
	})
}

func TestToolActivationPostToolResultWritesRecords(t *testing.T) {
	agt := New(
		"agent",
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "github",
				tools: []tool.Tool{activationTool{name: "search"}},
			},
			activationToolSet{
				name:  "browser",
				tools: []tool.Tool{activationTool{name: "open"}},
			},
		}),
		WithToolActivationOnSkillLoad(
			"research",
			[]string{"github"},
			WithToolActivationMode(ToolActivationModeInclude),
			WithToolActivationLifetime(ToolActivationLifetimeInvocation),
		),
		WithToolActivationOnSkillLoad(
			"research",
			[]string{"browser"},
			WithToolActivationMode(ToolActivationModeOnly),
			WithToolActivationLifetime(ToolActivationLifetimeSession),
		),
	)
	inv := &agent.Invocation{
		AgentName: "agent",
		Session:   session.NewSession("app", "user", "session"),
	}
	toolsnapshot.Set(inv, []tool.Tool{activationTool{name: "old"}}, true)
	ev := &event.Event{
		StateDelta: map[string][]byte{
			skill.LoadedKey("agent", "research"): []byte("1"),
		},
	}
	agt.handleToolActivationPostToolResult(context.Background(), inv, ev)
	require.Equal(t, []toolActivationRecord{
		{
			Mode:        ToolActivationModeInclude,
			Lifetime:    ToolActivationLifetimeInvocation,
			ToolSetName: "github",
		},
		{
			Mode:        ToolActivationModeOnly,
			Lifetime:    ToolActivationLifetimeSession,
			ToolSetName: "browser",
		},
	}, invocationToolActivationRecords(inv))
	sessionKey := toolActivationSessionKey("agent", toolActivationRecord{
		Mode:        ToolActivationModeOnly,
		Lifetime:    ToolActivationLifetimeSession,
		ToolSetName: "browser",
	})
	require.NotEmpty(t, ev.StateDelta[sessionKey])
	_, ok := toolsnapshot.Get(inv)
	require.False(t, ok)
}

func TestToolActivationPostToolResultIgnoresUnmatchedSkill(t *testing.T) {
	agt := New(
		"agent",
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{name: "github"},
		}),
		WithToolActivationOnSkillLoad("review", []string{"github"}),
	)
	inv := &agent.Invocation{
		AgentName: "agent",
		Session:   session.NewSession("app", "user", "session"),
	}
	ev := &event.Event{
		StateDelta: map[string][]byte{
			skill.LoadedKey("agent", "other"): []byte("1"),
		},
	}
	agt.handleToolActivationPostToolResult(context.Background(), inv, ev)
	require.Empty(t, invocationToolActivationRecords(inv))
}

func TestToolActivationSkillLoadUpdatesNextModelRequestTools(t *testing.T) {
	repo, err := skill.NewFSRepository(
		createNamedTestSkill(t, "research", "research skill"),
	)
	require.NoError(t, err)
	mockModel := &activationSequenceModel{
		responses: []*model.Response{
			activationToolCallResponse(t, "call-1", "research"),
			activationFinalResponse("done"),
		},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithSkills(repo),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "browser",
				tools: []tool.Tool{activationTool{name: "open"}},
			},
		}),
		WithToolActivationOnSkillLoad("research", []string{"browser"}),
	)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      session.NewSession("app", "user", "session"),
		Message:      model.NewUserMessage("load research"),
	}
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 2)
	require.Contains(t, requests[0].Tools, "skill_load")
	require.NotContains(t, requests[0].Tools, "browser_open")
	require.Contains(t, requests[1].Tools, "browser_open")
}

func TestToolActivationSessionLifetimeVisibleInNextInvocation(t *testing.T) {
	repo, err := skill.NewFSRepository(
		createNamedTestSkill(t, "research", "research skill"),
	)
	require.NoError(t, err)
	mockModel := &activationSequenceModel{
		responses: []*model.Response{
			activationToolCallResponse(t, "call-1", "research"),
			activationFinalResponse("first done"),
			activationFinalResponse("second done"),
		},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithSkills(repo),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "browser",
				tools: []tool.Tool{activationTool{name: "open"}},
			},
		}),
		WithToolActivationOnSkillLoad(
			"research",
			[]string{"browser"},
			WithToolActivationLifetime(ToolActivationLifetimeSession),
		),
	)
	sess := session.NewSession("app", "user", "session")
	inv1 := &agent.Invocation{
		InvocationID: "inv-1",
		Session:      sess,
		Message:      model.NewUserMessage("load research"),
	}
	events, err := agt.Run(context.Background(), inv1)
	require.NoError(t, err)
	drainEventsAndApplyStateDelta(t, sess, events)
	inv2 := &agent.Invocation{
		InvocationID: "inv-2",
		Session:      sess,
		Message:      model.NewUserMessage("use research"),
	}
	events, err = agt.Run(context.Background(), inv2)
	require.NoError(t, err)
	drainEventsAndApplyStateDelta(t, sess, events)
	requests := mockModel.Requests()
	require.Len(t, requests, 3)
	require.Contains(t, requests[1].Tools, "browser_open")
	require.Contains(t, requests[2].Tools, "browser_open")
}

func TestToolActivationInvocationLifetimeNotVisibleInNextInvocation(t *testing.T) {
	repo, err := skill.NewFSRepository(
		createNamedTestSkill(t, "research", "research skill"),
	)
	require.NoError(t, err)
	mockModel := &activationSequenceModel{
		responses: []*model.Response{
			activationToolCallResponse(t, "call-1", "research"),
			activationFinalResponse("first done"),
			activationFinalResponse("second done"),
		},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithSkills(repo),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "browser",
				tools: []tool.Tool{activationTool{name: "open"}},
			},
		}),
		WithToolActivationOnSkillLoad(
			"research",
			[]string{"browser"},
			WithToolActivationLifetime(ToolActivationLifetimeInvocation),
		),
	)
	sess := session.NewSession("app", "user", "session")
	inv1 := &agent.Invocation{
		InvocationID: "inv-1",
		Session:      sess,
		Message:      model.NewUserMessage("load research"),
	}
	events, err := agt.Run(context.Background(), inv1)
	require.NoError(t, err)
	drainEventsAndApplyStateDelta(t, sess, events)
	inv2 := &agent.Invocation{
		InvocationID: "inv-2",
		Session:      sess,
		Message:      model.NewUserMessage("use research"),
	}
	events, err = agt.Run(context.Background(), inv2)
	require.NoError(t, err)
	drainEventsAndApplyStateDelta(t, sess, events)
	requests := mockModel.Requests()
	require.Len(t, requests, 3)
	require.Contains(t, requests[1].Tools, "browser_open")
	require.NotContains(t, requests[2].Tools, "browser_open")
}

func TestToolActivationOnlyTrimsRunOptionTools(t *testing.T) {
	mockModel := &activationSequenceModel{
		responses: []*model.Response{activationFinalResponse("done")},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "safe",
				tools: []tool.Tool{activationTool{name: "browse"}},
			},
		}),
		WithToolActivationOnSkillLoad(
			"unused",
			[]string{"safe"},
			WithToolActivationMode(ToolActivationModeOnly),
		),
	)
	record, ok := newToolActivationRecord(
		ToolActivationModeOnly,
		ToolActivationLifetimeInvocation,
		"safe",
	)
	require.True(t, ok)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      session.NewSession("app", "user", "session"),
		Message:      model.NewUserMessage("use safe tools"),
		RunOptions: agent.RunOptions{
			AdditionalTools: []tool.Tool{activationTool{name: "extra"}},
			ExternalTools:   []tool.Tool{activationTool{name: "external"}},
		},
	}
	require.True(t, addInvocationToolActivationRecords(inv, []toolActivationRecord{record}))
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 1)
	require.Contains(t, requests[0].Tools, "safe_browse")
	require.NotContains(t, requests[0].Tools, "extra")
	require.NotContains(t, requests[0].Tools, "external")
	require.Empty(t, inv.RunOptions.ExternalToolNames)
}

func TestToolActivationOnlyExcludesIncludeActivatedTools(t *testing.T) {
	mockModel := &activationSequenceModel{
		responses: []*model.Response{activationFinalResponse("done")},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "safe",
				tools: []tool.Tool{activationTool{name: "browse"}},
			},
			activationToolSet{
				name:  "extra",
				tools: []tool.Tool{activationTool{name: "search"}},
			},
		}),
		WithToolActivationOnSkillLoad(
			"only",
			[]string{"safe"},
			WithToolActivationMode(ToolActivationModeOnly),
		),
		WithToolActivationOnSkillLoad("include", []string{"extra"}),
	)
	onlyRecord, ok := newToolActivationRecord(
		ToolActivationModeOnly,
		ToolActivationLifetimeInvocation,
		"safe",
	)
	require.True(t, ok)
	includeRecord, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeInvocation,
		"extra",
	)
	require.True(t, ok)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      session.NewSession("app", "user", "session"),
		Message:      model.NewUserMessage("use safe tools"),
	}
	require.True(t, addInvocationToolActivationRecords(
		inv,
		[]toolActivationRecord{onlyRecord, includeRecord},
	))
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 1)
	require.Contains(t, requests[0].Tools, "safe_browse")
	require.NotContains(t, requests[0].Tools, "extra_search")
}

func TestToolActivationIncludeReplacesExternalToolWithSameName(t *testing.T) {
	record, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeInvocation,
		"safe",
	)
	require.True(t, ok)
	inv := &agent.Invocation{
		AgentName: "agent",
		Session:   session.NewSession("app", "user", "session"),
	}
	require.True(t, addInvocationToolActivationRecords(
		inv,
		[]toolActivationRecord{record},
	))
	external := activationTool{name: "safe_browse"}
	userToolNames := map[string]bool{"safe_browse": true}
	externalToolNames := map[string]bool{"safe_browse": true}
	out, userNames, externalNames := applyToolActivationRecords(
		context.Background(),
		inv,
		[]tool.Tool{external},
		userToolNames,
		externalToolNames,
		[]tool.ToolSet{
			activationToolSet{
				name:  "safe",
				tools: []tool.Tool{activationTool{name: "browse"}},
			},
		},
		nil,
	)
	require.Len(t, out, 1)
	activated, ok := out[0].(*itool.NamedTool)
	require.True(t, ok)
	require.Equal(t, "safe_browse", activated.Declaration().Name)
	require.True(t, userNames["safe_browse"])
	require.Empty(t, externalNames)
	require.Equal(t, map[string]bool{"safe_browse": true}, externalToolNames)
}

func TestToolActivationRunFilterAppliesToActivatedTools(t *testing.T) {
	mockModel := &activationSequenceModel{
		responses: []*model.Response{activationFinalResponse("done")},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "safe",
				tools: []tool.Tool{activationTool{name: "browse"}},
			},
		}),
		WithToolActivationOnSkillLoad("unused", []string{"safe"}),
	)
	record, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeInvocation,
		"safe",
	)
	require.True(t, ok)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      session.NewSession("app", "user", "session"),
		Message:      model.NewUserMessage("use safe tools"),
		RunOptions: agent.RunOptions{
			ToolFilter: func(_ context.Context, tl tool.Tool) bool {
				return tl.Declaration().Name != "safe_browse"
			},
		},
	}
	require.True(t, addInvocationToolActivationRecords(inv, []toolActivationRecord{record}))
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 1)
	require.NotContains(t, requests[0].Tools, "safe_browse")
}

func TestToolActivationWithoutRulesIgnoresSessionRecords(t *testing.T) {
	mockModel := &activationSequenceModel{
		responses: []*model.Response{activationFinalResponse("done")},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "browser",
				tools: []tool.Tool{activationTool{name: "open"}},
			},
		}),
	)
	sess := session.NewSession("app", "user", "session")
	record, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeSession,
		"browser",
	)
	require.True(t, ok)
	sess.SetState(
		toolActivationSessionKey("agent", record),
		marshalToolActivationRecord(record),
	)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      sess,
		Message:      model.NewUserMessage("no activation rules"),
	}
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 1)
	require.NotContains(t, requests[0].Tools, "browser_open")
}

func TestToolActivationOutputSchemaIgnoresSessionRecords(t *testing.T) {
	mockModel := &activationSequenceModel{
		responses: []*model.Response{{
			Done: true,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: `{"ok":true}`,
				},
			}},
		}},
	}
	agt := New(
		"agent",
		WithModel(mockModel),
		WithOutputSchema(map[string]any{"type": "object"}),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "browser",
				tools: []tool.Tool{activationTool{name: "open"}},
			},
		}),
	)
	sess := session.NewSession("app", "user", "session")
	record, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeSession,
		"browser",
	)
	require.True(t, ok)
	sess.SetState(
		toolActivationSessionKey("agent", record),
		marshalToolActivationRecord(record),
	)
	inv := &agent.Invocation{
		InvocationID: "inv",
		Session:      sess,
		Message:      model.NewUserMessage("return json"),
	}
	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}
	requests := mockModel.Requests()
	require.Len(t, requests, 1)
	require.NotContains(t, requests[0].Tools, "browser_open")
}

func TestToolActivationSessionKeyEscapesSegmentsWithoutCollision(t *testing.T) {
	slashRecord, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeSession,
		"docs/a",
	)
	require.True(t, ok)
	escapedRecord, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeSession,
		"docs%2Fa",
	)
	require.True(t, ok)
	require.NotEqual(
		t,
		toolActivationSessionKey("agent", slashRecord),
		toolActivationSessionKey("agent", escapedRecord),
	)
	require.NotEqual(
		t,
		toolActivationSessionPrefixForAgent("agent/a"),
		toolActivationSessionPrefixForAgent("agent%2Fa"),
	)
}

func TestToolActivationValidationAndRecordEdgeCases(t *testing.T) {
	require.NoError(t, validateAndNormalizeToolActivationOptions(nil))
	_, err := collectActivatableToolSetNames([]tool.ToolSet{nil})
	require.Error(t, err)
	_, err = collectActivatableToolSetNames([]tool.ToolSet{
		activationToolSet{name: " "},
	})
	require.Error(t, err)
	trigger, err := normalizeToolActivationTrigger(toolActivationTrigger{
		kind: toolActivationTriggerKind("unsupported"),
	})
	require.Error(t, err)
	require.Empty(t, trigger)
	require.Equal(
		t,
		"unsupported",
		(toolActivationTrigger{kind: "unsupported"}).describe(),
	)
	require.Equal(t, []string{"a", "b"}, normalizeToolSetNames([]string{
		" a ",
		"",
		"a",
		"b",
	}))
	require.False(t, (toolActivationRule{
		trigger: toolActivationTrigger{kind: "unsupported"},
	}).matchesLoadedSkills(map[string]bool{"a": true}))
	_, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeInvocation,
		" ",
	)
	require.False(t, ok)
	_, ok = newToolActivationRecord(
		ToolActivationMode("append"),
		ToolActivationLifetimeInvocation,
		"x",
	)
	require.False(t, ok)
	_, ok = newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetime("turn"),
		"x",
	)
	require.False(t, ok)
	record, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeInvocation,
		"x",
	)
	require.True(t, ok)
	require.False(t, addInvocationToolActivationRecords(nil, []toolActivationRecord{record}))
	require.False(t, addInvocationToolActivationRecords(&agent.Invocation{}, nil))
	inv := &agent.Invocation{}
	require.True(t, addInvocationToolActivationRecords(inv, []toolActivationRecord{record}))
	require.False(t, addInvocationToolActivationRecords(inv, []toolActivationRecord{record}))
	require.False(t, sameToolActivationRecords(
		[]toolActivationRecord{record},
		[]toolActivationRecord{{
			Mode:        ToolActivationModeInclude,
			Lifetime:    ToolActivationLifetimeInvocation,
			ToolSetName: "y",
		}},
	))
	require.True(t, sameToolActivationRecords(
		[]toolActivationRecord{record},
		[]toolActivationRecord{record},
	))
}

func TestSessionToolActivationRecordsSkipsInvalidState(t *testing.T) {
	ctx := context.Background()
	require.Empty(t, sessionToolActivationRecords(ctx, nil))
	require.Empty(t, sessionToolActivationRecords(ctx, &agent.Invocation{}))
	sess := session.NewSession("app", "user", "session")
	inv := &agent.Invocation{AgentName: "agent", Session: sess}
	require.Empty(t, sessionToolActivationRecords(ctx, inv))
	prefix := toolActivationSessionPrefixForAgent("agent")
	valid, ok := newToolActivationRecord(
		ToolActivationModeInclude,
		ToolActivationLifetimeSession,
		"valid",
	)
	require.True(t, ok)
	invalidLifetime := toolActivationRecord{
		Mode:        ToolActivationModeInclude,
		Lifetime:    ToolActivationLifetimeInvocation,
		ToolSetName: "invalid",
	}
	sess.SetState(prefix+"empty", []byte{})
	sess.SetState(prefix+"bad-json", []byte("{"))
	sess.SetState(prefix+"invalid-lifetime", marshalToolActivationRecord(invalidLifetime))
	sess.SetState(toolActivationSessionKey("agent", valid), marshalToolActivationRecord(valid))
	require.Equal(t, []toolActivationRecord{valid}, sessionToolActivationRecords(ctx, inv))
}

func TestToolActivationExpansionSkipsDuplicatesAndFilteredTools(t *testing.T) {
	ctx := context.Background()
	toolSet := activationToolSet{
		name: "safe",
		tools: []tool.Tool{
			activationTool{name: "browse"},
			activationTool{name: "browse"},
			activationTool{name: "skip"},
		},
	}
	accepted := map[string]bool{}
	tools := expandOneToolActivationSet(
		ctx,
		toolSet,
		accepted,
		func(_ context.Context, tl tool.Tool) bool {
			return toolActivationToolName(tl) != "safe_skip"
		},
	)
	require.Len(t, tools, 1)
	require.Equal(t, "safe_browse", toolActivationToolName(tools[0]))
	require.True(t, accepted["safe_browse"])
	require.Empty(t, expandOneToolActivationSet(
		ctx,
		activationToolSet{
			name:  "safe",
			tools: []tool.Tool{activationTool{name: "browse"}},
		},
		accepted,
		nil,
	))
	require.Empty(t, expandOneToolActivationSet(
		ctx,
		activationToolSet{name: "empty"},
		map[string]bool{},
		nil,
	))
}

type activationSequenceModel struct {
	mu        sync.Mutex
	responses []*model.Response
	requests  []*model.Request
}

func (m *activationSequenceModel) GenerateContent(
	_ context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, request)
	idx := len(m.requests) - 1
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	if idx < len(m.responses) {
		ch <- m.responses[idx]
	}
	close(ch)
	return ch, nil
}

func (m *activationSequenceModel) Info() model.Info {
	return model.Info{Name: "activation-sequence-model"}
}

func (m *activationSequenceModel) Requests() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.requests...)
}

func activationFinalResponse(content string) *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: content,
			},
		}},
	}
}

func drainEventsAndApplyStateDelta(
	t *testing.T,
	sess *session.Session,
	events <-chan *event.Event,
) {
	t.Helper()
	for ev := range events {
		sess.ApplyEventStateDelta(ev)
	}
}

func activationToolCallResponse(
	t *testing.T,
	callID string,
	skillName string,
) *model.Response {
	t.Helper()
	args, err := json.Marshal(map[string]string{"skill": skillName})
	require.NoError(t, err)
	return &model.Response{
		Model: "activation-sequence-model",
		Done:  true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: callID,
					Function: model.FunctionDefinitionParam{
						Name:      "skill_load",
						Arguments: args,
					},
				}},
			},
		}},
	}
}
