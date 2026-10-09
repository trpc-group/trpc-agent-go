//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	coreevaluation "trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/usersimulation"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type scoringSessionKey struct{}

type traceObservation struct {
	session string
	parent  oteltrace.SpanContext
	started oteltrace.SpanContext
	options agent.RunOptions
}

// roleTraceRunner emits a different trace on every turn, including expected
// turns, so accidental overwrites cannot hide behind a shared trace parent.
type roleTraceRunner struct {
	mu           sync.Mutex
	role         byte
	observations []traceObservation
}

func (r *roleTraceRunner) Run(ctx context.Context, _, session string, _ model.Message, options ...agent.RunOption) (<-chan *event.Event, error) {
	opts := agent.NewRunOptions(options...)
	r.mu.Lock()
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: oteltrace.TraceID{r.role, byte(len(r.observations) + 1)}, SpanID: oteltrace.SpanID{1}, TraceFlags: oteltrace.FlagsSampled,
	})
	if r.role == 'j' {
		session, _ = ctx.Value(scoringSessionKey{}).(string)
	}
	r.observations = append(r.observations, traceObservation{session: session, parent: oteltrace.SpanContextFromContext(ctx), started: sc, options: opts})
	r.mu.Unlock()
	for _, callback := range opts.TraceStartedCallbacks {
		callback(oteltrace.SpanContext{}) // Ignore invalid notifications.
		callback(sc)
	}
	output := "hello"
	if r.role == 'j' {
		output = "reasoning: correct\nis_the_agent_response_valid: valid"
	}
	events := make(chan *event.Event, 1)
	events <- makeFinalEvent(output)
	close(events)
	return events, nil
}

func (r *roleTraceRunner) Close() error { return nil }

type twoTurnSimulator struct{}

func (twoTurnSimulator) Start(context.Context, *usersimulation.StartRequest) (usersimulation.Conversation, error) {
	return &twoTurnConversation{}, nil
}

type twoTurnConversation struct{ turns int }

func (c *twoTurnConversation) Next(context.Context, *usersimulation.TurnRequest) (*usersimulation.Decision, error) {
	if c.turns == 2 {
		return &usersimulation.Decision{Stop: true}, nil
	}
	c.turns++
	return &usersimulation.Decision{Message: &model.Message{Role: model.RoleUser, Content: "say hello"}}, nil
}

func (*twoTurnConversation) Close() error { return nil }

// TestBatchTraceRoles checks both inference and scoring against the real
// evaluator, including expected-first execution, repeated runs, and trace mode.
func TestBatchTraceRoles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expected bool
		driver   evalset.ConversationScenarioDriver
		trace    bool
	}{
		{name: "actual multi-turn"},
		{name: "static expected", expected: true},
		{name: "actual driver", expected: true, driver: evalset.ConversationScenarioDriverActual},
		{name: "expected driver", expected: true, driver: evalset.ConversationScenarioDriverExpected},
		{name: "trace mode", expected: true, trace: true},
	} {
		for _, parallel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parallel=%t", tc.name, parallel), func(t *testing.T) {
				ctx := context.Background()
				if parallel {
					// A request parent must not absorb every case's scoring spans.
					ctx = oteltrace.ContextWithRemoteSpanContext(ctx, oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
						TraceID: oteltrace.TraceID{255}, SpanID: oteltrace.SpanID{255}, TraceFlags: oteltrace.FlagsSampled,
					}))
				}
				actual, expected, judge := &roleTraceRunner{role: 'a'}, &roleTraceRunner{role: 'e'}, &roleTraceRunner{role: 'j'}
				callbacks := service.NewCallbacks().RegisterBeforeInferenceCase("user", func(ctx context.Context, args *service.BeforeInferenceCaseArgs) (*service.BeforeInferenceCaseResult, error) {
					args.RunOptions = append(args.RunOptions, agent.WithRequestID(args.EvalCaseID))
					return nil, nil
				}).RegisterBeforeEvaluateCase("user", func(ctx context.Context, args *service.BeforeEvaluateCaseArgs) (*service.BeforeEvaluateCaseResult, error) {
					for _, result := range args.Request.InferenceResults {
						if result.EvalCaseID == args.EvalCaseID {
							return &service.BeforeEvaluateCaseResult{Context: context.WithValue(ctx, scoringSessionKey{}, result.SessionID)}, nil
						}
					}
					return nil, fmt.Errorf("missing inference result for %s", args.EvalCaseID)
				})
				evaluator, err := coreevaluation.New("demo-app", actual,
					coreevaluation.WithExpectedRunner(expected), coreevaluation.WithJudgeRunner(judge), coreevaluation.WithJudgeRunnerNumSamples(1),
					coreevaluation.WithUserSimulator(twoTurnSimulator{}), coreevaluation.WithCallbacks(callbacks),
					coreevaluation.WithRunOptions(agent.WithRequestID("shared"), agent.WithTraceStartedCallback(func(oteltrace.SpanContext) {})),
					coreevaluation.WithEvalCaseParallelism(2), coreevaluation.WithEvalCaseParallelInferenceEnabled(parallel),
					coreevaluation.WithEvalCaseParallelEvaluationEnabled(parallel),
					coreevaluation.WithNumRuns(2), coreevaluation.WithNumRunsParallelEnabled(parallel),
				)
				require.NoError(t, err)
				defer evaluator.Close()
				api := &recordedAPIServer{}
				server := httptest.NewServer(api)
				defer server.Close()
				metrics := newTestMetricManager(t)
				require.NoError(t, metrics.Add(ctx, "demo-app", "set", &metric.EvalMetric{
					MetricName: "llm_final_response", Threshold: 1, Criterion: &criterion.Criterion{LLMJudge: &llm.LLMCriterion{}},
				}))
				saved := &recordingResultManager{Manager: newTestEvalResultManager(t)}
				h, err := New("demo-app", evaluator, newTestEvalSetManager(t), metrics, saved, WithBaseURL(server.URL), WithPublicKey("pk"), WithSecretKey("sk"))
				require.NoError(t, err)
				specs := []*CaseSpec{buildTestCaseSpec("one"), buildTestCaseSpec("two")}
				for _, spec := range specs {
					c := spec.EvalCase
					c.SessionInput = &evalset.SessionInput{AppName: "demo-app", UserID: "user"}
					c.ExpectedRunnerEnabled = tc.expected
					c.ContextMessages = []*model.Message{{Role: model.RoleSystem, Content: "seed"}}
					c.Conversation = append(c.Conversation, buildTestCaseSpec("unused").EvalCase.Conversation...)
					if tc.driver != "" {
						c.Conversation = nil
						c.ConversationScenario = &evalset.ConversationScenario{Driver: tc.driver, ConversationPlan: "greet twice", StopSignal: "done"}
					}
					if tc.trace {
						c.EvalMode = evalset.EvalModeTrace
					}
				}
				require.NoError(t, h.syncEvalSet(ctx, "set", specs))
				response, err := h.executeRemoteExperiment(ctx, &remoteExperimentRequest{DatasetID: "set"}, executionOptions{runName: "run"}, specs)
				require.NoError(t, err)
				assert.Equal(t, 1.0, response.AggregateScores["pass_rate"])
				if tc.trace {
					require.Empty(t, actual.observations)
				} else {
					require.Len(t, actual.observations, 8)
				}
				if tc.expected {
					require.Len(t, expected.observations, 8)
				} else {
					require.Empty(t, expected.observations)
				}
				firstActual := make(map[string]oteltrace.SpanContext)
				for _, call := range actual.observations {
					if _, ok := firstActual[call.session]; !ok {
						firstActual[call.session] = call.started
					}
					assert.NotEqual(t, "shared", call.options.RequestID)
					assert.Len(t, call.options.TraceStartedCallbacks, 2)
				}
				for _, call := range expected.observations {
					assert.Equal(t, "shared", call.options.RequestID, "actual-only case options leaked to expected runner")
					assert.Len(t, call.options.TraceStartedCallbacks, 1, "expected runner received Langfuse trace capture")
					require.NotEmpty(t, call.options.InjectedContextMessages)
					assert.Equal(t, "seed", call.options.InjectedContextMessages[0].Content)
				}
				require.Len(t, judge.observations, 8)
				for _, call := range judge.observations {
					assert.NotEmpty(t, call.session, "user scoring context was lost")
					assert.True(t, call.parent.IsValid(), "judge lost case trace")
					if !tc.trace {
						assert.Equal(t, firstActual[call.session].TraceID(), call.parent.TraceID(), "judge is on a different trace")
					}
				}
				for i, summary := range response.Cases {
					var session string
					for _, result := range saved.result.EvalCaseResults {
						if result.EvalID == summary.CaseID {
							session = result.SessionID
							break
						}
					}
					if !tc.trace {
						assert.Equal(t, firstActual[session].TraceID().String(), summary.TraceID, "published trace must be the first actual turn")
					}
					assert.Equal(t, summary.TraceID, api.traceRequests[i].ID)
					assert.Equal(t, summary.TraceID, api.runItemRequest[i].TraceID)
					for _, call := range judge.observations {
						if call.session == session {
							assert.Equal(t, summary.TraceID, call.parent.TraceID().String())
						}
					}
				}
			})
		}
	}
}
