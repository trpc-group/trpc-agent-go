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

func TestRedisSessionNativeProviderDataSurvivesStorageAndSkipsMalformedLegacyJSON(t *testing.T) {
	for _, storage := range replayCorruptionStorages {
		t.Run(storage.name, func(t *testing.T) {
			fixture := newProviderDataCorruptionFixture(t, storage)
			fixture.addMalformedLegacyJSON(t)

			got, err := fixture.service.GetSession(fixture.ctx, fixture.key)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Len(t, got.Events, len(fixture.events),
				"invalid legacy JSON remains skippable without dropping valid history")

			byID := indexProviderDataCorruptionEvents(t, got.Events)
			require.NotContains(t, byID, "malformed-legacy-json")

			reasoningOnly := replayCorruptionMessage(t, byID[replayOnlyEventID])
			require.Empty(t, reasoningOnly.Content)
			require.Empty(t, reasoningOnly.ReasoningContent)
			require.Empty(t, reasoningOnly.ToolCalls)
			require.True(t, model.HasPayload(reasoningOnly),
				"the native-only event is meaningful even with no visible text")
			assertProviderDataCorruptionPayload(t, reasoningOnly, fixture.expectedPayload[replayOnlyEventID])

			nativeCall := replayCorruptionMessage(t, byID[nativeToolCallEventID])
			require.Len(t, nativeCall.ToolCalls, 1)
			require.Equal(t, "call-native", nativeCall.ToolCalls[0].ID)
			assertProviderDataCorruptionPayload(t, nativeCall, fixture.expectedPayload[nativeToolCallEventID])
			require.Equal(t, "call-native", replayCorruptionMessage(t, byID["native-tool-result"]).ToolID)

			legacyCall := replayCorruptionMessage(t, byID[legacyToolCallEventID])
			require.Nil(t, legacyCall.ProviderData, "legacy Chat tool calls stay ordinary projections")
			require.Equal(t, "call-legacy", replayCorruptionMessage(t, byID["legacy-tool-result"]).ToolID)
		})
	}
}

func TestRedisSessionGetSessionSkipsMalformedNativeProviderDataRecords(t *testing.T) {
	for _, storage := range replayCorruptionStorages {
		for _, eventID := range []string{replayOnlyEventID, nativeToolCallEventID} {
			t.Run(storage.name+"/"+eventID, func(t *testing.T) {
				fixture := newProviderDataCorruptionFixture(t, storage)
				fixture.addMalformedLegacyJSON(t)
				fixture.mutatePersistedProviderData(t, eventID, func(_ *testing.T, record map[string]json.RawMessage) {
					record["payload"] = json.RawMessage(`null`)
				})

				got, err := fixture.service.GetSession(fixture.ctx, fixture.key)
				require.NoError(t, err)
				require.NotNil(t, got)
				require.Len(t, got.Events, len(fixture.events)-1)
				byID := indexProviderDataCorruptionEvents(t, got.Events)
				require.NotContains(t, byID, eventID)
				for _, original := range fixture.events {
					if original.ID != eventID {
						require.Contains(t, byID, original.ID, "valid history remains readable")
					}
				}
			})
		}
	}
}

func newProviderDataCorruptionFixture(
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
		AppName:   "record-corruption",
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
	reasoningOnly.ProviderData = &model.ProviderData{
		Protocol: replayCorruptionProvider,
		Model:    replayCorruptionModel,
		Payload:  reasoningPayload,
	}

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
	nativeToolCall.ProviderData = &model.ProviderData{
		Protocol: replayCorruptionProvider,
		Model:    replayCorruptionModel,
		Payload:  nativeToolPayload,
	}

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
		{id: "malformed-legacy-null-record", raw: `{"id":"malformed-legacy-null-record","choices":[{"message":{"role":7,"provider_data":null}}]}`},
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

func (f *replayCorruptionFixture) mutatePersistedProviderData(
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
	var record map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(message["provider_data"], &record))
	require.NotEmpty(t, record)

	mutate(t, record)
	message["provider_data"], _ = json.Marshal(record)
	choice["message"], _ = json.Marshal(message)
	choices[0], _ = json.Marshal(choice)
	encodedEvent["choices"], _ = json.Marshal(choices)
	updated, err := json.Marshal(encodedEvent)
	require.NoError(t, err)
	require.True(t, json.Valid(updated), "only the nested ProviderData JSON changes")

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

func indexProviderDataCorruptionEvents(t *testing.T, events []event.Event) map[string]event.Event {
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

func assertProviderDataCorruptionPayload(t *testing.T, message model.Message, expected json.RawMessage) {
	t.Helper()
	require.NotNil(t, message.ProviderData)
	actual := message.ProviderData.Payload
	require.JSONEq(t, string(expected), string(actual))
}
