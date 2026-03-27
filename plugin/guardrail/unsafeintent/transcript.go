//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package unsafeintent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/internal/currentinput"
	guardtranscript "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/internal/transcript"
	unsafereview "trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/unsafeintent/review"
)

func (p *Plugin) buildReviewRequest(ctx context.Context, messages []model.Message) *unsafereview.Request {
	req := currentinput.Build(ctx, messages, p.tokenCounter, func(entry guardtranscript.Entry) unsafereview.TranscriptEntry {
		return unsafereview.TranscriptEntry{
			Role:    entry.Role,
			Content: entry.Content,
		}
	})
	if req == nil {
		return nil
	}
	return &unsafereview.Request{
		LastUserInput: req.LastUserInput,
		Transcript:    req.Transcript,
	}
}
