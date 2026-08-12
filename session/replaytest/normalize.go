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
		normalizeMemoryValues(&normalized.Memories[i], options)
	}
	if options.SortMemories {
		sort.SliceStable(normalized.Memories, func(i, j int) bool {
			return memorySortKey(normalized.Memories[i]) < memorySortKey(normalized.Memories[j])
		})
	}
	for i := range normalized.Memories {
		normalizeMemoryID(&normalized.Memories[i], options, memoryIDs)
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
	memoryIDs *logicalIDMap,
) {
	normalizeMemoryValues(snapshot, options)
	normalizeMemoryID(snapshot, options, memoryIDs)
}

func normalizeMemoryValues(
	snapshot *MemorySnapshot,
	options NormalizeOptions,
) {
	normalizeTimes([]*time.Time{&snapshot.CreatedAt, &snapshot.UpdatedAt}, options.TimePrecision)
	snapshot.Topics = append([]string(nil), snapshot.Topics...)
	sort.Strings(snapshot.Topics)
	snapshot.Metadata = normalizeMetadataMap(snapshot.Metadata, options)
}

func normalizeMemoryID(
	snapshot *MemorySnapshot,
	options NormalizeOptions,
	memoryIDs *logicalIDMap,
) {
	if !options.PreserveMemoryIDs {
		snapshot.ID = memoryIDs.value(snapshot.ID)
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
	value, ok := boundary["cutoff_at"].(time.Time)
	if !ok || value.IsZero() {
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
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	ranks := make(map[time.Time]int, len(ordered))
	var anchor time.Time
	rank := 0
	for _, value := range ordered {
		if rank == 0 || value.Sub(anchor) >= precision {
			rank++
			anchor = value
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

func validateCloneableJSONLike(value any) error {
	if value == nil {
		return nil
	}
	return validateCloneableJSONLikeValue(
		reflect.ValueOf(value), make(map[cloneReference]struct{}),
	)
}

func validateCloneableJSONLikeValue(
	value reflect.Value,
	visited map[cloneReference]struct{},
) error {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface:
		return validateCloneableInterface(value, visited)
	case reflect.Map:
		return validateCloneableMap(value, visited)
	case reflect.Pointer:
		return validateCloneablePointer(value, visited)
	case reflect.Slice:
		return validateCloneableSlice(value, visited)
	case reflect.Array:
		return validateCloneableArray(value, visited)
	case reflect.Struct:
		return validateCloneableStruct(value, visited)
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return fmt.Errorf("value type %s cannot be safely cloned", value.Type())
	}
	return nil
}

func validateCloneableInterface(
	value reflect.Value,
	visited map[cloneReference]struct{},
) error {
	if value.IsNil() {
		return nil
	}
	return validateCloneableJSONLikeValue(value.Elem(), visited)
}

func validateCloneableMap(
	value reflect.Value,
	visited map[cloneReference]struct{},
) error {
	if value.IsNil() {
		return nil
	}
	if cloneReferenceVisited(visited, mapCloneReference(value)) {
		return nil
	}
	iterator := value.MapRange()
	for iterator.Next() {
		if !isSafeJSONMapKey(iterator.Key()) {
			return fmt.Errorf("map key type %s cannot be safely cloned", iterator.Key().Type())
		}
		if err := validateCloneableJSONLikeValue(iterator.Value(), visited); err != nil {
			return err
		}
	}
	return nil
}

func validateCloneablePointer(
	value reflect.Value,
	visited map[cloneReference]struct{},
) error {
	if value.IsNil() {
		return nil
	}
	if cloneReferenceVisited(visited, pointerCloneReference(value)) {
		return nil
	}
	return validateCloneableJSONLikeValue(value.Elem(), visited)
}

func validateCloneableSlice(
	value reflect.Value,
	visited map[cloneReference]struct{},
) error {
	if value.IsNil() {
		return nil
	}
	if cloneReferenceVisited(visited, sliceCloneReference(value)) {
		return nil
	}
	for i := 0; i < value.Len(); i++ {
		if err := validateCloneableJSONLikeValue(value.Index(i), visited); err != nil {
			return err
		}
	}
	return nil
}

func validateCloneableArray(
	value reflect.Value,
	visited map[cloneReference]struct{},
) error {
	for i := 0; i < value.Len(); i++ {
		if err := validateCloneableJSONLikeValue(value.Index(i), visited); err != nil {
			return err
		}
	}
	return nil
}

func validateCloneableStruct(
	value reflect.Value,
	visited map[cloneReference]struct{},
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
		if err := validateCloneableJSONLikeValue(value.Field(i), visited); err != nil {
			return err
		}
	}
	return nil
}

func cloneReferenceVisited(
	visited map[cloneReference]struct{},
	reference cloneReference,
) bool {
	if _, ok := visited[reference]; ok {
		return true
	}
	visited[reference] = struct{}{}
	return false
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
