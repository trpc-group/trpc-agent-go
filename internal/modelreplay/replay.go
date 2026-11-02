//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package modelreplay checks native replay compatibility for routing models
// and for the messages exchanged with them.
package modelreplay

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// CandidateInfo checks that a routing wrapper can report one stable
// native producer identity. Heterogeneous legacy candidates remain supported.
func CandidateInfo(candidates []model.Model) (model.Info, error) {
	if len(candidates) == 0 {
		return model.Info{}, fmt.Errorf("native replay requires at least one candidate")
	}
	primary := candidates[0].Info()
	hasNative := false
	for _, candidate := range candidates {
		hasNative = hasNative || candidate.Info().ReplayProtocol != ""
	}
	if hasNative {
		for _, candidate := range candidates {
			info := candidate.Info()
			if info.Name != primary.Name || info.ReplayProtocol != primary.ReplayProtocol {
				return model.Info{}, fmt.Errorf("native replay requires candidates with the same protocol and configured model name")
			}
		}
	}
	return primary, nil
}

// MessagesHaveReplay reports whether any message carries native replay data.
func MessagesHaveReplay(messages []model.Message) bool {
	for _, message := range messages {
		if message.Replay != nil {
			return true
		}
	}
	return false
}

// ResponseHasReplay reports whether any choice of response carries native
// replay data.
func ResponseHasReplay(response *model.Response) bool {
	if response == nil {
		return false
	}
	for _, choice := range response.Choices {
		if choice.Message.Replay != nil || choice.Delta.Replay != nil {
			return true
		}
	}
	return false
}

// ValidateMessages checks every native record in messages against the
// producer or consumer identity in info. Messages without Replay are ignored,
// so legacy histories never fail this check.
func ValidateMessages(messages []model.Message, info model.Info) error {
	for index, message := range messages {
		if message.Replay == nil {
			continue
		}
		if _, err := message.ReplayDataFor(info.ReplayProtocol, info.Name); err != nil {
			return fmt.Errorf("native replay message %d: %w", index, err)
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
		if choice.Message.Replay == nil && choice.Delta.Replay == nil {
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
