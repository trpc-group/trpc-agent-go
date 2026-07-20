//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// --- mocks ---

type mockReviewer struct {
	mu       sync.Mutex
	decision *ReviewDecision
	err      error
	calls    int
}

func (m *mockReviewer) Review(_ context.Context, _ *ReviewInput) (*ReviewDecision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return m.decision, m.err
}

type mockPublisher struct {
	mu        sync.Mutex
	skills    []*SkillSpec
	deletions []string
	err       error
	deleteErr error
}

func (m *mockPublisher) UpsertSkill(_ context.Context, spec *SkillSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.skills = append(m.skills, spec)
	return nil
}

func (m *mockPublisher) DeleteSkill(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletions = append(m.deletions, name)
	return nil
}

type mockSkillRepo struct {
	summaries []skill.Summary
	bodies    map[string]string // name -> body; missing names → Get returns error
	paths     map[string]string // name -> path; missing names → Path returns error
	refreshed int
	mu        sync.Mutex
}

func (m *mockSkillRepo) Summaries() []skill.Summary { return m.summaries }
func (m *mockSkillRepo) Get(name string) (*skill.Skill, error) {
	if body, ok := m.bodies[name]; ok {
		return &skill.Skill{
			Summary: skill.Summary{Name: name},
			Body:    body,
		}, nil
	}
	return nil, fmt.Errorf("skill %q not found", name)
}
func (m *mockSkillRepo) Path(name string) (string, error) {
	if m.paths != nil {
		if p, ok := m.paths[name]; ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("skill %q path not found", name)
}
func (m *mockSkillRepo) Refresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshed++
	return nil
}

type capturingReviewPolicy struct {
	mu    sync.Mutex
	input *ReviewPolicyInput
	allow bool
	err   error
}

func (p *capturingReviewPolicy) ShouldReview(
	_ context.Context,
	input *ReviewPolicyInput,
) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.input = input
	return p.allow, p.err
}

func (p *capturingReviewPolicy) snapshot() *ReviewPolicyInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.input
}

// --- helpers ---

func newTestSession() *session.Session {
	return session.NewSession("test-app", "user-1", "sess-1")
}

func addEvents(sess *session.Session, msgs ...model.Message) {
	now := time.Now()
	for i, msg := range msgs {
		sess.Events = append(sess.Events, event.Event{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Response:  &model.Response{Choices: []model.Choice{{Message: msg}}},
		})
	}
}

// --- tests ---

func TestWorker_ProcessJob_NoMessages(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := newWorker(workerConfig{Reviewer: rev})

	sess := newTestSession()
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should not be called when no messages")
	rev.mu.Unlock()
}

func TestWorker_ProcessJob_ReviewPolicyRejects(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := newWorker(workerConfig{Reviewer: rev})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hi"},
		model.Message{Role: model.RoleAssistant, Content: "hello"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should not be called when review policy rejects")
	rev.mu.Unlock()

	raw, ok := sess.GetState(sessionStateKeyLastReviewAt)
	assert.True(t, ok, "last_review_at should be written even when skipped")
	assert.NotEmpty(t, raw)
}

func TestWorker_ProcessJob_ReviewPolicyReceivesContext(t *testing.T) {
	policy := &capturingReviewPolicy{}
	w := newWorker(workerConfig{
		Reviewer:       &mockReviewer{decision: &ReviewDecision{}},
		ReviewPolicy:   policy,
		SkillScopeMode: skill.SkillScopeUser,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hi"},
		model.Message{Role: model.RoleAssistant, Content: "checking", ToolCalls: []model.ToolCall{{
			ID: "call-1",
			Function: model.FunctionDefinitionParam{
				Name: "lookup",
			},
		}}},
		model.Message{Role: model.RoleTool, ToolID: "call-1", Content: "ok"},
	)
	outcome := &Outcome{Status: OutcomeSuccess}

	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{Session: sess, Outcome: outcome},
	})

	input := policy.snapshot()
	require.NotNil(t, input)
	require.NotNil(t, input.ReviewContext)
	assert.Equal(t, sess.AppName, input.AppName)
	assert.Equal(t, sess.UserID, input.UserID)
	assert.Equal(t, sess.ID, input.SessionID)
	assert.Equal(t, skill.SkillScope{AppName: sess.AppName, UserID: sess.UserID}, input.Scope)
	assert.True(t, input.Scoped)
	assert.Same(t, outcome, input.Outcome)
	assert.Equal(t, 1, input.ReviewContext.ToolCallCount)
	assert.Len(t, input.ReviewContext.Messages, 2)
	assert.NotEmpty(t, input.ReviewContext.Transcript)
}

func TestWorker_ProcessJob_ReviewPolicyErrorDoesNotAdvanceCursor(t *testing.T) {
	policy := &capturingReviewPolicy{err: assert.AnError}
	w := newWorker(workerConfig{
		Reviewer:     &mockReviewer{decision: &ReviewDecision{}},
		ReviewPolicy: policy,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hi"},
		model.Message{Role: model.RoleAssistant, Content: "hello"},
	)

	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	_, ok := sess.GetState(sessionStateKeyLastReviewAt)
	assert.False(t, ok, "last_review_at should not be written when policy fails")
}

func TestWorker_ProcessJob_SkillWrittenAndRefreshed(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{
				{Name: "Test Skill", Steps: []string{"do stuff"}},
			},
		},
	}

	// Use an alwaysReviewPolicy to bypass the threshold.
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "help me"},
		model.Message{Role: model.RoleAssistant, Content: "sure"},
	)

	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1)
	assert.Equal(t, "Test Skill", pub.skills[0].Name)
	pub.mu.Unlock()

	repo.mu.Lock()
	assert.Equal(t, 1, repo.refreshed, "repo should be refreshed after writing skill")
	repo.mu.Unlock()
}

func TestWorker_ProcessJob_AppScopedFileBackends(t *testing.T) {
	root := t.TempDir()
	managedDir := filepath.Join(root, "managed")
	revisionsDir := filepath.Join(root, "revisions")
	provider := skill.RepositoryProviderFunc(func(_ context.Context, scope skill.SkillScope) (skill.Repository, error) {
		scopedDir, err := scopedTestDir(managedDir, skill.SkillScopeApp, scope)
		require.NoError(t, err)
		return skill.NewFSRepository(scopedDir)
	})
	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Weather Snapshot",
			Description: "Collects weather data.",
			WhenToUse:   "When weather data is needed.",
			Steps:       []string{"Fetch data.", "Summarize the result."},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:          rev,
		Publisher:         newFilePublisher(managedDir),
		PublisherBaseDir:  managedDir,
		ReviewPolicy:      alwaysReviewPolicy{},
		SkillScopeMode:    skill.SkillScopeApp,
		SkillRepoProvider: provider,
		CandidateStore:    NewFileCandidateStore(revisionsDir),
		ActivePointer:     NewFileActivePointer(revisionsDir),
		SpecGate:          NewDefaultSpecGate(),
		SafetyGate:        NewDefaultSafetyGate(),
	})
	sess := newTestSession()
	addEvents(sess, model.NewUserMessage("learn this workflow"), model.NewAssistantMessage("done"))

	require.NoError(t, w.Enqueue(context.Background(), LearningJob{Session: sess}))

	scopedManaged := filepath.Join(managedDir, "apps", sess.AppName)
	assert.FileExists(t, filepath.Join(scopedManaged, "weather-snapshot", "SKILL.md"))
	scopedRevisions := filepath.Join(revisionsDir, "apps", sess.AppName)
	entries, err := os.ReadDir(filepath.Join(scopedRevisions, "weather-snapshot", "revisions"))
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}

func scopedTestDir(root string, mode skill.SkillScopeMode, scope skill.SkillScope) (string, error) {
	parts, err := skill.ScopePathParts(mode, scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

func TestWorker_ResolveJobScope(t *testing.T) {
	t.Run("unscoped service with zero job scope", func(t *testing.T) {
		w := newWorker(workerConfig{Reviewer: &mockReviewer{}})
		scope, scoped, err := w.resolveJobScope(LearningJob{Session: newTestSession()})
		require.NoError(t, err)
		require.False(t, scoped)
		require.True(t, scope.IsZero())
	})
	t.Run("app mode derives scope from session", func(t *testing.T) {
		w := newWorker(workerConfig{
			Reviewer:       &mockReviewer{},
			SkillScopeMode: skill.SkillScopeApp,
		})
		scope, scoped, err := w.resolveJobScope(LearningJob{Session: newTestSession()})
		require.NoError(t, err)
		require.True(t, scoped)
		require.Equal(t, skill.SkillScope{AppName: "test-app"}, scope)
	})
	t.Run("user mode derives app+user from session", func(t *testing.T) {
		w := newWorker(workerConfig{
			Reviewer:       &mockReviewer{},
			SkillScopeMode: skill.SkillScopeUser,
		})
		scope, scoped, err := w.resolveJobScope(LearningJob{Session: newTestSession()})
		require.NoError(t, err)
		require.True(t, scoped)
		require.Equal(t, skill.SkillScope{AppName: "test-app", UserID: "user-1"}, scope)
	})
	t.Run("explicit job scope wins over session", func(t *testing.T) {
		w := newWorker(workerConfig{
			Reviewer:       &mockReviewer{},
			SkillScopeMode: skill.SkillScopeApp,
		})
		scope, scoped, err := w.resolveJobScope(LearningJob{
			Session: newTestSession(),
			Scope:   skill.SkillScope{AppName: "other-app"},
		})
		require.NoError(t, err)
		require.True(t, scoped)
		require.Equal(t, skill.SkillScope{AppName: "other-app"}, scope)
	})
	t.Run("none mode auto-detects user scope from explicit job UserID", func(t *testing.T) {
		w := newWorker(workerConfig{Reviewer: &mockReviewer{}})
		scope, scoped, err := w.resolveJobScope(LearningJob{
			Scope: skill.SkillScope{AppName: "app", UserID: "u-9"},
		})
		require.NoError(t, err)
		require.True(t, scoped)
		require.Equal(t, skill.SkillScope{AppName: "app", UserID: "u-9"}, scope)
	})
	t.Run("none mode auto-detects app scope from explicit job AppName", func(t *testing.T) {
		w := newWorker(workerConfig{Reviewer: &mockReviewer{}})
		scope, scoped, err := w.resolveJobScope(LearningJob{
			Scope: skill.SkillScope{AppName: "app"},
		})
		require.NoError(t, err)
		require.True(t, scoped)
		require.Equal(t, skill.SkillScope{AppName: "app"}, scope)
	})
	t.Run("scoped service with nil session and zero scope", func(t *testing.T) {
		w := newWorker(workerConfig{
			Reviewer:       &mockReviewer{},
			SkillScopeMode: skill.SkillScopeApp,
		})
		scope, scoped, err := w.resolveJobScope(LearningJob{})
		require.NoError(t, err)
		require.False(t, scoped)
		require.True(t, scope.IsZero())
	})
}

func TestWorker_ScopedRoot(t *testing.T) {
	w := newWorker(workerConfig{
		Reviewer:       &mockReviewer{},
		SkillScopeMode: skill.SkillScopeApp,
	})
	// Not scoped → root returned unchanged.
	got, err := w.scopedRoot("/base", skill.SkillScope{AppName: "app"}, false)
	require.NoError(t, err)
	require.Equal(t, "/base", got)
	// Empty root → returned unchanged regardless of scoping.
	got, err = w.scopedRoot("", skill.SkillScope{AppName: "app"}, true)
	require.NoError(t, err)
	require.Equal(t, "", got)
	// Scoped → app path parts appended.
	got, err = w.scopedRoot("/base", skill.SkillScope{AppName: "app"}, true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/base", "apps", "app"), got)
	// Invalid scope (app mode without app name) → error.
	_, err = w.scopedRoot("/base", skill.SkillScope{}, true)
	require.Error(t, err)
}

func TestWorker_ScopedRoot_NoneModeUsesExplicitScope(t *testing.T) {
	w := newWorker(workerConfig{Reviewer: &mockReviewer{}})

	got, err := w.scopedRoot("/base", skill.SkillScope{AppName: "app"}, true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/base", "apps", "app"), got)

	got, err = w.scopedRoot("/base", skill.SkillScope{AppName: "app", UserID: "u"}, true)
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/base", "users", "app", "u"), got)
}

func TestWorker_ScopedBackends_CacheAndFallback(t *testing.T) {
	root := t.TempDir()
	w := newWorker(workerConfig{
		Reviewer:         &mockReviewer{},
		SkillScopeMode:   skill.SkillScopeApp,
		Publisher:        newFilePublisher(root),
		PublisherBaseDir: root,
		CandidateStore:   NewFileCandidateStore(root),
		ActivePointer:    NewFileActivePointer(root),
	})
	scope := skill.SkillScope{AppName: "app"}

	pub1, err := w.publisherForScope(scope, true)
	require.NoError(t, err)
	pub2, err := w.publisherForScope(scope, true)
	require.NoError(t, err)
	require.Same(t, pub1, pub2, "scoped publisher should be cached per root")

	store1, err := w.candidateStoreForScope(scope, true)
	require.NoError(t, err)
	store2, err := w.candidateStoreForScope(scope, true)
	require.NoError(t, err)
	require.Same(t, store1, store2)

	ptr1, err := w.activePointerForScope(scope, true)
	require.NoError(t, err)
	ptr2, err := w.activePointerForScope(scope, true)
	require.NoError(t, err)
	require.Same(t, ptr1, ptr2)

	// Not scoped → fall back to the static backends.
	pubStatic, err := w.publisherForScope(scope, false)
	require.NoError(t, err)
	require.Same(t, w.publisher, pubStatic)
	storeStatic, err := w.candidateStoreForScope(scope, false)
	require.NoError(t, err)
	require.Same(t, w.candidateStore, storeStatic)
	ptrStatic, err := w.activePointerForScope(scope, false)
	require.NoError(t, err)
	require.Same(t, w.activePointer, ptrStatic)
}

func TestWorker_RepositoryForScope(t *testing.T) {
	staticRepo := &mockSkillRepo{}
	scopedRepo := &mockSkillRepo{}
	provider := skill.RepositoryProviderFunc(
		func(_ context.Context, _ skill.SkillScope) (skill.Repository, error) {
			return scopedRepo, nil
		},
	)
	w := newWorker(workerConfig{
		Reviewer:          &mockReviewer{},
		SkillScopeMode:    skill.SkillScopeApp,
		SkillRepo:         staticRepo,
		SkillRepoProvider: provider,
	})
	// Not scoped → static repo.
	repo, err := w.repositoryForScope(context.Background(), skill.SkillScope{AppName: "app"}, false)
	require.NoError(t, err)
	require.Same(t, staticRepo, repo)
	// Scoped → provider repo.
	repo, err = w.repositoryForScope(context.Background(), skill.SkillScope{AppName: "app"}, true)
	require.NoError(t, err)
	require.Same(t, scopedRepo, repo)
}

func TestWorker_ProcessJob_SkipReason(t *testing.T) {
	pub := &mockPublisher{}
	rev := &mockReviewer{
		decision: &ReviewDecision{SkipReason: "nothing useful"},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hello"},
		model.Message{Role: model.RoleAssistant, Content: "hi"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "should not publish when skip_reason is set")
	pub.mu.Unlock()
}

func TestWorker_ProcessJob_SkipsWhenSkillWritesDetected(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "create a skill"},
		model.Message{Role: model.RoleAssistant, Content: "I wrote SKILL.md for you"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should be skipped when assistant already wrote SKILL.md")
	rev.mu.Unlock()
}

func TestWorker_ProcessJob_SkipsWhenStructuredSkillWriteDetected(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "create a reusable release skill"},
		model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "workspace_exec",
					Arguments: []byte(`{"command":"cat > skills/release/SKILL.md <<'EOF'"}`),
				},
			}},
		},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should be skipped when a tool call writes SKILL.md")
	rev.mu.Unlock()
}

func TestWorker_AsyncEnqueue(t *testing.T) {
	pub := &mockPublisher{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{{Name: "Async Skill", Steps: []string{"go"}}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
	})
	w.Start()
	defer w.Stop()

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "do it"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	err := w.Enqueue(context.Background(), LearningJob{Session: sess})
	require.NoError(t, err)

	// Wait for async processing.
	require.Eventually(t, func() bool {
		pub.mu.Lock()
		defer pub.mu.Unlock()
		return len(pub.skills) > 0
	}, 5*time.Second, 50*time.Millisecond)

	pub.mu.Lock()
	assert.Equal(t, "Async Skill", pub.skills[0].Name)
	pub.mu.Unlock()
}

func TestWorker_DeltaScan_Incremental(t *testing.T) {
	rev := &mockReviewer{
		decision: &ReviewDecision{},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
	})

	sess := newTestSession()
	base := time.Now()
	sess.Events = append(sess.Events, event.Event{
		Timestamp: base,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "old"},
		}}},
	})
	// Simulate a previous review.
	writeLastReviewAt(sess, base)

	sess.Events = append(sess.Events, event.Event{
		Timestamp: base.Add(time.Minute),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleUser, Content: "new"},
		}}},
	})

	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 1, rev.calls, "reviewer should see only the new delta")
	rev.mu.Unlock()
}

func TestScanDelta_CountsToolCalls(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{Type: "function"}, {Type: "function"}, {Type: "function"}, {Type: "function"},
					},
				},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "ok"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.Equal(t, 4, ctx.ToolCallCount)
}

func TestScanDelta_DetectsCorrection(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "here is the result"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "No, that's wrong, try again"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasUserCorrection)
}

func TestScanDelta_DetectsChineseCorrection(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "这里是结果"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "不是这样，应该按一手来源和发布日期整理"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasUserCorrection)
}

func TestScanDelta_PositiveChineseFeedbackIsNotCorrection(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "这里是结果"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "这个清单挺好，谢谢"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.False(t, ctx.HasUserCorrection)
}

func TestScanDelta_DetectsChineseFutureWorkflowFeedback(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "这里是结果"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "以后遇到 AI Agent 官方资讯整理，默认按这套工作流输出；每条必须有一手来源、发布日期、影响维度、低置信度风险。",
				},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasUserCorrection)
}

func TestScanDelta_DetectsEnglishFutureWorkflowFeedback(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "Here is the report."},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "Going forward, use this workflow and keep the same output fields by default.",
				},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasUserCorrection)
}

func TestScanDelta_DetectsRecoveredError(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleTool, Content: "Error: file not found"},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "I found the file at another path"},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	assert.True(t, ctx.HasRecoveredError)
}

func TestScanDelta_TranscriptIncludesToolMessagesAndCalls(t *testing.T) {
	sess := newTestSession()
	now := time.Now()
	sess.Events = append(sess.Events,
		event.Event{
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I will create a skill.",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call-1",
						Function: model.FunctionDefinitionParam{
							Name:      "workspace_exec",
							Arguments: []byte(`{"command":"cat > skills/new/SKILL.md <<'EOF'"}`),
						},
					}},
				},
			}}},
		},
		event.Event{
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					ToolName: "workspace_exec",
					ToolID:   "call-1",
					Content:  "wrote skills/new/SKILL.md",
				},
			}}},
		},
	)

	_, ctx := scanDelta(sess, time.Time{})
	require.Len(t, ctx.Transcript, 2)
	assert.Equal(t, model.RoleAssistant, ctx.Transcript[0].Role)
	require.Len(t, ctx.Transcript[0].ToolCalls, 1)
	assert.Equal(t, "workspace_exec", ctx.Transcript[0].ToolCalls[0].Name)
	assert.Contains(t, ctx.Transcript[0].ToolCalls[0].Arguments, "SKILL.md")
	assert.Equal(t, model.RoleTool, ctx.Transcript[1].Role)
	assert.Equal(t, "workspace_exec", ctx.Transcript[1].ToolName)
}

func TestWorker_ApplyDecision_UpdateExistingSkill(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Existing", Description: "old"}},
		bodies:    map[string]string{"Existing": "body"},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Updates: []*SkillUpdate{{
				Name: "Existing",
				NewSpec: &SkillSpec{
					Name:        "Existing",
					Description: "new desc",
					WhenToUse:   "always",
					Steps:       []string{"do better"},
				},
			}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "improve"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "update should call UpsertSkill")
	assert.Equal(t, "Existing", pub.skills[0].Name)
	assert.Equal(t, "new desc", pub.skills[0].Description)
	pub.mu.Unlock()

	repo.mu.Lock()
	assert.Equal(t, 1, repo.refreshed, "repo should refresh after update")
	repo.mu.Unlock()
}

func TestWorker_ApplyDecision_UpdateUnknownSkillIsDropped(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{} // no skills
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Updates: []*SkillUpdate{{
				Name: "Ghost",
				NewSpec: &SkillSpec{
					Name:        "Ghost",
					Description: "phantom",
					WhenToUse:   "never",
					Steps:       []string{"haunt"},
				},
			}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "unknown update target must not be written")
	pub.mu.Unlock()
	repo.mu.Lock()
	assert.Equal(t, 0, repo.refreshed, "no refresh when no mutation occurred")
	repo.mu.Unlock()
}

func TestWorker_ApplyDecision_DeleteExistingSkill(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Stale"}},
		bodies:    map[string]string{"Stale": "outdated"},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{Deletions: []string{"Stale"}},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "drop"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Equal(t, []string{"Stale"}, pub.deletions)
	pub.mu.Unlock()
	repo.mu.Lock()
	assert.Equal(t, 1, repo.refreshed)
	repo.mu.Unlock()
}

func TestWorker_ApplyDecision_DeleteUnknownIsIdempotent(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{} // no skills
	rev := &mockReviewer{
		decision: &ReviewDecision{Deletions: []string{"Phantom"}},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "drop"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.deletions)
	pub.mu.Unlock()
	repo.mu.Lock()
	assert.Equal(t, 0, repo.refreshed)
	repo.mu.Unlock()
}

func TestWorker_ProcessJob_ForwardsOutcomeToReviewer(t *testing.T) {
	rev := &capturingReviewer{}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)

	score := 0.0
	want := &Outcome{
		Status:    OutcomeFail,
		Score:     &score,
		Notes:     "missing economic_snapshot.json",
		Evaluator: "skillcraft",
	}
	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{Session: sess, Outcome: want},
	})

	got := rev.snapshot()
	require.NotNil(t, got)
	require.NotNil(t, got.Outcome, "outcome must be forwarded to reviewer")
	assert.Equal(t, want, got.Outcome)
}

func TestWorker_Enqueue_ForwardsOutcomeViaAsyncQueue(t *testing.T) {
	rev := &capturingReviewer{}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
	})
	w.Start()
	defer w.Stop()

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)

	want := &Outcome{Status: OutcomePartial, Notes: "wrong indicator code"}
	require.NoError(t, w.Enqueue(context.Background(), LearningJob{
		Session: sess,
		Outcome: want,
	}))

	require.Eventually(t, func() bool {
		return rev.snapshot() != nil
	}, 5*time.Second, 25*time.Millisecond)

	got := rev.snapshot()
	require.NotNil(t, got.Outcome)
	assert.Equal(t, OutcomePartial, got.Outcome.Status)
	assert.Equal(t, "wrong indicator code", got.Outcome.Notes)
}

func TestWorker_ProcessJob_RedactsSecretsBeforeReviewer(t *testing.T) {
	rev := &capturingReviewer{}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{
			Role:    model.RoleUser,
			Content: "OPENAI_API_KEY=sk-test-REDACT-ME-333",
		},
		model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "workspace_exec",
					Arguments: []byte(`{"token":"tok-FAKE-0000000"}`),
				},
			}},
		},
		model.Message{
			Role:     model.RoleTool,
			ToolName: "workspace_exec",
			Content:  "Authorization: Bearer tok-FAKE-0000000",
		},
	)

	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{
			Session: sess,
			Outcome: &Outcome{Status: OutcomeFail, Notes: "api_key=sk-test-REDACT-ME-222"},
		},
	})

	got := rev.snapshot()
	require.NotNil(t, got)
	require.Len(t, got.Transcript, 3)
	assert.NotContains(t, got.Transcript[0].Content, "sk-test-REDACT-ME-333")
	assert.Contains(t, got.Transcript[0].Content, reviewerRedactedValue)
	assert.NotContains(t, got.Transcript[1].ToolCalls[0].Arguments, "tok-FAKE-0000000")
	assert.NotContains(t, got.Transcript[2].Content, "tok-FAKE-0000000")
	require.NotNil(t, got.Outcome)
	assert.NotContains(t, got.Outcome.Notes, "sk-test-REDACT-ME-222")
	assert.Contains(t, got.Outcome.Notes, reviewerRedactedValue)
}

func TestWorker_ProcessJob_ForwardsExistingSkillsWithBodyExcerpt(t *testing.T) {
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Known", Description: "desc"}},
		bodies:    map[string]string{"Known": "full body content"},
	}
	rev := &capturingReviewer{}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	in := rev.snapshot()
	require.NotNil(t, in)
	require.Len(t, in.ExistingSkills, 1)
	got := in.ExistingSkills[0]
	assert.Equal(t, "Known", got.Name)
	assert.Equal(t, "desc", got.Description)
	// Default budget is large enough to fit the short body verbatim.
	assert.Equal(t, "full body content", got.BodyExcerpt)
}

func TestWorker_ProcessJob_TruncatesBodyExcerptToConfiguredBudget(t *testing.T) {
	const longBody = "step 1: do a long thing\nstep 2: do another long thing\nstep 3: save"
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Known", Description: "desc"}},
		bodies:    map[string]string{"Known": longBody},
	}
	rev := &capturingReviewer{}
	w := newWorker(workerConfig{
		Reviewer:                  rev,
		ReviewPolicy:              alwaysReviewPolicy{},
		SkillRepo:                 repo,
		ExistingSkillBodyMaxChars: 30,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	in := rev.snapshot()
	require.NotNil(t, in)
	require.Len(t, in.ExistingSkills, 1)
	got := in.ExistingSkills[0]
	assert.LessOrEqual(t, len(got.BodyExcerpt), 30,
		"body excerpt must respect the configured budget")
	assert.Contains(t, got.BodyExcerpt, "[truncated]",
		"truncation marker must be present")
	assert.Contains(t, got.BodyExcerpt, "step 1",
		"head of the body must be preserved")
}

func TestWorker_ProcessJob_OmitsBodyWhenBudgetIsNegative(t *testing.T) {
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Known", Description: "desc"}},
		bodies:    map[string]string{"Known": "full body content"},
	}
	rev := &capturingReviewer{}
	w := newWorker(workerConfig{
		Reviewer:                  rev,
		ReviewPolicy:              alwaysReviewPolicy{},
		SkillRepo:                 repo,
		ExistingSkillBodyMaxChars: -1,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	in := rev.snapshot()
	require.NotNil(t, in)
	require.Len(t, in.ExistingSkills, 1)
	got := in.ExistingSkills[0]
	assert.Equal(t, "", got.BodyExcerpt,
		"negative budget must opt out of body loading entirely")
}

func TestWorker_ProcessJob_ReconcilerRewritesSupersetCandidate(t *testing.T) {
	// Reviewer emits a proliferation candidate ("Foo Workflow - 3 Cities")
	// for a library that already contains "Foo Workflow". The worker
	// must invoke the reconciler, rewrite the candidate to an `updates`
	// entry against the existing parent, and the publisher must see one
	// upsert against the parent name (not the suffixed candidate name).
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Foo Workflow", Description: "shared"}},
		bodies:    map[string]string{"Foo Workflow": "step 1: do thing"},
	}
	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Foo Workflow - 3 Cities",
			Description: "specific to 3 cities",
			WhenToUse:   "when there are 3 cities",
			Steps:       []string{"a", "b", "c"},
		}},
	}}
	pub := &mockPublisher{}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
		Publisher:    pub,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.skills, 1, "exactly one upsert should reach the publisher")
	assert.Equal(t, "Foo Workflow", pub.skills[0].Name,
		"reconciler must redirect the upsert to the existing parent's name, "+
			"not the proliferation suffix")
	// Body content should still come from the candidate's spec — only
	// the name moves.
	assert.Equal(t, "specific to 3 cities", pub.skills[0].Description)
}

// --- test helpers ---

// capturingReviewer is a Reviewer that records the last input it received.
// All access goes through mu so the async-enqueue tests do not race on
// the recorded input pointer.
type capturingReviewer struct {
	mu    sync.Mutex
	input *ReviewInput
}

func (c *capturingReviewer) Review(_ context.Context, in *ReviewInput) (*ReviewDecision, error) {
	c.mu.Lock()
	c.input = in
	c.mu.Unlock()
	return &ReviewDecision{}, nil
}

func (c *capturingReviewer) snapshot() *ReviewInput {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.input
}

// alwaysReviewPolicy is a ReviewPolicy that always triggers review.
type alwaysReviewPolicy struct{}

func (alwaysReviewPolicy) ShouldReview(context.Context, *ReviewPolicyInput) (bool, error) {
	return true, nil
}

func TestWorker_ApprovalGate_PromotesCleanCandidate(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Clean Skill",
			Description: "desc",
			WhenToUse:   "use",
			Steps:       []string{"a", "b"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "publisher should receive the skill")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 1, metrics.RevisionsPromoted)
	assert.Equal(t, 1, metrics.CreatesApplied)
	assert.Equal(t, 0, metrics.SpecGateRejected)
	assert.Equal(t, 0, metrics.SafetyGateRejected)

	got, err := ptr.Get(context.Background(), skillIDFromName("Clean Skill"))
	require.NoError(t, err)
	assert.NotEmpty(t, got, "active pointer should be set")
}

func TestWorker_ApprovalGate_PromoteArchivesPreviousActive(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	ctx := context.Background()

	oldRev := &Revision{
		SkillID:    skillIDFromName("Clean Skill"),
		RevisionID: "rev-old",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionActive,
		Spec:       &SkillSpec{Name: "Clean Skill", Description: "old", WhenToUse: "use", Steps: []string{"a", "b"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, oldRev))
	require.NoError(t, ptr.Set(ctx, oldRev.SkillID, oldRev.RevisionID))

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Clean Skill",
			Description: "desc",
			WhenToUse:   "use",
			Steps:       []string{"a", "b"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: ctx, job: LearningJob{Session: sess}})

	activeRev, err := ptr.Get(ctx, oldRev.SkillID)
	require.NoError(t, err)
	require.NotEmpty(t, activeRev)
	require.NotEqual(t, oldRev.RevisionID, activeRev)
	storedOld, err := store.ReadRevision(ctx, oldRev.SkillID, oldRev.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, storedOld.Status)
	raw, err := os.ReadFile(filepath.Join(dir, oldRev.SkillID, "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"action":"archive"`)
	assert.Contains(t, string(raw), `"revision_id":"rev-old"`)
}

func TestWorker_ApprovalGate_SpecGateRejects(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Bad", // missing description, when_to_use, enough steps
			Description: "",
			Steps:       []string{},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "publisher should NOT receive a gate-rejected skill")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 0, metrics.RevisionsPromoted)
	assert.Equal(t, 1, metrics.SpecGateRejected)
}

func TestWorker_ApprovalGate_ShadowModePublishesAnyway(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	// A reviewer decision where the reconciler cannot rewrite to an
	// existing skill (no matching parent). SpecGate will still reject
	// because description / when_to_use / steps are missing.
	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Rough Draft Skill",
			Description: "",
			WhenToUse:   "",
			Steps:       []string{},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:           rev,
		Publisher:          pub,
		ReviewPolicy:       alwaysReviewPolicy{},
		SkillRepo:          repo,
		CandidateStore:     store,
		ActivePointer:      ptr,
		SpecGate:           NewDefaultSpecGate(),
		SafetyGate:         NewDefaultSafetyGate(),
		ApprovalGateShadow: true,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Len(t, pub.skills, 1, "shadow mode should still publish")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.SpecGateRejected)
	assert.Equal(t, 1, metrics.ShadowModeBypassed)
}

// --- Tests for applyDeletionsWithGate ---

func TestWorker_ApplyDeletionsWithGate_DeletesExistingSkill(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Stale"}},
		bodies:    map[string]string{"Stale": "old body"},
	}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	// Pre-set the active pointer so we can verify Clear is called.
	require.NoError(t, ptr.Set(context.Background(), skillIDFromName("Stale"), "fake-rev"))

	rev := &mockReviewer{decision: &ReviewDecision{Deletions: []string{"Stale"}}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "drop it"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Equal(t, []string{"Stale"}, pub.deletions, "publisher should receive the deletion")
	pub.mu.Unlock()

	// Active pointer should be cleared.
	got, err := ptr.Get(context.Background(), skillIDFromName("Stale"))
	require.NoError(t, err)
	assert.Empty(t, got, "active pointer should be cleared after deletion")

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.DeletionsApplied)
}

func TestWorker_ApplyDeletionsWithGate_NilPublisherSkips(t *testing.T) {
	dir := t.TempDir()
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Existing"}},
		bodies:    map[string]string{"Existing": "body"},
	}
	store := NewFileCandidateStore(dir)

	rev := &mockReviewer{decision: &ReviewDecision{Deletions: []string{"Existing"}}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      nil, // no publisher
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "drop it"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	// With nil publisher, the deletion should be skipped (no panic, no mutation).
	repo.mu.Lock()
	assert.Equal(t, 0, repo.refreshed, "repo should not refresh when publisher is nil")
	repo.mu.Unlock()
}

func TestWorker_ApplyDeletionsWithGate_NonexistentSkillSkipped(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{} // no skills
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{Deletions: []string{"Ghost"}}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "drop it"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.deletions, "publisher should NOT receive deletion for nonexistent skill")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 0, metrics.DeletionsApplied)
}

// --- Tests for applyUpdatesWithGate ---

func TestWorker_ApplyUpdatesWithGate_PassesGates(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Existing", Description: "old"}},
		bodies:    map[string]string{"Existing": "old body"},
	}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Updates: []*SkillUpdate{{
			Name: "Existing",
			NewSpec: &SkillSpec{
				Name:        "Existing",
				Description: "updated desc",
				WhenToUse:   "always",
				Steps:       []string{"step one", "step two"},
			},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "improve"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "update should reach publisher")
	assert.Equal(t, "Existing", pub.skills[0].Name)
	assert.Equal(t, "updated desc", pub.skills[0].Description)
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.UpdatesApplied)
}

func TestWorker_ApplyUpdatesWithGate_SpecGateRejects(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Existing", Description: "old"}},
		bodies:    map[string]string{"Existing": "old body"},
	}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	// The update has empty description, so spec gate will reject it.
	rev := &mockReviewer{decision: &ReviewDecision{
		Updates: []*SkillUpdate{{
			Name: "Existing",
			NewSpec: &SkillSpec{
				Name:        "Existing",
				Description: "d",
				WhenToUse:   "w",
				Steps:       []string{"only one"}, // min 2 steps
			},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "improve"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "publisher should NOT receive spec-gate-rejected update")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.SpecGateRejected)
	assert.Equal(t, 0, metrics.UpdatesApplied)
}

func TestWorker_ApplyUpdatesWithGate_SkipsNonEvolutionSkill(t *testing.T) {
	dir := t.TempDir()
	managedDir := "/state/skills/evolution"
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Bundled Weather", Description: "bundled"}},
		bodies:    map[string]string{"Bundled Weather": "body"},
		paths: map[string]string{
			"Bundled Weather": "/state/skills/bundled/weather-monitor",
		},
	}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Updates: []*SkillUpdate{{
			Name: "Bundled Weather",
			NewSpec: &SkillSpec{
				Name:        "Bundled Weather",
				Description: "updated desc",
				WhenToUse:   "always",
				Steps:       []string{"step one", "step two"},
			},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:         rev,
		Publisher:        pub,
		ReviewPolicy:     alwaysReviewPolicy{},
		SkillRepo:        repo,
		ManagedSkillsDir: managedDir,
		CandidateStore:   store,
		ActivePointer:    ptr,
		SpecGate:         NewDefaultSpecGate(),
		SafetyGate:       NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "improve"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "gated update to bundled skill should be skipped")
	pub.mu.Unlock()
	metrics := w.approvalGateMetrics()
	assert.Equal(t, 0, metrics.CandidatesSeen)
	assert.Equal(t, 0, metrics.UpdatesApplied)
}

// --- Tests for Enqueue ---

func TestWorker_Enqueue_NilReviewer(t *testing.T) {
	w := newWorker(workerConfig{Reviewer: nil})
	err := w.Enqueue(context.Background(), LearningJob{Session: newTestSession()})
	assert.NoError(t, err, "enqueue with nil reviewer should silently return nil")
}

func TestWorker_Enqueue_NilSession(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := newWorker(workerConfig{Reviewer: rev})
	err := w.Enqueue(context.Background(), LearningJob{Session: nil})
	assert.NoError(t, err, "enqueue with nil session should silently return nil")
}

func TestWorker_Enqueue_SynchronousFallbackWhenNotStarted(t *testing.T) {
	pub := &mockPublisher{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{{
				Name:        "Sync Skill",
				Description: "d",
				WhenToUse:   "w",
				Steps:       []string{"go"},
			}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
	})
	// Do NOT call w.Start() — the worker is not started.

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	err := w.Enqueue(context.Background(), LearningJob{Session: sess})
	require.NoError(t, err)

	// Since worker is not started, the job should be processed synchronously.
	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "job should be processed synchronously when worker not started")
	assert.Equal(t, "Sync Skill", pub.skills[0].Name)
	pub.mu.Unlock()
}

func TestWorker_Enqueue_CancelledContextReturnsNil(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := newWorker(workerConfig{Reviewer: rev})
	w.Start()
	defer w.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before enqueue

	err := w.Enqueue(ctx, LearningJob{Session: newTestSession()})
	assert.NoError(t, err, "cancelled context should return nil")
}

// --- Tests for runSafetyGate ---

func TestWorker_SafetyGate_PassesCleanRevision(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Clean Skill",
			Description: "no dangerous content",
			WhenToUse:   "when needed",
			Steps:       []string{"step 1", "step 2"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "clean revision should pass safety gate")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 0, metrics.SafetyGateRejected)
}

func TestWorker_SafetyGate_RejectsDangerousContent(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Dangerous Skill",
			Description: "run rm -rf / to clean up",
			WhenToUse:   "when cleaning",
			Steps:       []string{"rm -rf /home", "done"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		SpecGate:       NewDefaultSpecGate(),
		SafetyGate:     NewDefaultSafetyGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "dangerous revision should be rejected by safety gate")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.SafetyGateRejected)
}

// --- Tests for applySkills (non-gated path with publisher) ---

func TestWorker_ApplySkills_WithPublisher(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{
				{Name: "Skill A", Steps: []string{"a"}},
				{Name: "Skill B", Steps: []string{"b"}},
			},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 2, "both skills should be published")
	pub.mu.Unlock()

	repo.mu.Lock()
	assert.Equal(t, 1, repo.refreshed, "repo should be refreshed after writing skills")
	repo.mu.Unlock()
}

func TestWorker_ApplySkills_NilPublisher(t *testing.T) {
	repo := &mockSkillRepo{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{
				{Name: "Orphan", Steps: []string{"a"}},
			},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    nil, // no publisher
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	repo.mu.Lock()
	assert.Equal(t, 0, repo.refreshed, "repo should not refresh when no publisher")
	repo.mu.Unlock()
}

func TestWorker_ApplySkills_NilSpecInSlice(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{nil, {Name: "Valid", Steps: []string{"go"}}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "nil specs should be skipped")
	assert.Equal(t, "Valid", pub.skills[0].Name)
	pub.mu.Unlock()
}

func TestWorker_ApplySkills_PublisherError(t *testing.T) {
	pub := &mockPublisher{err: fmt.Errorf("disk full")}
	repo := &mockSkillRepo{}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Skills: []*SkillSpec{
				{Name: "Fail", Steps: []string{"a"}},
			},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "go"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	// Publisher error means no mutation -> no refresh.
	repo.mu.Lock()
	assert.Equal(t, 0, repo.refreshed, "repo should not refresh when publisher errors")
	repo.mu.Unlock()
}

func TestWorker_ApprovalGate_EffectivenessGateHoldsOnFail(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Learn From Disaster",
			Description: "d",
			WhenToUse:   "u",
			Steps:       []string{"a", "b"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:          rev,
		Publisher:         pub,
		ReviewPolicy:      alwaysReviewPolicy{},
		SkillRepo:         repo,
		CandidateStore:    store,
		ActivePointer:     ptr,
		SpecGate:          NewDefaultSpecGate(),
		SafetyGate:        NewDefaultSafetyGate(),
		EffectivenessGate: NewOutcomeBasedEffectivenessGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "work"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	// Attach a failure outcome
	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{
			Session: sess,
			Outcome: &Outcome{Status: OutcomeFail, Notes: "weather_report.json not found"},
		},
	})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "publisher should NOT receive a revision held by effectiveness gate")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 0, metrics.RevisionsPromoted)
	assert.Equal(t, 0, metrics.SpecGateRejected)
	assert.Equal(t, 0, metrics.SafetyGateRejected)
	assert.Equal(t, 1, metrics.EffectivenessGateRejected)

	// Revision should be in PendingEval status on disk
	list, _ := store.ListRevisions(context.Background(), skillIDFromName("Learn From Disaster"))
	require.Len(t, list, 1)
	stored, _ := store.ReadRevision(context.Background(), skillIDFromName("Learn From Disaster"), list[0])
	assert.Equal(t, RevisionPendingEval, stored.Status)
}

// ---------------------------------------------------------------------------
// HumanGate integration tests
// ---------------------------------------------------------------------------

func TestWorker_HumanGate_HoldsRevision(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "New Skill",
			Description: "does something",
			WhenToUse:   "when needed",
			Steps:       []string{"step1", "step2"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		HumanGate:      NewAlwaysHoldGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "do work"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{
			Session: sess,
			Outcome: &Outcome{Status: OutcomeSuccess},
		},
	})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "publisher should NOT receive a revision held by human gate")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 0, metrics.RevisionsPromoted)
	assert.Equal(t, 1, metrics.HumanGateHeld)

	// Revision should be in pending_approval status on disk
	list, _ := store.ListRevisions(context.Background(), skillIDFromName("New Skill"))
	require.Len(t, list, 1)
	stored, _ := store.ReadRevision(context.Background(), skillIDFromName("New Skill"), list[0])
	assert.Equal(t, RevisionPendingApproval, stored.Status)
	assert.NotNil(t, stored.HumanReport)
	assert.True(t, stored.HumanReport.Held)
}

func TestWorker_HumanGate_HoldsDeleteRevision(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Stale Skill"}},
		bodies:    map[string]string{"Stale Skill": "old body"},
	}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Deletions: []string{"Stale Skill"},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		HumanGate:      NewAlwaysHoldGate(),
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "drop stale skill"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)
	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{
			Session: sess,
			Outcome: &Outcome{Status: OutcomeSuccess},
		},
	})

	pub.mu.Lock()
	assert.Empty(t, pub.deletions, "publisher should NOT delete a revision held by human gate")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 0, metrics.RevisionsPromoted)
	assert.Equal(t, 1, metrics.HumanGateHeld)
	assert.Equal(t, 0, metrics.DeletionsApplied)

	list, err := store.ListRevisions(context.Background(), skillIDFromName("Stale Skill"))
	require.NoError(t, err)
	require.Len(t, list, 1)
	stored, err := store.ReadRevision(context.Background(), skillIDFromName("Stale Skill"), list[0])
	require.NoError(t, err)
	assert.Equal(t, RevisionActionDelete, stored.Action)
	assert.Equal(t, "Stale Skill", stored.TargetName)
	assert.Nil(t, stored.Spec)
	assert.Equal(t, RevisionPendingApproval, stored.Status)
	require.NotNil(t, stored.HumanReport)
	assert.True(t, stored.HumanReport.Held)
}

func TestWorker_HumanGate_PassesUpdate(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{{Name: "Existing Skill"}},
		bodies:    map[string]string{"Existing Skill": "body"},
	}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Updates: []*SkillUpdate{{
			Name: "Existing Skill",
			NewSpec: &SkillSpec{
				Name:        "Existing Skill",
				Description: "updated",
				WhenToUse:   "when needed",
				Steps:       []string{"s1", "s2"},
			},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		HumanGate:      NewCreateOnlyHoldGate(), // only holds creates
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "update work"},
		model.Message{Role: model.RoleAssistant, Content: "updated"},
	)
	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{
			Session: sess,
			Outcome: &Outcome{Status: OutcomeSuccess},
		},
	})

	pub.mu.Lock()
	assert.Len(t, pub.skills, 1, "update should pass through CreateOnlyHoldGate")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.RevisionsPromoted)
	assert.Equal(t, 0, metrics.HumanGateHeld)
}

type errorHumanGate struct{}

func (g *errorHumanGate) ShouldHold(_ context.Context, _ *Revision, _ *Outcome) (bool, error) {
	return false, fmt.Errorf("gate unavailable")
}

func TestWorker_HumanGate_ErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	pub := &mockPublisher{}
	repo := &mockSkillRepo{}
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)

	rev := &mockReviewer{decision: &ReviewDecision{
		Skills: []*SkillSpec{{
			Name:        "Error Gate Skill",
			Description: "test",
			WhenToUse:   "test",
			Steps:       []string{"a", "b"},
		}},
	}}
	w := newWorker(workerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		ReviewPolicy:   alwaysReviewPolicy{},
		SkillRepo:      repo,
		CandidateStore: store,
		ActivePointer:  ptr,
		HumanGate:      &errorHumanGate{},
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "x"},
		model.Message{Role: model.RoleAssistant, Content: "y"},
	)
	w.processJob(&pendingJob{
		ctx: context.Background(),
		job: LearningJob{
			Session: sess,
			Outcome: &Outcome{Status: OutcomeSuccess},
		},
	})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "human gate error should fail closed (hold)")
	pub.mu.Unlock()

	metrics := w.approvalGateMetrics()
	assert.Equal(t, 1, metrics.HumanGateHeld)
	assert.Equal(t, 0, metrics.RevisionsPromoted)
}

// ---------------------------------------------------------------------------
// Write isolation tests (ManagedSkillsDir)
// ---------------------------------------------------------------------------

func TestWorker_ApplyUpdates_SkipsNonEvolutionSkill(t *testing.T) {
	managedDir := "/state/skills/evolution"
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{
			{Name: "Bundled Weather", Description: "bundled skill"},
		},
		bodies: map[string]string{"Bundled Weather": "body"},
		paths: map[string]string{
			"Bundled Weather": "/state/skills/bundled/weather-monitor",
		},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Updates: []*SkillUpdate{{
				Name: "Bundled Weather",
				NewSpec: &SkillSpec{
					Name:        "Bundled Weather",
					Description: "new desc",
					WhenToUse:   "always",
					Steps:       []string{"step 1"},
				},
			}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:         rev,
		Publisher:        pub,
		ReviewPolicy:     alwaysReviewPolicy{},
		SkillRepo:        repo,
		ManagedSkillsDir: managedDir,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "update weather"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.skills, "update to bundled skill should be skipped")
	pub.mu.Unlock()
}

func TestWorker_ApplyUpdates_AllowsEvolutionSkill(t *testing.T) {
	managedDir := "/state/skills/evolution"
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{
			{Name: "Learned Analysis", Description: "evolution skill"},
		},
		bodies: map[string]string{"Learned Analysis": "body"},
		paths: map[string]string{
			"Learned Analysis": "/state/skills/evolution/learned-analysis",
		},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Updates: []*SkillUpdate{{
				Name: "Learned Analysis",
				NewSpec: &SkillSpec{
					Name:        "Learned Analysis",
					Description: "updated desc",
					WhenToUse:   "always",
					Steps:       []string{"step 1"},
				},
			}},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:         rev,
		Publisher:        pub,
		ReviewPolicy:     alwaysReviewPolicy{},
		SkillRepo:        repo,
		ManagedSkillsDir: managedDir,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "update analysis"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "update to evolution skill should be allowed")
	assert.Equal(t, "Learned Analysis", pub.skills[0].Name)
	pub.mu.Unlock()
}

func TestWorker_ApplyDeletions_SkipsNonEvolutionSkill(t *testing.T) {
	root := t.TempDir()
	managedDir := filepath.Join(root, "skills", "evolution")
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{
			{Name: "User Skill", Description: "user-authored skill"},
		},
		bodies: map[string]string{"User Skill": "body"},
		paths: map[string]string{
			"User Skill": filepath.Join(root, "skills", "local", "user-skill"),
		},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Deletions: []string{"User Skill"},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:         rev,
		Publisher:        pub,
		ReviewPolicy:     alwaysReviewPolicy{},
		SkillRepo:        repo,
		ManagedSkillsDir: managedDir,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "delete user skill"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	assert.Empty(t, pub.deletions, "delete to user-authored skill should be skipped")
	pub.mu.Unlock()
	repo.mu.Lock()
	assert.Equal(t, 0, repo.refreshed, "repo should not refresh when deletion was skipped")
	repo.mu.Unlock()
}

func TestWorker_ApplyDeletions_AllowsEvolutionSkill(t *testing.T) {
	root := t.TempDir()
	managedDir := filepath.Join(root, "skills", "evolution")
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{
			{Name: "Learned Analysis", Description: "evolution skill"},
		},
		bodies: map[string]string{"Learned Analysis": "body"},
		paths: map[string]string{
			"Learned Analysis": filepath.Join(managedDir, "learned-analysis"),
		},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Deletions: []string{"Learned Analysis"},
		},
	}
	w := newWorker(workerConfig{
		Reviewer:         rev,
		Publisher:        pub,
		ReviewPolicy:     alwaysReviewPolicy{},
		SkillRepo:        repo,
		ManagedSkillsDir: managedDir,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "delete learned skill"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Equal(t, []string{"Learned Analysis"}, pub.deletions, "delete to evolution skill should be allowed")
	pub.mu.Unlock()
	repo.mu.Lock()
	assert.Equal(t, 1, repo.refreshed)
	repo.mu.Unlock()
}

func TestWorker_ApplyUpdates_NoIsolationWhenManagedDirEmpty(t *testing.T) {
	pub := &mockPublisher{}
	repo := &mockSkillRepo{
		summaries: []skill.Summary{
			{Name: "Any Skill", Description: "any"},
		},
		bodies: map[string]string{"Any Skill": "body"},
		paths: map[string]string{
			"Any Skill": "/anywhere/any-skill",
		},
	}
	rev := &mockReviewer{
		decision: &ReviewDecision{
			Updates: []*SkillUpdate{{
				Name: "Any Skill",
				NewSpec: &SkillSpec{
					Name:        "Any Skill",
					Description: "updated",
					WhenToUse:   "always",
					Steps:       []string{"step"},
				},
			}},
		},
	}
	// ManagedSkillsDir not set → no isolation.
	w := newWorker(workerConfig{
		Reviewer:     rev,
		Publisher:    pub,
		ReviewPolicy: alwaysReviewPolicy{},
		SkillRepo:    repo,
	})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "update"},
		model.Message{Role: model.RoleAssistant, Content: "ok"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "no isolation when ManagedSkillsDir empty")
	pub.mu.Unlock()
}
