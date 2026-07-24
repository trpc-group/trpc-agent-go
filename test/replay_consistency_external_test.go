//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memmysql "trpc.group/trpc-go/trpc-agent-go/memory/mysql"
	mempostgres "trpc.group/trpc-go/trpc-agent-go/memory/postgres"
	memredis "trpc.group/trpc-go/trpc-agent-go/memory/redis"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessclickhouse "trpc.group/trpc-go/trpc-agent-go/session/clickhouse"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessmysql "trpc.group/trpc-go/trpc-agent-go/session/mysql"
	sesspostgres "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	sessredis "trpc.group/trpc-go/trpc-agent-go/session/redis"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	replayExternalTablePrefix = "trpc_replay"
	replayExternalMemoryTable = "trpc_replay_memories"
	replayExternalCaseTimeout = 30 * time.Second
)

type replayExternalServices struct {
	session      session.Service
	memory       memory.Service
	backendName  string
	capabilities map[string]replaytest.Capability
}

type replayServiceCloser interface {
	Close() error
}

func TestReplayConsistencyMiniRedisLocal(t *testing.T) {
	server := miniredis.RunT(t)
	services := newRedisReplayServices(t, "redis://"+server.Addr())
	services.capabilities[replaytest.CapabilityEventStateDeltaNull] = replaytest.Capability{
		Supported:   false,
		Reason:      "miniredis Lua/cjson emulation preserves the previous value for a null Event.StateDelta",
		AllowedDiff: true,
	}
	runExternalReplayCases(t, "miniredis", services)
}

func TestReplayConsistencyExternalBackends(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		services func(*testing.T, string) replayExternalServices
	}{
		{"redis", "TRPC_AGENT_REPLAY_REDIS_URL", newRedisReplayServices},
		{"postgres", "TRPC_AGENT_REPLAY_POSTGRES_DSN", newPostgresReplayServices},
		{"mysql", "TRPC_AGENT_REPLAY_MYSQL_DSN", newMySQLReplayServices},
		{"clickhouse", "TRPC_AGENT_REPLAY_CLICKHOUSE_DSN", newClickHouseReplayServices},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			endpoint := replayExternalEndpoint(t, test.env)
			services := test.services(t, endpoint)
			runExternalReplayCases(t, test.name, services)
		})
	}
}

func replayExternalEndpoint(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Skipf("%s is not set; skipping external replay backend", name)
	}
	return value
}

func runExternalReplayCases(
	t *testing.T,
	name string,
	external replayExternalServices,
) {
	t.Helper()
	backendName := external.backendName
	if backendName == "" {
		backendName = name
	}
	for _, replayCase := range replayCases() {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			key := session.Key{
				AppName:   uniqueID("replay-" + name + "-" + replayCase.Name),
				UserID:    "user",
				SessionID: "session",
			}
			baselineSession := sessinmemory.NewSessionService(
				sessinmemory.WithSummarizer(deterministicSummarizer{}),
				sessinmemory.WithSummaryFilterAllowlist(replaySummaryFilter),
				sessinmemory.WithCascadeFullSessionSummary(false),
			)
			baselineMemory := meminmemory.NewMemoryService()
			registerExternalReplayCleanup(t, baselineSession, baselineMemory)

			backends := []replaytest.Backend{
				{
					Name: "inmemory", Session: baselineSession, Memory: baselineMemory,
					SessionKey: key, Capabilities: replayExternalCapabilities(true),
				},
				{
					Name: backendName, Session: external.session, Memory: external.memory,
					SessionKey: key, Capabilities: external.capabilities,
				},
			}
			cleanupExternal := true
			t.Cleanup(func() {
				if cleanupExternal {
					cleanupExternalReplayBackend(t, backends[1])
				}
			})
			for i := range backends {
				switch replayCase.Name {
				case "summary_event_window_recovery":
					backends[i].Load = loadReplaySummaryWindow
				case "memory_search_order_and_score":
					backends[i].Load = loadReplayMemorySearch
				}
			}
			allowed := replayExternalAllowedDiffs(backendName, replayCase.Name, backends[1])
			ctx, cancel := context.WithTimeout(context.Background(), replayExternalCaseTimeout)
			defer cancel()
			report, err := (replaytest.Harness{
				Backends: backends, Normalizer: replaytest.DefaultNormalizer(),
				Allowed: allowed,
			}).Run(ctx, replayCase)
			require.NoError(t, err)
			if report.Inconclusive {
				require.NoError(t, validateAllowedReplaySkips(report))
				cleanupExternal = false
				t.Skipf(
					"backend %s skipped required capabilities: %v",
					backendName, report.SkippedBackends[backendName],
				)
			}
			require.False(t, replaytest.HasUnexpectedDiff(report), "diffs: %+v", report.Diffs)
			for _, rule := range allowed {
				require.True(t, replayReportContainsAllowedDiff(report, rule),
					"expected allowed diff for %s, got %+v", rule.Path, report.Diffs)
			}
		})
	}
}

func validateAllowedReplaySkips(report replaytest.CaseReport) error {
	if len(report.SkippedBackends) == 0 {
		return fmt.Errorf("inconclusive report has no skipped backends")
	}
	for backend, names := range report.SkippedBackends {
		capabilities, exists := report.Capabilities[backend]
		if !exists {
			return fmt.Errorf("skipped backend %q has no capability report", backend)
		}
		for _, name := range names {
			capability, declared := capabilities[name]
			if !declared {
				return fmt.Errorf("backend %q did not declare skipped capability %q", backend, name)
			}
			if capability.Supported {
				return fmt.Errorf("backend %q reported supported capability %q as skipped", backend, name)
			}
			if !capability.AllowedDiff {
				return fmt.Errorf("backend %q skipped disallowed capability %q", backend, name)
			}
		}
	}
	return nil
}

func replayExternalAllowedDiffs(
	backendName string,
	caseName string,
	backend replaytest.Backend,
) []replaytest.AllowedDiff {
	if replaytest.Supports(backend, replaytest.CapabilityEventStateDeltaNull) {
		return nil
	}
	path := ""
	switch caseName {
	case "state_update_overwrite_delete":
		path = "$.state.remove_me"
	case "failure_recovery_without_duplicates":
		path = "$.state.pending"
	default:
		return nil
	}
	return []replaytest.AllowedDiff{{
		Section: "state", Path: path, BackendA: "inmemory", BackendB: backendName,
		Reason: backend.Capabilities[replaytest.CapabilityEventStateDeltaNull].Reason,
	}}
}

func replayReportContainsAllowedDiff(
	report replaytest.CaseReport,
	rule replaytest.AllowedDiff,
) bool {
	for _, diff := range report.Diffs {
		if diff.Allowed && diff.Section == rule.Section && diff.Path == rule.Path {
			return true
		}
	}
	return false
}

func replayExternalCapabilities(tracks bool) map[string]replaytest.Capability {
	capabilities := map[string]replaytest.Capability{
		replaytest.CapabilityEvents:              {Supported: true},
		replaytest.CapabilityState:               {Supported: true},
		replaytest.CapabilityEventStateDeltaNull: {Supported: true},
		replaytest.CapabilityMemory:              {Supported: true},
		replaytest.CapabilitySummary:             {Supported: true},
		replaytest.CapabilityTracks:              {Supported: true},
	}
	if !tracks {
		capabilities[replaytest.CapabilityTracks] = replaytest.Capability{
			Supported:   false,
			Reason:      "session/clickhouse does not implement session.TrackService",
			AllowedDiff: true,
		}
	}
	return capabilities
}

func newRedisReplayServices(t *testing.T, endpoint string) replayExternalServices {
	t.Helper()
	summarizer := deterministicSummarizer{}
	sessionService, err := sessredis.NewService(
		sessredis.WithRedisClientURL(endpoint),
		sessredis.WithKeyPrefix(replayExternalTablePrefix),
		sessredis.WithCompatMode(sessredis.CompatModeNone),
		sessredis.WithSummarizer(summarizer),
		sessredis.WithSummaryFilterAllowlist(replaySummaryFilter),
		sessredis.WithCascadeFullSessionSummary(false),
	)
	require.NoError(t, err)
	memoryService, err := memredis.NewService(
		memredis.WithRedisClientURL(endpoint),
		memredis.WithKeyPrefix(replayExternalTablePrefix),
	)
	if err != nil {
		_ = sessionService.Close()
	}
	require.NoError(t, err)
	registerExternalReplayCleanup(t, sessionService, memoryService)
	return replayExternalServices{
		session: sessionService, memory: memoryService,
		capabilities: replayExternalCapabilities(true),
	}
}

func newPostgresReplayServices(t *testing.T, endpoint string) replayExternalServices {
	t.Helper()
	summarizer := deterministicSummarizer{}
	sessionService, err := sesspostgres.NewService(
		sesspostgres.WithPostgresClientDSN(endpoint),
		sesspostgres.WithTablePrefix(replayExternalTablePrefix),
		sesspostgres.WithSoftDelete(false),
		sesspostgres.WithSummarizer(summarizer),
		sesspostgres.WithSummaryFilterAllowlist(replaySummaryFilter),
		sesspostgres.WithCascadeFullSessionSummary(false),
	)
	require.NoError(t, err)
	memoryService, err := mempostgres.NewService(
		mempostgres.WithPostgresClientDSN(endpoint),
		mempostgres.WithTableName(replayExternalMemoryTable),
		mempostgres.WithSoftDelete(false),
	)
	if err != nil {
		_ = sessionService.Close()
	}
	require.NoError(t, err)
	registerExternalReplayCleanup(t, sessionService, memoryService)
	return replayExternalServices{
		session: sessionService, memory: memoryService,
		capabilities: replayExternalCapabilities(true),
	}
}

func newMySQLReplayServices(t *testing.T, endpoint string) replayExternalServices {
	t.Helper()
	summarizer := deterministicSummarizer{}
	sessionService, err := sessmysql.NewService(
		sessmysql.WithMySQLClientDSN(endpoint),
		sessmysql.WithTablePrefix(replayExternalTablePrefix),
		sessmysql.WithSoftDelete(false),
		sessmysql.WithSummarizer(summarizer),
		sessmysql.WithSummaryFilterAllowlist(replaySummaryFilter),
		sessmysql.WithCascadeFullSessionSummary(false),
	)
	require.NoError(t, err)
	memoryService, err := memmysql.NewService(
		memmysql.WithMySQLClientDSN(endpoint),
		memmysql.WithTableName(replayExternalMemoryTable),
		memmysql.WithSoftDelete(false),
	)
	if err != nil {
		_ = sessionService.Close()
	}
	require.NoError(t, err)
	registerExternalReplayCleanup(t, sessionService, memoryService)
	return replayExternalServices{
		session: sessionService, memory: memoryService,
		capabilities: replayExternalCapabilities(true),
	}
}

func newClickHouseReplayServices(t *testing.T, endpoint string) replayExternalServices {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("output_format_native_write_json_as_string", "1")
	query.Set("output_format_json_quote_64bit_integers", "0")
	parsed.RawQuery = query.Encode()
	summarizer := deterministicSummarizer{}
	sessionService, err := sessclickhouse.NewService(
		sessclickhouse.WithClickHouseDSN(parsed.String()),
		sessclickhouse.WithTablePrefix(replayExternalTablePrefix),
		sessclickhouse.WithSummarizer(summarizer),
		sessclickhouse.WithSummaryFilterAllowlist(replaySummaryFilter),
		sessclickhouse.WithCascadeFullSessionSummary(false),
	)
	require.NoError(t, err)
	memoryService := meminmemory.NewMemoryService()
	registerExternalReplayCleanup(t, sessionService, memoryService)
	return replayExternalServices{
		session: sessionService, memory: memoryService,
		backendName:  "clickhouse-session+inmemory-memory",
		capabilities: replayClickHouseCapabilities(),
	}
}

func replayClickHouseCapabilities() map[string]replaytest.Capability {
	capabilities := replayExternalCapabilities(false)
	capabilities[replaytest.CapabilityEvents] = replaytest.Capability{
		Supported:   false,
		Reason:      "session/clickhouse JSON columns do not preserve opaque event JSON semantics",
		AllowedDiff: true,
	}
	capabilities[replaytest.CapabilityEventStateDeltaNull] = replaytest.Capability{
		Supported:   false,
		Reason:      "session/clickhouse JSON columns do not preserve explicit null values",
		AllowedDiff: true,
	}
	capabilities[replaytest.CapabilitySummary] = replaytest.Capability{
		Supported:   false,
		Reason:      "session/clickhouse cannot round-trip persisted summaries with the current JSON schema and driver",
		AllowedDiff: true,
	}
	return capabilities
}

func registerExternalReplayCleanup(
	t *testing.T,
	sessionService replayServiceCloser,
	memoryService replayServiceCloser,
) {
	t.Helper()
	t.Cleanup(func() {
		if err := memoryService.Close(); err != nil {
			t.Errorf("close replay memory service: %v", err)
		}
		if err := sessionService.Close(); err != nil {
			t.Errorf("close replay session service: %v", err)
		}
	})
}

func cleanupExternalReplayBackend(t *testing.T, backend replaytest.Backend) {
	t.Helper()
	if replaytest.Supports(backend, replaytest.CapabilityMemory) && backend.Memory != nil {
		runExternalReplayCleanupStep(t, "clear replay memories", func(ctx context.Context) error {
			return backend.Memory.ClearMemories(ctx, replayMemoryUser(backend))
		})
	}
	runExternalReplayCleanupStep(t, "delete replay session", func(ctx context.Context) error {
		return backend.Session.DeleteSession(ctx, backend.SessionKey)
	})

	var appState session.StateMap
	runExternalReplayCleanupStep(t, "list replay app state", func(ctx context.Context) error {
		var err error
		appState, err = backend.Session.ListAppStates(ctx, backend.SessionKey.AppName)
		return err
	})
	for key := range appState {
		key := key
		runExternalReplayCleanupStep(t, "delete replay app state "+key, func(ctx context.Context) error {
			return backend.Session.DeleteAppState(ctx, backend.SessionKey.AppName, key)
		})
	}
	userKey := session.UserKey{
		AppName: backend.SessionKey.AppName,
		UserID:  backend.SessionKey.UserID,
	}
	var userState session.StateMap
	runExternalReplayCleanupStep(t, "list replay user state", func(ctx context.Context) error {
		var err error
		userState, err = backend.Session.ListUserStates(ctx, userKey)
		return err
	})
	for key := range userState {
		key := key
		runExternalReplayCleanupStep(t, "delete replay user state "+key, func(ctx context.Context) error {
			return backend.Session.DeleteUserState(ctx, userKey, key)
		})
	}
}

func runExternalReplayCleanupStep(
	t *testing.T,
	operation string,
	run func(context.Context) error,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := run(ctx); err != nil {
		t.Errorf("%s: %v", operation, err)
	}
}
