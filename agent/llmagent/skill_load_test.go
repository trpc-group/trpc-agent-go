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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type skillLoadTestRepository struct {
	skills    map[string]*skill.Skill
	summaries []skill.Summary
	err       error
	getCalls  *atomic.Int32
	getFunc   func(string, int32) (*skill.Skill, error)
}

func (r *skillLoadTestRepository) Summaries() []skill.Summary {
	if r.summaries != nil {
		return append([]skill.Summary(nil), r.summaries...)
	}
	var out []skill.Summary
	for _, resolved := range r.skills {
		out = append(out, resolved.Summary)
	}
	return out
}

func (r *skillLoadTestRepository) Get(name string) (*skill.Skill, error) {
	var call int32
	if r.getCalls != nil {
		call = r.getCalls.Add(1)
	}
	if r.getFunc != nil {
		return r.getFunc(name, call)
	}
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
	require.Empty(
		t,
		inv.Session.SnapshotState(),
		"preflight must not partially commit state",
	)
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

func TestPrepareSkillLoadsRejectsInvalidRequests(t *testing.T) {
	reviewSkill := &skill.Skill{
		Summary: skill.Summary{Name: "review"},
		Docs: []skill.Doc{
			{Path: "guide.md"},
			{Path: "other.md"},
		},
	}
	repo := &skillLoadTestRepository{
		skills: map[string]*skill.Skill{
			"review": reviewSkill,
			"other":  {Summary: skill.Summary{Name: "other"}},
		},
	}
	nilSkillRepo := &skillLoadTestRepository{
		skills:    map[string]*skill.Skill{"review": nil},
		summaries: []skill.Summary{{Name: "review"}},
	}
	tests := []struct {
		name    string
		repo    *skillLoadTestRepository
		session *session.Session
		loads   []skill.LoadRequest
		options []Option
		wantErr error
	}{
		{
			name:    "missing session",
			repo:    repo,
			loads:   []skill.LoadRequest{{Name: "review"}},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "exceeds maximum",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{
				{Name: "review"},
				{Name: "other"},
			},
			options: []Option{WithMaxLoadedSkills(1)},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "conflicting skill declarations",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{
				{Name: "review", Docs: []string{"guide.md"}},
				{Name: " review ", Docs: []string{"other.md"}},
			},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "conflicting include all declarations",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{
				{Name: "review", IncludeAllDocs: true},
				{Name: "review"},
			},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "empty skill name",
			repo:    repo,
			session: &session.Session{},
			loads:   []skill.LoadRequest{{Name: " "}},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "nil resolved skill",
			repo:    nilSkillRepo,
			session: &session.Session{},
			loads:   []skill.LoadRequest{{Name: "review"}},
			wantErr: skill.ErrSkillUnavailable,
		},
		{
			name:    "empty document",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{{
				Name: "review",
				Docs: []string{" "},
			}},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "skill file document",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{{
				Name: "review",
				Docs: []string{skill.SkillFile},
			}},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "duplicate document",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{{
				Name: "review",
				Docs: []string{"guide.md", " guide.md "},
			}},
			wantErr: skill.ErrInvalidLoadRequest,
		},
		{
			name:    "unavailable document",
			repo:    repo,
			session: &session.Session{},
			loads: []skill.LoadRequest{{
				Name: "review",
				Docs: []string{"missing.md"},
			}},
			wantErr: skill.ErrSkillUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := []Option{WithSkills(tt.repo)}
			options = append(options, tt.options...)
			agt := New("tester", options...)
			original := make([]skill.LoadRequest, len(tt.loads))
			for i, load := range tt.loads {
				original[i] = load
				original[i].Docs = append([]string(nil), load.Docs...)
			}
			inv := agent.NewInvocation(
				agent.WithInvocationSession(tt.session),
				agent.WithInvocationRunOptions(agent.RunOptions{
					SkillLoads: tt.loads,
				}),
			)
			agt.setupInvocation(inv)

			err := agt.prepareSkillLoads(context.Background(), inv)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, original, inv.RunOptions.SkillLoads)
			if inv.Session != nil {
				require.Empty(t, inv.Session.SnapshotState())
			}
		})
	}
}

func TestPrepareSkillLoadsCoalescesEquivalentDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		loads []skill.LoadRequest
		want  skill.LoadRequest
	}{
		{
			name: "same document set",
			loads: []skill.LoadRequest{
				{
					Name: " review ",
					Docs: []string{" guide.md ", "security.md"},
				},
				{
					Name: "review",
					Docs: []string{" security.md ", "guide.md"},
				},
			},
			want: skill.LoadRequest{
				Name: "review",
				Docs: []string{"guide.md", "security.md"},
			},
		},
		{
			name: "include all documents",
			loads: []skill.LoadRequest{
				{Name: " review ", IncludeAllDocs: true},
				{Name: "review", IncludeAllDocs: true},
			},
			want: skill.LoadRequest{
				Name:           "review",
				Docs:           []string{},
				IncludeAllDocs: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getCalls atomic.Int32
			repo := &skillLoadTestRepository{
				getCalls: &getCalls,
				skills: map[string]*skill.Skill{
					"review": {
						Summary: skill.Summary{Name: "review"},
						Docs: []skill.Doc{
							{Path: "guide.md"},
							{Path: "security.md"},
						},
					},
				},
			}
			agt := New(
				"tester",
				WithSkills(repo),
				WithMaxLoadedSkills(1),
			)
			runOptions := agent.NewRunOptions(
				agent.WithSkillLoads(tt.loads[0]),
				agent.WithSkillLoads(tt.loads[1]),
			)
			inv := agent.NewInvocation(
				agent.WithInvocationSession(&session.Session{}),
				agent.WithInvocationRunOptions(runOptions),
			)
			agt.setupInvocation(inv)

			err := agt.prepareSkillLoads(context.Background(), inv)
			require.NoError(t, err)
			require.Equal(t, []skill.LoadRequest{tt.want}, inv.RunOptions.SkillLoads)
			require.Empty(t, inv.Session.SnapshotState())
			require.Equal(t, int32(1), getCalls.Load())
		})
	}
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
			Docs: []skill.Doc{
				{
					Path:    "old.md",
					Content: "Old doc body",
				},
				{
					Path:    "other.md",
					Content: "Other doc body",
				},
			},
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
	system := skillLoadTestSystemContent(modelStub.got)
	require.Contains(t, system, "Old doc body")
	require.NotContains(t, system, "Other doc body")
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

func TestDeclaredSkillLoadActivatesToolsForFirstModelRequest(t *testing.T) {
	repo := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {Summary: skill.Summary{Name: "review"}},
	}}
	modelStub := &captureModel{}
	agt := New(
		"tester",
		WithModel(modelStub),
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
	require.Contains(t, modelStub.got.Tools, "review-tools_inspect")
	records := invocationToolActivationRecords(inv)
	require.Len(t, records, 1)
	require.Equal(t, "review-tools", records[0].ToolSetName)
}

func TestPreparedSkillRepositoryCopiesValidatedContent(t *testing.T) {
	resolved := &skill.Skill{
		Summary: skill.Summary{Name: "review"},
		Body:    "validated body",
		Docs: []skill.Doc{{
			Path:    "guide.md",
			Content: "validated guide",
		}},
	}
	base := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": resolved,
	}}
	repo := newPreparedSkillRepository(
		base,
		map[string]*skill.Skill{"review": resolved},
	)

	resolved.Body = "changed body"
	resolved.Docs[0].Content = "changed guide"
	first, err := repo.Get("review")
	require.NoError(t, err)
	require.Equal(t, "validated body", first.Body)
	require.Equal(t, "validated guide", first.Docs[0].Content)

	first.Body = "consumer mutation"
	first.Docs[0].Content = "consumer mutation"
	second, err := repo.Get("review")
	require.NoError(t, err)
	require.Equal(t, "validated body", second.Body)
	require.Equal(t, "validated guide", second.Docs[0].Content)
}

func TestDeclaredSkillLoadsReusePreparedRepository(t *testing.T) {
	tests := []struct {
		name    string
		options []Option
	}{
		{name: "system prompt mode"},
		{
			name: "tool result mode",
			options: []Option{
				WithSkillsLoadedContentInToolResults(true),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var providerCalls atomic.Int32
			var getCalls atomic.Int32
			repo := &skillLoadTestRepository{
				summaries: []skill.Summary{{Name: "review"}},
				getCalls:  &getCalls,
				getFunc: func(_ string, call int32) (*skill.Skill, error) {
					if call == 1 {
						return &skill.Skill{
							Summary: skill.Summary{Name: "review"},
							Body:    "Pinned review body",
							Docs: []skill.Doc{{
								Path:    "guide.md",
								Content: "Pinned guide body",
							}},
						}, nil
					}
					return nil, errors.New("transient skill read failure")
				},
			}
			provider := skill.RepositoryProviderFunc(
				func(context.Context, skill.SkillScope) (skill.Repository, error) {
					if providerCalls.Add(1) == 1 {
						return repo, nil
					}
					return nil, errors.New("transient repository failure")
				},
			)
			modelStub := &captureModel{}
			options := []Option{
				WithModel(modelStub),
				WithSkillRepositoryProvider(provider),
				WithSkillScopeMode(skill.SkillScopeApp),
			}
			options = append(options, tt.options...)
			agt := New("tester", options...)
			inv := agent.NewInvocation(
				agent.WithInvocationSession(&session.Session{AppName: "app"}),
				agent.WithInvocationMessage(model.NewUserMessage("review")),
				agent.WithInvocationRunOptions(agent.RunOptions{
					SkillLoads: []skill.LoadRequest{{
						Name: "review",
						Docs: []string{"guide.md"},
					}},
				}),
			)

			events, err := agt.Run(context.Background(), inv)
			require.NoError(t, err)
			for range events {
			}

			require.Equal(t, int32(1), providerCalls.Load())
			require.Equal(t, int32(1), getCalls.Load())
			require.NotNil(t, modelStub.got)
			system := skillLoadTestSystemContent(modelStub.got)
			require.Contains(t, system, "Pinned review body")
			require.Contains(t, system, "Pinned guide body")
			require.Contains(t, modelStub.got.Tools, "skill_load")
		})
	}
}

func TestPreparedSkillRepositoryDoesNotLeakToClone(t *testing.T) {
	repoA := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"review": {Summary: skill.Summary{Name: "review"}},
	}}
	repoB := &skillLoadTestRepository{skills: map[string]*skill.Skill{
		"other": {Summary: skill.Summary{Name: "other"}},
	}}
	var providerCalls atomic.Int32
	provider := skill.RepositoryProviderFunc(
		func(context.Context, skill.SkillScope) (skill.Repository, error) {
			if providerCalls.Add(1) == 1 {
				return repoA, nil
			}
			return repoB, nil
		},
	)
	agt := New(
		"tester",
		WithSkillRepositoryProvider(provider),
		WithSkillScopeMode(skill.SkillScopeApp),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{AppName: "app"}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			SkillLoads: []skill.LoadRequest{{Name: "review"}},
		}),
	)
	agt.setupInvocation(inv)

	require.NoError(t, agt.prepareSkillLoads(context.Background(), inv))
	preparedRepo := agt.skillRepositoryForInvocation(context.Background(), inv)
	require.NotSame(t, repoA, preparedRepo)
	resolved, err := skill.GetForContext(
		context.Background(),
		preparedRepo,
		"review",
	)
	require.NoError(t, err)
	require.Equal(t, "review", resolved.Summary.Name)
	require.Equal(t, int32(1), providerCalls.Load())
	require.Same(
		t,
		repoA,
		agt.InvocationSkillRepository(context.Background(), inv),
	)

	view := inv.View()
	require.Same(
		t,
		preparedRepo,
		agt.skillRepositoryForInvocation(context.Background(), view),
	)
	require.Equal(t, int32(1), providerCalls.Load())

	child := inv.Clone()
	require.Empty(t, child.RunOptions.SkillLoads)
	require.Same(
		t,
		repoB,
		agt.skillRepositoryForInvocation(context.Background(), child),
	)
	require.Equal(t, int32(2), providerCalls.Load())
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
