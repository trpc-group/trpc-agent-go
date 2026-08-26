//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/internal/trpcagentwire"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type session struct {
	UserID    string `json:"userId"`
	SessionID string `json:"sessionId"`
}

type runOptions = trpcagentwire.RunOptions

type latestTurnReplacement = trpcagentwire.LatestTurnReplacement

type runRequest struct {
	Session session       `json:"session"`
	Input   model.Message `json:"input"`
	// Profile must be runtime-normalized and include nodeID and type.
	Profile    *profilecompiler.Profile `json:"profile,omitempty"`
	RunOptions runOptions               `json:"runOptions,omitempty"`
}

type runResponse = trpcagentwire.RunResponse

type structureResponse struct {
	Structure *astructure.Snapshot `json:"structure"`
}
