package service

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemeory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type Options struct {
	SessionService    session.Service
	ArtifactService   artifact.Service
	MemoryService     memory.Service
	EvalSetManager    evalset.Manager
	EvalResultManager evalresult.Manager
	Registry          evaluator.Registry
	SessionIDSupplier func(ctx context.Context) string
}

// Option configures the local evaluation service.
type Option func(*Options)

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SessionService:  sessioninmemory.NewSessionService(),
		ArtifactService: artifactinmemory.NewService(),
		MemoryService:   memoryinmemory.NewMemoryService(),
		EvalSetManager:  evalsetinmemory.New(),
		Registry:        evaluator.NewRegistry(),
		SessionIDSupplier: func(ctx context.Context) string {
			return uuid.New().String()
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithSessionService overrides the session service used when running evals.
func WithSessionService(s session.Service) Option {
	return func(o *Options) {
		o.SessionService = s
	}
}

// WithArtifactService overrides the artifact service used during inference.
func WithArtifactService(s artifact.Service) Option {
	return func(o *Options) {
		o.ArtifactService = s
	}
}

// WithMemoryService overrides the memory service used during inference.
func WithMemoryService(m memory.Service) Option {
	return func(o *Options) {
		o.MemoryService = m
	}
}

// WithEvalSetManager overrides the manager used to retrieve eval sets.
func WithEvalSetManager(m evalset.Manager) Option {
	return func(o *Options) {
		o.EvalSetManager = m
	}
}

// WithEvalResultManager overrides the manager used to retrieve eval results.
func WithEvalResultManager(m evalresult.Manager) Option {
	return func(o *Options) {
		o.EvalResultManager = m
	}
}

// WithEvaluatorRegistry sets the evaluator registry used when scoring metrics.
func WithEvaluatorRegistry(r evaluator.Registry) Option {
	return func(o *Options) {
		o.Registry = r
	}
}

// WithSessionIDSupplier overrides the function used to generate session IDs.
func WithSessionIDSupplier(s func(ctx context.Context) string) Option {
	return func(o *Options) {
		o.SessionIDSupplier = s
	}
}
