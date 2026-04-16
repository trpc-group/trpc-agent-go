//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package evaluation provides a reusable HTTP API server for online evaluation workflows.
package evaluation

import (
	"time"

	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

const (
	defaultBasePath    = "/evaluation"
	defaultSetsPath    = "/sets"
	defaultRunsPath    = "/runs"
	defaultResultsPath = "/results"
)

// Option configures the evaluation server.
type Option func(*options)

type options struct {
	appName           string
	basePath          string
	setsPath          string
	runsPath          string
	resultsPath       string
	timeout           time.Duration
	agentEvaluator    coreevaluation.AgentEvaluator
	evalSetManager    evalset.Manager
	evalResultManager evalresult.Manager
	routeRegistrars   []RouteRegistrar
}

func newOptions(opt ...Option) *options {
	opts := &options{
		basePath:    defaultBasePath,
		setsPath:    defaultSetsPath,
		runsPath:    defaultRunsPath,
		resultsPath: defaultResultsPath,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithAppName sets the app name used by the evaluation server.
func WithAppName(name string) Option {
	return func(opts *options) {
		opts.appName = name
	}
}

// WithBasePath sets the base path used by the evaluation server.
func WithBasePath(path string) Option {
	return func(opts *options) {
		opts.basePath = path
	}
}

// WithSetsPath sets the sets collection path relative to BasePath.
func WithSetsPath(path string) Option {
	return func(opts *options) {
		opts.setsPath = path
	}
}

// WithRunsPath sets the runs collection path relative to BasePath.
func WithRunsPath(path string) Option {
	return func(opts *options) {
		opts.runsPath = path
	}
}

// WithResultsPath sets the results collection path relative to BasePath.
func WithResultsPath(path string) Option {
	return func(opts *options) {
		opts.resultsPath = path
	}
}

// WithTimeout sets the maximum execution time for an online evaluation run.
func WithTimeout(timeout time.Duration) Option {
	return func(opts *options) {
		opts.timeout = timeout
	}
}

// WithAgentEvaluator sets the agent evaluator used by the evaluation server.
func WithAgentEvaluator(agentEvaluator coreevaluation.AgentEvaluator) Option {
	return func(opts *options) {
		opts.agentEvaluator = agentEvaluator
	}
}

// WithEvalSetManager sets the eval set manager used by the evaluation server.
func WithEvalSetManager(manager evalset.Manager) Option {
	return func(opts *options) {
		opts.evalSetManager = manager
	}
}

// WithEvalResultManager sets the eval result manager used by the evaluation server.
func WithEvalResultManager(manager evalresult.Manager) Option {
	return func(opts *options) {
		opts.evalResultManager = manager
	}
}

// WithRouteRegistrar appends a custom route registrar to the evaluation server.
func WithRouteRegistrar(registrar RouteRegistrar) Option {
	return func(opts *options) {
		opts.routeRegistrars = append(opts.routeRegistrars, registrar)
	}
}
