package evaluation

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
)

type options struct {
	evalService       service.Service
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	registry          evaluator.Registry
	numRuns           int
	evalMetrics       []*evalset.EvalMetric
}

func newOptions(opt ...Option) *options {
	opts := &options{
		numRuns:           1,
		evalSetManager:    evalsetinmemory.New(),
		evalResultManager: evalresultinmemory.NewManager(),
		registry:          evaluator.NewRegistry(),
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

type Option func(*options)

func WithEvaluationService(s service.Service) Option {
	return func(o *options) {
		o.evalService = s
	}
}

func WithEvalSetManager(m evalset.Manager) Option {
	return func(o *options) {
		o.evalSetManager = m
	}
}

func WithEvalResultManager(m evalresult.Manager) Option {
	return func(o *options) {
		o.evalResultManager = m
	}
}

func WithEvaluatorRegistry(r evaluator.Registry) Option {
	return func(o *options) {
		o.registry = r
	}
}

func WithNumRuns(numRuns int) Option {
	return func(o *options) {
		if numRuns > 0 {
			o.numRuns = numRuns
		}
	}
}

func WithEvalMetrics(metrics []*evalset.EvalMetric) Option {
	return func(o *options) {
		o.evalMetrics = append(o.evalMetrics, metrics...)
	}
}
