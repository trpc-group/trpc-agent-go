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
	"reflect"

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

// ResponseGuard holds the native records of a model response as the model
// produced them, before callbacks and repairs can edit or replace the
// response. The zero value guards a response without native records.
type ResponseGuard struct {
	records     []guardedRecord
	choiceCount int
}

type guardedRecord struct {
	choice int
	index  int
	delta  bool
	data   model.ReplayData
}

// GuardResponse captures the native records of response. Only the records are
// copied: callbacks edit messages in place, so the message projections are
// checked against the captured records later instead of being captured.
func GuardResponse(response *model.Response) ResponseGuard {
	var guard ResponseGuard
	if response == nil {
		return guard
	}
	for i, choice := range response.Choices {
		if choice.Message.Replay != nil {
			guard.records = append(guard.records, guardedRecord{
				choice: i,
				index:  choice.Index,
				data:   *choice.Message.Replay,
			})
		}
		if choice.Delta.Replay != nil {
			guard.records = append(guard.records, guardedRecord{
				choice: i,
				index:  choice.Index,
				delta:  true,
				data:   *choice.Delta.Replay,
			})
		}
	}
	if len(guard.records) > 0 {
		guard.choiceCount = len(response.Choices)
	}
	return guard
}

// Active reports whether response needs native validation: the model produced
// native records, or the final response carries any.
func (g ResponseGuard) Active(response *model.Response) bool {
	return len(g.records) > 0 || ResponseHasReplay(response)
}

// Validate checks the final response before it is published or its tool calls
// are executed. Every captured record must still be attached, unchanged, to the
// message or delta of the same choice position and index, and every native
// record in response must belong to producer and match its projection. A
// removed, replaced, rebound, or moved native record is rejected, so callbacks
// cannot change native tool calls or discard opaque output. A response with
// captured native records also retains its original number of choices.
func (g ResponseGuard) Validate(response *model.Response, producer model.Info) error {
	for _, record := range g.records {
		if !record.attached(response) {
			location := "message"
			if record.delta {
				location = "delta"
			}
			return fmt.Errorf(
				"invalid model response: native %s record of choice %d was removed, replaced, or moved",
				location,
				record.choice,
			)
		}
	}
	if len(g.records) > 0 && len(response.Choices) != g.choiceCount {
		return fmt.Errorf("invalid model response: native response choices were added or removed")
	}
	return ValidateResponse(response, producer)
}

func (r guardedRecord) attached(response *model.Response) bool {
	if response == nil || r.choice >= len(response.Choices) {
		return false
	}
	choice := response.Choices[r.choice]
	replay := choice.Message.Replay
	if r.delta {
		replay = choice.Delta.Replay
	}
	return choice.Index == r.index && replay != nil && reflect.DeepEqual(*replay, r.data)
}

// ValidateResponse checks the native records of a model response before its
// messages are published or its tool calls are executed. Callbacks and repairs
// may not change the projection of a native record.
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
