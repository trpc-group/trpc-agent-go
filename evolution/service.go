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

// service is the default Service implementation backed by an async Worker.
type service struct {
	worker *Worker
}

// NewService creates an evolution Service that uses reviewModel to evaluate
// session deltas and persists extracted skills as managed SKILL.md files.
func NewService(reviewModel model.Model, opts ...Option) Service {
	var o serviceOpts
	for _, fn := range opts {
		fn(&o)
	}

	var publisher Publisher
	switch {
	case o.publisher != nil:
		publisher = o.publisher
	case o.managedSkillsDir != "":
		publisher = NewFilePublisher(o.managedSkillsDir)
	}

	var reviewer Reviewer
	if o.customReviewer != nil {
		reviewer = o.customReviewer
	} else {
		reviewer = NewLLMReviewer(reviewModel, o.reviewerOptions...)
	}

	w := NewWorker(WorkerConfig{
		Reviewer:                  reviewer,
		Publisher:                 publisher,
		Policy:                    o.policy,
		SkillRepo:                 o.skillRepo,
		WorkerNum:                 o.workerNum,
		QueueSize:                 o.queueSize,
		ExistingSkillBodyMaxChars: o.existingSkillBodyMaxChars,
		CandidateStore:            o.candidateStore,
		ActivePointer:             o.activePointer,
		SpecGate:                  o.specGate,
		SafetyGate:                o.safetyGate,
		EffectivenessGate:         o.effectivenessGate,
		ApprovalGateShadow:        o.approvalGateShadow,
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

// Worker returns the underlying async worker. Exported so adopters
// can read approval-gate metrics (and nothing else) after Close
// without racing the worker goroutine. Callers MUST NOT mutate the
// returned worker's config; treat it as read-only.
func (s *service) Worker() *Worker { return s.worker }

// ServiceWithWorker is an optional extension interface that exposes
// the underlying Worker for read-only introspection. The default
// service implementation satisfies it; alternative Service
// implementations MAY satisfy it if they want to expose gate metrics
// too.
type ServiceWithWorker interface {
	Service
	Worker() *Worker
}
