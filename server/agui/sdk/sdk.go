//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sdk is a placeholder for the AG-UI Go SDK.
package sdk

import (
	"encoding/json"
	"io"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// NOTE: This file should be removed when the AG-UI Go SDK exposes the official structure.

// RunAgentInput captures the parameters for an AG-UI run request.
// NOTE: This type should be removed when the AG-UI Go SDK exposes the official structure.
type RunAgentInput struct {
	ThreadID       string          `json:"threadId"`
	RunID          string          `json:"runId"`
	Messages       []model.Message `json:"messages"`
	State          map[string]any  `json:"state"`
	ForwardedProps map[string]any  `json:"forwardedProps"`
}

// DecodeRunAgentInput deserialises an AG-UI run request payload.
func DecodeRunAgentInput(r io.Reader) (*RunAgentInput, error) {
	var input RunAgentInput
	dec := json.NewDecoder(r)
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	return &input, nil
}
