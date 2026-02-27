//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memextractor "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func newSessionService(
	mdl model.Model,
	opts runOptions,
) (session.Service, error) {
	summarizer, err := newSessionSummarizer(mdl, opts)
	if err != nil {
		return nil, err
	}

	backend := strings.ToLower(strings.TrimSpace(opts.SessionBackend))
	if backend == "" {
		backend = sessionBackendInMemory
	}

	f, ok := registry.LookupSessionBackend(backend)
	if !ok {
		return nil, fmt.Errorf("unsupported session backend: %s", backend)
	}

	return f(
		registry.SessionDeps{
			Model:      mdl,
			Summarizer: summarizer,
			AppName:    opts.AppName,
		},
		registry.SessionBackendSpec{
			Type: backend,
			Redis: registry.RedisSpec{
				URL:       opts.SessionRedisURL,
				Instance:  opts.SessionRedisInstance,
				KeyPrefix: opts.SessionRedisKeyPref,
			},
			Config: opts.SessionConfig,
		},
	)
}

func newInMemorySessionBackend(
	deps registry.SessionDeps,
	_ registry.SessionBackendSpec,
) (session.Service, error) {
	serviceOpts := make([]sessioninmemory.ServiceOpt, 0, 1)
	if deps.Summarizer != nil {
		serviceOpts = append(
			serviceOpts,
			sessioninmemory.WithSummarizer(deps.Summarizer),
		)
	}
	return sessioninmemory.NewSessionService(serviceOpts...), nil
}

func newRedisSessionBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	sessionURL := strings.TrimSpace(spec.Redis.URL)
	instance := strings.TrimSpace(spec.Redis.Instance)
	if sessionURL == "" && instance == "" {
		return nil, errors.New(
			"session redis backend requires url or instance",
		)
	}

	serviceOpts := make([]sessionredis.ServiceOpt, 0, 3)
	if sessionURL != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithRedisClientURL(sessionURL),
		)
	}
	if instance != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithRedisInstance(instance),
		)
	}
	keyPref := strings.TrimSpace(spec.Redis.KeyPrefix)
	if keyPref != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithKeyPrefix(keyPref),
		)
	}
	if deps.Summarizer != nil {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithSummarizer(deps.Summarizer),
		)
	}
	return sessionredis.NewService(serviceOpts...)
}

func newMemoryService(
	mdl model.Model,
	opts runOptions,
) (memory.Service, error) {
	ext, err := newAutoMemoryExtractor(mdl, opts)
	if err != nil {
		return nil, err
	}

	backend := strings.ToLower(strings.TrimSpace(opts.MemoryBackend))
	if backend == "" {
		backend = memoryBackendInMemory
	}

	f, ok := registry.LookupMemoryBackend(backend)
	if !ok {
		return nil, fmt.Errorf("unsupported memory backend: %s", backend)
	}

	return f(
		registry.MemoryDeps{
			Model:     mdl,
			Extractor: ext,
			AppName:   opts.AppName,
		},
		registry.MemoryBackendSpec{
			Type: backend,
			Redis: registry.RedisSpec{
				URL:       opts.MemoryRedisURL,
				Instance:  opts.MemoryRedisInstance,
				KeyPrefix: opts.MemoryRedisKeyPref,
			},
			Limit:  opts.MemoryLimit,
			Config: opts.MemoryConfig,
		},
	)
}

func newInMemoryMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	serviceOpts := make([]meminmemory.ServiceOpt, 0, 3)
	if spec.Limit > 0 {
		serviceOpts = append(
			serviceOpts,
			meminmemory.WithMemoryLimit(spec.Limit),
		)
	}
	if deps.Extractor != nil {
		serviceOpts = append(
			serviceOpts,
			meminmemory.WithExtractor(deps.Extractor),
		)
	}
	return meminmemory.NewMemoryService(serviceOpts...), nil
}

func newRedisMemoryBackend(
	deps registry.MemoryDeps,
	spec registry.MemoryBackendSpec,
) (memory.Service, error) {
	memURL := strings.TrimSpace(spec.Redis.URL)
	instance := strings.TrimSpace(spec.Redis.Instance)
	if memURL == "" && instance == "" {
		return nil, errors.New(
			"memory redis backend requires url or instance",
		)
	}

	serviceOpts := make([]memredis.ServiceOpt, 0, 4)
	if memURL != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithRedisClientURL(memURL),
		)
	}
	if instance != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithRedisInstance(instance),
		)
	}
	keyPref := strings.TrimSpace(spec.Redis.KeyPrefix)
	if keyPref != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithKeyPrefix(keyPref),
		)
	}
	if spec.Limit > 0 {
		serviceOpts = append(
			serviceOpts,
			memredis.WithMemoryLimit(spec.Limit),
		)
	}
	if deps.Extractor != nil {
		serviceOpts = append(
			serviceOpts,
			memredis.WithExtractor(deps.Extractor),
		)
	}
	return memredis.NewService(serviceOpts...)
}

func newSessionSummarizer(
	mdl model.Model,
	opts runOptions,
) (summary.SessionSummarizer, error) {
	if !opts.SessionSummaryEnabled {
		return nil, nil
	}
	if mdl == nil {
		return nil, errors.New("session summary requires a model")
	}

	checks := make([]summary.Checker, 0, 3)
	if opts.SessionSummaryEventCount > 0 {
		checks = append(
			checks,
			summary.CheckEventThreshold(opts.SessionSummaryEventCount),
		)
	}
	if opts.SessionSummaryTokenCount > 0 {
		checks = append(
			checks,
			summary.CheckTokenThreshold(opts.SessionSummaryTokenCount),
		)
	}
	if opts.SessionSummaryIdleThreshold > 0 {
		checks = append(
			checks,
			summary.CheckTimeThreshold(opts.SessionSummaryIdleThreshold),
		)
	}

	if len(checks) == 0 {
		checks = append(
			checks,
			summary.CheckEventThreshold(defaultSessionSummaryEventThreshold),
		)
	}

	options := make([]summary.Option, 0, 3)
	options = append(options, summary.WithName(appName))
	if opts.SessionSummaryMaxWords > 0 {
		options = append(
			options,
			summary.WithMaxSummaryWords(opts.SessionSummaryMaxWords),
		)
	}

	policy, err := parseSummaryPolicy(opts.SessionSummaryPolicy)
	if err != nil {
		return nil, err
	}
	switch policy {
	case summaryPolicyAll:
		options = append(options, summary.WithChecksAll(checks...))
	case summaryPolicyAny:
		options = append(options, summary.WithChecksAny(checks...))
	default:
		return nil, fmt.Errorf("unsupported summary policy: %s", policy)
	}

	return summary.NewSummarizer(mdl, options...), nil
}

func parseSummaryPolicy(raw string) (string, error) {
	policy := strings.ToLower(strings.TrimSpace(raw))
	if policy == "" {
		return summaryPolicyAny, nil
	}
	switch policy {
	case summaryPolicyAny, summaryPolicyAll:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported summary policy: %s", raw)
	}
}

func newAutoMemoryExtractor(
	mdl model.Model,
	opts runOptions,
) (memextractor.MemoryExtractor, error) {
	if !opts.MemoryAutoEnabled {
		return nil, nil
	}
	if mdl == nil {
		return nil, errors.New("memory auto requires a model")
	}

	checks := make([]memextractor.Checker, 0, 2)
	if opts.MemoryAutoMessageThreshold > 0 {
		checks = append(
			checks,
			memextractor.CheckMessageThreshold(
				opts.MemoryAutoMessageThreshold,
			),
		)
	}
	if opts.MemoryAutoTimeInterval > 0 {
		checks = append(
			checks,
			memextractor.CheckTimeInterval(opts.MemoryAutoTimeInterval),
		)
	}
	if len(checks) == 0 {
		checks = append(
			checks,
			memextractor.CheckMessageThreshold(
				defaultMemoryAutoMessageThreshold,
			),
		)
	}

	policy, err := parseSummaryPolicy(opts.MemoryAutoPolicy)
	if err != nil {
		return nil, err
	}

	extOpts := make([]memextractor.Option, 0, 2)
	switch policy {
	case summaryPolicyAny:
		extOpts = append(
			extOpts,
			memextractor.WithCheckersAny(checks...),
		)
	case summaryPolicyAll:
		for _, check := range checks {
			extOpts = append(extOpts, memextractor.WithChecker(check))
		}
	default:
		return nil, fmt.Errorf("unsupported memory auto policy: %s", policy)
	}

	return memextractor.NewExtractor(mdl, extOpts...), nil
}
