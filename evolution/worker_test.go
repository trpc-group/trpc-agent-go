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
func (m *mockSkillRepo) Path(string) (string, error) { return "", nil }
func (m *mockSkillRepo) Refresh() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshed++
	return nil
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
	w := NewWorker(WorkerConfig{Reviewer: rev})

	sess := newTestSession()
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should not be called when no messages")
	rev.mu.Unlock()
}

func TestWorker_ProcessJob_PolicyRejects(t *testing.T) {
	rev := &mockReviewer{decision: &ReviewDecision{}}
	w := NewWorker(WorkerConfig{Reviewer: rev})

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "hi"},
		model.Message{Role: model.RoleAssistant, Content: "hello"},
	)
	w.processJob(&pendingJob{ctx: context.Background(), job: LearningJob{Session: sess}})

	rev.mu.Lock()
	assert.Equal(t, 0, rev.calls, "reviewer should not be called when policy rejects")
	rev.mu.Unlock()

	raw, ok := sess.GetState(SessionStateKeyLastReviewAt)
	assert.True(t, ok, "last_review_at should be written even when skipped")
	assert.NotEmpty(t, raw)
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

	// Use an AlwaysPolicy to bypass the threshold.
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
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

func TestWorker_ProcessJob_SkipReason(t *testing.T) {
	pub := &mockPublisher{}
	rev := &mockReviewer{
		decision: &ReviewDecision{SkipReason: "nothing useful"},
	}
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Publisher: pub,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
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
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer: rev,
		Policy:   alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
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
	w := NewWorker(WorkerConfig{
		Reviewer:                  rev,
		Policy:                    alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer:                  rev,
		Policy:                    alwaysPolicy{},
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
	w := NewWorker(WorkerConfig{
		Reviewer:  rev,
		Policy:    alwaysPolicy{},
		SkillRepo: repo,
		Publisher: pub,
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

// alwaysPolicy is a Policy that always triggers review.
type alwaysPolicy struct{}

func (alwaysPolicy) ShouldReview(_ *ReviewContext) bool { return true }

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
	w := NewWorker(WorkerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		Policy:         alwaysPolicy{},
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

	metrics := w.ApprovalGateMetricsJSON()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 1, metrics.RevisionsPromoted)
	assert.Equal(t, 1, metrics.CreatesApplied)
	assert.Equal(t, 0, metrics.SpecGateRejected)
	assert.Equal(t, 0, metrics.SafetyGateRejected)

	got, err := ptr.Get(context.Background(), SkillIDFromName("Clean Skill"))
	require.NoError(t, err)
	assert.NotEmpty(t, got, "active pointer should be set")
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
	w := NewWorker(WorkerConfig{
		Reviewer:       rev,
		Publisher:      pub,
		Policy:         alwaysPolicy{},
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

	metrics := w.ApprovalGateMetricsJSON()
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
	w := NewWorker(WorkerConfig{
		Reviewer:           rev,
		Publisher:          pub,
		Policy:             alwaysPolicy{},
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

	metrics := w.ApprovalGateMetricsJSON()
	assert.Equal(t, 1, metrics.SpecGateRejected)
	assert.Equal(t, 1, metrics.ShadowModeBypassed)
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
	w := NewWorker(WorkerConfig{
		Reviewer:          rev,
		Publisher:         pub,
		Policy:            alwaysPolicy{},
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

	metrics := w.ApprovalGateMetricsJSON()
	assert.Equal(t, 1, metrics.CandidatesSeen)
	assert.Equal(t, 1, metrics.RevisionsWritten)
	assert.Equal(t, 0, metrics.RevisionsPromoted)
	assert.Equal(t, 0, metrics.SpecGateRejected)
	assert.Equal(t, 0, metrics.SafetyGateRejected)
	assert.Equal(t, 1, metrics.EffectivenessGateRejected)

	// Revision should be in PendingEval status on disk
	list, _ := store.ListRevisions(context.Background(), SkillIDFromName("Learn From Disaster"))
	require.Len(t, list, 1)
	stored, _ := store.ReadRevision(context.Background(), SkillIDFromName("Learn From Disaster"), list[0])
	assert.Equal(t, RevisionPendingEval, stored.Status)
}
