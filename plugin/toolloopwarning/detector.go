//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

type detector struct{}

type detectorState struct {
	Previous string
	Warned   bool
	Pending  bool
}

type roundFingerprint struct {
	ToolCalls []callFingerprint `json:"tool_calls"`
}

type callFingerprint struct {
	ToolName  string        `json:"tool_name"`
	Arguments string        `json:"arguments"`
	Result    model.Message `json:"result"`
}

func (detector) observe(
	state detectorState,
	toolCallResponse *model.Response,
	toolResultMessages []model.Message,
	complete bool,
) detectorState {
	if !complete {
		return detectorState{}
	}
	fingerprint, ok := fingerprintRound(toolCallResponse, toolResultMessages)
	if !ok {
		return detectorState{}
	}
	if fingerprint != state.Previous {
		return detectorState{Previous: fingerprint}
	}
	if !state.Warned {
		state.Pending = true
		state.Warned = true
	}
	return state
}

func fingerprintRound(
	toolCallResponse *model.Response,
	toolResultMessages []model.Message,
) (string, bool) {
	if toolCallResponse == nil || len(toolCallResponse.Choices) == 0 {
		return "", false
	}
	toolCalls := toolCallResponse.Choices[0].Message.ToolCalls
	if len(toolCalls) == 0 || len(toolCalls) != len(toolResultMessages) {
		return "", false
	}
	round := roundFingerprint{
		ToolCalls: make([]callFingerprint, 0, len(toolCalls)),
	}
	for i, toolCall := range toolCalls {
		result := toolResultMessages[i]
		result.ToolID = ""
		round.ToolCalls = append(round.ToolCalls, callFingerprint{
			ToolName:  toolCall.Function.Name,
			Arguments: canonicalArguments(toolCall.Function.Arguments),
			Result:    result,
		})
	}
	encoded, err := json.Marshal(round)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func canonicalArguments(arguments []byte) string {
	trimmed := bytes.TrimSpace(arguments)
	if len(trimmed) == 0 {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return string(trimmed)
		}
		if canonical, err := json.Marshal(value); err == nil {
			return string(canonical)
		}
	}
	return string(trimmed)
}
