//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package adapter provides the adapter for the AG-UI SDK.
package adapter

import (
	"encoding/json"
	"io"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// RunAgentInput represents the parameters for an AG-UI run request.
type RunAgentInput struct {
	ThreadID       string          `json:"threadId"`
	RunID          string          `json:"runId"`
	Messages       []model.Message `json:"messages"`
	State          map[string]any  `json:"state"`
	ForwardedProps map[string]any  `json:"forwardedProps"`
}

// RunAgentInputFromReader parses an AG-UI run request payload from a reader.
func RunAgentInputFromReader(r io.Reader) (*RunAgentInput, error) {
	var input RunAgentInput
	dec := json.NewDecoder(r)
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	return &input, nil
}
