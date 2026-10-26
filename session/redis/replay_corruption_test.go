//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/zset"
)

const (
	replayCorruptionProvider = "openai.responses"
	replayCorruptionModel    = "test-model"
	replayOnlyEventID        = "native-reasoning-only"
	nativeToolCallEventID    = "native-tool-call"
	legacyToolCallEventID    = "legacy-tool-call"
)

type replayCorruptionStorage struct {
	name string
	mode CompatMode
}

var replayCorruptionStorages = []replayCorruptionStorage{
	{name: util.StorageTypeZset, mode: CompatModeTransition},
	{name: util.StorageTypeHashIdx, mode: CompatModeNone},
}

type replayCorruptionFixture struct {
	ctx             context.Context
	storage         replayCorruptionStorage
	service         *Service
	client          *redis.Client
	key             session.Key
	events          []*event.Event
	expectedPayload map[string]json.RawMessage
}

func TestRedisSessionNativeReplaySurvivesStorageAndSkipsMalformedLegacyJSON(t *testing.T) {
	for _, storage := range replayCorruptionStorages {
		t.Run(storage.name, func(t *testing.T) {
			fixture := newReplayCorruptionFixture(t, storage)
			fixture.addMalformedLegacyJSON(t)

			got, err := fixture.service.GetSession(fixture.ctx, fixture.key)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Len(t, got.Events, len(fixture.events),
				"invalid legacy JSON remains skippable without dropping valid history")

			byID := indexReplayCorruptionEvents(t, got.Events)
			require.NotContains(t, byID, "malformed-legacy-json")

			reasoningOnly := replayCorruptionMessage(t, byID[replayOnlyEventID])
			require.Empty(t, reasoningOnly.Content)
			require.Empty(t, reasoningOnly.ReasoningContent)
			require.Empty(t, reasoningOnly.ToolCalls)
			require.True(t, model.HasPayload(reasoningOnly),
				"the native-only event is meaningful even with no visible text")
			assertReplayCorruptionPayload(t, reasoningOnly, fixture.expectedPayload[replayOnlyEventID])

			nativeCall := replayCorruptionMessage(t, byID[nativeToolCallEventID])
			require.Len(t, nativeCall.ToolCalls, 1)
			require.Equal(t, "call-native", nativeCall.ToolCalls[0].ID)
			assertReplayCorruptionPayload(t, nativeCall, fixture.expectedPayload[nativeToolCallEventID])
			require.Equal(t, "call-native", replayCorruptionMessage(t, byID["native-tool-result"]).ToolID)

			legacyCall := replayCorruptionMessage(t, byID[legacyToolCallEventID])
			require.Nil(t, legacyCall.Replay, "legacy Chat tool calls stay ordinary projections")
			require.Equal(t, "call-legacy", replayCorruptionMessage(t, byID["legacy-tool-result"]).ToolID)
		})
	}
}

func TestRedisSessionGetSessionRejectsMalformedNativeReplayRecords(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*testing.T, map[string]json.RawMessage)
		wantErrMsg string
	}{
		{
			name: "unknown version",
			mutate: func(_ *testing.T, replay map[string]json.RawMessage) {
				replay["version"] = json.RawMessage(`99`)
			},
			wantErrMsg: "unsupported replay data version 99",
		},
		{
			name: "invalid fingerprint encoding",
			mutate: func(_ *testing.T, replay map[string]json.RawMessage) {
				replay["projection_sha256"] = json.RawMessage(`"not-a-sha256"`)
			},
			wantErrMsg: "invalid replay message fingerprint",
		},
	}

	for _, storage := range replayCorruptionStorages {
		for _, tt := range tests {
			for _, eventID := range []string{replayOnlyEventID, nativeToolCallEventID} {
				t.Run(storage.name+"/"+eventID+"/"+tt.name, func(t *testing.T) {
					fixture := newReplayCorruptionFixture(t, storage)
					fixture.addMalformedLegacyJSON(t)
					fixture.mutatePersistedReplay(t, eventID, tt.mutate)

					got, err := fixture.service.GetSession(fixture.ctx, fixture.key)
					if err == nil {
						ids := replayCorruptionEventIDs(got)
						t.Errorf("GetSession silently skipped the malformed native replay event; loaded events: %v", ids)
						return
					}
					require.ErrorContains(t, err, tt.wantErrMsg)
					require.Nil(t, got, "a failed native decode must not expose a partial session")
				})
			}
		}
	}
}

func TestRedisSessionGetSessionDefersValidFingerprintMismatchToReplayValidation(t *testing.T) {
	for _, storage := range replayCorruptionStorages {
		t.Run(storage.name, func(t *testing.T) {
			fixture := newReplayCorruptionFixture(t, storage)
			fixture.addMalformedLegacyJSON(t)
			fixture.mutatePersistedReplay(t, replayOnlyEventID, func(
				t *testing.T,
				replay map[string]json.RawMessage,
			) {
				var current string
				require.NoError(t, json.Unmarshal(replay["projection_sha256"], &current))
				replacement := strings.Repeat("0", 64)
				if replacement == current {
					replacement = strings.Repeat("1", 64)
				}
				replay["projection_sha256"], _ = json.Marshal(replacement)
			})

			got, err := fixture.service.GetSession(fixture.ctx, fixture.key)
			require.NoError(t, err, "a syntactically valid record is decoded by the storage layer")
			require.NotNil(t, got)
			require.Len(t, got.Events, len(fixture.events))
			byID := indexReplayCorruptionEvents(t, got.Events)
			require.Contains(t, byID, replayOnlyEventID)
			assertReplayCorruptionPayload(t,
				replayCorruptionMessage(t, byID[nativeToolCallEventID]),
				fixture.expectedPayload[nativeToolCallEventID],
			)

			_, err = replayCorruptionMessage(t, byID[replayOnlyEventID]).ReplayDataFor(
				replayCorruptionProvider,
				replayCorruptionModel,
			)
			require.ErrorContains(t, err, "message projection changed after native replay data was bound")
		})
	}
}

func newReplayCorruptionFixture(
	t *testing.T,
	storage replayCorruptionStorage,
) *replayCorruptionFixture {
	t.Helper()
	redisURL, cleanupRedis := setupTestRedis(t)
	t.Cleanup(cleanupRedis)
	service, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(storage.mode),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })

	client := buildRedisClient(t, redisURL)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	ctx := context.Background()
	key := session.Key{
		AppName:   "replay-corruption",
		UserID:    "user",
		SessionID: storage.name + "-session",
	}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)
	require.Equal(t, storage.name, sess.ServiceMeta[util.ServiceMetaStorageTypeKey])

	reasoningPayload := json.RawMessage(`[
		{"type":"reasoning","id":"rs_only","summary":[],"encrypted_content":"opaque-reasoning-only"}
	]`)
	reasoningOnly := model.Message{Role: model.RoleAssistant}
	require.NoError(t, reasoningOnly.SetReplayData(
		replayCorruptionProvider,
		replayCorruptionModel,
		reasoningPayload,
	))

	nativeToolPayload := json.RawMessage(`[
		{"type":"reasoning","id":"rs_tool","summary":[],"encrypted_content":"opaque-tool-state"},
		{"type":"message","id":"msg_tool","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"Looking up."}]},
		{"type":"function_call","id":"fc_native","call_id":"call-native","name":"lookup","arguments":"{\"q\":\"native\"}"}
	]`)
	nativeToolCall := model.Message{
		Role:    model.RoleAssistant,
		Content: "Looking up.",
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call-native",
			Function: model.FunctionDefinitionParam{
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"native"}`),
			},
		}},
	}
	require.NoError(t, nativeToolCall.SetReplayData(
		replayCorruptionProvider,
		replayCorruptionModel,
		nativeToolPayload,
	))

	legacyToolCall := model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call-legacy",
			Function: model.FunctionDefinitionParam{
				Name:      "legacy_lookup",
				Arguments: json.RawMessage(`{"q":"legacy"}`),
			},
		}},
	}

	responses := []struct {
		id      string
		author  string
		request string
		message model.Message
	}{
		{id: "user-initial", author: "user", request: "req-1", message: model.NewUserMessage("start")},
		{id: replayOnlyEventID, author: "assistant", request: "req-1", message: reasoningOnly},
		{id: "user-native-tool", author: "user", request: "req-2", message: model.NewUserMessage("native lookup")},
		{id: nativeToolCallEventID, author: "assistant", request: "req-2", message: nativeToolCall},
		{id: "native-tool-result", author: "tool", request: "req-2", message: model.NewToolMessage("call-native", "lookup", "native result")},
		{id: "user-legacy-tool", author: "user", request: "req-3", message: model.NewUserMessage("legacy lookup")},
		{id: legacyToolCallEventID, author: "assistant", request: "req-3", message: legacyToolCall},
		{id: "legacy-tool-result", author: "tool", request: "req-3", message: model.NewToolMessage("call-legacy", "legacy_lookup", "legacy result")},
	}
	events := make([]*event.Event, 0, len(responses))
	expectedPayload := map[string]json.RawMessage{
		replayOnlyEventID:     reasoningPayload,
		nativeToolCallEventID: nativeToolPayload,
	}
	baseTime := time.Now().UTC().Add(-time.Minute)
	for i, item := range responses {
		response := &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
			Choices: []model.Choice{{
				Index:   0,
				Message: item.message,
			}},
		}
		evt := event.NewResponseEvent("inv-"+item.request, item.author, response)
		evt.ID = item.id
		evt.RequestID = item.request
		evt.Timestamp = baseTime.Add(time.Duration(i+1) * time.Second)
		require.NoError(t, service.AppendEvent(ctx, sess, evt))
		events = append(events, evt)
	}

	return &replayCorruptionFixture{
		ctx:             ctx,
		storage:         storage,
		service:         service,
		client:          client,
		key:             key,
		events:          events,
		expectedPayload: expectedPayload,
	}
}

func (f *replayCorruptionFixture) addMalformedLegacyJSON(t *testing.T) {
	t.Helper()
	when := float64(time.Now().Add(time.Hour).UnixNano())
	for _, record := range []struct{ id, raw string }{
		{id: "malformed-legacy-json", raw: "not valid legacy event JSON"},
		{id: "malformed-legacy-field", raw: `{"id":"malformed-legacy-field","choices":[{"message":{"role":7}}]}`},
		{id: "malformed-legacy-null-replay", raw: `{"id":"malformed-legacy-null-replay","choices":[{"message":{"role":7,"replay":null}}]}`},
	} {
		if f.storage.name == util.StorageTypeZset {
			err := f.client.ZAdd(f.ctx, zset.GetEventKey(f.key), redis.Z{
				Score:  when,
				Member: record.raw,
			}).Err()
			require.NoError(t, err)
			continue
		}
		eventDataKey := hashidx.GetEventDataKey("", f.key)
		eventTimeKey := hashidx.GetEventTimeIndexKey("", f.key)
		require.NoError(t, f.client.HSet(f.ctx, eventDataKey, record.id, record.raw).Err())
		require.NoError(t, f.client.ZAdd(f.ctx, eventTimeKey, redis.Z{
			Score:  when,
			Member: record.id,
		}).Err())
	}
}

func (f *replayCorruptionFixture) mutatePersistedReplay(
	t *testing.T,
	eventID string,
	mutate func(*testing.T, map[string]json.RawMessage),
) {
	t.Helper()
	raw, score := f.persistedEventRaw(t, eventID)
	var encodedEvent map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &encodedEvent))

	var choices []json.RawMessage
	require.NoError(t, json.Unmarshal(encodedEvent["choices"], &choices))
	require.NotEmpty(t, choices)
	var choice map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(choices[0], &choice))
	var message map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(choice["message"], &message))
	var replay map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(message["replay"], &replay))
	require.NotEmpty(t, replay)

	mutate(t, replay)
	message["replay"], _ = json.Marshal(replay)
	choice["message"], _ = json.Marshal(message)
	choices[0], _ = json.Marshal(choice)
	encodedEvent["choices"], _ = json.Marshal(choices)
	updated, err := json.Marshal(encodedEvent)
	require.NoError(t, err)
	require.True(t, json.Valid(updated), "only the nested Replay JSON changes")

	if f.storage.name == util.StorageTypeZset {
		eventKey := zset.GetEventKey(f.key)
		require.NoError(t, f.client.ZRem(f.ctx, eventKey, raw).Err())
		require.NoError(t, f.client.ZAdd(f.ctx, eventKey, redis.Z{
			Score:  score,
			Member: string(updated),
		}).Err())
		return
	}
	require.NoError(t, f.client.HSet(
		f.ctx,
		hashidx.GetEventDataKey("", f.key),
		eventID,
		string(updated),
	).Err())
}

func (f *replayCorruptionFixture) persistedEventRaw(t *testing.T, eventID string) (string, float64) {
	t.Helper()
	if f.storage.name == util.StorageTypeZset {
		members, err := f.client.ZRangeWithScores(f.ctx, zset.GetEventKey(f.key), 0, -1).Result()
		require.NoError(t, err)
		for _, member := range members {
			raw, ok := member.Member.(string)
			require.True(t, ok)
			var stored struct {
				ID string `json:"id"`
			}
			if json.Unmarshal([]byte(raw), &stored) == nil && stored.ID == eventID {
				return raw, member.Score
			}
		}
		t.Fatalf("zset event %q not found", eventID)
	}
	raw, err := f.client.HGet(f.ctx, hashidx.GetEventDataKey("", f.key), eventID).Result()
	require.NoError(t, err)
	return raw, 0
}

func indexReplayCorruptionEvents(t *testing.T, events []event.Event) map[string]event.Event {
	t.Helper()
	byID := make(map[string]event.Event, len(events))
	for _, evt := range events {
		byID[evt.ID] = evt
	}
	return byID
}

func replayCorruptionMessage(t *testing.T, evt event.Event) model.Message {
	t.Helper()
	require.NotNil(t, evt.Response)
	require.NotEmpty(t, evt.Response.Choices)
	return evt.Response.Choices[0].Message
}

func assertReplayCorruptionPayload(t *testing.T, message model.Message, expected json.RawMessage) {
	t.Helper()
	actual, err := message.ReplayDataFor(replayCorruptionProvider, replayCorruptionModel)
	require.NoError(t, err)
	require.JSONEq(t, string(expected), string(actual))
}

func replayCorruptionEventIDs(sess *session.Session) []string {
	if sess == nil {
		return nil
	}
	ids := make([]string, 0, len(sess.Events))
	for _, evt := range sess.Events {
		ids = append(ids, evt.ID)
	}
	return ids
}
