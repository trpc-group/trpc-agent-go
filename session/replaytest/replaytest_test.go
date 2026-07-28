//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

type staticSummarizer struct{}

func (staticSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (staticSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	events := sess.GetEvents()
	if len(events) == 0 || events[len(events)-1].Response == nil ||
		len(events[len(events)-1].Choices) == 0 {
		return "replay summary: empty", nil
	}
	return fmt.Sprintf("replay summary: %s", events[len(events)-1].Choices[0].Message.Content), nil
}

func (staticSummarizer) SetPrompt(string) {}

func (staticSummarizer) SetModel(model.Model) {}

func (staticSummarizer) Metadata() map[string]any { return nil }

var _ summary.SessionSummarizer = staticSummarizer{}

func TestStandardCasesDetectInjectedDifferences(t *testing.T) {
	for _, replayCase := range StandardCases() {
		t.Run(replayCase.Name, func(t *testing.T) {
			backend := newInMemoryBackend(t)
			baseline, err := replayCase.Replay(context.Background(), backend)
			require.NoError(t, err)
			actual := cloneSnapshot(t, baseline)
			actual.Data["injected_mismatch"] = replayCase.Name

			diffs := Compare(replayCase.Name, "injected", baseline, actual)
			require.Len(t, diffs, 1)
			require.Equal(t, "data.injected_mismatch", diffs[0].Path)
		})
	}
}

func TestCaptureNormalizesVolatileFieldsAndPreservesMemoryID(t *testing.T) {
	backend := newInMemoryBackend(t)
	key := session.Key{AppName: replayApp, UserID: replayUser, SessionID: "normalize"}
	sess, err := backend.Session.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	require.NoError(t, backend.Session.AppendEvent(context.Background(), sess,
		newReplayEvent("generated-id", model.RoleUser, "hello", "")))
	require.NoError(t, backend.Memory.AddMemory(context.Background(), memory.UserKey{
		AppName: replayApp,
		UserID:  replayUser,
	}, "keep memory identity", nil))

	snapshot, err := Capture(context.Background(), backend, key)
	require.NoError(t, err)
	events := snapshot.Data["events"].([]any)
	require.NotContains(t, events[0].(map[string]any), "id")
	memories := snapshot.Data["memories"].([]any)
	require.NotEmpty(t, memories[0].(map[string]any)["id"])
	scope := memories[0].(map[string]any)["scope"].(map[string]any)
	require.Equal(t, replayApp, scope["app_name"])
	require.Equal(t, replayUser, scope["user_id"])
}

func TestRunValidatesBackends(t *testing.T) {
	_, err := Run(context.Background(), []Backend{{Name: "only"}}, StandardCases())
	require.EqualError(t, err, "replay requires at least two backends")
}

func TestSummaryDifferencesIdentifyLossOverwriteScopeAndSession(t *testing.T) {
	backend := newInMemoryBackend(t)
	var summaryCase Case
	for _, replayCase := range StandardCases() {
		if replayCase.Name == "summary_update" {
			summaryCase = replayCase
			break
		}
	}
	baseline, err := summaryCase.Replay(context.Background(), backend)
	require.NoError(t, err)
	summaries := baseline.Data["summaries"].(map[string]any)
	require.Contains(t, summaries, "branch-a")
	summary := summaries["branch-a"].(map[string]any)
	require.Equal(t, "replay summary: add this too", summary["summary"])
	require.Equal(t, "normalized", summary["updated_at"])
	boundary := summary["boundary"].(map[string]any)
	require.Equal(t, float64(session.SummaryBoundaryVersion), boundary["version"])
	require.Equal(t, "branch-a", boundary["filter_key"])

	lost := cloneSnapshot(t, baseline)
	delete(lost.Data["summaries"].(map[string]any), "branch-a")
	require.NotEmpty(t, Compare("summary_update", "injected", baseline, lost))

	overwritten := cloneSnapshot(t, baseline)
	overwritten.Data["summaries"].(map[string]any)["branch-a"].(map[string]any)["summary"] = "wrong summary"
	require.NotEmpty(t, Compare("summary_update", "injected", baseline, overwritten))

	wrongScope := cloneSnapshot(t, baseline)
	wrongScope.Data["summaries"].(map[string]any)["other-branch"] = wrongScope.Data["summaries"].(map[string]any)["branch-a"]
	delete(wrongScope.Data["summaries"].(map[string]any), "branch-a")
	require.NotEmpty(t, Compare("summary_update", "injected", baseline, wrongScope))

	wrongSession := cloneSnapshot(t, baseline)
	wrongSession.SessionID = "another-session"
	diffs := Compare("summary_update", "injected", baseline, wrongSession)
	require.Equal(t, "session_id", diffs[0].Path)
}

func TestUnsupportedFieldsAreReportedAsAllowedDifferences(t *testing.T) {
	baseline := Snapshot{SessionID: "s", Data: map[string]any{"tracks": map[string]any{"tool": "present"}}}
	actual := Snapshot{SessionID: "s", Data: map[string]any{"tracks": map[string]any{}}}
	differences := Compare("tracks", "limited", baseline, actual)
	markAllowedUnsupported(differences, []Unsupported{{Path: "tracks", Reason: "backend has no track storage"}})
	require.NotEmpty(t, differences)
	require.True(t, differences[0].AllowedDiff)
	require.Equal(t, "backend has no track storage", differences[0].Reason)

	unsupported := unsupportedDifferences("tracks", "limited", "s", []Unsupported{{
		Path: "tracks", Reason: "backend has no track storage",
	}})
	require.Len(t, unsupported, 1)
	require.True(t, unsupported[0].AllowedDiff)

	stateDifference := []Difference{{Path: "data.state.tracks"}}
	markAllowedUnsupported(stateDifference, []Unsupported{{
		Path: "tracks", Reason: "backend has no track storage",
	}})
	require.False(t, stateDifference[0].AllowedDiff)
}

func TestTrackCasePreservesReplayFields(t *testing.T) {
	backend := newInMemoryBackend(t)
	var trackCase Case
	for _, replayCase := range StandardCases() {
		if replayCase.Name == "track_events" {
			trackCase = replayCase
			break
		}
	}
	snapshot, err := trackCase.Replay(context.Background(), backend)
	require.NoError(t, err)
	track := snapshot.Data["tracks"].(map[string]any)["tool"].(map[string]any)
	events := track["events"].([]any)
	require.Len(t, events, 2)
	payload := events[1].(map[string]any)["payload"].(map[string]any)
	require.Equal(t, "exception", payload["event_type"])
	require.Equal(t, "timeout", payload["error"])
	require.Equal(t, "normalized", payload["duration_ms"])
	require.Equal(t, "normalized", events[0].(map[string]any)["timestamp"])
	require.Equal(t, float64(0), events[0].(map[string]any)["sequence"])
	require.Equal(t, float64(1), events[1].(map[string]any)["sequence"])
}

func TestTrackNormalizationPreservesOrderAndIgnoresBackendTiming(t *testing.T) {
	baseline, err := normalize(map[string]any{"tracks": map[string]any{"tool": map[string]any{
		"events": []any{
			map[string]any{"timestamp": "2026-01-01T00:00:00Z", "payload": map[string]any{"duration_ms": 5}},
			map[string]any{"timestamp": "2026-01-01T00:00:01Z", "payload": map[string]any{"duration_ms": 10}},
		},
	}}}, nil)
	require.NoError(t, err)
	actual, err := normalize(map[string]any{"tracks": map[string]any{"tool": map[string]any{
		"events": []any{
			map[string]any{"timestamp": "2026-02-01T00:00:00Z", "payload": map[string]any{"duration_ms": 500}},
			map[string]any{"timestamp": "2026-02-01T00:01:00Z", "payload": map[string]any{"duration_ms": 1000}},
		},
	}}}, nil)
	require.NoError(t, err)
	require.Empty(t, Compare("tracks", "injected", Snapshot{Data: baseline.(map[string]any)}, Snapshot{Data: actual.(map[string]any)}))

	wrongOrder := cloneSnapshot(t, Snapshot{Data: actual.(map[string]any)})
	wrongOrder.Data["tracks"].(map[string]any)["tool"].(map[string]any)["events"].([]any)[1].(map[string]any)["sequence"] = float64(0)
	differences := Compare("tracks", "injected", Snapshot{Data: baseline.(map[string]any)}, wrongOrder)
	require.NotEmpty(t, differences)
	require.Equal(t, "data.tracks.tool.events[1].sequence", differences[0].Path)
}

func TestUnsupportedTrackStorageDoesNotHideStateDifference(t *testing.T) {
	limited := newInMemoryBackend(t)
	limited.Name = "limited"
	limited.Unsupported = []Unsupported{{
		Path: "tracks", Reason: "backend has no track storage",
	}}
	report, err := Run(context.Background(), []Backend{newInMemoryBackend(t), limited}, StandardCases())
	require.NoError(t, err)
	require.True(t, report.HasDisallowedDifferences())
	var foundAllowedTrack, foundStateDifference bool
	for _, difference := range report.Differences {
		if difference.Case == "track_events" && difference.Path == "data.tracks" {
			foundAllowedTrack = true
			require.True(t, difference.AllowedDiff)
		}
		if difference.Case == "track_events" && difference.Path == "data.state.tracks" {
			foundStateDifference = true
			require.False(t, difference.AllowedDiff)
		}
	}
	require.True(t, foundAllowedTrack)
	require.True(t, foundStateDifference)
}

func TestMemoryDifferencesUseMemoryIDInPath(t *testing.T) {
	baseline := Snapshot{SessionID: "s", Data: map[string]any{
		"memories": []any{map[string]any{"id": "memory-1", "memory": "expected"}},
	}}
	actual := Snapshot{SessionID: "s", Data: map[string]any{
		"memories": []any{map[string]any{"id": "memory-1", "memory": "actual"}},
	}}
	differences := Compare("memory", "injected", baseline, actual)
	require.Len(t, differences, 1)
	require.Equal(t, "data.memories[memory_id=memory-1].memory", differences[0].Path)
}

func TestMemoryScopeDifferencesUseMemoryIDInPath(t *testing.T) {
	baseline := Snapshot{SessionID: "s", Data: map[string]any{
		"memories": []any{map[string]any{"id": "memory-1", "scope": map[string]any{"app_name": "app", "user_id": "user-a"}}},
	}}
	actual := cloneSnapshot(t, baseline)
	actual.Data["memories"].([]any)[0].(map[string]any)["scope"].(map[string]any)["user_id"] = "user-b"

	differences := Compare("memory", "injected", baseline, actual)
	require.Len(t, differences, 1)
	require.Equal(t, "data.memories[memory_id=memory-1].scope.user_id", differences[0].Path)
}

func TestStateAndMemoryCasesPreserveFinalStateAndRetrievalOrder(t *testing.T) {
	backend := newInMemoryBackend(t)
	cases := map[string]Case{}
	for _, replayCase := range StandardCases() {
		cases[replayCase.Name] = replayCase
	}
	stateSnapshot, err := cases["state_updates"].Replay(context.Background(), backend)
	require.NoError(t, err)
	state := stateSnapshot.Data["state"].(map[string]any)
	require.Equal(t, "ZmluYWw=", state["status"])

	memorySnapshot, err := cases["memory_read_write"].Replay(context.Background(), backend)
	require.NoError(t, err)
	searches := memorySnapshot.Data["memory_search"].(map[string]any)
	results := searches["prefers"].([]any)
	require.NotEmpty(t, results)
	require.Equal(t, "prefers concise answers", results[0].(map[string]any)["memory"].(map[string]any)["memory"])
}

func TestCaptureOmitsDeclaredPrivateMetadata(t *testing.T) {
	backend := newInMemoryBackend(t)
	backend.PrivateMetadataPaths = []string{"events.*.extensions.storage_private"}
	key := session.Key{AppName: replayApp, UserID: replayUser, SessionID: "private-metadata"}
	sess, err := backend.Session.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	event := newReplayEvent("event", model.RoleUser, "hello", "")
	if event.Extensions == nil {
		event.Extensions = make(map[string]json.RawMessage)
	}
	event.Extensions["storage_private"] = json.RawMessage(`{"connection":"one"}`)
	require.NoError(t, backend.Session.AppendEvent(context.Background(), sess, event))

	snapshot, err := Capture(context.Background(), backend, key)
	require.NoError(t, err)
	extensions := snapshot.Data["events"].([]any)[0].(map[string]any)["extensions"].(map[string]any)
	require.NotContains(t, extensions, "storage_private")
}

func TestToolCasePreservesBranchTagStateAndExtension(t *testing.T) {
	backend := newInMemoryBackend(t)
	var toolCase Case
	for _, replayCase := range StandardCases() {
		if replayCase.Name == "tool_call" {
			toolCase = replayCase
			break
		}
	}
	snapshot, err := toolCase.Replay(context.Background(), backend)
	require.NoError(t, err)
	events := snapshot.Data["events"].([]any)
	require.Len(t, events, 3)
	toolResult := events[2].(map[string]any)
	require.Equal(t, "tools", toolResult["filterKey"])
	require.Equal(t, "tool-response", toolResult["tag"])
	require.Equal(t, "d2VhdGhlci1yZWFk", toolResult["stateDelta"].(map[string]any)["event_state"])
	require.Contains(t, toolResult["extensions"].(map[string]any), event.ToolCallArgsExtensionKey)
}

func TestToolEventsRoundTripThroughJSON(t *testing.T) {
	call, result, err := newReplayToolEvents()
	require.NoError(t, err)
	for _, original := range []*event.Event{call, result} {
		encoded, err := json.Marshal(original)
		require.NoError(t, err)
		var decoded event.Event
		require.NoError(t, json.Unmarshal(encoded, &decoded), string(encoded))
		require.NotNil(t, decoded.Response)
		require.True(t, decoded.IsValidContent())
	}
}

func TestLoadOptionalBackendsSkipsAndEnablesFromEnvironment(t *testing.T) {
	t.Setenv(EnvRedisURL, "")
	backends, skipped, err := LoadOptionalBackends(context.Background(), OptionalBackend{
		Name: "redis", Environment: EnvRedisURL,
	})
	require.NoError(t, err)
	require.Empty(t, backends)
	require.Equal(t, []string{"redis: REPLAYTEST_REDIS_URL is not set"}, skipped)

	t.Setenv(EnvRedisURL, "redis://127.0.0.1:6379")
	backends, skipped, err = LoadOptionalBackends(context.Background(), OptionalBackend{
		Name:        "redis",
		Environment: EnvRedisURL,
		Factory: func(context.Context, string) (Backend, error) {
			return newInMemoryBackend(t), nil
		},
	})
	require.NoError(t, err)
	require.Len(t, backends, 1)
	require.Equal(t, "inmemory", backends[0].Name)
	require.Empty(t, skipped)
}

func TestLoadOptionalBackendsClosesServicesOnFactoryFailure(t *testing.T) {
	const (
		firstEnvironment  = "REPLAYTEST_FIRST_BACKEND"
		secondEnvironment = "REPLAYTEST_SECOND_BACKEND"
	)
	t.Setenv(firstEnvironment, "first")
	t.Setenv(secondEnvironment, "second")

	firstSession := &closeTrackingSession{}
	firstMemory := &closeTrackingMemory{}
	secondSession := &closeTrackingSession{}
	secondMemory := &closeTrackingMemory{closeErr: errors.New("close memory")}
	factoryErr := errors.New("factory failed")

	backends, skipped, err := LoadOptionalBackends(
		context.Background(),
		OptionalBackend{
			Name:        "first",
			Environment: firstEnvironment,
			Factory: func(context.Context, string) (Backend, error) {
				return Backend{
					Name:    "first",
					Session: firstSession,
					Memory:  firstMemory,
				}, nil
			},
		},
		OptionalBackend{
			Name:        "second",
			Environment: secondEnvironment,
			Factory: func(context.Context, string) (Backend, error) {
				return Backend{
					Name:    "second",
					Session: secondSession,
					Memory:  secondMemory,
				}, factoryErr
			},
		},
	)
	require.ErrorIs(t, err, factoryErr)
	require.ErrorIs(t, err, secondMemory.closeErr)
	require.Nil(t, backends)
	require.Nil(t, skipped)
	require.Equal(t, 1, firstSession.closeCalls)
	require.Equal(t, 1, firstMemory.closeCalls)
	require.Equal(t, 1, secondSession.closeCalls)
	require.Equal(t, 1, secondMemory.closeCalls)
}

func TestLoadOptionalBackendsClosesServicesOnValidationFailure(t *testing.T) {
	const (
		firstEnvironment  = "REPLAYTEST_VALID_BACKEND"
		secondEnvironment = "REPLAYTEST_INVALID_BACKEND"
	)
	t.Setenv(firstEnvironment, "first")
	t.Setenv(secondEnvironment, "second")

	firstSession := &closeTrackingSession{}
	firstMemory := &closeTrackingMemory{}
	invalidSession := &closeTrackingSession{}
	_, _, err := LoadOptionalBackends(
		context.Background(),
		OptionalBackend{
			Name:        "first",
			Environment: firstEnvironment,
			Factory: func(context.Context, string) (Backend, error) {
				return Backend{
					Name:    "first",
					Session: firstSession,
					Memory:  firstMemory,
				}, nil
			},
		},
		OptionalBackend{
			Name:        "invalid",
			Environment: secondEnvironment,
			Factory: func(context.Context, string) (Backend, error) {
				return Backend{
					Name:    "invalid",
					Session: invalidSession,
				}, nil
			},
		},
	)
	require.EqualError(t, err, `backend "invalid" memory service is required`)
	require.Equal(t, 1, firstSession.closeCalls)
	require.Equal(t, 1, firstMemory.closeCalls)
	require.Equal(t, 1, invalidSession.closeCalls)
}

func TestSortMemoryEntriesHandlesNilEntries(t *testing.T) {
	entries := []*memory.Entry{
		nil,
		{ID: "memory-b"},
		{ID: "memory-a"},
		nil,
	}
	sortMemoryEntries(entries)
	require.Equal(t, "memory-a", entries[0].ID)
	require.Equal(t, "memory-b", entries[1].ID)
	require.Nil(t, entries[2])
	require.Nil(t, entries[3])
}

func TestNormalizationScopesGeneratedFields(t *testing.T) {
	normalized, err := normalize(map[string]any{
		"state": map[string]any{
			"timestamp":    "business timestamp",
			"updated_at":   "business update",
			"last_updated": "business memory label",
		},
		"events": []any{map[string]any{
			"id":        "generated event id",
			"timestamp": "generated event timestamp",
			"created":   123,
			"extensions": map[string]any{
				"timestamp":  "extension timestamp",
				"updated_at": "extension update",
			},
		}},
		"summaries": map[string]any{
			"branch": map[string]any{
				"updated_at": "summary update",
				"boundary": map[string]any{
					"cutoff_at": "summary cutoff",
					"version":   1,
				},
			},
		},
		"tracks": map[string]any{
			"tool": map[string]any{
				"events": []any{map[string]any{
					"timestamp": "track timestamp",
					"payload": map[string]any{
						"timestamp": "payload timestamp",
					},
				}},
			},
		},
		"memories": []any{map[string]any{
			"memory": map[string]any{
				"last_updated": "generated memory timestamp",
				"memory":       "content",
			},
		}},
		"memory_search": map[string]any{
			"query": []any{map[string]any{
				"memory": map[string]any{
					"last_updated": "generated search timestamp",
					"memory":       "search content",
				},
			}},
		},
	}, nil)
	require.NoError(t, err)
	data := normalized.(map[string]any)

	state := data["state"].(map[string]any)
	require.Equal(t, "business timestamp", state["timestamp"])
	require.Equal(t, "business update", state["updated_at"])
	require.Equal(t, "business memory label", state["last_updated"])

	event := data["events"].([]any)[0].(map[string]any)
	require.NotContains(t, event, "id")
	require.NotContains(t, event, "timestamp")
	require.NotContains(t, event, "created")
	extensions := event["extensions"].(map[string]any)
	require.Equal(t, "extension timestamp", extensions["timestamp"])
	require.Equal(t, "extension update", extensions["updated_at"])

	summary := data["summaries"].(map[string]any)["branch"].(map[string]any)
	require.Equal(t, "normalized", summary["updated_at"])
	boundary := summary["boundary"].(map[string]any)
	require.NotContains(t, boundary, "cutoff_at")
	require.Equal(t, float64(1), boundary["version"])

	trackEvent := data["tracks"].(map[string]any)["tool"].(map[string]any)["events"].([]any)[0].(map[string]any)
	require.Equal(t, "normalized", trackEvent["timestamp"])
	payload := trackEvent["payload"].(map[string]any)
	require.Equal(t, "payload timestamp", payload["timestamp"])

	memoryPayload := data["memories"].([]any)[0].(map[string]any)["memory"].(map[string]any)
	require.NotContains(t, memoryPayload, "last_updated")
	require.Equal(t, "content", memoryPayload["memory"])
	searchPayload := data["memory_search"].(map[string]any)["query"].([]any)[0].(map[string]any)["memory"].(map[string]any)
	require.NotContains(t, searchPayload, "last_updated")
	require.Equal(t, "search content", searchPayload["memory"])
}

func TestReportFixtureDocumentsDiffAndAllowedDiff(t *testing.T) {
	fixture := readReportFixture(t)
	var report Report
	require.NoError(t, json.Unmarshal(fixture, &report))
	require.Len(t, report.Differences, 2)
	require.False(t, report.Differences[0].AllowedDiff)
	require.True(t, report.Differences[1].AllowedDiff)
	require.NotEmpty(t, report.Differences[0].SessionID)
	require.NotEmpty(t, report.Differences[0].Path)
	generated, err := report.JSON()
	require.NoError(t, err)
	require.JSONEq(t, string(fixture), string(generated))
}

func TestRunReportsReplayFailures(t *testing.T) {
	baseline := newInMemoryBackend(t)
	actual := newInMemoryBackend(t)
	actual.Name = "actual"
	replayErr := errors.New("replay failed")

	report, err := Run(context.Background(), []Backend{baseline, actual}, []Case{
		{
			Name: "baseline_failure",
			Replay: func(_ context.Context, backend Backend) (Snapshot, error) {
				if backend.Name == baseline.Name {
					return Snapshot{}, replayErr
				}
				return Snapshot{SessionID: "unused"}, nil
			},
		},
		{
			Name: "actual_failure",
			Replay: func(_ context.Context, backend Backend) (Snapshot, error) {
				if backend.Name == actual.Name {
					return Snapshot{}, replayErr
				}
				return Snapshot{SessionID: "session", Data: map[string]any{}}, nil
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, report.Differences, 2)
	require.True(t, report.HasDisallowedDifferences())
	require.Equal(t, "replay", report.Differences[0].Path)
	require.Equal(t, "backend replay failed", report.Differences[0].Reason)
	require.Equal(t, "actual_failure", report.Differences[1].Case)
	require.Equal(t, actual.Name, report.Differences[1].Backend)
	require.Equal(t, "replay", report.Differences[1].Path)
	require.Equal(t, "backend replay failed", report.Differences[1].Reason)

	allowed := Report{Differences: []Difference{{AllowedDiff: true}}}
	require.False(t, allowed.HasDisallowedDifferences())
}

func TestRunAndBackendValidationErrors(t *testing.T) {
	valid := newInMemoryBackend(t)
	tests := []struct {
		name     string
		backends []Backend
		want     string
	}{
		{
			name:     "missing name",
			backends: []Backend{{Session: valid.Session, Memory: valid.Memory}, valid},
			want:     "backend name is required",
		},
		{
			name:     "missing session",
			backends: []Backend{{Name: "missing-session", Memory: valid.Memory}, valid},
			want:     `backend "missing-session" session service is required`,
		},
		{
			name:     "missing memory",
			backends: []Backend{{Name: "missing-memory", Session: valid.Session}, valid},
			want:     `backend "missing-memory" memory service is required`,
		},
		{
			name: "invalid unsupported",
			backends: []Backend{{
				Name: "invalid", Session: valid.Session, Memory: valid.Memory,
				Unsupported: []Unsupported{{Path: "tracks"}},
			}, valid},
			want: `backend "invalid" unsupported path and reason are required`,
		},
		{
			name: "invalid private metadata",
			backends: []Backend{{
				Name: "invalid", Session: valid.Session, Memory: valid.Memory,
				PrivateMetadataPaths: []string{" "},
			}, valid},
			want: `backend "invalid" private metadata path is required`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Run(context.Background(), test.backends, StandardCases())
			require.EqualError(t, err, test.want)
		})
	}

	_, err := Run(context.Background(), []Backend{valid, valid}, []Case{{Name: "invalid"}})
	require.EqualError(t, err, "replay case name and function are required")
}

func TestLoadOptionalBackendsConfigurationErrors(t *testing.T) {
	_, _, err := LoadOptionalBackends(context.Background(), OptionalBackend{})
	require.EqualError(t, err, "optional backend name and environment are required")

	const environment = "REPLAYTEST_MISSING_FACTORY"
	t.Setenv(environment, "endpoint")
	_, _, err = LoadOptionalBackends(context.Background(), OptionalBackend{
		Name: "missing-factory", Environment: environment,
	})
	require.EqualError(t, err, `optional backend "missing-factory" factory is required`)
}

type captureSessionService struct {
	session.Service
	getSessionFunc  func(context.Context, session.Key, ...session.Option) (*session.Session, error)
	listSessionFunc func(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error)
}

func (s *captureSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	return s.getSessionFunc(ctx, key, options...)
}

func (s *captureSessionService) ListSessions(
	ctx context.Context,
	key session.UserKey,
	options ...session.Option,
) ([]*session.Session, error) {
	return s.listSessionFunc(ctx, key, options...)
}

type captureMemoryService struct {
	memory.Service
	readFunc   func(context.Context, memory.UserKey, int) ([]*memory.Entry, error)
	searchFunc func(context.Context, memory.UserKey, string, ...memory.SearchOption) ([]*memory.Entry, error)
}

func (m *captureMemoryService) ReadMemories(
	ctx context.Context,
	key memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	return m.readFunc(ctx, key, limit)
}

func (m *captureMemoryService) SearchMemories(
	ctx context.Context,
	key memory.UserKey,
	query string,
	options ...memory.SearchOption,
) ([]*memory.Entry, error) {
	return m.searchFunc(ctx, key, query, options...)
}

func TestCaptureReadFailures(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	readErr := errors.New("read failed")
	baseSession := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sessionService := &captureSessionService{
		getSessionFunc: func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return baseSession, nil
		},
		listSessionFunc: func(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error) {
			return []*session.Session{baseSession}, nil
		},
	}
	memoryService := &captureMemoryService{
		readFunc: func(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
			return nil, nil
		},
		searchFunc: func(context.Context, memory.UserKey, string, ...memory.SearchOption) ([]*memory.Entry, error) {
			return nil, nil
		},
	}
	backend := Backend{Name: "stub", Session: sessionService, Memory: memoryService}

	sessionService.getSessionFunc = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
		return nil, readErr
	}
	_, err := Capture(context.Background(), backend, key)
	require.ErrorIs(t, err, readErr)

	sessionService.getSessionFunc = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
		return nil, nil
	}
	_, err = Capture(context.Background(), backend, key)
	require.EqualError(t, err, "session not found")

	sessionService.getSessionFunc = func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
		return baseSession, nil
	}
	memoryService.readFunc = func(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
		return nil, readErr
	}
	_, err = Capture(context.Background(), backend, key)
	require.ErrorIs(t, err, readErr)

	memoryService.readFunc = func(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
		return nil, nil
	}
	sessionService.listSessionFunc = func(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error) {
		return nil, readErr
	}
	_, err = Capture(context.Background(), backend, key)
	require.ErrorIs(t, err, readErr)

	sessionService.listSessionFunc = func(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error) {
		return []*session.Session{nil, session.NewSession("app", "user", "other")}, nil
	}
	_, err = Capture(context.Background(), backend, key)
	require.EqualError(t, err, "session not found while loading summaries")

	sessionService.listSessionFunc = func(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error) {
		return []*session.Session{baseSession}, nil
	}
	memoryService.searchFunc = func(context.Context, memory.UserKey, string, ...memory.SearchOption) ([]*memory.Entry, error) {
		return nil, readErr
	}
	_, err = Capture(context.Background(), backend, key, WithMemorySearchQueries("query"))
	require.ErrorIs(t, err, readErr)
}

func TestCaptureHandlesEmptyValuesAndNormalizeError(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.Summaries = map[string]*session.Summary{
		"branch": nil,
		"kept":   {Summary: "summary"},
	}
	sessionService := &captureSessionService{
		getSessionFunc: func(context.Context, session.Key, ...session.Option) (*session.Session, error) {
			return sess, nil
		},
		listSessionFunc: func(context.Context, session.UserKey, ...session.Option) ([]*session.Session, error) {
			return []*session.Session{sess}, nil
		},
	}
	memoryService := &captureMemoryService{
		readFunc: func(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
			return []*memory.Entry{nil}, nil
		},
		searchFunc: func(context.Context, memory.UserKey, string, ...memory.SearchOption) ([]*memory.Entry, error) {
			return nil, nil
		},
	}
	backend := Backend{Name: "stub", Session: sessionService, Memory: memoryService}
	snapshot, err := Capture(context.Background(), backend, key,
		WithSummaryFilterKeys("branch", "kept"),
		WithMemorySearchQueries("empty"),
	)
	require.NoError(t, err)
	require.Equal(t, []any{}, snapshot.Data["memory_search"].(map[string]any)["empty"])
	require.Equal(t, map[string]any{}, snapshot.Data["memories"].([]any)[0])

	sess.Tracks = map[session.Track]*session.TrackEvents{
		"invalid": {
			Track:  "invalid",
			Events: []session.TrackEvent{{Track: "invalid", Payload: json.RawMessage("{")}},
		},
	}
	_, err = Capture(context.Background(), backend, key, WithSummaryFilterKeys("kept"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "normalize snapshot")
}

func TestNormalizationAndPathFallbacks(t *testing.T) {
	require.NotEmpty(t, marshalValue(func() {}))
	_, err := normalize(make(chan int), nil)
	require.Error(t, err)
	require.Equal(t, "", lastPathComponent(nil))
	require.Equal(t, "data.items[0]", sliceItemPath("data.items", 0, "plain", nil))
	require.Equal(t, "data.memories[memory_id=left]",
		sliceItemPath("data.memories", 0, map[string]any{"id": "left"}, nil))
	require.Equal(t, "data.memories[memory_id=right]",
		sliceItemPath("data.memories", 0, nil, map[string]any{"id": "right"}))
	require.Equal(t, "", snapshotID(map[string]any{}))
	require.Equal(t, "", snapshotID(map[string]any{"id": 3}))
}

type failingSessionService struct {
	session.Service
	operation string
	err       error
}

func (s *failingSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	if s.operation == "create" {
		return nil, s.err
	}
	return s.Service.CreateSession(ctx, key, state, options...)
}

func (s *failingSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	replayEvent *event.Event,
	options ...session.Option,
) error {
	if s.operation == "append" {
		return s.err
	}
	return s.Service.AppendEvent(ctx, sess, replayEvent, options...)
}

func (s *failingSessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	if s.operation == "state" {
		return s.err
	}
	return s.Service.UpdateSessionState(ctx, key, state)
}

func (s *failingSessionService) GetSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) (*session.Session, error) {
	if s.operation == "get" {
		return nil, s.err
	}
	return s.Service.GetSession(ctx, key, options...)
}

func (s *failingSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if s.operation == "summary" {
		return s.err
	}
	return s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
}

type failingMemoryService struct {
	memory.Service
	err error
}

func (m *failingMemoryService) AddMemory(
	context.Context,
	memory.UserKey,
	string,
	[]string,
	...memory.AddOption,
) error {
	return m.err
}

type failingTrackSessionService struct {
	*failingSessionService
}

func (s *failingTrackSessionService) AppendTrackEvent(
	context.Context,
	*session.Session,
	*session.TrackEvent,
	...session.Option,
) error {
	return s.err
}

func TestStandardCaseErrorPaths(t *testing.T) {
	failure := errors.New("injected failure")
	cases := make(map[string]Case)
	for _, replayCase := range StandardCases() {
		cases[replayCase.Name] = replayCase
	}

	for name, replayCase := range cases {
		t.Run(name+"/create", func(t *testing.T) {
			backend := newInMemoryBackend(t)
			backend.Session = &failingSessionService{
				Service: backend.Session, operation: "create", err: failure,
			}
			_, err := replayCase.Replay(context.Background(), backend)
			require.ErrorIs(t, err, failure)
		})
	}

	for _, name := range []string{
		"single_turn", "multi_turn", "tool_call", "summary_update",
		"summary_with_follow_up_events", "interleaved_writes", "retry_recovery",
	} {
		t.Run(name+"/append", func(t *testing.T) {
			backend := newInMemoryBackend(t)
			backend.Session = &failingSessionService{
				Service: backend.Session, operation: "append", err: failure,
			}
			_, err := cases[name].Replay(context.Background(), backend)
			require.ErrorIs(t, err, failure)
		})
	}

	for _, name := range []string{"state_updates", "retry_recovery"} {
		t.Run(name+"/state", func(t *testing.T) {
			backend := newInMemoryBackend(t)
			backend.Session = &failingSessionService{
				Service: backend.Session, operation: "state", err: failure,
			}
			_, err := cases[name].Replay(context.Background(), backend)
			require.ErrorIs(t, err, failure)
		})
	}

	for _, name := range []string{"memory_read_write", "retry_recovery"} {
		t.Run(name+"/memory", func(t *testing.T) {
			backend := newInMemoryBackend(t)
			backend.Memory = &failingMemoryService{Service: backend.Memory, err: failure}
			_, err := cases[name].Replay(context.Background(), backend)
			require.ErrorIs(t, err, failure)
		})
	}

	for _, operation := range []string{"get", "summary"} {
		for _, name := range []string{"summary_update", "summary_with_follow_up_events"} {
			t.Run(name+"/"+operation, func(t *testing.T) {
				backend := newInMemoryBackend(t)
				backend.Session = &failingSessionService{
					Service: backend.Session, operation: operation, err: failure,
				}
				_, err := cases[name].Replay(context.Background(), backend)
				require.ErrorIs(t, err, failure)
			})
		}
	}

	t.Run("track service missing", func(t *testing.T) {
		backend := newInMemoryBackend(t)
		backend.Session = &failingSessionService{Service: backend.Session}
		_, err := cases["track_events"].Replay(context.Background(), backend)
		require.EqualError(t, err, `backend "inmemory" does not support track events`)
	})
	t.Run("track append", func(t *testing.T) {
		backend := newInMemoryBackend(t)
		backend.Session = &failingTrackSessionService{failingSessionService: &failingSessionService{
			Service: backend.Session, err: failure,
		}}
		_, err := cases["track_events"].Replay(context.Background(), backend)
		require.ErrorIs(t, err, failure)
	})
}

type closeTrackingSession struct {
	session.Service
	closeCalls int
	closeErr   error
}

func (s *closeTrackingSession) Close() error {
	s.closeCalls++
	return s.closeErr
}

type closeTrackingMemory struct {
	memory.Service
	closeCalls int
	closeErr   error
}

func (m *closeTrackingMemory) Close() error {
	m.closeCalls++
	return m.closeErr
}

func newInMemoryBackend(t *testing.T) Backend {
	t.Helper()
	sessionService := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(staticSummarizer{}),
		sessinmemory.WithAsyncSummaryNum(0),
	)
	memoryService := meminmemory.NewMemoryService()
	t.Cleanup(func() {
		require.NoError(t, memoryService.Close())
		require.NoError(t, sessionService.Close())
	})
	return Backend{Name: "inmemory", Session: sessionService, Memory: memoryService}
}

func readReportFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	require.NoError(t, err)
	return data
}

func cloneSnapshot(t *testing.T, source Snapshot) Snapshot {
	t.Helper()
	encoded, err := json.Marshal(source.Data)
	require.NoError(t, err)
	var data map[string]any
	require.NoError(t, json.Unmarshal(encoded, &data))
	return Snapshot{SessionID: source.SessionID, Data: data}
}
