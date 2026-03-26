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
	HistoryMode string `json:"history_mode,omitempty"`
	ActorID     string `json:"actor_id,omitempty"`
	ActorLabel  string `json:"actor_label,omitempty"`
	QuoteText   string `json:"quote_text,omitempty"`
}

// MergeRequestExtension stores conversation metadata in request
// extensions.
func MergeRequestExtension(
	extensions map[string]json.RawMessage,
	annotation Annotation,
) (map[string]json.RawMessage, error) {
	if isZeroAnnotation(annotation) {
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
	if !ok || isZeroAnnotation(annotation) {
		return Annotation{}, false
	}
	return annotation, true
}

// RuntimeState returns runtime state for one run.
func RuntimeState(annotation Annotation) map[string]any {
	if isZeroAnnotation(annotation) {
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
	if isZeroAnnotation(annotation) {
		return nil
	}
	return event.SetExtension(
		evt,
		ExtensionKey,
		eventAnnotation{
			ActorID:    annotation.ActorID,
			ActorLabel: annotation.ActorLabel,
			QuoteText:  annotation.QuoteText,
		},
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
	decoded.HistoryMode = strings.TrimSpace(decoded.HistoryMode)
	decoded.ActorID = strings.TrimSpace(decoded.ActorID)
	decoded.ActorLabel = strings.TrimSpace(decoded.ActorLabel)
	decoded.QuoteText = strings.TrimSpace(decoded.QuoteText)
	if isZeroAnnotation(decoded) {
		return Annotation{}, false, nil
	}
	return decoded, true, nil
}

func isZeroAnnotation(annotation Annotation) bool {
	return strings.TrimSpace(annotation.HistoryMode) == "" &&
		strings.TrimSpace(annotation.ActorID) == "" &&
		strings.TrimSpace(annotation.ActorLabel) == "" &&
		strings.TrimSpace(annotation.QuoteText) == ""
}
