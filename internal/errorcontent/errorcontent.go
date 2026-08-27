//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package errorcontent contains internal contracts for assistant content
// synthesized to make error events persistable and user-visible.
package errorcontent

import (
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

const (
	syntheticExtensionKey = "trpc_agent.error.synthetic_content"

	// FallbackMessage is the assistant content Runner synthesizes for an error
	// event that otherwise has no persistable content.
	FallbackMessage = "An error occurred during execution. Please contact the service provider."
)

// MarkSynthetic marks an event whose assistant content was synthesized for
// presentation and persistence rather than produced by the model. It owns a
// copy of the extension map before mutating it because callers commonly clone
// events shallowly.
func MarkSynthetic(evt *event.Event) {
	if evt == nil {
		return
	}
	extensions := make(map[string]json.RawMessage, len(evt.Extensions)+1)
	for key, raw := range evt.Extensions {
		extensions[key] = append(json.RawMessage(nil), raw...)
	}
	evt.Extensions = extensions
	_ = event.SetExtension(evt, syntheticExtensionKey, true)
}

// IsSynthetic reports whether an error event carries assistant content that
// was synthesized for presentation and persistence. The fallback comparison
// recognizes events persisted before the extension marker was introduced,
// including placeholders whose original role Runner preserved.
func IsSynthetic(evt *event.Event) bool {
	marked, ok, err := event.GetExtension[bool](evt, syntheticExtensionKey)
	if err != nil {
		return false
	}
	if ok {
		return marked
	}
	if evt == nil || evt.Response == nil || evt.Response.Error == nil ||
		len(evt.Response.Choices) == 0 {
		return false
	}
	message := evt.Response.Choices[0].Message
	return message.Content == FallbackMessage
}
