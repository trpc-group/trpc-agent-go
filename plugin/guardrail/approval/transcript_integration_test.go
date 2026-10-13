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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const currentAssistantText = "I will record only the requested value."

type transcriptModel struct{}

func (transcriptModel) Info() model.Info { return model.Info{Name: "transcript-model"} }
func (transcriptModel) GenerateContent(_ context.Context, req *model.Request) (<-chan *model.Response, error) {
	msg := model.NewAssistantMessage("Finished.")
	if req.Messages[len(req.Messages)-1].Role != model.RoleTool {
		msg.Content = currentAssistantText
		for _, id := range []string{"first", "second"} {
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{ID: id, Type: "function",
				Function: model.FunctionDefinitionParam{Name: "record", Arguments: []byte(`{"value":"requested"}`)}})
		}
	}
	out := make(chan *model.Response, 2)
	out <- &model.Response{IsPartial: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("I will")}}}
	out <- &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}
	close(out)
	return out, nil
}

type transcriptTool struct{ calls atomic.Int32 }

func (*transcriptTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "record", Description: "Record a test value.", InputSchema: &tool.Schema{Type: "object"}}
}
func (t *transcriptTool) Call(context.Context, []byte) (any, error) {
	t.calls.Add(1)
	return "recorded", nil
}

type transcriptReviewer struct {
	approved bool
	reviewed chan struct{}
	once     sync.Once
	mu       sync.Mutex
	requests []*review.Request
}

func (r *transcriptReviewer) Review(_ context.Context, req *review.Request) (*review.Decision, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	r.once.Do(func() { close(r.reviewed) })
	return &review.Decision{Approved: r.approved, RiskLevel: "low", Reason: "test decision"}, nil
}

// delayedTranscriptAgent reproduces a scheduling gap between agent event
// channels and the runner. Ordinary tool-call events remain upstream until
// review. Events requiring completion are forwarded so their producer can
// receive the runner's persistence acknowledgement.
type delayedTranscriptAgent struct {
	agent.Agent
	reviewed <-chan struct{}
}

func (a delayedTranscriptAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	source, err := a.Agent.Run(ctx, inv)
	if err != nil {
		return nil, err
	}
	out := make(chan *event.Event, cap(source))
	go func() {
		defer close(out)
		for evt := range source {
			if evt.IsToolCallResponse() && !evt.IsPartial && !evt.RequiresCompletion {
				select {
				case <-a.reviewed:
				case <-ctx.Done():
					return
				}
			}
			if err := event.EmitEvent(ctx, out, evt); err != nil {
				return
			}
		}
	}()
	return out, nil
}

type firstCandidate struct{}

func (firstCandidate) Select(_ context.Context, req *runner.CandidateSelectRequest) (int, error) {
	return req.Attempts[0].Index, nil
}

func TestBeforeToolReviewerIncludesCurrentAssistantAcrossForwarders(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		for _, candidate := range []bool{false, true} {
			for _, approved := range []bool{false, true} {
				t.Run(fmt.Sprintf("parallel=%v/candidate=%v/approved=%v", parallel, candidate, approved), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					reviewer := &transcriptReviewer{approved: approved, reviewed: make(chan struct{})}
					gate, err := approval.New(approval.WithReviewer(reviewer))
					require.NoError(t, err)
					guard, err := guardrail.New(guardrail.WithApproval(gate))
					require.NoError(t, err)
					leaf := &transcriptTool{}
					inner := llmagent.New("main", llmagent.WithModel(transcriptModel{}),
						llmagent.WithTools([]tool.Tool{leaf}), llmagent.WithEnableParallelTools(parallel), llmagent.WithMaxLLMCalls(2))
					opts := []runner.Option{runner.WithPlugins(guard)}
					wantReviews := 2
					if candidate {
						opts = append(opts, runner.WithCandidateSelector(firstCandidate{}, runner.WithCandidateAttempts(2)))
						wantReviews = 4
					}
					r := runner.NewRunner("review-transcript", delayedTranscriptAgent{Agent: inner, reviewed: reviewer.reviewed}, opts...)
					defer r.Close()
					events, err := r.Run(ctx, "user", "session", model.NewUserMessage("Record the requested value."))
					require.NoError(t, err)
					for evt := range events {
						require.Nil(t, evt.Error)
					}
					require.NoError(t, ctx.Err())
					wantCalls := int32(0)
					if approved {
						wantCalls = int32(wantReviews)
					}
					require.Equal(t, wantCalls, leaf.calls.Load())
					reviewer.mu.Lock()
					defer reviewer.mu.Unlock()
					require.Len(t, reviewer.requests, wantReviews)
					for _, req := range reviewer.requests {
						require.JSONEq(t, `{"value":"requested"}`, string(req.Action.Arguments))
						var assistantText, toolCalls int
						for _, entry := range req.Transcript {
							if entry.Role != model.RoleAssistant {
								continue
							}
							if entry.Content == currentAssistantText {
								assistantText++
							}
							if strings.HasPrefix(entry.Content, "tool record call:") {
								require.Contains(t, entry.Content, `"requested"`)
								toolCalls++
							}
						}
						require.Equal(t, 1, assistantText, "review must include the current assistant message exactly once")
						require.Equal(t, 2, toolCalls, "review must include both current tool calls exactly once")
					}
				})
			}
		}
	}
}

func TestBeforeToolReviewerGraphPaths(t *testing.T) {
	for _, agentNode := range []bool{false, true} {
		t.Run(fmt.Sprintf("agentNode=%v", agentNode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			reviewer := &transcriptReviewer{approved: true, reviewed: make(chan struct{})}
			gate, err := approval.New(approval.WithReviewer(reviewer))
			require.NoError(t, err)
			leaf := &transcriptTool{}
			sg := graph.NewStateGraph(graph.MessagesStateSchema())
			var opts []graphagent.Option
			if agentNode {
				child := llmagent.New("child", llmagent.WithModel(transcriptModel{}),
					llmagent.WithTools([]tool.Tool{leaf}), llmagent.WithMaxLLMCalls(2))
				sg.AddAgentNode("child").SetEntryPoint("child").SetFinishPoint("child")
				opts = append(opts, graphagent.WithSubAgents([]agent.Agent{child}))
			} else {
				tools := map[string]tool.Tool{"record": leaf}
				sg.AddLLMNode("llm", transcriptModel{}, "", tools).
					AddToolsNode("tools", tools).AddEdge("llm", "tools").
					SetEntryPoint("llm").SetFinishPoint("tools")
			}
			g, err := sg.Compile()
			require.NoError(t, err)
			ag, err := graphagent.New("graph", g, opts...)
			require.NoError(t, err)
			r := runner.NewRunner("graph-review", ag, runner.WithPlugins(gate))
			defer r.Close()
			// Native LLM nodes only persist final responses when requested.
			// Their existing node barrier then orders persistence before tools.
			events, err := r.Run(ctx, "user", "session", model.NewUserMessage("Record the requested value."),
				agent.WithGraphEmitFinalModelResponses(true))
			require.NoError(t, err)
			for evt := range events {
				require.Nil(t, evt.Error)
			}
			require.NoError(t, ctx.Err())
			require.Equal(t, int32(2), leaf.calls.Load())
			reviewer.mu.Lock()
			defer reviewer.mu.Unlock()
			require.Len(t, reviewer.requests, 2)
			for _, req := range reviewer.requests {
				var assistantText, calls int
				for _, entry := range req.Transcript {
					if entry.Role == model.RoleAssistant && entry.Content == currentAssistantText {
						assistantText++
					}
					if strings.HasPrefix(entry.Content, "tool record call:") {
						calls++
					}
				}
				require.Equal(t, 1, assistantText)
				require.Equal(t, 2, calls)
			}
		})
	}
}
