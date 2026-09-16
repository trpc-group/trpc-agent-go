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

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// service is the default Service implementation backed by an async worker.
type service struct {
	worker *worker
}

// NewService creates an evolution Service that uses reviewModel to evaluate
// session deltas and persists extracted skills as managed SKILL.md files.
func NewService(reviewModel model.Model, opts ...Option) Service {
	var o serviceOpts
	for _, fn := range opts {
		fn(&o)
	}

	var publisher Publisher
	publisherBaseDir := ""
	switch {
	case o.publisher != nil:
		publisher = o.publisher
	case o.managedSkillsDir != "":
		publisher = newFilePublisher(o.managedSkillsDir)
		publisherBaseDir = o.managedSkillsDir
	}

	var reviewer Reviewer
	if o.customReviewer != nil {
		reviewer = o.customReviewer
	} else {
		reviewer = NewLLMReviewer(reviewModel, o.reviewerOptions...)
	}

	w := newWorker(workerConfig{
		Reviewer:                  reviewer,
		Publisher:                 publisher,
		PublisherBaseDir:          publisherBaseDir,
		ReviewPolicy:              o.reviewPolicy,
		SkillRepo:                 o.skillRepo,
		SkillRepoProvider:         o.skillRepoProvider,
		SkillScopeMode:            o.skillScopeMode,
		WorkerNum:                 o.workerNum,
		QueueSize:                 o.queueSize,
		ExistingSkillBodyMaxChars: o.existingSkillBodyMaxChars,
		CandidateStore:            o.candidateStore,
		ActivePointer:             o.activePointer,
		SpecGate:                  o.specGate,
		SafetyGate:                o.safetyGate,
		EffectivenessGate:         o.effectivenessGate,
		HumanGate:                 o.humanGate,
		ApprovalGateShadow:        o.approvalGateShadow,
		ManagedSkillsDir:          o.managedSkillsDir,
		ApprovalTimeout:           o.approvalTimeout,
		ApprovalSweepInterval:     o.approvalSweepInterval,
	})
	w.Start()

	return &service{worker: w}
}

// EnqueueLearningJob implements Service. The job carries the session to
// review and an optional Outcome describing the evaluator verdict.
func (s *service) EnqueueLearningJob(ctx context.Context, job LearningJob) error {
	return s.worker.Enqueue(ctx, job)
}

// Close implements Service.
func (s *service) Close() error {
	s.worker.Stop()
	return nil
}

// ApprovalGateMetrics returns a JSON-friendly snapshot of quality-gate
// activity. It is safe to call after Close.
func (s *service) ApprovalGateMetrics() ApprovalGateMetrics {
	return s.worker.approvalGateMetrics()
}

// ApprovalGateMetricsProvider is an optional extension interface for
// services that expose quality-gate counters without exposing their
// worker implementation.
type ApprovalGateMetricsProvider interface {
	Service
	ApprovalGateMetrics() ApprovalGateMetrics
}
