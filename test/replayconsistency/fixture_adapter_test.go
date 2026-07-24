//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	overlapOperationCount = 2
	overlapTestTimeout    = 2 * time.Second
)

func TestReplayFixturesUseUniquePhysicalScopes(t *testing.T) {
	newFixture := func() *replayFixture {
		summarizer := &replaySummarizer{}
		return newReplayFixture(replayFixtureConfig{
			name: "inmemory",
			sessionService: sessioninmemory.NewSessionService(
				sessioninmemory.WithSummarizer(summarizer),
			),
			memoryService: memoryinmemory.NewMemoryService(),
			summarizer:    summarizer,
		})
	}
	first := newFixture()
	second := newFixture()
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first fixture: %v", err)
		}
		if err := second.Close(); err != nil {
			t.Errorf("close second fixture: %v", err)
		}
	})
	if first.appName == replayAppName || first.userID == replayUserID {
		t.Fatalf("fixture uses shared scope %q/%q", first.appName, first.userID)
	}
	if first.appName == second.appName || first.userID == second.userID {
		t.Fatalf("fixtures share scope %q/%q", first.appName, first.userID)
	}
	if key := first.sessionKey("session"); key.AppName != first.appName || key.UserID != first.userID {
		t.Fatalf("session key = %#v", key)
	}
	key := first.memoryKey(replayAppName, replayUserID)
	if key.AppName == first.appName || key.UserID == first.userID {
		t.Fatalf("memory key does not isolate logical scope: %#v", key)
	}
	if same := first.memoryKey(replayAppName, replayUserID); same != key {
		t.Fatalf("memory key changed: %#v, want %#v", same, key)
	}
	if other := first.memoryKey(replayAppName, "user-2"); other == key {
		t.Fatalf("distinct logical scopes share physical key %#v", key)
	}
}

func TestValidatePhysicalMemoryScopeRejectsLeaks(t *testing.T) {
	want := memory.UserKey{AppName: "physical-app", UserID: "physical-user"}
	if err := validatePhysicalMemoryScope(&memory.Entry{
		ID: "memory-1", AppName: want.AppName, UserID: want.UserID,
	}, want); err != nil {
		t.Fatalf("validatePhysicalMemoryScope() error = %v", err)
	}
	for _, entry := range []*memory.Entry{
		nil,
		{ID: "memory-1", AppName: "other-app", UserID: want.UserID},
		{ID: "memory-1", AppName: want.AppName, UserID: "other-user"},
	} {
		if err := validatePhysicalMemoryScope(entry, want); err == nil {
			t.Fatalf("validatePhysicalMemoryScope(%#v) error = nil", entry)
		}
	}
}

func TestValidatePhysicalSessionScopeRejectsLeaks(t *testing.T) {
	want := session.Key{
		AppName: "physical-app", UserID: "physical-user", SessionID: "physical-session",
	}
	valid := &session.Session{AppName: want.AppName, UserID: want.UserID, ID: want.SessionID}
	if err := validatePhysicalSessionScope(valid, want); err != nil {
		t.Fatalf("validatePhysicalSessionScope() error = %v", err)
	}
	for _, sess := range []*session.Session{
		nil,
		{AppName: "other-app", UserID: want.UserID, ID: want.SessionID},
		{AppName: want.AppName, UserID: "other-user", ID: want.SessionID},
		{AppName: want.AppName, UserID: want.UserID, ID: "other-session"},
	} {
		if err := validatePhysicalSessionScope(sess, want); err == nil {
			t.Fatalf("validatePhysicalSessionScope(%#v) error = nil", sess)
		}
	}
}

func TestReplayFixtureCapturesMemorySearchAtApplyTime(t *testing.T) {
	fixture := newReplayFixture(replayFixtureConfig{
		name:           "inmemory",
		sessionService: sessioninmemory.NewSessionService(),
		memoryService:  memoryinmemory.NewMemoryService(),
		summarizer:     &replaySummarizer{},
	})
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	first := replaytest.Operation{
		Kind: replaytest.OperationWriteMemory,
		Memory: &replaytest.MemorySnapshot{
			AppName: replayAppName, UserID: replayUserID, Content: "shared first",
			Topics: []string{"first"}, Metadata: map[string]any{"participants": []string{"one"}},
		},
	}
	search := replaytest.Operation{
		Kind: replaytest.OperationSearchMemory, SearchQuery: "shared", SearchLimit: 10,
		SearchAppName: replayAppName, SearchUserID: replayUserID,
	}
	second := replaytest.Operation{
		Kind: replaytest.OperationWriteMemory,
		Memory: &replaytest.MemorySnapshot{
			AppName: replayAppName, UserID: replayUserID, Content: "shared second",
			Topics: []string{"second"},
		},
	}
	for _, operation := range []replaytest.Operation{first, search, second} {
		if err := fixture.Apply(context.Background(), operation); err != nil {
			t.Fatalf("fixture.Apply(%s) error = %v", operation.Kind, err)
		}
	}
	snapshot, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("fixture.Snapshot() error = %v", err)
	}
	if len(snapshot.MemorySearches) != 1 || len(snapshot.MemorySearches[0].Results) != 1 ||
		snapshot.MemorySearches[0].Results[0].Content != "shared first" {
		t.Fatalf("point-in-time search = %#v", snapshot.MemorySearches)
	}
	snapshot.MemorySearches[0].Results[0].Topics[0] = "mutated"
	snapshot.MemorySearches[0].Results[0].Metadata["participants"].([]string)[0] = "mutated"
	again, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("fixture.Snapshot() again error = %v", err)
	}
	result := again.MemorySearches[0].Results[0]
	if result.Topics[0] != "first" || result.Metadata["participants"].([]string)[0] != "one" {
		t.Fatalf("snapshot mutation escaped into fixture: %#v", result)
	}
}

func TestReplayFixtureCanDeclareUnsupportedCapability(t *testing.T) {
	summarizer := &replaySummarizer{}
	fixture := newReplayFixture(replayFixtureConfig{
		name: "clickhouse",
		sessionService: sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(summarizer),
		),
		memoryService: memoryinmemory.NewMemoryService(),
		summarizer:    summarizer,
		unsupported:   []replaytest.Capability{replaytest.CapabilityTrack},
	})
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
	})
	if fixture.Capabilities().Supports(replaytest.CapabilityTrack) {
		t.Fatal("fixture unexpectedly supports track")
	}
	if !fixture.Capabilities().Supports(replaytest.CapabilitySession) {
		t.Fatal("fixture lost supported session capability")
	}
}

func TestReplayFixtureDoesNotSerializeServiceWrites(t *testing.T) {
	baseService := sessioninmemory.NewSessionService()
	release := make(chan struct{})
	blockingService := &blockingSessionService{
		Service: baseService, entered: make(chan struct{}, overlapOperationCount), release: release,
	}
	fixture := newReplayFixture(replayFixtureConfig{
		name: "overlap", sessionService: blockingService,
		memoryService: memoryinmemory.NewMemoryService(), summarizer: &replaySummarizer{},
	})
	ctx, cancel := context.WithTimeout(context.Background(), overlapTestTimeout)
	defer cancel()
	if err := fixture.Apply(ctx, replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: "session-overlap",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	errorsByIndex := make([]error, overlapOperationCount)
	var waitGroup sync.WaitGroup
	waitGroup.Add(overlapOperationCount)
	for i := 0; i < overlapOperationCount; i++ {
		go func(index int) {
			defer waitGroup.Done()
			errorsByIndex[index] = fixture.Apply(ctx, replaytest.Operation{
				Kind: replaytest.OperationAppendEvent, SessionID: "session-overlap",
				Event: &replaytest.EventSnapshot{ID: fmt.Sprintf("event-%d", index)},
			})
		}(i)
	}
	overlapped := waitForWriteOverlap(ctx, blockingService.entered)
	close(release)
	waitGroup.Wait()
	if !overlapped {
		t.Fatal("service writes did not overlap")
	}
	for i, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
}

type blockingSessionService struct {
	session.Service
	entered chan struct{}
	release <-chan struct{}
}

func (service *blockingSessionService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	options ...session.Option,
) error {
	select {
	case service.entered <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-service.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return service.Service.AppendEvent(ctx, sess, evt, options...)
}

func waitForWriteOverlap(ctx context.Context, entered <-chan struct{}) bool {
	for i := 0; i < overlapOperationCount; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

func TestEventConversionRoundTripsToolResponseExtra(t *testing.T) {
	want := &replaytest.EventSnapshot{
		ID:           "event-1",
		InvocationID: "invocation-1",
		Author:       "tool",
		Role:         "tool",
		Object:       "tool.response",
		Done:         true,
		Extensions:   map[string]any{"trace": "kept"},
		ToolResponse: &replaytest.ToolResponse{
			ToolCallID: "call-1",
			Name:       "weather",
			Content:    "sunny",
			Extra:      map[string]any{"provider_status": "ok"},
		},
	}
	evt, err := toEvent(want)
	if err != nil {
		t.Fatalf("toEvent() error = %v", err)
	}
	got := toEventSnapshot(evt, false)
	if got.InvocationID != want.InvocationID || got.Object != want.Object ||
		got.Done != want.Done || !reflect.DeepEqual(got.Extensions, want.Extensions) ||
		!reflect.DeepEqual(got.ToolResponse, want.ToolResponse) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestTakeToolResponseExtraSupportsClickHouseNestedJSON(t *testing.T) {
	extensions := map[string]any{
		replayAppName: map[string]any{
			"tool_response_extra": map[string]any{"provider_status": "ok"},
			"keep":                true,
		},
	}
	got := takeToolResponseExtra(extensions, true)
	if !reflect.DeepEqual(got, map[string]any{"provider_status": "ok"}) {
		t.Fatalf("takeToolResponseExtra() = %#v", got)
	}
	if !reflect.DeepEqual(extensions, map[string]any{
		replayAppName: map[string]any{"keep": true},
	}) {
		t.Fatalf("remaining extensions = %#v", extensions)
	}
}

func TestTakeToolResponseExtraPreservesNestedJSONOutsideClickHouse(t *testing.T) {
	extensions := map[string]any{
		replayAppName: map[string]any{
			"tool_response_extra": map[string]any{"provider_status": "user-value"},
		},
	}
	want := map[string]any{
		replayAppName: map[string]any{
			"tool_response_extra": map[string]any{"provider_status": "user-value"},
		},
	}
	if got := takeToolResponseExtra(extensions, false); got != nil {
		t.Fatalf("takeToolResponseExtra() = %#v, want nil", got)
	}
	if !reflect.DeepEqual(extensions, want) {
		t.Fatalf("extensions = %#v, want preserved %#v", extensions, want)
	}
}

func TestEventConversionPreservesExplicitIncompleteResponse(t *testing.T) {
	want := &replaytest.EventSnapshot{
		ID: "event-incomplete", InvocationID: "", Object: "chat.completion.chunk", Done: false,
		Author: "assistant", Role: "assistant", Content: "partial",
	}
	evt, err := toEvent(want)
	if err != nil {
		t.Fatalf("toEvent() error = %v", err)
	}
	got := toEventSnapshot(evt, false)
	if got.InvocationID != want.InvocationID || got.Object != want.Object || got.Done != want.Done {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestMemoryConversionPreservesScope(t *testing.T) {
	got := toMemorySnapshot(&memory.Entry{
		ID:      "memory-1",
		AppName: "app-1",
		UserID:  "user-1",
		Memory:  &memory.Memory{Memory: "preference"},
	})
	want := replaytest.MemoryScope{AppName: "app-1", UserID: "user-1"}
	if got.Scope != want {
		t.Fatalf("memory scope = %#v, want %#v", got.Scope, want)
	}
}

func TestEventConversionPropagatesSerializationErrors(t *testing.T) {
	tests := []struct {
		name  string
		event *replaytest.EventSnapshot
		want  string
	}{
		{
			name: "tool arguments",
			event: &replaytest.EventSnapshot{ToolCalls: []replaytest.ToolCallSnapshot{{
				ID: "call-1", Arguments: make(chan int),
			}}},
			want: "tool call",
		},
		{
			name: "state delta",
			event: &replaytest.EventSnapshot{StateDelta: map[string]replaytest.StateValueSnapshot{
				"bad": replaytest.JSONStateValue(make(chan int)),
			}},
			want: "state delta",
		},
		{
			name:  "extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{"bad": make(chan int)}},
			want:  "event extension",
		},
		{
			name: "tool response extra",
			event: &replaytest.EventSnapshot{ToolResponse: &replaytest.ToolResponse{
				Extra: map[string]any{"bad": make(chan int)},
			}},
			want: "tool response extra",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := toEvent(test.event)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("toEvent() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStateConversionPreservesStorageSemantics(t *testing.T) {
	const (
		largeInteger            = "9007199254740993"
		invalidUTF8Lead         = byte(0xff)
		invalidUTF8Continuation = byte(0xfe)
	)
	binary := []byte{invalidUTF8Lead, invalidUTF8Continuation}
	got := decodeStateMap(map[string][]byte{
		"nil":    nil,
		"null":   []byte("null"),
		"number": []byte(largeInteger),
		"text":   []byte("not-json"),
		"binary": binary,
	})
	want := map[string]replaytest.StateValueSnapshot{
		"nil":    replaytest.NullStateValue(),
		"null":   replaytest.NullStateValue(),
		"number": replaytest.JSONStateValue(json.Number(largeInteger)),
		"text":   replaytest.TextStateValue("not-json"),
		"binary": replaytest.BinaryStateValue(binary),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeStateMap() = %#v, want %#v", got, want)
	}
}

func TestStateEncodingRejectsInvalidTypedValues(t *testing.T) {
	tests := []replaytest.StateValueSnapshot{
		{Kind: replaytest.StateValueNull, Value: "unexpected"},
		{Kind: replaytest.StateValueText, Value: struct{}{}},
		{Kind: replaytest.StateValueBinary, Value: "not-binary"},
		{Kind: replaytest.StateValueKind("unknown")},
	}
	for _, value := range tests {
		if _, err := encodeSnapshotStateValue(value); err == nil {
			t.Fatalf("encodeSnapshotStateValue(%#v) accepted invalid value", value)
		}
	}
}

func TestMemoryMetadataConversion(t *testing.T) {
	eventTime := time.Unix(100, 0).UTC()
	got, err := toMemoryMetadata(map[string]any{
		"kind":         "episode",
		"event_time":   eventTime,
		"participants": []string{"user", "assistant"},
		"location":     "Shenzhen",
	})
	if err != nil {
		t.Fatalf("toMemoryMetadata() error = %v", err)
	}
	want := &memory.Metadata{
		Kind:         memory.Kind("episode"),
		EventTime:    &eventTime,
		Participants: []string{"user", "assistant"},
		Location:     "Shenzhen",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toMemoryMetadata() = %#v, want %#v", got, want)
	}
	if _, err := toMemoryMetadata(map[string]any{"participants": "user"}); err == nil {
		t.Fatal("toMemoryMetadata() accepted invalid participants")
	}
}
