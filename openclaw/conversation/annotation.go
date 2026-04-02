//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversation

import (
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// Annotation stores channel-provided conversation metadata.
type Annotation struct {
	HistoryMode   string            `json:"history_mode,omitempty"`
	StorageUserID string            `json:"storage_user_id,omitempty"`
	ActorID       string            `json:"actor_id,omitempty"`
	ActorLabel    string            `json:"actor_label,omitempty"`
	ActorLabels   map[string]string `json:"actor_labels,omitempty"`
	QuoteText     string            `json:"quote_text,omitempty"`
}

// MergeRequestExtension stores conversation metadata in request
// extensions.
func MergeRequestExtension(
	extensions map[string]json.RawMessage,
	annotation Annotation,
) (map[string]json.RawMessage, error) {
	annotation = normalizeAnnotation(annotation)
	if isZeroRuntimeAnnotation(annotation) {
		return extensions, nil
	}
	raw, err := json.Marshal(annotation)
	if err != nil {
		return nil, err
	}
	if extensions == nil {
		extensions = make(map[string]json.RawMessage)
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	extensions[ExtensionKey] = json.RawMessage(cloned)
	return extensions, nil
}

// AnnotationFromRequestExtensions decodes request conversation metadata.
func AnnotationFromRequestExtensions(
	extensions map[string]json.RawMessage,
) (Annotation, bool, error) {
	return decodeAnnotation(extensions)
}

// AnnotationFromRuntimeState decodes runtime conversation metadata.
func AnnotationFromRuntimeState(
	state map[string]any,
) (Annotation, bool) {
	if len(state) == 0 {
		return Annotation{}, false
	}
	value, ok := state[RuntimeStateKey]
	if !ok {
		return Annotation{}, false
	}
	annotation, ok := value.(Annotation)
	if !ok || isZeroRuntimeAnnotation(annotation) {
		return Annotation{}, false
	}
	return annotation, true
}

// RuntimeState returns runtime state for one run.
func RuntimeState(annotation Annotation) map[string]any {
	annotation = normalizeAnnotation(annotation)
	if isZeroRuntimeAnnotation(annotation) {
		return nil
	}
	return map[string]any{
		RuntimeStateKey: annotation,
	}
}

// SetEventAnnotation persists conversation metadata on one event.
func SetEventAnnotation(
	evt *event.Event,
	annotation Annotation,
) error {
	evtAnnotation, ok := persistableEventAnnotation(annotation)
	if !ok {
		return nil
	}
	return event.SetExtension(
		evt,
		ExtensionKey,
		evtAnnotation,
	)
}

// AnnotationFromEvent decodes persisted event metadata.
func AnnotationFromEvent(
	evt event.Event,
) (Annotation, bool, error) {
	type annotationAlias eventAnnotation
	decoded, ok, err := event.GetExtension[annotationAlias](
		&evt,
		ExtensionKey,
	)
	if err != nil || !ok {
		return Annotation{}, ok, err
	}
	if isZeroEventAnnotation(eventAnnotation(decoded)) {
		return Annotation{}, false, nil
	}
	return Annotation{
		ActorID:    strings.TrimSpace(decoded.ActorID),
		ActorLabel: strings.TrimSpace(decoded.ActorLabel),
		QuoteText:  strings.TrimSpace(decoded.QuoteText),
	}, true, nil
}

type eventAnnotation struct {
	ActorID    string `json:"actor_id,omitempty"`
	ActorLabel string `json:"actor_label,omitempty"`
	QuoteText  string `json:"quote_text,omitempty"`
}

func decodeAnnotation(
	extensions map[string]json.RawMessage,
) (Annotation, bool, error) {
	if len(extensions) == 0 {
		return Annotation{}, false, nil
	}
	raw, ok := extensions[ExtensionKey]
	if !ok || len(raw) == 0 {
		return Annotation{}, false, nil
	}
	var decoded Annotation
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Annotation{}, false, err
	}
	decoded = normalizeAnnotation(decoded)
	if isZeroRuntimeAnnotation(decoded) {
		return Annotation{}, false, nil
	}
	return decoded, true, nil
}

func isZeroRuntimeAnnotation(annotation Annotation) bool {
	return strings.TrimSpace(annotation.HistoryMode) == "" &&
		strings.TrimSpace(annotation.StorageUserID) == "" &&
		strings.TrimSpace(annotation.ActorID) == "" &&
		strings.TrimSpace(annotation.ActorLabel) == "" &&
		len(annotation.ActorLabels) == 0 &&
		strings.TrimSpace(annotation.QuoteText) == ""
}

func normalizeAnnotation(annotation Annotation) Annotation {
	annotation.HistoryMode = strings.TrimSpace(
		annotation.HistoryMode,
	)
	annotation.StorageUserID = strings.TrimSpace(
		annotation.StorageUserID,
	)
	annotation.ActorID = strings.TrimSpace(annotation.ActorID)
	annotation.ActorLabel = strings.TrimSpace(
		annotation.ActorLabel,
	)
	annotation.ActorLabels = normalizeActorLabels(
		annotation.ActorLabels,
	)
	annotation.QuoteText = strings.TrimSpace(annotation.QuoteText)
	return annotation
}

func normalizeActorLabels(
	labels map[string]string,
) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for actorID, label := range labels {
		actorID = strings.TrimSpace(actorID)
		label = strings.TrimSpace(label)
		if actorID == "" || label == "" {
			continue
		}
		out[actorID] = label
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func persistableEventAnnotation(
	annotation Annotation,
) (eventAnnotation, bool) {
	annotation = normalizeAnnotation(annotation)
	evtAnnotation := eventAnnotation{
		ActorID:    annotation.ActorID,
		ActorLabel: annotation.ActorLabel,
		QuoteText:  annotation.QuoteText,
	}
	if isZeroEventAnnotation(evtAnnotation) {
		return eventAnnotation{}, false
	}
	return evtAnnotation, true
}

func isZeroEventAnnotation(annotation eventAnnotation) bool {
	return strings.TrimSpace(annotation.ActorID) == "" &&
		strings.TrimSpace(annotation.ActorLabel) == "" &&
		strings.TrimSpace(annotation.QuoteText) == ""
}
