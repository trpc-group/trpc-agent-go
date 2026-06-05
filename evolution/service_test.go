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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// stubModel implements model.Model and returns a canned JSON response.
type stubModel struct {
	response string
}

func (m *stubModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{Content: m.response},
		}},
	}
	close(ch)
	return ch, nil
}

func (m *stubModel) Info() model.Info { return model.Info{Name: "stub"} }

func TestService_ApprovalGateMetrics(t *testing.T) {
	mdl := &stubModel{response: `{"skip_reason":"nothing useful"}`}
	svc := NewService(mdl,
		WithManagedSkillsDir(t.TempDir()),
		WithReviewPolicy(alwaysReviewPolicy{}),
	)
	require.NotNil(t, svc)
	t.Cleanup(func() { _ = svc.Close() })

	provider, ok := svc.(ApprovalGateMetricsProvider)
	require.True(t, ok, "service should expose approval-gate metrics")

	metrics := provider.ApprovalGateMetrics()
	// A freshly-created service has no gate activity yet.
	assert.Equal(t, 0, metrics.CandidatesSeen)
	assert.Equal(t, 0, metrics.SpecGateRejected)
	assert.Equal(t, 0, metrics.SafetyGateRejected)
	assert.Equal(t, 0, metrics.HumanGateHeld)
}

func TestNewService_EnqueueAndClose(t *testing.T) {
	dir := t.TempDir()
	mdl := &stubModel{response: `{"skip_reason":"nothing useful"}`}

	svc := NewService(mdl,
		WithManagedSkillsDir(dir),
		WithReviewPolicy(alwaysReviewPolicy{}),
	)
	require.NotNil(t, svc)

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "test input"},
		model.Message{Role: model.RoleAssistant, Content: "test output"},
	)

	err := svc.EnqueueLearningJob(context.Background(), LearningJob{Session: sess})
	assert.NoError(t, err)

	err = svc.Close()
	assert.NoError(t, err)
}

func TestNewService_WritesSkill(t *testing.T) {
	dir := t.TempDir()
	mdl := &stubModel{response: `{
		"skills": [{
			"name": "Integration Skill",
			"description": "test",
			"when_to_use": "always",
			"steps": ["step 1"]
		}]
	}`}

	pub := &mockPublisher{}
	svc := NewService(mdl,
		WithManagedSkillsDir(dir),
		WithPublisher(pub),
		WithReviewPolicy(alwaysReviewPolicy{}),
	)

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "help"},
		model.Message{Role: model.RoleAssistant, Content: "done"},
	)

	require.NoError(t, svc.EnqueueLearningJob(context.Background(), LearningJob{Session: sess}))
	require.NoError(t, svc.Close())

	pub.mu.Lock()
	require.Len(t, pub.skills, 1)
	assert.Equal(t, "Integration Skill", pub.skills[0].Name)
	pub.mu.Unlock()
}

// Reviewer JSON containing a top-level "facts" key (legacy schema or a
// hallucinating model) must not break decoding or trigger any fact
// persistence -- evolution intentionally owns only the skill library.
func TestNewService_FactsKeyInResponseIsIgnored(t *testing.T) {
	mdl := &stubModel{response: `{
		"facts": [{"memory": "user prefers dark mode"}],
		"skills": [{
			"name": "demo skill",
			"description": "d",
			"when_to_use": "always",
			"steps": ["step"]
		}]
	}`}

	pub := &mockPublisher{}
	svc := NewService(mdl,
		WithManagedSkillsDir(t.TempDir()),
		WithPublisher(pub),
		WithReviewPolicy(alwaysReviewPolicy{}),
	)

	sess := newTestSession()
	addEvents(sess,
		model.Message{Role: model.RoleUser, Content: "I prefer dark mode"},
		model.Message{Role: model.RoleAssistant, Content: "noted"},
	)
	require.NoError(t, svc.EnqueueLearningJob(context.Background(), LearningJob{Session: sess}))
	require.NoError(t, svc.Close())

	pub.mu.Lock()
	require.Len(t, pub.skills, 1, "skills should still be published even when extra unknown keys are present")
	assert.Equal(t, "demo skill", pub.skills[0].Name)
	pub.mu.Unlock()
}

func TestDefaultReviewPolicy(t *testing.T) {
	p := DefaultReviewPolicy{}

	assertReviewPolicy(t, p, nil, false)
	assertReviewPolicy(t, p, &ReviewContext{}, false)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: 2,
	}, false)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: defaultMinToolCalls,
	}, true)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:          []model.Message{{Role: model.RoleUser, Content: "hi"}},
		HasUserCorrection: true,
	}, true)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:          []model.Message{{Role: model.RoleUser, Content: "hi"}},
		HasRecoveredError: true,
	}, true)
}

func TestDefaultReviewPolicy_CustomTriggers(t *testing.T) {
	p := DefaultReviewPolicy{
		MinToolCalls:                 10,
		DisableUserCorrectionTrigger: true,
		DisableRecoveredErrorTrigger: true,
	}

	assertReviewPolicy(t, p, &ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: 9,
	}, false)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: 10,
	}, true)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:          []model.Message{{Role: model.RoleUser, Content: "hi"}},
		HasUserCorrection: true,
	}, false)
	assertReviewPolicy(t, p, &ReviewContext{
		Messages:          []model.Message{{Role: model.RoleUser, Content: "hi"}},
		HasRecoveredError: true,
	}, false)
}

func TestDefaultReviewPolicy_DisableToolCallTrigger(t *testing.T) {
	p := DefaultReviewPolicy{MinToolCalls: -1}

	assertReviewPolicy(t, p, &ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: 100,
	}, false)
}

func TestDefaultReviewPolicy_ContextCancelled(t *testing.T) {
	p := DefaultReviewPolicy{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := p.ShouldReview(ctx, &ReviewPolicyInput{
		ReviewContext: &ReviewContext{
			Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
			ToolCallCount: defaultMinToolCalls,
		},
	})
	require.Error(t, err)
	assert.False(t, got)
}

func assertReviewPolicy(t *testing.T, p DefaultReviewPolicy, reviewCtx *ReviewContext, want bool) {
	t.Helper()

	got, err := p.ShouldReview(context.Background(), &ReviewPolicyInput{ReviewContext: reviewCtx})
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
