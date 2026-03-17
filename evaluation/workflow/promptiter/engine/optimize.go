//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
)

// optimize calls optimizer components to create patch candidates for this round.
func (e *engine) optimize(ctx context.Context) error {
	req := &optimizer.Request{}
	rsp, err := e.optimizer.Optimize(ctx, req)
	if err != nil {
		return err
	}
	_ = rsp
	return nil
}
