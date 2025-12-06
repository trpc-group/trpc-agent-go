//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package responsescorer

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type ResponseScorer interface {
	// ScoreBasedOnResponse extracts a score from the judge response.
	ScoreBasedOnResponse(ctx context.Context,
		resp *model.Response,
		evalMetric *metric.EvalMetric) (*evalresult.ScoreResult, error)
}
