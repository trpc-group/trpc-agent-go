package evaluation

import (
    "context"
    "fmt"

    "trpc.group/trpc-go/trpc-agent-go/agent"
    "trpc.group/trpc-go/trpc-agent-go/event"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
    evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
    "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
    localservice "trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
    "trpc.group/trpc-go/trpc-agent-go/model"
)

// stubRunner produces a single assistant message for every invocation.
type stubRunner struct {
    response string
}

func (r stubRunner) Run(ctx context.Context, userID, sessionID string, msg model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
    ch := make(chan *event.Event, 1)
    ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: r.response}}}}}
    close(ch)
    return ch, nil
}

func ExampleAgentEvaluator() {
    ctx := context.Background()

    mgr := evalsetinmemory.NewManager()
    const setID = "demo-set"
    _, _ = mgr.Create(ctx, setID)

    expected := &evalset.EvalCase{
        EvalID: "case-1",
        Conversation: []evalset.Invocation{{
            InvocationID: "invoke-1",
            UserContent:  &evalset.Content{Role: "user", Parts: []evalset.Part{{Text: "hello"}}},
            FinalResponse: &evalset.Content{Role: "assistant", Parts: []evalset.Part{{Text: "hi there"}}},
        }},
    }
    _ = mgr.AddCase(ctx, setID, expected)

    svc := localservice.New(localservice.WithEvalSetManager(mgr), localservice.WithEvaluatorRegistry(DefaultRegistry()))

    evaluator := NewAgentEvaluator(
        WithEvaluationService(svc),
        WithAgentEvaluatorConfig(AgentEvaluatorConfig{
            AppName:    "demo-app",
            EvalSetID:  setID,
            NumRuns:    1,
            DefaultCriteria: map[string]float64{
                metric.MetricResponseMatchScore: 1.0,
            },
        }),
    )

    result, err := evaluator.Evaluate(ctx, stubRunner{response: "hi there"})
    if err != nil {
        fmt.Println("evaluation error:", err)
        return
    }

    metricSummary := result.MetricResults[metric.MetricResponseMatchScore]
    score := 0.0
    if metricSummary.OverallScore != nil {
        score = *metricSummary.OverallScore
    }

    fmt.Printf("status=%s cases=%d score=%.2f\n", result.OverallStatus.String(), result.TotalCases, score)
    // Output: status=passed cases=1 score=1.00
}

