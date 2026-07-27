//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestRecordRoundTripPreservesMetadata(t *testing.T) {
	eventTime := time.Unix(0, 1730500123456789012).UTC()
	createdAt := time.Unix(0, 1730400123456789012).UTC()
	record := newAddRecord(
		recordScope{appName: "app", userID: "user"},
		"Alice met Bob",
		[]string{"meeting", "alice"},
		&memory.Metadata{
			Kind:         memory.KindEpisode,
			EventTime:    &eventTime,
			Participants: []string{"Bob", "Alice"},
			Location:     "Office",
		},
		createdAt,
	)
	encoded, err := json.Marshal(addMetadata(record))
	require.NoError(t, err)
	decodedMetadata := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&decodedMetadata))
	document := record.entry.Memory.Memory

	decoded, err := decodeStoredRecord(record.entry.ID, &document, decodedMetadata)
	require.NoError(t, err)

	assert.True(t, samePersistedRecord(record, decoded))
	assert.Equal(t, []string{"meeting", "alice"}, decoded.entry.Memory.Topics)
	assert.Equal(t, []string{"Alice", "Bob"}, decoded.entry.Memory.Participants)
	assert.Equal(t, eventTime, *decoded.entry.Memory.EventTime)
}

func TestRecordCodecUpdateClearsOptionalMetadataWithNull(t *testing.T) {
	now := time.Now().UTC()
	record := newAddRecord(
		recordScope{appName: "app", userID: "user"},
		"fact",
		nil,
		nil,
		now,
	)

	metadata := updateMetadata(record)

	for _, key := range []string{
		metadataTopicsKey,
		metadataEventTimeKey,
		metadataParticipantsKey,
		metadataLocationKey,
		metadataUpdateTokenKey,
		metadataReplacesIDKey,
	} {
		value, ok := metadata[key]
		assert.True(t, ok, key)
		assert.Nil(t, value, key)
	}
}

func TestRecordCodecRejectsMissingRequiredMetadata(t *testing.T) {
	document := "memory"
	metadata := map[string]any{
		metadataSchemaVersionKey: schemaVersion,
		metadataAppNameKey:       "app",
		metadataUserIDKey:        "user",
	}

	_, err := decodeStoredRecord("id", &document, metadata)

	require.Error(t, err)
	assert.Contains(t, err.Error(), metadataKindKey)
}

func TestRecordCodecRejectsInvalidEnvelope(t *testing.T) {
	document := "memory"
	tests := []struct {
		name     string
		document *string
		metadata map[string]any
		match    string
	}{
		{
			name:     "missing document",
			metadata: validTestMetadata(),
			match:    "has no document",
		},
		{
			name:     "missing metadata",
			document: &document,
			match:    "has no metadata",
		},
		{
			name:     "unsupported schema",
			document: &document,
			metadata: func() map[string]any {
				metadata := validTestMetadata()
				metadata[metadataSchemaVersionKey] = schemaVersion + 1
				return metadata
			}(),
			match: "unsupported schema version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeStoredRecord("id", tt.document, tt.metadata)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

func TestRecordCodecRejectsInconsistentEventTime(t *testing.T) {
	now := time.Now().UTC().UnixNano()
	document := "memory"
	metadata := map[string]any{
		metadataSchemaVersionKey: schemaVersion,
		metadataAppNameKey:       "app",
		metadataUserIDKey:        "user",
		metadataKindKey:          string(memory.KindEpisode),
		metadataHasEventTimeKey:  false,
		metadataEventTimeKey:     now,
		metadataCreatedAtKey:     now,
		metadataUpdatedAtKey:     now,
		metadataDeletedAtKey:     notDeletedAtNS,
	}

	_, err := decodeStoredRecord("id", &document, metadata)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "has_event_time is false")
}

func TestRecordCodecRejectsCorruptOptionalMetadata(t *testing.T) {
	document := "memory"
	metadata := validTestMetadata()
	metadata[metadataLocationKey] = []string{"not", "a", "string"}

	_, err := decodeStoredRecord("id", &document, metadata)

	require.Error(t, err)
	assert.Contains(t, err.Error(), metadataLocationKey)
}

func TestRecordMetadataReaders(t *testing.T) {
	t.Run("required string", func(t *testing.T) {
		_, err := requiredString(map[string]any{}, "key")
		require.ErrorContains(t, err, "is required")
		_, err = requiredString(map[string]any{"key": 1}, "key")
		require.ErrorContains(t, err, "non-empty string")
		_, err = requiredString(map[string]any{"key": ""}, "key")
		require.ErrorContains(t, err, "non-empty string")
		value, err := requiredString(map[string]any{"key": "value"}, "key")
		require.NoError(t, err)
		assert.Equal(t, "value", value)
	})

	t.Run("optional string", func(t *testing.T) {
		value, err := optionalString(map[string]any{}, "key")
		require.NoError(t, err)
		assert.Empty(t, value)
		value, err = optionalString(map[string]any{"key": nil}, "key")
		require.NoError(t, err)
		assert.Empty(t, value)
		_, err = optionalString(map[string]any{"key": 1}, "key")
		require.ErrorContains(t, err, "must be a string")
	})

	t.Run("required bool", func(t *testing.T) {
		_, err := requiredBool(map[string]any{}, "key")
		require.ErrorContains(t, err, "is required")
		_, err = requiredBool(map[string]any{"key": "true"}, "key")
		require.ErrorContains(t, err, "must be a boolean")
		value, err := requiredBool(map[string]any{"key": true}, "key")
		require.NoError(t, err)
		assert.True(t, value)
	})

	t.Run("required integer", func(t *testing.T) {
		_, err := requiredInt64(map[string]any{}, "key")
		require.ErrorContains(t, err, "is required")
		_, err = requiredInt64(map[string]any{"key": "1"}, "key")
		require.ErrorContains(t, err, "must be an integer")
		value, err := requiredInt64(map[string]any{"key": int64(1)}, "key")
		require.NoError(t, err)
		assert.Equal(t, int64(1), value)
	})
}

func TestOptionalStringSlice(t *testing.T) {
	value, err := optionalStringSlice(map[string]any{}, "key")
	require.NoError(t, err)
	assert.Nil(t, value)

	input := []string{"one", "two"}
	value, err = optionalStringSlice(map[string]any{"key": input}, "key")
	require.NoError(t, err)
	assert.Equal(t, input, value)
	input[0] = "changed"
	assert.Equal(t, "one", value[0])

	value, err = optionalStringSlice(map[string]any{
		"key": []any{"one", "two"},
	}, "key")
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, value)

	_, err = optionalStringSlice(map[string]any{"key": []any{"one", 2}}, "key")
	require.ErrorContains(t, err, "item 1")
	_, err = optionalStringSlice(map[string]any{"key": "one"}, "key")
	require.ErrorContains(t, err, "string array")
}

func TestDecodeEventTime(t *testing.T) {
	nanoseconds := int64(1730500123456789012)
	eventTime, err := decodeEventTime(map[string]any{
		metadataHasEventTimeKey: true,
		metadataEventTimeKey:    json.Number("1730500123456789012"),
	})
	require.NoError(t, err)
	require.NotNil(t, eventTime)
	assert.Equal(t, time.Unix(0, nanoseconds).UTC(), *eventTime)

	eventTime, err = decodeEventTime(map[string]any{
		metadataHasEventTimeKey: false,
		metadataEventTimeKey:    nil,
	})
	require.NoError(t, err)
	assert.Nil(t, eventTime)

	_, err = decodeEventTime(map[string]any{metadataHasEventTimeKey: true})
	require.ErrorContains(t, err, "is required")
	_, err = decodeEventTime(map[string]any{
		metadataHasEventTimeKey: true,
		metadataEventTimeKey:    "invalid",
	})
	require.ErrorContains(t, err, metadataEventTimeKey)
}

func TestDecodeTimestampsRejectsMissingFields(t *testing.T) {
	for _, key := range []string{
		metadataCreatedAtKey,
		metadataUpdatedAtKey,
		metadataDeletedAtKey,
	} {
		t.Run(key, func(t *testing.T) {
			metadata := validTestMetadata()
			delete(metadata, key)

			_, _, _, err := decodeTimestamps(metadata)

			require.Error(t, err)
			assert.Contains(t, err.Error(), key)
		})
	}
}

func TestUpdateTokenNormalizesExplicitMetadata(t *testing.T) {
	base := updateCommand{
		key:     memory.Key{AppName: "app", UserID: "user", MemoryID: "old"},
		content: "memory",
		topics:  []string{"topic"},
		metadata: &memory.Metadata{
			Kind:         memory.KindEpisode,
			Participants: []string{" Bob ", "alice", "ALICE"},
			Location:     " office ",
		},
	}
	normalized := base
	normalized.metadata = &memory.Metadata{
		Kind:         memory.KindEpisode,
		Participants: []string{"ALICE", "Bob"},
		Location:     "office",
	}

	left, err := updateToken(base)
	require.NoError(t, err)
	right, err := updateToken(normalized)
	require.NoError(t, err)

	assert.Equal(t, left, right)
}

func TestUpdateTokenIncludesEventTimeAndNilMetadata(t *testing.T) {
	eventTime := time.Unix(0, 1730500123456789012).UTC()
	base := updateCommand{
		key:     memory.Key{AppName: "app", UserID: "user", MemoryID: "old"},
		content: "memory",
	}
	withoutMetadata, err := updateToken(base)
	require.NoError(t, err)

	withMetadata := base
	withMetadata.metadata = &memory.Metadata{
		Kind:      memory.KindEpisode,
		EventTime: &eventTime,
	}
	withEventTime, err := updateToken(withMetadata)
	require.NoError(t, err)

	assert.Len(t, withoutMetadata, 64)
	assert.Len(t, withEventTime, 64)
	assert.NotEqual(t, withoutMetadata, withEventTime)
}

func TestInt64ValueRejectsLossyFloat(t *testing.T) {
	_, err := int64Value(1.5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exact integer")
}

func TestInt64Value(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    int64
		wantErr string
	}{
		{name: "JSON number", value: json.Number("42"), want: 42},
		{name: "invalid JSON number", value: json.Number("1.5"), wantErr: "must be an integer"},
		{name: "int64", value: int64(42), want: 42},
		{name: "int", value: 42, want: 42},
		{name: "float", value: float64(42), want: 42},
		{name: "NaN", value: math.NaN(), wantErr: "must be an int64"},
		{name: "infinity", value: math.Inf(1), wantErr: "must be an int64"},
		{name: "overflow", value: math.MaxFloat64, wantErr: "must be an int64"},
		{name: "wrong type", value: "42", wantErr: "got string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := int64Value(tt.value)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, value)
		})
	}
}

func TestRecordComparison(t *testing.T) {
	scope := recordScope{appName: "app", userID: "user"}
	now := time.Unix(100, 0).UTC()
	left := newAddRecord(scope, "memory", []string{"topic"}, nil, now)
	right := newAddRecord(scope, "memory", []string{"topic"}, nil, now)

	assert.False(t, sameRecordIdentity(nil, right))
	assert.True(t, sameRecordIdentity(left, right))
	assert.True(t, sameRecordState(left, right))
	assert.True(t, samePersistedRecord(left, right))

	right.entry.ID = "different"
	assert.False(t, sameRecordIdentity(left, right))
	right.entry.ID = left.entry.ID

	right.entry.Memory.Topics = []string{"different"}
	assert.False(t, sameRecordState(left, right))
	right.entry.Memory.Topics = []string{"topic"}

	right.deletedAtNS = 1
	assert.False(t, sameRecordState(left, right))
	right.deletedAtNS = left.deletedAtNS
	right.updateToken = "token"
	assert.False(t, sameRecordState(left, right))
	right.updateToken = left.updateToken
	right.replacesID = "old"
	assert.False(t, sameRecordState(left, right))
	right.replacesID = left.replacesID

	right.entry.UpdatedAt = now.Add(time.Second)
	assert.False(t, samePersistedRecord(left, right))
	assert.False(t, moreRecentRecord(left, right))
	assert.True(t, moreRecentRecord(right, left))
	right.entry.UpdatedAt = now

	right.entry.CreatedAt = now.Add(time.Second)
	assert.False(t, samePersistedRecord(left, right))
	assert.False(t, moreRecentRecord(left, right))
	assert.True(t, moreRecentRecord(right, left))
	right.entry.CreatedAt = now

	right.entry.ID = "z"
	left.entry.ID = "a"
	assert.True(t, moreRecentRecord(left, right))
}

func TestSameMemoryIdentity(t *testing.T) {
	eventTime := time.Unix(100, 0).UTC()
	base := &memory.Memory{
		Memory:       "memory",
		Kind:         memory.KindEpisode,
		EventTime:    &eventTime,
		Participants: []string{"Alice"},
		Location:     "office",
	}
	copy := *base
	copy.Participants = append([]string(nil), base.Participants...)
	copyTime := eventTime
	copy.EventTime = &copyTime

	assert.False(t, sameMemoryIdentity(nil, &copy))
	assert.True(t, sameMemoryIdentity(base, &copy))

	copy.Memory = "different"
	assert.False(t, sameMemoryIdentity(base, &copy))
	copy.Memory = base.Memory
	copy.Location = "different"
	assert.False(t, sameMemoryIdentity(base, &copy))
	copy.Location = base.Location
	copy.Participants = []string{"Bob"}
	assert.False(t, sameMemoryIdentity(base, &copy))
	copy.Participants = append([]string(nil), base.Participants...)
	copy.EventTime = nil
	assert.False(t, sameMemoryIdentity(base, &copy))
}

func TestWhereBuilders(t *testing.T) {
	assert.Nil(t, logicalWhere("$and", nil))
	single := eqWhere("key", "value")
	assert.Equal(t, single, logicalWhere("$and", []map[string]any{nil, single}))
	assert.Equal(t, map[string]any{
		"$or": []map[string]any{
			eqWhere("first", 1),
			eqWhere("second", 2),
		},
	}, orWhere(eqWhere("first", 1), nil, eqWhere("second", 2)))

	after := time.Unix(100, 0).UTC()
	before := time.Unix(200, 0).UTC()
	where := eventTimeWhere(memory.SearchOptions{
		TimeAfter:  &after,
		TimeBefore: &before,
	})
	assert.NotNil(t, where)
	assert.Contains(t, where, "$or")
	assert.Nil(t, eventTimeWhere(memory.SearchOptions{}))
}

func TestMatchesFakeWhereRequiresEveryTopLevelClause(t *testing.T) {
	where := map[string]any{
		metadataAppNameKey: map[string]any{"$eq": "app"},
		metadataUserIDKey:  map[string]any{"$eq": "user"},
	}

	assert.True(t, matchesFakeWhere(map[string]any{
		metadataAppNameKey: "app",
		metadataUserIDKey:  "user",
	}, where))
	assert.False(t, matchesFakeWhere(map[string]any{
		metadataAppNameKey: "app",
		metadataUserIDKey:  "different",
	}, where))
}
