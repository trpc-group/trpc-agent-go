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
		WithPolicy(alwaysPolicy{}),
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
		WithPolicy(alwaysPolicy{}),
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
		WithPolicy(alwaysPolicy{}),
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
		WithPolicy(alwaysPolicy{}),
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

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy{}

	assert.False(t, p.ShouldReview(nil))
	assert.False(t, p.ShouldReview(&ReviewContext{}))
	assert.False(t, p.ShouldReview(&ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: 2,
	}))
	assert.True(t, p.ShouldReview(&ReviewContext{
		Messages:      []model.Message{{Role: model.RoleUser, Content: "hi"}},
		ToolCallCount: 4,
	}))
	assert.True(t, p.ShouldReview(&ReviewContext{
		Messages:          []model.Message{{Role: model.RoleUser, Content: "hi"}},
		HasUserCorrection: true,
	}))
	assert.True(t, p.ShouldReview(&ReviewContext{
		Messages:          []model.Message{{Role: model.RoleUser, Content: "hi"}},
		HasRecoveredError: true,
	}))
}
