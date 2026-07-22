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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestRecord_RoundTripPreservesMetadata(t *testing.T) {
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

func TestRecordCodec_UpdateClearsOptionalMetadataWithNull(t *testing.T) {
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

func TestRecordCodec_RejectsMissingRequiredMetadata(t *testing.T) {
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

func TestRecordCodec_RejectsInconsistentEventTime(t *testing.T) {
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

func TestRecordCodec_RejectsCorruptOptionalMetadata(t *testing.T) {
	document := "memory"
	metadata := validTestMetadata()
	metadata[metadataLocationKey] = []string{"not", "a", "string"}

	_, err := decodeStoredRecord("id", &document, metadata)

	require.Error(t, err)
	assert.Contains(t, err.Error(), metadataLocationKey)
}

func TestUpdateToken_NormalizesExplicitMetadata(t *testing.T) {
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

func TestInt64Value_RejectsLossyFloat(t *testing.T) {
	_, err := int64Value(1.5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exact integer")
}
