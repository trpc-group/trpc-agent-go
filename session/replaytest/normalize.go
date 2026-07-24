//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
)

// NormalizeOptions controls narrowly scoped normalization rules.
type NormalizeOptions struct {
	PreserveEventIDs       bool
	PreserveMemoryIDs      bool
	NormalizeToolCallIDs   bool
	NormalizeInvocationIDs bool
	SortMemories           bool
	TimePrecision          time.Duration
	IgnoredMetadataFields  map[string]struct{}
}

type normalizationIDs struct {
	events      *logicalIDMap
	invocations *logicalIDMap
	toolCalls   *logicalIDMap
}

// DefaultNormalizeOptions returns conservative cross-backend defaults.
func DefaultNormalizeOptions() NormalizeOptions {
	return NormalizeOptions{
		SortMemories:  true,
		TimePrecision: time.Millisecond,
		IgnoredMetadataFields: map[string]struct{}{
			"backend_metadata": {},
			"storage_metadata": {},
		},
	}
}

// NormalizeSnapshot returns a deep, deterministically ordered snapshot.
func NormalizeSnapshot(snapshot Snapshot, options NormalizeOptions) Snapshot {
	if options.TimePrecision <= 0 {
		options.TimePrecision = time.Millisecond
	}
	if options.IgnoredMetadataFields == nil {
		options.IgnoredMetadataFields = DefaultNormalizeOptions().IgnoredMetadataFields
	}

	normalized := cloneSnapshot(snapshot)
	sort.Slice(normalized.Sessions, func(i, j int) bool {
		return sessionSortKey(normalized.Sessions[i]) < sessionSortKey(normalized.Sessions[j])
	})
	if options.SortMemories {
		sort.SliceStable(normalized.Memories, func(i, j int) bool {
			return memorySortKey(normalized.Memories[i]) < memorySortKey(normalized.Memories[j])
		})
	}
	sort.SliceStable(normalized.MemorySearches, func(i, j int) bool {
		return memorySearchSortKey(normalized.MemorySearches[i]) <
			memorySearchSortKey(normalized.MemorySearches[j])
	})
	sort.Slice(normalized.Unsupported, func(i, j int) bool {
		left := string(normalized.Unsupported[i].Capability) + "\x00" + normalized.Unsupported[i].Reason
		right := string(normalized.Unsupported[j].Capability) + "\x00" + normalized.Unsupported[j].Reason
		return left < right
	})

	eventIDs := newLogicalIDMap("event")
	memoryIDs := newLogicalIDMap("memory")
	ids := normalizationIDs{
		events:      eventIDs,
		invocations: newLogicalIDMap("invocation"),
		toolCalls:   newLogicalIDMap("tool-call"),
	}
	for i := range normalized.Sessions {
		normalizeSession(&normalized.Sessions[i], options, ids)
	}
	for i := range normalized.Memories {
		normalizeMemory(&normalized.Memories[i], options, memoryIDs)
	}
	for i := range normalized.MemorySearches {
		for j := range normalized.MemorySearches[i].Results {
			normalizeMemory(&normalized.MemorySearches[i].Results[j], options, memoryIDs)
		}
	}
	return normalized
}

func normalizeSession(
	snapshot *SessionSnapshot,
	options NormalizeOptions,
	ids normalizationIDs,
) {
	normalizeSessionTimes(snapshot, options.TimePrecision)
	snapshot.State = normalizeStateMap(snapshot.State, options)
	for i := range snapshot.Events {
		event := &snapshot.Events[i]
		if !options.PreserveEventIDs {
			event.ID = ids.events.value(event.ID)
		}
		if options.NormalizeInvocationIDs {
			event.InvocationID = ids.invocations.value(event.InvocationID)
		}
		event.StateDelta = normalizeStateMap(event.StateDelta, options)
		event.Extensions = normalizeStringMap(event.Extensions, options)
		for j := range event.ToolCalls {
			if options.NormalizeToolCallIDs {
				event.ToolCalls[j].ID = ids.toolCalls.value(event.ToolCalls[j].ID)
			}
			event.ToolCalls[j].Arguments = normalizeToolArguments(event.ToolCalls[j].Arguments, options)
			event.ToolCalls[j].Extra = normalizeStringMap(event.ToolCalls[j].Extra, options)
		}
		if event.ToolResponse != nil {
			response := *event.ToolResponse
			if options.NormalizeToolCallIDs {
				response.ToolCallID = ids.toolCalls.value(response.ToolCallID)
			}
			response.Extra = normalizeStringMap(response.Extra, options)
			event.ToolResponse = &response
		}
	}
	for i := range snapshot.Summaries {
		snapshot.Summaries[i].Boundary = normalizeStringMap(snapshot.Summaries[i].Boundary, options)
		if id, ok := snapshot.Summaries[i].Boundary["last_event_id"].(string); ok && !options.PreserveEventIDs {
			snapshot.Summaries[i].Boundary["last_event_id"] = ids.events.value(id)
		}
	}
	sort.Slice(snapshot.Summaries, func(i, j int) bool {
		return snapshot.Summaries[i].FilterKey < snapshot.Summaries[j].FilterKey
	})
	sort.SliceStable(snapshot.Tracks, func(i, j int) bool {
		return snapshot.Tracks[i].Name < snapshot.Tracks[j].Name
	})
	for i := range snapshot.Tracks {
		track := &snapshot.Tracks[i]
		for j := range track.Events {
			trackEvent := &track.Events[j]
			if options.NormalizeInvocationIDs {
				trackEvent.InvocationID = ids.invocations.value(trackEvent.InvocationID)
			}
			trackEvent.Payload = normalizeStringMap(trackEvent.Payload, options)
		}
	}
}

func normalizeMemory(
	snapshot *MemorySnapshot,
	options NormalizeOptions,
	memoryIDs *logicalIDMap,
) {
	normalizeTimes([]*time.Time{&snapshot.CreatedAt, &snapshot.UpdatedAt}, options.TimePrecision)
	if !options.PreserveMemoryIDs {
		snapshot.ID = memoryIDs.value(snapshot.ID)
	}
	snapshot.Topics = append([]string(nil), snapshot.Topics...)
	sort.Strings(snapshot.Topics)
	snapshot.Metadata = normalizeMetadataMap(snapshot.Metadata, options)
}

func normalizeStringMap(value map[string]any, options NormalizeOptions) map[string]any {
	if value == nil {
		return nil
	}
	normalized := make(map[string]any, len(value))
	for key, item := range value {
		normalized[key] = normalizeJSONLike(item, options)
	}
	return normalized
}

func normalizeStateMap(
	values map[string]StateValueSnapshot,
	options NormalizeOptions,
) map[string]StateValueSnapshot {
	if values == nil {
		return nil
	}
	normalized := make(map[string]StateValueSnapshot, len(values))
	for key, value := range values {
		switch value.Kind {
		case StateValueNull:
			value.Value = nil
		case StateValueJSON:
			value.Value = normalizeJSONLike(value.Value, options)
		case StateValueBinary:
			if binary, ok := value.Value.([]byte); ok {
				value.Value = append([]byte(nil), binary...)
			}
		}
		normalized[key] = value
	}
	return normalized
}

func normalizeMetadataMap(value map[string]any, options NormalizeOptions) map[string]any {
	if value == nil {
		return nil
	}
	normalized := make(map[string]any, len(value))
	for key, item := range value {
		if _, ignored := options.IgnoredMetadataFields[key]; ignored {
			continue
		}
		normalized[key] = normalizeJSONLike(item, options)
	}
	return normalized
}

func normalizeJSONLike(value any, options NormalizeOptions) any {
	switch typed := value.(type) {
	case nil:
		return typed
	case json.Number:
		return normalizeJSONNumbers(typed)
	case json.RawMessage:
		if decoded, valid := decodeJSON(typed); valid {
			return normalizeJSONLike(decoded, options)
		}
		return string(typed)
	case []byte:
		if decoded, valid := decodeJSON(typed); valid {
			return normalizeJSONLike(decoded, options)
		}
		return string(typed)
	case map[string]any:
		return normalizeStringMap(typed, options)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = normalizeJSONLike(typed[i], options)
		}
		return out
	}
	if scalar, ok := normalizeScalar(value); ok {
		return scalar
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	decoded, valid := decodeJSON(encoded)
	if !valid {
		return fmt.Sprint(value)
	}
	return normalizeJSONLike(decoded, options)
}

func normalizeScalar(value any) (any, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Bool:
		return reflected.Bool(), true
	case reflect.String:
		return reflected.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflected.Uint(), true
	case reflect.Float32, reflect.Float64:
		return reflected.Float(), true
	default:
		return nil, false
	}
}

func normalizeToolArguments(value any, options NormalizeOptions) any {
	if text, ok := value.(string); ok {
		if decoded, valid := decodeJSON([]byte(text)); valid {
			return normalizeJSONLike(decoded, options)
		}
	}
	return normalizeJSONLike(value, options)
}

func decodeJSON(data []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false
	}
	return normalizeJSONNumbers(decoded), true
}

func normalizeJSONNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		if strings.ContainsAny(typed.String(), ".eE") {
			decimal, err := typed.Float64()
			if err != nil {
				return typed.String()
			}
			return decimal
		}
		return typed.String()
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeJSONNumbers(item)
		}
	case []any:
		for i := range typed {
			typed[i] = normalizeJSONNumbers(typed[i])
		}
	}
	return value
}

func sessionSortKey(snapshot SessionSnapshot) string {
	return snapshot.AppName + "\x00" + snapshot.UserID + "\x00" + snapshot.ID
}

func normalizeSessionTimes(snapshot *SessionSnapshot, precision time.Duration) {
	normalizeSessionMetadataTimes(&snapshot.CreatedAt, &snapshot.UpdatedAt)
	conversationTimes := make([]*time.Time, 0, len(snapshot.Events)+len(snapshot.Summaries))
	for i := range snapshot.Events {
		conversationTimes = append(conversationTimes, &snapshot.Events[i].Timestamp)
	}
	for i := range snapshot.Summaries {
		conversationTimes = append(conversationTimes, &snapshot.Summaries[i].UpdatedAt)
	}
	normalizeTimes(conversationTimes, precision)
	trackTimes := make([]*time.Time, 0)
	for i := range snapshot.Tracks {
		for j := range snapshot.Tracks[i].Events {
			trackTimes = append(trackTimes, &snapshot.Tracks[i].Events[j].Timestamp)
		}
	}
	normalizeTimes(trackTimes, precision)
}

func normalizeSessionMetadataTimes(createdAt, updatedAt *time.Time) {
	created := *createdAt
	updated := *updatedAt
	if !created.IsZero() {
		*createdAt = time.Unix(0, 1).UTC()
	}
	if updated.IsZero() {
		return
	}
	if !created.IsZero() && updated.Before(created) {
		*updatedAt = time.Unix(0, -1).UTC()
		return
	}
	*updatedAt = time.Unix(0, 1).UTC()
}

func normalizeTimes(values []*time.Time, precision time.Duration) {
	ordered := make([]time.Time, 0, len(values))
	for _, value := range values {
		if value != nil && !value.IsZero() {
			ordered = append(ordered, *value)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	ranks := make(map[time.Time]int, len(ordered))
	var clusterStart time.Time
	rank := 0
	for _, value := range ordered {
		if rank == 0 || value.Sub(clusterStart) > precision {
			rank++
			clusterStart = value
		}
		ranks[value] = rank
	}
	for _, value := range values {
		if value == nil || value.IsZero() {
			continue
		}
		*value = time.Unix(0, int64(ranks[*value])).UTC()
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := Snapshot{
		Sessions:       append([]SessionSnapshot(nil), snapshot.Sessions...),
		Memories:       append([]MemorySnapshot(nil), snapshot.Memories...),
		MemorySearches: append([]MemorySearchSnapshot(nil), snapshot.MemorySearches...),
		Unsupported:    append([]UnsupportedFeature(nil), snapshot.Unsupported...),
	}
	for i := range cloned.Sessions {
		session := &cloned.Sessions[i]
		session.State = cloneStateMap(session.State)
		session.Events = append([]EventSnapshot(nil), session.Events...)
		for j := range session.Events {
			event := &session.Events[j]
			event.StateDelta = cloneStateMap(event.StateDelta)
			event.Extensions = cloneStringMap(event.Extensions)
			event.ToolCalls = append([]ToolCallSnapshot(nil), event.ToolCalls...)
			for k := range event.ToolCalls {
				event.ToolCalls[k].Arguments = cloneJSONLike(event.ToolCalls[k].Arguments)
				event.ToolCalls[k].Extra = cloneStringMap(event.ToolCalls[k].Extra)
			}
			if event.ToolResponse != nil {
				response := *event.ToolResponse
				response.Extra = cloneStringMap(response.Extra)
				event.ToolResponse = &response
			}
		}
		session.Summaries = append([]SummarySnapshot(nil), session.Summaries...)
		for j := range session.Summaries {
			session.Summaries[j].Boundary = cloneStringMap(session.Summaries[j].Boundary)
		}
		session.Tracks = append([]TrackSnapshot(nil), session.Tracks...)
		for j := range session.Tracks {
			session.Tracks[j].Events = append([]TrackEventSnapshot(nil), session.Tracks[j].Events...)
			for k := range session.Tracks[j].Events {
				session.Tracks[j].Events[k].Payload = cloneStringMap(session.Tracks[j].Events[k].Payload)
			}
		}
	}
	for i := range cloned.Memories {
		cloned.Memories[i].Topics = append([]string(nil), cloned.Memories[i].Topics...)
		cloned.Memories[i].Metadata = cloneStringMap(cloned.Memories[i].Metadata)
	}
	for i := range cloned.MemorySearches {
		search := &cloned.MemorySearches[i]
		search.Results = append([]MemorySnapshot(nil), search.Results...)
		for j := range search.Results {
			search.Results[j].Topics = append([]string(nil), search.Results[j].Topics...)
			search.Results[j].Metadata = cloneStringMap(search.Results[j].Metadata)
		}
	}
	return cloned
}

func cloneStringMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = cloneJSONLike(item)
	}
	return cloned
}

func cloneStateMap(
	values map[string]StateValueSnapshot,
) map[string]StateValueSnapshot {
	if values == nil {
		return nil
	}
	cloned := make(map[string]StateValueSnapshot, len(values))
	for key, value := range values {
		value.Value = cloneJSONLike(value.Value)
		cloned[key] = value
	}
	return cloned
}

func cloneJSONLike(value any) any {
	if value == nil {
		return nil
	}
	return cloneJSONLikeValue(reflect.ValueOf(value)).Interface()
}

func cloneJSONLikeValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type()).Elem()
		cloned.Set(cloneJSONLikeValue(value.Elem()))
		return cloned
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			cloned.SetMapIndex(iterator.Key(), cloneJSONLikeValue(iterator.Value()))
		}
		return cloned
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(cloneJSONLikeValue(value.Elem()))
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneJSONLikeValue(value.Index(i)))
		}
		return cloned
	case reflect.Array:
		cloned := reflect.New(value.Type()).Elem()
		for i := 0; i < value.Len(); i++ {
			cloned.Index(i).Set(cloneJSONLikeValue(value.Index(i)))
		}
		return cloned
	default:
		return value
	}
}

func memorySortKey(snapshot MemorySnapshot) string {
	return snapshot.AppName + "\x00" + snapshot.UserID + "\x00" +
		snapshot.Content + "\x00" + snapshot.ID
}

func memorySearchSortKey(snapshot MemorySearchSnapshot) string {
	return snapshot.AppName + "\x00" + snapshot.UserID + "\x00" + snapshot.Query
}

type logicalIDMap struct {
	prefix string
	values map[string]string
}

func newLogicalIDMap(prefix string) *logicalIDMap {
	return &logicalIDMap{prefix: prefix, values: make(map[string]string)}
}

func (mapping *logicalIDMap) value(id string) string {
	if id == "" {
		return ""
	}
	if value, ok := mapping.values[id]; ok {
		return value
	}
	value := fmt.Sprintf("%s-%04d", mapping.prefix, len(mapping.values)+1)
	mapping.values[id] = value
	return value
}
