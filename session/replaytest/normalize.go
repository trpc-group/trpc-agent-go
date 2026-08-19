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
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
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

type mapTimeReference struct {
	values map[string]any
	key    string
	value  *time.Time
}

type invalidRawJSONValue string

const (
	toolCallArgsExtensionKey   = "trpc_agent.tool_call_args"
	toolCallArgsEntryKey       = "key"
	toolCallArgsEntryKnown     = "known"
	toolCallArgsEntryValue     = "value"
	memoryEventTimeMetadataKey = "event_time"
)

type toolCallArgsEntry struct {
	key   string
	known bool
	value any
}

func (value invalidRawJSONValue) raw() string {
	return string(value)
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
	sort.SliceStable(normalized.MemorySearches, func(i, j int) bool {
		return memorySearchSortKey(normalized.MemorySearches[i]) <
			memorySearchSortKey(normalized.MemorySearches[j])
	})
	sort.Slice(normalized.Unsupported, func(i, j int) bool {
		left := string(normalized.Unsupported[i].Capability) + "\x00" + normalized.Unsupported[i].Reason
		right := string(normalized.Unsupported[j].Capability) + "\x00" + normalized.Unsupported[j].Reason
		return left < right
	})

	memoryIDs := newScopedLogicalIDMaps("memory")
	ids := normalizationIDs{}
	for i := range normalized.Sessions {
		normalizeSession(&normalized.Sessions[i], options, ids)
	}
	for i := range normalized.Memories {
		normalizeMemoryValues(&normalized.Memories[i], options)
	}
	for i := range normalized.MemorySearches {
		search := &normalized.MemorySearches[i]
		if search.Results == nil {
			search.Results = []MemorySnapshot{}
		}
		for j := range search.Results {
			normalizeMemoryValues(&search.Results[j], options)
		}
	}
	normalizeMemoryTimes(&normalized)
	normalizeMemoryEventTimes(&normalized, options.TimePrecision)
	if options.SortMemories {
		sort.SliceStable(normalized.Memories, func(i, j int) bool {
			return memorySortKey(normalized.Memories[i]) < memorySortKey(normalized.Memories[j])
		})
	}
	for i := range normalized.Memories {
		normalizeMemoryID(&normalized.Memories[i], options, memoryIDs, MemoryScope{})
	}
	for i := range normalized.MemorySearches {
		searchScope := MemoryScope{
			AppName: normalized.MemorySearches[i].AppName,
			UserID:  normalized.MemorySearches[i].UserID,
		}
		for j := range normalized.MemorySearches[i].Results {
			normalizeMemoryID(&normalized.MemorySearches[i].Results[j], options, memoryIDs, searchScope)
		}
	}
	return normalized
}

func normalizeSession(
	snapshot *SessionSnapshot,
	options NormalizeOptions,
	ids normalizationIDs,
) {
	ids.events = newLogicalIDMap("event")
	ids.invocations = newLogicalIDMap("invocation")
	ids.toolCalls = newLogicalIDMap("tool-call")
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
		event.Extensions = normalizeEventExtensions(event.Extensions, options, ids)
	}
	sort.Slice(snapshot.Summaries, func(i, j int) bool {
		return snapshot.Summaries[i].FilterKey < snapshot.Summaries[j].FilterKey
	})
	for i := range snapshot.Summaries {
		snapshot.Summaries[i].Boundary = normalizeStringMap(snapshot.Summaries[i].Boundary, options)
		if id, ok := snapshot.Summaries[i].Boundary["last_event_id"].(string); ok && !options.PreserveEventIDs {
			snapshot.Summaries[i].Boundary["last_event_id"] = ids.events.value(id)
		}
	}
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
	memoryIDs *scopedLogicalIDMaps,
	fallbackScope MemoryScope,
) {
	normalizeMemoryValues(snapshot, options)
	normalizeMemoryID(snapshot, options, memoryIDs, fallbackScope)
}

func normalizeMemoryValues(
	snapshot *MemorySnapshot,
	options NormalizeOptions,
) {
	snapshot.Topics = append([]string(nil), snapshot.Topics...)
	sort.Strings(snapshot.Topics)
	snapshot.Metadata = normalizeMetadataMap(snapshot.Metadata, options)
}

// normalizeMemoryTimes ranks memory timestamps jointly within each logical
// scope so that cross-entry chronology remains observable after normalization,
// while tolerating the absolute time skew between independently executed
// backends. Ranks are derived from the chronological position of each entry
// within its scope, so entries with swapped timestamps receive different
// ranks. Search-result copies share the identity of their top-level memory
// and are ranked together with it.
func normalizeMemoryTimes(snapshot *Snapshot) {
	entries := make([]memoryTimeEntry, 0, len(snapshot.Memories)*2+len(snapshot.MemorySearches)*2)
	appendEntry := func(memory *MemorySnapshot, fallback MemoryScope) {
		entries = append(entries, memoryTimeEntry{
			scope:     memoryIDScope(*memory, fallback),
			createdAt: &memory.CreatedAt,
			updatedAt: &memory.UpdatedAt,
		})
	}
	for i := range snapshot.Memories {
		appendEntry(&snapshot.Memories[i], MemoryScope{})
	}
	for i := range snapshot.MemorySearches {
		scope := MemoryScope{
			AppName: snapshot.MemorySearches[i].AppName,
			UserID:  snapshot.MemorySearches[i].UserID,
		}
		for j := range snapshot.MemorySearches[i].Results {
			appendEntry(&snapshot.MemorySearches[i].Results[j], scope)
		}
	}
	scoped := make(map[MemoryScope][]memoryTimeEntry, len(entries))
	for _, item := range entries {
		scoped[item.scope] = append(scoped[item.scope], item)
	}
	for _, group := range scoped {
		sort.SliceStable(group, func(i, j int) bool {
			return memoryTimeKeyLess(
				memoryTimeKeyFor(group[i]),
				memoryTimeKeyFor(group[j]),
			)
		})
		position := -1
		var previous memoryTimeKey
		for i := range group {
			key := memoryTimeKeyFor(group[i])
			if i == 0 || memoryTimeKeyLess(previous, key) {
				position++
				previous = key
			}
			assignMemoryTimeRanks(group[i], key, position)
		}
	}
}

// memoryTimeEntry is one memory timestamp pair and its logical scope.
type memoryTimeEntry struct {
	scope     MemoryScope
	createdAt *time.Time
	updatedAt *time.Time
}

// memoryTimeKey is the (updated, created) pair used to order memory entries
// within one logical scope. Zero times sort last.
type memoryTimeKey struct {
	updated time.Time
	created time.Time
}

func memoryTimeKeyFor(item memoryTimeEntry) memoryTimeKey {
	key := memoryTimeKey{}
	if !item.updatedAt.IsZero() {
		key.updated = *item.updatedAt
	}
	if !item.createdAt.IsZero() {
		key.created = *item.createdAt
	}
	return key
}

func memoryTimeKeyLess(left, right memoryTimeKey) bool {
	if left.updated.IsZero() != right.updated.IsZero() {
		return !left.updated.IsZero()
	}
	if !left.updated.Equal(right.updated) {
		return left.updated.Before(right.updated)
	}
	if left.created.IsZero() != right.created.IsZero() {
		return !left.created.IsZero()
	}
	return left.created.Before(right.created)
}

// assignMemoryTimeRanks writes the position-derived rank times for one entry.
// The within-entry created/updated order is preserved unless the two times
// are equal.
func assignMemoryTimeRanks(item memoryTimeEntry, key memoryTimeKey, position int) {
	base := 2*position + 1
	created, updated := key.created, key.updated
	switch {
	case created.IsZero() && updated.IsZero():
		return
	case created.IsZero():
		*item.updatedAt = time.Unix(0, int64(base)).UTC()
	case updated.IsZero():
		*item.createdAt = time.Unix(0, int64(base)).UTC()
	case created.Equal(updated):
		*item.createdAt = time.Unix(0, int64(base)).UTC()
		*item.updatedAt = time.Unix(0, int64(base)).UTC()
	case created.Before(updated):
		*item.createdAt = time.Unix(0, int64(base)).UTC()
		*item.updatedAt = time.Unix(0, int64(base+1)).UTC()
	default:
		*item.createdAt = time.Unix(0, int64(base+1)).UTC()
		*item.updatedAt = time.Unix(0, int64(base)).UTC()
	}
}

func normalizeMemoryID(
	snapshot *MemorySnapshot,
	options NormalizeOptions,
	memoryIDs *scopedLogicalIDMaps,
	fallbackScope MemoryScope,
) {
	if !options.PreserveMemoryIDs {
		snapshot.ID = memoryIDs.value(memoryIDScope(*snapshot, fallbackScope), snapshot.ID)
	}
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

func normalizeEventExtensions(
	value map[string]any,
	options NormalizeOptions,
	ids normalizationIDs,
) map[string]any {
	if value == nil {
		return nil
	}
	normalized := make(map[string]any, len(value))
	for key, item := range value {
		if key == toolCallArgsExtensionKey && options.NormalizeToolCallIDs && ids.toolCalls != nil {
			normalized[key] = normalizeToolCallArgsExtension(item, options, ids.toolCalls)
			continue
		}
		normalized[key] = normalizeJSONLike(item, options)
	}
	return normalized
}

func normalizeToolCallArgsExtension(
	value any,
	options NormalizeOptions,
	toolCalls *logicalIDMap,
) any {
	normalized := normalizeJSONLike(value, options)
	args, ok := normalized.(map[string]any)
	if !ok {
		return normalized
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]toolCallArgsEntry, 0, len(args))
	seen := make(map[string]struct{}, len(args))
	collided := false
	for _, key := range keys {
		normalizedKey, known := toolCalls.lookup(key)
		if !known {
			normalizedKey = key
		}
		if _, exists := seen[normalizedKey]; exists {
			collided = true
		}
		seen[normalizedKey] = struct{}{}
		entries = append(entries, toolCallArgsEntry{
			key:   normalizedKey,
			known: known,
			value: args[key],
		})
	}
	if collided {
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].key != entries[j].key {
				return entries[i].key < entries[j].key
			}
			return entries[i].known && !entries[j].known
		})
		return toolCallArgsEntries(entries)
	}
	remapped := make(map[string]any, len(args))
	for _, entry := range entries {
		remapped[entry.key] = entry.value
	}
	return remapped
}

func toolCallArgsEntries(entries []toolCallArgsEntry) []map[string]any {
	normalized := make([]map[string]any, len(entries))
	for i, entry := range entries {
		normalized[i] = map[string]any{
			toolCallArgsEntryKey:   entry.key,
			toolCallArgsEntryKnown: entry.known,
			toolCallArgsEntryValue: entry.value,
		}
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

func normalizeMemoryEventTimes(snapshot *Snapshot, precision time.Duration) {
	references := make([]mapTimeReference, 0)
	for i := range snapshot.Memories {
		references = appendMemoryEventTimeReference(references, snapshot.Memories[i].Metadata)
	}
	for i := range snapshot.MemorySearches {
		for j := range snapshot.MemorySearches[i].Results {
			references = appendMemoryEventTimeReference(
				references,
				snapshot.MemorySearches[i].Results[j].Metadata,
			)
		}
	}
	if len(references) == 0 {
		return
	}
	times := make([]*time.Time, 0, len(references))
	for _, reference := range references {
		times = append(times, reference.value)
	}
	normalizeTimes(times, precision)
	for _, reference := range references {
		reference.values[reference.key] = reference.value.UTC().Format(time.RFC3339Nano)
	}
}

func appendMemoryEventTimeReference(
	references []mapTimeReference,
	values map[string]any,
) []mapTimeReference {
	if values == nil {
		return references
	}
	timestamp, ok := memoryEventTime(values[memoryEventTimeMetadataKey])
	if !ok || timestamp.IsZero() {
		return references
	}
	value := timestamp
	return append(references, mapTimeReference{
		values: values,
		key:    memoryEventTimeMetadataKey,
		value:  &value,
	})
}

func memoryEventTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed, true
	case *time.Time:
		if typed == nil {
			return time.Time{}, false
		}
		return *typed, true
	case string:
		timestamp, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			return time.Time{}, false
		}
		return timestamp, true
	default:
		return time.Time{}, false
	}
}

func normalizeJSONLike(value any, options NormalizeOptions) any {
	if invalid, ok := normalizeInvalidRawJSON(value); ok {
		return invalid
	}
	switch typed := value.(type) {
	case nil:
		return typed
	case json.Number:
		return normalizeJSONNumbers(typed)
	case json.RawMessage:
		if decoded, valid := decodeJSON(typed); valid {
			return normalizeJSONLike(decoded, options)
		}
		return invalidRawJSONValue(typed)
	case []byte:
		if decoded, valid := decodeJSON(typed); valid {
			return normalizeJSONLike(decoded, options)
		}
		return invalidRawJSONValue(typed)
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

func normalizeInvalidRawJSON(value any) (invalidRawJSONValue, bool) {
	invalid, ok := value.(invalidRawJSONValue)
	if !ok {
		return "", false
	}
	return invalid, true
}

func normalizeScalar(value any) (any, bool) {
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Bool:
		return reflected.Bool(), true
	case reflect.String:
		return reflected.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return normalizeJSONNumbers(json.Number(strconv.FormatInt(reflected.Int(), 10))), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return normalizeJSONNumbers(json.Number(strconv.FormatUint(reflected.Uint(), 10))), true
	case reflect.Float32, reflect.Float64:
		if math.IsNaN(reflected.Float()) || math.IsInf(reflected.Float(), 0) {
			return reflected.Float(), true
		}
		return normalizeJSONNumbers(json.Number(strconv.FormatFloat(
			reflected.Float(), 'g', -1, reflected.Type().Bits(),
		))), true
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
		canonical, ok := canonicalJSONNumber(typed.String())
		if !ok {
			return typed.String()
		}
		if !strings.ContainsAny(canonical, ".eE") {
			if integer, err := strconv.ParseInt(canonical, 10, 64); err == nil {
				return integer
			}
		}
		return json.Number(canonical)
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

const maxPlainJSONNumberLength = 4096

func canonicalJSONNumber(value string) (string, bool) {
	if _, err := json.Marshal(json.Number(value)); err != nil {
		return "", false
	}
	negative := strings.HasPrefix(value, "-")
	if negative {
		value = value[1:]
	}
	mantissa := value
	exponent := new(big.Int)
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		mantissa = value[:index]
		if _, ok := exponent.SetString(value[index+1:], 10); !ok {
			return "", false
		}
	}
	integer := mantissa
	fraction := ""
	if index := strings.IndexByte(mantissa, '.'); index >= 0 {
		integer = mantissa[:index]
		fraction = mantissa[index+1:]
	}
	digits := strings.TrimLeft(integer+fraction, "0")
	if digits == "" {
		return "0", true
	}
	scale := new(big.Int).Set(exponent)
	scale.Sub(scale, big.NewInt(int64(len(fraction))))
	trimmed := strings.TrimRight(digits, "0")
	scale.Add(scale, big.NewInt(int64(len(digits)-len(trimmed))))
	digits = trimmed
	sign := ""
	if negative {
		sign = "-"
	}
	if scale.IsInt64() {
		if plain, ok := plainJSONNumber(digits, scale.Int64()); ok {
			return sign + plain, true
		}
	}
	scientificExponent := new(big.Int).Add(
		scale, big.NewInt(int64(len(digits)-1)),
	)
	coefficient := digits[:1]
	if len(digits) > 1 {
		coefficient += "." + digits[1:]
	}
	return sign + coefficient + "e" + scientificExponent.String(), true
}

func plainJSONNumber(digits string, scale int64) (string, bool) {
	digitCount := int64(len(digits))
	if scale >= 0 {
		if scale > maxPlainJSONNumberLength-digitCount {
			return "", false
		}
		return digits + strings.Repeat("0", int(scale)), true
	}
	decimalPosition := digitCount + scale
	if decimalPosition > 0 {
		return digits[:decimalPosition] + "." + digits[decimalPosition:], true
	}
	zeroCount := -decimalPosition
	if zeroCount > maxPlainJSONNumberLength-digitCount-2 {
		return "", false
	}
	return "0." + strings.Repeat("0", int(zeroCount)) + digits, true
}

func sessionSortKey(snapshot SessionSnapshot) string {
	return snapshot.AppName + "\x00" + snapshot.UserID + "\x00" + snapshot.ID
}

func normalizeSessionTimes(snapshot *SessionSnapshot, precision time.Duration) {
	normalizeSessionMetadataTimes(&snapshot.CreatedAt, &snapshot.UpdatedAt)
	conversationTimes := make([]*time.Time, 0, len(snapshot.Events)+len(snapshot.Summaries))
	var boundaryTimes []mapTimeReference
	for i := range snapshot.Events {
		conversationTimes = append(conversationTimes, &snapshot.Events[i].Timestamp)
	}
	for i := range snapshot.Summaries {
		conversationTimes = append(conversationTimes, &snapshot.Summaries[i].UpdatedAt)
		if cutoff, ok := summaryBoundaryCutoffTime(snapshot.Summaries[i].Boundary); ok {
			value := cutoff
			boundaryTimes = append(boundaryTimes, mapTimeReference{
				values: snapshot.Summaries[i].Boundary,
				key:    "cutoff_at",
				value:  &value,
			})
			conversationTimes = append(conversationTimes, &value)
		}
	}
	normalizeTimes(conversationTimes, precision)
	for _, boundaryTime := range boundaryTimes {
		boundaryTime.values[boundaryTime.key] = *boundaryTime.value
	}
	trackTimes := make([]*time.Time, 0)
	for i := range snapshot.Tracks {
		for j := range snapshot.Tracks[i].Events {
			trackTimes = append(trackTimes, &snapshot.Tracks[i].Events[j].Timestamp)
		}
	}
	normalizeTimes(trackTimes, precision)
}

func summaryBoundaryCutoffTime(boundary map[string]any) (time.Time, bool) {
	if boundary == nil {
		return time.Time{}, false
	}
	raw, ok := boundary["cutoff_at"]
	if !ok {
		return time.Time{}, false
	}
	var value time.Time
	switch typed := raw.(type) {
	case time.Time:
		value = typed
	case *time.Time:
		if typed == nil {
			return time.Time{}, false
		}
		value = *typed
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			return time.Time{}, false
		}
		value = parsed
	default:
		return time.Time{}, false
	}
	if value.IsZero() {
		return time.Time{}, false
	}
	return value, true
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

// timeRanks returns a mapping from each input time to a deterministic rank
// time. Times closer than precision share one rank, and ranks preserve the
// relative order of the input times.
func timeRanks(values []time.Time, precision time.Duration) map[time.Time]time.Time {
	if precision <= 0 {
		precision = time.Millisecond
	}
	ordered := append([]time.Time(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	ranks := make(map[time.Time]time.Time, len(ordered))
	var anchor time.Time
	rank := 0
	for _, value := range ordered {
		if rank == 0 || value.Sub(anchor) >= precision {
			rank++
			anchor = value
		}
		ranks[value] = time.Unix(0, int64(rank)).UTC()
	}
	return ranks
}

func normalizeTimes(values []*time.Time, precision time.Duration) {
	if precision <= 0 {
		precision = time.Millisecond
	}
	ordered := make([]time.Time, 0, len(values))
	for _, value := range values {
		if value != nil && !value.IsZero() {
			ordered = append(ordered, *value)
		}
	}
	ranks := timeRanks(ordered, precision)
	for _, value := range values {
		if value == nil || value.IsZero() {
			continue
		}
		*value = ranks[*value]
	}
}

// cloneSnapshot returns a deep copy of the snapshot with JSON-like values
// cloned through reflection.
func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned, err := transformSnapshotValues(snapshot, func(value any) (any, error) {
		return cloneJSONLike(value), nil
	})
	if err != nil {
		// Unreachable: the reflection clone transform never returns an error.
		panic(err)
	}
	return cloned
}

// isolateSnapshot validates and detaches every JSON-like snapshot value so
// that downstream normalization, invariants, comparison, and report encoding
// only ever observe isolated, serializable JSON trees. Values that cannot be
// isolated (unexported mutable fields, cyclic references, or non-serializable
// types) are rejected before fixture cleanup can mutate them.
func isolateSnapshot(snapshot Snapshot) (Snapshot, error) {
	return transformSnapshotValues(snapshot, validateAndDetachJSONLike)
}

// validateAndDetachJSONLike validates that a JSON-like value can be safely
// isolated and then converts it into a pure JSON tree. Validation rejects
// values with unexported mutable state (which JSON encoding would silently
// drop), cyclic references, and non-serializable types; detachment guarantees
// the result cannot share references with fixture-owned data.
func validateAndDetachJSONLike(value any) (any, error) {
	if err := validateCloneableJSONLike(value); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("snapshot value cannot be serialized: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var detached any
	if err := decoder.Decode(&detached); err != nil {
		return nil, fmt.Errorf("snapshot value cannot be detached: %w", err)
	}
	return detached, nil
}

// transformSnapshotValues returns a deep copy of the snapshot in which every
// JSON-like value slot is replaced by the transform result.
func transformSnapshotValues(
	snapshot Snapshot,
	transform func(any) (any, error),
) (Snapshot, error) {
	cloned := Snapshot{
		Sessions:       append([]SessionSnapshot(nil), snapshot.Sessions...),
		Memories:       append([]MemorySnapshot(nil), snapshot.Memories...),
		MemorySearches: append([]MemorySearchSnapshot(nil), snapshot.MemorySearches...),
		Unsupported:    append([]UnsupportedFeature(nil), snapshot.Unsupported...),
	}
	for i := range cloned.Sessions {
		if err := transformSessionValues(&cloned.Sessions[i], transform); err != nil {
			return Snapshot{}, err
		}
	}
	for i := range cloned.Memories {
		cloned.Memories[i].Topics = append([]string(nil), cloned.Memories[i].Topics...)
		var err error
		cloned.Memories[i].Metadata, err = transformStringMap(cloned.Memories[i].Metadata, transform)
		if err != nil {
			return Snapshot{}, fmt.Errorf("memory %q metadata: %w", cloned.Memories[i].ID, err)
		}
	}
	for i := range cloned.MemorySearches {
		search := &cloned.MemorySearches[i]
		search.Results = append([]MemorySnapshot(nil), search.Results...)
		for j := range search.Results {
			search.Results[j].Topics = append([]string(nil), search.Results[j].Topics...)
			var err error
			search.Results[j].Metadata, err = transformStringMap(search.Results[j].Metadata, transform)
			if err != nil {
				return Snapshot{}, fmt.Errorf("memory search %q result %d metadata: %w", search.Query, j, err)
			}
		}
	}
	return cloned, nil
}

func transformSessionValues(
	session *SessionSnapshot,
	transform func(any) (any, error),
) error {
	var err error
	session.State, err = transformStateMap(session.State, transform)
	if err != nil {
		return fmt.Errorf("session %q state: %w", session.ID, err)
	}
	session.Events = append([]EventSnapshot(nil), session.Events...)
	for j := range session.Events {
		if err := transformEventValues(&session.Events[j], session.ID, j, transform); err != nil {
			return err
		}
	}
	session.Summaries = append([]SummarySnapshot(nil), session.Summaries...)
	for j := range session.Summaries {
		session.Summaries[j].Boundary, err = transformStringMap(session.Summaries[j].Boundary, transform)
		if err != nil {
			return fmt.Errorf(
				"session %q summary %q boundary: %w", session.ID, session.Summaries[j].FilterKey, err,
			)
		}
	}
	session.Tracks = append([]TrackSnapshot(nil), session.Tracks...)
	for j := range session.Tracks {
		session.Tracks[j].Events = append([]TrackEventSnapshot(nil), session.Tracks[j].Events...)
		for k := range session.Tracks[j].Events {
			session.Tracks[j].Events[k].Payload, err = transformStringMap(
				session.Tracks[j].Events[k].Payload, transform,
			)
			if err != nil {
				return fmt.Errorf(
					"session %q track %q event %d payload: %w", session.ID, session.Tracks[j].Name, k, err,
				)
			}
		}
	}
	return nil
}

func transformEventValues(
	event *EventSnapshot,
	sessionID string,
	index int,
	transform func(any) (any, error),
) error {
	var err error
	event.StateDelta, err = transformStateMap(event.StateDelta, transform)
	if err != nil {
		return fmt.Errorf("session %q event %d state delta: %w", sessionID, index, err)
	}
	event.Extensions, err = transformStringMap(event.Extensions, transform)
	if err != nil {
		return fmt.Errorf("session %q event %d extensions: %w", sessionID, index, err)
	}
	event.ToolCalls = append([]ToolCallSnapshot(nil), event.ToolCalls...)
	for k := range event.ToolCalls {
		event.ToolCalls[k].Arguments, err = transform(event.ToolCalls[k].Arguments)
		if err != nil {
			return fmt.Errorf(
				"session %q event %d tool call %d arguments: %w", sessionID, index, k, err,
			)
		}
		event.ToolCalls[k].Extra, err = transformStringMap(event.ToolCalls[k].Extra, transform)
		if err != nil {
			return fmt.Errorf(
				"session %q event %d tool call %d extra: %w", sessionID, index, k, err,
			)
		}
	}
	if event.ToolResponse != nil {
		response := *event.ToolResponse
		response.Extra, err = transformStringMap(response.Extra, transform)
		if err != nil {
			return fmt.Errorf(
				"session %q event %d tool response extra: %w", sessionID, index, err,
			)
		}
		event.ToolResponse = &response
	}
	return nil
}

func transformStringMap(
	value map[string]any,
	transform func(any) (any, error),
) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		transformed, err := transform(item)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		cloned[key] = transformed
	}
	return cloned, nil
}

func transformStateMap(
	values map[string]StateValueSnapshot,
	transform func(any) (any, error),
) (map[string]StateValueSnapshot, error) {
	if values == nil {
		return nil, nil
	}
	cloned := make(map[string]StateValueSnapshot, len(values))
	for key, value := range values {
		transformed, err := transform(value.Value)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		value.Value = transformed
		cloned[key] = value
	}
	return cloned, nil
}

func cloneJSONLike(value any) any {
	if value == nil {
		return nil
	}
	cloner := jsonLikeCloner{visited: make(map[cloneReference]reflect.Value)}
	return cloner.clone(reflect.ValueOf(value)).Interface()
}

type cloneReference struct {
	typeOf   reflect.Type
	kind     reflect.Kind
	pointer  uintptr
	length   int
	capacity int
}

type jsonLikeCloner struct {
	visited map[cloneReference]reflect.Value
}

// cloneValidationState tracks references that are fully validated (done) and
// references on the current validation path (stack). A reference on the stack
// is a cycle; a reference in done is a shared acyclic subgraph that was
// already validated and can be skipped, matching JSON serialization semantics.
type cloneValidationState struct {
	done  map[cloneReference]struct{}
	stack map[cloneReference]struct{}
}

func validateCloneableJSONLike(value any) error {
	if value == nil {
		return nil
	}
	return validateCloneableJSONLikeValue(reflect.ValueOf(value), &cloneValidationState{
		done:  make(map[cloneReference]struct{}),
		stack: make(map[cloneReference]struct{}),
	})
}

func validateCloneableJSONLikeValue(
	value reflect.Value,
	state *cloneValidationState,
) error {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface:
		return validateCloneableInterface(value, state)
	case reflect.Map:
		return validateCloneableMap(value, state)
	case reflect.Pointer:
		return validateCloneablePointer(value, state)
	case reflect.Slice:
		return validateCloneableSlice(value, state)
	case reflect.Array:
		return validateCloneableArray(value, state)
	case reflect.Struct:
		return validateCloneableStruct(value, state)
	case reflect.Chan, reflect.Func, reflect.UnsafePointer,
		reflect.Complex64, reflect.Complex128, reflect.Uintptr:
		return fmt.Errorf("value type %s cannot be safely cloned", value.Type())
	}
	return nil
}

func validateCloneableInterface(
	value reflect.Value,
	state *cloneValidationState,
) error {
	if value.IsNil() {
		return nil
	}
	return validateCloneableJSONLikeValue(value.Elem(), state)
}

func validateCloneableMap(
	value reflect.Value,
	state *cloneValidationState,
) error {
	if value.IsNil() {
		return nil
	}
	reference := mapCloneReference(value)
	proceed, err := enterCloneReference(state, reference, value.Type())
	if err != nil || !proceed {
		return err
	}
	defer exitCloneReference(state, reference)
	iterator := value.MapRange()
	for iterator.Next() {
		if !isSafeJSONMapKey(iterator.Key()) {
			return fmt.Errorf("map key type %s cannot be safely cloned", iterator.Key().Type())
		}
		if err := validateCloneableJSONLikeValue(iterator.Value(), state); err != nil {
			return err
		}
	}
	finishCloneReference(state, reference)
	return nil
}

func validateCloneablePointer(
	value reflect.Value,
	state *cloneValidationState,
) error {
	if value.IsNil() {
		return nil
	}
	reference := pointerCloneReference(value)
	proceed, err := enterCloneReference(state, reference, value.Type())
	if err != nil || !proceed {
		return err
	}
	defer exitCloneReference(state, reference)
	if err := validateCloneableJSONLikeValue(value.Elem(), state); err != nil {
		return err
	}
	finishCloneReference(state, reference)
	return nil
}

func validateCloneableSlice(
	value reflect.Value,
	state *cloneValidationState,
) error {
	if value.IsNil() {
		return nil
	}
	reference := sliceCloneReference(value)
	proceed, err := enterCloneReference(state, reference, value.Type())
	if err != nil || !proceed {
		return err
	}
	defer exitCloneReference(state, reference)
	for i := 0; i < value.Len(); i++ {
		if err := validateCloneableJSONLikeValue(value.Index(i), state); err != nil {
			return err
		}
	}
	finishCloneReference(state, reference)
	return nil
}

func validateCloneableArray(
	value reflect.Value,
	state *cloneValidationState,
) error {
	for i := 0; i < value.Len(); i++ {
		if err := validateCloneableJSONLikeValue(value.Index(i), state); err != nil {
			return err
		}
	}
	return nil
}

func validateCloneableStruct(
	value reflect.Value,
	state *cloneValidationState,
) error {
	if value.Type() == reflect.TypeOf(time.Time{}) {
		return nil
	}
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		if field.PkgPath != "" {
			if typeContainsMutableReference(field.Type, make(map[reflect.Type]bool)) {
				return fmt.Errorf(
					"struct %s has unexported mutable field %s",
					value.Type(), field.Name,
				)
			}
			continue
		}
		if err := validateCloneableJSONLikeValue(value.Field(i), state); err != nil {
			return err
		}
	}
	return nil
}

func enterCloneReference(
	state *cloneValidationState,
	reference cloneReference,
	valueType reflect.Type,
) (bool, error) {
	if _, onStack := state.stack[reference]; onStack {
		return false, fmt.Errorf("value type %s contains a cyclic reference", valueType)
	}
	if _, finished := state.done[reference]; finished {
		return false, nil
	}
	state.stack[reference] = struct{}{}
	return true, nil
}

func exitCloneReference(state *cloneValidationState, reference cloneReference) {
	delete(state.stack, reference)
}

func finishCloneReference(state *cloneValidationState, reference cloneReference) {
	state.done[reference] = struct{}{}
}

func isSafeJSONMapKey(value reflect.Value) bool {
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

func typeContainsMutableReference(value reflect.Type, visiting map[reflect.Type]bool) bool {
	if value == reflect.TypeOf(time.Time{}) {
		return false
	}
	if visiting[value] {
		return false
	}
	visiting[value] = true
	defer delete(visiting, value)
	switch value.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice, reflect.Interface,
		reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return typeContainsMutableReference(value.Elem(), visiting)
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if typeContainsMutableReference(value.Field(i).Type, visiting) {
				return true
			}
		}
	}
	return false
}

func (cloner *jsonLikeCloner) clone(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		return cloner.cloneInterface(value)
	case reflect.Map:
		return cloner.cloneMap(value)
	case reflect.Pointer:
		return cloner.clonePointer(value)
	case reflect.Slice:
		return cloner.cloneSlice(value)
	case reflect.Array:
		return cloner.cloneArray(value)
	case reflect.Struct:
		return cloner.cloneStruct(value)
	default:
		return value
	}
}

func mapCloneReference(value reflect.Value) cloneReference {
	return cloneReference{
		typeOf: value.Type(), kind: value.Kind(), pointer: value.Pointer(),
	}
}

func pointerCloneReference(value reflect.Value) cloneReference {
	return cloneReference{
		typeOf: value.Type(), kind: value.Kind(), pointer: value.Pointer(),
	}
}

func sliceCloneReference(value reflect.Value) cloneReference {
	return cloneReference{
		typeOf: value.Type(), kind: value.Kind(), pointer: value.Pointer(),
		length: value.Len(), capacity: value.Cap(),
	}
}

func (cloner *jsonLikeCloner) cloneInterface(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	cloned := reflect.New(value.Type()).Elem()
	cloned.Set(cloner.clone(value.Elem()))
	return cloned
}

func (cloner *jsonLikeCloner) cloneMap(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	reference := mapCloneReference(value)
	if cloned, ok := cloner.visited[reference]; ok {
		return cloned
	}
	cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
	cloner.visited[reference] = cloned
	iterator := value.MapRange()
	for iterator.Next() {
		cloned.SetMapIndex(iterator.Key(), cloner.clone(iterator.Value()))
	}
	return cloned
}

func (cloner *jsonLikeCloner) clonePointer(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	reference := pointerCloneReference(value)
	if cloned, ok := cloner.visited[reference]; ok {
		return cloned
	}
	cloned := reflect.New(value.Type().Elem())
	cloner.visited[reference] = cloned
	cloned.Elem().Set(cloner.clone(value.Elem()))
	return cloned
}

func (cloner *jsonLikeCloner) cloneSlice(value reflect.Value) reflect.Value {
	if value.IsNil() {
		return reflect.Zero(value.Type())
	}
	reference := sliceCloneReference(value)
	if cloned, ok := cloner.visited[reference]; ok {
		return cloned
	}
	cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	cloner.visited[reference] = cloned
	for i := 0; i < value.Len(); i++ {
		cloned.Index(i).Set(cloner.clone(value.Index(i)))
	}
	return cloned
}

func (cloner *jsonLikeCloner) cloneArray(value reflect.Value) reflect.Value {
	cloned := reflect.New(value.Type()).Elem()
	for i := 0; i < value.Len(); i++ {
		cloned.Index(i).Set(cloner.clone(value.Index(i)))
	}
	return cloned
}

func (cloner *jsonLikeCloner) cloneStruct(value reflect.Value) reflect.Value {
	cloned := reflect.New(value.Type()).Elem()
	cloned.Set(value)
	for i := 0; i < value.NumField(); i++ {
		target := cloned.Field(i)
		source := value.Field(i)
		if !target.CanSet() || !source.CanInterface() {
			continue
		}
		target.Set(cloner.clone(source))
	}
	return cloned
}

func memorySortKey(snapshot MemorySnapshot) string {
	semantic := struct {
		AppName   string         `json:"app_name"`
		UserID    string         `json:"user_id"`
		Scope     MemoryScope    `json:"scope"`
		Content   string         `json:"content"`
		Topics    []string       `json:"topics,omitempty"`
		Metadata  map[string]any `json:"metadata,omitempty"`
		Score     float64        `json:"score"`
		CreatedAt time.Time      `json:"created_at,omitempty"`
		UpdatedAt time.Time      `json:"updated_at,omitempty"`
	}{
		AppName: snapshot.AppName, UserID: snapshot.UserID, Scope: snapshot.Scope,
		Content: snapshot.Content, Topics: snapshot.Topics, Metadata: snapshot.Metadata,
		Score: snapshot.Score, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt,
	}
	return stableKey(semantic) + "\x00" + snapshot.ID
}

func memorySearchSortKey(snapshot MemorySearchSnapshot) string {
	return snapshot.AppName + "\x00" + snapshot.UserID + "\x00" + snapshot.Query
}

func stableKey(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(encoded)
}

type logicalIDMap struct {
	prefix string
	values map[string]string
}

type scopedLogicalIDMaps struct {
	prefix string
	values map[MemoryScope]*logicalIDMap
}

func newLogicalIDMap(prefix string) *logicalIDMap {
	return &logicalIDMap{prefix: prefix, values: make(map[string]string)}
}

func newScopedLogicalIDMaps(prefix string) *scopedLogicalIDMaps {
	return &scopedLogicalIDMaps{
		prefix: prefix,
		values: make(map[MemoryScope]*logicalIDMap),
	}
}

func (mappings *scopedLogicalIDMaps) value(scope MemoryScope, id string) string {
	mapping, ok := mappings.values[scope]
	if !ok {
		mapping = newLogicalIDMap(mappings.prefix)
		mappings.values[scope] = mapping
	}
	return mapping.value(id)
}

func memoryIDScope(snapshot MemorySnapshot, fallback MemoryScope) MemoryScope {
	if snapshot.Scope != (MemoryScope{}) {
		return snapshot.Scope
	}
	if snapshot.AppName != "" || snapshot.UserID != "" {
		return MemoryScope{AppName: snapshot.AppName, UserID: snapshot.UserID}
	}
	return fallback
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

func (mapping *logicalIDMap) lookup(id string) (string, bool) {
	if id == "" {
		return "", true
	}
	value, ok := mapping.values[id]
	return value, ok
}
