//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/hedge"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

func newHedgeModel(config appConfig) (model.Model, error) {
	primary := openai.New(
		config.primaryModelName,
		openai.WithBaseURL(config.primaryBaseURL),
	)
	backup := openai.New(
		config.backupModelName,
		openai.WithBaseURL(config.backupBaseURL),
	)
	return hedge.New(
		hedge.WithName("hedge-chat-model"),
		hedge.WithCandidates(primary, backup),
		hedge.WithDelay(config.hedgeDelay),
	)
}
