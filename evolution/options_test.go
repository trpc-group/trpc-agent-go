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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestWithManagedSkillsDir(t *testing.T) {
	var opts serviceOpts
	WithManagedSkillsDir("/tmp/skills")(&opts)
	assert.Equal(t, "/tmp/skills", opts.managedSkillsDir)
}

func TestWithSkillRepository(t *testing.T) {
	repo := &mockSkillRepo{}
	var opts serviceOpts
	WithSkillRepository(repo)(&opts)
	assert.Equal(t, repo, opts.skillRepo)
}

func TestWithReviewPolicy(t *testing.T) {
	p := alwaysReviewPolicy{}
	var opts serviceOpts
	WithReviewPolicy(p)(&opts)
	assert.Equal(t, p, opts.reviewPolicy)
}

func TestWithPublisher(t *testing.T) {
	pub := &mockPublisher{}
	var opts serviceOpts
	WithPublisher(pub)(&opts)
	assert.Equal(t, pub, opts.publisher)
}

func TestWithWorkerNum(t *testing.T) {
	var opts serviceOpts
	WithWorkerNum(4)(&opts)
	assert.Equal(t, 4, opts.workerNum)
}

func TestWithQueueSize(t *testing.T) {
	var opts serviceOpts
	WithQueueSize(32)(&opts)
	assert.Equal(t, 32, opts.queueSize)
}

func TestWithExistingSkillBodyMaxChars(t *testing.T) {
	var opts serviceOpts
	WithExistingSkillBodyMaxChars(1024)(&opts)
	assert.Equal(t, 1024, opts.existingSkillBodyMaxChars)
}

func TestWithReviewerOptions(t *testing.T) {
	var opts serviceOpts
	opt := WithMessageContentMaxChars(500)
	WithReviewerOptions(opt)(&opts)
	require.Len(t, opts.reviewerOptions, 1)
	assert.True(t, opts.hasReviewerOptions)
}

func TestWithReviewerOptions_Appends(t *testing.T) {
	var opts serviceOpts
	WithReviewerOptions(WithMessageContentMaxChars(100))(&opts)
	WithReviewerOptions(WithMessageContentMaxChars(200))(&opts)
	assert.Len(t, opts.reviewerOptions, 2)
}

func TestWithReviewer(t *testing.T) {
	rev := &mockReviewer{}
	var opts serviceOpts
	WithReviewer(rev)(&opts)
	assert.Equal(t, rev, opts.customReviewer)
}

func TestWithCandidateStore(t *testing.T) {
	store := NewFileCandidateStore(t.TempDir())
	var opts serviceOpts
	WithCandidateStore(store)(&opts)
	assert.Equal(t, store, opts.candidateStore)
}

func TestWithCandidateStore_Nil(t *testing.T) {
	var opts serviceOpts
	WithCandidateStore(nil)(&opts)
	assert.Nil(t, opts.candidateStore)
}

func TestWithActivePointer(t *testing.T) {
	ptr := NewFileActivePointer(t.TempDir())
	var opts serviceOpts
	WithActivePointer(ptr)(&opts)
	assert.Equal(t, ptr, opts.activePointer)
}

func TestWithActivePointer_Nil(t *testing.T) {
	var opts serviceOpts
	WithActivePointer(nil)(&opts)
	assert.Nil(t, opts.activePointer)
}

func TestWithSpecGate(t *testing.T) {
	g := NewDefaultSpecGate()
	var opts serviceOpts
	WithSpecGate(g)(&opts)
	assert.Equal(t, g, opts.specGate)
}

func TestWithSafetyGate(t *testing.T) {
	g := NewDefaultSafetyGate()
	var opts serviceOpts
	WithSafetyGate(g)(&opts)
	assert.Equal(t, g, opts.safetyGate)
}

func TestWithEffectivenessGate(t *testing.T) {
	g := NewOutcomeBasedEffectivenessGate()
	var opts serviceOpts
	WithEffectivenessGate(g)(&opts)
	assert.Equal(t, g, opts.effectivenessGate)
}

func TestWithApprovalGateShadow(t *testing.T) {
	var opts serviceOpts
	WithApprovalGateShadow(true)(&opts)
	assert.True(t, opts.approvalGateShadow)

	WithApprovalGateShadow(false)(&opts)
	assert.False(t, opts.approvalGateShadow)
}

func TestWithHumanGate(t *testing.T) {
	g := &errorHumanGate{}
	var opts serviceOpts
	WithHumanGate(g)(&opts)
	assert.Equal(t, g, opts.humanGate)
}

func TestWithSkillScopeMode(t *testing.T) {
	var opts serviceOpts
	WithSkillScopeMode(skill.SkillScopeUser)(&opts)
	assert.Equal(t, skill.SkillScopeUser, opts.skillScopeMode)
}

func TestWithSkillRepositoryProvider(t *testing.T) {
	provider := skill.RepositoryProviderFunc(
		func(context.Context, skill.SkillScope) (skill.Repository, error) {
			return nil, nil
		},
	)
	var opts serviceOpts
	WithSkillRepositoryProvider(provider)(&opts)
	assert.NotNil(t, opts.skillRepoProvider)
}

func TestWithApprovalTimeout(t *testing.T) {
	var opts serviceOpts
	WithApprovalTimeout(2 * time.Hour)(&opts)
	assert.Equal(t, 2*time.Hour, opts.approvalTimeout)
}

func TestWithApprovalSweepInterval(t *testing.T) {
	var opts serviceOpts
	WithApprovalSweepInterval(15 * time.Minute)(&opts)
	assert.Equal(t, 15*time.Minute, opts.approvalSweepInterval)
}
