//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package delivery

import (
	"encoding/json"
	"strings"
)

const extensionKey = "openclaw:delivery_target:v1"

// Target stores one channel-specific default outbound destination.
type Target struct {
	Channel string `json:"channel,omitempty"`
	Target  string `json:"target,omitempty"`
}

// MergeRequestExtension stores delivery metadata in request extensions.
func MergeRequestExtension(
	extensions map[string]json.RawMessage,
	target Target,
) (map[string]json.RawMessage, error) {
	target = sanitizeTarget(target)
	if isZeroTarget(target) {
		return extensions, nil
	}

	raw, err := json.Marshal(target)
	if err != nil {
		return nil, err
	}
	if extensions == nil {
		extensions = make(map[string]json.RawMessage)
	}

	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	extensions[extensionKey] = json.RawMessage(cloned)
	return extensions, nil
}

// TargetFromRequestExtensions decodes request delivery metadata.
func TargetFromRequestExtensions(
	extensions map[string]json.RawMessage,
) (Target, bool, error) {
	if len(extensions) == 0 {
		return Target{}, false, nil
	}

	raw, ok := extensions[extensionKey]
	if !ok || len(raw) == 0 {
		return Target{}, false, nil
	}

	var decoded Target
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Target{}, false, err
	}
	decoded = sanitizeTarget(decoded)
	if isZeroTarget(decoded) {
		return Target{}, false, nil
	}
	return decoded, true, nil
}

func sanitizeTarget(target Target) Target {
	return Target{
		Channel: strings.TrimSpace(target.Channel),
		Target:  strings.TrimSpace(target.Target),
	}
}

func isZeroTarget(target Target) bool {
	return target.Channel == "" || target.Target == ""
}
