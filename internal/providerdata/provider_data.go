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

func validateRecord(message model.Message) error {
	data := message.ProviderData
	if data == nil {
		return nil
	}
	if message.Role != model.RoleAssistant {
		return fmt.Errorf("provider data is only valid on assistant messages")
	}
	if strings.TrimSpace(data.Protocol) == "" || strings.TrimSpace(data.Model) == "" {
		return fmt.Errorf("provider data requires a protocol and model identity")
	}
	payload := bytes.TrimSpace(data.Payload)
	if len(payload) == 0 || (payload[0] != '{' && payload[0] != '[') || !json.Valid(payload) {
		return fmt.Errorf("provider payload must be a valid JSON object or array")
	}
	return nil
}

// ValidateMessage checks a record against its exact producer identity.
func ValidateMessage(message model.Message, info model.Info) error {
	if err := validateRecord(message); err != nil || message.ProviderData == nil {
		return err
	}
	data := message.ProviderData
	if data.Protocol != info.ProviderDataProtocol || data.Model != info.Name {
		return fmt.Errorf("provider data belongs to protocol %q and model %q, not %q and %q",
			data.Protocol, data.Model, info.ProviderDataProtocol, info.Name)
	}
	return nil
}

// ValidateHistory checks incoming history against the selected adapter's
// replay capability. Producer validation and tool recovery use ValidateResponse.
func ValidateHistory(messages []model.Message, target model.Model) error {
	consumer, ok := target.(model.ProviderHistoryConsumer)
	if !ok {
		return ValidateMessages(messages, target.Info())
	}
	info := target.Info()
	for i, msg := range messages {
		if msg.ProviderData == nil {
			continue
		}
		var err error
		if consumer.SupportsProviderHistory(msg.ProviderData.Protocol) {
			err = validateRecord(msg)
		} else {
			err = ValidateMessage(msg, info)
		}
		if err != nil {
			return fmt.Errorf("provider data message %d: %w", i, err)
		}
	}
	return nil
}

// CandidatesSupportHistory reports whether every route can replay the protocol.
func CandidatesSupportHistory(candidates []model.Model, protocol string) bool {
	for _, candidate := range candidates {
		consumer, ok := candidate.(model.ProviderHistoryConsumer)
		if !ok || !consumer.SupportsProviderHistory(protocol) {
			return false
		}
	}
	return len(candidates) > 0
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
