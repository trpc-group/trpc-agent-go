//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package providerdata checks provider data compatibility and isolates mutable
// message fields at callback and tool-execution boundaries.
package providerdata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CandidateInfo checks that a routing wrapper can report one stable
// native producer identity. Heterogeneous legacy candidates remain supported.
func CandidateInfo(candidates []model.Model) (model.Info, error) {
	if len(candidates) == 0 {
		return model.Info{}, fmt.Errorf("provider data requires at least one candidate")
	}
	primary := candidates[0].Info()
	hasNative := false
	for _, candidate := range candidates {
		hasNative = hasNative || candidate.Info().ProviderDataProtocol != ""
	}
	if hasNative {
		for _, candidate := range candidates {
			info := candidate.Info()
			if info.Name != primary.Name || info.ProviderDataProtocol != primary.ProviderDataProtocol {
				return model.Info{}, fmt.Errorf("provider data requires candidates with the same protocol and configured model name")
			}
		}
	}
	return primary, nil
}

// MessagesHaveData reports whether any message carries provider data.
func MessagesHaveData(messages []model.Message) bool {
	for _, message := range messages {
		if message.ProviderData != nil {
			return true
		}
	}
	return false
}

// ResponseHasData reports whether any choice of response carries native
// provider data.
func ResponseHasData(response *model.Response) bool {
	if response == nil {
		return false
	}
	for _, choice := range response.Choices {
		if choice.Message.ProviderData != nil || choice.Delta.ProviderData != nil {
			return true
		}
	}
	return false
}

// ResponseRequestsActions reports whether response is a completed native
// response that asks the caller to complete provider actions before the next
// model step, as described by model.ProviderActionCompleter.
func ResponseRequestsActions(response *model.Response) bool {
	return response != nil && !response.IsPartial && !response.Done &&
		response.Error == nil && ResponseHasData(response)
}

// ValidateMessage checks a native record before the selected model consumes it.
// Ordinary messages carry no record and pass through unchanged. A record is
// valid on an assistant message or on a tool message that names the native
// action it answers.
func ValidateMessage(message model.Message, info model.Info) error {
	data := message.ProviderData
	if data == nil {
		return nil
	}
	switch message.Role {
	case model.RoleAssistant:
	case model.RoleTool:
		if strings.TrimSpace(message.ToolID) == "" {
			return fmt.Errorf("provider data on a tool message requires a tool id")
		}
	default:
		return fmt.Errorf("provider data is only valid on assistant messages and tool messages")
	}
	if strings.TrimSpace(data.Protocol) == "" || strings.TrimSpace(data.Model) == "" {
		return fmt.Errorf("provider data requires a protocol and model identity")
	}
	if data.Protocol != info.ProviderDataProtocol || data.Model != info.Name {
		return fmt.Errorf("provider data belongs to protocol %q and model %q, not %q and %q",
			data.Protocol, data.Model, info.ProviderDataProtocol, info.Name)
	}
	payload := bytes.TrimSpace(data.Payload)
	if len(payload) == 0 || (payload[0] != '{' && payload[0] != '[') || !json.Valid(payload) {
		return fmt.Errorf("provider payload must be a valid JSON object or array")
	}
	return nil
}

// ValidateMessages checks every native record in messages against the
// producer or consumer identity in info. Messages without ProviderData are ignored,
// so legacy histories never fail this check.
func ValidateMessages(messages []model.Message, info model.Info) error {
	for index, message := range messages {
		if err := ValidateMessage(message, info); err != nil {
			return fmt.Errorf("provider data message %d: %w", index, err)
		}
	}
	return nil
}

// ValidateResponse checks the native records that remain in a model response
// before its messages are published or its tool calls are executed. Callbacks
// may edit message projections or replace responses; each final native record
// must match the configured producer identity and contain a valid payload.
func ValidateResponse(response *model.Response, producer model.Info) error {
	if response == nil {
		return nil
	}
	for _, choice := range response.Choices {
		if choice.Message.ProviderData == nil && choice.Delta.ProviderData == nil {
			continue
		}
		if err := ValidateMessages(
			[]model.Message{choice.Message, choice.Delta},
			producer,
		); err != nil {
			return fmt.Errorf("invalid model response: %w", err)
		}
	}
	return nil
}
