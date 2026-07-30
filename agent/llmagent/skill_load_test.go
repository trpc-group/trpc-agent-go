//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type skillLoadTestRepository struct {
	skills map[string]*skill.Skill
	err    error
}

func (r *skillLoadTestRepository) Summaries() []skill.Summary {
	var out []skill.Summary
	for _, resolved := range r.skills {
		out = append(out, resolved.Summary)
	}
	return out
}

func (r *skillLoadTestRepository) Get(name string) (*skill.Skill, error) {
	if r.err != nil {
		return nil, r.err
	}
	resolved, ok := r.skills[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	return resolved, nil
}

func TestPrepareSkillLoadsPreservesRepositoryFailure(t *testing.T) {
	repositoryErr := errors.New("repository unavailable")
	repo := &skillLoadTestRepository{
		skills: map[string]*skill.Skill{
			"review": {Summary: skill.Summary{Name: "review"}},
		},
		err: repositoryErr,
	}
	agt := New("tester", WithSkills(repo))
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{Name: "review"}},
		}),
	)
	agt.setupInvocation(inv)

	err := agt.prepareSkillLoads(context.Background(), inv)
	require.Error(t, err)
	require.True(t, errors.Is(err, repositoryErr))
	require.False(t, errors.Is(err, skill.ErrSkillUnavailable))
	require.Empty(t, inv.Session.SnapshotState())
}

func TestPrepareSkillLoadsWithoutRepositoryIsUnavailable(t *testing.T) {
	agt := New("tester")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{Name: "review"}},
		}),
	)
	agt.setupInvocation(inv)

	err := agt.prepareSkillLoads(context.Background(), inv)
	require.ErrorIs(t, err, skill.ErrSkillUnavailable)
	require.NotErrorIs(t, err, skill.ErrInvalidLoadRequest)
	require.Empty(t, inv.Session.SnapshotState())
}

func (r *skillLoadTestRepository) Path(string) (string, error) {
	return "", nil
}

func TestPrepareSkillLoadsNormalizesCompleteBatch(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {
			Summary: skill.Summary{Name: "review"},
			Docs: []skill.Doc{{
				Path: "guide.md",
			}},
		},
	}}
	agt := New("tester", WithSkills(repo))
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{
				Name: " review ",
				Docs: []string{" guide.md "},
			}},
		}),
	)
	agt.setupInvocation(inv)

	err := agt.prepareSkillLoads(context.Background(), inv)
	require.NoError(t, err)
	require.Equal(t, []skill.LoadRequest{{
		Name: "review",
		Docs: []string{"guide.md"},
	}}, inv.RunOptions.SkillLoads)
	_, loaded := inv.Session.GetState(skill.LoadedKey("tester", "review"))
	require.False(t, loaded, "preflight must not partially commit state")
}

func TestPrepareSkillLoadsFailsAtomically(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {
			Summary: skill.Summary{Name: "review"},
		},
	}}
	agt := New("tester", WithSkills(repo))
	original := []skill.LoadRequest{
		{Name: "review"},
		{Name: "missing"},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: original,
		}),
	)
	agt.setupInvocation(inv)

	err := agt.prepareSkillLoads(context.Background(), inv)
	require.Error(t, err)
	require.True(t, errors.Is(err, skill.ErrSkillUnavailable))
	require.Equal(t, original, inv.RunOptions.SkillLoads)
	require.Empty(t, inv.Session.SnapshotState())
}

func TestPrepareSkillLoadsRejectsConflictingDocSelection(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {
			Summary: skill.Summary{Name: "review"},
		},
	}}
	agt := New("tester", WithSkills(repo))
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{
				Name:           "review",
				Docs:           []string{"guide.md"},
				IncludeAllDocs: true,
			}},
		}),
	)
	agt.setupInvocation(inv)

	err := agt.prepareSkillLoads(context.Background(), inv)
	require.Error(t, err)
	require.True(t, errors.Is(err, skill.ErrInvalidLoadRequest))
	require.Empty(t, inv.Session.SnapshotState())
}

func TestDeclaredSkillLoadIncludesAllDocs(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {
			Summary: skill.Summary{Name: "review"},
			Body:    "Review body",
			Docs: []skill.Doc{
				{Path: "a.md", Content: "Doc A"},
				{Path: "b.md", Content: "Doc B"},
			},
		},
	}}
	modelStub := &captureModel{}
	agt := New(
		"tester",
		WithModel(modelStub),
		WithSkills(repo),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationMessage(model.NewUserMessage("review")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{
				Name:           "review",
				IncludeAllDocs: true,
			}},
		}),
	)

	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.NotNil(t, modelStub.got)
	system := skillLoadTestSystemContent(modelStub.got)
	require.Contains(t, system, "Doc A")
	require.Contains(t, system, "Doc B")
	docs, ok := inv.Session.GetState(skill.DocsKey("tester", "review"))
	require.True(t, ok)
	require.Equal(t, []byte("*"), docs)
}

func TestDeclaredSkillLoadPreservesExistingDocs(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {
			Summary: skill.Summary{Name: "review"},
			Body:    "Review body",
			Docs: []skill.Doc{{
				Path:    "old.md",
				Content: "Old doc body",
			}},
		},
	}}
	modelStub := &captureModel{}
	agt := New(
		"tester",
		WithModel(modelStub),
		WithSkills(repo),
		WithSkillLoadMode(SkillLoadModeSession),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			State: session.StateMap{
				skill.DocsKey("tester", "review"): []byte(`["old.md"]`),
			},
		}),
		agent.WithInvocationMessage(model.NewUserMessage("review")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{Name: "review"}},
		}),
	)

	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.NotNil(t, modelStub.got)
	require.Contains(
		t,
		skillLoadTestSystemContent(modelStub.got),
		"Old doc body",
	)
	docs, ok := inv.Session.GetState(skill.DocsKey("tester", "review"))
	require.True(t, ok)
	require.JSONEq(t, `["old.md"]`, string(docs))
}

func skillLoadTestSystemContent(req *model.Request) string {
	if req == nil {
		return ""
	}
	var system string
	for _, message := range req.Messages {
		if message.Role == model.RoleSystem {
			system += message.Content
		}
	}
	return system
}

func TestPrepareSkillLoadsActivatesToolsForFirstModelRequest(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {Summary: skill.Summary{Name: "review"}},
	}}
	agt := New(
		"tester",
		WithSkills(repo),
		WithActivatableToolSets([]tool.ToolSet{
			activationToolSet{
				name:  "review-tools",
				tools: []tool.Tool{activationTool{name: "inspect"}},
			},
		}),
		WithToolActivationOnSkillLoad(
			"review",
			[]string{"review-tools"},
		),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{Name: "review"}},
		}),
	)
	agt.setupInvocation(inv)

	err := agt.prepareSkillLoads(context.Background(), inv)
	require.NoError(t, err)
	records := invocationToolActivationRecords(inv)
	require.Len(t, records, 1)
	require.Equal(t, "review-tools", records[0].ToolSetName)
}

func TestSkillLoadsUseContextReturnedByBeforeAgent(t *testing.T) {
	type visibilityKey struct{}
	base := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {
			Summary: skill.Summary{Name: "review"},
			Body:    "VISIBLE BODY",
		},
	}}
	repo := skill.NewFilteredRepository(
		base,
		func(ctx context.Context, _ skill.Summary) bool {
			visible, _ := ctx.Value(visibilityKey{}).(bool)
			return visible
		},
	)
	callbacks := agent.NewCallbacks()
	callbacks.RegisterBeforeAgent(
		func(
			ctx context.Context,
			_ *agent.BeforeAgentArgs,
		) (*agent.BeforeAgentResult, error) {
			return &agent.BeforeAgentResult{
				Context: context.WithValue(ctx, visibilityKey{}, true),
			}, nil
		},
	)
	modelStub := &captureModel{}
	agt := New(
		"tester",
		WithModel(modelStub),
		WithSkills(repo),
		WithAgentCallbacks(callbacks),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{}),
		agent.WithInvocationMessage(model.NewUserMessage("review")),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{Name: "review"}},
		}),
	)

	events, err := agt.Run(context.Background(), inv)
	require.NoError(t, err)
	for range events {
	}

	require.NotNil(t, modelStub.got)
	var system string
	for _, message := range modelStub.got.Messages {
		if message.Role == model.RoleSystem {
			system += message.Content
		}
	}
	require.Contains(t, system, "VISIBLE BODY")
}
