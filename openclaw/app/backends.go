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
	switch backend {
	case "", sessionBackendInMemory:
		serviceOpts := make([]sessioninmemory.ServiceOpt, 0, 1)
		if summarizer != nil {
			serviceOpts = append(
				serviceOpts,
				sessioninmemory.WithSummarizer(summarizer),
			)
		}
		return sessioninmemory.NewSessionService(serviceOpts...), nil
	case sessionBackendRedis:
		return newRedisSessionService(summarizer, opts)
	default:
		return nil, fmt.Errorf("unsupported session backend: %s", backend)
	}
}

func newRedisSessionService(
	summarizer summary.SessionSummarizer,
	opts runOptions,
) (session.Service, error) {
	sessionURL := strings.TrimSpace(opts.SessionRedisURL)
	instance := strings.TrimSpace(opts.SessionRedisInstance)
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
	keyPref := strings.TrimSpace(opts.SessionRedisKeyPref)
	if keyPref != "" {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithKeyPrefix(keyPref),
		)
	}
	if summarizer != nil {
		serviceOpts = append(
			serviceOpts,
			sessionredis.WithSummarizer(summarizer),
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
	switch backend {
	case "", memoryBackendInMemory:
		serviceOpts := make([]meminmemory.ServiceOpt, 0, 3)
		if opts.MemoryLimit > 0 {
			serviceOpts = append(
				serviceOpts,
				meminmemory.WithMemoryLimit(opts.MemoryLimit),
			)
		}
		if ext != nil {
			serviceOpts = append(
				serviceOpts,
				meminmemory.WithExtractor(ext),
			)
		}
		return meminmemory.NewMemoryService(serviceOpts...), nil
	case memoryBackendRedis:
		return newRedisMemoryService(ext, opts)
	default:
		return nil, fmt.Errorf("unsupported memory backend: %s", backend)
	}
}

func newRedisMemoryService(
	ext memextractor.MemoryExtractor,
	opts runOptions,
) (memory.Service, error) {
	memURL := strings.TrimSpace(opts.MemoryRedisURL)
	instance := strings.TrimSpace(opts.MemoryRedisInstance)
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
	keyPref := strings.TrimSpace(opts.MemoryRedisKeyPref)
	if keyPref != "" {
		serviceOpts = append(
			serviceOpts,
			memredis.WithKeyPrefix(keyPref),
		)
	}
	if opts.MemoryLimit > 0 {
		serviceOpts = append(
			serviceOpts,
			memredis.WithMemoryLimit(opts.MemoryLimit),
		)
	}
	if ext != nil {
		serviceOpts = append(serviceOpts, memredis.WithExtractor(ext))
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
