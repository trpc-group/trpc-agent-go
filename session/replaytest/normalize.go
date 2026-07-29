//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type identityNormalizer struct {
	invocations map[string]string
	toolCalls   map[string]string
}

func newIdentityNormalizer() *identityNormalizer {
	return &identityNormalizer{
		invocations: make(map[string]string),
		toolCalls:   make(map[string]string),
	}
}

// NormalizeEvents converts persisted events into deterministic semantic
// snapshots. It returns canonical events and the original persisted order.
func NormalizeEvents(
	events []event.Event,
	canonicalOrder bool,
) ([]EventSnapshot, []string) {
	ids := newIdentityNormalizer()
	seedEventIdentities(events, canonicalOrder, ids)
	out := make([]EventSnapshot, 0, len(events))
	observed := make([]string, 0, len(events))
	for i := range events {
		normalized := normalizeEvent(events[i], i, ids)
		observed = append(observed, normalized.ID)
		out = append(out, normalized)
	}
	if canonicalOrder {
		sort.SliceStable(out, func(i, j int) bool {
			left := out[i].Sequence
			right := out[j].Sequence
			switch {
			case left > 0 && right > 0 && left != right:
				return left < right
			case left > 0 && right == 0:
				return true
			case left == 0 && right > 0:
				return false
			default:
				return false
			}
		})
	}
	for i := range out {
		out[i].Index = i
	}
	return out, observed
}

func seedEventIdentities(
	events []event.Event,
	canonicalOrder bool,
	ids *identityNormalizer,
) {
	order := make([]int, len(events))
	sequences := make([]int, len(events))
	for i := range events {
		order[i] = i
		sequences[i] = extensionInt(
			normalizeExtensions(events[i].Extensions)[ExtensionSequence],
		)
	}
	if canonicalOrder {
		sort.SliceStable(order, func(i, j int) bool {
			left := sequences[order[i]]
			right := sequences[order[j]]
			switch {
			case left > 0 && right > 0 && left != right:
				return left < right
			case left > 0 && right == 0:
				return true
			case left == 0 && right > 0:
				return false
			default:
				return false
			}
		})
	}
	for _, index := range order {
		item := events[index]
		ids.invocation(item.InvocationID)
		ids.invocation(item.ParentInvocationID)
		message := responseMessage(item.Response)
		ids.toolCall(message.ToolID)
		for _, call := range message.ToolCalls {
			ids.toolCall(call.ID)
		}
	}
}

func normalizeEvent(
	input event.Event,
	index int,
	ids *identityNormalizer,
) EventSnapshot {
	extensions := normalizeExtensions(input.Extensions)
	sequence := extensionInt(extensions[ExtensionSequence])
	logicalID := extensionString(extensions[ExtensionLogicalID])
	if logicalID == "" {
		logicalID = normalizeEventID(input.ID, index, sequence)
	}
	message := responseMessage(input.Response)

	out := EventSnapshot{
		Index:              index,
		ID:                 logicalID,
		Sequence:           sequence,
		Author:             input.Author,
		Role:               string(message.Role),
		Content:            message.Content,
		ToolID:             ids.toolCall(message.ToolID),
		ToolName:           message.ToolName,
		InvocationID:       ids.invocation(input.InvocationID),
		ParentInvocationID: ids.invocation(input.ParentInvocationID),
		Branch:             input.Branch,
		Tag:                normalizeTags(input.Tag),
		FilterKey:          input.FilterKey,
		Timestamp:          normalizeTime(input.Timestamp),
	}
	if len(message.ToolCalls) > 0 {
		out.ToolCalls = make(
			[]ToolCallSnapshot,
			0,
			len(message.ToolCalls),
		)
		for _, call := range message.ToolCalls {
			extraFields := make(map[string]any, len(call.ExtraFields))
			for key, value := range call.ExtraFields {
				extraFields[key] = normalizeAny(value)
			}
			if len(extraFields) == 0 {
				extraFields = nil
			}
			out.ToolCalls = append(out.ToolCalls, ToolCallSnapshot{
				ID:          ids.toolCall(call.ID),
				Type:        call.Type,
				Name:        call.Function.Name,
				Arguments:   normalizeJSONBytes(call.Function.Arguments),
				ExtraFields: extraFields,
			})
		}
	}
	if len(input.StateDelta) > 0 {
		out.StateDelta = make(map[string]any, len(input.StateDelta))
		for key, value := range input.StateDelta {
			out.StateDelta[key] = normalizeJSONBytes(value)
		}
	}
	if len(extensions) > 0 {
		out.Extensions = make(map[string]any, len(extensions))
		for key, value := range extensions {
			if key == ExtensionLogicalID || key == ExtensionSequence {
				continue
			}
			out.Extensions[key] = value
		}
		if len(out.Extensions) == 0 {
			out.Extensions = nil
		}
	}
	return out
}

func normalizeExtensions(
	extensions map[string]json.RawMessage,
) map[string]any {
	if len(extensions) == 0 {
		return nil
	}
	out := make(map[string]any, len(extensions))
	for key, raw := range extensions {
		value := normalizeJSONBytes(raw)
		if key == "trpc_agent" {
			expandTRPCExtensions(out, key, value)
			continue
		}
		out[key] = value
	}
	return out
}

func expandTRPCExtensions(
	out map[string]any,
	prefix string,
	value any,
) {
	if isKnownTRPCExtension(prefix) {
		out[prefix] = value
		return
	}
	values, ok := value.(map[string]any)
	if !ok || prefix != "trpc_agent" && prefix != "trpc_agent.replay" {
		out[prefix] = value
		return
	}
	for key, child := range values {
		expandTRPCExtensions(out, prefix+"."+key, child)
	}
}

func isKnownTRPCExtension(key string) bool {
	return key == ExtensionLogicalID ||
		key == ExtensionSequence ||
		key == event.ToolCallArgsExtensionKey
}

func extensionString(value any) string {
	text, _ := value.(string)
	return text
}

func extensionInt(value any) int {
	number, ok := asFloat64(value)
	if !ok {
		return 0
	}
	return int(number)
}

func responseMessage(response *model.Response) model.Message {
	if response == nil || len(response.Choices) == 0 {
		return model.Message{}
	}
	choice := response.Choices[0]
	if messageHasReplayData(choice.Message) {
		return choice.Message
	}
	return choice.Delta
}

func messageHasReplayData(message model.Message) bool {
	return message.Role != "" ||
		message.Content != "" ||
		message.ToolID != "" ||
		message.ToolName != "" ||
		len(message.ToolCalls) > 0
}

func normalizeEventID(id string, index, sequence int) string {
	if id == "" || isUUID(id) {
		if sequence > 0 {
			return eventAlias(sequence - 1)
		}
		return eventAlias(index)
	}
	return id
}

func eventAlias(index int) string {
	const digits = "000000"
	value := []byte(digits)
	number := index + 1
	for position := len(value) - 1; position >= 0 && number > 0; position-- {
		value[position] = byte('0' + number%10)
		number /= 10
	}
	return "event-" + string(value)
}

func (n *identityNormalizer) invocation(id string) string {
	return normalizeGeneratedID(id, "invocation", n.invocations)
}

func (n *identityNormalizer) toolCall(id string) string {
	return normalizeGeneratedID(id, "tool-call", n.toolCalls)
}

func normalizeGeneratedID(
	id string,
	prefix string,
	aliases map[string]string,
) string {
	if id == "" || !isUUID(id) {
		return id
	}
	if alias, ok := aliases[id]; ok {
		return alias
	}
	alias := prefix + "-" + intString(len(aliases)+1)
	aliases[id] = alias
	return alias
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

func isUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func normalizeTags(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	parts := strings.Split(value, event.TagDelimiter)
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	sort.Strings(normalized)
	return strings.Join(normalized, event.TagDelimiter)
}

// NormalizeState converts JSON-encoded state values into structural values.
// The internal track index is omitted because tracks are compared separately.
func NormalizeState(state session.StateMap) map[string]any {
	out := make(map[string]any, len(state))
	for key, value := range state {
		if key == "tracks" {
			continue
		}
		out[key] = normalizeJSONBytes(value)
	}
	return out
}

// NormalizeMemory converts a backend entry into a semantic memory snapshot.
// The canonical ID is derived from scope, content, and metadata so backend
// generated IDs and timestamps do not create false positives.
func NormalizeMemory(entry *memory.Entry) MemorySnapshot {
	if entry == nil {
		return MemorySnapshot{}
	}
	out := MemorySnapshot{
		AppName: entry.AppName,
		UserID:  entry.UserID,
		Score:   roundFloat(entry.Score, 6),
	}
	if entry.Memory != nil {
		out.Content = entry.Memory.Memory
		out.Topics = sortedStrings(entry.Memory.Topics)
		out.Kind = string(entry.Memory.Kind)
		out.EventTime = normalizeTimePointer(entry.Memory.EventTime)
		out.Participants = sortedStrings(entry.Memory.Participants)
		out.Location = entry.Memory.Location
	}
	out.ID = semanticMemoryID(out)
	return out
}

func semanticMemoryID(memory MemorySnapshot) string {
	identity := struct {
		AppName      string   `json:"app_name"`
		UserID       string   `json:"user_id"`
		Content      string   `json:"content"`
		Topics       []string `json:"topics,omitempty"`
		Kind         string   `json:"kind,omitempty"`
		EventTime    string   `json:"event_time,omitempty"`
		Participants []string `json:"participants,omitempty"`
		Location     string   `json:"location,omitempty"`
	}{
		AppName:      memory.AppName,
		UserID:       memory.UserID,
		Content:      memory.Content,
		Topics:       memory.Topics,
		Kind:         memory.Kind,
		EventTime:    memory.EventTime,
		Participants: memory.Participants,
		Location:     memory.Location,
	}
	data, _ := json.Marshal(identity)
	sum := sha256.Sum256(data)
	return "memory-" + hex.EncodeToString(sum[:8])
}

// NormalizeTrackEvent converts a track payload into structural JSON and
// extracts portable fields used for cross-backend localization.
func NormalizeTrackEvent(
	input session.TrackEvent,
	index int,
) TrackEventSnapshot {
	payload := normalizeJSONBytes(input.Payload)
	payload = normalizeDurationFields(payload)
	out := TrackEventSnapshot{
		Index:     index,
		Payload:   payload,
		Timestamp: normalizeTime(input.Timestamp),
	}
	if values, ok := payload.(map[string]any); ok {
		out.EventType = firstString(
			values,
			"event_type",
			"eventType",
			"type",
		)
		out.InvocationID = firstString(
			values,
			"invocation_id",
			"invocationId",
		)
		out.Error = firstString(values, "error", "error_message", "message")
		out.DurationMS = firstNumber(
			values,
			"duration_ms",
			"durationMs",
			"latency_ms",
			"elapsed_ms",
		)
	}
	return out
}

// NormalizeSummary converts one filter-key summary into a portable revision.
func NormalizeSummary(
	sessionID string,
	filterKey string,
	summary *session.Summary,
	revision int,
) SummarySnapshot {
	if summary == nil {
		return SummarySnapshot{}
	}
	out := SummarySnapshot{
		ID:               sessionID + ":" + filterKey,
		SessionID:        sessionID,
		FilterKey:        filterKey,
		Text:             summary.Summary,
		Topics:           sortedStrings(summary.Topics),
		Revision:         revision,
		ReplacesRevision: maxInt(0, revision-1),
		UpdatedAt:        normalizeTime(summary.UpdatedAt),
	}
	if boundary := summary.CutoffBoundary(); boundary != nil {
		out.Version = boundary.Version
		out.BoundaryLastEventID = boundary.LastEventID
		if isUUID(out.BoundaryLastEventID) {
			out.BoundaryLastEventID = "event-auto"
		}
	}
	return out
}

func normalizeJSONBytes(raw []byte) any {
	if raw == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return string(raw)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return string(raw)
	}
	return normalizeJSONValue(value)
}

func normalizeAny(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return normalizeJSONBytes(data)
}

func normalizeJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeJSONValue(item)
		}
		return out
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		if number, err := typed.Float64(); err == nil {
			return number
		}
		return typed.String()
	default:
		return value
	}
}

func normalizeDurationFields(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			item = normalizeDurationFields(item)
			if isDurationField(key) {
				if number, ok := asFloat64(item); ok {
					item = roundFloat(number, 3)
				}
			}
			out[key] = item
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeDurationFields(item)
		}
		return out
	default:
		return value
	}
}

func isDurationField(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(
		strings.ToLower(key),
	)
	return normalized == "durationms" ||
		normalized == "latencyms" ||
		normalized == "elapsedms"
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return value
		}
	}
	return ""
}

func firstNumber(values map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := asFloat64(values[key]); ok {
			return roundFloat(value, 3)
		}
	}
	return 0
}

func normalizeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano)
}

func normalizeTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return normalizeTime(*value)
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func roundFloat(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
