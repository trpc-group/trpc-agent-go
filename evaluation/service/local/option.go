package local

import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
)

// Option configures the local evaluation service.
type Option func(*Service)

// WithEvalSetManager overrides the manager used to retrieve eval sets.
func WithEvalSetManager(m evalset.Manager) Option {
    return func(s *Service) {
        if m != nil {
            s.evalSetManager = m
        }
    }
}

// WithEvalResultManager overrides the manager used to persist eval results.
// WithEvaluatorRegistry sets the evaluator registry used when scoring metrics.
func WithEvaluatorRegistry(r *evaluator.Registry) Option {
    return func(s *Service) {
        if r != nil {
            s.registry = r
        }
    }
}

// WithNow allows tests to inject a deterministic clock.
func WithNow(now func() time.Time) Option {
    return func(s *Service) {
        if now != nil {
            s.now = now
        }
    }
}

// WithSessionIDSupplier overrides how evaluation session IDs are generated.
func WithSessionIDSupplier(fn func() string) Option {
    return func(s *Service) {
        if fn != nil {
            s.sessionIDSupplier = fn
        }
    }
}
