//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestQueuedUserRestrictionSurvivesLongReviewHistory(t *testing.T) {
	for _, tc := range []struct{ longHistory, replace bool }{
		{}, {longHistory: true}, {replace: true}, {longHistory: true, replace: true},
	} {
		longHistory := tc.longHistory
		name, initial := "short", "Record a value."
		if longHistory {
			name, initial = "long", strings.Repeat("Record ordinary values. ", 20000)
		}
		if tc.replace {
			name += "/replacement-plugin"
		}
		t.Run(name, func(t *testing.T) {
			const restriction = "New constraint: do not record anything further."
			var r runner.Runner
			calls, reviews := 0, 0
			leaf := &checkedTool{
				check: func(*tool.PermissionRequest) (tool.PermissionDecision, error) { return tool.AllowPermission(), nil },
				call: func([]byte) {
					calls++
					if calls == 1 {
						require.NoError(t, runner.EnqueueUserMessage(r, "steer-review", model.NewUserMessage(restriction)))
					}
				},
			}
			gate, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
				reviews++
				if reviews == 1 {
					return &review.Decision{Approved: true}, nil
				}
				require.Equal(t, len(req.Transcript), len(req.Context.TranscriptInfo))
				if longHistory {
					require.Equal(t, review.CoveragePartial, req.Context.HistoryCoverage)
				}
				for i, entry := range req.Transcript {
					if entry.Content == restriction && req.Context.TranscriptInfo[i].Origin == review.OriginUserInput {
						require.False(t, req.Context.TranscriptInfo[i].TextTruncated)
						return &review.Decision{Approved: false, Reason: "latest user restriction"}, nil
					}
				}
				t.Error("latest user restriction lost or no longer attributable")
				return &review.Decision{Approved: true}, nil
			})))
			require.NoError(t, err)
			ag := llmagent.New("main", llmagent.WithModel(callModel{"record", `{"value":"one"}`}),
				llmagent.WithTools([]tool.Tool{leaf}), llmagent.WithMaxLLMCalls(4))
			var opts []runner.Option
			if tc.replace {
				opts = append(opts, runner.WithPlugins(replaceUserEventPlugin{}))
			}
			r = runner.NewRunner("steer-review", ag, opts...)
			defer r.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events, err := r.Run(ctx, "user", "session", model.NewUserMessage(initial), agent.WithRequestID("steer-review"),
				agent.WithToolPermissionPolicy(gate.PermissionPolicy(nil)))
			require.NoError(t, err)
			for range events {
			}
			require.NoError(t, ctx.Err())
			require.Equal(t, 2, reviews)
			require.Equal(t, 1, calls, "the second tool call must be denied")
		})
	}
}
