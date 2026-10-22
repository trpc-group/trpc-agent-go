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
	"fmt"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/messageorigin"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type replaceUserEventPlugin struct{}

func (replaceUserEventPlugin) Name() string { return "replace-user-event" }
func (replaceUserEventPlugin) Register(r *plugin.Registry) {
	r.OnEvent(func(_ context.Context, _ *agent.Invocation, e *event.Event) (*event.Event, error) {
		if !e.IsUserMessage() {
			return e, nil
		}
		// Preserve event lifecycle metadata while reconstructing the payload.
		updated := *e
		updated.Response = e.Response.Clone()
		updated.Extensions = nil
		return &updated, nil
	})
}

func TestPermissionReviewPreservesInputProvenance(t *testing.T) {
	for _, delegated := range []bool{false, true} {
		for _, seeded := range []bool{false, true} {
			for _, replace := range []bool{false, true} {
				t.Run(fmt.Sprintf("delegated=%v/seeded=%v/replace=%v", delegated, seeded, replace), func(t *testing.T) {
					const prompt = "Record a value."
					seen := false
					seenHistory := false
					wantOrigin := review.OriginUserInput
					if delegated {
						wantOrigin = review.OriginAgent
					}
					gate, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
						for i, entry := range req.Transcript {
							if entry.Content == "Imported user history." {
								seenHistory = true
								if got := req.Context.TranscriptInfo[i].Origin; got != review.OriginUnknown {
									t.Errorf("imported history provenance = %q, want unknown", got)
								}
							}
							if entry.Content == prompt {
								seen = true
								got := req.Context.TranscriptInfo[i].Origin
								if got != wantOrigin {
									t.Errorf("current input provenance = %q, want %q", got, wantOrigin)
								}
							}
						}
						return &review.Decision{Approved: true}, nil
					})))
					if err != nil {
						t.Fatal(err)
					}
					leaf := &checkedTool{check: func(*tool.PermissionRequest) (tool.PermissionDecision, error) { return tool.AllowPermission(), nil }, call: func([]byte) {}}
					ag := llmagent.New("main", llmagent.WithModel(callModel{"record", `{}`}), llmagent.WithTools([]tool.Tool{leaf}), llmagent.WithMaxLLMCalls(3))
					var opts []runner.Option
					if replace {
						opts = append(opts, runner.WithPlugins(replaceUserEventPlugin{}))
					}
					r := runner.NewRunner("input-provenance", ag, opts...)
					defer r.Close()
					runOpts := []agent.RunOption{agent.WithToolPermissionPolicy(gate.PermissionPolicy(nil))}
					if seeded {
						runOpts = append(runOpts, agent.WithMessages([]model.Message{model.NewUserMessage("Imported user history."), model.NewUserMessage(prompt)}))
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if delegated {
						ctx = messageorigin.WithAgentInput(ctx)
					}
					events, err := r.Run(ctx, "user", "session", model.NewUserMessage(prompt), runOpts...)
					if err != nil {
						t.Fatal(err)
					}
					for range events {
					}
					if seeded && !seenHistory {
						t.Error("imported history missing")
					}
					if !seen {
						t.Error("host input missing")
					}
					if err := ctx.Err(); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
