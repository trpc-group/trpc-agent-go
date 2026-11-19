//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestNewSkillFilter_RequiresRepository(t *testing.T) {
	t.Parallel()

	_, err := NewSkillFilter("https://decision.example", nil)
	require.ErrorContains(t, err, "repository")

	_, err = NewSkillFilter("", &fakeSkillRepo{})
	require.Error(t, err)

	_, err = NewSkillFilter("https://decision.example", &fakeSkillRepo{}, WithThreshold(2))
	require.ErrorContains(t, err, "threshold")

	_, err = NewSkillFilter("https://decision.example", &fakeSkillRepo{}, WithThreshold(math.NaN()))
	require.ErrorContains(t, err, "threshold")

	_, err = NewSkillFilter("https://decision.example", &fakeSkillRepo{}, WithSlack(math.NaN()))
	require.ErrorContains(t, err, "slack")
}

func testSkillSummaries() []skill.Summary {
	return []skill.Summary{
		{Name: "travel-booking", Description: "find and book flights"},
		{Name: "expense-report", Description: "file expense reports"},
		{Name: "math-helper", Description: "arithmetic and algebra"},
		{Name: "cooking", Description: "recipes and menus"},
		{Name: "core-skill", Description: "core workflow"},
	}
}

func skillFilterEnv(
	t *testing.T,
	status int,
	overrides map[string]float64,
	opts ...Option,
) (*SkillFilter, *decisionRecorder, skill.VisibilityFilter, context.Context) {
	t.Helper()

	server, rec := decisionServer(t, binaryResponder(overrides, 0.05), status)
	baseOpts := append([]Option{
		WithHTTPClient(server.Client()),
		WithKeepSkills("core-skill"),
	}, opts...)
	f, err := NewSkillFilter(server.URL, &fakeSkillRepo{summaries: testSkillSummaries()}, baseOpts...)
	require.NoError(t, err)

	inv := agent.NewInvocation(agent.WithInvocationMessage(
		model.NewUserMessage("book a flight to paris"),
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	return f, rec, f.VisibilityFilter(), ctx
}

func TestVisibility_NarrowsSkills(t *testing.T) {
	t.Parallel()

	_, rec, filter, ctx := skillFilterEnv(t, http.StatusOK, map[string]float64{
		"skill:travel-booking": 0.9,
		"skill:expense-report": 0.5, // below threshold, within slack
	})

	visible := map[string]bool{}
	for _, s := range testSkillSummaries() {
		visible[s.Name] = filter(ctx, s)
	}
	require.Equal(t, map[string]bool{
		"travel-booking": true,  // approved
		"expense-report": false, // threshold met by another skill
		"math-helper":    false,
		"cooking":        false,
		"core-skill":     true, // WithKeepSkills
	}, visible)
	require.Equal(t, 1, rec.requests()) // one batch decision for the run
}

func TestVisibility_NormalizesSnapshotNames(t *testing.T) {
	server, rec := decisionServer(t, binaryResponder(map[string]float64{"skill:approved": 0.9}, 0.05), http.StatusOK)
	repo := &fakeSkillRepo{summaries: []skill.Summary{
		{Name: " core-skill "},
		{Name: " approved "},
		{Name: " dropped "},
	}}
	f, err := NewSkillFilter(server.URL, repo, WithHTTPClient(server.Client()), WithKeepSkills("core-skill"))
	require.NoError(t, err)
	ctx := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test"))))
	filter := f.VisibilityFilter()
	require.True(t, filter(ctx, repo.summaries[0]))
	require.True(t, filter(ctx, repo.summaries[1]))
	require.False(t, filter(ctx, repo.summaries[2]))
	require.True(t, filter(ctx, skill.Summary{Name: " new-skill "}))
	require.Equal(t, 1, rec.requests())
}

func TestVisibility_SpacedNamePreservesDescriptionAndTokenReport(t *testing.T) {
	server, rec := decisionServer(t, binaryResponder(map[string]float64{"skill:kept": 0.9}, 0.05), http.StatusOK)
	summaries := []skill.Summary{
		{Name: "kept", Description: "keep this skill"},
		{Name: " dropped ", Description: "a long description for the dropped skill"},
	}
	var report *Report
	f, err := NewSkillFilter(server.URL, &fakeSkillRepo{summaries: summaries},
		WithHTTPClient(server.Client()), WithObserver(func(r *Report) { report = r }))
	require.NoError(t, err)
	ctx := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test"))))
	require.False(t, f.VisibilityFilter()(ctx, summaries[1]))
	state := rec.lastState().(map[string]any)
	catalog := state["skill_catalog"].([]any)
	require.Contains(t, catalog, map[string]any{"name": "dropped", "description": "a long description for the dropped skill"})
	require.Equal(t, 4, report.CatalogTokensAfter)
	require.Equal(t, 12, report.SavedTokens)
}

func TestVisibility_SkillsAddedAfterDecision(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name string) {
		t.Helper()
		dir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: test skill\n---\nInstructions\n"), 0o644))
	}
	writeSkill("kept")
	writeSkill("dropped")
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)
	server, rec := decisionServer(t, binaryResponder(map[string]float64{"skill:kept": 0.9}, 0.05), http.StatusOK)
	f, err := NewSkillFilter(server.URL, repo, WithHTTPClient(server.Client()))
	require.NoError(t, err)
	filtered := skill.NewFilteredRepository(repo, f.VisibilityFilter())
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("test")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	_, err = skill.GetForContext(ctx, filtered, "kept")
	require.NoError(t, err)
	_, err = skill.GetForContext(ctx, filtered, "dropped")
	require.Error(t, err)

	writeSkill("new-skill")
	require.NoError(t, repo.Refresh())
	_, err = skill.GetForContext(ctx, filtered, "new-skill")
	require.NoError(t, err)
	_, err = skill.GetForContext(ctx, filtered, "dropped")
	require.Error(t, err)
	require.Equal(t, 1, rec.requests())

	next := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("test"))))
	_, err = skill.GetForContext(next, filtered, "new-skill")
	require.Error(t, err)
	require.Equal(t, 2, rec.requests())
}

func TestVisibility_ShortlistsFromUserHistory(t *testing.T) {
	t.Parallel()
	_, rec, filter, ctx := skillFilterEnv(t, http.StatusOK, map[string]float64{
		"skill:travel-booking": 0.9,
	}, WithShortlistLimit(1))
	inv, _ := agent.InvocationFromContext(ctx)
	inv.Session = &session.Session{Events: []event.Event{conversationEvent(model.RoleUser, "book a flight to paris", "")}}
	inv.Message = model.NewUserMessage("continue")
	require.True(t, filter(ctx, skill.Summary{Name: "travel-booking"}))
	require.Contains(t, rec.lastQuestions(), "skill:travel-booking")
	require.Len(t, rec.lastQuestions(), 1)
}

func TestVisibility_NoLexicalMatchKeepsSkills(t *testing.T) {
	t.Parallel()
	_, rec, filter, ctx := skillFilterEnv(t, http.StatusOK, nil, WithShortlistLimit(1))
	inv, _ := agent.InvocationFromContext(ctx)
	inv.Message = model.NewUserMessage("xyzzy")
	require.True(t, filter(ctx, skill.Summary{Name: "travel-booking"}))
	require.Equal(t, 0, rec.requests())
}

func TestVisibility_SlackFallbackWithKeepSkills(t *testing.T) {
	t.Parallel()
	_, _, filter, ctx := skillFilterEnv(t, http.StatusOK, map[string]float64{
		"skill:expense-report": 0.5,
	})
	require.True(t, filter(ctx, skill.Summary{Name: "core-skill"}))
	require.True(t, filter(ctx, skill.Summary{Name: "expense-report"}))
	require.False(t, filter(ctx, skill.Summary{Name: "math-helper"}))
}

func TestVisibility_SlackFallbackWithUnaskedSkills(t *testing.T) {
	t.Parallel()
	_, _, filter, ctx := skillFilterEnv(t, http.StatusOK, map[string]float64{
		"skill:cooking": 0.5,
	}, WithMaxQuestions(1))
	require.True(t, filter(ctx, skill.Summary{Name: "cooking"}))
	require.True(t, filter(ctx, skill.Summary{Name: "expense-report"}))
}

func TestVisibility_CachesPerInvocation(t *testing.T) {
	t.Parallel()

	_, rec, filter, ctx := skillFilterEnv(t, http.StatusOK, map[string]float64{
		"skill:travel-booking": 0.9,
	})

	// The framework asks per skill; only the first call decides.
	for i := 0; i < len(testSkillSummaries()); i++ {
		filter(ctx, skill.Summary{Name: "travel-booking"})
	}
	require.Equal(t, 1, rec.requests())

	// A new invocation reuses the same user message but is a new run.
	inv := agent.NewInvocation(agent.WithInvocationMessage(
		model.NewUserMessage("book a flight to paris"),
	))
	newCtx := agent.NewInvocationContext(context.Background(), inv)
	filter(newCtx, skill.Summary{Name: "travel-booking"})
	require.Equal(t, 2, rec.requests())
}

func TestVisibility_FailOpenOnError(t *testing.T) {
	t.Parallel()

	_, rec, filter, ctx := skillFilterEnv(t, http.StatusInternalServerError, nil)

	for _, s := range testSkillSummaries() {
		require.True(t, filter(ctx, s))
	}
	require.Equal(t, 1, rec.requests())

	// The fail-open result is cached for the rest of the run.
	filter(ctx, skill.Summary{Name: "travel-booking"})
	require.Equal(t, 1, rec.requests())
}

func TestVisibility_NoInvocationFailsOpen(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	f, err := NewSkillFilter(server.URL,
		&fakeSkillRepo{summaries: testSkillSummaries()},
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)
	filter := f.VisibilityFilter()

	require.True(t, filter(context.Background(), skill.Summary{Name: "math-helper"}))
	require.True(t, filter(context.Background(), skill.Summary{Name: "cooking"}))
	require.Equal(t, 0, rec.requests())
}

func TestVisibility_BlankMessageFailsOpen(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(nil, 0.05), http.StatusOK)
	f, err := NewSkillFilter(server.URL,
		&fakeSkillRepo{summaries: testSkillSummaries()},
		WithHTTPClient(server.Client()),
	)
	require.NoError(t, err)
	filter := f.VisibilityFilter()

	inv := agent.NewInvocation() // no message
	ctx := agent.NewInvocationContext(context.Background(), inv)
	require.True(t, filter(ctx, skill.Summary{Name: "math-helper"}))
	require.Equal(t, 0, rec.requests())
}

func TestVisibility_ConcurrentFirstCallSendsOneDecision(t *testing.T) {
	t.Parallel()

	_, rec, filter, ctx := skillFilterEnv(t, http.StatusOK, map[string]float64{
		"skill:travel-booking": 0.9,
	})

	summaries := testSkillSummaries()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			filter(ctx, summaries[i%len(summaries)])
		}(i)
	}
	wg.Wait()
	require.Equal(t, 1, rec.requests())
}

func TestVisibility_CanceledWaiterDoesNotCancelDecision(t *testing.T) {
	started, release := make(chan struct{}, 8), make(chan struct{}, 1)
	defer close(release)
	server, rec := decisionServer(t, func(q map[string]string) string {
		started <- struct{}{}
		<-release
		return binaryResponder(nil, 0.05)(q)
	}, http.StatusOK)
	f, err := NewSkillFilter(server.URL, &fakeSkillRepo{summaries: testSkillSummaries()}, WithHTTPClient(server.Client()))
	require.NoError(t, err)
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("book a flight")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	filter := f.VisibilityFilter()
	done := make(chan bool, 1)
	go func() { done <- filter(ctx, skill.Summary{Name: "math-helper"}) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("decision did not start")
	}
	waitCtx, cancel := context.WithCancel(ctx)
	waiter := make(chan bool, 1)
	go func() { waiter <- filter(waitCtx, skill.Summary{Name: "math-helper"}) }()
	select {
	case <-started:
		t.Fatal("waiter started a duplicate decision")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case visible := <-waiter:
		require.True(t, visible)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter did not return")
	}
	release <- struct{}{}
	select {
	case visible := <-done:
		require.False(t, visible)
	case <-time.After(2 * time.Second):
		t.Fatal("decision did not finish")
	}
	require.False(t, filter(ctx, skill.Summary{Name: "math-helper"}))
	require.Equal(t, 1, rec.requests())
}

func TestVisibility_ReportsOutcome(t *testing.T) {
	t.Parallel()

	server, rec := decisionServer(t, binaryResponder(map[string]float64{
		"skill:travel-booking": 0.9,
	}, 0.05), http.StatusOK)

	var reports []*Report
	f, err := NewSkillFilter(server.URL,
		&fakeSkillRepo{summaries: testSkillSummaries()},
		WithHTTPClient(server.Client()),
		WithKeepSkills("core-skill"),
		WithObserver(func(r *Report) { reports = append(reports, r) }),
	)
	require.NoError(t, err)

	inv := agent.NewInvocation(agent.WithInvocationMessage(
		model.NewUserMessage("book a flight to paris"),
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	filter := f.VisibilityFilter()
	for _, s := range testSkillSummaries() {
		filter(ctx, s)
	}

	require.Equal(t, 1, rec.requests())
	require.Len(t, reports, 1)
	report := reports[0]
	require.Equal(t, CatalogSkill, report.Catalog)
	require.Equal(t, OutcomeNarrowed, report.Outcome)
	require.Equal(t, inv.InvocationID, report.TurnKey)
	require.Contains(t, report.Dropped, "math-helper")
	require.NotContains(t, report.Dropped, "core-skill")
	require.InDelta(t, 0.9, report.Scores["travel-booking"], 1e-9)
	require.Greater(t, report.SavedTokens, 0)
}

func TestSharedObserverDistinguishesCatalogs(t *testing.T) {
	server, _ := decisionServer(t, binaryResponder(nil, 0.9), http.StatusOK)
	var catalogs []string
	observer := WithObserver(func(report *Report) { catalogs = append(catalogs, report.Catalog) })
	cb, err := NewToolCallback(server.URL, WithHTTPClient(server.Client()), WithMinToolCount(0), observer)
	require.NoError(t, err)
	f, err := NewSkillFilter(server.URL, &fakeSkillRepo{summaries: []skill.Summary{{Name: "helper"}}}, WithHTTPClient(server.Client()), observer)
	require.NoError(t, err)
	inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("use helper")))
	ctx := agent.NewInvocationContext(context.Background(), inv)
	req := newUserRequest("use helper", newToolMap(&stubTool{name: "helper"}))
	_, err = cb.Callback()(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.True(t, f.VisibilityFilter()(ctx, skill.Summary{Name: "helper"}))
	require.Equal(t, []string{CatalogTool, CatalogSkill}, catalogs)
}

func TestVisibility_ObserverCanReenterSameInvocation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		summaries []skill.Summary
		status    int
		opts      []Option
		outcome   string
		visible   bool
	}{
		{name: "narrowed", summaries: testSkillSummaries(), status: http.StatusOK, outcome: OutcomeNarrowed},
		{name: "no scoreable skills", summaries: []skill.Summary{{Name: "core"}}, status: http.StatusOK, opts: []Option{WithKeepSkills("core")}, outcome: OutcomeUntouched, visible: true},
		{name: "no lexical overlap", summaries: []skill.Summary{{Name: "alpha"}, {Name: "beta"}}, status: http.StatusOK, opts: []Option{WithShortlistLimit(1)}, outcome: OutcomeUntouched, visible: true},
		{name: "decision failure", summaries: testSkillSummaries(), status: http.StatusInternalServerError, outcome: OutcomeFailOpen, visible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, rec := decisionServer(t, binaryResponder(map[string]float64{"skill:travel-booking": 0.9}, 0.05), tc.status)
			inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("book a flight")))
			ctx := agent.NewInvocationContext(context.Background(), inv)
			var f *SkillFilter
			var reports int
			var reentered, timedOut bool
			var outcome string
			opts := append([]Option{WithHTTPClient(server.Client()), WithObserver(func(report *Report) {
				reports++
				outcome = report.Outcome
				waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
				defer cancel()
				reentered = f.VisibilityFilter()(waitCtx, skill.Summary{Name: "math-helper"})
				timedOut = waitCtx.Err() != nil
			})}, tc.opts...)
			var err error
			f, err = NewSkillFilter(server.URL, &fakeSkillRepo{summaries: tc.summaries}, opts...)
			require.NoError(t, err)
			visible := f.VisibilityFilter()(ctx, skill.Summary{Name: "math-helper"})
			require.False(t, timedOut, "observer must not wait on its own decision")
			require.Equal(t, tc.visible, reentered)
			require.Equal(t, tc.visible, visible)
			require.Equal(t, tc.outcome, outcome)
			require.Equal(t, 1, reports)
			if tc.outcome == OutcomeUntouched {
				require.Zero(t, rec.requests())
			} else {
				require.Equal(t, 1, rec.requests())
			}
		})
	}
}

func TestVisibility_AllCandidatesKeptReportsUntouched(t *testing.T) {
	for _, tc := range []struct {
		name  string
		score float64
		opts  []Option
	}{
		{name: "threshold", score: 0.9},
		{name: "slack", score: 0.5},
		{name: "unasked and always kept", score: 0.9, opts: []Option{WithMaxQuestions(1), WithKeepSkills("fixed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, rec := decisionServer(t, binaryResponder(nil, tc.score), http.StatusOK)
			summaries := []skill.Summary{{Name: "alpha"}, {Name: "beta"}, {Name: "fixed"}}
			var reports []*Report
			opts := append([]Option{WithHTTPClient(server.Client()), WithObserver(func(r *Report) { reports = append(reports, r) })}, tc.opts...)
			f, err := NewSkillFilter(server.URL, &fakeSkillRepo{summaries: summaries}, opts...)
			require.NoError(t, err)
			inv := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("find skills")))
			ctx := agent.NewInvocationContext(context.Background(), inv)
			for _, s := range summaries {
				require.True(t, f.VisibilityFilter()(ctx, s))
			}
			require.Equal(t, 1, rec.requests())
			require.Len(t, reports, 1)
			require.Equal(t, CatalogSkill, reports[0].Catalog)
			require.Equal(t, OutcomeUntouched, reports[0].Outcome)
			require.Empty(t, reports[0].Dropped)
			require.Zero(t, reports[0].SavedTokens)
			cached, ok := f.cache.get(inv.InvocationID)
			require.True(t, ok)
			require.Equal(t, OutcomeUntouched, cached.outcome)
		})
	}
}
