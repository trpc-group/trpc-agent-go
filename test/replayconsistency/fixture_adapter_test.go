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
	"errors"
	"fmt"
	"reflect"
	"runtime"
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

func TestFixtureJSONDecodingPreservesExactNumbers(t *testing.T) {
	const number = "1234567890.1234567890123456789"
	decoded := decodeRawMap(map[string]json.RawMessage{
		"number": json.RawMessage(number),
	})
	if got, ok := decoded["number"].(json.Number); !ok || got.String() != number {
		t.Fatalf("decodeRawMap() number = %#v", decoded["number"])
	}
	var payload trackPayload
	data := []byte(`{"payload":{"number":` + number + `}}`)
	if err := decodeJSONUseNumber(data, &payload); err != nil {
		t.Fatalf("decodeJSONUseNumber() error = %v", err)
	}
	if got, ok := payload.Payload["number"].(json.Number); !ok || got.String() != number {
		t.Fatalf("track payload number = %#v", payload.Payload["number"])
	}
}

func TestReplayFixturePreservesBackendMemoryTimes(t *testing.T) {
	fixture := newReplayFixture(replayFixtureConfig{
		name:           "inmemory",
		sessionService: sessioninmemory.NewSessionService(),
		memoryService:  memoryinmemory.NewMemoryService(),
	})
	t.Cleanup(func() {
		if err := fixture.Close(); err != nil {
			t.Errorf("fixture.Close() error = %v", err)
		}
	})
	before := time.Now().Add(-time.Second)
	users := []string{"user-1", "user-2"}
	for _, userID := range users {
		err := fixture.Apply(context.Background(), replaytest.Operation{
			Kind: replaytest.OperationWriteMemory,
			Memory: &replaytest.MemorySnapshot{
				AppName: "app", UserID: userID, Content: "same content",
				Topics: []string{"fact"}, Metadata: map[string]any{"kind": "fact"},
			},
		})
		if err != nil {
			t.Fatalf("fixture.Apply() error = %v", err)
		}
	}
	after := time.Now().Add(time.Second)
	snapshot, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("fixture.Snapshot() error = %v", err)
	}
	if len(snapshot.Memories) != len(users) {
		t.Fatalf("snapshot memories = %#v", snapshot.Memories)
	}
	for index, item := range snapshot.Memories {
		if item.UserID != users[index] {
			t.Fatalf("snapshot memory scopes = %#v, want %v", snapshot.Memories, users)
		}
		if item.CreatedAt.Before(before) || item.CreatedAt.After(after) ||
			item.UpdatedAt.Before(before) || item.UpdatedAt.After(after) {
			t.Fatalf("fixture replaced backend memory times: %#v", item)
		}
	}
}

func TestFixtureJSONDecodingTagsInvalidRawJSON(t *testing.T) {
	decoded := decodeRawMap(map[string]json.RawMessage{
		"invalid": json.RawMessage(`value`),
		"string":  json.RawMessage(`"value"`),
	})
	if decoded["string"] != "value" {
		t.Fatalf("valid JSON string = %#v, want value", decoded["string"])
	}
	invalid, ok := decoded["invalid"].(json.RawMessage)
	if !ok || string(invalid) != "value" {
		t.Fatalf("invalid JSON raw = %#v", decoded["invalid"])
	}
	if reflect.DeepEqual(decoded["invalid"], decoded["string"]) {
		t.Fatalf("invalid raw JSON collides with valid string: %#v", decoded)
	}
	object := decodeRawMap(map[string]json.RawMessage{
		"invalid": json.RawMessage(`value`),
		"object":  json.RawMessage(`{"replaytest.invalid_json_raw":"value"}`),
	})
	if reflect.DeepEqual(object["invalid"], object["object"]) {
		t.Fatalf("invalid raw JSON collides with valid object: %#v", object)
	}
}

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

func TestReplayFixtureCleansUpUncertainSessionCreation(t *testing.T) {
	service := &uncertainCreateSessionService{
		Service: sessioninmemory.NewSessionService(),
	}
	fixture := newReplayFixture(replayFixtureConfig{
		name:           "uncertain-create",
		sessionService: service,
		memoryService:  memoryinmemory.NewMemoryService(),
		summarizer:     &replaySummarizer{},
	})
	const sessionID = "uncertain-session"
	err := fixture.Apply(context.Background(), replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: sessionID,
	})
	if !errors.Is(err, errUncertainCreate) {
		t.Fatalf("fixture.Apply() error = %v, want %v", err, errUncertainCreate)
	}
	key := fixture.sessionKey(sessionID)
	sess, err := service.Service.GetSession(context.Background(), key)
	if err != nil {
		t.Fatalf("committed session missing before cleanup: %v", err)
	}
	if sess == nil {
		t.Fatal("committed session is nil before cleanup")
	}
	if err := fixture.Close(); err != nil {
		t.Fatalf("fixture.Close() error = %v", err)
	}
	if deleted := service.deletedKeys(); !reflect.DeepEqual(deleted, []session.Key{key}) {
		t.Fatalf("deleted session keys = %#v, want %#v", deleted, []session.Key{key})
	}
	sess, err = service.Service.GetSession(context.Background(), key)
	if err != nil {
		t.Fatalf("get session after cleanup: %v", err)
	}
	if sess != nil {
		t.Fatalf("session remains after cleanup: %#v", sess)
	}
	if err := fixture.Apply(context.Background(), replaytest.Operation{
		Kind: replaytest.OperationCreateSession, SessionID: "after-close",
	}); !errors.Is(err, errReplayFixtureClosed) {
		t.Fatalf("fixture.Apply() after close error = %v, want %v", err, errReplayFixtureClosed)
	}
	if _, err := fixture.Snapshot(context.Background()); !errors.Is(err, errReplayFixtureClosed) {
		t.Fatalf("fixture.Snapshot() after close error = %v, want %v", err, errReplayFixtureClosed)
	}
}

func TestReplayFixtureCloseWaitsForInFlightCreate(t *testing.T) {
	committed := make(chan struct{})
	release := make(chan struct{})
	deleted := make(chan session.Key, 1)
	service := &uncertainCreateSessionService{
		Service:       sessioninmemory.NewSessionService(),
		committed:     committed,
		release:       release,
		deletedSignal: deleted,
	}
	fixture := newReplayFixture(replayFixtureConfig{
		name:           "concurrent-close",
		sessionService: service,
		memoryService:  memoryinmemory.NewMemoryService(),
		summarizer:     &replaySummarizer{},
	})
	const sessionID = "in-flight-session"
	applyResult := make(chan error, 1)
	go func() {
		applyResult <- fixture.Apply(context.Background(), replaytest.Operation{
			Kind: replaytest.OperationCreateSession, SessionID: sessionID,
		})
	}()
	<-committed
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- fixture.Close()
	}()
	deadline := time.Now().Add(time.Second)
	for fixture.lifecycleMu.TryRLock() {
		fixture.lifecycleMu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("fixture.Close() did not queue for the lifecycle lock")
		}
		runtime.Gosched()
	}
	select {
	case key := <-deleted:
		t.Fatalf("session deleted before create returned: %#v", key)
	case err := <-closeResult:
		t.Fatalf("fixture.Close() returned before create: %v", err)
	default:
	}
	close(release)
	if err := <-applyResult; !errors.Is(err, errUncertainCreate) {
		t.Fatalf("fixture.Apply() error = %v, want %v", err, errUncertainCreate)
	}
	if err := <-closeResult; err != nil {
		t.Fatalf("fixture.Close() error = %v", err)
	}
	select {
	case key := <-deleted:
		if key != fixture.sessionKey(sessionID) {
			t.Fatalf("deleted session key = %#v", key)
		}
	case <-time.After(time.Second):
		t.Fatal("in-flight session was not deleted")
	}
}

func TestReplayFixtureCloseReturnsCachedError(t *testing.T) {
	closeErr := errors.New("close session service")
	fixture := newReplayFixture(replayFixtureConfig{
		name: "close-error",
		sessionService: &closeErrorSessionService{
			Service: sessioninmemory.NewSessionService(),
			err:     closeErr,
		},
		memoryService: memoryinmemory.NewMemoryService(),
		summarizer:    &replaySummarizer{},
	})
	for i := 0; i < 2; i++ {
		if err := fixture.Close(); !errors.Is(err, closeErr) {
			t.Fatalf("fixture.Close() call %d error = %v, want %v", i+1, err, closeErr)
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

func TestReplayFixtureRejectsUnsupportedMemoryMetadata(t *testing.T) {
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
	err := fixture.Apply(context.Background(), replaytest.Operation{
		Kind: replaytest.OperationWriteMemory,
		Memory: &replaytest.MemorySnapshot{
			AppName: replayAppName, UserID: replayUserID, Content: "memory",
			Metadata: map[string]any{"source": "import"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), `memory metadata "source" is unsupported`) {
		t.Fatalf("fixture.Apply() error = %v", err)
	}
}

func TestReplayFixtureSnapshotReadsAllMemories(t *testing.T) {
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
	const memoryCount = 105
	for i := 0; i < memoryCount; i++ {
		err := fixture.Apply(context.Background(), replaytest.Operation{
			Kind: replaytest.OperationWriteMemory,
			Memory: &replaytest.MemorySnapshot{
				AppName: replayAppName, UserID: replayUserID,
				Content: fmt.Sprintf("memory-%03d", i),
			},
		})
		if err != nil {
			t.Fatalf("write memory %d: %v", i, err)
		}
	}

	snapshot, err := fixture.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("fixture.Snapshot() error = %v", err)
	}
	if len(snapshot.Memories) != memoryCount {
		t.Fatalf("snapshot memory count = %d, want %d", len(snapshot.Memories), memoryCount)
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

var errUncertainCreate = errors.New("uncertain session creation")

type uncertainCreateSessionService struct {
	session.Service
	mu            sync.Mutex
	deleted       []session.Key
	committed     chan struct{}
	release       <-chan struct{}
	deletedSignal chan<- session.Key
}

type closeErrorSessionService struct {
	session.Service
	err error
}

func (service *closeErrorSessionService) Close() error {
	return errors.Join(service.Service.Close(), service.err)
}

func (service *uncertainCreateSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	sess, err := service.Service.CreateSession(ctx, key, state, options...)
	if err != nil {
		return nil, err
	}
	if service.committed != nil {
		close(service.committed)
	}
	if service.release != nil {
		select {
		case <-service.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return sess, errUncertainCreate
}

func (service *uncertainCreateSessionService) DeleteSession(
	ctx context.Context,
	key session.Key,
	options ...session.Option,
) error {
	service.mu.Lock()
	service.deleted = append(service.deleted, key)
	service.mu.Unlock()
	if service.deletedSignal != nil {
		select {
		case service.deletedSignal <- key:
		default:
		}
	}
	return service.Service.DeleteSession(ctx, key, options...)
}

func (service *uncertainCreateSessionService) deletedKeys() []session.Key {
	service.mu.Lock()
	defer service.mu.Unlock()
	return append([]session.Key(nil), service.deleted...)
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

func TestEventConversionRoundTripsUnambiguousStateKinds(t *testing.T) {
	binary := []byte{0xff, 0xfe}
	want := &replaytest.EventSnapshot{
		ID:           "event-state",
		InvocationID: "invocation-state",
		Author:       "assistant",
		Role:         "assistant",
		Object:       "chat.completion",
		Done:         true,
		StateDelta: map[string]replaytest.StateValueSnapshot{
			"text":   replaytest.TextStateValue("not-json"),
			"binary": replaytest.BinaryStateValue(binary),
		},
	}
	evt, err := toEvent(want)
	if err != nil {
		t.Fatalf("toEvent() error = %v", err)
	}
	got := toEventSnapshot(evt, false)
	if got.StateDelta["text"].Kind != replaytest.StateValueText {
		t.Fatalf("text state kind = %q, want %q", got.StateDelta["text"].Kind, replaytest.StateValueText)
	}
	if got.StateDelta["binary"].Kind != replaytest.StateValueBinary {
		t.Fatalf("binary state kind = %q, want %q", got.StateDelta["binary"].Kind, replaytest.StateValueBinary)
	}
	if !reflect.DeepEqual(got.StateDelta, want.StateDelta) {
		t.Fatalf("state delta = %#v, want %#v", got.StateDelta, want.StateDelta)
	}
}

func TestEventConversionRejectsAmbiguousStateKinds(t *testing.T) {
	tests := []struct {
		name  string
		value replaytest.StateValueSnapshot
	}{
		{name: "text null", value: replaytest.TextStateValue("null")},
		{name: "text JSON object", value: replaytest.TextStateValue(`{"kind":"text"}`)},
		{name: "binary null", value: replaytest.BinaryStateValue([]byte("null"))},
		{name: "binary JSON object", value: replaytest.BinaryStateValue([]byte(`{"kind":"binary"}`))},
		{name: "binary UTF-8 text", value: replaytest.BinaryStateValue([]byte("not-json"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := replaytest.Operation{
				Kind:      replaytest.OperationAppendEvent,
				SessionID: "session-state",
				Event: &replaytest.EventSnapshot{
					StateDelta: map[string]replaytest.StateValueSnapshot{
						"state": test.value,
					},
				},
			}
			err := validateReplayAdapterOperation(operation)
			if err == nil || !strings.Contains(err.Error(), "cannot round-trip") {
				t.Fatalf("validateReplayAdapterOperation() error = %v, want cannot round-trip", err)
			}
		})
	}
}

func TestEventConversionPreservesRawJSONPayloads(t *testing.T) {
	want := &replaytest.EventSnapshot{
		ID:           "event-raw",
		InvocationID: "invocation-raw",
		Author:       "assistant",
		Role:         "assistant",
		Object:       "chat.completion",
		Done:         true,
		ToolCalls: []replaytest.ToolCallSnapshot{{
			ID:        "call-1",
			Name:      "weather",
			Arguments: pointerToBytes([]byte(`{"city":"Shenzhen"}`)),
			Extra: map[string]any{
				"raw":            []byte(`{"provider_status":"ok"}`),
				"raw_message":    json.RawMessage(`[true]`),
				"nested":         map[string]any{"raw": []byte(`{"inner":true}`)},
				"pointer_nested": pointerToAnyMap(map[string]any{"raw": []byte(`{"inner":true}`)}),
			},
		}},
		Extensions: map[string]any{
			"raw":            []byte(`{"value":1}`),
			"raw_message":    json.RawMessage(`[true]`),
			"nested":         map[string]any{"raw": []byte(`{"inner":true}`)},
			"pointer_nested": pointerToAnyMap(map[string]any{"raw": []byte(`{"inner":true}`)}),
			"custom_map": pointerToCustomMarshalerMap(
				customMarshalerMap{"raw": []byte(`{"inner":true}`)},
			),
			"custom_slice": pointerToCustomMarshalerSlice(
				customMarshalerSlice{[]byte(`{"inner":true}`)},
			),
			"value_custom_map": customValueMarshalerMap{
				"raw": []byte(`{"inner":true}`),
			},
			"value_custom_slice": customValueMarshalerSlice{
				[]byte(`{"inner":true}`),
			},
		},
		ToolResponse: &replaytest.ToolResponse{
			ToolCallID: "call-1",
			Name:       "weather",
			Content:    "sunny",
			Extra: map[string]any{
				"raw":            []byte(`{"provider_status":"ok"}`),
				"pointer_nested": pointerToAnyMap(map[string]any{"raw": []byte(`{"inner":true}`)}),
			},
		},
	}
	evt, err := toEvent(want)
	if err != nil {
		t.Fatalf("toEvent() error = %v", err)
	}
	if got := string(evt.Response.Choices[0].Message.ToolCalls[0].Function.Arguments); got != `{"city":"Shenzhen"}` {
		t.Fatalf("tool arguments = %q, want raw JSON object", got)
	}
	encodedToolCall, err := json.Marshal(evt.Response.Choices[0].Message.ToolCalls[0])
	if err != nil {
		t.Fatalf("marshal tool call: %v", err)
	}
	var decodedToolCall struct {
		ExtraFields map[string]any `json:"extra_fields"`
	}
	if err := decodeJSONUseNumber(encodedToolCall, &decodedToolCall); err != nil {
		t.Fatalf("decode encoded tool call: %v", err)
	}
	if got, want := decodedToolCall.ExtraFields["raw"],
		map[string]any{"provider_status": "ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call raw extra = %#v, want %#v", got, want)
	}
	if got, want := decodedToolCall.ExtraFields["raw_message"], []any{true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call raw message extra = %#v, want %#v", got, want)
	}
	if got, want := decodedToolCall.ExtraFields["nested"],
		map[string]any{"raw": map[string]any{"inner": true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call nested raw extra = %#v, want %#v", got, want)
	}
	if got, want := decodedToolCall.ExtraFields["pointer_nested"],
		map[string]any{"raw": map[string]any{"inner": true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call pointer nested raw extra = %#v, want %#v", got, want)
	}
	if got := string(evt.Extensions["raw"]); got != `{"value":1}` {
		t.Fatalf("raw extension = %q, want raw JSON object", got)
	}
	if got := string(evt.Extensions["raw_message"]); got != `[true]` {
		t.Fatalf("raw message extension = %q, want raw JSON array", got)
	}
	if got := string(evt.Extensions["nested"]); got != `{"raw":{"inner":true}}` {
		t.Fatalf("nested raw extension = %q, want nested raw JSON object", got)
	}
	if got := string(evt.Extensions["pointer_nested"]); got != `{"raw":{"inner":true}}` {
		t.Fatalf("pointer nested raw extension = %q, want nested raw JSON object", got)
	}
	if got := string(evt.Extensions["custom_map"]); got != `{"custom":"map"}` {
		t.Fatalf("custom marshaler map extension = %q, want custom JSON", got)
	}
	if got := string(evt.Extensions["custom_slice"]); got != `["custom","slice"]` {
		t.Fatalf("custom marshaler slice extension = %q, want custom JSON", got)
	}
	if got := string(evt.Extensions["value_custom_map"]); got != `{"custom":"value-map"}` {
		t.Fatalf("custom value marshaler map extension = %q, want custom JSON", got)
	}
	if got := string(evt.Extensions["value_custom_slice"]); got != `["custom","value-slice"]` {
		t.Fatalf("custom value marshaler slice extension = %q, want custom JSON", got)
	}
	if got := string(evt.Extensions[toolResponseExtraExtensionKey]); got !=
		`{"pointer_nested":{"raw":{"inner":true}},"raw":{"provider_status":"ok"}}` {
		t.Fatalf("tool response extra = %q, want nested raw JSON object", got)
	}
}

func TestEventConversionUsesEncodedReservedExtensionContract(t *testing.T) {
	event := &replaytest.EventSnapshot{
		ID: "event-marshaler", Author: "assistant", Role: "assistant",
		Object: "chat.completion", Done: true,
		Extensions: map[string]any{
			replayAppName: pointerToCustomMarshalerMap(customMarshalerMap{
				"tool_response_extra": "not-on-wire",
			}),
		},
	}
	evt, err := toEvent(event)
	if err != nil {
		t.Fatalf("toEvent() error = %v", err)
	}
	if got := string(evt.Extensions[replayAppName]); got != `{"custom":"map"}` {
		t.Fatalf("encoded namespace = %q, want custom marshaler output", got)
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

func TestTakeToolResponseExtraPreservesWrongTypeReservedValues(t *testing.T) {
	extensions := map[string]any{
		toolResponseExtraExtensionKey: "wrong-type",
	}
	if got := takeToolResponseExtra(extensions, true); got != nil {
		t.Fatalf("takeToolResponseExtra() = %#v, want nil", got)
	}
	if extensions[toolResponseExtraExtensionKey] != "wrong-type" {
		t.Fatalf("top-level wrong type was deleted: %#v", extensions)
	}

	extensions = map[string]any{
		replayAppName: map[string]any{"tool_response_extra": "wrong-type"},
	}
	if got := takeToolResponseExtra(extensions, true); got != nil {
		t.Fatalf("nested takeToolResponseExtra() = %#v, want nil", got)
	}
	if !reflect.DeepEqual(extensions, map[string]any{
		replayAppName: map[string]any{"tool_response_extra": "wrong-type"},
	}) {
		t.Fatalf("nested wrong type was deleted: %#v", extensions)
	}
}

func TestEventSnapshotPreservesReservedExtensionOnNonToolEvent(t *testing.T) {
	evt := event.NewResponseEvent("invocation-1", "assistant", nil)
	evt.Extensions = map[string]json.RawMessage{
		toolResponseExtraExtensionKey: json.RawMessage(`{"provider_status":"user-value"}`),
	}
	got := toEventSnapshot(evt, false)
	if !reflect.DeepEqual(got.Extensions, map[string]any{
		toolResponseExtraExtensionKey: map[string]any{"provider_status": "user-value"},
	}) {
		t.Fatalf("reserved non-tool extension was consumed: %#v", got.Extensions)
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
			name: "invalid raw tool arguments",
			event: &replaytest.EventSnapshot{ToolCalls: []replaytest.ToolCallSnapshot{{
				ID: "call-1", Arguments: []byte(`{"city"`),
			}}},
			want: "invalid raw JSON",
		},
		{
			name: "invalid raw tool call extra",
			event: &replaytest.EventSnapshot{ToolCalls: []replaytest.ToolCallSnapshot{{
				ID:    "call-1",
				Extra: map[string]any{"raw": []byte(`{"bad"`)},
			}}},
			want: "invalid raw JSON",
		},
		{
			name: "cyclic tool arguments",
			event: &replaytest.EventSnapshot{ToolCalls: []replaytest.ToolCallSnapshot{{
				ID: "call-1", Arguments: cyclicMap(),
			}}},
			want: "cyclic",
		},
		{
			name: "cyclic tool call extra",
			event: &replaytest.EventSnapshot{ToolCalls: []replaytest.ToolCallSnapshot{{
				ID:    "call-1",
				Extra: map[string]any{"cycle": cyclicSlice()},
			}}},
			want: "cyclic",
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
			name:  "cyclic extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{"cycle": cyclicMap()}},
			want:  "cyclic",
		},
		{
			name:  "invalid raw extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{"raw": json.RawMessage(`{"bad"`)}},
			want:  "invalid raw JSON",
		},
		{
			name:  "invalid byte extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{"raw": []byte(`{"bad"`)}},
			want:  "invalid raw JSON",
		},
		{
			name: "invalid tool response extra bytes",
			event: &replaytest.EventSnapshot{ToolResponse: &replaytest.ToolResponse{
				Extra: map[string]any{"raw": []byte(`{"bad"`)},
			}},
			want: "invalid raw JSON",
		},
		{
			name: "reserved extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				toolResponseExtraExtensionKey: map[string]any{"provider_status": "user-value"},
			}},
			want: "reserved",
		},
		{
			name: "nested reserved extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: map[string]string{"tool_response_extra": "user-value"},
			}},
			want: "reserved",
		},
		{
			name: "raw nested reserved extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: json.RawMessage(`{"tool_response_extra":{"provider_status":"user-value"}}`),
			}},
			want: "reserved",
		},
		{
			name: "byte nested reserved extension",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: []byte(`{"tool_response_extra":{"provider_status":"user-value"}}`),
			}},
			want: "reserved",
		},
		{
			name: "tool response extra",
			event: &replaytest.EventSnapshot{ToolResponse: &replaytest.ToolResponse{
				Extra: map[string]any{"bad": make(chan int)},
			}},
			want: "tool response extra",
		},
		{
			name: "cyclic tool response extra",
			event: &replaytest.EventSnapshot{ToolResponse: &replaytest.ToolResponse{
				Extra: map[string]any{"cycle": cyclicMap()},
			}},
			want: "cyclic",
		},
		{
			name: "nested reserved extension with named string key",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: map[namedExtensionKey]any{
					"tool_response_extra": "user-value",
				},
			}},
			want: "reserved",
		},
		{
			name: "nested reserved extension through pointer",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: pointerToMap(map[namedExtensionKey]any{
					"tool_response_extra": "user-value",
				}),
			}},
			want: "reserved",
		},
		{
			name: "nested reserved extension through raw pointer",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: pointerToRawMessage(
					json.RawMessage(`{"tool_response_extra":"user-value"}`),
				),
			}},
			want: "reserved",
		},
		{
			name: "nested reserved extension through marshaler",
			event: &replaytest.EventSnapshot{Extensions: map[string]any{
				replayAppName: reservedExtensionPayload{},
			}},
			want: "reserved",
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

type namedExtensionKey string

type reservedExtensionPayload struct{}

func (reservedExtensionPayload) MarshalJSON() ([]byte, error) {
	return []byte(`{"tool_response_extra":"user-value"}`), nil
}

type customMarshalerMap map[string]any

func (*customMarshalerMap) MarshalJSON() ([]byte, error) {
	return []byte(`{"custom":"map"}`), nil
}

type customMarshalerSlice []any

func (*customMarshalerSlice) MarshalJSON() ([]byte, error) {
	return []byte(`["custom","slice"]`), nil
}

type customValueMarshalerMap map[string]any

func (customValueMarshalerMap) MarshalJSON() ([]byte, error) {
	return []byte(`{"custom":"value-map"}`), nil
}

type customValueMarshalerSlice []any

func (customValueMarshalerSlice) MarshalJSON() ([]byte, error) {
	return []byte(`["custom","value-slice"]`), nil
}

func cyclicMap() map[string]any {
	value := map[string]any{}
	value["self"] = value
	return value
}

func cyclicSlice() []any {
	value := make([]any, 1)
	value[0] = value
	return value
}

func pointerToBytes(value []byte) *[]byte {
	return &value
}

func pointerToRawMessage(value json.RawMessage) *json.RawMessage {
	return &value
}

func pointerToAnyMap(value map[string]any) *map[string]any {
	return &value
}

func pointerToCustomMarshalerMap(value customMarshalerMap) *customMarshalerMap {
	return &value
}

func pointerToCustomMarshalerSlice(value customMarshalerSlice) *customMarshalerSlice {
	return &value
}

func pointerToMap(value map[namedExtensionKey]any) *map[namedExtensionKey]any {
	return &value
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
	if _, err := toMemoryMetadata(map[string]any{"source": "import"}); err == nil ||
		!strings.Contains(err.Error(), `memory metadata "source" is unsupported`) {
		t.Fatalf("toMemoryMetadata() unsupported key error = %v", err)
	}
}
