//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memextractor "trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	redisstorage "trpc.group/trpc-go/trpc-agent-go/storage/redis"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestParseSummaryPolicy(t *testing.T) {
	t.Parallel()

	policy, err := parseSummaryPolicy("")
	require.NoError(t, err)
	require.Equal(t, summaryPolicyAny, policy)

	policy, err = parseSummaryPolicy("ALL")
	require.NoError(t, err)
	require.Equal(t, summaryPolicyAll, policy)

	_, err = parseSummaryPolicy("nope")
	require.Error(t, err)
}

func TestNewSessionSummarizer_DefaultThreshold(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)

	sess := session.NewSession("app", "user", "sess")
	sess.Events = append(sess.Events, event.Event{
		Timestamp: time.Now(),
	})
	require.False(t, summarizer.ShouldSummarize(sess))
}

func TestNewSessionSummarizer_AutoMode(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled: true,
		SessionSummaryMode:    "auto",
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)

	// Use many empty events: manual-default event threshold (20) would
	// trigger, but auto token-threshold should still stay false because
	// the events carry negligible tokens.
	sess := session.NewSession("app", "user", "sess")
	for i := 0; i < 50; i++ {
		sess.Events = append(sess.Events, event.Event{
			Timestamp: time.Now(),
		})
	}
	require.False(t, summarizer.ShouldSummarize(sess))
}

func TestNewSessionSummarizer_ManualMode(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled:    true,
		SessionSummaryMode:       "manual",
		SessionSummaryEventCount: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)

	// With event_threshold=1, 2 events should trigger.
	sess := session.NewSession("app", "user", "sess")
	for i := 0; i < 2; i++ {
		sess.Events = append(sess.Events, event.Event{
			Timestamp: time.Now(),
		})
	}
	require.True(t, summarizer.ShouldSummarize(sess))
}

func TestNewSessionSummarizer_ManualTokenThreshold(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled:    true,
		SessionSummaryMode:       "manual",
		SessionSummaryTokenCount: 999999,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)

	sess := session.NewSession("app", "user", "sess")
	sess.Events = append(sess.Events, event.Event{
		Timestamp: time.Now(),
	})
	require.False(t, summarizer.ShouldSummarize(sess))
}

func TestNewSessionSummarizer_ManualIdleThreshold(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled:       true,
		SessionSummaryMode:          "manual",
		SessionSummaryIdleThreshold: time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)
}

func TestNewSessionSummarizer_ManualMaxWords(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled:    true,
		SessionSummaryMaxWords:   200,
		SessionSummaryEventCount: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)
}

func TestNewSessionSummarizer_ManualPolicyAll(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	summarizer, err := newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled:    true,
		SessionSummaryPolicy:     "all",
		SessionSummaryEventCount: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, summarizer)
}

func TestNewSessionSummarizer_InvalidPolicy(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled: true,
		SessionSummaryPolicy:  "nope",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported summary policy")
}

func TestNewSessionSummarizer_RequiresModel(t *testing.T) {
	t.Parallel()

	_, err := newSessionSummarizer(nil, runOptions{
		SessionSummaryEnabled: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a model")
}

func TestNewSessionSummarizer_Disabled(t *testing.T) {
	t.Parallel()

	summarizer, err := newSessionSummarizer(nil, runOptions{
		SessionSummaryEnabled: false,
	})
	require.NoError(t, err)
	require.Nil(t, summarizer)
}

func TestNewSessionSummarizer_InvalidMode(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = newSessionSummarizer(mdl, runOptions{
		SessionSummaryEnabled: true,
		SessionSummaryMode:    "invalid",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported session summary mode")
}

func TestNewAutoMemoryExtractor_RequiresModel(t *testing.T) {
	t.Parallel()

	_, err := newAutoMemoryExtractor(nil, runOptions{
		MemoryAutoEnabled: true,
	})
	require.Error(t, err)
}

func TestNewAutoMemoryExtractor_NoCheckers(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	ext, err := newAutoMemoryExtractor(mdl, runOptions{
		MemoryAutoEnabled: true,
	})
	require.NoError(t, err)
	require.NotNil(t, ext)

	// Without checkers, ShouldExtract always returns true.
	ctx := &memextractor.ExtractionContext{
		Messages: make([]model.Message, 1),
	}
	require.True(t, ext.ShouldExtract(ctx))
}

func TestNewAutoMemoryExtractor_PolicyAll(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	ext, err := newAutoMemoryExtractor(mdl, runOptions{
		MemoryAutoEnabled:          true,
		MemoryAutoPolicy:           summaryPolicyAll,
		MemoryAutoMessageThreshold: 1,
		MemoryAutoTimeInterval:     time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, ext)

	now := time.Now()
	ctx := &memextractor.ExtractionContext{
		Messages:      make([]model.Message, 2),
		LastExtractAt: &now,
	}
	require.False(t, ext.ShouldExtract(ctx))

	old := now.Add(-2 * time.Hour)
	ctx.LastExtractAt = &old
	require.True(t, ext.ShouldExtract(ctx))
}

func TestNewAutoMemoryExtractor_PolicyAny(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	ext, err := newAutoMemoryExtractor(mdl, runOptions{
		MemoryAutoEnabled:          true,
		MemoryAutoPolicy:           summaryPolicyAny,
		MemoryAutoMessageThreshold: 2,
		MemoryAutoTimeInterval:     time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, ext)

	now := time.Now()
	ctx := &memextractor.ExtractionContext{
		Messages:      make([]model.Message, 3),
		LastExtractAt: &now,
	}
	// Message threshold is met.
	require.True(t, ext.ShouldExtract(ctx))
}

func TestNewAutoMemoryExtractor_InvalidPolicy(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	// Invalid policy without checkers still returns error.
	_, err = newAutoMemoryExtractor(mdl, runOptions{
		MemoryAutoEnabled: true,
		MemoryAutoPolicy:  "invalid",
	})
	require.Error(t, err)

	// Invalid policy with checkers also returns error.
	_, err = newAutoMemoryExtractor(mdl, runOptions{
		MemoryAutoEnabled:          true,
		MemoryAutoPolicy:           "invalid",
		MemoryAutoMessageThreshold: 1,
	})
	require.Error(t, err)
}

func TestNewSessionService_RedisRequiresConfig(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	_, err = newSessionService(mdl, runOptions{
		SessionBackend: sessionBackendRedis,
	})
	require.Error(t, err)
}

func TestNewMemoryService_RedisRequiresConfig(t *testing.T) {
	t.Parallel()

	_, err := newMemoryService(nil, runOptions{
		MemoryBackend: memoryBackendRedis,
	})
	require.Error(t, err)
}

func TestNewMemoryService_FileDisablesStructuredMemory(t *testing.T) {
	t.Parallel()

	svc, err := newMemoryService(nil, runOptions{
		MemoryBackend:     memoryBackendFile,
		MemoryAutoEnabled: true,
	})
	require.NoError(t, err)
	require.Nil(t, svc)
}

func TestNewDisabledMemoryBackend_ReturnsNilService(t *testing.T) {
	t.Parallel()

	svc, err := newDisabledMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.NoError(t, err)
	require.Nil(t, svc)
}

func TestNewMemoryService_OffIsUnsupported(t *testing.T) {
	t.Parallel()

	_, err := newMemoryService(nil, runOptions{
		MemoryBackend: "off",
	})
	require.ErrorContains(t, err, "unsupported memory backend: off")
}

func TestNewBackends_Redis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	url := "redis://" + mr.Addr()
	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	opts := runOptions{
		SessionBackend:  sessionBackendRedis,
		SessionRedisURL: url,
		MemoryBackend:   memoryBackendRedis,
		MemoryRedisURL:  url,
	}

	sessionSvc, err := newSessionService(mdl, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sessionSvc.Close() })

	memSvc, err := newMemoryService(mdl, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = memSvc.Close() })
}

func TestNewBackends_RedisInstance(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	const instanceName = "test_redis_instance"
	redisstorage.RegisterRedisInstance(
		instanceName,
		redisstorage.WithClientBuilderURL("redis://"+mr.Addr()),
	)

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	opts := runOptions{
		SessionBackend:       sessionBackendRedis,
		SessionRedisInstance: instanceName,
		SessionRedisKeyPref:  "sess:",
		MemoryBackend:        memoryBackendRedis,
		MemoryRedisInstance:  instanceName,
		MemoryRedisKeyPref:   "mem:",
		MemoryLimit:          3,
	}

	sessionSvc, err := newSessionService(mdl, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sessionSvc.Close() })

	memSvc, err := newMemoryService(mdl, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = memSvc.Close() })
}

func TestNewBackends_InMemoryWithSummarizerAndExtractor(t *testing.T) {
	t.Parallel()

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	opts := runOptions{
		SessionSummaryEnabled: true,
		MemoryAutoEnabled:     true,
		MemoryLimit:           2,
	}

	sessionSvc, err := newSessionService(mdl, opts)
	require.NoError(t, err)
	require.NotNil(t, sessionSvc)
	t.Cleanup(func() { _ = sessionSvc.Close() })

	memSvc, err := newMemoryService(mdl, opts)
	require.NoError(t, err)
	require.NotNil(t, memSvc)
	t.Cleanup(func() { _ = memSvc.Close() })
}

func TestNewSessionService_CustomBackendUsesConfig(t *testing.T) {
	const backendName = "test_session_backend"

	var gotNote string
	require.NoError(t, registry.RegisterSessionBackend(
		backendName,
		func(
			_ registry.SessionDeps,
			spec registry.SessionBackendSpec,
		) (session.Service, error) {
			var cfg struct {
				Note string `yaml:"note"`
			}
			if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
				return nil, err
			}
			gotNote = cfg.Note
			return sessioninmemory.NewSessionService(), nil
		},
	))

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("note: hello"), &node))

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	svc, err := newSessionService(mdl, runOptions{
		AppName:        "demo",
		SessionBackend: backendName,
		SessionConfig:  &node,
	})
	require.NoError(t, err)
	require.NotNil(t, svc)
	t.Cleanup(func() { _ = svc.Close() })

	require.Equal(t, "hello", gotNote)
}

func TestNewMemoryService_CustomBackendUsesConfig(t *testing.T) {
	const backendName = "test_memory_backend"

	var gotNote string
	require.NoError(t, registry.RegisterMemoryBackend(
		backendName,
		func(
			_ registry.MemoryDeps,
			spec registry.MemoryBackendSpec,
		) (memory.Service, error) {
			var cfg struct {
				Note string `yaml:"note"`
			}
			if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
				return nil, err
			}
			gotNote = cfg.Note
			return meminmemory.NewMemoryService(), nil
		},
	))

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte("note: hello"), &node))

	mdl, err := modelFromOptions(runOptions{ModelMode: modeMock})
	require.NoError(t, err)

	svc, err := newMemoryService(mdl, runOptions{
		AppName:       "demo",
		MemoryBackend: backendName,
		MemoryConfig:  &node,
	})
	require.NoError(t, err)
	require.NotNil(t, svc)
	t.Cleanup(func() { _ = svc.Close() })

	require.Equal(t, "hello", gotNote)
}

func TestSummaryToolResultFormatter(t *testing.T) {
	t.Parallel()

	t.Run("empty content", func(t *testing.T) {
		msg := model.Message{ToolName: "web_fetch", Content: ""}
		require.Equal(t, "", summaryToolResultFormatter(msg))
	})

	t.Run("short content", func(t *testing.T) {
		msg := model.Message{ToolName: "web_fetch", Content: "hello"}
		require.Equal(t, "[web_fetch returned: hello]", summaryToolResultFormatter(msg))
	})

	t.Run("fallback tool name", func(t *testing.T) {
		msg := model.Message{Content: "result"}
		require.Equal(t, "[tool returned: result]", summaryToolResultFormatter(msg))
	})

	t.Run("truncates large content", func(t *testing.T) {
		long := strings.Repeat("x", summaryToolResultMaxRunes+500)
		msg := model.Message{ToolName: "web_fetch", Content: long}
		result := summaryToolResultFormatter(msg)
		require.Contains(t, result, "... [truncated]")
		runes := []rune(result)
		require.Less(t, len(runes), summaryToolResultMaxRunes+100)
	})

	t.Run("truncates CJK at rune boundary", func(t *testing.T) {
		long := strings.Repeat("中", summaryToolResultMaxRunes+100)
		msg := model.Message{ToolName: "search", Content: long}
		result := summaryToolResultFormatter(msg)
		require.Contains(t, result, "... [truncated]")
		require.True(t, len([]rune(result)) < summaryToolResultMaxRunes+100)
	})
}
