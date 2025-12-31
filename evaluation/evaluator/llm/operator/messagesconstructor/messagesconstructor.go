//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package messagesconstructor builds judge prompts from invocation context.
package messagesconstructor

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MessagesConstructor defines the interface for building judge prompts.
type MessagesConstructor interface {
	// ConstructMessages builds prompts for the judge model.
	ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
		evalMetric *metric.EvalMetric) ([]model.Message, error)
}
