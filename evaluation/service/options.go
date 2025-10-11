package service

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// Options holds the options for the evaluation service.
type Options struct {
	SessionService    session.Service                  // SessionService is used to store and retrieve session data.
	ArtifactService   artifact.Service                 // ArtifactService is used to store and retrieve artifact data.
	MemoryService     memory.Service                   // MemoryService is used to store and retrieve memory data.
	EvalSetManager    evalset.Manager                  // EvalSetManager is used to store and retrieve eval set data.
	EvalResultManager evalresult.Manager               // EvalResultManager is used to store and retrieve eval result data.
	EvaluatorRegistry registry.Registry                // EvaluatorRegistry is used to store and retrieve evaluator data.
	SessionIDSupplier func(ctx context.Context) string // SessionIDSupplier is used to generate session IDs.
}

// Option defines a function type for configuring the evaluation service.
type Option func(*Options)

// NewOptions creates a new Options with the default values.
func NewOptions(opt ...Option) *Options {
	opts := &Options{
		SessionService:    sessioninmemory.NewSessionService(),
		ArtifactService:   artifactinmemory.NewService(),
		MemoryService:     memoryinmemory.NewMemoryService(),
		EvalSetManager:    evalsetinmemory.New(),
		EvaluatorRegistry: registry.NewRegistry(),
		SessionIDSupplier: func(ctx context.Context) string {
			return uuid.New().String()
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithSessionService sets the session service.
// InMemory session service is used by default.
func WithSessionService(s session.Service) Option {
	return func(o *Options) {
		o.SessionService = s
	}
}

// WithArtifactService sets the artifact service.
// InMemory artifact service is used by default.
func WithArtifactService(s artifact.Service) Option {
	return func(o *Options) {
		o.ArtifactService = s
	}
}

// WithMemoryService sets the memory service.
// InMemory memory service is used by default.
func WithMemoryService(m memory.Service) Option {
	return func(o *Options) {
		o.MemoryService = m
	}
}

// WithEvalSetManager sets the eval set manager.
// InMemory eval set manager is used by default.
func WithEvalSetManager(m evalset.Manager) Option {
	return func(o *Options) {
		o.EvalSetManager = m
	}
}

// WithEvalResultManager sets the eval result manager.
// InMemory eval result manager is used by default.
func WithEvalResultManager(m evalresult.Manager) Option {
	return func(o *Options) {
		o.EvalResultManager = m
	}
}

// WithEvaluatorRegistry sets the evaluator registry.
// Default evaluator registry is used by default.
func WithEvaluatorRegistry(r registry.Registry) Option {
	return func(o *Options) {
		o.EvaluatorRegistry = r
	}
}

// WithSessionIDSupplier sets the function used to generate session IDs.
// UUID generator is used by default.
func WithSessionIDSupplier(s func(ctx context.Context) string) Option {
	return func(o *Options) {
		o.SessionIDSupplier = s
	}
}
