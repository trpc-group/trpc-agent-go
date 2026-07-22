//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package chromadb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	schemaVersion     int64 = 1
	notDeletedAtNS    int64 = 0
	collectionBackend       = "trpc-agent-go-memory"

	metadataSchemaVersionKey = "trpc_schema_version"
	metadataAppNameKey       = "app_name"
	metadataUserIDKey        = "user_id"
	metadataKindKey          = "kind"
	metadataTopicsKey        = "topics"
	metadataHasEventTimeKey  = "has_event_time"
	metadataEventTimeKey     = "event_time_ns"
	metadataParticipantsKey  = "participants"
	metadataLocationKey      = "location"
	metadataCreatedAtKey     = "created_at_ns"
	metadataUpdatedAtKey     = "updated_at_ns"
	metadataDeletedAtKey     = "deleted_at_ns"
	metadataUpdateTokenKey   = "trpc_update_token" // #nosec G101 -- Metadata key, not a credential.
	metadataReplacesIDKey    = "trpc_replaces_id"
)

type recordScope struct {
	appName string
	userID  string
}

type storedRecord struct {
	entry       *memory.Entry
	embedding   []float32
	deletedAtNS int64
	updateToken string
	replacesID  string
}

type updateCommand struct {
	key      memory.Key
	content  string
	topics   []string
	metadata *memory.Metadata
}

type tokenMetadata struct {
	Kind         memory.Kind `json:"kind,omitempty"`
	EventTimeNS  *int64      `json:"event_time_ns,omitempty"`
	Participants []string    `json:"participants,omitempty"`
	Location     string      `json:"location,omitempty"`
}

func newAddRecord(
	scope recordScope,
	content string,
	topics []string,
	metadata *memory.Metadata,
	now time.Time,
) *storedRecord {
	mem := &memory.Memory{
		Memory:      content,
		Topics:      slices.Clone(topics),
		LastUpdated: timePointer(now),
	}
	imemory.ApplyMetadata(mem, metadata)
	entry := &memory.Entry{
		AppName:   scope.appName,
		UserID:    scope.userID,
		Memory:    mem,
		CreatedAt: now,
		UpdatedAt: now,
	}
	entry.ID = imemory.GenerateMemoryID(mem, scope.appName, scope.userID)
	return &storedRecord{
		entry:       entry,
		deletedAtNS: notDeletedAtNS,
	}
}

func decodeStoredRecord(id string, document *string, metadata map[string]any) (*storedRecord, error) {
	if document == nil {
		return nil, fmt.Errorf("memory %s has no document", id)
	}
	if metadata == nil {
		return nil, fmt.Errorf("memory %s has no metadata", id)
	}
	schema, err := requiredInt64(metadata, metadataSchemaVersionKey)
	if err != nil {
		return nil, fmt.Errorf("decode memory %s: %w", id, err)
	}
	if schema != schemaVersion {
		return nil, fmt.Errorf("memory %s has unsupported schema version %d", id, schema)
	}

	decoded, err := decodeRecordMetadata(metadata)
	if err != nil {
		return nil, fmt.Errorf("decode memory %s: %w", id, err)
	}
	mem := &memory.Memory{
		Memory:       *document,
		Topics:       decoded.topics,
		LastUpdated:  timePointer(decoded.updatedAt),
		Kind:         decoded.kind,
		EventTime:    decoded.eventTime,
		Participants: decoded.participants,
		Location:     decoded.location,
	}
	entry := &memory.Entry{
		ID:        id,
		AppName:   decoded.appName,
		UserID:    decoded.userID,
		Memory:    mem,
		CreatedAt: decoded.createdAt,
		UpdatedAt: decoded.updatedAt,
	}
	imemory.NormalizeEntry(entry)
	return &storedRecord{
		entry:       entry,
		deletedAtNS: decoded.deletedAtNS,
		updateToken: decoded.updateToken,
		replacesID:  decoded.replacesID,
	}, nil
}

type decodedMetadata struct {
	appName      string
	userID       string
	kind         memory.Kind
	topics       []string
	eventTime    *time.Time
	participants []string
	location     string
	createdAt    time.Time
	updatedAt    time.Time
	deletedAtNS  int64
	updateToken  string
	replacesID   string
}

func decodeRecordMetadata(metadata map[string]any) (*decodedMetadata, error) {
	appName, err := requiredString(metadata, metadataAppNameKey)
	if err != nil {
		return nil, err
	}
	userID, err := requiredString(metadata, metadataUserIDKey)
	if err != nil {
		return nil, err
	}
	kind, err := requiredKind(metadata)
	if err != nil {
		return nil, err
	}
	eventTime, err := decodeEventTime(metadata)
	if err != nil {
		return nil, err
	}
	createdAt, updatedAt, deletedAtNS, err := decodeTimestamps(metadata)
	if err != nil {
		return nil, err
	}
	topics, err := optionalStringSlice(metadata, metadataTopicsKey)
	if err != nil {
		return nil, err
	}
	participants, err := optionalStringSlice(metadata, metadataParticipantsKey)
	if err != nil {
		return nil, err
	}
	location, err := optionalString(metadata, metadataLocationKey)
	if err != nil {
		return nil, err
	}
	updateToken, err := optionalString(metadata, metadataUpdateTokenKey)
	if err != nil {
		return nil, err
	}
	replacesID, err := optionalString(metadata, metadataReplacesIDKey)
	if err != nil {
		return nil, err
	}
	return &decodedMetadata{
		appName:      appName,
		userID:       userID,
		kind:         kind,
		topics:       topics,
		eventTime:    eventTime,
		participants: participants,
		location:     location,
		createdAt:    createdAt,
		updatedAt:    updatedAt,
		deletedAtNS:  deletedAtNS,
		updateToken:  updateToken,
		replacesID:   replacesID,
	}, nil
}

func decodeEventTime(metadata map[string]any) (*time.Time, error) {
	hasEventTime, err := requiredBool(metadata, metadataHasEventTimeKey)
	if err != nil {
		return nil, err
	}
	value, exists := metadata[metadataEventTimeKey]
	if !hasEventTime {
		if exists && value != nil {
			return nil, errors.New("event_time_ns is set when has_event_time is false")
		}
		return nil, nil
	}
	if !exists || value == nil {
		return nil, errors.New("event_time_ns is required when has_event_time is true")
	}
	nanoseconds, err := int64Value(value)
	if err != nil {
		return nil, fmt.Errorf("metadata %s: %w", metadataEventTimeKey, err)
	}
	eventTime := time.Unix(0, nanoseconds).UTC()
	return &eventTime, nil
}

func decodeTimestamps(metadata map[string]any) (time.Time, time.Time, int64, error) {
	createdAtNS, err := requiredInt64(metadata, metadataCreatedAtKey)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	updatedAtNS, err := requiredInt64(metadata, metadataUpdatedAtKey)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	deletedAtNS, err := requiredInt64(metadata, metadataDeletedAtKey)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	return time.Unix(0, createdAtNS).UTC(), time.Unix(0, updatedAtNS).UTC(), deletedAtNS, nil
}

func addMetadata(record *storedRecord) map[string]any {
	metadata := requiredMetadata(record)
	mem := record.entry.Memory
	if len(mem.Topics) > 0 {
		metadata[metadataTopicsKey] = slices.Clone(mem.Topics)
	}
	if mem.EventTime != nil {
		metadata[metadataEventTimeKey] = mem.EventTime.UTC().UnixNano()
	}
	if len(mem.Participants) > 0 {
		metadata[metadataParticipantsKey] = slices.Clone(mem.Participants)
	}
	if mem.Location != "" {
		metadata[metadataLocationKey] = mem.Location
	}
	if record.updateToken != "" {
		metadata[metadataUpdateTokenKey] = record.updateToken
	}
	if record.replacesID != "" {
		metadata[metadataReplacesIDKey] = record.replacesID
	}
	return metadata
}

func updateMetadata(record *storedRecord) map[string]any {
	metadata := requiredMetadata(record)
	mem := record.entry.Memory
	setOptionalMetadata(metadata, metadataTopicsKey, slices.Clone(mem.Topics), len(mem.Topics) > 0)
	if mem.EventTime == nil {
		metadata[metadataEventTimeKey] = nil
	} else {
		metadata[metadataEventTimeKey] = mem.EventTime.UTC().UnixNano()
	}
	setOptionalMetadata(
		metadata,
		metadataParticipantsKey,
		slices.Clone(mem.Participants),
		len(mem.Participants) > 0,
	)
	setOptionalMetadata(metadata, metadataLocationKey, mem.Location, mem.Location != "")
	setOptionalMetadata(metadata, metadataUpdateTokenKey, record.updateToken, record.updateToken != "")
	setOptionalMetadata(metadata, metadataReplacesIDKey, record.replacesID, record.replacesID != "")
	return metadata
}

func requiredMetadata(record *storedRecord) map[string]any {
	entry := record.entry
	mem := entry.Memory
	return map[string]any{
		metadataSchemaVersionKey: schemaVersion,
		metadataAppNameKey:       entry.AppName,
		metadataUserIDKey:        entry.UserID,
		metadataKindKey:          string(imemory.EffectiveKind(mem)),
		metadataHasEventTimeKey:  mem.EventTime != nil,
		metadataCreatedAtKey:     entry.CreatedAt.UTC().UnixNano(),
		metadataUpdatedAtKey:     entry.UpdatedAt.UTC().UnixNano(),
		metadataDeletedAtKey:     record.deletedAtNS,
	}
}

func setOptionalMetadata(metadata map[string]any, key string, value any, present bool) {
	if present {
		metadata[key] = value
		return
	}
	metadata[key] = nil
}

func requiredKind(metadata map[string]any) (memory.Kind, error) {
	value, err := requiredString(metadata, metadataKindKey)
	if err != nil {
		return "", err
	}
	kind := memory.Kind(value)
	if kind != memory.KindFact && kind != memory.KindEpisode {
		return "", fmt.Errorf("metadata %s has invalid value %q", metadataKindKey, value)
	}
	return kind, nil
}

func requiredString(metadata map[string]any, key string) (string, error) {
	value, ok := metadata[key]
	if !ok {
		return "", fmt.Errorf("metadata %s is required", key)
	}
	result, ok := value.(string)
	if !ok || result == "" {
		return "", fmt.Errorf("metadata %s must be a non-empty string", key)
	}
	return result, nil
}

func optionalString(metadata map[string]any, key string) (string, error) {
	value, exists := metadata[key]
	if !exists || value == nil {
		return "", nil
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("metadata %s must be a string", key)
	}
	return result, nil
}

func requiredBool(metadata map[string]any, key string) (bool, error) {
	value, ok := metadata[key]
	if !ok {
		return false, fmt.Errorf("metadata %s is required", key)
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("metadata %s must be a boolean", key)
	}
	return result, nil
}

func requiredInt64(metadata map[string]any, key string) (int64, error) {
	value, ok := metadata[key]
	if !ok {
		return 0, fmt.Errorf("metadata %s is required", key)
	}
	result, err := int64Value(value)
	if err != nil {
		return 0, fmt.Errorf("metadata %s: %w", key, err)
	}
	return result, nil
}

func int64Value(value any) (int64, error) {
	switch number := value.(type) {
	case json.Number:
		result, err := number.Int64()
		if err != nil {
			return 0, fmt.Errorf("must be an integer: %w", err)
		}
		return result, nil
	case int64:
		return number, nil
	case int:
		return int64(number), nil
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) ||
			number < float64(math.MinInt64) || number >= 1<<63 {
			return 0, errors.New("must be an int64")
		}
		result := int64(number)
		if float64(result) != number {
			return 0, errors.New("must be an exact integer")
		}
		return result, nil
	default:
		return 0, fmt.Errorf("must be an integer, got %T", value)
	}
}

func optionalStringSlice(metadata map[string]any, key string) ([]string, error) {
	value, ok := metadata[key]
	if !ok || value == nil {
		return nil, nil
	}
	switch values := value.(type) {
	case []string:
		return slices.Clone(values), nil
	case []any:
		result := make([]string, len(values))
		for i, item := range values {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("metadata %s item %d must be a string", key, i)
			}
			result[i] = text
		}
		return result, nil
	default:
		return nil, fmt.Errorf("metadata %s must be a string array", key)
	}
}

func updateToken(command updateCommand) (string, error) {
	type tokenPayload struct {
		AppName  string         `json:"app_name"`
		UserID   string         `json:"user_id"`
		OldID    string         `json:"old_id"`
		Content  string         `json:"content"`
		Topics   []string       `json:"topics"`
		Metadata *tokenMetadata `json:"metadata,omitempty"`
	}
	payload := tokenPayload{
		AppName: command.key.AppName,
		UserID:  command.key.UserID,
		OldID:   command.key.MemoryID,
		Content: command.content,
		Topics:  slices.Clone(command.topics),
	}
	if command.metadata != nil {
		metadata := normalizedTokenMetadata(command.metadata)
		if command.metadata.EventTime != nil {
			nanoseconds := command.metadata.EventTime.UTC().UnixNano()
			metadata.EventTimeNS = &nanoseconds
		}
		payload.Metadata = metadata
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode update token: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func normalizedTokenMetadata(metadata *memory.Metadata) *tokenMetadata {
	normalized := &memory.Memory{Kind: metadata.Kind}
	imemory.ApplyMetadataPatch(normalized, metadata)
	return &tokenMetadata{
		Kind:         metadata.Kind,
		Participants: slices.Clone(normalized.Participants),
		Location:     normalized.Location,
	}
}

func sameRecordIdentity(left, right *storedRecord) bool {
	if left == nil || right == nil || left.entry == nil || right.entry == nil {
		return false
	}
	leftEntry := left.entry
	rightEntry := right.entry
	if leftEntry.ID != rightEntry.ID || leftEntry.AppName != rightEntry.AppName ||
		leftEntry.UserID != rightEntry.UserID {
		return false
	}
	return sameMemoryIdentity(leftEntry.Memory, rightEntry.Memory)
}

func sameSemanticRecord(left, right *storedRecord) bool {
	if !sameRecordIdentity(left, right) {
		return false
	}
	return slices.Equal(left.entry.Memory.Topics, right.entry.Memory.Topics) &&
		left.deletedAtNS == right.deletedAtNS &&
		left.updateToken == right.updateToken &&
		left.replacesID == right.replacesID
}

func samePersistedRecord(left, right *storedRecord) bool {
	if !sameSemanticRecord(left, right) {
		return false
	}
	return left.entry.CreatedAt.Equal(right.entry.CreatedAt) &&
		left.entry.UpdatedAt.Equal(right.entry.UpdatedAt)
}

func sameMemoryIdentity(left, right *memory.Memory) bool {
	if left == nil || right == nil {
		return false
	}
	if left.Memory != right.Memory || imemory.EffectiveKind(left) != imemory.EffectiveKind(right) ||
		left.Location != right.Location || !slices.Equal(left.Participants, right.Participants) {
		return false
	}
	return equalTimePointers(left.EventTime, right.EventTime)
}

func equalTimePointers(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}

func activeScopeWhere(scope recordScope) map[string]any {
	return andWhere(
		eqWhere(metadataSchemaVersionKey, schemaVersion),
		eqWhere(metadataAppNameKey, scope.appName),
		eqWhere(metadataUserIDKey, scope.userID),
		eqWhere(metadataDeletedAtKey, notDeletedAtNS),
	)
}

func ownedScopeWhere(scope recordScope) map[string]any {
	return andWhere(
		eqWhere(metadataSchemaVersionKey, schemaVersion),
		eqWhere(metadataAppNameKey, scope.appName),
		eqWhere(metadataUserIDKey, scope.userID),
	)
}

func tokenWhere(scope recordScope, token string) map[string]any {
	return andWhere(
		activeScopeWhere(scope),
		eqWhere(metadataUpdateTokenKey, token),
	)
}

func searchWhere(scope recordScope, opts memory.SearchOptions) map[string]any {
	clauses := []map[string]any{activeScopeWhere(scope)}
	if opts.Kind != "" {
		clauses = append(clauses, eqWhere(metadataKindKey, string(opts.Kind)))
	}
	if timeClause := eventTimeWhere(opts); timeClause != nil {
		clauses = append(clauses, timeClause)
	}
	return andWhere(clauses...)
}

func eventTimeWhere(opts memory.SearchOptions) map[string]any {
	bounds := make([]map[string]any, 0, 2)
	if opts.TimeAfter != nil {
		bounds = append(bounds, comparisonWhere(
			metadataEventTimeKey,
			"$gte",
			opts.TimeAfter.UTC().UnixNano(),
		))
	}
	if opts.TimeBefore != nil {
		bounds = append(bounds, comparisonWhere(
			metadataEventTimeKey,
			"$lte",
			opts.TimeBefore.UTC().UnixNano(),
		))
	}
	if len(bounds) == 0 {
		return nil
	}
	return orWhere(
		eqWhere(metadataHasEventTimeKey, false),
		andWhere(bounds...),
	)
}

func eqWhere(key string, value any) map[string]any {
	return comparisonWhere(key, "$eq", value)
}

func comparisonWhere(key, operator string, value any) map[string]any {
	return map[string]any{
		key: map[string]any{
			operator: value,
		},
	}
}

func andWhere(clauses ...map[string]any) map[string]any {
	return logicalWhere("$and", clauses)
}

func orWhere(clauses ...map[string]any) map[string]any {
	return logicalWhere("$or", clauses)
}

func logicalWhere(operator string, clauses []map[string]any) map[string]any {
	filtered := make([]map[string]any, 0, len(clauses))
	for _, clause := range clauses {
		if len(clause) > 0 {
			filtered = append(filtered, clause)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return map[string]any{operator: filtered}
	}
}
